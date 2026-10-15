package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/xaroth/dashboard/device"
	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/modules"
	"github.com/xaroth/dashboard/render"
	"github.com/xaroth/dashboard/storage"
)

// Renderer renders pages; *render.Process in production.
type Renderer interface {
	render.Renderer
	io.Closer
}

type Service struct {
	cfg      Config
	log      *slog.Logger
	renderer Renderer
	db       *storage.DB
	modules  *module.Set
	regs     []module.Registration
	device   *http.ServeMux
	admin    *http.ServeMux
}

type Option func(*Service)

// WithRegistrations replaces modules.All.
func WithRegistrations(regs ...module.Registration) Option {
	return func(s *Service) { s.regs = regs }
}

// WithRenderer replaces the Bun render process.
func WithRenderer(r Renderer) Option {
	return func(s *Service) { s.renderer = r }
}

// New opens storage, builds the service and loads the page bundle. Pages
// named by devices must exist.
func New(ctx context.Context, cfg Config, log *slog.Logger, opts ...Option) (_ *Service, err error) {
	s := &Service{
		cfg:    cfg,
		log:    log,
		regs:   modules.All(),
		device: http.NewServeMux(),
		admin:  http.NewServeMux(),
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("loading timezone: %w", err)
	}
	for _, opt := range opts {
		opt(s)
	}
	defer func() {
		if err != nil {
			s.close()
		}
	}()
	if s.db, err = storage.Open(ctx, cfg.DataDir); err != nil {
		return nil, fmt.Errorf("opening storage: %w", err)
	}
	s.modules, err = module.Load(ctx, module.Options{
		Registrations: s.regs,
		Configs:       cfg.Modules,
		Migrator:      s.db,
		Writer:        s.db.Writer,
		Reader:        s.db.Reader,
		Log:           log,
		HTTPClient:    &http.Client{Timeout: 30 * time.Second},
		Now:           time.Now,
	})
	if err != nil {
		return nil, fmt.Errorf("loading modules: %w", err)
	}
	if s.renderer == nil {
		cmd := []string{cfg.Renderer.Bun, "renderer/main.ts", "--pages", "pages"}
		if cfg.Renderer.Watch {
			cmd = append(cmd, "--watch")
		}
		s.renderer = render.NewProcess(cmd, cfg.Renderer.Dir, log.With("component", "renderer"))
	}

	pipeline := render.NewPipeline(s.renderer, resolver{set: s.modules, loc: loc}, loc, time.Now)
	if err := s.checkPages(ctx, pipeline); err != nil {
		return nil, err
	}

	s.admin.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	var configs []device.Config
	for name, d := range cfg.Devices {
		mac, _ := device.NormalizeMAC(d.MAC) // validated with the config
		configs = append(configs, device.Config{Name: name, MAC: mac, Rotation: d.Rotation, Pages: d.Pages, Params: d.Params})
	}
	devices := device.New(device.Options{
		Devices:   configs,
		Pipeline:  pipeline,
		Store:     &device.Store{Max: 16},
		PublicURL: cfg.PublicURL,
		Schedule:  cfg.Schedule.device(loc),
		Log:       log.With("component", "device"),
		Now:       time.Now,
	})
	devices.Register(s.device)

	ui := &adminHandler{
		Pipeline:  pipeline,
		Devices:   devices,
		Modules:   s.modules,
		DeviceAPI: s.device,
		Log:       log.With("component", "web"),
	}
	ui.Register(s.admin)
	return s, nil
}

// Device is the handler for the device listener.
func (s *Service) Device() http.Handler { return s.device }

// Admin is the handler for the admin listener.
func (s *Service) Admin() http.Handler { return s.admin }

// Run serves both listeners until ctx is cancelled or one of them fails, then
// stops the renderer and closes storage.
func (s *Service) Run(ctx context.Context) error {
	defer s.close()
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		s.modules.Run(ctx)
		return nil
	})
	s.serve(ctx, g, "device", s.cfg.Listen.Device, s.device)
	s.serve(ctx, g, "admin", s.cfg.Listen.Admin, s.admin)
	return g.Wait()
}

// checkPages validates what pages and devices ask for: pages exist, every
// declared query exists and its params decode, also with each device's
// overrides. Mistakes stop startup instead of showing up at render.
func (s *Service) checkPages(ctx context.Context, pipeline *render.Pipeline) error {
	pages, err := pipeline.PageInfo(ctx)
	if err != nil {
		return fmt.Errorf("reading pages: %w", err)
	}
	names := slices.Sorted(maps.Keys(pages))
	for _, page := range names {
		for key, q := range pages[page].Data {
			if err := s.modules.Check(q.Module, q.Query, q.Params); err != nil {
				return fmt.Errorf("page %s, data %s: %w", page, key, err)
			}
		}
	}
	for name, d := range s.cfg.Devices {
		for _, page := range d.Pages {
			if _, ok := pages[page]; !ok {
				return fmt.Errorf("devices.%s: page %q does not exist (have %v)", name, page, names)
			}
		}
		for ref, override := range d.Params {
			page, key, _ := strings.Cut(ref, ".")
			q, ok := pages[page].Data[key]
			if !ok {
				return fmt.Errorf("devices.%s.params: %q is not a page data key (page.key)", name, ref)
			}
			if err := s.modules.Check(q.Module, q.Query, render.MergeParams(q.Params, override)); err != nil {
				return fmt.Errorf("devices.%s.params.%s: %w", name, ref, err)
			}
		}
	}
	return nil
}

// resolver runs page queries on the module set and shapes the results for pages.
type resolver struct {
	set *module.Set
	loc *time.Location
}

func (r resolver) Resolve(ctx context.Context, q render.QueryDescriptor) render.Result {
	res := r.set.Resolve(ctx, q.Module, q.Query, q.Params)
	// A value JSON cannot encode (NaN) would fail the whole render; fail this key only.
	if _, err := json.Marshal(res.Value); err != nil {
		res.Value, res.Error = nil, fmt.Sprintf("encoding result: %v", err)
	}
	out := render.Result{Value: res.Value, Stale: res.Stale}
	if res.FetchedAt != nil {
		at := res.FetchedAt.In(r.loc).Format(time.RFC3339)
		out.FetchedAt = &at
	}
	if res.Error != "" {
		out.Error = &res.Error
	}
	return out
}

func (r resolver) StaleModules(ctx context.Context) ([]string, error) {
	return r.set.StaleModules(ctx)
}

func (s *Service) close() {
	if s.renderer != nil {
		s.renderer.Close()
	}
	if s.db != nil {
		s.db.Close()
	}
}

func (s *Service) serve(ctx context.Context, g *errgroup.Group, name, addr string, h http.Handler) {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Covers a render on a cold or restarting renderer.
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  2 * time.Minute,
		// Requests keep their context during the shutdown drain.
		BaseContext: func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	g.Go(func() error {
		s.log.Info("listening", "listener", name, "addr", addr)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serving %s: %w", name, err)
		}
		return nil
	})
	g.Go(func() error {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	})
}
