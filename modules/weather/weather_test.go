package weather_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v3"

	"github.com/xaroth/dashboard/modules/weather"
	"github.com/xaroth/dashboard/service/modules"
	"github.com/xaroth/dashboard/service/storage"
)

// The fixtures were recorded at this moment: 2026-10-16 00:45 in Amsterdam.
var fixtureNow = time.Unix(1792104300, 0)

// fakeOpenMeteo serves the recorded responses and records the requests.
type fakeOpenMeteo struct {
	*httptest.Server
	mu           sync.Mutex
	geocodes     int
	forecast     url.Values
	fail         bool
	forecastBody []byte
}

func newFake(tb testing.TB) *fakeOpenMeteo {
	tb.Helper()
	geocode, err := os.ReadFile("testdata/geocode.json")
	if err != nil {
		tb.Fatal(err)
	}
	forecast, err := os.ReadFile("testdata/forecast.json")
	if err != nil {
		tb.Fatal(err)
	}
	f := &fakeOpenMeteo{forecastBody: forecast}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.fail {
			http.Error(w, "upstream down", http.StatusBadGateway)
			return
		}
		switch r.URL.Path {
		case "/geocode":
			f.geocodes++
			w.Write(geocode)
		case "/forecast":
			f.forecast = r.URL.Query()
			w.Write(f.forecastBody)
		default:
			http.NotFound(w, r)
		}
	}))
	tb.Cleanup(f.Close)
	return f
}

func registration(tb testing.TB) modules.Registration {
	tb.Helper()
	for _, r := range modules.Registered() {
		if r.Name() == "weather" {
			return r
		}
	}
	tb.Fatal("weather is not registered")
	return modules.Registration{}
}

