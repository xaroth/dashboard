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

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/module/yamlconfig"
	"github.com/xaroth/dashboard/modules/weather"
	"github.com/xaroth/dashboard/storage"
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

func (f *fakeOpenMeteo) geocodeCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.geocodes
}

func (f *fakeOpenMeteo) forecastParams() url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.forecast
}

// serve replaces the forecast response.
func (f *fakeOpenMeteo) serve(body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forecastBody = body
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

// load loads the weather module against the fake, with the given extra config keys.
func load(tb testing.TB, dir string, fake *fakeOpenMeteo, extra string) *module.Set {
	tb.Helper()
	return loadAt(tb, dir, fake, extra, fixtureNow)
}

func loadAt(tb testing.TB, dir string, fake *fakeOpenMeteo, extra string, now time.Time) *module.Set {
	tb.Helper()
	db, err := storage.Open(tb.Context(), dir)
	if err != nil {
		tb.Fatalf("storage: %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	src := fmt.Sprintf("weather: {geocoding_url: %q, forecast_url: %q, %s}", fake.URL+"/geocode", fake.URL+"/forecast", extra)
	configs, err := yamlconfig.Parse([]byte(src))
	if err != nil {
		tb.Fatalf("config: %v", err)
	}
	set, err := module.Load(tb.Context(), module.Options{
		Registrations: []module.Registration{weather.Module},
		Configs:       configs,
		Migrator:      db,
		Writer:        db.Writer,
		Reader:        db.Reader,
		Log:           slog.New(slog.DiscardHandler),
		HTTPClient:    fake.Client(),
		Now:           func() time.Time { return now },
	})
	if err != nil {
		tb.Fatalf("Load: %v", err)
	}
	return set
}

func forecast(tb testing.TB, set *module.Set, params map[string]any) (*weather.Forecast, module.Result) {
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
		"latitude": {"52.374"}, "longitude": {"4.88969"}, "timezone": {"Europe/Amsterdam"},
		"timeformat": {"unixtime"}, "forecast_days": {"3"}, "wind_speed_unit": {"kmh"},
	}
	for k, v := range want {
		if diff := cmp.Diff(v, fake.forecastParams()[k]); diff != "" {
			t.Errorf("forecast param %s (-want +got):\n%s", k, diff)
		}
	}

	f, res := forecast(t, set, map[string]any{"hours": 4, "days": 2})
	if res.Stale || res.FetchedAt == nil || !res.FetchedAt.Equal(fixtureNow) {
		t.Errorf("got %+v, want fresh, fetched at the fixture time", res)
	}
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	if diff := cmp.Diff(weather.Location{Name: "Amsterdam", Country: "The Netherlands", Latitude: 52.374, Longitude: 4.88969, Timezone: "Europe/Amsterdam"}, f.Location); diff != "" {
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
			t.Errorf("%s: got sunrise %v, want it before sunset %v", d.Date, d.Sunrise, d.Sunset)
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
	if fake.geocodeCalls() != 1 {
		t.Errorf("got %d geocode calls, want 1", fake.geocodeCalls())
	}
	// A different location is looked up again.
	set = load(t, dir, fake, `location: "Amsterdam"`)
	if err := set.Update(t.Context(), "weather"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if fake.geocodeCalls() != 2 {
		t.Errorf("got %d geocode calls after changing the location, want 2", fake.geocodeCalls())
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
		{"country mismatch", `location: "Amsterdam, Belgium"`, nil, "no match"},
		{"upstream error", "", func(f *fakeOpenMeteo) { f.fail = true }, "502"},
		{"uneven series", "", func(f *fakeOpenMeteo) {
			f.serve([]byte(`{"current":{},"hourly":{"time":[1,2],"temperature_2m":[1]},"daily":{}}`))
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
		want  module.Phase
	}{
		{"default", "", module.PhaseWaiting},
		{"too many days", "forecast_days: 17", module.PhaseInitFail},
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

func TestForecastNullProbabilities(t *testing.T) {
	t.Parallel()
	fake := newFake(t)
	var body map[string]any
	if err := json.Unmarshal(fake.forecastBody, &body); err != nil {
		t.Fatalf("decoding fixture: %v", err)
	}
	body["hourly"].(map[string]any)["precipitation_probability"].([]any)[1] = nil
	body["daily"].(map[string]any)["precipitation_probability_max"].([]any)[1] = nil
	b, _ := json.Marshal(body)
	fake.serve(b)
	set := load(t, t.TempDir(), fake, "forecast_days: 3")
	if err := set.Update(t.Context(), "weather"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	f, _ := forecast(t, set, map[string]any{"hours": 2, "days": 2})
	if f.Hourly[0].PrecipitationProbability == nil || f.Hourly[1].PrecipitationProbability != nil {
		t.Errorf("got hourly %v, %v, want a value then null", f.Hourly[0].PrecipitationProbability, f.Hourly[1].PrecipitationProbability)
	}
	if f.Daily[0].PrecipitationProbabilityMax == nil || f.Daily[1].PrecipitationProbabilityMax != nil {
		t.Errorf("got daily %v, %v, want a value then null", f.Daily[0].PrecipitationProbabilityMax, f.Daily[1].PrecipitationProbabilityMax)
	}
}

func TestForecastModel(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name       string
		extra      string
		wantModel  string
		nullHourly map[string]int // series -> entries from the end set to null
		nullDaily  map[string]int
		wantHours  int // entries cut from the end
		wantDays   int
	}{
		{name: "open-meteo pick", extra: "forecast_days: 3"},
		{
			name: "past the model horizon", extra: "forecast_days: 3, model: knmi_seamless", wantModel: "knmi_seamless",
			// As knmi_seamless returns them: precipitation and code end before temperature.
			nullHourly: map[string]int{"temperature_2m": 1, "precipitation": 3, "weather_code": 3},
			nullDaily:  map[string]int{"temperature_2m_max": 1, "precipitation_sum": 2},
			wantHours:  3, wantDays: 2,
		},
		{
			name: "all null", extra: "forecast_days: 3, model: knmi_seamless", wantModel: "knmi_seamless",
			nullHourly: map[string]int{"temperature_2m": 1000}, nullDaily: map[string]int{"temperature_2m_min": 1000},
			wantHours: 1000, wantDays: 1000,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(t)
			var body map[string]any
			if err := json.Unmarshal(fake.forecastBody, &body); err != nil {
				t.Fatalf("decoding fixture: %v", err)
			}
			nullTail := func(series map[string]any, nulls map[string]int) int {
				n := len(series["time"].([]any))
				for key, k := range nulls {
					values := series[key].([]any)
					for i := max(0, n-k); i < n; i++ {
						values[i] = nil
					}
				}
				return n
			}
			hours := nullTail(body["hourly"].(map[string]any), testCase.nullHourly)
			days := nullTail(body["daily"].(map[string]any), testCase.nullDaily)
			b, _ := json.Marshal(body)
			fake.serve(b)
			// Before the first entry, so the query windows start at the beginning.
			set := loadAt(t, t.TempDir(), fake, testCase.extra, time.Unix(0, 0))
			if err := set.Update(t.Context(), "weather"); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if got := fake.forecastParams().Get("models"); got != testCase.wantModel {
				t.Errorf("got models %q, want %q", got, testCase.wantModel)
			}
			f, _ := forecast(t, set, map[string]any{"hours": 1000, "days": 16})
			if got, want := len(f.Hourly), max(0, hours-testCase.wantHours); got != want {
				t.Errorf("got %d hours, want %d", got, want)
			}
			if got, want := len(f.Daily), max(0, days-testCase.wantDays); got != want {
				t.Errorf("got %d days, want %d", got, want)
			}
		})
	}
}

func TestForecastWindows(t *testing.T) {
	t.Parallel()
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	// 25 Oct 2026 has 25 hours in Amsterdam: the clocks go back at 03:00.
	dst := []byte(`{"current":{"time":1792879200},
		"hourly":{"time":[],"temperature_2m":[],"precipitation_probability":[],"precipitation":[],"weather_code":[]},
		"daily":{"time":[1792879200,1792969200],"weather_code":[3,3],"temperature_2m_max":[1,1],"temperature_2m_min":[0,0],
			"precipitation_sum":[0,0],"precipitation_probability_max":[0,0],"sunrise":[1792908000,1792994400],"sunset":[1792944000,1793030400]}}`)
	testCases := []struct {
		name      string
		body      []byte
		now       time.Time
		firstHour string
		firstDay  string
	}{
		{"on the hour", nil, time.Date(2026, 10, 16, 1, 0, 0, 0, ams), "10-16 01:00", "10-16"},
		{"at local midnight", nil, time.Date(2026, 10, 17, 0, 0, 0, 0, ams), "10-17 00:00", "10-17"},
		{"late on a 25 hour day", dst, time.Date(2026, 10, 25, 23, 30, 0, 0, ams), "", "10-25"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(t)
			if testCase.body != nil {
				fake.serve(testCase.body)
			}
			set := loadAt(t, t.TempDir(), fake, "forecast_days: 3", testCase.now)
			if err := set.Update(t.Context(), "weather"); err != nil {
				t.Fatalf("Update: %v", err)
			}
			f, _ := forecast(t, set, map[string]any{"hours": 1, "days": 1})
			if testCase.firstHour != "" && (len(f.Hourly) == 0 || f.Hourly[0].Time.In(ams).Format("01-02 15:04") != testCase.firstHour) {
				t.Errorf("got hourly %v, want first %s", f.Hourly, testCase.firstHour)
			}
			if len(f.Daily) == 0 || f.Daily[0].Date.In(ams).Format("01-02") != testCase.firstDay {
				t.Errorf("got daily %v, want first %s", f.Daily, testCase.firstDay)
			}
		})
	}
}

func TestForecastRejectsNegative(t *testing.T) {
	t.Parallel()
	set := load(t, t.TempDir(), newFake(t), "")
	res := set.Resolve(t.Context(), "weather", "forecast", map[string]any{"hours": -1})
	if res.Error == "" {
		t.Error("got no error for negative hours, want one")
	}
}
