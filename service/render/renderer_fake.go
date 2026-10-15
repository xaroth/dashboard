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
	lastProps any
	closed    bool
}

// NewFakeRenderer returns a FakeRenderer with the given pages.
func NewFakeRenderer(pages ...PageInfo) *FakeRenderer {
	return &FakeRenderer{pages: pages}
}

func (f *FakeRenderer) Describe(context.Context) ([]PageInfo, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.pages), "fake", nil
}

func (f *FakeRenderer) Render(_ context.Context, _ string, vp Viewport, props any) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.lastProps = props
	var buf bytes.Buffer
	err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, vp.Width, vp.Height)))
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
func (f *FakeRenderer) LastProps() any {
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
