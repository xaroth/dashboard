package render

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"
)

// Renderer is the part of Process the pipeline uses.
type Renderer interface {
	Describe(ctx context.Context) ([]PageInfo, string, error)
	Render(ctx context.Context, page string, vp Viewport, props any) ([]byte, error)
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

var (
	ErrUnknownPage  = errors.New("unknown page")
	ErrUnknownModel = errors.New("unknown model")
)

// Pipeline turns a page and a target into a device-ready image.
type Pipeline struct {
	renderer Renderer
	location *time.Location
	now      func() time.Time
}

func NewPipeline(r Renderer, loc *time.Location, now func() time.Time) *Pipeline {
	return &Pipeline{renderer: r, location: loc, now: now}
}

// Pages returns the page names, sorted.
func (p *Pipeline) Pages(ctx context.Context) ([]string, error) {
	pages, err := p.describe(ctx)
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(pages)), nil
}

// Props resolves what page would receive for t.
func (p *Pipeline) Props(ctx context.Context, t Target, page string) (Props, error) {
	pages, err := p.describe(ctx)
	if err != nil {
		return Props{}, err
	}
	info, ok := pages[page]
	if !ok {
		return Props{}, fmt.Errorf("%w %q", ErrUnknownPage, page)
	}
	panel, ok := Panels[t.Model]
	if !ok {
		return Props{}, fmt.Errorf("%w %q", ErrUnknownModel, t.Model)
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

// describe asks the renderer for its pages on every use: one small frame, and
// a watch rebuild is seen by the next request without tracking bundle hashes.
func (p *Pipeline) describe(ctx context.Context) (map[string]PageInfo, error) {
	pages, _, err := p.renderer.Describe(ctx)
	if err != nil {
		return nil, fmt.Errorf("describing pages: %w", err)
	}
	m := make(map[string]PageInfo, len(pages))
	for _, pg := range pages {
		m[pg.Name] = pg
	}
	return m, nil
}