// load loads the weather module against the fake, with the given extra config keys.
func load(tb testing.TB, dir string, fake *fakeOpenMeteo, extra string) *modules.Set {
	tb.Helper()
	db, err := storage.Open(tb.Context(), dir)
	if err != nil {
		tb.Fatalf("storage: %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	src := fmt.Sprintf("weather: {geocoding_url: %q, forecast_url: %q, %s}", fake.URL+"/geocode", fake.URL+"/forecast", extra)
	var configs map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(src), &configs); err != nil {
		tb.Fatalf("config: %v", err)
	}
	set, err := modules.Load(tb.Context(), modules.Options{
		Registrations: []modules.Registration{registration(tb)},
		Configs:       configs,
		DB:            db,
		Log:           slog.New(slog.DiscardHandler),
		HTTPClient:    fake.Client(),
		Now:           func() time.Time { return fixtureNow },
	})
	if err != nil {
		tb.Fatalf("Load: %v", err)
	}
	return set
}

func forecast(tb testing.TB, set *modules.Set, params map[string]any) (*weather.Forecast, modules.Result) {
	tb.Helper()
	res := set.Resolve(tb.Context(), "weather", "forecast", params)
	if res.Error != "" {
		tb.Fatalf("forecast: %s", res.Error)
	}
	f, _ := res.Value.(*weather.Forecast)
	return f, res
}

func TestUpdateAndForecast(t *testing.T) {
	t.Parallel()
	fake := newFake(t)
	set := load(t, t.TempDir(), fake, "forecast_days: 3")

	if err := set.Update(t.Context(), "weather"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	want := url.Values{
		"latitude": {"52.2375"}, "longitude": {"4.82639"}, "timezone": {"Europe/Amsterdam"},
		"timeformat": {"unixtime"}, "forecast_days": {"3"}, "wind_speed_unit": {"kmh"},
	}
	for k, v := range want {
		if diff := cmp.Diff(v, fake.forecast[k]); diff != "" {
			t.Errorf("forecast param %s (-want +got):\n%s", k, diff)
		}
	}

	f, res := forecast(t, set, map[string]any{"hours": 4, "days": 2})
	if res.Stale || res.FetchedAt == nil || !res.FetchedAt.Equal(fixtureNow) {
		t.Errorf("got %+v, want fresh, fetched at the fixture time", res)
	}
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	if diff := cmp.Diff(weather.Location{Name: "Uithoorn", Country: "The Netherlands", Latitude: 52.2375, Longitude: 4.82639, Timezone: "Europe/Amsterdam"}, f.Location); diff != "" {
		t.Errorf("location (-want +got):\n%s", diff)
	}
	if got, want := f.Current.Time, time.Date(2026, 10, 16, 0, 45, 0, 0, ams); !got.Equal(want) {
		t.Errorf("got current time %v, want %v", got, want)
	}
	// Hourly from the current hour (00:00 local), daily from today.
	var hours []string
	for _, h := range f.Hourly {
		hours = append(hours, h.Time.In(ams).Format("01-02 15:04"))
	}
	if diff := cmp.Diff([]string{"10-16 00:00", "10-16 01:00", "10-16 02:00", "10-16 03:00"}, hours); diff != "" {
		t.Errorf("hours (-want +got):\n%s", diff)
	}
	var days []string
	for _, d := range f.Daily {
		days = append(days, d.Date.In(ams).Format("01-02 15:04"))
		if !d.Sunrise.Before(d.Sunset) {
			t.Errorf("%s: sunrise %v not before sunset %v", d.Date, d.Sunrise, d.Sunset)
		}
	}
	if diff := cmp.Diff([]string{"10-16 00:00", "10-17 00:00"}, days); diff != "" {
		t.Errorf("days (-want +got):\n%s", diff)
	}

	// The result is what pages get: JSON with times as RFC 3339.
	b, err := json.Marshal(f)
	if err != nil || !strings.Contains(string(b), `"time":"2026-10-16T00:45:00+02:00"`) {
		t.Errorf("got %s (%v), want RFC 3339 local times", b, err)
	}
}

func TestForecastDefaults(t *testing.T) {
	t.Parallel()
	set := load(t, t.TempDir(), newFake(t), "forecast_days: 3")
	if f, res := forecast(t, set, nil); f != nil || !res.Stale {
		t.Errorf("before any update: got %v, %+v, want no value, stale", f, res)
	}
	if err := set.Update(t.Context(), "weather"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	f, _ := forecast(t, set, nil)
	if len(f.Hourly) != 24 || len(f.Daily) != 3 {
		t.Errorf("got %d hours, %d days, want 24 and 3", len(f.Hourly), len(f.Daily))
	}
}

func TestGeocodeCached(t *testing.T) {
	t.Parallel()
	fake := newFake(t)
	dir := t.TempDir()
	set := load(t, dir, fake, "")
	for range 2 {
		if err := set.Update(t.Context(), "weather"); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}
	// Also across a restart.
	set = load(t, dir, fake, "")
	if err := set.Update(t.Context(), "weather"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if fake.geocodes != 1 {
		t.Errorf("got %d geocode calls, want 1", fake.geocodes)
	}
	// A different location is looked up again.
	set = load(t, dir, fake, `location: "Uithoorn"`)
	if err := set.Update(t.Context(), "weather"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if fake.geocodes != 2 {
		t.Errorf("got %d geocode calls after changing the location, want 2", fake.geocodes)
	}
}

func TestUpdateErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name  string
		extra string
		setup func(*fakeOpenMeteo)
		want  string
	}{
		{"country mismatch", `location: "Uithoorn, Belgium"`, nil, "no match"},
		{"upstream error", "", func(f *fakeOpenMeteo) { f.fail = true }, "502"},
		{"uneven series", "", func(f *fakeOpenMeteo) {
			f.forecastBody = []byte(`{"current":{},"hourly":{"time":[1,2],"temperature_2m":[1]},"daily":{}}`)
		}, "different lengths"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(t)
			if testCase.setup != nil {
				testCase.setup(fake)
			}
			set := load(t, t.TempDir(), fake, testCase.extra)
			err := set.Update(t.Context(), "weather")
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("got %v, want error containing %q", err, testCase.want)
			}
			st, _ := set.Statuses(t.Context())
			if st[0].Error == "" || st[0].FetchedAt != nil {
				t.Errorf("got status %+v, want the failure recorded", st[0])
			}
		})
	}
}

func TestConfig(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name  string
		extra string
		want  modules.Phase
	}{
		{"default", "", modules.PhaseWaiting},
		{"too many days", "forecast_days: 17", modules.PhaseInitFail},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			set := load(t, t.TempDir(), newFake(t), testCase.extra)
			st, err := set.Statuses(t.Context())
			if err != nil || st[0].Phase != testCase.want {
				t.Errorf("got %+v, %v, want phase %s", st, err, testCase.want)
			}
		})
	}
}

func TestForecastRejectsNegative(t *testing.T) {
	t.Parallel()
	set := load(t, t.TempDir(), newFake(t), "")
	res := set.Resolve(t.Context(), "weather", "forecast", map[string]any{"hours": -1})
	if res.Error == "" {
		t.Error("got no error for negative hours")
	}
}
