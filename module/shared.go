package module

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"reflect"
	"time"
)

// Shared is a value several modules use, such as an API client. It is
// configured once, under modules.<name>, and built before the modules that
// declare it with WithShared are initialized. D is what a module declares
// along with it, such as the API scopes it needs.
type Shared[T any, D any] struct {
	name       string
	newConfig  func() any
	build      func(ctx context.Context, cfg any, decls []D) (T, error)
	migrations fs.FS
	sched      *sharedSchedule
}

// Updater is a shared value with a schedule; see SharedSchedule.
type Updater interface {
	// Update fetches from the source and writes to storage, as a module's
	// Update does.
	Update(ctx context.Context) error
}

// Versioned is a shared value that reports the version of the source it
// serves, for the admin UI. ctx has Database (the read-only pool), Logger,
// HTTPClient and Clock.
type Versioned interface {
	Version(ctx context.Context) (string, error)
}

// SharedOption configures a shared value.
type SharedOption func(*sharedOptions)

type sharedOptions struct {
	migrations fs.FS
	sched      *sharedSchedule
	timeout    time.Duration
}

type sharedSchedule struct {
	interval, staleAfter, timeout time.Duration
}

// SharedMigrations uses the sql/migrations directory of fsys as the shared
// value's goose migrations.
func SharedMigrations(fsys fs.FS) SharedOption {
	return func(o *sharedOptions) { o.migrations = fsys }
}

// SharedSchedule runs the value's Update every interval, scheduled, recorded
// and updated manually like a module, under the shared value's name. Data is
// stale after staleAfter. The value must implement Updater.
func SharedSchedule(interval, staleAfter time.Duration) SharedOption {
	return func(o *sharedOptions) { o.sched = &sharedSchedule{interval: interval, staleAfter: staleAfter} }
}

// SharedUpdateTimeout lets one scheduled Update take up to d instead of the
// default 2 minutes.
func SharedUpdateTimeout(d time.Duration) SharedOption {
	return func(o *sharedOptions) { o.timeout = d }
}

// DefineShared defines a shared value. build gets the decoded config, the
// declarations of every module that declared the value, in module name
// order, and a context with Logger, HTTPClient and Clock; like Initialize,
// it does no network I/O. It runs only when a loaded module declares the
// value.
//
// A value that is an http.Handler is served on the admin listener under
// /<name>/, with Database (the writer), Logger, HTTPClient and Clock on the
// request context.
func DefineShared[T any, C any, D any](name string, defaults func() *C, build func(ctx context.Context, cfg *C, decls []D) (T, error), opts ...SharedOption) *Shared[T, D] {
	if !validName.MatchString(name) {
		panic(fmt.Sprintf("module: invalid shared name %q", name))
	}
	var o sharedOptions
	for _, opt := range opts {
		opt(&o)
	}
	if o.timeout != 0 && o.sched == nil {
		panic(fmt.Sprintf("module: shared %s: update timeout without a schedule", name))
	}
	if sc := o.sched; sc != nil {
		sc.timeout = o.timeout
		if sc.interval <= 0 || sc.staleAfter <= 0 || sc.timeout < 0 {
			panic(fmt.Sprintf("module: shared %s: schedule durations must be positive", name))
		}
		if !reflect.TypeFor[T]().Implements(reflect.TypeFor[Updater]()) {
			panic(fmt.Sprintf("module: shared %s: scheduled but %s does not implement Updater", name, reflect.TypeFor[T]()))
		}
	}
	return &Shared[T, D]{
		name:       name,
		newConfig:  func() any { return defaults() },
		migrations: o.migrations,
		sched:      o.sched,
		build: func(ctx context.Context, cfg any, decls []D) (v T, err error) {
			defer recovered(&err)
			return build(ctx, cfg.(*C), decls)
		},
	}
}

// Get returns the value in a context of a module that declared it.
func (s *Shared[T, D]) Get(ctx context.Context) T {
	v, ok := ctx.Value(s).(T)
	if !ok {
		panic(fmt.Sprintf("module: context has no shared %s; declare it with WithShared", s.name))
	}
	return v
}

// WithShared makes s available to the module through s.Get, with the
// module's declarations for it.
func WithShared[T any, D any](s *Shared[T, D], decls ...D) Option {
	return func(r *Registration) {
		u := sharedUse{def: s}
		for _, d := range decls {
			u.decls = append(u.decls, d)
		}
		r.shared = append(r.shared, u)
	}
}

// sharedDef is a Shared without its types, for Load.
type sharedDef interface {
	sharedName() string
	config() any
	sharedMigrations() fs.FS
	schedule() *sharedSchedule
	buildAny(ctx context.Context, cfg any, decls []any) (any, error)
}

// sharedUse is a module's declaration of a shared value.
type sharedUse struct {
	def   sharedDef
	decls []any
}

func (s *Shared[T, D]) sharedName() string        { return s.name }
func (s *Shared[T, D]) config() any               { return s.newConfig() }
func (s *Shared[T, D]) sharedMigrations() fs.FS   { return s.migrations }
func (s *Shared[T, D]) schedule() *sharedSchedule { return s.sched }

func (s *Shared[T, D]) buildAny(ctx context.Context, cfg any, decls []any) (any, error) {
	typed := make([]D, len(decls))
	for i, d := range decls {
		typed[i] = d.(D)
	}
	return s.build(ctx, cfg, typed)
}

// built is a shared value, or the error that kept it from being built.
type built struct {
	value any
	err   error
}

// RegisterShared serves the shared values that are http.Handlers on mux,
// each under /<name>/. One that failed to build is not served.
func (s *Set) RegisterShared(mux *http.ServeMux) {
	for _, d := range s.sharedOrder {
		h, ok := s.handler(d)
		if !ok {
			continue
		}
		name := d.sharedName()
		log := s.log.With("shared", name)
		mux.Handle("/"+name+"/", http.StripPrefix("/"+name, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(s.baseContext(r.Context(), log), keyDatabase, s.writer)
			h.ServeHTTP(w, r.WithContext(ctx))
		})))
	}
}

// SharedStatus is a shared value's state for the admin UI.
type SharedStatus struct {
	Name string
	// Page is whether it is served under /<name>/.
	Page bool
	// Version is what a Versioned value reports.
	Version string
	// Error is the build error, or the error reporting the version.
	Error string
}

// Shared returns every shared value's status, sorted by name.
func (s *Set) Shared(ctx context.Context) []SharedStatus {
	out := make([]SharedStatus, 0, len(s.sharedOrder))
	for _, d := range s.sharedOrder {
		st := SharedStatus{Name: d.sharedName()}
		_, st.Page = s.handler(d)
		b := s.shared[d]
		if b.err != nil {
			st.Error = b.err.Error()
		} else if v, ok := b.value.(Versioned); ok {
			vctx := context.WithValue(s.baseContext(ctx, s.log.With("shared", st.Name)), keyDatabase, s.reader)
			var err error
			if st.Version, err = versionOf(vctx, v); err != nil {
				st.Error = err.Error()
			}
		}
		out = append(out, st)
	}
	return out
}

func versionOf(ctx context.Context, v Versioned) (version string, err error) {
	defer recovered(&err)
	return v.Version(ctx)
}

func (s *Set) handler(d sharedDef) (http.Handler, bool) {
	b := s.shared[d]
	if b.err != nil {
		return nil, false
	}
	h, ok := b.value.(http.Handler)
	return h, ok
}
