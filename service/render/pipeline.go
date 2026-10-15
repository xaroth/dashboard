package render

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"
)

// Renderer is the part of Process the pipeline uses.
type Renderer interface {
	Describe(ctx context.Context) ([]PageInfo, string, error)
	Render(ctx context.Context, page string, vp Viewport, props any) ([]byte, error)
	Hash() string
}

// Target is who a page is rendered for: a device, or a panel in a preview.
type Target struct {
	Name     string
	Model    string
	Rotation int
	Wake     *Wake
}

// Wake is what the device reported on its latest request.
type Wake struct {
	Source   string    `json:"source,omitempty"`
	Battery  *float64  `json:"battery,omitempty"`
	RSSI     *int      `json:"rssi,omitempty"`
	Firmware string    `json:"firmware,omitempty"`
	At       time.Time `json:"at"`
}

// Props are what a page component receives; the shape of PageProps in pages/lib/page.ts.
type Props struct {
	Data     map[string]Result `json:"data"`
	Device   DeviceProps       `json:"device"`
	Viewport Viewport          `json:"viewport"`
	System   SystemProps       `json:"system"`
	Now      string            `json:"now"`
	Timezone string            `json:"timezone"`
}

type DeviceProps struct {
	Name     string `json:"name"`
	Model    string `json:"model"`
	Rotation int    `json:"rotation"`
	Wake     *Wake  `json:"wake,omitempty"`
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

// Image is a finished render.
type Image struct {
	PNG      []byte
	Filename string
	Props    Props
}

// Pipeline turns a page and a target into a device-ready image.
type Pipeline struct {
	renderer Renderer
	location *time.Location
	now      func() time.Time

	mu    sync.Mutex
	pages map[string]PageInfo
	hash  string
}

// NewPipeline returns a pipeline that renders through r, with times in loc.
// Call Load before use.
func NewPipeline(r Renderer, loc *time.Location, now func() time.Time) *Pipeline {
	return &Pipeline{renderer: r, location: loc, now: now}
}

// Load asks the renderer for its pages. Called at start and again when the
// bundle hash changes.
func (p *Pipeline) Load(ctx context.Context) error {
	pages, hash, err := p.renderer.Describe(ctx)
	if err != nil {
		return fmt.Errorf("describing pages: %w", err)
	}
	m := make(map[string]PageInfo, len(pages))
	for _, pg := range pages {
		m[pg.Name] = pg
	}
	p.mu.Lock()
	p.pages, p.hash = m, hash
	p.mu.Unlock()
	return nil
}

// Pages returns the page names, sorted.
func (p *Pipeline) Pages() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Sorted(maps.Keys(p.pages))
}

// Props resolves what page would receive for t.
func (p *Pipeline) Props(ctx context.Context, t Target, page string) (Props, error) {
	if err := p.refresh(ctx); err != nil {
		return Props{}, err
	}
	p.mu.Lock()
	info, ok := p.pages[page]
	p.mu.Unlock()
	if !ok {
		return Props{}, fmt.Errorf("unknown page %q", page)
	}
	panel, ok := Panels[t.Model]
	if !ok {
		return Props{}, fmt.Errorf("unknown model %q", t.Model)
	}

	data := make(map[string]Result, len(info.Data))
	for key, q := range info.Data {
		// Queries arrive with the module registry; until then every key is an error envelope.
		msg := fmt.Sprintf("module %s not available", q.Module)
		data[key] = Result{Stale: true, Error: &msg}
	}
	return Props{
		Data:     data,
		Device:   DeviceProps{Name: t.Name, Model: t.Model, Rotation: t.Rotation, Wake: t.Wake},
		Viewport: ViewportFor(panel, t.Rotation),
		System:   SystemProps{StaleModules: []string{}},
		Now:      p.now().In(p.location).Format(time.RFC3339),
		Timezone: p.location.String(),
	}, nil
}

// Render resolves props, renders and finishes the image for t.
func (p *Pipeline) Render(ctx context.Context, t Target, page string) (Image, error) {
	props, err := p.Props(ctx, t, page)
	if err != nil {
		return Image{}, err
	}
	raw, err := p.renderer.Render(ctx, page, props.Viewport, props)
	if err != nil {
		return Image{}, err
	}
	png, err := Finish(raw, t.Rotation)
	if err != nil {
		return Image{}, err
	}
	return Image{PNG: png, Filename: Filename(png, p.now()), Props: props}, nil
}

// refresh reloads the page list after the renderer rebuilt its bundle.
func (p *Pipeline) refresh(ctx context.Context) error {
	p.mu.Lock()
	stale := p.pages == nil || p.renderer.Hash() != p.hash
	p.mu.Unlock()
	if !stale {
		return nil
	}
	return p.Load(ctx)
}
