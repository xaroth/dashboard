package render

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"slices"
	"sync"
)

// FakeRenderer stands in for the Bun process: it describes the pages it was
// given and renders blank images of the requested viewport.
type FakeRenderer struct {
	mu        sync.Mutex
	pages     []PageInfo
	err       error
	lastProps Props
	closed    bool
}

// NewFakeRenderer returns a FakeRenderer with the given pages.
func NewFakeRenderer(pages ...PageInfo) *FakeRenderer {
	return &FakeRenderer{pages: pages}
}

func (f *FakeRenderer) Describe(context.Context) ([]PageInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.pages), nil
}

func (f *FakeRenderer) Render(_ context.Context, _ string, props Props) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.lastProps = props
	var buf bytes.Buffer
	err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, props.Viewport.Width, props.Viewport.Height)))
	return buf.Bytes(), err
}

func (f *FakeRenderer) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// SetPages replaces the pages, as a rebuild would.
func (f *FakeRenderer) SetPages(pages ...PageInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages = pages
}

// FailRenders makes every render return err; nil restores rendering.
func (f *FakeRenderer) FailRenders(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// LastProps returns the props of the last successful render.
func (f *FakeRenderer) LastProps() Props {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastProps
}

// Closed reports whether Close was called.
func (f *FakeRenderer) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// FakeResolver answers queries from a map keyed "module.query" and records
// the params it was asked with. Unknown queries resolve to an error result.
type FakeResolver struct {
	mu      sync.Mutex
	results map[string]Result
	stale   []string
	asked   map[string]map[string]any
}

func NewFakeResolver(results map[string]Result, stale ...string) *FakeResolver {
	return &FakeResolver{results: results, stale: stale, asked: map[string]map[string]any{}}
}

func (f *FakeResolver) Resolve(_ context.Context, q QueryDescriptor) Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := q.Module + "." + q.Query
	f.asked[key] = q.Params
	if r, ok := f.results[key]; ok {
		return r
	}
	msg := "unknown query " + key
	return Result{Stale: true, Error: &msg}
}

func (f *FakeResolver) StaleModules(context.Context) ([]string, error) {
	return append([]string{}, f.stale...), nil
}

// Asked returns the params the last resolve of module.query used.
func (f *FakeResolver) Asked(key string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked[key]
}
