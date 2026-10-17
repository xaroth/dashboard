package module_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

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
func (*pager) Update(context.Context) error { return nil }

func (*pager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, err := module.Database(ctx).ExecContext(ctx, "INSERT INTO test_items DEFAULT VALUES")
	fmt.Fprintf(w, "%s %s %v", r.URL.Path, greetingShared.Get(ctx).Text, err)
}

func pagerRegistration(name string, opts ...module.Option) module.Registration {
	opts = append(opts, module.WithShared(greetingShared))
	return module.Define[pager, pagerConfig](name, func() *pagerConfig { return &pagerConfig{} }, opts...)
}

func TestAdminPages(t *testing.T) {
	t.Parallel()
	set, err := tryLoad(t, "broken: {fail: true}",
		pagerRegistration("pages", module.WithMigrations(migrations)), pagerRegistration("broken"), greeterRegistration("plain", module.WithShared(greetingShared)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Modules and shared values together, without the failed module or the
	// one that is no handler.
	if diff := cmp.Diff([]string{"greeting", "pages"}, set.AdminPages()); diff != "" {
		t.Errorf("admin pages (-want +got):\n%s", diff)
	}
	mux := http.NewServeMux()
	set.RegisterAdmin(mux)
	testCases := []struct {
		name string
		path string
		code int
		body string
	}{
		{"module", "/pages/x", http.StatusOK, "/x hello <nil>"},
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
