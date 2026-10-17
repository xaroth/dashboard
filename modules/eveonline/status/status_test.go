package status_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v3"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/modules/eveonline"
	"github.com/xaroth/dashboard/modules/eveonline/status"
	"github.com/xaroth/dashboard/storage"
)

func load(tb testing.TB, handler http.Handler) *module.Set {
	tb.Helper()
	db, err := storage.Open(tb.Context(), tb.TempDir())
	if err != nil {
		tb.Fatalf("storage: %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	var configs map[string]yaml.Node
	if err := yaml.Unmarshal([]byte("eveonline: {contact: [mailto:me@example.com]}"), &configs); err != nil {
		tb.Fatalf("config: %v", err)
	}
	set, err := module.Load(tb.Context(), module.Options{
		Registrations: []module.Registration{status.Module},
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

func TestServer(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 17, 11, 0, 12, 0, time.UTC)
	testCases := []struct {
		name    string
		handler http.HandlerFunc
		want    *status.Status
		wantErr string
	}{
		{
			name: "online",
			handler: func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"players":23456,"server_version":"3012345","start_time":"2026-10-17T11:00:12Z","vip":false}`)
			},
			want: &status.Status{Online: true, Players: 23456, ServerVersion: "3012345", StartTime: &start},
		},
		{
			name: "unavailable",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, `{"error":"The datasource tranquility is temporarily unavailable"}`, http.StatusServiceUnavailable)
			},
			want: &status.Status{},
		},
		{
			// ESI does not answer /status while the cluster is offline.
			name: "timeout",
			handler: func(w http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
			},
			want: &status.Status{},
		},
		{
			name: "error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
			},
			wantErr: "requesting status: 500 Internal Server Error: boom",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				set := load(t, testCase.handler)
				err := set.Update(t.Context(), "eveonline_status")
				if testCase.wantErr != "" {
					if err == nil || err.Error() != testCase.wantErr {
						t.Errorf("got Update error %v, want %q", err, testCase.wantErr)
					}
					return
				}
				if err != nil {
					t.Fatalf("Update: %v", err)
				}
				res := set.Resolve(t.Context(), "eveonline_status", "server", nil)
				if res.Error != "" || res.Stale {
					t.Errorf("got error %q, stale %v, want fresh data", res.Error, res.Stale)
				}
				if diff := cmp.Diff(testCase.want, res.Value); diff != "" {
					t.Errorf("status (-want +got):\n%s", diff)
				}
			})
		})
	}
}

func TestServerBeforeFirstUpdate(t *testing.T) {
	t.Parallel()
	set := load(t, http.NotFoundHandler())
	res := set.Resolve(t.Context(), "eveonline_status", "server", nil)
	if res.Value.(*status.Status) != nil || !res.Stale {
		t.Errorf("got %v, stale %v, want no status, stale", res.Value, res.Stale)
	}
}
