// Package car reads Hyundai, Kia and Genesis vehicles from the Pleos Vehicle
// Data API, with private My Vehicle API keys entered on its admin page. It
// polls often while a car is in use and backs off while all stand still.
package car

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/xaroth/dashboard/module"
)

//go:embed sql/migrations/*.sql
var migrations embed.FS

const (
	// moveThreshold is how far a car must move between polls to count as
	// driven; GPS fixes of a parked car wander by tens of meters.
	moveThreshold = 200.0 // meters
	// odometerThreshold is the odometer increase that counts as driven,
	// for cars without a position.
	odometerThreshold = 1.0 // km
	// keyValidityDays is how long a My Vehicle API key works after it is
	// issued.
	keyValidityDays = 90
)

type config struct {
	APIURL string `yaml:"api_url"`
	// ActiveInterval is the poll interval while a car is in use; idle cars
	// are polled every quarter of their idle time, up to MaxInterval.
	ActiveInterval time.Duration `yaml:"active_interval"`
	MaxInterval    time.Duration `yaml:"max_interval"`
}

func defaultConfig() *config {
	return &config{
		APIURL:         "https://api.pleos.ai",
		ActiveInterval: 15 * time.Minute,
		MaxInterval:    12 * time.Hour,
	}
}

type car struct {
	cfg *config
	// clients are the API clients by client ID. Update only: the admin page
	// changes keys in the database, and Update follows.
	clients map[string]*pleos
	// listed is the keys whose vehicles this process listed.
	listed map[string]bool
}

// Module is the car registration.
var Module = module.Define[car, config](
	"car",
	defaultConfig,
	// The interval before the first update; after that Update sets it.
	module.WithUpdateInterval(15*time.Minute),
	// A parked car is polled every 12 hours.
	module.WithStaleAfter(13*time.Hour),
	module.WithMigrations(migrations),
	module.WithQuery("vehicle", (*car).vehicle),
	module.WithQuery("vehicles", (*car).vehicles),
)

func (m *car) Initialize(_ context.Context, cfg *config) error {
	if cfg.ActiveInterval <= 0 || cfg.MaxInterval < cfg.ActiveInterval {
		return fmt.Errorf("active_interval must be positive and at most max_interval, got %v and %v", cfg.ActiveInterval, cfg.MaxInterval)
	}
	m.cfg = cfg
	m.clients = map[string]*pleos{}
	m.listed = map[string]bool{}
	return nil
}

// Enabled reports whether a key was entered.
func (m *car) Enabled(ctx context.Context) bool {
	n, err := New(module.Database(ctx)).CountKeys(ctx)
	if err != nil {
		module.Logger(ctx).Error("counting keys", "err", err)
	}
	return n > 0
}

// errKey is a problem with one key that retrying will not fix: Pleos
// rejected it, or it lists no vehicles. It is recorded on the key until the
// key is saved again.
type errKey struct{ err error }

func (e errKey) Error() string { return e.err.Error() }
func (e errKey) Unwrap() error { return e.err }

