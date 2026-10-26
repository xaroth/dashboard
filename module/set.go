package module

import (
	"cmp"
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"

	"github.com/xaroth/dashboard/series"
)

//go:embed sql/migrations/*.sql
var stateMigrations embed.FS

// Owner of the module_state migrations. Renaming it would orphan the
// goose_storage version table of existing databases.
const stateOwner = "storage"

// Owner of the series migrations.
const seriesOwner = "series"

// Migrator applies an owner's goose migrations, the .sql files in dir of
// fsys; *storage.DB in production.
type Migrator interface {
	Migrate(ctx context.Context, owner string, fsys fs.FS, dir string) error
}

// Schedule overrides a module's timing; it lives under the reserved
// schedule key of the module's config section.
type Schedule struct {
	UpdateInterval time.Duration `yaml:"update_interval"`
	StaleAfter     time.Duration `yaml:"stale_after"`
}

// Config is a modules.<name> section of the config file, decoded by the
// caller's format; the zero value is an empty section, all defaults.
type Config struct {
	// Decode overlays the section, without the reserved schedule key, on the
	// defaults in v, rejecting unknown keys; nil decodes nothing.
	Decode func(v any) error
	// Schedule decodes the schedule key; nil if the section has none.
	Schedule func(s *Schedule) error
}

// Options are what Load needs. Migrator, Writer and Reader are required.
type Options struct {
	Registrations []Registration
	Configs       map[string]Config
	Migrator      Migrator
	// Reserved are the migration owners of other packages, such as
	// device; no module or shared value may take their name.
	Reserved []string
	// Writer is the only connection that writes; Reader is read-only.
	Writer     *sql.DB
	Reader     *sql.DB
	Log        *slog.Logger     // nil discards
	HTTPClient *http.Client     // nil is a client with a 30 s timeout
	Dialer     ContextDialer    // nil is a net.Dialer
	Now        func() time.Time // nil is time.Now
	// Location is the configured time zone, in Env.Location; series buckets
	// of an hour or longer align to it. nil is UTC.
	Location *time.Location
	// Metrics registers the update metrics; nil records them nowhere.
	Metrics prometheus.Registerer
	// Tracing starts the update and query spans; nil is a no-op.
	Tracing trace.TracerProvider
}

// Set is the loaded modules.
type Set struct {
	writer     *sql.DB
	reader     *sql.DB
	log        *slog.Logger
	httpClient *http.Client
	dialer     ContextDialer
	now        func() time.Time
	location   *time.Location
	metrics    *metrics
	tracer     trace.Tracer
	instances  []*instance
	series     *series.Store
	shared     map[sharedDef]built
	// sharedOrder is the shared values by name.
	sharedOrder []sharedDef
}

type instance struct {
	reg        Registration
	module     any
	interval   time.Duration
	staleAfter time.Duration
	// timeout bounds one update; zero is the default.
	timeout time.Duration
	log     *slog.Logger
	// shared is a scheduled shared value; its admin page is the shared value's.
	shared bool

	// updating serializes Enabled and scheduled and manual updates; modules
	// are not required to be safe for concurrent calls. A one-slot channel
	// rather than a mutex, so a waiter can give up when its context ends.
	updating chan struct{}
	// kick tells the schedule a manual update succeeded.
	kick chan struct{}

	mu      sync.Mutex
	initErr error
	phase   Phase
	nextRun time.Time
	// next is the delay after the last successful update, scheduled or
	// manual: the interval, or what it set with NextUpdate.
	next time.Duration
	// successes counts successful updates, so a scheduled update can tell
	// one landed while it waited.
	successes uint64
}

// Phase is where a module's schedule stands.
type Phase string

const (
	PhaseWaiting  Phase = "waiting"
	PhaseUpdating Phase = "updating"
	PhaseDisabled Phase = "disabled"
	PhaseFailed   Phase = "failed"      // last update failed; retrying with backoff
	PhaseAuth     Phase = "auth"        // credentials rejected; no retries until a manual update succeeds
	PhaseInitFail Phase = "init failed" // Initialize failed; off until restart
)

