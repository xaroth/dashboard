package render

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/xaroth/dashboard/screen"
)

// Renderer is the part of Process the pipeline uses.
type Renderer interface {
	Describe(ctx context.Context) ([]PageInfo, error)
	Render(ctx context.Context, page string, props Props) ([]byte, error)
}

// Props are what a page component receives; gentypes writes their TypeScript
// types to pages/generated/props.ts.
type Props struct {
	Data     map[string]Result `json:"data"`
	Device   DeviceProps       `json:"device"`
	Viewport Viewport          `json:"viewport"`
	System   SystemProps       `json:"system"`
	Now      string            `json:"now"`
	Timezone string            `json:"timezone"`
}

type DeviceProps struct {
	Name     string       `json:"name"`
	Model    string       `json:"model"`
	Rotation int          `json:"rotation"`
	Wake     *screen.Wake `json:"wake,omitempty"`
}

type SystemProps struct {
	StaleModules []string `json:"staleModules"`
}

// Result is the envelope every data key arrives in.
type Result struct {
	Value     any     `json:"value"`
	FetchedAt *string `json:"fetchedAt"`
	Stale     bool    `json:"stale"`
	Error     *string `json:"error"`
}

var (
	ErrUnknownPage  = errors.New("unknown page")
	ErrUnknownModel = errors.New("unknown model")
)

// Resolver runs the queries pages declare.
type Resolver interface {
	// Resolve never fails: errors are in the result.
	Resolve(ctx context.Context, q QueryDescriptor) Result
	StaleModules(ctx context.Context) ([]string, error)
}

const resolveTimeout = 10 * time.Second

// Pipeline turns a page and a target into a device-ready image.
type Pipeline struct {
	renderer Renderer
	resolver Resolver
	location *time.Location
	now      func() time.Time
}

func NewPipeline(r Renderer, res Resolver, loc *time.Location, now func() time.Time) *Pipeline {
	return &Pipeline{renderer: r, resolver: res, location: loc, now: now}
}

// KnownModel reports whether pages can be rendered for model.
func (p *Pipeline) KnownModel(model string) bool {
	_, ok := PanelFor(model)
	return ok
}

// Pages returns the page names, sorted.
func (p *Pipeline) Pages(ctx context.Context) ([]string, error) {
	pages, err := p.PageInfo(ctx)
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(pages)), nil
}

// Props resolves what page would receive for t.
func (p *Pipeline) Props(ctx context.Context, t screen.Target, page string) (Props, error) {
	pages, err := p.PageInfo(ctx)
	if err != nil {
		return Props{}, err
	}
	info, ok := pages[page]
	if !ok {
		return Props{}, fmt.Errorf("%w %q", ErrUnknownPage, page)
	}
	panel, ok := PanelFor(t.Model)
	if !ok {
		return Props{}, fmt.Errorf("%w %q", ErrUnknownModel, t.Model)
	}

	// Like a failing query, unreadable module states must not fail the page.
	stale, err := p.resolver.StaleModules(ctx)
	if err != nil {
		stale = []string{}
	}
	return Props{
		Data:     p.resolve(ctx, t, page, info),
		Device:   DeviceProps{Name: t.Name, Model: t.Model, Rotation: t.Rotation, Wake: t.Wake},
		Viewport: ViewportFor(panel, t.Rotation),
		System:   SystemProps{StaleModules: stale},
		Now:      p.now().In(p.location).Format(time.RFC3339),
		Timezone: p.location.String(),
	}, nil
}

// resolve runs the page's queries concurrently, with the target's param
// overrides merged in.
func (p *Pipeline) resolve(ctx context.Context, t screen.Target, page string, info PageInfo) map[string]Result {
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	data := make(map[string]Result, len(info.Data))
	for key, q := range info.Data {
		q.Params = MergeParams(q.Params, t.Params[page+"."+key])
		wg.Go(func() {
			r := p.resolver.Resolve(ctx, q)
			mu.Lock()
			data[key] = r
			mu.Unlock()
		})
	}
	wg.Wait()
	return data
}

// MergeParams returns base with override's keys replacing its own.
func MergeParams(base, override map[string]any) map[string]any {
	out := maps.Clone(base)
	if out == nil {
		out = map[string]any{}
	}
	maps.Copy(out, override)
	return out
}

// Render resolves props, renders and finishes the image for t.
func (p *Pipeline) Render(ctx context.Context, t screen.Target, page string) (img screen.Image, err error) {
	ctx, span := tracer.Start(ctx, "render "+page, trace.WithAttributes(
		attribute.String("page", page), attribute.String("target", t.Name), attribute.String("model", t.Model)))
	start := time.Now()
	defer func() {
		result := "success"
		if err != nil {
			result = "failure"
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		label := page
		if errors.Is(err, ErrUnknownPage) {
			label = "unknown" // keep request input out of the label values
		}
		renderDuration.WithLabelValues(label, result).Observe(time.Since(start).Seconds())
		span.End()
	}()
	props, err := p.Props(ctx, t, page)
	if err != nil {
		return screen.Image{}, err
	}
	raw, err := p.renderer.Render(ctx, page, props)
	if err != nil {
		return screen.Image{}, err
	}
	png, err := Finish(raw, t.Rotation)
	if err != nil {
		return screen.Image{}, err
	}
	return screen.Image{PNG: png, Filename: Filename(png, p.now())}, nil
}

// PageInfo returns every page with its declared data, by name. It asks the
// renderer on every use: one small frame, and a watch rebuild is seen by the
// next request.
func (p *Pipeline) PageInfo(ctx context.Context) (map[string]PageInfo, error) {
	pages, err := p.renderer.Describe(ctx)
	if err != nil {
		return nil, fmt.Errorf("describing pages: %w", err)
	}
	m := make(map[string]PageInfo, len(pages))
	for _, pg := range pages {
		m[pg.Name] = pg
	}
	return m, nil
}
