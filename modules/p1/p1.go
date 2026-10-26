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
	"github.com/xaroth/dashboard/series"
)

//go:embed sql/migrations/*.sql
var migrations embed.FS

const (
	// The dongle's Wi-Fi is weak; a reading that takes longer is not coming.
	requestTimeout = 10 * time.Second
	// A telegram this far from the clock is stale (the dongle lost the
	// meter and serves its last one) or the meter's clock is wrong.
	maxClockOffset = 10 * time.Minute
)

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
	module.WithQuery("flow", (*p1).flow),
	module.WithSeries("flow", series.Rate),
	module.WithSeries("taken", series.Counter),
	module.WithSeries("returned", series.Counter),
	module.WithSeries("gas", series.Counter),
)

func (m *p1) Initialize(_ context.Context, cfg *config, _ module.Env) error {
	if cfg.URL != "" {
		u, err := url.Parse(cfg.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("url must be an http or https URL, got %q", cfg.URL)
		}
	}
	m.url = strings.TrimRight(cfg.URL, "/")
	return nil
}

func (m *p1) Enabled(context.Context, module.Query) (bool, error) { return m.url != "", nil }

func (m *p1) Update(ctx context.Context, u *module.Update) error {
	r, err := fetch(ctx, u.HTTP, m.url)
	if err != nil {
		return err
	}
	now := u.Now()
	if off := r.MeasuredAt.Sub(now).Abs(); off > maxClockOffset {
		return fmt.Errorf("meter time %s is %s off the clock", r.MeasuredAt.Format(time.RFC3339), off.Round(time.Second))
	}
	db := New(u.DB)
	err = db.StartDay(ctx, StartDayParams{
		Date: r.Date, ReadAt: r.MeasuredAt,
		DeliveredT1: r.DeliveredTariff1, DeliveredT2: r.DeliveredTariff2,
		ReturnedT1: r.ReturnedTariff1, ReturnedT2: r.ReturnedTariff2,
	})
	if err != nil {
		return fmt.Errorf("saving start of day: %w", err)
	}
	if r.Gas != nil {
		err := db.StartGasDay(ctx, StartGasDayParams{Date: r.GasDate, ReadAt: r.gasTime(), Gas: *r.Gas})
		if err != nil {
			return fmt.Errorf("saving start of gas day: %w", err)
		}
	}
	samples := map[string]float64{
		"flow":     r.PowerDelivered - r.PowerReturned,
		"taken":    r.DeliveredTariff1 + r.DeliveredTariff2,
		"returned": r.ReturnedTariff1 + r.ReturnedTariff2,
	}
	for name, v := range samples {
		if err := u.AddSample(name, r.MeasuredAt, v); err != nil {
			return err
		}
	}
	if err := addGas(ctx, u, r); err != nil {
		return err
	}
	return u.SaveSnapshot(r)
}

// addGas adds the gas sample at the gas meter's time. A reading the dongle
// repeats is skipped: it would stretch the last value over the hour, or past
// a gas meter that stopped reporting.
func addGas(ctx context.Context, u *module.Update, r Reading) error {
	if r.Gas == nil {
		return nil
	}
	if r.GasAt != nil {
		var prev Reading
		if _, err := u.LoadSnapshot(ctx, &prev); err != nil {
			return err
		}
		if prev.GasAt != nil && !r.GasAt.After(*prev.GasAt) {
			return nil
		}
	}
	return u.AddSample("gas", r.gasTime(), *r.Gas)
}

