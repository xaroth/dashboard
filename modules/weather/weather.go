// Package weather fetches forecasts from Open-Meteo for one configured place.
package weather

import (
	"cmp"
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/xaroth/dashboard/module"
)

//go:embed sql/migrations/*.sql
var migrations embed.FS

type config struct {
	// Location is a place name, optionally with its country after a comma.
	Location     string `yaml:"location"`
	ForecastDays int    `yaml:"forecast_days"`
	// Model is an Open-Meteo weather model, e.g. knmi_seamless. Empty lets
	// Open-Meteo pick per location.
	Model        string `yaml:"model"`
	GeocodingURL string `yaml:"geocoding_url"`
	ForecastURL  string `yaml:"forecast_url"`
}

func defaultConfig() *config {
	return &config{
		Location:     "Amsterdam, The Netherlands",
		ForecastDays: 7,
		GeocodingURL: "https://geocoding-api.open-meteo.com/v1/search",
		ForecastURL:  "https://api.open-meteo.com/v1/forecast",
	}
}

type weather struct {
	cfg *config
}

// Module is the weather registration.
var Module = module.Define[weather, config](
	"weather",
	defaultConfig,
	module.WithUpdateInterval(15*time.Minute),
	module.WithStaleAfter(2*time.Hour),
	module.WithMigrations(migrations),
	module.WithQuery("forecast", (*weather).forecast),
)

func (m *weather) Initialize(_ context.Context, cfg *config, _ module.Env) error {
	if cfg.ForecastDays < 1 || cfg.ForecastDays > 16 {
		return fmt.Errorf("forecast_days must be 1 to 16, got %d", cfg.ForecastDays)
	}
	m.cfg = cfg
	return nil
}

func (m *weather) Enabled(context.Context, module.Query) (bool, error) {
	return m.cfg.Location != "", nil
}

func (m *weather) Update(ctx context.Context, u *module.Update) error {
	loc, err := m.location(ctx, u)
	if err != nil {
		return err
	}
	f, err := fetchForecast(ctx, u.HTTP, m.cfg.ForecastURL, m.cfg.Model, loc, m.cfg.ForecastDays)
	if err != nil {
		return err
	}
	return u.SaveSnapshot(f)
}

// location returns the coordinates for the configured location, geocoding
// it on first use and when the setting changes.
func (m *weather) location(ctx context.Context, u *module.Update) (Location, error) {
	q := New(u.DB)
	row, err := q.GetLocation(ctx, m.cfg.Location)
	if err == nil {
		return Location{Name: row.Name, Country: row.Country, Latitude: row.Latitude, Longitude: row.Longitude, Timezone: row.Timezone}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Location{}, fmt.Errorf("reading cached location: %w", err)
	}
	loc, err := geocode(ctx, u.HTTP, m.cfg.GeocodingURL, m.cfg.Location)
	if err != nil {
		return Location{}, err
	}
	err = q.SaveLocation(ctx, SaveLocationParams{
		Query: m.cfg.Location, Name: loc.Name, Country: loc.Country,
		Latitude: loc.Latitude, Longitude: loc.Longitude, Timezone: loc.Timezone,
		ResolvedAt: u.Now(),
	})
	if err != nil {
		return Location{}, fmt.Errorf("caching location: %w", err)
	}
	u.Log.Info("geocoded location", "query", m.cfg.Location, "name", loc.Name, "lat", loc.Latitude, "lon", loc.Longitude)
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
func (m *weather) forecast(ctx context.Context, q module.Query, p forecastParams) (*Forecast, error) {
	if p.Hours < 0 || p.Days < 0 {
		return nil, errors.New("hours and days must not be negative")
	}
	var f Forecast
	ok, err := q.LoadSnapshot(ctx, &f)
	if err != nil || !ok {
		return nil, err
	}
	tz, err := time.LoadLocation(f.Location.Timezone)
	if err != nil {
		return nil, fmt.Errorf("forecast timezone: %w", err)
	}
	now := q.Now()
	f.Hourly = from(f.Hourly, func(h Hour) bool { return now.Before(h.Time.Add(time.Hour)) }, cmp.Or(p.Hours, 24))
	// The next local midnight, not Date+24h: days are 23 or 25 hours on DST changes.
	f.Daily = from(f.Daily, func(d Day) bool {
		y, m, day := d.Date.In(tz).Date()
		return now.Before(time.Date(y, m, day+1, 0, 0, 0, 0, tz))
	}, cmp.Or(p.Days, 3))
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
