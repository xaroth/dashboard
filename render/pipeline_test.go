package render_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/xaroth/dashboard/render"
	"github.com/xaroth/dashboard/screen"
)

var testNow = time.Date(2026, 10, 16, 5, 30, 0, 0, time.UTC)

var testPages = []render.PageInfo{
	{Name: "hello", Data: map[string]render.QueryDescriptor{}},
	{Name: "overview", Data: map[string]render.QueryDescriptor{
		"power": {Module: "p1", Query: "power", Params: map[string]any{"range": "24h"}},
	}},
}

var testFetched = "2026-10-16T07:15:00+02:00"

func mustPipeline(tb testing.TB, o render.PipelineOptions) *render.Pipeline {
	tb.Helper()
	p, err := render.NewPipeline(o)
	if err != nil {
		tb.Fatalf("NewPipeline: %v", err)
	}
	return p
}

func newPipeline(tb testing.TB) (*render.Pipeline, *render.FakeRenderer) {
	tb.Helper()
	p, fake, _ := newPipelineWithResolver(tb)
	return p, fake
}

func newPipelineWithResolver(tb testing.TB) (*render.Pipeline, *render.FakeRenderer, *render.FakeResolver) {
	tb.Helper()
	fake := render.NewFakeRenderer(tb, render.ExpectPages(testPages...))
	res := render.NewFakeResolver(tb,
		render.ExpectResult("p1.power", render.Result{Value: []float64{1, 2}, FetchedAt: &testFetched}),
		render.ExpectStale("car"),
	)
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		tb.Fatalf("loading location: %v", err)
	}
	return mustPipeline(tb, render.PipelineOptions{Renderer: fake, Resolver: res, Location: loc, Now: func() time.Time { return testNow }}), fake, res
}

func ptr[T any](v T) *T { return &v }

func TestPipelineProps(t *testing.T) {
	t.Parallel()
	p, _ := newPipeline(t)
	wake := &screen.Wake{Source: "button", Battery: ptr(4.05), At: testNow}
	got, err := p.Props(t.Context(), screen.Target{Name: "hallway", Model: "reterminal_e1001", Rotation: 90, Wake: wake}, "overview")
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

func TestPipelinePropsStaleError(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("test", 0)
	res := render.NewFakeResolver(t, render.ExpectStaleError(errors.New("reading module states")))
	p := mustPipeline(t, render.PipelineOptions{Renderer: render.NewFakeRenderer(t, render.ExpectPages(testPages...)), Resolver: res, Location: loc, Now: func() time.Time { return testNow }})
	spans := tracetest.NewSpanRecorder()
	ctx, span := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)).Tracer("test").Start(t.Context(), "test")
	got, err := p.Props(ctx, screen.Target{Name: "hallway", Model: "reterminal_e1001"}, "hello")
	span.End()
	if err != nil {
		t.Fatalf("Props: %v", err)
	}
	if got.System.StaleModules == nil || len(got.System.StaleModules) != 0 {
		t.Errorf("got stale modules %#v, want empty", got.System.StaleModules)
	}
	if ev := spans.Ended()[0].Events(); len(ev) != 1 || ev[0].Name != "exception" {
		t.Errorf("got span events %+v, want the error recorded", ev)
	}
}