// flow returns the average flow in kW per step over the last hours,
// positive when taking from the net.
func (*p1) flow(ctx context.Context, q module.Query, p series.HistoryParams) ([]series.Point, error) {
	from, to, step, err := p.Range(q.Now())
	if err != nil {
		return nil, err
	}
	return q.QuerySeries(ctx, "flow", from, to, step)
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
// midnight when no reading came in at midnight, e.g. on the first day. Gas
// has its own times: the gas meter reports less often than the electricity
// meter.
type Usage struct {
	From        time.Time  `json:"from"`
	To          time.Time  `json:"to"`
	TakenKwh    float64    `json:"takenKwh"`
	ReturnedKwh float64    `json:"returnedKwh"`
	GasM3       *float64   `json:"gasM3"` // null without a gas meter
	GasFrom     *time.Time `json:"gasFrom"`
	GasTo       *time.Time `json:"gasTo"`
}

type actualParams struct{}

// actual returns the latest reading. Today runs from the first reading of
// the meter's current day to the latest; yesterday from the first reading of
// the day before to the first of today. A day without a reading is null.
// Gas days are the gas meter's: today's gas is null until it has read on the
// meter's current day.
func (*p1) actual(ctx context.Context, q module.Query, _ actualParams) (*Actual, error) {
	var r Reading
	ok, err := q.LoadSnapshot(ctx, &r)
	if err != nil || !ok {
		return nil, err
	}
	today, err := time.Parse(time.DateOnly, r.Date)
	if err != nil {
		return nil, fmt.Errorf("reading date: %w", err)
	}
	yesterday := today.AddDate(0, 0, -1).Format(time.DateOnly)
	db := New(q.DB)
	rows, err := db.ListDays(ctx, yesterday)
	if err != nil {
		return nil, fmt.Errorf("reading days: %w", err)
	}
	days := make(map[string]P1Day, len(rows))
	for _, row := range rows {
		days[row.Date] = row
	}
	gasRows, err := db.ListGasDays(ctx, yesterday)
	if err != nil {
		return nil, fmt.Errorf("reading gas days: %w", err)
	}
	gasDays := make(map[string]P1GasDay, len(gasRows))
	for _, row := range gasRows {
		gasDays[row.Date] = row
	}
	a := &Actual{MeasuredAt: r.MeasuredAt, Voltage: r.Voltage, FlowKw: round(r.PowerDelivered - r.PowerReturned)}
	latest := P1Day{
		ReadAt: r.MeasuredAt, DeliveredT1: r.DeliveredTariff1, DeliveredT2: r.DeliveredTariff2,
		ReturnedT1: r.ReturnedTariff1, ReturnedT2: r.ReturnedTariff2,
	}
	if start, ok := days[r.Date]; ok {
		a.Today = usage(start, latest)
		gasStart, ok := gasDays[r.Date]
		if ok && r.Gas != nil {
			a.Today.addGas(gasStart, P1GasDay{ReadAt: r.gasTime(), Gas: *r.Gas})
		}
		if prev, ok := days[yesterday]; ok {
			a.Yesterday = usage(prev, start)
			if gasPrev, ok := gasDays[yesterday]; ok && gasStart.Date != "" {
				a.Yesterday.addGas(gasPrev, gasStart)
			}
		}
	}
	return a, nil
}

// usage returns the electricity used from a to b, or nil when a counter
// went down: the meter was replaced in between.
func usage(a, b P1Day) *Usage {
	u := &Usage{
		From:        a.ReadAt,
		To:          b.ReadAt,
		TakenKwh:    round(b.DeliveredT1 + b.DeliveredT2 - a.DeliveredT1 - a.DeliveredT2),
		ReturnedKwh: round(b.ReturnedT1 + b.ReturnedT2 - a.ReturnedT1 - a.ReturnedT2),
	}
	if u.TakenKwh < 0 || u.ReturnedKwh < 0 {
		return nil
	}
	return u
}

// addGas sets the gas used from a to b. Gas stays null when its counter
// went down; the gas meter is a separate device.
func (u *Usage) addGas(a, b P1GasDay) {
	if u == nil || b.Gas < a.Gas {
		return
	}
	g := round(b.Gas - a.Gas)
	u.GasM3, u.GasFrom, u.GasTo = &g, &a.ReadAt, &b.ReadAt
}

// round drops float noise below the meter's resolution of 1 Wh and 1 dm³.
func round(v float64) float64 {
	r := math.Round(v*1000) / 1000
	if r == 0 {
		return 0 // not -0
	}
	return r
}
