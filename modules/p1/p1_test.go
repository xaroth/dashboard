package p1_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v3"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/module/moduletest"
	"github.com/xaroth/dashboard/modules/p1"
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
	if err := yaml.Unmarshal([]byte("p1: {url: http://p1.example/}"), &configs); err != nil {
		tb.Fatalf("config: %v", err)
	}
	set, err := module.Load(tb.Context(), module.Options{
		Registrations: []module.Registration{p1.Module},
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

func actual(tb testing.TB, set *module.Set) *p1.Actual {
	tb.Helper()
	res := set.Resolve(tb.Context(), "p1", "actual", nil)
	if res.Error != "" {
		tb.Fatalf("actual: %s", res.Error)
	}
	return res.Value.(*p1.Actual)
}

func ptr(v float64) *float64 { return &v }

func TestActualFixture(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/actual.json")
	if err != nil {
		t.Fatal(err)
	}
	set := load(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/sm/actual" {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	if err := set.Update(t.Context(), "p1"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	at := time.Date(2026, 10, 25, 13, 14, 15, 0, time.UTC)
	want := &p1.Actual{
		MeasuredAt: at,
		Voltage:    p1.Phases{L1: ptr(234.9), L2: ptr(231.5), L3: ptr(234.3)},
		FlowKw:     -0.007,
		Today:      &p1.Usage{From: at, To: at, TakenKwh: 0, ReturnedKwh: 0, GasM3: ptr(0)},
	}
	got := actual(t, set)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("actual (-want +got):\n%s", diff)
	}
	// The sum of both tariffs minus itself is -0 in floating point.
	if math.Signbit(got.Today.TakenKwh) {
		t.Errorf("got today taken -0, want 0")
	}
}

// fakeDongle answers /api/v2/sm/actual with the telegram set last.
type fakeDongle struct {
	mu   sync.Mutex
	body map[string]any
}

// set replaces the telegram: timestamp, delivered and returned per tariff
// (kWh), power delivered and returned (kW), and gas (m³; negative leaves it
// out).
func (f *fakeDongle) set(ts string, d1, d2, r1, r2, pd, pr, gas float64) {
	v := func(x any) map[string]any { return map[string]any{"value": x} }
	body := map[string]any{
		"timestamp":                v(ts),
		"energy_delivered_tariff1": v(d1),
		"energy_delivered_tariff2": v(d2),
		"energy_returned_tariff1":  v(r1),
		"energy_returned_tariff2":  v(r2),
		"power_delivered":          v(pd),
		"power_returned":           v(pr),
		"voltage_l1":               v(230.0),
	}
	if gas >= 0 {
		body["gas_delivered"] = v(gas)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.body = body
}

func (f *fakeDongle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	json.NewEncoder(w).Encode(f.body)
}

func TestDays(t *testing.T) {
	t.Parallel()
	fake := &fakeDongle{}
	set := load(t, fake)
	update := func(ts string, d1, d2, r1, r2, pd, pr, gas float64) {
		t.Helper()
		fake.set(ts, d1, d2, r1, r2, pd, pr, gas)
		if err := set.Update(t.Context(), "p1"); err != nil {
			t.Fatalf("Update %s: %v", ts, err)
		}
	}
	utc := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, time.UTC) }

	// Started mid-day on the 23rd, in summer time.
	update("261023150000S", 100, 200, 10, 20, 0.5, 0, 50)
	update("261023235900S", 101, 202, 10, 21, 0.5, 0, 51)
	// Midnight of the 24th missed: the first reading is at 00:30.
	update("261024003000S", 101.5, 202, 10, 21, 0.5, 0, 51.25)
	update("261024120000S", 102, 203, 12, 21, 0, 1.5, 52)
	// DST ended at 03:00 on the 25th.
	update("261025000000S", 103, 204, 13, 22, 0.2, 0, 53)
	update("261025140000W", 104, 204, 15, 22, 0, 2.25, 53.5)

	want := &p1.Actual{
		MeasuredAt: utc(25, 13, 0),
		Voltage:    p1.Phases{L1: ptr(230)},
		FlowKw:     -2.25,
		Today:      &p1.Usage{From: utc(24, 22, 0), To: utc(25, 13, 0), TakenKwh: 1, ReturnedKwh: 2, GasM3: ptr(0.5)},
		// From the 00:30 reading: the first of the 24th.
		Yesterday: &p1.Usage{From: utc(23, 22, 30), To: utc(24, 22, 0), TakenKwh: 3.5, ReturnedKwh: 4, GasM3: ptr(1.75)},
	}
	if diff := cmp.Diff(want, actual(t, set)); diff != "" {
		t.Errorf("actual (-want +got):\n%s", diff)
	}
}

func TestNoYesterday(t *testing.T) {
	t.Parallel()
	fake := &fakeDongle{}
	set := load(t, fake)
	// A day without readings in between: the 23rd is not yesterday.
	for _, ts := range []string{"261023120000W", "261025120000W"} {
		fake.set(ts, 1, 2, 3, 4, 0, 0, -1)
		if err := set.Update(t.Context(), "p1"); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}
	got := actual(t, set)
	if got.Yesterday != nil {
		t.Errorf("got yesterday %+v, want null", got.Yesterday)
	}
	if got.Today == nil || got.Today.GasM3 != nil {
		t.Errorf("got today %+v, want usage without gas", got.Today)
	}
}

func TestMeterReplaced(t *testing.T) {
	t.Parallel()
	fake := &fakeDongle{}
	set := load(t, fake)
	for _, d1 := range []float64{1000, 5} {
		fake.set("261025120000W", d1, 0, 0, 0, 0, 0, -1)
		if err := set.Update(t.Context(), "p1"); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}
	if got := actual(t, set); got.Today != nil {
		t.Errorf("got today %+v, want null after a counter went down", got.Today)
	}
}

func TestUpdateErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		handler  http.HandlerFunc
		wantErr  string
		wantAuth bool
	}{
		{
			name:    "server error",
			handler: func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) },
			wantErr: "requesting actual: 500 Internal Server Error: boom",
		},
		{
			name:     "basic auth",
			handler:  func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
			wantErr:  "requesting actual: 401 Unauthorized: authentication rejected",
			wantAuth: true,
		},
		{
			// The dongle answers before it has read a telegram.
			name:    "no telegram",
			handler: func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) },
			wantErr: "actual has no energy_delivered_tariff1",
		},
		{
			name: "bad timestamp",
			handler: func(w http.ResponseWriter, r *http.Request) {
				f := &fakeDongle{}
				f.set("261025120000X", 0, 0, 0, 0, 0, 0, -1)
				f.ServeHTTP(w, r)
			},
			wantErr: `timestamp "261025120000X": want W or S at the end`,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			set := load(t, testCase.handler)
			err := set.Update(t.Context(), "p1")
			if err == nil || !strings.HasPrefix(err.Error(), testCase.wantErr) {
				t.Errorf("got Update error %v, want %q", err, testCase.wantErr)
			}
			if got := errors.Is(err, module.ErrAuth); got != testCase.wantAuth {
				t.Errorf("got ErrAuth %v, want %v", got, testCase.wantAuth)
			}
		})
	}
}

func TestActualBeforeFirstUpdate(t *testing.T) {
	t.Parallel()
	set := load(t, http.NotFoundHandler())
	res := set.Resolve(t.Context(), "p1", "actual", nil)
	if res.Value.(*p1.Actual) != nil || !res.Stale {
		t.Errorf("got %v, stale %v, want no reading, stale", res.Value, res.Stale)
	}
}
