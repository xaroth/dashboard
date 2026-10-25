package p1

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"
)

// FakeDongle answers /api/v2/sm/actual with the telegram set last, for
// moduletest.HTTPClient. Before one is set it answers null.
type FakeDongle struct {
	mu   sync.Mutex
	body map[string]any
	at   time.Time
}

// FakeOption sets up a FakeDongle.
type FakeOption func(testing.TB, *FakeDongle)

// NewFake returns a FakeDongle without a telegram, then opts applied.
func NewFake(tb testing.TB, opts ...FakeOption) *FakeDongle {
	tb.Helper()
	f := &FakeDongle{}
	for _, opt := range opts {
		opt(tb, f)
	}
	return f
}

// ExpectTelegram sets the telegram: DSMR timestamp, delivered and returned
// per tariff (kWh), power delivered and returned (kW), and gas (m³;
// negative leaves it out).
func ExpectTelegram(ts string, d1, d2, r1, r2, pd, pr, gas float64) FakeOption {
	return func(_ testing.TB, f *FakeDongle) {
		v := func(x any) map[string]any { return map[string]any{"value": x} }
		f.body = map[string]any{
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
			f.body["gas_delivered"] = v(gas)
		}
		f.at = fakeMeterTime(ts)
	}
}

// ExpectGasTimestamp adds the gas meter's DSMR timestamp to the telegram.
func ExpectGasTimestamp(ts string) FakeOption {
	return func(_ testing.TB, f *FakeDongle) {
		f.body["gas_delivered_timestamp"] = map[string]any{"value": ts}
	}
}

// Set applies opts to a serving FakeDongle.
func (f *FakeDongle) Set(tb testing.TB, opts ...FakeOption) {
	tb.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, opt := range opts {
		opt(tb, f)
	}
}

// Now is the time of the telegram, as a clock for module.Options.
func (f *FakeDongle) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.at
}

func (f *FakeDongle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path != "/api/v2/sm/actual" {
		http.NotFound(w, r)
		return
	}
	json.NewEncoder(w).Encode(f.body)
}

// fakeMeterTime parses a DSMR timestamp as a Dutch meter means it,
// ignoring a malformed zone.
func fakeMeterTime(ts string) time.Time {
	offset := 3600
	if ts[12] == 'S' {
		offset = 7200
	}
	t, _ := time.ParseInLocation("060102150405", ts[:12], time.FixedZone("", offset))
	return t
}
