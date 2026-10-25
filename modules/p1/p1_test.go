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
	"github.com/xaroth/dashboard/series"
	"github.com/xaroth/dashboard/storage"
)

// load runs p1 against handler, with now as the clock.
func load(tb testing.TB, handler http.Handler, now func() time.Time) *module.Set {
	tb.Helper()
	set, _ := open(tb, handler, now)
	return set
}

// open is load that also returns the database.
func open(tb testing.TB, handler http.Handler, now func() time.Time) (*module.Set, *storage.DB) {
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
		Now:           now,
	})
	if err != nil {
		tb.Fatalf("Load: %v", err)
	}
	return set, db
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
	}), func() time.Time { return time.Date(2026, 10, 25, 13, 15, 0, 0, time.UTC) })
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
	at   time.Time
}

// now is the clock of a module reading fakeDongle: the telegram's time.
func (f *fakeDongle) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.at
}

// meterTime parses a DSMR timestamp as a Dutch meter means it.
func meterTime(ts string) time.Time {
	offset := 3600
	if ts[12] == 'S' {
		offset = 7200
	}
	t, _ := time.ParseInLocation("060102150405", ts[:12], time.FixedZone("", offset))
	return t
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
	f.at = meterTime(ts)
}

// setGasAt adds the gas meter's timestamp to the telegram set last.
func (f *fakeDongle) setGasAt(ts string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.body["gas_delivered_timestamp"] = map[string]any{"value": ts}
}

func (f *fakeDongle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	json.NewEncoder(w).Encode(f.body)
}

func TestDays(t *testing.T) {
	t.Parallel()
	fake := &fakeDongle{}
	set := load(t, fake, fake.now)
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
	set := load(t, fake, fake.now)
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
	testCases := []struct {
		name      string
		d1, gas   float64 // the second reading
		wantToday bool
		wantGas   bool
	}{
		{name: "electricity meter", d1: 5, gas: 20, wantToday: false},
		// The gas meter is a separate device: electricity still counts.
		{name: "gas meter", d1: 1001, gas: 1, wantToday: true, wantGas: false},
		{name: "neither", d1: 1001, gas: 21, wantToday: true, wantGas: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeDongle{}
			set := load(t, fake, fake.now)
			for _, r := range [][2]float64{{1000, 20}, {testCase.d1, testCase.gas}} {
				fake.set("261025120000W", r[0], 0, 0, 0, 0, 0, r[1])
				if err := set.Update(t.Context(), "p1"); err != nil {
					t.Fatalf("Update: %v", err)
				}
			}
			got := actual(t, set).Today
			if (got != nil) != testCase.wantToday {
				t.Fatalf("got today %+v, want usage %v", got, testCase.wantToday)
			}
			if got != nil && (got.GasM3 != nil) != testCase.wantGas {
				t.Errorf("got gas %v, want gas %v", got.GasM3, testCase.wantGas)
			}
		})
	}
}

// After a restart the dongle reports gas only once the gas meter has sent
// a reading.
func TestGasLate(t *testing.T) {
	t.Parallel()
	fake := &fakeDongle{}
	set := load(t, fake, fake.now)
	for _, r := range []struct {
		ts      string
		d1, gas float64
	}{{"261025000100W", 100, -1}, {"261025000600W", 101, 50}, {"261025120000W", 102, 52}} {
		fake.set(r.ts, r.d1, 0, 0, 0, 0, 0, r.gas)
		if err := set.Update(t.Context(), "p1"); err != nil {
			t.Fatalf("Update %s: %v", r.ts, err)
		}
	}
	at := func(h, m int) time.Time { return time.Date(2026, 10, 24, h, m, 0, 0, time.UTC) }
	// Electricity counts from the first reading, gas from the first with gas.
	want := &p1.Usage{From: at(23, 1), To: at(11, 0).AddDate(0, 0, 1), TakenKwh: 2, GasM3: ptr(2)}
	if diff := cmp.Diff(want, actual(t, set).Today); diff != "" {
		t.Errorf("today (-want +got):\n%s", diff)
	}
}

