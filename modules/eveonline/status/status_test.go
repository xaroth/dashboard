package status_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v3"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/module/moduletest"
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
	if err := yaml.Unmarshal([]byte("eveonline_esi: {contact: [mailto:me@example.com]}"), &configs); err != nil {
		tb.Fatalf("config: %v", err)
	}
	set, err := module.Load(tb.Context(), module.Options{
		Registrations: []module.Registration{status.Module},
		Configs:       configs,
		Migrator:      db,
		Writer:        db.Writer,
		Reader:        db.Reader,
		Log:           slog.New(slog.DiscardHandler),
		HTTPClient:    moduletest.HTTPClient(handler),
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

// fakeStatus answers /status with whatever was set last.
type fakeStatus struct {
	mu      sync.Mutex
	players int64 // 0 is offline: 503
}

func (f *fakeStatus) set(players int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.players = players
}

func (f *fakeStatus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.players == 0 {
		http.Error(w, `{"error":"unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	fmt.Fprintf(w, `{"players":%d,"server_version":"1","start_time":"2000-01-01T00:00:00Z"}`, f.players)
}

func TestHistory(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// The bubble starts at 2000-01-01 00:00 UTC.
		fake := &fakeStatus{}
		set := load(t, fake)
		// 00:00 100, 00:05 200, 00:10 offline, 00:15 not polled, 00:20 301.
		for _, step := range []struct {
			players int64
			wait    time.Duration
		}{{100, 5 * time.Minute}, {200, 5 * time.Minute}, {0, 10 * time.Minute}, {301, 0}} {
			fake.set(step.players)
			if err := set.Update(t.Context(), "eveonline_status"); err != nil {
				t.Fatalf("Update: %v", err)
			}
			time.Sleep(step.wait)
		}

		at := func(hh, mm int) time.Time { return time.Date(2000, 1, 1, hh, mm, 0, 0, time.UTC) }
		day0 := func(mm int) time.Time { return time.Date(1999, 12, 31, 23, mm, 0, 0, time.UTC) }
		p := func(n int64) *int64 { return &n }
		testCases := []struct {
			name   string
			params map[string]any
			want   []status.Point
		}{
			{
				name:   "5 minute steps",
				params: map[string]any{"hours": 1},
				want: []status.Point{
					{Time: day0(25)}, {Time: day0(30)}, {Time: day0(35)}, {Time: day0(40)}, {Time: day0(45)}, {Time: day0(50)}, {Time: day0(55)},
					{Time: at(0, 0), Players: p(100)}, {Time: at(0, 5), Players: p(200)}, {Time: at(0, 10)}, {Time: at(0, 15)},
					{Time: at(0, 20), Players: p(301)},
				},
			},
			{
				name:   "10 minute steps average",
				params: map[string]any{"hours": 1, "stepMinutes": 10},
				want: []status.Point{
					{Time: day0(30)}, {Time: day0(40)}, {Time: day0(50)},
					{Time: at(0, 0), Players: p(150)}, {Time: at(0, 10)}, {Time: at(0, 20), Players: p(301)},
				},
			},
		}
		for _, testCase := range testCases {
			res := set.Resolve(t.Context(), "eveonline_status", "history", testCase.params)
			if res.Error != "" {
				t.Fatalf("%s: %s", testCase.name, res.Error)
			}
			if diff := cmp.Diff(testCase.want, res.Value); diff != "" {
				t.Errorf("%s (-want +got):\n%s", testCase.name, diff)
			}
		}
	})
}

func TestHistoryParams(t *testing.T) {
	t.Parallel()
	set := load(t, http.NotFoundHandler())
	testCases := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"negative", map[string]any{"hours": -1}, "hours and stepMinutes must not be negative"},
		{"too long", map[string]any{"hours": 2161}, "hours must be at most 2160, got 2161"},
		{"too many points", map[string]any{"hours": 2160, "stepMinutes": 1}, "hours and stepMinutes give 129600 points, want 1 to 2000"},
		{"step longer than range", map[string]any{"hours": 1, "stepMinutes": 120}, "hours and stepMinutes give 0 points, want 1 to 2000"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if res := set.Resolve(t.Context(), "eveonline_status", "history", testCase.params); res.Error != testCase.want {
				t.Errorf("got error %q, want %q", res.Error, testCase.want)
			}
		})
	}
}
