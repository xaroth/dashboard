// Package device implements the TRMNL BYOS protocol for the configured devices.
package device

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/xaroth/dashboard/service/render"
)

// Config is a configured device.
type Config struct {
	Name     string
	MAC      string // normalized: upper case with colons
	Rotation int
	Pages    []string
}

// Pipeline renders pages; *render.Pipeline in production.
type Pipeline interface {
	Render(ctx context.Context, t render.Target, page string) (render.Image, error)
}

// Status is what the service knows about a device right now.
type Status struct {
	Config
	Model    string
	Wake     *render.Wake
	Page     string
	Filename string
}

// Pending is a MAC that asked for setup but is not configured.
type Pending struct {
	MAC  string
	Seen time.Time
}

// Devices holds the configured devices and their in-memory state: API keys,
// current page, last wake and last image. State is lost on restart.
type Devices struct {
	pipeline  Pipeline
	store     *Store
	publicURL string
	log       *slog.Logger
	now       func() time.Time

	mu    sync.Mutex
	byMAC map[string]*state
	// Simulator requests get their own state, so they never touch a real
	// device's key, page or wake.
	simByMAC map[string]*state
	byName   map[string]*state
	pending  map[string]time.Time
}

type state struct {
	cfg      Config
	apiKey   string
	model    string
	wake     *render.Wake
	page     int
	filename string
}

// SimulatorHeader marks a request from the admin simulator.
const SimulatorHeader = "X-Dashboard-Simulator"

// maxPending caps the unknown MACs remembered, so setup calls cannot grow it without bound.
const maxPending = 32

// DefaultModel is assumed for a device that has not reported its model yet.
const DefaultModel = "reterminal_e1001"

// Options are the dependencies of Devices.
type Options struct {
	Devices  []Config
	Pipeline Pipeline
	Store    *Store
	// PublicURL is the base of image_url, as the devices reach this service.
	PublicURL string
	Log       *slog.Logger
	Now       func() time.Time
}

func New(o Options) *Devices {
	d := &Devices{
		pipeline:  o.Pipeline,
		store:     o.Store,
		publicURL: strings.TrimSuffix(o.PublicURL, "/"),
		log:       o.Log,
		now:       o.Now,
		byMAC:     map[string]*state{},
		simByMAC:  map[string]*state{},
		byName:    map[string]*state{},
		pending:   map[string]time.Time{},
	}
	for _, c := range o.Devices {
		s := &state{cfg: c}
		d.byMAC[c.MAC] = s
		d.simByMAC[c.MAC] = &state{cfg: c}
		d.byName[c.Name] = s
	}
	return d
}

// Store is where rendered images are kept.
func (d *Devices) Store() *Store { return d.store }

// Target is how the named device is rendered: its reported model and wake.
func (d *Devices) Target(name string) (render.Target, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, ok := d.byName[name]
	if !ok {
		return render.Target{}, false
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
			Config:   s.cfg,
			Model:    cmp.Or(s.model, DefaultModel),
			Wake:     s.wake,
			Page:     s.cfg.Pages[s.page],
			Filename: s.filename,
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

func (s *state) target() render.Target {
	return render.Target{Name: s.cfg.Name, Model: cmp.Or(s.model, DefaultModel), Rotation: s.cfg.Rotation, Wake: s.wake}
}

func newAPIKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
