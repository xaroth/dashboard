package status_test

import (
	"fmt"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/module/moduletest"
	"github.com/xaroth/dashboard/modules/eveonline/status"
	"github.com/xaroth/dashboard/series"
)

func load(tb testing.TB, handler http.Handler) *module.Set {
	tb.Helper()
	return loadClient(tb, moduletest.HTTPClient(handler))
}

func loadClient(tb testing.TB, client *http.Client) *module.Set {
	tb.Helper()
	return moduletest.Load(tb, nil, "eveonline_esi: {contact: [mailto:me@example.com]}", module.Options{
		Registrations: []module.Registration{status.Module},
		HTTPClient:    client,
	})
}

func TestServer(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 17, 11, 0, 12, 0, time.UTC)
	testCases := []struct {
		name    string
		handler http.HandlerFunc
		// client replaces handler, for failures before ESI answers.
		client  *http.Client
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
			// Our network is down: the request times out before connecting.
			name: "unreachable",
			client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				<-r.Context().Done()
				return nil, r.Context().Err()
			})},
			wantErr: `requesting status: Get "https://esi.evetech.net/status": context deadline exceeded`,
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
				client := testCase.client
				if client == nil {
					client = moduletest.HTTPClient(testCase.handler)
				}
				set := loadClient(t, client)
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestServerBeforeFirstUpdate(t *testing.T) {
	t.Parallel()
	set := load(t, http.NotFoundHandler())
	res := set.Resolve(t.Context(), "eveonline_status", "server", nil)
	if res.Value.(*status.Status) != nil || !res.Stale {
		t.Errorf("got %v, stale %v, want no status, stale", res.Value, res.Stale)
	}
}

func TestHistory(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// The bubble starts at 2000-01-01 00:00 UTC.
		fake := status.NewFake(t)
		set := load(t, fake)
		// 00:00 100, 00:05 200, 00:10 offline, 00:15 not polled, 00:20 301.
		for _, step := range []struct {
			players int64
			wait    time.Duration
		}{{100, 5 * time.Minute}, {200, 5 * time.Minute}, {0, 10 * time.Minute}, {301, 0}} {
			fake.Set(t, status.ExpectPlayers(step.players))
			if err := set.Update(t.Context(), "eveonline_status"); err != nil {
				t.Fatalf("Update: %v", err)
			}
			time.Sleep(step.wait)
		}
		if err := set.FlushSeries(t.Context()); err != nil {
			t.Fatalf("FlushSeries: %v", err)
		}

		at := func(hh, mm int) time.Time { return time.Date(2000, 1, 1, hh, mm, 0, 0, time.UTC) }
		day0 := func(mm int) time.Time { return time.Date(1999, 12, 31, 23, mm, 0, 0, time.UTC) }
		p := func(v float64) *float64 { return &v }
		testCases := []struct {
			name   string
			params map[string]any
			want   []series.Point
		}{
			{
				name:   "5 minute steps",
				params: map[string]any{"hours": 1, "stepMinutes": 5},
				want: []series.Point{
					{T: day0(20)}, {T: day0(25)}, {T: day0(30)}, {T: day0(35)}, {T: day0(40)}, {T: day0(45)}, {T: day0(50)}, {T: day0(55)},
					{T: at(0, 0), V: p(100)}, {T: at(0, 5), V: p(200)}, {T: at(0, 10)}, {T: at(0, 15)},
					{T: at(0, 20), V: p(301)},
				},
			},
			{
				name:   "10 minute steps average",
				params: map[string]any{"hours": 1, "stepMinutes": 10},
				want: []series.Point{
					{T: day0(20)}, {T: day0(30)}, {T: day0(40)}, {T: day0(50)},
					{T: at(0, 0), V: p(150)}, {T: at(0, 10)}, {T: at(0, 20), V: p(301)},
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
