package eveonline_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v3"

	defaults "github.com/eve-online-tools/lib-esi-go"
	"github.com/eve-online-tools/lib-esi-go/esi/getstatus"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/modules/eveonline"
	"github.com/xaroth/dashboard/storage"
)

// probe sends one ESI request per update.
type probe struct{}

type probeConfig struct{}

func (*probe) Initialize(context.Context, *probeConfig) error { return nil }
func (*probe) Enabled(context.Context) bool                   { return false }

func (*probe) Update(ctx context.Context) error {
	_, err := getstatus.Request(ctx, eveonline.ESI.Get(ctx))
	return err
}

var probeModule = module.Define[probe, probeConfig]("probe", func() *probeConfig { return &probeConfig{} },
	module.WithShared(eveonline.ESI))

func load(tb testing.TB, src string, handler http.Handler) *module.Set {
	tb.Helper()
	db, err := storage.Open(tb.Context(), tb.TempDir())
	if err != nil {
		tb.Fatalf("storage: %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	var configs map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(src), &configs); err != nil {
		tb.Fatalf("config: %v", err)
	}
	set, err := module.Load(tb.Context(), module.Options{
		Registrations: []module.Registration{probeModule},
		Configs:       configs,
		Migrator:      db,
		Writer:        db.Writer,
		Reader:        db.Reader,
		Log:           slog.New(slog.DiscardHandler),
		HTTPClient:    eveonline.NewFakeClient(handler),
		Now:           time.Now,
	})
	if err != nil {
		tb.Fatalf("Load: %v", err)
	}
	return set
}

func TestESIRequest(t *testing.T) {
	t.Parallel()
	var got http.Header
	var url string
	set := load(t, "eveonline: {contact: [mailto:me@example.com, https://example.com]}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, url = r.Header, r.URL.String()
		fmt.Fprint(w, `{}`)
	}))
	if err := set.Update(t.Context(), "probe"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if want := "https://esi.evetech.net/status"; url != want {
		t.Errorf("got URL %s, want %s", url, want)
	}
	want := map[string]string{
		"User-Agent":           "dashboard/(devel) (mailto:me@example.com; https://example.com); " + defaults.UserAgent,
		"X-Compatibility-Date": defaults.CompatibilityDate,
		"Accept-Language":      defaults.Language,
		"X-Tenant":             defaults.Tenant,
	}
	gotHeaders := map[string]string{}
	for k := range want {
		gotHeaders[k] = got.Get(k)
	}
	if diff := cmp.Diff(want, gotHeaders); diff != "" {
		t.Errorf("headers (-want +got):\n%s", diff)
	}
}

func TestESIRequiresContact(t *testing.T) {
	t.Parallel()
	set := load(t, "", http.NotFoundHandler())
	st, err := set.Statuses(t.Context())
	if err != nil || len(st) != 1 {
		t.Fatalf("Statuses: %v, %v", st, err)
	}
	if want := "eveonline: contact is required"; st[0].Phase != module.PhaseInitFail || st[0].Error != want {
		t.Errorf("got %s %q, want %s %q", st[0].Phase, st[0].Error, module.PhaseInitFail, want)
	}
}
