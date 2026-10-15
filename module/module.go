// Package module is the registration API and contract for data sources.
// main lists the modules and passes their registrations to Load.
package module

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"reflect"
	"regexp"
	"slices"
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
// stop until a manual update succeeds or the service restarts.
var ErrAuth = errors.New("authentication rejected")

const (
	defaultInterval   = 15 * time.Minute
	defaultStaleAfter = time.Hour
)

// Registration is a module definition, built by Define.
type Registration struct {
	name       string
	newModule  func() any
	newConfig  func() any
	interval   time.Duration
	staleAfter time.Duration
	migrations fs.FS
	queries    map[string]query
	moduleType reflect.Type
	initialize func(ctx context.Context, m any, cfg any) error
	enabled    func(ctx context.Context, m any) (bool, error)
	update     func(ctx context.Context, m any) error
}

// recovered turns a panic into *err. Module methods run in the scheduler's
// goroutines, outside net/http's recovery, where a panic ends the service.
func recovered(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("panicked: %v", r)
	}
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

// Define builds a registration. PM is inferred: *M must implement Module[C].
func Define[M any, C any, PM interface {
	*M
	Module[C]
}](name string, defaults func() *C, opts ...Option) Registration {
	if !validName.MatchString(name) {
		panic(fmt.Sprintf("module: invalid name %q", name))
	}
	r := Registration{
		name:       name,
		newModule:  func() any { return PM(new(M)) },
		newConfig:  func() any { return defaults() },
		interval:   defaultInterval,
		staleAfter: defaultStaleAfter,
		queries:    map[string]query{},
		moduleType: reflect.TypeFor[*M](),
		initialize: func(ctx context.Context, m any, cfg any) (err error) {
			defer recovered(&err)
			return m.(PM).Initialize(ctx, cfg.(*C))
		},
		enabled: func(ctx context.Context, m any) (on bool, err error) {
			defer recovered(&err)
			return m.(PM).Enabled(ctx), nil
		},
		update: func(ctx context.Context, m any) (err error) {
			defer recovered(&err)
			return m.(PM).Update(ctx)
		},
	}
	for _, opt := range opts {
		opt(&r)
	}
	if r.interval <= 0 || r.staleAfter <= 0 {
		panic(fmt.Sprintf("module: %s: update interval and stale after must be positive", name))
	}
	for _, qname := range r.queryNames() {
		if q := r.queries[qname]; q.moduleType != r.moduleType {
			panic(fmt.Sprintf("module: %s: query %s is a method of %s, not %s", r.name, qname, q.moduleType, r.moduleType))
		}
	}
	return r
}

func (r Registration) queryNames() []string {
	return slices.Sorted(maps.Keys(r.queries))
}
