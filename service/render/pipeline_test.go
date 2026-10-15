package render_test

import (
	"context"
	"errors"
	"image"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/service/render"
)

// stubRenderer answers describe from pages and renders a white image of the viewport size.
type stubRenderer struct {
	tb testing.TB

	mu        sync.Mutex
	pages     []render.PageInfo
	hash      string
	describes int
	err       error
	props     any
}

func (s *stubRenderer) Describe(context.Context) ([]render.PageInfo, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.describes++
	return s.pages, s.hash, nil
}

func (s *stubRenderer) Render(_ context.Context, _ string, vp render.Viewport, props any) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	s.props = props
	return encodePNG(s.tb, image.NewNRGBA(image.Rect(0, 0, vp.Width, vp.Height))), nil
}

func (s *stubRenderer) Hash() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hash
}

var testNow = time.Date(2026, 10, 16, 5, 30, 0, 0, time.UTC)

func newPipeline(tb testing.TB) (*render.Pipeline, *stubRenderer) {
	tb.Helper()
	stub := &stubRenderer{
		tb:   tb,
		hash: "v1",
		pages: []render.PageInfo{
			{Name: "hello", Data: map[string]render.QueryDescriptor{}},
			{Name: "overview", Data: map[string]render.QueryDescriptor{
				"power": {Module: "p1", Query: "power", Params: map[string]any{"range": "24h"}},
			}},
		},
	}
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		tb.Fatalf("loading location: %v", err)
	}
	p := render.NewPipeline(stub, loc, func() time.Time { return testNow })
	if err := p.Load(tb.Context()); err != nil {
		tb.Fatalf("Load: %v", err)
	}
	return p, stub
}

func ptr[T any](v T) *T { return &v }

func TestPipelineProps(t *testing.T) {
	t.Parallel()
	p, _ := newPipeline(t)
	wake := &render.Wake{Source: "button", Battery: ptr(4.05), At: testNow}
	got, err := p.Props(t.Context(), render.Target{Name: "hallway", Model: "reterminal_e1001", Rotation: 90, Wake: wake}, "overview")
	if err != nil {
		t.Fatalf("Props: %v", err)
	}
	want := render.Props{
		Data: map[string]render.Result{
			"power": {Stale: true, Error: ptr("module p1 not available")},
		},
		Device:   render.DeviceProps{Name: "hallway", Model: "reterminal_e1001", Rotation: 90, Wake: wake},
		Viewport: render.Viewport{Width: 480, Height: 800, Portrait: true},
		System:   render.SystemProps{StaleModules: []string{}},
		Now:      "2026-10-16T07:30:00+02:00",
		Timezone: "Europe/Amsterdam",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("props (-want +got):\n%s", diff)
	}
}

func TestPipelinePropsErrors(t *testing.T) {
	t.Parallel()
	p, _ := newPipeline(t)
	testCases := []struct {
		name   string
		target render.Target
		page   string
	}{
		{"unknown page", render.Target{Model: "reterminal_e1001"}, "nope"},
		{"unknown model", render.Target{Model: "kindle"}, "hello"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, err := p.Props(t.Context(), testCase.target, testCase.page); err == nil {
				t.Error("got nil error, want one")
			}
		})
	}
}

func TestPipelineRender(t *testing.T) {
	t.Parallel()
	p, stub := newPipeline(t)
	img, err := p.Render(t.Context(), render.Target{Name: "x", Model: "reterminal_e1003", Rotation: 270}, "hello")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if w, h := pngSize(img.PNG); w != 1404 || h != 1872 {
		t.Errorf("got %dx%d, want 1404x1872", w, h)
	}
	if want := render.Filename(img.PNG, testNow); img.Filename != want {
		t.Errorf("got filename %q, want %q", img.Filename, want)
	}
	if diff := cmp.Diff(img.Props, stub.props); diff != "" {
		t.Errorf("props sent to renderer (-want +got):\n%s", diff)
	}
}

func TestPipelineRenderError(t *testing.T) {
	t.Parallel()
	p, stub := newPipeline(t)
	stub.err = errors.New("boom")
	if _, err := p.Render(t.Context(), render.Target{Model: "reterminal_e1001"}, "hello"); !errors.Is(err, stub.err) {
		t.Errorf("got %v, want %v", err, stub.err)
	}
}

func TestPipelineReloadsAfterRebuild(t *testing.T) {
	t.Parallel()
	p, stub := newPipeline(t)
	target := render.Target{Model: "reterminal_e1001"}
	if _, err := p.Props(t.Context(), target, "hello"); err != nil {
		t.Fatalf("Props: %v", err)
	}
	stub.mu.Lock()
	stub.hash = "v2"
	stub.pages = append(stub.pages, render.PageInfo{Name: "energy"})
	stub.mu.Unlock()

	if _, err := p.Props(t.Context(), target, "energy"); err != nil {
		t.Fatalf("Props after rebuild: %v", err)
	}
	if diff := cmp.Diff([]string{"energy", "hello", "overview"}, p.Pages()); diff != "" {
		t.Errorf("pages (-want +got):\n%s", diff)
	}
	if stub.describes != 2 {
		t.Errorf("got %d describes, want 2", stub.describes)
	}
}
