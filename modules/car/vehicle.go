package car

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/xaroth/dashboard/module"
)

// Snapshot is what one update stores.
type Snapshot struct {
	Vehicles []Vehicle `json:"vehicles"`
}

// Vehicle is a car as pages see it. A field is null when the car does not
// report it.
type Vehicle struct {
	VIN string `json:"vin"`
	// ShortName is the name set on the admin page; null until set.
	ShortName *string `json:"shortName"`
	// Brand is hyundai, kia or genesis.
	Brand string `json:"brand"`
	// Nickname is the one set in the brand's app; often empty.
	Nickname string `json:"nickname"`
	// KeyExpiresAt is when the API key that reads it expires.
	KeyExpiresAt time.Time `json:"keyExpiresAt"`
	// Model is the sales name, e.g. KONA Electric; ModelCode the model code.
	Model     string `json:"model"`
	ModelCode string `json:"modelCode"`
	// Type is GN (combustion), EV, HV (hybrid), PE (plug-in hybrid), FV
	// (fuel cell) or UNKNOWN.
	Type string `json:"type"`
	// UpdatedAt is when the car last sent data.
	UpdatedAt *time.Time `json:"updatedAt"`
	// ActiveAt is the last poll that found it on, charging or moved.
	ActiveAt   *time.Time `json:"activeAt"`
	IgnitionOn *bool      `json:"ignitionOn"`
	OdometerKm *float64   `json:"odometerKm"`
	Location   *Position  `json:"location"`
	// StateOfCharge is the high-voltage battery level, in percent.
	StateOfCharge *float64 `json:"stateOfCharge"`
	// FuelLevel is the fuel tank level, in percent.
	FuelLevel *float64 `json:"fuelLevel"`
	// Ranges is the distance to empty per energy source.
	Ranges []Range `json:"ranges"`
	Charge *Charge `json:"charge"`
	Locked *bool   `json:"locked"`
	// Open lists what is open: frontLeft, frontRight, rearLeft, rearRight,
	// hood, trunk, sunroof.
	Open      []string `json:"open"`
	ClimateOn *bool    `json:"climateOn"`
	// OutsideTemperature is in °C.
	OutsideTemperature *float64 `json:"outsideTemperature"`
	// Warnings are Pleos warning names, e.g. washFluid, tirePressureLow.
	Warnings []string `json:"warnings"`
}

type Position struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type Range struct {
	// Type is ICE, EV, HEV, PHEV or FCEV.
	Type string  `json:"type"`
	Km   float64 `json:"km"`
}

type Charge struct {
	PluggedIn bool `json:"pluggedIn"`
	Charging  bool `json:"charging"`
	// Status is the Pleos charge status, e.g. notCharging, fastCharging.
	Status string `json:"status"`
	// TargetAC and TargetDC are the charge limits, in percent.
	TargetAC         *int `json:"targetAC"`
	TargetDC         *int `json:"targetDC"`
	RemainingMinutes *int `json:"remainingMinutes"`
}