// DSMR 4 gas meters report hourly: gas is stamped with the gas meter's
// time, and a repeated gas reading is not stored again.
func TestGasTimestamp(t *testing.T) {
	t.Parallel()
	fake := &fakeDongle{}
	set, db := open(t, fake, fake.now)
	for _, r := range []struct {
		ts, gasAt string
		gas       float64
	}{
		{"261025120000W", "261025110000W", 50},
		{"261025120100W", "261025110000W", 50},
		{"261025125900W", "261025110000W", 50},
		{"261025130000W", "261025120000W", 51},
	} {
		fake.set(r.ts, 1, 2, 3, 4, 0, 0, r.gas)
		fake.setGasAt(r.gasAt)
		if err := set.Update(t.Context(), "p1"); err != nil {
			t.Fatalf("Update %s: %v", r.ts, err)
		}
	}
	if err := set.FlushSeries(t.Context()); err != nil {
		t.Fatalf("FlushSeries: %v", err)
	}
	q := series.New(db.Reader)
	id, err := q.GetName(t.Context(), "p1.gas")
	if err != nil {
		t.Fatalf("gas series: %v", err)
	}
	rows, err := q.ListSamples(t.Context(), series.ListSamplesParams{Series: id, To: math.MaxInt64})
	if err != nil {
		t.Fatalf("gas samples: %v", err)
	}
	want := []series.ListSamplesRow{
		{Ts: time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC).Unix(), Value: 50},
		{Ts: time.Date(2026, 10, 25, 11, 0, 0, 0, time.UTC).Unix(), Value: 51},
	}
	if diff := cmp.Diff(want, rows); diff != "" {
		t.Errorf("gas samples (-want +got):\n%s", diff)
	}
}

func TestUpdateErrors(t *testing.T) {
	t.Parallel()
	telegram := func(ts string, d1 float64) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			f := &fakeDongle{}
			f.set(ts, d1, 0, 0, 0, 0, 0, -1)
			f.ServeHTTP(w, r)
		}
	}
	testCases := []struct {
		name     string
		handler  http.HandlerFunc
		now      time.Time
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
			name:    "zero readings",
			handler: telegram("261025120000W", 0),
			wantErr: "actual has zero meter readings",
		},
		{
			name:    "bad zone",
			handler: telegram("261025120000X", 1),
			wantErr: `timestamp "261025120000X": want W or S at the end`,
		},
		{
			name:    "bad date",
			handler: telegram("261332120000W", 1),
			wantErr: `timestamp "261332120000W": parsing time`,
		},
		{
			name: "bad gas timestamp",
			handler: func(w http.ResponseWriter, r *http.Request) {
				f := &fakeDongle{}
				f.set("261025120000W", 1, 0, 0, 0, 0, 0, 5)
				f.setGasAt("261025110000X")
				f.ServeHTTP(w, r)
			},
			wantErr: `gas timestamp "261025110000X": want W or S at the end`,
		},
		{
			// The dongle lost the meter and serves its last telegram.
			name:    "stale telegram",
			handler: telegram("261025120000W", 1),
			now:     time.Date(2026, 10, 25, 11, 11, 0, 0, time.UTC),
			wantErr: "meter time 2026-10-25T11:00:00Z is 11m0s off the clock",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			set := load(t, testCase.handler, func() time.Time { return testCase.now })
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
	set := load(t, http.NotFoundHandler(), time.Now)
	res := set.Resolve(t.Context(), "p1", "actual", nil)
	if res.Value.(*p1.Actual) != nil || !res.Stale {
		t.Errorf("got %v, stale %v, want no reading, stale", res.Value, res.Stale)
	}
}

func TestFlow(t *testing.T) {
	t.Parallel()
	fake := &fakeDongle{}
	set := load(t, fake, fake.now)
	// Taking 1.5 kW, then returning 2 kW a minute later.
	fake.set("261025120000W", 1, 2, 3, 4, 1.5, 0, 5)
	if err := set.Update(t.Context(), "p1"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	fake.set("261025120100W", 1, 2, 3, 4, 0, 2, 5)
	if err := set.Update(t.Context(), "p1"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := set.FlushSeries(t.Context()); err != nil {
		t.Fatalf("FlushSeries: %v", err)
	}
	res := set.Resolve(t.Context(), "p1", "flow", map[string]any{"hours": 1, "stepMinutes": 1})
	if res.Error != "" {
		t.Fatalf("flow: %s", res.Error)
	}
	pts := res.Value.([]series.Point)
	var got []float64
	for _, p := range pts[len(pts)-2:] {
		if p.V == nil {
			t.Fatalf("no flow at %v", p.T)
		}
		got = append(got, *p.V)
	}
	if diff := cmp.Diff([]float64{1.5, -2}, got); diff != "" {
		t.Errorf("flow (-want +got):\n%s", diff)
	}
	if res := set.Resolve(t.Context(), "p1", "flow", map[string]any{"stepMinutes": 7}); res.Error != "stepMinutes must divide 1440, got 7" {
		t.Errorf("got error %q for 7 minute steps", res.Error)
	}
}
