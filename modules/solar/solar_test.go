package solar_test

import (
	"context"
	"math"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/module/moduletest"
	"github.com/xaroth/dashboard/modules/solar"
	"github.com/xaroth/dashboard/series"
)

// load runs solar against fake, with extra config keys after the host.
func load(tb testing.TB, fake *solar.FakeDTU, extra string) *module.Set {
	tb.Helper()
	return moduletest.Load(tb, nil, "solar: {host: "+solar.FakeHost+extra+"}", module.Options{
		Registrations: []module.Registration{solar.Module},
		Dialer:        fake,
	})
}

func production(tb testing.TB, set *module.Set) *solar.Production {
	tb.Helper()
	res := set.Resolve(tb.Context(), "solar", "production", nil)
	if res.Error != "" {
		tb.Fatalf("production: %s", res.Error)
	}
	return res.Value.(*solar.Production)
}

func TestProduction(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		opts []solar.FakeOption
	}{
		{name: "byte count right"},
		{name: "byte count wrong", opts: []solar.FakeOption{solar.ExpectWrongByteCount}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			fake := solar.NewFake(t, testCase.opts...)
			fake.SetInputs(
				solar.RealInput,
				solar.FakeInput("100000000687", 1, 0, true), // no panel
				solar.FakeInput("100000000687", 2, 40.3, true),
				// Counts while the link is down.
				solar.FakeInput("100000000815", 1, 39.9, false),
			)
			set := load(t, fake, `, unused: ["100000000687:1"]`)
			if err := set.Update(t.Context(), "solar"); err != nil {
				t.Fatalf("Update: %v", err)
			}
			want := &solar.Production{
				PowerW: 122.3,
				Panels: []solar.Panel{
					{Inverter: "100000000456", Port: 1, PowerW: 42.1},
					{Inverter: "100000000687", Port: 2, PowerW: 40.3},
					{Inverter: "100000000815", Port: 1, PowerW: 39.9},
				},
			}
			if diff := cmp.Diff(want, production(t, set)); diff != "" {
				t.Errorf("production (-want +got):\n%s", diff)
			}
			// DTU serial, 4 inputs and the empty one after them.
			if got := fake.Requests(); got != 6 {
				t.Errorf("got %d requests, want 6", got)
			}
		})
	}
}

func TestUpdateErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name    string
		opts    []solar.FakeOption
		extra   string
		wantErr string
	}{
		{
			name:    "no inverters",
			wantErr: "DTU reports no inverter inputs",
		},
		{
			name:    "exception",
			opts:    []solar.FakeOption{solar.ExpectException},
			wantErr: "reading DTU serial: exception 2 reading 0x2000",
		},
		{
			name:    "hang",
			opts:    []solar.FakeOption{solar.ExpectHang},
			wantErr: "reading DTU serial: reading response: ",
		},
		{
			name:    "unreachable",
			extra:   ", port: 503",
			wantErr: "connecting to DTU: connection refused",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				set := load(t, solar.NewFake(t, testCase.opts...), testCase.extra)
				start := time.Now()
				err := set.Update(t.Context(), "solar")
				if err == nil || !strings.HasPrefix(err.Error(), testCase.wantErr) {
					t.Errorf("got Update error %v, want %q", err, testCase.wantErr)
				}
				if d := time.Since(start); d > 30*time.Second {
					t.Errorf("update took %v, want at most 30s", d)
				}
			})
		})
	}
}

func TestConfigErrors(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct{ extra, wantErr string }{
		{`, unused: ["100000000687"]`, `unused: want serial:port, got "100000000687"`},
		{`, port: 0`, "port must be 1 to 65535, got 0"},
		{`, night_interval: 10s`, "night_interval must be at least 1m, got 10s"},
		{`, night_interval: 30m`, "night_interval must be under 30m0s, got 30m0s"},
	} {
		set := load(t, solar.NewFake(t), testCase.extra)
		err := set.Update(t.Context(), "solar")
		if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
			t.Errorf("%s: got %v, want %q", testCase.extra, err, testCase.wantErr)
		}
	}
}

// TestNight runs the schedule: every minute while any input produces, every
// night_interval while none does.
func TestNight(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := solar.NewFake(t)
		set := load(t, fake, "")
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			set.Run(ctx)
			close(done)
		}()
		defer func() { cancel(); <-done }()

		var fetched *time.Time
		// next waits for the next update and returns the delay it set.
		next := func() time.Duration {
			t.Helper()
			for {
				step := time.Second
				st := status(t, set)
				if st.NextRun.After(time.Now()) {
					step = st.NextRun.Sub(time.Now())
				}
				time.Sleep(step)
				synctest.Wait()
				st = status(t, set)
				if st.Error != "" {
					t.Fatalf("update failed: %s", st.Error)
				}
				if st.FetchedAt != nil && (fetched == nil || st.FetchedAt.After(*fetched)) {
					fetched = st.FetchedAt
					return st.NextRun.Sub(*st.FetchedAt)
				}
			}
		}
		fake.SetInputs(solar.FakeInput("100000000456", 1, 0, true))
		if got := next(); got != 10*time.Minute {
			t.Errorf("night: got next in %v, want 10m", got)
		}
		fake.SetInputs(solar.FakeInput("100000000456", 1, 0.1, true))
		if got := next(); got != time.Minute {
			t.Errorf("day: got next in %v, want 1m", got)
		}
	})
}

func status(tb testing.TB, set *module.Set) module.Status {
	tb.Helper()
	st, err := set.Statuses(tb.Context())
	if err != nil || len(st) != 1 {
		tb.Fatalf("Statuses: %v, %v", st, err)
	}
	return st[0]
}

func TestProductionBeforeFirstUpdate(t *testing.T) {
	t.Parallel()
	set := load(t, solar.NewFake(t), "")
	res := set.Resolve(t.Context(), "solar", "production", nil)
	if res.Value.(*solar.Production) != nil || !res.Stale {
		t.Errorf("got %v, stale %v, want no production, stale", res.Value, res.Stale)
	}
}

// TestPower runs in a bubble: the minute must not turn between the update
// and the query.
func TestPower(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := solar.NewFake(t)
		fake.SetInputs(solar.RealInput, solar.FakeInput("100000000687", 2, 40.3, true))
		set := load(t, fake, "")
		if err := set.Update(t.Context(), "solar"); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if err := set.FlushSeries(t.Context()); err != nil {
			t.Fatalf("FlushSeries: %v", err)
		}
		res := set.Resolve(t.Context(), "solar", "power", map[string]any{"hours": 1, "stepMinutes": 1})
		if res.Error != "" {
			t.Fatalf("power: %s", res.Error)
		}
		pts := res.Value.([]series.Point)
		if last := pts[len(pts)-1]; last.V == nil || math.Abs(*last.V-82.4) > 1e-9 {
			t.Errorf("got %v W in the last minute, want 82.4", last.V)
		}
	})
}
