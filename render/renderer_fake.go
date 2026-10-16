package render

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"slices"
	"sync"
	"testing"
)

// FakeRenderer stands in for the Bun process: it describes the pages it was
// given and renders blank images of the requested viewport.
type FakeRenderer struct {
	mu          sync.Mutex
	pages       []PageInfo
	describeErr error
	renderErr   error
	lastProps   Props
	closed      bool
}

// FakeRendererOption sets up a FakeRenderer.
type FakeRendererOption func(testing.TB, *FakeRenderer)

// NewFakeRenderer returns a FakeRenderer set up by opts.
func NewFakeRenderer(tb testing.TB, opts ...FakeRendererOption) *FakeRenderer {
	tb.Helper()
	f := &FakeRenderer{}
	for _, opt := range opts {
		opt(tb, f)
	}
	return f
}

// ExpectPages makes the renderer describe pages.
func ExpectPages(pages ...PageInfo) FakeRendererOption {
	return func(_ testing.TB, f *FakeRenderer) { f.SetPages(pages...) }
}

// ExpectDescribe_Error makes describe fail with err.
func ExpectDescribe_Error(err error) FakeRendererOption {
	return func(_ testing.TB, f *FakeRenderer) { f.describeErr = err }
}

// ExpectRender_Error makes every render fail with err.
func ExpectRender_Error(err error) FakeRendererOption {
	return func(_ testing.TB, f *FakeRenderer) { f.FailRenders(err) }
}

func (f *FakeRenderer) Describe(context.Context) ([]PageInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	return slices.Clone(f.pages), nil
}

func (f *FakeRenderer) Render(_ context.Context, _ string, props Props) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.renderErr != nil {
		return nil, f.renderErr
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
	f.renderErr = err
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
