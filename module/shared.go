package module

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
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
}

// SharedOption configures a shared value.
type SharedOption func(*sharedOptions)

type sharedOptions struct {
	migrations fs.FS
}

// SharedMigrations uses the sql/migrations directory of fsys as the shared
// value's goose migrations.
func SharedMigrations(fsys fs.FS) SharedOption {
	return func(o *sharedOptions) { o.migrations = fsys }
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
	return &Shared[T, D]{
		name:       name,
		newConfig:  func() any { return defaults() },
		migrations: o.migrations,
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
	buildAny(ctx context.Context, cfg any, decls []any) (any, error)
}

// sharedUse is a module's declaration of a shared value.
type sharedUse struct {
	def   sharedDef
	decls []any
}

func (s *Shared[T, D]) sharedName() string      { return s.name }
func (s *Shared[T, D]) config() any             { return s.newConfig() }
func (s *Shared[T, D]) sharedMigrations() fs.FS { return s.migrations }

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

// SharedPages returns the names of the shared values RegisterShared serves, sorted.
func (s *Set) SharedPages() []string {
	out := []string{}
	for _, d := range s.sharedOrder {
		if _, ok := s.handler(d); ok {
			out = append(out, d.sharedName())
		}
	}
	return out
}

func (s *Set) handler(d sharedDef) (http.Handler, bool) {
	b := s.shared[d]
	if b.err != nil {
		return nil, false
	}
	h, ok := b.value.(http.Handler)
	return h, ok
}