// Update polls the vehicles of every key. Keys fail on their own: the
// vehicles of a failed key keep their last data. The update fails only when
// no key worked, with module.ErrAuth when every key was rejected.
func (m *car) Update(ctx context.Context) error {
	q := New(module.Database(ctx))
	now := module.Clock(ctx)()
	// Vehicles of a key deleted while an update listed them.
	if err := q.DeleteOrphanVehicles(ctx); err != nil {
		return fmt.Errorf("removing vehicles without a key: %w", err)
	}
	keys, err := q.ListKeys(ctx)
	if err != nil {
		return fmt.Errorf("reading keys: %w", err)
	}
	if len(keys) == 0 {
		return errors.New("no API keys; add one on the car admin page")
	}
	rows, err := q.ListVehicles(ctx)
	if err != nil {
		return fmt.Errorf("reading vehicles: %w", err)
	}
	var prev Snapshot
	if _, err := module.LoadSnapshot(ctx, &prev); err != nil {
		return err
	}
	snap := Snapshot{Vehicles: []Vehicle{}}
	var lastActive time.Time
	var failed []error
	rejected := 0
	for _, k := range keys {
		vs, active, err := m.updateKey(ctx, q, k, now)
		if err != nil {
			err = fmt.Errorf("%s key %s: %w", k.Brand, k.ClientID, err)
			failed = append(failed, err)
			snap.Vehicles = append(snap.Vehicles, keyVehicles(prev, rows, k.ClientID)...)
			var ke errKey
			if !errors.As(err, &ke) {
				module.Logger(ctx).Warn("key failed; its vehicles keep their last data", "err", err)
				continue
			}
			rejected++
			if k.AuthError == nil || *k.AuthError != ke.Error() {
				msg := ke.Error()
				if err := q.SaveKeyError(ctx, SaveKeyErrorParams{AuthError: &msg, ClientID: k.ClientID}); err != nil {
					return fmt.Errorf("recording key error: %w", err)
				}
			}
			continue
		}
		if k.AuthError != nil {
			if err := q.SaveKeyError(ctx, SaveKeyErrorParams{ClientID: k.ClientID}); err != nil {
				return fmt.Errorf("clearing key error: %w", err)
			}
		}
		snap.Vehicles = append(snap.Vehicles, vs...)
		lastActive = latest(lastActive, active)
	}
	for id := range m.clients {
		if !slices.ContainsFunc(keys, func(k CarKey) bool { return k.ClientID == id }) {
			delete(m.clients, id)
			delete(m.listed, id)
		}
	}
	switch {
	case len(failed) == len(keys) && rejected == len(keys):
		// Nothing to poll until a key is fixed on the admin page.
		return fmt.Errorf("%w: %w", errors.Join(failed...), module.ErrAuth)
	case len(failed) == len(keys):
		return errors.Join(failed...)
	}
	next := pollInterval(now.Sub(lastActive), m.cfg.ActiveInterval, m.cfg.MaxInterval)
	if err := module.NextUpdate(ctx, next); err != nil {
		return err
	}
	return module.SaveSnapshot(ctx, snap)
}

// updateKey polls the vehicles of key k, listing them first if this process
// did not yet: a key's vehicles are fixed when it is issued. It returns them
// and when the most recently active one was last active. A key that expired
// after it was rejected is not tried again.
func (m *car) updateKey(ctx context.Context, q *Queries, k CarKey, now time.Time) ([]Vehicle, time.Time, error) {
	if k.AuthError != nil && now.After(keyExpiry(k)) {
		return nil, time.Time{}, errKey{fmt.Errorf("expired on %s: %s", keyExpiry(k).Format(time.DateOnly), *k.AuthError)}
	}
	c := m.clients[k.ClientID]
	if c == nil || c.clientSecret != k.ClientSecret || c.brand != k.Brand {
		c = &pleos{url: m.cfg.APIURL, brand: k.Brand, clientID: k.ClientID, clientSecret: k.ClientSecret}
		m.clients[k.ClientID] = c
		m.listed[k.ClientID] = false
	}
	if !m.listed[k.ClientID] {
		vs, err := fetchListing(ctx, c)
		if err != nil {
			return nil, time.Time{}, err
		}
		if err := storeListing(ctx, q, c.clientID, vs); err != nil {
			return nil, time.Time{}, err
		}
		m.listed[k.ClientID] = true
	}
	rows, err := q.ListVehicles(ctx)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("reading vehicles: %w", err)
	}
	var out []Vehicle
	var lastActive time.Time
	for _, row := range rows {
		if row.ClientID != k.ClientID {
			continue
		}
		r, err := c.read(ctx, row.Vin, row.Type)
		if e, ok := asAPIError(err); ok && e.is(codeNoConsent, codeUnknownVIN) {
			m.listed[k.ClientID] = false
		}
		if err != nil {
			return nil, time.Time{}, keyErr(fmt.Errorf("reading %s: %w", row.Vin, err))
		}
		act := observe(row, r, now)
		if act.activeAt != nil {
			lastActive = latest(lastActive, *act.activeAt)
		}
		err = q.SaveActivity(ctx, SaveActivityParams{
			ActiveAt: act.activeAt, Latitude: act.latitude, Longitude: act.longitude, OdometerKm: act.odometerKm, Vin: row.Vin,
		})
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("saving activity of %s: %w", row.Vin, err)
		}
		out = append(out, vehicle(row, k.Brand, r, act.activeAt))
	}
	return out, lastActive, nil
}

// fetchListing returns the vehicles c's key lists; none is a key error.
func fetchListing(ctx context.Context, c *pleos) ([]listedVehicle, error) {
	vs, err := c.vehicles(ctx)
	if err != nil {
		return nil, keyErr(err)
	}
	if len(vs) == 0 {
		return nil, errKey{errors.New("no vehicles are shared with this key; reissue it in Pleos Playground after adding one")}
	}
	return vs, nil
}