// Load decodes each registered module's config, runs its migrations, builds
// the shared values the modules declare and initializes the modules. Config
// and migration errors stop startup; an Initialize or shared build error only
// turns the modules involved off. A scheduled shared value runs alongside the
// modules, under its own name.
func Load(ctx context.Context, o Options) (*Set, error) {
	if o.Migrator == nil || o.Writer == nil || o.Reader == nil {
		return nil, errors.New("module: Options need a Migrator, Writer and Reader")
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if o.Dialer == nil {
		o.Dialer = &net.Dialer{}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Location == nil {
		o.Location = time.UTC
	}
	regs := slices.SortedFunc(slices.Values(o.Registrations), func(a, b Registration) int { return cmp.Compare(a.name, b.name) })
	reserved := map[string]bool{stateOwner: true, seriesOwner: true}
	for _, owner := range o.Reserved {
		reserved[owner] = true
	}
	known := map[string]bool{}
	for _, r := range regs {
		if reserved[r.name] {
			return nil, fmt.Errorf("module name %q is reserved", r.name)
		}
		if known[r.name] {
			return nil, fmt.Errorf("module %s: registered twice", r.name)
		}
		known[r.name] = true
	}
	shared, err := sharedDefs(regs, known)
	if err != nil {
		return nil, err
	}
	for _, d := range shared {
		if reserved[d.sharedName()] {
			return nil, fmt.Errorf("shared name %q is reserved", d.sharedName())
		}
		known[d.sharedName()] = true
	}
	for name := range o.Configs {
		if !known[name] {
			return nil, fmt.Errorf("modules.%s: no such module (have %v)", name, slices.Sorted(maps.Keys(known)))
		}
	}

	m, err := newMetrics(o.Metrics)
	if err != nil {
		return nil, fmt.Errorf("module metrics: %w", err)
	}
	s := &Set{writer: o.Writer, reader: o.Reader, log: o.Log, httpClient: o.HTTPClient, dialer: o.Dialer, now: o.Now, location: o.Location, metrics: m, tracer: newTracer(o.Tracing), shared: map[sharedDef]built{}, sharedOrder: shared}
	// Three passes, so a config error stops startup before any migration, and
	// every module's tables exist before any Initialize.
	sharedConfigs := make([]any, len(shared))
	sharedScheds := make([]Schedule, len(shared))
	for i, d := range shared {
		cfg := d.config()
		sched, err := decodeConfig(o.Configs[d.sharedName()], cfg, d.schedule() != nil)
		if err != nil {
			return nil, fmt.Errorf("modules.%s: %w", d.sharedName(), err)
		}
		sharedConfigs[i], sharedScheds[i] = cfg, sched
	}
	configs := make([]any, len(regs))
	for i, r := range regs {
		cfg := r.newConfig()
		sched, err := decodeConfig(o.Configs[r.name], cfg, true)
		if err != nil {
			return nil, fmt.Errorf("modules.%s: %w", r.name, err)
		}
		configs[i] = cfg
		s.instances = append(s.instances, &instance{
			reg:        r,
			module:     r.newModule(),
			interval:   cmp.Or(sched.UpdateInterval, r.interval),
			staleAfter: cmp.Or(sched.StaleAfter, r.staleAfter),
			log:        o.Log.With("module", r.name),
			kick:       make(chan struct{}, 1),
			updating:   make(chan struct{}, 1),
			phase:      PhaseWaiting,
		})
	}
	if err := o.Migrator.Migrate(ctx, stateOwner, stateMigrations, "sql/migrations"); err != nil {
		return nil, err
	}
	declared := map[string]series.Kind{}
	for _, r := range regs {
		for name, kind := range r.series {
			declared[r.name+"."+name] = kind
		}
	}
	s.series, err = series.Open(ctx, series.Options{
		Migrator: o.Migrator,
		Writer:   o.Writer,
		Reader:   o.Reader,
		Location: o.Location,
		Now:      o.Now,
		Log:      o.Log.With("component", "series"),
		Series:   declared,
	})
	if err != nil {
		return nil, err
	}
	for _, d := range shared {
		if fsys := d.sharedMigrations(); fsys != nil {
			if err := o.Migrator.Migrate(ctx, d.sharedName(), fsys, "sql/migrations"); err != nil {
				return nil, err
			}
		}
	}
	for _, r := range regs {
		if r.migrations == nil {
			continue
		}
		if err := o.Migrator.Migrate(ctx, r.name, r.migrations, "sql/migrations"); err != nil {
			return nil, err
		}
	}
	for i, d := range shared {
		log := o.Log.With("shared", d.sharedName())
		v, err := d.buildAny(ctx, sharedConfigs[i], declarations(regs, d), s.env(log))
		if err != nil {
			log.Error("build failed; modules using it off until restart", "err", err)
			err = fmt.Errorf("%s: %w", d.sharedName(), err)
		}
		s.shared[d] = built{value: v, err: err}
	}
	for i, inst := range s.instances {
		if err := s.sharedErr(inst); err != nil {
			inst.log.Error("shared value failed; module off until restart", "err", err)
			inst.initErr = err
			inst.phase = PhaseInitFail
			continue
		}
		if err := inst.reg.initialize(ctx, inst.module, configs[i], s.env(inst.log)); err != nil {
			inst.log.Error("initialize failed; module off until restart", "err", err)
			inst.initErr = err
			inst.phase = PhaseInitFail
		}
	}
	for i, d := range shared {
		if sc := d.schedule(); sc != nil {
			s.instances = append(s.instances, s.sharedInstance(d, sc, sharedScheds[i]))
		}
	}
	slices.SortFunc(s.instances, func(a, b *instance) int { return cmp.Compare(a.reg.name, b.reg.name) })
	return s, nil
}

// sharedInstance schedules a shared value's Update as a module. One that
// failed to build is listed as failed to initialize.
func (s *Set) sharedInstance(d sharedDef, sc *sharedSchedule, sched Schedule) *instance {
	b := s.shared[d]
	inst := &instance{
		shared: true,
		reg: Registration{
			name:    d.sharedName(),
			queries: map[string]queryDef{},
			shared:  []sharedUse{{def: d}},
			enabled: func(context.Context, any, Query) (bool, error) { return true, nil },
			update: func(ctx context.Context, v any, u *Update) (err error) {
				defer recovered(&err)
				return v.(Updater).Update(ctx, u)
			},
		},
		module:     b.value,
		interval:   cmp.Or(sched.UpdateInterval, sc.interval),
		staleAfter: cmp.Or(sched.StaleAfter, sc.staleAfter),
		timeout:    sc.timeout,
		log:        s.log.With("shared", d.sharedName()),
		kick:       make(chan struct{}, 1),
		updating:   make(chan struct{}, 1),
		phase:      PhaseWaiting,
	}
	if b.err != nil {
		inst.initErr, inst.phase = b.err, PhaseInitFail
	}
	return inst
}

// sharedDefs returns the shared values regs declare, sorted by name. A name
// may not be used by two definitions or by a module.
func sharedDefs(regs []Registration, modules map[string]bool) ([]sharedDef, error) {
	byName := map[string]sharedDef{}
	for _, r := range regs {
		for _, u := range r.shared {
			d := u.def
			name := d.sharedName()
			if modules[name] {
				return nil, fmt.Errorf("shared %s: name is also a module", name)
			}
			if prev, ok := byName[name]; ok && prev != d {
				return nil, fmt.Errorf("shared %s: defined twice", name)
			}
			byName[name] = d
		}
	}
	return slices.SortedFunc(maps.Values(byName), func(a, b sharedDef) int { return cmp.Compare(a.sharedName(), b.sharedName()) }), nil
}

// declarations returns what regs declare for d, in module name order.
func declarations(regs []Registration, d sharedDef) []any {
	var out []any
	for _, r := range regs {
		for _, u := range r.shared {
			if u.def == d {
				out = append(out, u.decls...)
			}
		}
	}
	return out
}

// minUpdateInterval keeps an override from hammering a rate-limited source.
const minUpdateInterval = time.Minute

// decodeConfig overlays c on the defaults in cfg and returns its schedule.
// Without scheduled, a schedule key is an error.
func decodeConfig(c Config, cfg any, scheduled bool) (Schedule, error) {
	var sched Schedule
	if c.Schedule != nil {
		if !scheduled {
			return sched, errors.New("schedule: has no schedule to override")
		}
		if err := c.Schedule(&sched); err != nil {
			return sched, fmt.Errorf("schedule: %w", err)
		}
		if sched.UpdateInterval < 0 || sched.StaleAfter < 0 {
			return sched, errors.New("schedule: durations must be positive")
		}
		if sched.UpdateInterval != 0 && sched.UpdateInterval < minUpdateInterval {
			return sched, fmt.Errorf("schedule: update_interval must be at least %v", minUpdateInterval)
		}
	}
	if c.Decode == nil {
		return sched, nil
	}
	return sched, c.Decode(cfg)
}

// sharedErr is the first error among the shared values inst declares.
func (s *Set) sharedErr(inst *instance) error {
	for _, u := range inst.reg.shared {
		if err := s.shared[u.def].err; err != nil {
			return err
		}
	}
	return nil
}

// env returns the Env with log.
func (s *Set) env(log *slog.Logger) Env {
	return Env{Log: log, HTTP: s.httpClient, Dialer: s.dialer, Now: s.now, Location: s.location}
}

// query returns inst's Query on db, with the shared values inst declares
// that were built.
func (s *Set) query(inst *instance, db *sql.DB) Query {
	shared := make(map[sharedDef]any, len(inst.reg.shared))
	for _, u := range inst.reg.shared {
		if b := s.shared[u.def]; b.err == nil {
			shared[u.def] = b.value
		}
	}
	return Query{Env: s.env(inst.log), DB: db, name: inst.reg.name, series: s.series, shared: shared}
}

// sharedQuery returns the Query on db for shared value d's Version and
// admin page.
func (s *Set) sharedQuery(d sharedDef, db *sql.DB) Query {
	return Query{Env: s.env(s.log.With("shared", d.sharedName())), DB: db, name: d.sharedName(), series: s.series}
}
