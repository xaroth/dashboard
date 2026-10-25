// Package p1 reads the smart meter through a Smartstuff P1 dongle (DSMR-API
// firmware) on the local network.
package p1

import (
	"context"
	"embed"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/xaroth/dashboard/module"
)

//go:embed sql/migrations/*.sql
var migrations embed.FS

// The dongle's Wi-Fi is weak; a reading that takes longer is not coming.
const requestTimeout = 10 * time.Second

type config struct {
	// URL is the dongle's base URL, e.g. http://192.168.5.180. Empty
	// disables the module.
	URL string `yaml:"url"`
}

type p1 struct {
	url string
}

// Module is the p1 registration.
var Module = module.Define[p1, config](
	"p1",
	func() *config { return &config{} },
	module.WithUpdateInterval(time.Minute),
	module.WithStaleAfter(10*time.Minute),
	module.WithMigrations(migrations),
	module.WithQuery("actual", (*p1).actual),
)

func (m *p1) Initialize(_ context.Context, cfg *config) error {
	if cfg.URL != "" {
		u, err := url.Parse(cfg.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("url must be an http or https URL, got %q", cfg.URL)
		}
	}
	m.url = strings.TrimRight(cfg.URL, "/")
	return nil
}

func (m *p1) Enabled(context.Context) bool { return m.url != "" }

func (m *p1) Update(ctx context.Context) error {
	r, err := fetch(ctx, m.url)
	if err != nil {
		return err
	}
	err = New(module.Database(ctx)).StartDay(ctx, StartDayParams{
		Date: r.Date, ReadAt: r.MeasuredAt,
		DeliveredT1: r.DeliveredTariff1, DeliveredT2: r.DeliveredTariff2,
		ReturnedT1: r.ReturnedTariff1, ReturnedT2: r.ReturnedTariff2,
		Gas: r.Gas,
	})
	if err != nil {
		return fmt.Errorf("saving start of day: %w", err)
	}
	return module.SaveSnapshot(ctx, r)
}

// Actual is the latest reading with usage today and yesterday.
type Actual struct {
	MeasuredAt time.Time `json:"measuredAt"`
	Voltage    Phases    `json:"voltage"` // V
	// FlowKw is positive when taking from the net, negative when returning.
	FlowKw    float64 `json:"flowKw"`
	Today     *Usage  `json:"today"`
	Yesterday *Usage  `json:"yesterday"`
}

// Usage is the difference between two readings. From is later than local
// midnight when no reading came in at midnight, e.g. on the first day.
type Usage struct {
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	TakenKwh    float64   `json:"takenKwh"`
	ReturnedKwh float64   `json:"returnedKwh"`
	GasM3       *float64  `json:"gasM3"` // null without a gas meter
}

type actualParams struct{}

// actual returns the latest reading. Today runs from the first reading of
// the meter's current day to the latest; yesterday from the first reading of
// the day before to the first of today. A day without a reading is null.
func (*p1) actual(ctx context.Context, _ actualParams) (*Actual, error) {
	var r Reading
	ok, err := module.LoadSnapshot(ctx, &r)
	if err != nil || !ok {
		return nil, err
	}
	today, err := time.Parse(time.DateOnly, r.Date)
	if err != nil {
		return nil, fmt.Errorf("reading date: %w", err)
	}
	yesterday := today.AddDate(0, 0, -1).Format(time.DateOnly)
	rows, err := New(module.Database(ctx)).ListDays(ctx, yesterday)
	if err != nil {
		return nil, fmt.Errorf("reading days: %w", err)
	}
	days := make(map[string]P1Day, len(rows))
	for _, row := range rows {
		days[row.Date] = row
	}
	a := &Actual{MeasuredAt: r.MeasuredAt, Voltage: r.Voltage, FlowKw: round(r.PowerDelivered - r.PowerReturned)}
	latest := P1Day{
		ReadAt: r.MeasuredAt, DeliveredT1: r.DeliveredTariff1, DeliveredT2: r.DeliveredTariff2,
		ReturnedT1: r.ReturnedTariff1, ReturnedT2: r.ReturnedTariff2, Gas: r.Gas,
	}
	if start, ok := days[r.Date]; ok {
		a.Today = usage(start, latest)
		if prev, ok := days[yesterday]; ok {
			a.Yesterday = usage(prev, start)
		}
	}
	return a, nil
}

// usage returns the difference from a to b, or nil when a counter went
// down: the meter was replaced in between.
func usage(a, b P1Day) *Usage {
	u := &Usage{
		From:        a.ReadAt,
		To:          b.ReadAt,
		TakenKwh:    round(b.DeliveredT1 + b.DeliveredT2 - a.DeliveredT1 - a.DeliveredT2),
		ReturnedKwh: round(b.ReturnedT1 + b.ReturnedT2 - a.ReturnedT1 - a.ReturnedT2),
	}
	if a.Gas != nil && b.Gas != nil {
		g := round(*b.Gas - *a.Gas)
		u.GasM3 = &g
	}
	if u.TakenKwh < 0 || u.ReturnedKwh < 0 || u.GasM3 != nil && *u.GasM3 < 0 {
		return nil
	}
	return u
}

// round drops float noise below the meter's resolution of 1 Wh and 1 dm³.
func round(v float64) float64 {
	r := math.Round(v*1000) / 1000
	if r == 0 {
		return 0 // not -0
	}
	return r
}
