// Package solar reads Hoymiles micro inverters through the DTU's Modbus TCP
// interface (DTU-Pro, DTU-Pro-S).
package solar

import (
	"cmp"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/series"
)

const (
	unitID = 1
	// Each inverter input is a block of 20 registers, 40 apart.
	inputBase   = 0x1000
	inputStride = 40
	inputRegs   = 20
	// The DTU-Pro-S takes up to 99 inverter inputs.
	maxInputs = 99
	dtuSerial = 0x2000
	// Bounds the whole read: the DTU hangs at times.
	readTimeout = 30 * time.Second
	staleAfter  = 30 * time.Minute
)

type config struct {
	// Host is the DTU's address. Empty disables the module.
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	// Unused lists inputs without a panel, as serial:port.
	Unused []string `yaml:"unused"`
	// NightInterval is the update interval while no input produces.
	NightInterval time.Duration `yaml:"night_interval"`
}

func defaultConfig() *config {
	return &config{Port: 502, NightInterval: 10 * time.Minute}
}

type input struct {
	serial string
	port   int
}

type solar struct {
	cfg    *config
	unused map[input]bool
}

// Module is the solar registration.
var Module = module.Define[solar, config](
	"solar",
	defaultConfig,
	// Frequent polling is reported to crash DTUs; once a minute is the limit.
	module.WithUpdateInterval(time.Minute),
	module.WithStaleAfter(staleAfter),
	module.WithQuery("production", (*solar).production),
	module.WithQuery("power", (*solar).power),
	module.WithSeries("power", series.Rate),
	module.WithSeries("total", series.Counter),
)

func (m *solar) Initialize(_ context.Context, cfg *config) error {
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("port must be 1 to 65535, got %d", cfg.Port)
	}
	if cfg.NightInterval < time.Minute {
		return fmt.Errorf("night_interval must be at least 1m, got %s", cfg.NightInterval)
	}
	if cfg.NightInterval >= staleAfter {
		return fmt.Errorf("night_interval must be under %s, got %s", staleAfter, cfg.NightInterval)
	}
	m.unused = make(map[input]bool, len(cfg.Unused))
	for _, u := range cfg.Unused {
		serial, port, ok := strings.Cut(u, ":")
		n, err := strconv.Atoi(port)
		if !ok || serial == "" || err != nil {
			return fmt.Errorf("unused: want serial:port, got %q", u)
		}
		m.unused[input{serial, n}] = true
	}
	m.cfg = cfg
	return nil
}

func (m *solar) Enabled(context.Context) (bool, error) { return m.cfg.Host != "", nil }

// Plant is the snapshot one update stores.
type Plant struct {
	DTU    string  `json:"dtu"` // serial
	Inputs []Input `json:"inputs"`
}

// Input is one inverter input with its panel.
type Input struct {
	Inverter      string  `json:"inverter"` // serial
	Port          int     `json:"port"`
	Voltage       float64 `json:"voltage"` // V
	Current       float64 `json:"current"` // A
	Power         float64 `json:"power"`   // W
	GridVoltage   float64 `json:"gridVoltage"`
	GridFrequency float64 `json:"gridFrequency"` // Hz
	TodayWh       int     `json:"todayWh"`
	TotalWh       int64   `json:"totalWh"`
	Temperature   float64 `json:"temperature"` // °C
	Status        int     `json:"status"`
	Alarm         int     `json:"alarm"`
	AlarmCount    int     `json:"alarmCount"`
	// Link drops to false at times while the counters keep updating.
	Link bool `json:"link"`
}

func (m *solar) Update(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	p, err := m.read(ctx)
	if err != nil {
		return err
	}
	producing := false
	var power float64
	var total int64
	for _, in := range p.Inputs {
		producing = producing || in.Power > 0
		power += in.Power
		total += in.TotalWh
	}
	// The DTU reports no measurement time.
	now := module.Clock(ctx)()
	if err := module.Series(ctx).Add("power", now, power); err != nil {
		return err
	}
	if err := module.Series(ctx).Add("total", now, float64(total)); err != nil {
		return err
	}
	if !producing {
		if err := module.NextUpdate(ctx, m.cfg.NightInterval); err != nil {
			return err
		}
	}
	return module.SaveSnapshot(ctx, p)
}

