// Package device implements the TRMNL BYOS protocol for the configured devices.
package device

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/xaroth/dashboard/screen"
)

//go:embed sql/migrations/*.sql
var migrations embed.FS

// Migrator applies an owner's goose migrations, the .sql files in dir of
// fsys; *storage.DB in production.
type Migrator interface {
	Migrate(ctx context.Context, owner string, fsys fs.FS, dir string) error
}

// Config is a configured device.
type Config struct {
	Name     string
	MAC      string // normalized: upper case with colons
	Rotation int
	Pages    []string
	Power    Power
	// Params overrides query params by "page.key".
	Params map[string]map[string]any
}

// Power is how a device's power source is decided.
type Power string

const (
	// PowerAuto follows the USB-Connected header and assumes battery without it.
	PowerAuto    Power = "auto"
	PowerUSB     Power = "usb"
	PowerBattery Power = "battery"
)

// Pipeline renders pages; *render.Pipeline in production.
type Pipeline interface {
	Render(ctx context.Context, t screen.Target, page string) (screen.Image, error)
	// KnownModel reports whether pages can be rendered for a Model header.
	KnownModel(model string) bool
}

// Status is what the service knows about a device right now.
type Status struct {
	Config
	Model string
	Wake  *screen.Wake
	Page  string
}

// Pending is a MAC that asked for setup but is not configured.
type Pending struct {
	MAC  string
	Seen time.Time
}

// Devices holds the configured devices. API keys are stored in the
// database and cached here; current page, last wake and last image are in
// memory and lost on restart.
type Devices struct {
	pipeline  Pipeline
	store     *Store
	queries   *Queries
	publicURL string
	schedule  Schedule
	log       *slog.Logger
	now       func() time.Time
	simulated bool

	mu      sync.Mutex
	byMAC   map[string]*state
	byName  map[string]*state
	pending map[string]time.Time
}

type state struct {
	cfg      Config
	model    string
	wake     *screen.Wake
	page     int
	filename string
	key      string // API key, cached: keys never change once created
}

// maxPending caps the unknown MACs remembered, so setup calls cannot grow it without bound.
const maxPending = 32

// DefaultModel is assumed for a device that has not reported its model yet.
const DefaultModel = "reterminal_e1001"

// Options are the dependencies of Devices.
type Options struct {
	Devices  []Config
	Pipeline Pipeline
	Store    *Store
	// DB stores the API keys; Migrator creates its tables.
	DB       *sql.DB
	Migrator Migrator
	// PublicURL is the base of image_url, as the devices reach this service.
	PublicURL string
	Schedule  Schedule
	Log       *slog.Logger
	Now       func() time.Time
	// Simulated keeps wakes out of the device metrics, for the simulator.
	Simulated bool
}

// Load validates the options, runs the device migrations and returns the
// devices. Invalid options or a failing migration are an error.
func Load(ctx context.Context, o Options) (*Devices, error) {
	if err := validate(o); err != nil {
		return nil, err
	}
	if err := o.Migrator.Migrate(ctx, "device", migrations, "sql/migrations"); err != nil {
		return nil, fmt.Errorf("device migrations: %w", err)
	}
	d := &Devices{
		pipeline:  o.Pipeline,
		store:     o.Store,
		queries:   New(o.DB),
		publicURL: strings.TrimSuffix(o.PublicURL, "/"),
		schedule:  o.Schedule,
		log:       o.Log,
		now:       o.Now,
		simulated: o.Simulated,
		byMAC:     map[string]*state{},
		byName:    map[string]*state{},
		pending:   map[string]time.Time{},
	}
	for _, c := range o.Devices {
		s := &state{cfg: c}
		d.byMAC[c.MAC] = s
		d.byName[c.Name] = s
	}
	return d, nil
}

// validate rejects options the protocol cannot serve: a zero interval loops
// forever in Schedule.next, a device without pages divides by zero.
func validate(o Options) error {
	intervals := []struct {
		name string
		d    time.Duration
	}{{"battery", o.Schedule.Battery}, {"usb", o.Schedule.USB}, {"detail", o.Schedule.Detail}}
	for _, i := range intervals {
		if i.d <= 0 {
			return fmt.Errorf("schedule: %s interval must be positive", i.name)
		}
	}
	for _, c := range o.Devices {
		if len(c.Pages) == 0 {
			return fmt.Errorf("device %s: pages is empty", c.Name)
		}
		switch c.Power {
		case "", PowerAuto, PowerUSB, PowerBattery:
		default:
			return fmt.Errorf("device %s: unknown power %q", c.Name, c.Power)
		}
	}
	return nil
}

// Target is how the named device is rendered: its reported model and wake.
func (d *Devices) Target(name string) (screen.Target, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, ok := d.byName[name]
	if !ok {
		return screen.Target{}, false
	}
	return s.target(), true
}

// Statuses returns every configured device, sorted by name.
func (d *Devices) Statuses() []Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Status, 0, len(d.byName))
	for _, s := range d.byName {
		out = append(out, Status{
			Config: s.cfg,
			Model:  cmp.Or(s.model, DefaultModel),
			Wake:   s.wake,
			Page:   s.cfg.Pages[s.page],
		})
	}
	slices.SortFunc(out, func(a, b Status) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// Pending returns MACs that asked for setup but are not configured, sorted.
func (d *Devices) Pending() []Pending {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Pending, 0, len(d.pending))
	for mac, seen := range d.pending {
		out = append(out, Pending{MAC: mac, Seen: seen})
	}
	slices.SortFunc(out, func(a, b Pending) int { return cmp.Compare(a.MAC, b.MAC) })
	return out
}

func (s *state) target() screen.Target {
	return screen.Target{Name: s.cfg.Name, Model: cmp.Or(s.model, DefaultModel), Rotation: s.cfg.Rotation, Wake: s.wake, Params: s.cfg.Params}
}

func newAPIKey() string { return rand.Text() }
