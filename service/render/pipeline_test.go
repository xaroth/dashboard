package render_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/service/render"
)

var testNow = time.Date(2026, 10, 16, 5, 30, 0, 0, time.UTC)

var testPages = []render.PageInfo{
	{Name: "hello", Data: map[string]render.QueryDescriptor{}},
	{Name: "overview", Data: map[string]render.QueryDescriptor{
		"power": {Module: "p1", Query: "power", Params: map[string]any{"range": "24h"}},
	}},
}

var testFetched = "2026-10-16T07:15:00+02:00"

func newPipeline(tb testing.TB) (*render.Pipeline, *render.FakeRenderer) {
	p, fake, _ := newPipelineWithResolver(tb)
	return p, fake
}

func newPipelineWithResolver(tb testing.TB) (*render.Pipeline, *render.FakeRenderer, *render.FakeResolver) {
	tb.Helper()
	fake := render.NewFakeRenderer(testPages...)
	res := render.NewFakeResolver(map[string]render.Result{
		"p1.power": {Value: []float64{1, 2}, FetchedAt: &testFetched},
	}, "car")
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		tb.Fatalf("loading location: %v", err)
	}
	return render.NewPipeline(fake, res, loc, func() time.Time { return testNow }), fake, res
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
			"power": {Value: []float64{1, 2}, FetchedAt: &testFetched},
		},
		Device:   render.DeviceProps{Name: "hallway", Model: "reterminal_e1001", Rotation: 90, Wake: wake},
		Viewport: render.Viewport{Width: 480, Height: 800, Portrait: true},
		System:   render.SystemProps{StaleModules: []string{"car"}},
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
		want   error
	}{
		{"unknown page", render.Target{Model: "reterminal_e1001"}, "nope", render.ErrUnknownPage},
		{"unknown model", render.Target{Model: "kindle"}, "hello", render.ErrUnknownModel},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, err := p.Props(t.Context(), testCase.target, testCase.page); !errors.Is(err, testCase.want) {
				t.Errorf("got %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestPipelineRender(t *testing.T) {
	t.Parallel()
	p, fake := newPipeline(t)
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
	if diff := cmp.Diff(img.Props, fake.LastProps()); diff != "" {
		t.Errorf("props sent to renderer (-want +got):\n%s", diff)
	}
}

func TestPipelineRenderError(t *testing.T) {
	t.Parallel()
	p, fake := newPipeline(t)
	boom := errors.New("boom")
	fake.FailRenders(boom)
	if _, err := p.Render(t.Context(), render.Target{Model: "reterminal_e1001"}, "hello"); !errors.Is(err, boom) {
		t.Errorf("got %v, want %v", err, boom)
	}
}

func TestPipelineSeesRebuilds(t *testing.T) {
	t.Parallel()
	p, fake := newPipeline(t)
	fake.SetPages(append(testPages, render.PageInfo{Name: "energy"})...)

	got, err := p.Pages(t.Context())
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if diff := cmp.Diff([]string{"energy", "hello", "overview"}, got); diff != "" {
		t.Errorf("pages (-want +got):\n%s", diff)
	}
	if _, err := p.Props(t.Context(), render.Target{Model: "reterminal_e1001"}, "energy"); err != nil {
		t.Errorf("Props for new page: %v", err)
	}
}

func TestPipelineParamOverrides(t *testing.T) {
	t.Parallel()
	p, _, res := newPipelineWithResolver(t)
	target := render.Target{Model: "reterminal_e1001", Params: map[string]map[string]any{
		"overview.power": {"step": "5m"},
		"hello.other":    {"ignored": true},
	}}
	if _, err := p.Props(t.Context(), target, "overview"); err != nil {
		t.Fatalf("Props: %v", err)
	}
	if diff := cmp.Diff(map[string]any{"range": "24h", "step": "5m"}, res.Asked("p1.power")); diff != "" {
		t.Errorf("params (-want +got):\n%s", diff)
	}
	// The page's own descriptor is not changed by the override.
	if _, err := p.Props(t.Context(), render.Target{Model: "reterminal_e1001"}, "overview"); err != nil {
		t.Fatalf("Props: %v", err)
	}
	if diff := cmp.Diff(map[string]any{"range": "24h"}, res.Asked("p1.power")); diff != "" {
		t.Errorf("params without override (-want +got):\n%s", diff)
	}
}
