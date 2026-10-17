package car

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/xaroth/dashboard/module"
)

// warnDays is how many days before expiry the page warns about a key.
const warnDays = 10

//go:embed page.html
var pageHTML string

var pageTmpl = template.Must(template.New("page").Parse(pageHTML))

var shortName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ServeHTTP serves the admin page under /car/: the keys with their expiry
// and vehicles, and forms to add, replace and delete keys and to name
// vehicles. It calls Pleos only to check a key before saving it, and shares
// nothing with Update but the database.
func (m *car) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/":
		m.page(w, r, http.StatusOK, "")
	case r.Method == http.MethodPost && r.URL.Path == "/keys":
		m.saveKey(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/keys/delete":
		m.deleteKey(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/vehicles/name":
		m.nameVehicle(w, r)
	default:
		http.NotFound(w, r)
	}
}

type keyView struct {
	CarKey
	ExpiresAt time.Time
	DaysLeft  int
	Warn      bool
	Vehicles  []CarVehicle
}

// page renders the page with status code, showing problem if set.
func (m *car) page(w http.ResponseWriter, r *http.Request, code int, problem string) {
	ctx := r.Context()
	q := New(module.Database(ctx))
	keys, err := q.ListKeys(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading keys: %v", err), http.StatusInternalServerError)
		return
	}
	rows, err := q.ListVehicles(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading vehicles: %v", err), http.StatusInternalServerError)
		return
	}
	now := module.Clock(ctx)()
	views := make([]keyView, 0, len(keys))
	for _, k := range keys {
		v := keyView{CarKey: k, ExpiresAt: keyExpiry(k)}
		v.DaysLeft = int(math.Ceil(v.ExpiresAt.Sub(now).Hours() / 24))
		v.Warn = v.DaysLeft <= warnDays
		for _, row := range rows {
			if row.ClientID == k.ClientID {
				v.Vehicles = append(v.Vehicles, row)
			}
		}
		views = append(views, v)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	err = pageTmpl.Execute(w, map[string]any{
		"Keys":    views,
		"Brands":  brands,
		"Today":   now.Format(time.DateOnly),
		"Saved":   r.URL.Query().Get("saved"),
		"Problem": problem,
	})
	if err != nil {
		module.Logger(ctx).Error("rendering car page", "err", err)
	}
}

// saveKey checks a key by listing its vehicles, then stores both. Saving a
// key that exists replaces it, as after reissuing it in Pleos Playground.
func (m *car) saveKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	brand := r.PostFormValue("brand")
	id, secret := strings.TrimSpace(r.PostFormValue("client_id")), strings.TrimSpace(r.PostFormValue("client_secret"))
	now := module.Clock(ctx)()
	issued, err := time.ParseInLocation(time.DateOnly, r.PostFormValue("issued"), now.Location())
	switch {
	case !slices.Contains(brands, brand):
		m.page(w, r, http.StatusBadRequest, fmt.Sprintf("brand must be one of %s", strings.Join(brands, ", ")))
		return
	case id == "" || secret == "":
		m.page(w, r, http.StatusBadRequest, "client ID and client secret are required")
		return
	case err != nil:
		m.page(w, r, http.StatusBadRequest, "issue date must be a date, e.g. "+now.Format(time.DateOnly))
		return
	case issued.After(now):
		m.page(w, r, http.StatusBadRequest, "issue date is in the future")
		return
	}
	c := &pleos{url: m.cfg.APIURL, brand: brand, clientID: id, clientSecret: secret}
	vs, err := fetchListing(ctx, c)
	if err != nil {
		m.page(w, r, http.StatusBadRequest, fmt.Sprintf("checking the key with Pleos: %v", err))
		return
	}
	err = inTx(ctx, func(q *Queries) error {
		if err := storeListing(ctx, q, id, vs); err != nil {
			return err
		}
		return q.SaveKey(ctx, SaveKeyParams{ClientID: id, ClientSecret: secret, Brand: brand, IssuedAt: issued})
	})
	if err != nil {
		m.page(w, r, http.StatusInternalServerError, fmt.Sprintf("saving key: %v", err))
		return
	}
	module.Logger(ctx).Info("key saved", "brand", brand, "client_id", id)
	http.Redirect(w, r, "/car/?saved="+url.QueryEscape(id), http.StatusSeeOther)
}

func (m *car) deleteKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PostFormValue("client_id")
	err := inTx(ctx, func(q *Queries) error {
		if err := q.DeleteKeyVehicles(ctx, id); err != nil {
			return err
		}
		return q.DeleteKey(ctx, id)
	})
	if err != nil {
		m.page(w, r, http.StatusInternalServerError, fmt.Sprintf("deleting key: %v", err))
		return
	}
	http.Redirect(w, r, "/car/", http.StatusSeeOther)
}

// nameVehicle sets or, when empty, clears a vehicle's short name.
func (m *car) nameVehicle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := New(module.Database(ctx))
	vin := r.PostFormValue("vin")
	name := strings.ToLower(strings.TrimSpace(r.PostFormValue("short_name")))
	rows, err := q.ListVehicles(ctx)
	if err != nil {
		m.page(w, r, http.StatusInternalServerError, fmt.Sprintf("reading vehicles: %v", err))
		return
	}
	switch {
	case name != "" && !shortName.MatchString(name):
		m.page(w, r, http.StatusBadRequest, "short name must be 1 to 32 lowercase letters, digits, - or _, starting with a letter or digit")
		return
	case !slices.ContainsFunc(rows, func(v CarVehicle) bool { return v.Vin == vin }):
		m.page(w, r, http.StatusBadRequest, fmt.Sprintf("no vehicle %q", vin))
		return
	case name != "" && slices.ContainsFunc(rows, func(v CarVehicle) bool { return v.Vin != vin && v.ShortName != nil && *v.ShortName == name }):
		m.page(w, r, http.StatusBadRequest, fmt.Sprintf("short name %q is taken", name))
		return
	}
	var p *string
	if name != "" {
		p = &name
	}
	if err := q.SaveShortName(ctx, SaveShortNameParams{ShortName: p, Vin: vin}); err != nil {
		m.page(w, r, http.StatusInternalServerError, fmt.Sprintf("saving short name: %v", err))
		return
	}
	http.Redirect(w, r, "/car/", http.StatusSeeOther)
}

// inTx runs fn in a transaction on the module's database.
func inTx(ctx context.Context, fn func(*Queries) error) error {
	tx, err := module.Database(ctx).BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(New(tx)); err != nil {
		return err
	}
	return tx.Commit()
}