func vehicle(row CarVehicle, brand string, r reading, activeAt *time.Time) Vehicle {
	v := Vehicle{
		VIN: row.Vin, Brand: brand, Nickname: row.Nickname, Model: row.Sellname, ModelCode: row.Name, Type: row.Type,
		ActiveAt: activeAt, Ranges: []Range{}, Open: []string{}, Warnings: []string{},
	}
	var updated time.Time
	if d := r.driving; d != nil {
		updated = latest(updated, d.Timestamp.Time)
		if d.Ignition != nil && d.Ignition.Status != "invalid" {
			on := d.Ignition.Status == "on"
			v.IgnitionOn = &on
		}
		v.OdometerKm = odometerKm(d)
	}
	if l := r.location; l != nil && l.Location != nil {
		updated = latest(updated, l.Timestamp.Time)
		v.Location = &Position{Latitude: l.Location.Latitude, Longitude: l.Location.Longitude}
	}
	if p := r.powertrains; p != nil {
		updated = latest(updated, p.Timestamp.Time)
		// EVs report 0 rather than the documented null.
		if p.Engine != nil && row.Type != "EV" {
			v.FuelLevel = p.Engine.FuelLevel
		}
		for _, b := range p.Batteries {
			if b.Type == "main" {
				v.StateOfCharge = b.RemainRatio
			}
		}
		for _, d := range p.DistanceToEmpties {
			n, err := strconv.ParseFloat(d.Value, 64)
			km := toKm(n, d.Unit)
			if err != nil || km == nil || d.Type == "invalid" {
				continue
			}
			v.Ranges = append(v.Ranges, Range{Type: d.Type, Km: *km})
		}
	}
	if b := r.batteries; b != nil && b.Charge != nil {
		updated = latest(updated, b.Timestamp.Time)
		c := b.Charge
		if c.StateOfCharge != nil {
			v.StateOfCharge = c.StateOfCharge
		}
		v.Charge = &Charge{PluggedIn: c.Plugin == "connected", Charging: charging(b), Status: c.Status, RemainingMinutes: c.RemainTime}
		if t := c.TargetStateOfCharge; t != nil {
			v.Charge.TargetAC, v.Charge.TargetDC = t.Standard, t.Quick
		}
	}
	if s := r.status; s != nil {
		updated = latest(updated, s.Timestamp.Time)
		v.Locked = locked(s.Doors)
		rows := [][2]string{{"frontLeft", "frontRight"}, {"rearLeft", "rearRight"}}
		for i, d := range s.Doors {
			if i >= len(rows) {
				break
			}
			if d.Left == "open" {
				v.Open = append(v.Open, rows[i][0])
			}
			if d.Right == "open" {
				v.Open = append(v.Open, rows[i][1])
			}
		}
		for _, part := range [][2]string{{"hood", s.Hood}, {"trunk", s.Trunk}, {"sunroof", s.Sunroof}} {
			if part[1] == "open" {
				v.Open = append(v.Open, part[0])
			}
		}
		if s.ClimateControl != nil && s.ClimateControl.Status != "" {
			on := s.ClimateControl.Status == "on"
			v.ClimateOn = &on
		}
		if t := s.OutsideTemperature; t != nil && t.Value != nil {
			c := *t.Value
			if t.Unit == "F" {
				c = (c - 32) * 5 / 9
			}
			v.OutsideTemperature = &c
		}
		if s.Warnings != nil {
			v.Warnings = s.Warnings
		}
	}
	if !updated.IsZero() {
		v.UpdatedAt = &updated
	}
	return v
}

// locked is whether every door reports locked; nil when none reports.
func locked(doors []sides) *bool {
	known, all := false, true
	for _, d := range doors {
		for _, st := range []string{d.Left, d.Right} {
			if st == "" || st == "invalid" {
				continue
			}
			known = true
			all = all && st == "locked"
		}
	}
	if !known {
		return nil
	}
	return &all
}

type vehicleParams struct {
	// ID is the vehicle's short name or VIN; empty is the first vehicle.
	ID string `json:"id"`
}

// vehicle returns the stored state of one vehicle, or null before the first
// update.
func (m *car) vehicle(ctx context.Context, q module.Query, p vehicleParams) (*Vehicle, error) {
	vs, err := m.vehicles(ctx, q, struct{}{})
	if err != nil || len(vs) == 0 {
		return nil, err
	}
	if p.ID == "" {
		return &vs[0], nil
	}
	for _, v := range vs {
		if strings.EqualFold(v.VIN, p.ID) || (v.ShortName != nil && strings.EqualFold(*v.ShortName, p.ID)) {
			return &v, nil
		}
	}
	return nil, fmt.Errorf("no vehicle %q", p.ID)
}

// vehicles returns the stored state of every vehicle, by short name, then
// VIN. Short names and key expiry come from the admin page's current
// settings, not the last update.
func (*car) vehicles(ctx context.Context, q module.Query, _ struct{}) ([]Vehicle, error) {
	var snap Snapshot
	ok, err := q.LoadSnapshot(ctx, &snap)
	if err != nil || !ok {
		return []Vehicle{}, err
	}
	db := New(q.DB)
	rows, err := db.ListVehicles(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading vehicles: %w", err)
	}
	keys, err := db.ListKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading keys: %w", err)
	}
	expires := map[string]time.Time{}
	for _, k := range keys {
		expires[k.ClientID] = keyExpiry(k)
	}
	byVIN := map[string]Vehicle{}
	for _, v := range snap.Vehicles {
		byVIN[v.VIN] = v
	}
	out := []Vehicle{}
	for _, row := range rows {
		v, ok := byVIN[row.Vin]
		if !ok {
			continue // listed since the last update
		}
		v.ShortName, v.KeyExpiresAt = row.ShortName, expires[row.ClientID]
		out = append(out, v)
	}
	slices.SortStableFunc(out, func(a, b Vehicle) int {
		switch {
		case a.ShortName != nil && b.ShortName != nil:
			return strings.Compare(*a.ShortName, *b.ShortName)
		case a.ShortName != nil:
			return -1
		case b.ShortName != nil:
			return 1
		}
		return strings.Compare(a.VIN, b.VIN)
	})
	return out, nil
}