func (m *solar) read(ctx context.Context) (Plant, error) {
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))
	c, err := dialModbus(ctx, addr, unitID)
	if err != nil {
		return Plant{}, fmt.Errorf("connecting to DTU: %w", err)
	}
	defer c.Close()
	d, err := c.readHolding(dtuSerial, 3)
	if err != nil {
		return Plant{}, fmt.Errorf("reading DTU serial: %w", err)
	}
	p := Plant{DTU: hex.EncodeToString(d), Inputs: []Input{}}
	for i := range maxInputs {
		d, err := c.readHolding(uint16(inputBase+i*inputStride), inputRegs)
		if err != nil {
			return Plant{}, fmt.Errorf("reading input %d: %w", i, err)
		}
		in, ok := decodeInput(d)
		if !ok {
			break
		}
		if !m.unused[input{in.Inverter, in.Port}] {
			p.Inputs = append(p.Inputs, in)
		}
	}
	if len(p.Inputs) == 0 {
		return Plant{}, errors.New("DTU reports no inverter inputs")
	}
	return p, nil
}

// decodeInput decodes an input's registers; false after the last input.
func decodeInput(d []byte) (Input, bool) {
	serial := hex.EncodeToString(d[1:7])
	if serial == "000000000000" {
		return Input{}, false
	}
	u16 := func(off int) int { return int(binary.BigEndian.Uint16(d[off:])) }
	return Input{
		Inverter:      serial,
		Port:          int(d[7]),
		Voltage:       float64(u16(8)) / 10,
		Current:       float64(u16(10)) / 100,
		GridVoltage:   float64(u16(12)) / 10,
		GridFrequency: float64(u16(14)) / 100,
		Power:         float64(u16(16)) / 10,
		TodayWh:       u16(18),
		TotalWh:       int64(binary.BigEndian.Uint32(d[20:])),
		Temperature:   float64(int16(binary.BigEndian.Uint16(d[24:]))) / 10,
		Status:        u16(26),
		Alarm:         u16(28),
		AlarmCount:    u16(30),
		Link:          d[32] != 0,
	}, true
}

// Production is the plant's current output.
type Production struct {
	PowerW float64 `json:"powerW"`
	Panels []Panel `json:"panels"`
}

// Panel is the output of one inverter input.
type Panel struct {
	Inverter string  `json:"inverter"`
	Port     int     `json:"port"`
	PowerW   float64 `json:"powerW"`
}

type productionParams struct{}

// production returns the total and per-panel power of the last update.
// Totals count every input, whatever its link.
func (*solar) production(ctx context.Context, _ productionParams) (*Production, error) {
	var p Plant
	ok, err := module.LoadSnapshot(ctx, &p)
	if err != nil || !ok {
		return nil, err
	}
	out := &Production{Panels: make([]Panel, 0, len(p.Inputs))}
	for _, in := range p.Inputs {
		out.PowerW += in.Power
		out.Panels = append(out.Panels, Panel{Inverter: in.Inverter, Port: in.Port, PowerW: in.Power})
	}
	out.PowerW = math.Round(out.PowerW*10) / 10
	return out, nil
}

type powerParams struct {
	// Hours back from now, 1 to 168.
	Hours int `json:"hours"`
	// StepMinutes is the bucket size; it divides a day.
	StepMinutes int `json:"stepMinutes"`
}

// power returns the plant's average power in W per step over the last hours.
func (*solar) power(ctx context.Context, p powerParams) ([]series.Point, error) {
	hours, step := cmp.Or(p.Hours, 24), cmp.Or(p.StepMinutes, 15)
	if hours < 1 || hours > 168 {
		return nil, fmt.Errorf("hours must be 1 to 168, got %d", hours)
	}
	if step < 1 || step > 1440 {
		return nil, fmt.Errorf("stepMinutes must be 1 to 1440, got %d", step)
	}
	now := module.Clock(ctx)()
	// Up to a second past now: the bucket holding now has the latest sample.
	return module.Series(ctx).Query(ctx, "power", now.Add(-time.Duration(hours)*time.Hour), now.Add(time.Second), time.Duration(step)*time.Minute)
}