// storeListing stores the vehicles key clientID lists and drops the ones it
// no longer does.
func storeListing(ctx context.Context, q *Queries, clientID string, vs []listedVehicle) error {
	vins := make([]string, 0, len(vs))
	for _, v := range vs {
		err := q.SaveListing(ctx, SaveListingParams{
			Vin: v.VIN, ClientID: clientID, Nickname: v.Nickname, Name: v.Name, Sellname: v.Sellname, Type: v.Type,
		})
		if err != nil {
			return fmt.Errorf("saving vehicle %s: %w", v.VIN, err)
		}
		vins = append(vins, v.VIN)
	}
	if err := q.DeleteUnlisted(ctx, DeleteUnlistedParams{ClientID: clientID, Vins: vins}); err != nil {
		return fmt.Errorf("removing unlisted vehicles: %w", err)
	}
	return nil
}

// keyErr marks rejected credentials as a problem of the key.
func keyErr(err error) error {
	if errors.Is(err, module.ErrAuth) {
		return errKey{err}
	}
	return err
}

// keyVehicles returns the vehicles of key clientID as the previous update
// stored them.
func keyVehicles(prev Snapshot, rows []CarVehicle, clientID string) []Vehicle {
	var out []Vehicle
	for _, v := range prev.Vehicles {
		if slices.ContainsFunc(rows, func(r CarVehicle) bool { return r.Vin == v.VIN && r.ClientID == clientID }) {
			out = append(out, v)
		}
	}
	return out
}

// keyExpiry is when key k stops working: 90 days after the day it was
// issued, in the time zone it was entered in.
func keyExpiry(k CarKey) time.Time { return k.IssuedAt.AddDate(0, 0, keyValidityDays) }

// pollInterval is how long to wait after a poll when the most recently used
// car was last active idle ago: a quarter of that, from active to max.
// 15m while in use, 1h after 4h parked, 12h after 2 days.
func pollInterval(idle, active, maxInterval time.Duration) time.Duration {
	return min(max(idle/4, active), maxInterval)
}

// activity is what a poll saw of a car, as stored for the next one.
type activity struct {
	activeAt   *time.Time
	latitude   *float64
	longitude  *float64
	odometerKm *float64
}

// observe compares a reading with the previous poll. The car is active when
// it is on, charging, or has moved since; a first poll counts as active, so
// a new car starts at the short interval. Position and odometer the reading
// lacks are carried over.
func observe(prev CarVehicle, r reading, now time.Time) activity {
	act := activity{activeAt: prev.ActiveAt, latitude: prev.Latitude, longitude: prev.Longitude, odometerKm: prev.OdometerKm}
	moved := false
	if r.location != nil && r.location.Location != nil {
		lat, lon := r.location.Location.Latitude, r.location.Location.Longitude
		if prev.Latitude != nil && prev.Longitude != nil {
			moved = distance(*prev.Latitude, *prev.Longitude, lat, lon) >= moveThreshold
		}
		act.latitude, act.longitude = &lat, &lon
	}
	if km := odometerKm(r.driving); km != nil {
		if prev.OdometerKm != nil && *km-*prev.OdometerKm >= odometerThreshold {
			moved = true
		}
		act.odometerKm = km
	}
	first := prev.ActiveAt == nil && prev.Latitude == nil && prev.OdometerKm == nil
	if first || moved || ignitionOn(r.driving) || charging(r.batteries) {
		act.activeAt = &now
	}
	return act
}

func ignitionOn(d *driving) bool {
	return d != nil && d.Ignition != nil && d.Ignition.Status == "on"
}

func charging(b *batteries) bool {
	return b != nil && b.Charge != nil && b.Charge.Charging != nil && *b.Charge.Charging
}

func odometerKm(d *driving) *float64 {
	if d == nil || d.Odometer == nil || d.Odometer.Value == nil {
		return nil
	}
	return toKm(*d.Odometer.Value, d.Odometer.Unit)
}

// toKm converts a distance to km; nil for an invalid unit.
func toKm(v float64, unit string) *float64 {
	var km float64
	switch unit {
	case "km":
		km = v
	case "mile", "miles":
		km = v * 1.609344
	case "meter":
		km = v / 1000
	case "feet":
		km = v * 0.0003048
	default:
		return nil
	}
	return &km
}

// distance is the great-circle distance between two points, in meters.
func distance(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadius = 6371000.0
	rad := math.Pi / 180
	dlat, dlon := (lat2-lat1)*rad, (lon2-lon1)*rad
	a := math.Sin(dlat/2)*math.Sin(dlat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return 2 * earthRadius * math.Asin(math.Sqrt(a))
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
