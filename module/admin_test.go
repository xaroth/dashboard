package module_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/module"
)

// pager is a module with an admin page. The page writes to the module's
// table and reads the shared greeting, so it shows it has the writer and
// the module's shared values.
type pager struct{}

type pagerConfig struct {
	Fail bool `yaml:"fail"`
}

func (p *pager) Initialize(_ context.Context, cfg *pagerConfig) error {
	if cfg.Fail {
		return errBadInit
	}
	return nil
}
func (*pager) Enabled(context.Context) bool { return false }
func (*pager) Update(ctx context.Context) error {
	return module.SaveSnapshot(ctx, "saved by update")
}

// ServeHTTP shows the page has the module's own context: the writer, its
// shared values, a logger, its snapshot to read, but no snapshot to save
// or next update to set, which belong to Update.
func (*pager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	module.Logger(ctx).Info("page")
	_, err := module.Database(ctx).ExecContext(ctx, "INSERT INTO test_items DEFAULT VALUES")
	var snap string
	_, lerr := module.LoadSnapshot(ctx, &snap)
	fmt.Fprintf(w, "%s %s %v %q %v | %v | %v", r.URL.Path, greetingShared.Get(ctx).Text, err, snap, lerr,
		module.SaveSnapshot(ctx, "x"), module.NextUpdate(ctx, time.Minute))
}

func pagerRegistration(name string, opts ...module.Option) module.Registration {
	opts = append(opts, module.WithShared(greetingShared))
	return module.Define[pager, pagerConfig](name, func() *pagerConfig { return &pagerConfig{} }, opts...)
}

// ticker is a scheduled shared value with a page: an instance like a
// module, served once, as a shared value.
type ticker struct{}

func (ticker) Update(context.Context) error { return nil }

func (ticker) ServeHTTP(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ticker ", r.URL.Path) }

var tickerShared = module.DefineShared("ticker", func() *struct{} { return &struct{}{} },
	func(context.Context, *struct{}, []struct{}) (ticker, error) { return ticker{}, nil },
	module.SharedSchedule(time.Hour, 2*time.Hour))

func TestAdminPages(t *testing.T) {
	t.Parallel()
	set, err := tryLoad(t, "broken: {fail: true}",
		pagerRegistration("pages", module.WithMigrations(migrations)), pagerRegistration("broken"),
		greeterRegistration("plain", module.WithShared(greetingShared), module.WithShared(tickerShared)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Modules and shared values together, without the failed module or the
	// one that is no handler; a scheduled shared value once.
	if diff := cmp.Diff([]string{"greeting", "pages", "ticker"}, set.AdminPages()); diff != "" {
		t.Errorf("admin pages (-want +got):\n%s", diff)
	}
	// Module statuses link their own page; the ticker's is a shared one.
	st, err := set.Statuses(t.Context())
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	pages := map[string]bool{}
	for _, m := range st {
		pages[m.Name] = m.Page
	}
	if diff := cmp.Diff(map[string]bool{"broken": false, "pages": true, "plain": false, "ticker": false}, pages); diff != "" {
		t.Errorf("status pages (-want +got):\n%s", diff)
	}
	if err := set.Update(t.Context(), "pages"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	mux := http.NewServeMux()
	set.RegisterAdmin(mux)
	testCases := []struct {
		name string
		path string
		code int
		body string
	}{
		{"module", "/pages/x", http.StatusOK, `/x hello <nil> "saved by update" <nil> | snapshots are saved from Update | the next update is set from Update`},
		{"shared value", "/greeting/x", http.StatusOK, "/x hello 0 <nil>"},
		{"scheduled shared value", "/ticker/x", http.StatusOK, "ticker /x"},
		{"failed module", "/broken/x", http.StatusNotFound, "404 page not found\n"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, testCase.path, nil))
			if rec.Code != testCase.code || rec.Body.String() != testCase.body {
				t.Errorf("got %d %q, want %d %q", rec.Code, rec.Body.String(), testCase.code, testCase.body)
			}
		})
	}
}
