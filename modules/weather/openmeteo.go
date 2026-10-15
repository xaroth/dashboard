package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/xaroth/dashboard/module"
)

// Location is a geocoded place.
type Location struct {
	Name      string  `json:"name"`
	Country   string  `json:"country"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timezone  string  `json:"timezone"`
}

// Forecast is the snapshot one update stores and the forecast query returns.
type Forecast struct {
	Location Location `json:"location"`
	Current  Current  `json:"current"`
	Hourly   []Hour   `json:"hourly"`
	Daily    []Day    `json:"daily"`
}

type Current struct {
	Time          time.Time `json:"time"`
	Temperature   float64   `json:"temperature"`
	Apparent      float64   `json:"apparent"`
	WeatherCode   int       `json:"weatherCode"`
	WindSpeed     float64   `json:"windSpeed"`     // km/h
	WindDirection float64   `json:"windDirection"` // degrees
	Precipitation float64   `json:"precipitation"` // mm
	IsDay         bool      `json:"isDay"`
}

type Hour struct {
	Time                     time.Time `json:"time"`
	Temperature              float64   `json:"temperature"`
	PrecipitationProbability *int      `json:"precipitationProbability"` // %, null where the model has none
	Precipitation            float64   `json:"precipitation"`            // mm
	WeatherCode              int       `json:"weatherCode"`
}

type Day struct {
	Date                        time.Time `json:"date"` // local midnight
	WeatherCode                 int       `json:"weatherCode"`
	TemperatureMax              float64   `json:"temperatureMax"`
	TemperatureMin              float64   `json:"temperatureMin"`
	PrecipitationSum            float64   `json:"precipitationSum"`
	PrecipitationProbabilityMax *int      `json:"precipitationProbabilityMax"` // null where the model has none
	Sunrise                     time.Time `json:"sunrise"`
	Sunset                      time.Time `json:"sunset"`
}

// geocode resolves "Place" or "Place, Country" to coordinates.
func geocode(ctx context.Context, base, query string) (Location, error) {
	name, country, _ := strings.Cut(query, ",")
	name, country = strings.TrimSpace(name), strings.TrimSpace(country)
	v := url.Values{"name": {name}, "count": {"10"}, "language": {"en"}, "format": {"json"}}
	var resp struct {
		Results []struct {
			Name        string  `json:"name"`
			Country     string  `json:"country"`
			CountryCode string  `json:"country_code"`
			Latitude    float64 `json:"latitude"`
			Longitude   float64 `json:"longitude"`
			Timezone    string  `json:"timezone"`
		} `json:"results"`
	}
	if err := getJSON(ctx, base+"?"+v.Encode(), &resp); err != nil {
		return Location{}, fmt.Errorf("geocoding %q: %w", query, err)
	}
	for _, r := range resp.Results {
		if country == "" || strings.EqualFold(country, r.Country) || strings.EqualFold(country, r.CountryCode) {
			return Location{Name: r.Name, Country: r.Country, Latitude: r.Latitude, Longitude: r.Longitude, Timezone: r.Timezone}, nil
		}
	}
	return Location{}, fmt.Errorf("geocoding %q: no match", query)
}

// fetchForecast asks for times as Unix seconds in the location's timezone,
// so daily values cover local days.
func fetchForecast(ctx context.Context, base string, loc Location, days int) (Forecast, error) {
	v := url.Values{
		"latitude":        {strconv.FormatFloat(loc.Latitude, 'f', -1, 64)},
		"longitude":       {strconv.FormatFloat(loc.Longitude, 'f', -1, 64)},
		"current":         {"temperature_2m,apparent_temperature,weather_code,wind_speed_10m,wind_direction_10m,precipitation,is_day"},
		"hourly":          {"temperature_2m,precipitation_probability,precipitation,weather_code"},
		"daily":           {"weather_code,temperature_2m_max,temperature_2m_min,precipitation_sum,precipitation_probability_max,sunrise,sunset"},
		"timezone":        {loc.Timezone},
		"timeformat":      {"unixtime"},
		"forecast_days":   {strconv.Itoa(days)},
		"wind_speed_unit": {"kmh"},
	}
	var r struct {
		Current struct {
			Time          int64   `json:"time"`
			Temperature   float64 `json:"temperature_2m"`
			Apparent      float64 `json:"apparent_temperature"`
			WeatherCode   int     `json:"weather_code"`
			WindSpeed     float64 `json:"wind_speed_10m"`
			WindDirection float64 `json:"wind_direction_10m"`
			Precipitation float64 `json:"precipitation"`
			IsDay         int     `json:"is_day"`
		} `json:"current"`
		Hourly struct {
			Time                     []int64   `json:"time"`
			Temperature              []float64 `json:"temperature_2m"`
			PrecipitationProbability []*int    `json:"precipitation_probability"`
			Precipitation            []float64 `json:"precipitation"`
			WeatherCode              []int     `json:"weather_code"`
		} `json:"hourly"`
		Daily struct {
			Time                        []int64   `json:"time"`
			WeatherCode                 []int     `json:"weather_code"`
			TemperatureMax              []float64 `json:"temperature_2m_max"`
			TemperatureMin              []float64 `json:"temperature_2m_min"`
			PrecipitationSum            []float64 `json:"precipitation_sum"`
			PrecipitationProbabilityMax []*int    `json:"precipitation_probability_max"`
			Sunrise                     []int64   `json:"sunrise"`
			Sunset                      []int64   `json:"sunset"`
		} `json:"daily"`
	}
	if err := getJSON(ctx, base+"?"+v.Encode(), &r); err != nil {
		return Forecast{}, fmt.Errorf("forecast: %w", err)
	}
	tz, err := time.LoadLocation(loc.Timezone)
	if err != nil {
		return Forecast{}, fmt.Errorf("forecast timezone: %w", err)
	}
	at := func(s int64) time.Time { return time.Unix(s, 0).In(tz) }

	h, d := r.Hourly, r.Daily
	if !sameLen(len(h.Time), len(h.Temperature), len(h.PrecipitationProbability), len(h.Precipitation), len(h.WeatherCode)) ||
		!sameLen(len(d.Time), len(d.WeatherCode), len(d.TemperatureMax), len(d.TemperatureMin), len(d.PrecipitationSum), len(d.PrecipitationProbabilityMax), len(d.Sunrise), len(d.Sunset)) {
		return Forecast{}, errors.New("forecast: series of different lengths")
	}
	f := Forecast{
		Location: loc,
		Current: Current{
			Time: at(r.Current.Time), Temperature: r.Current.Temperature, Apparent: r.Current.Apparent,
			WeatherCode: r.Current.WeatherCode, WindSpeed: r.Current.WindSpeed, WindDirection: r.Current.WindDirection,
			Precipitation: r.Current.Precipitation, IsDay: r.Current.IsDay == 1,
		},
		Hourly: make([]Hour, len(h.Time)),
		Daily:  make([]Day, len(d.Time)),
	}
	for i := range h.Time {
		f.Hourly[i] = Hour{Time: at(h.Time[i]), Temperature: h.Temperature[i], PrecipitationProbability: h.PrecipitationProbability[i], Precipitation: h.Precipitation[i], WeatherCode: h.WeatherCode[i]}
	}
	for i := range d.Time {
		f.Daily[i] = Day{
			Date: at(d.Time[i]), WeatherCode: d.WeatherCode[i], TemperatureMax: d.TemperatureMax[i], TemperatureMin: d.TemperatureMin[i],
			PrecipitationSum: d.PrecipitationSum[i], PrecipitationProbabilityMax: d.PrecipitationProbabilityMax[i],
			Sunrise: at(d.Sunrise[i]), Sunset: at(d.Sunset[i]),
		}
	}
	return f, nil
}

func sameLen(n int, rest ...int) bool {
	for _, m := range rest {
		if m != n {
			return false
		}
	}
	return true
}

func getJSON(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := module.HTTPClient(ctx).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}
