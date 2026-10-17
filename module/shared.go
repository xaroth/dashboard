package module

import (
	"context"
	"fmt"
)

// Shared is a value several modules use, such as an API client. It is
// configured once, under modules.<name>, and built before the modules that
// declare it with WithShared are initialized.
type Shared[T any] struct {
	name      string
	newConfig func() any
	build     func(ctx context.Context, cfg any) (T, error)
}

// DefineShared defines a shared value. build gets the decoded config and a
// context with Logger, HTTPClient and Clock; like Initialize, it does no
// network I/O. It runs only when a loaded module declares the value.
func DefineShared[T any, C any](name string, defaults func() *C, build func(ctx context.Context, cfg *C) (T, error)) *Shared[T] {
	if !validName.MatchString(name) {
		panic(fmt.Sprintf("module: invalid shared name %q", name))
	}
	return &Shared[T]{
		name:      name,
		newConfig: func() any { return defaults() },
		build: func(ctx context.Context, cfg any) (v T, err error) {
			defer recovered(&err)
			return build(ctx, cfg.(*C))
		},
	}
}

// Get returns the value in a context of a module that declared it.
func (s *Shared[T]) Get(ctx context.Context) T {
	v, ok := ctx.Value(s).(T)
	if !ok {
		panic(fmt.Sprintf("module: context has no shared %s; declare it with WithShared", s.name))
	}
	return v
}

// WithShared makes s available to the module through s.Get.
func WithShared[T any](s *Shared[T]) Option {
	return func(r *Registration) { r.shared = append(r.shared, s) }
}

// sharedDef is a Shared without its type, for Load.
type sharedDef interface {
	sharedName() string
	config() any
	buildAny(ctx context.Context, cfg any) (any, error)
}

func (s *Shared[T]) sharedName() string { return s.name }
func (s *Shared[T]) config() any        { return s.newConfig() }

func (s *Shared[T]) buildAny(ctx context.Context, cfg any) (any, error) {
	return s.build(ctx, cfg)
}

// built is a shared value, or the error that kept it from being built.
type built struct {
	value any
	err   error
}
