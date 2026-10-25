package p1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/xaroth/dashboard/module"
)

// Reading is one telegram from the meter, the snapshot an update stores.
type Reading struct {
	MeasuredAt time.Time `json:"measuredAt"`
	// Date is the meter's local date, YYYY-MM-DD.
	Date             string   `json:"date"`
	PowerDelivered   float64  `json:"powerDelivered"` // kW taken from the net
	PowerReturned    float64  `json:"powerReturned"`  // kW returned to the net
	Voltage          Phases   `json:"voltage"`
	DeliveredTariff1 float64  `json:"deliveredTariff1"` // kWh
	DeliveredTariff2 float64  `json:"deliveredTariff2"`
	ReturnedTariff1  float64  `json:"returnedTariff1"`
	ReturnedTariff2  float64  `json:"returnedTariff2"`
	Gas              *float64 `json:"gas"` // m³, null without a gas meter
	// GasAt is when the gas meter read Gas; DSMR 4 gas meters report hourly.
	GasAt *time.Time `json:"gasAt"`
}

// Phases holds a value per phase; null for phases the meter does not have.
type Phases struct {
	L1 *float64 `json:"l1"`
	L2 *float64 `json:"l2"`
	L3 *float64 `json:"l3"`
}

type value[T any] struct {
	Value T `json:"value"`
}

// actual is the DSMR-API /api/v2/sm/actual response. Fields the meter does
// not report are missing.
type actual struct {
	Timestamp        *value[string]  `json:"timestamp"`
	DeliveredTariff1 *value[float64] `json:"energy_delivered_tariff1"`
	DeliveredTariff2 *value[float64] `json:"energy_delivered_tariff2"`
	ReturnedTariff1  *value[float64] `json:"energy_returned_tariff1"`
	ReturnedTariff2  *value[float64] `json:"energy_returned_tariff2"`
	PowerDelivered   *value[float64] `json:"power_delivered"`
	PowerReturned    *value[float64] `json:"power_returned"`
	VoltageL1        *value[float64] `json:"voltage_l1"`
	VoltageL2        *value[float64] `json:"voltage_l2"`
	VoltageL3        *value[float64] `json:"voltage_l3"`
	GasDelivered     *value[float64] `json:"gas_delivered"`
	GasTimestamp     *value[string]  `json:"gas_delivered_timestamp"`
}

func fetch(ctx context.Context, base string) (Reading, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v2/sm/actual", nil)
	if err != nil {
		return Reading{}, err
	}
	resp, err := module.HTTPClient(ctx).Do(req)
	if err != nil {
		return Reading{}, fmt.Errorf("requesting actual: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return Reading{}, fmt.Errorf("requesting actual: %s: %w", resp.Status, module.ErrAuth)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Reading{}, fmt.Errorf("requesting actual: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var a actual
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		return Reading{}, fmt.Errorf("decoding actual: %w", err)
	}
	return a.reading()
}

func (a actual) reading() (Reading, error) {
	for _, f := range []struct {
		name string
		v    *value[float64]
	}{
		{"energy_delivered_tariff1", a.DeliveredTariff1}, {"energy_delivered_tariff2", a.DeliveredTariff2},
		{"energy_returned_tariff1", a.ReturnedTariff1}, {"energy_returned_tariff2", a.ReturnedTariff2},
		{"power_delivered", a.PowerDelivered}, {"power_returned", a.PowerReturned},
	} {
		if f.v == nil {
			return Reading{}, fmt.Errorf("actual has no %s", f.name)
		}
	}
	if a.DeliveredTariff1.Value == 0 && a.DeliveredTariff2.Value == 0 && a.ReturnedTariff1.Value == 0 && a.ReturnedTariff2.Value == 0 {
		return Reading{}, errors.New("actual has zero meter readings")
	}
	if a.Timestamp == nil {
		return Reading{}, errors.New("actual has no timestamp")
	}
	at, err := parseTimestamp(a.Timestamp.Value)
	if err != nil {
		return Reading{}, err
	}
	var gasAt *time.Time
	if a.GasDelivered != nil && a.GasTimestamp != nil {
		t, err := parseTimestamp(a.GasTimestamp.Value)
		if err != nil {
			return Reading{}, fmt.Errorf("gas %w", err)
		}
		t = t.UTC()
		gasAt = &t
	}
	return Reading{
		MeasuredAt:       at.UTC(),
		Date:             at.Format(time.DateOnly),
		PowerDelivered:   a.PowerDelivered.Value,
		PowerReturned:    a.PowerReturned.Value,
		Voltage:          Phases{L1: ptr(a.VoltageL1), L2: ptr(a.VoltageL2), L3: ptr(a.VoltageL3)},
		DeliveredTariff1: a.DeliveredTariff1.Value,
		DeliveredTariff2: a.DeliveredTariff2.Value,
		ReturnedTariff1:  a.ReturnedTariff1.Value,
		ReturnedTariff2:  a.ReturnedTariff2.Value,
		Gas:              ptr(a.GasDelivered),
		GasAt:            gasAt,
	}, nil
}

var (
	winter = time.FixedZone("CET", 3600)
	summer = time.FixedZone("CEST", 2*3600)
)

// parseTimestamp parses a DSMR timestamp, YYMMDDhhmmss in local time with W
// (winter) or S (summer, DST) appended. Dutch meters use CET and CEST.
func parseTimestamp(s string) (time.Time, error) {
	if len(s) != 13 {
		return time.Time{}, fmt.Errorf("timestamp %q: want YYMMDDhhmmss and W or S", s)
	}
	var zone *time.Location
	switch s[12] {
	case 'W':
		zone = winter
	case 'S':
		zone = summer
	default:
		return time.Time{}, fmt.Errorf("timestamp %q: want W or S at the end", s)
	}
	t, err := time.ParseInLocation("060102150405", s[:12], zone)
	if err != nil {
		return time.Time{}, fmt.Errorf("timestamp %q: %w", s, err)
	}
	return t, nil
}

func ptr(v *value[float64]) *float64 {
	if v == nil {
		return nil
	}
	return &v.Value
}
