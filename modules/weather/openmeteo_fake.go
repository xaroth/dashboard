package weather

import (
	_ "embed"
	"net/http"
	"net/url"
	"sync"
	"testing"
)

var (
	//go:embed testdata/geocode.json
	fakeGeocode []byte
	//go:embed testdata/forecast.json
	fakeForecast []byte
)

// FakeOpenMeteo serves the Open-Meteo geocoding and forecast APIs at their
// default URLs, for moduletest.HTTPClient. It answers with responses
// recorded for Amsterdam on 2026-10-16 at 00:45 local time, and records the
// requests.
type FakeOpenMeteo struct {
	mu       sync.Mutex
	geocodes int
	params   url.Values
	down     bool
	forecast []byte
}

// FakeOption sets up a FakeOpenMeteo.
type FakeOption func(testing.TB, *FakeOpenMeteo)

// NewFake returns a FakeOpenMeteo serving the recorded responses, then opts
// applied.
func NewFake(tb testing.TB, opts ...FakeOption) *FakeOpenMeteo {
	tb.Helper()
	f := &FakeOpenMeteo{forecast: fakeForecast}
	for _, opt := range opts {
		opt(tb, f)
	}
	return f
}

// ExpectForecast replaces the forecast response.
func ExpectForecast(body []byte) FakeOption {
	return func(_ testing.TB, f *FakeOpenMeteo) { f.forecast = body }
}

// ExpectUnavailable makes every request fail with 502.
func ExpectUnavailable(_ testing.TB, f *FakeOpenMeteo) { f.down = true }

// GeocodeCalls returns how many geocoding requests were served.
func (f *FakeOpenMeteo) GeocodeCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.geocodes
}

// ForecastParams returns the query of the last forecast request.
func (f *FakeOpenMeteo) ForecastParams() url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.params
}

func (f *FakeOpenMeteo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		http.Error(w, "upstream down", http.StatusBadGateway)
		return
	}
	switch "https://" + r.URL.Host + r.URL.Path {
	case defaultConfig().GeocodingURL:
		f.geocodes++
		w.Write(fakeGeocode)
	case defaultConfig().ForecastURL:
		f.params = r.URL.Query()
		w.Write(f.forecast)
	default:
		http.NotFound(w, r)
	}
}
