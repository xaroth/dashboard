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
}

type instance struct {
	reg        Registration
	module     any
	interval   time.Duration
	staleAfter time.Duration
	log        *slog.Logger

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

// Load decodes each registered module's config, runs its migrations and
// initializes it. Config and migration errors stop startup; an Initialize
// error only turns that module off.
func Load(ctx context.Context, o Options) (*Set, error) {
	regs := slices.SortedFunc(slices.Values(o.Registrations), func(a, b Registration) int { return cmp.Compare(a.name, b.name) })
	known := map[string]bool{}
	for _, r := range regs {
		known[r.name] = true
	}
	for name := range o.Configs {
		if !known[name] {
			return nil, fmt.Errorf("modules.%s: no such module (have %v)", name, slices.Sorted(maps.Keys(known)))
		}
	}

	s := &Set{writer: o.Writer, reader: o.Reader, log: o.Log, httpClient: o.HTTPClient, now: o.Now}
	// Three passes, so a config error stops startup before any migration, and
	// every module's tables exist before any Initialize.
	configs := make([]any, len(regs))
	for i, r := range regs {
		cfg, sched, err := decodeConfig(r, o.Configs[r.name])
		if err != nil {
			return nil, fmt.Errorf("modules.%s: %w", r.name, err)
		}
		if sched.UpdateInterval < 0 || sched.StaleAfter < 0 {
			return nil, fmt.Errorf("modules.%s.schedule: durations must be positive", r.name)
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
	for _, r := range regs {
		if r.migrations == nil {
			continue
		}
		if err := o.Migrator.Migrate(ctx, r.name, r.migrations, "sql/migrations"); err != nil {
			return nil, err
		}
	}
	for i, inst := range s.instances {
		ictx, _ := s.context(ctx, inst, o.Writer, false)
		if err := inst.reg.initialize(ictx, inst.module, configs[i]); err != nil {
			inst.log.Error("initialize failed; module off until restart", "err", err)
			inst.initErr = err
			inst.phase = PhaseInitFail
		}
	}
	return s, nil
}

// decodeConfig overlays node on the module's defaults. Unknown keys are errors.
func decodeConfig(r Registration, node yaml.Node) (any, Schedule, error) {
	cfg := r.newConfig()
	var sched Schedule
	// Absent, or present without keys ("weather:"): defaults.
	if node.Kind == 0 || (node.Kind == yaml.ScalarNode && node.Tag == "!!null") {
		return cfg, sched, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, sched, errors.New("must be a mapping")
	}
	// Take the reserved schedule key out before decoding the module's own keys.
	rest := node
	rest.Content = nil
	for i := 0; i+1 < len(node.Content); i += 2 {
		k, v := node.Content[i], node.Content[i+1]
		if k.Value == "schedule" {
			if err := strictDecode(v, &sched); err != nil {
				return nil, sched, fmt.Errorf("schedule: %w", err)
			}
			continue
		}
		rest.Content = append(rest.Content, k, v)
	}
	if err := strictDecode(&rest, cfg); err != nil {
		return nil, sched, err
	}
	return cfg, sched, nil
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

// context returns ctx carrying the module's dependencies, with db as its
// connection. Only an update's context can save a snapshot.
func (s *Set) context(ctx context.Context, inst *instance, db *sql.DB, update bool) (context.Context, *stateStore) {
	state := &stateStore{q: New(db), module: inst.reg.name, update: update}
	ctx = context.WithValue(ctx, keyDatabase, db)
	ctx = context.WithValue(ctx, keyLogger, inst.log)
	ctx = context.WithValue(ctx, keyHTTPClient, s.httpClient)
	ctx = context.WithValue(ctx, keyClock, s.now)
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
