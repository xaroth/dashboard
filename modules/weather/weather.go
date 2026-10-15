// Package weather fetches forecasts from Open-Meteo for one configured place.
package weather

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/xaroth/dashboard/service/modules"
)

//go:embed sql/migrations/*.sql
var migrations embed.FS

type config struct {
	// Location is a place name, optionally with its country after a comma.
	Location     string `yaml:"location"`
	ForecastDays int    `yaml:"forecast_days"`
	GeocodingURL string `yaml:"geocoding_url"`
	ForecastURL  string `yaml:"forecast_url"`
}

func defaultConfig() *config {
	return &config{
		Location:     "Uithoorn, The Netherlands",
		ForecastDays: 7,
		GeocodingURL: "https://geocoding-api.open-meteo.com/v1/search",
		ForecastURL:  "https://api.open-meteo.com/v1/forecast",
	}
}

type module struct {
	cfg *config
}

var _ = modules.Register[module, config](
	"weather",
	defaultConfig,
	modules.WithUpdateInterval(15*time.Minute),
	modules.WithStaleAfter(2*time.Hour),
	modules.WithMigrations(migrations),
	modules.WithQuery("forecast", (*module).forecast),
)

func (m *module) Initialize(_ context.Context, cfg *config) error {
	if cfg.ForecastDays < 1 || cfg.ForecastDays > 16 {
		return fmt.Errorf("forecast_days must be 1 to 16, got %d", cfg.ForecastDays)
	}
	m.cfg = cfg
	return nil
}

func (m *module) Enabled(context.Context) bool { return m.cfg.Location != "" }

func (m *module) Update(ctx context.Context) error {
	loc, err := m.location(ctx)
	if err != nil {
		return err
	}
	f, err := fetchForecast(ctx, m.cfg.ForecastURL, loc, m.cfg.ForecastDays)
	if err != nil {
		return err
	}
	return modules.SaveSnapshot(ctx, f)
}

// location returns the coordinates for the configured location, geocoding
// it on first use and when the setting changes.
func (m *module) location(ctx context.Context) (Location, error) {
	q := New(modules.Database(ctx))
	row, err := q.GetLocation(ctx, m.cfg.Location)
	if err == nil {
		return Location{Name: row.Name, Country: row.Country, Latitude: row.Latitude, Longitude: row.Longitude, Timezone: row.Timezone}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Location{}, err
	}
	loc, err := geocode(ctx, m.cfg.GeocodingURL, m.cfg.Location)
	if err != nil {
		return Location{}, err
	}
	err = q.SaveLocation(ctx, SaveLocationParams{
		Query: m.cfg.Location, Name: loc.Name, Country: loc.Country,
		Latitude: loc.Latitude, Longitude: loc.Longitude, Timezone: loc.Timezone,
		ResolvedAt: modules.Clock(ctx)(),
	})
	if err != nil {
		return Location{}, fmt.Errorf("caching location: %w", err)
	}
	modules.Logger(ctx).Info("geocoded location", "query", m.cfg.Location, "name", loc.Name, "lat", loc.Latitude, "lon", loc.Longitude)
	return loc, nil
}

type forecastParams struct {
	// Hours of hourly forecast from the current hour; default 24.
	Hours int `json:"hours"`
	// Days of daily forecast from today; default 3.
	Days int `json:"days"`
}

// forecast returns the stored forecast from now on: Hours hourly entries
// starting at the current hour, Days daily entries starting today.
func (m *module) forecast(ctx context.Context, p forecastParams) (*Forecast, error) {
	if p.Hours < 0 || p.Days < 0 {
		return nil, errors.New("hours and days must not be negative")
	}
	var f Forecast
	ok, err := modules.LoadSnapshot(ctx, &f)
	if err != nil || !ok {
		return nil, err
	}
	now := modules.Clock(ctx)()
	f.Hourly = from(f.Hourly, func(h Hour) bool { return !h.Time.Add(time.Hour).Before(now) }, cmpOr(p.Hours, 24))
	f.Daily = from(f.Daily, func(d Day) bool { return !d.Date.AddDate(0, 0, 1).Before(now) }, cmpOr(p.Days, 3))
	return &f, nil
}

// from returns up to n entries starting at the first for which keep is true.
func from[T any](s []T, keep func(T) bool, n int) []T {
	for i, v := range s {
		if keep(v) {
			s = s[i:]
			return s[:min(n, len(s))]
		}
	}
	return []T{}
}

func cmpOr(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}
