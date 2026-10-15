// Package modules is the registration API and contract for data sources.
// Modules register at init, like database/sql drivers; the service reads the
// registry once at start.
package modules

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sync"
	"time"
)

// Module is the contract every data source implements on its pointer type.
type Module[C any] interface {
	// Initialize receives the config. Migrations have run; no network I/O.
	Initialize(ctx context.Context, cfg *C) error
	// Enabled reports whether the scheduler should run Update.
	Enabled(ctx context.Context) bool
	// Update fetches from the source and writes to storage.
	Update(ctx context.Context) error
}

// ErrAuth marks an update error the source rejected for credentials: retries
// stop until the service restarts.
var ErrAuth = errors.New("authentication rejected")

const (
	defaultInterval   = 15 * time.Minute
	defaultStaleAfter = time.Hour
)

// Registration is a module definition. Register adds it to the registry.
type Registration struct {
	name       string
	newModule  func() any
	newConfig  func() any
	interval   time.Duration
	staleAfter time.Duration
	migrations fs.FS
	initialize func(ctx context.Context, m any, cfg any) error
	enabled    func(ctx context.Context, m any) bool
	update     func(ctx context.Context, m any) error
}

func (r Registration) Name() string { return r.name }

// Option configures a registration.
type Option func(*Registration)

func WithUpdateInterval(d time.Duration) Option {
	return func(r *Registration) { r.interval = d }
}

func WithStaleAfter(d time.Duration) Option {
	return func(r *Registration) { r.staleAfter = d }
}

// WithMigrations uses the sql/migrations directory of fsys as the module's
// goose migrations.
func WithMigrations(fsys fs.FS) Option {
	return func(r *Registration) { r.migrations = fsys }
}

var validName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Define builds a registration without adding it to the registry; tests use
// it directly. PM is inferred: *M must implement Module[C].
func Define[M any, C any, PM interface {
	*M
	Module[C]
}](name string, defaults func() *C, opts ...Option) Registration {
	if !validName.MatchString(name) {
		panic(fmt.Sprintf("modules: invalid name %q", name))
	}
	r := Registration{
		name:       name,
		newModule:  func() any { return PM(new(M)) },
		newConfig:  func() any { return defaults() },
		interval:   defaultInterval,
		staleAfter: defaultStaleAfter,
		initialize: func(ctx context.Context, m any, cfg any) error { return m.(PM).Initialize(ctx, cfg.(*C)) },
		enabled:    func(ctx context.Context, m any) bool { return m.(PM).Enabled(ctx) },
		update:     func(ctx context.Context, m any) error { return m.(PM).Update(ctx) },
	}
	for _, opt := range opts {
		opt(&r)
	}
	if r.interval <= 0 || r.staleAfter <= 0 {
		panic(fmt.Sprintf("modules: %s: update interval and stale after must be positive", name))
	}
	return r
}

var (
	registryMu sync.Mutex
	registry   = map[string]Registration{}
)

// Register defines a module and adds it to the registry. Call it from a
// package-level var so it runs at init.
func Register[M any, C any, PM interface {
	*M
	Module[C]
}](name string, defaults func() *C, opts ...Option) Registration {
	r := Define[M, C, PM](name, defaults, opts...)
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := registry[name]; ok {
		panic(fmt.Sprintf("modules: %s registered twice", name))
	}
	registry[name] = r
	return r
}

// Registered returns every registered module.
func Registered() []Registration {
	registryMu.Lock()
	defer registryMu.Unlock()
	out := make([]Registration, 0, len(registry))
	for _, r := range registry {
		out = append(out, r)
	}
	return out
}
