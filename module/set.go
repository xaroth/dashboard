package module

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"
)

//go:embed sql/migrations/*.sql
var stateMigrations embed.FS

// Owner of the module_state migrations. Renaming it would orphan the
// goose_storage version table of existing databases.
const stateOwner = "storage"

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

// Options are what Load needs.
type Options struct {
	Registrations []Registration
	// Configs are the modules.<name> sections of the config file.
	Configs  map[string]yaml.Node
	Migrator Migrator
	// Writer is the only connection that writes; Reader is read-only.
	Writer     *sql.DB
	Reader     *sql.DB
	Log        *slog.Logger
	HTTPClient *http.Client
	Now        func() time.Time
}

// Set is the loaded modules.
type Set struct {
	writer     *sql.DB
	reader     *sql.DB
	log        *slog.Logger
	httpClient *http.Client
	now        func() time.Time
	instances  []*instance
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

	// updating serializes scheduled and manual updates; modules are not
	// required to be safe for concurrent Update calls.
	updating sync.Mutex
	// kick tells the schedule a manual update succeeded.
	kick chan struct{}

	mu      sync.Mutex
	initErr error
	phase   Phase
	nextRun time.Time
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
	regs := slices.SortedFunc(slices.Values(o.Registrations), func(a, b Registration) int { return cmp.Compare(a.name, b.name) })
	known := map[string]bool{}
	for _, r := range regs {
		known[r.name] = true
	}
	shared, err := sharedDefs(regs, known)
	if err != nil {
		return nil, err
	}
	for _, d := range shared {
		known[d.sharedName()] = true
	}
	for name := range o.Configs {
		if !known[name] {
			return nil, fmt.Errorf("modules.%s: no such module (have %v)", name, slices.Sorted(maps.Keys(known)))
		}
	}

	s := &Set{writer: o.Writer, reader: o.Reader, log: o.Log, httpClient: o.HTTPClient, now: o.Now, shared: map[sharedDef]built{}, sharedOrder: shared}
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
			phase:      PhaseWaiting,
		})
	}
	if err := o.Migrator.Migrate(ctx, stateOwner, stateMigrations, "sql/migrations"); err != nil {
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
		v, err := d.buildAny(s.baseContext(ctx, log), sharedConfigs[i], declarations(regs, d))
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
		ictx, _ := s.context(ctx, inst, o.Writer, false)
		if err := inst.reg.initialize(ictx, inst.module, configs[i]); err != nil {
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
		reg: Registration{
			name:    d.sharedName(),
			queries: map[string]query{},
			shared:  []sharedUse{{def: d}},
			enabled: func(context.Context, any) (bool, error) { return true, nil },
			update: func(ctx context.Context, v any) (err error) {
				defer recovered(&err)
				return v.(Updater).Update(ctx)
			},
		},
		module:     b.value,
		interval:   cmp.Or(sched.UpdateInterval, sc.interval),
		staleAfter: cmp.Or(sched.StaleAfter, sc.staleAfter),
		timeout:    sc.timeout,
		log:        s.log.With("shared", d.sharedName()),
		kick:       make(chan struct{}, 1),
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

// isEmpty reports whether a config section is absent, or present without
// keys ("weather:"); both mean defaults.
func isEmpty(node yaml.Node) bool {
	return node.Kind == 0 || (node.Kind == yaml.ScalarNode && node.Tag == "!!null")
}

// decodeConfig overlays node on the defaults in cfg. With scheduled, the
// reserved schedule key is decoded into the returned Schedule; without, it is
// an unknown key. Unknown keys are errors.
func decodeConfig(node yaml.Node, cfg any, scheduled bool) (Schedule, error) {
	var sched Schedule
	if isEmpty(node) {
		return sched, nil
	}
	if node.Kind != yaml.MappingNode {
		return sched, errors.New("must be a mapping")
	}
	// Take the reserved schedule key out before decoding the module's own keys.
	rest := node
	rest.Content = nil
	for i := 0; i+1 < len(node.Content); i += 2 {
		k, v := node.Content[i], node.Content[i+1]
		if scheduled && k.Value == "schedule" {
			if err := strictDecode(v, &sched); err != nil {
				return sched, fmt.Errorf("schedule: %w", err)
			}
			if sched.UpdateInterval < 0 || sched.StaleAfter < 0 {
				return sched, errors.New("schedule: durations must be positive")
			}
			continue
		}
		rest.Content = append(rest.Content, k, v)
	}
	return sched, strictDecode(&rest, cfg)
}

// strictDecode decodes n into v, rejecting unknown keys; Node.Decode cannot.
func strictDecode(n *yaml.Node, v any) error {
	b, err := yaml.Marshal(n)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	return dec.Decode(v)
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

// baseContext returns ctx carrying the dependencies that need no module.
func (s *Set) baseContext(ctx context.Context, log *slog.Logger) context.Context {
	ctx = context.WithValue(ctx, keyLogger, log)
	ctx = context.WithValue(ctx, keyHTTPClient, s.httpClient)
	return context.WithValue(ctx, keyClock, s.now)
}

// context returns ctx carrying the module's dependencies, with db as its
// connection. Only an update's context can save a snapshot.
func (s *Set) context(ctx context.Context, inst *instance, db *sql.DB, update bool) (context.Context, *stateStore) {
	state := &stateStore{q: New(db), module: inst.reg.name, update: update}
	ctx = s.baseContext(ctx, inst.log)
	ctx = context.WithValue(ctx, keyDatabase, db)
	for _, u := range inst.reg.shared {
		ctx = context.WithValue(ctx, u.def, s.shared[u.def].value)
	}
	return context.WithValue(ctx, keyState, state), state
}

// stateStore keeps a module's snapshot: the one document a module like
// weather produces per update. A saved one is held until the update
// succeeds, then written with the success record in one statement.
type stateStore struct {
	q       *Queries
	module  string
	update  bool
	pending *string
}

func (s *stateStore) save(data []byte) error {
	if !s.update {
		return errors.New("snapshots are saved from Update")
	}
	str := string(data)
	s.pending = &str
	return nil
}

func (s *stateStore) load(ctx context.Context) ([]byte, error) {
	st, err := s.q.GetState(ctx, s.module)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && st.Data == nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading snapshot: %w", err)
	}
	return []byte(*st.Data), nil
}
