package render

import (
	"context"
	"maps"
	"slices"
	"sync"
	"testing"
)

// FakeResolver answers queries from results keyed "module.query" and records
// the params it was asked with. Unknown queries resolve to an error result.
type FakeResolver struct {
	mu      sync.Mutex
	results map[string]Result
	stale   []string
	asked   map[string]map[string]any
}

// FakeResolverOption sets up a FakeResolver.
type FakeResolverOption func(testing.TB, *FakeResolver)

// NewFakeResolver returns a FakeResolver set up by opts.
func NewFakeResolver(tb testing.TB, opts ...FakeResolverOption) *FakeResolver {
	tb.Helper()
	f := &FakeResolver{results: map[string]Result{}, stale: []string{}, asked: map[string]map[string]any{}}
	for _, opt := range opts {
		opt(tb, f)
	}
	return f
}

// ExpectResult makes module.query, as key, resolve to r.
func ExpectResult(key string, r Result) FakeResolverOption {
	return func(_ testing.TB, f *FakeResolver) { f.results[key] = r }
}

// ExpectStale reports modules as stale.
func ExpectStale(modules ...string) FakeResolverOption {
	return func(_ testing.TB, f *FakeResolver) { f.stale = append(f.stale, modules...) }
}

func (f *FakeResolver) Resolve(_ context.Context, q QueryDescriptor) Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := q.Module + "." + q.Query
	f.asked[key] = maps.Clone(q.Params)
	if r, ok := f.results[key]; ok {
		return r
	}
	msg := "unknown query " + key
	return Result{Stale: true, Error: &msg}
}

func (f *FakeResolver) StaleModules(context.Context) ([]string, error) {
	return slices.Clone(f.stale), nil
}

// Asked returns the params the last resolve of module.query used.
func (f *FakeResolver) Asked(key string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked[key]
}