func TestPipelinePropsErrors(t *testing.T) {
	t.Parallel()
	p, _ := newPipeline(t)
	testCases := []struct {
		name   string
		target screen.Target
		page   string
		want   error
	}{
		{"unknown page", screen.Target{Model: "reterminal_e1001"}, "nope", render.ErrUnknownPage},
		{"unknown model", screen.Target{Model: "kindle"}, "hello", render.ErrUnknownModel},
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
	img, err := p.Render(t.Context(), screen.Target{Name: "x", Model: "reterminal_e1003", Rotation: 270}, "hello")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if w, h := pngSize(img.PNG); w != 1404 || h != 1872 {
		t.Errorf("got %dx%d, want 1404x1872", w, h)
	}
	if want := render.Filename(img.PNG, testNow); img.Filename != want {
		t.Errorf("got filename %q, want %q", img.Filename, want)
	}
	want, err := p.Props(t.Context(), screen.Target{Name: "x", Model: "reterminal_e1003", Rotation: 270}, "hello")
	if err != nil {
		t.Fatalf("Props: %v", err)
	}
	if diff := cmp.Diff(want, fake.LastProps()); diff != "" {
		t.Errorf("props sent to renderer (-want +got):\n%s", diff)
	}
}

func TestPipelineRenderError(t *testing.T) {
	t.Parallel()
	p, fake := newPipeline(t)
	boom := errors.New("boom")
	fake.FailRenders(boom)
	if _, err := p.Render(t.Context(), screen.Target{Model: "reterminal_e1001"}, "hello"); !errors.Is(err, boom) {
		t.Errorf("got %v, want %v", err, boom)
	}
}

// renderCounts is the number of renders in reg, by page and result.
func renderCounts(tb testing.TB, reg prometheus.Gatherer) map[[2]string]uint64 {
	tb.Helper()
	families, err := reg.Gather()
	if err != nil {
		tb.Fatalf("Gather: %v", err)
	}
	counts := map[[2]string]uint64{}
	for _, f := range families {
		if f.GetName() != "dashboard_render_duration_seconds" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			counts[[2]string{labels["page"], labels["result"]}] = m.GetHistogram().GetSampleCount()
		}
	}
	return counts
}

func TestPipelineRenderTelemetry(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	spans := tracetest.NewSpanRecorder()
	fake := render.NewFakeRenderer(t, render.ExpectPages(render.PageInfo{Name: "metrics"}))
	p := mustPipeline(t, render.PipelineOptions{
		Renderer: fake,
		Resolver: render.NewFakeResolver(t),
		Location: time.UTC,
		Now:      func() time.Time { return testNow },
		Metrics:  reg,
		Tracing:  sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)),
	})
	target := screen.Target{Model: "reterminal_e1001"}

	if _, err := p.Render(t.Context(), target, "metrics"); err != nil {
		t.Fatalf("Render: %v", err)
	}
	fake.FailRenders(errors.New("boom"))
	p.Render(t.Context(), target, "metrics")
	p.Render(t.Context(), target, "no-such-page")

	// The requested page stays out of the label values.
	want := map[[2]string]uint64{{"metrics", "success"}: 1, {"metrics", "failure"}: 1, {"unknown", "failure"}: 1}
	if diff := cmp.Diff(want, renderCounts(t, reg)); diff != "" {
		t.Errorf("render counts (-want +got):\n%s", diff)
	}
	type span struct {
		Name   string
		Status codes.Code
	}
	var got []span
	for _, s := range spans.Ended() {
		got = append(got, span{s.Name(), s.Status().Code})
	}
	wantSpans := []span{{"render metrics", codes.Unset}, {"render metrics", codes.Error}, {"render no-such-page", codes.Error}}
	if diff := cmp.Diff(wantSpans, got); diff != "" {
		t.Errorf("spans (-want +got):\n%s", diff)
	}
}

func TestNewPipelineMetricsRegistered(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	if _, err := render.NewPipeline(render.PipelineOptions{Metrics: reg}); err != nil {
		t.Fatalf("first NewPipeline: %v", err)
	}
	if _, err := render.NewPipeline(render.PipelineOptions{Metrics: reg}); err == nil {
		t.Error("got no error registering the render metrics twice, want one")
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
	if _, err := p.Props(t.Context(), screen.Target{Model: "reterminal_e1001"}, "energy"); err != nil {
		t.Errorf("got %v for the new page, want nil", err)
	}
}

func TestPipelineParamOverrides(t *testing.T) {
	t.Parallel()
	p, _, res := newPipelineWithResolver(t)
	target := screen.Target{Model: "reterminal_e1001", Params: map[string]map[string]any{
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
	if _, err := p.Props(t.Context(), screen.Target{Model: "reterminal_e1001"}, "overview"); err != nil {
		t.Fatalf("Props: %v", err)
	}
	if diff := cmp.Diff(map[string]any{"range": "24h"}, res.Asked("p1.power")); diff != "" {
		t.Errorf("params without override (-want +got):\n%s", diff)
	}
}

func BenchmarkPipelineRender(b *testing.B) {
	p, _ := newPipeline(b)
	target := screen.Target{Name: "x", Model: "reterminal_e1003"}
	for b.Loop() {
		if _, err := p.Render(b.Context(), target, "overview"); err != nil {
			b.Fatal(err)
		}
	}
}
