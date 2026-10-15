// Package service wires the dashboard's parts together and runs its listeners.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"time"

	"golang.org/x/sync/errgroup"

	_ "github.com/xaroth/dashboard/modules"
	"github.com/xaroth/dashboard/service/device"
	"github.com/xaroth/dashboard/service/modules"
	"github.com/xaroth/dashboard/service/render"
	"github.com/xaroth/dashboard/service/storage"
	"github.com/xaroth/dashboard/service/web"
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
	modules  *modules.Set
	regs     []modules.Registration
	device   *http.ServeMux
	admin    *http.ServeMux
}

type Option func(*Service)

// WithRegistrations replaces the registered modules.
func WithRegistrations(regs ...modules.Registration) Option {
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
		regs:   modules.Registered(),
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
	s.modules, err = modules.Load(ctx, modules.Options{
		Registrations: s.regs,
		Configs:       cfg.Modules,
		DB:            s.db,
		Log:           log,
		HTTPClient:    &http.Client{Timeout: 30 * time.Second},
		Now:           time.Now,
	})
	if err != nil {
		return nil, err
	}
	if s.renderer == nil {
		cmd := []string{cfg.Renderer.Bun, "renderer/main.ts", "--pages", "pages", "--fonts", "fonts"}
		if cfg.Renderer.Watch {
			cmd = append(cmd, "--watch")
		}
		s.renderer = render.NewProcess(cmd, cfg.Renderer.Dir, log.With("component", "renderer"))
	}

	pipeline := render.NewPipeline(s.renderer, loc, time.Now)
	pages, err := pipeline.Pages(ctx)
	if err != nil {
		return nil, err
	}
	for name, d := range cfg.Devices {
		for _, page := range d.Pages {
			if !slices.Contains(pages, page) {
				return nil, fmt.Errorf("devices.%s: page %q does not exist (have %v)", name, page, pages)
			}
		}
	}

	s.admin.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	var configs []device.Config
	for name, d := range cfg.Devices {
		mac, _ := NormalizeMAC(d.MAC) // validated with the config
		configs = append(configs, device.Config{Name: name, MAC: mac, Rotation: d.Rotation, Pages: d.Pages})
	}
	devices := device.New(device.Options{
		Devices:   configs,
		Pipeline:  pipeline,
		Store:     &device.Store{Max: 16},
		PublicURL: cfg.PublicURL,
		Log:       log.With("component", "device"),
		Now:       time.Now,
	})
	devices.Register(s.device)

	webHandler := &web.Handler{
		Pipeline:  pipeline,
		Devices:   devices,
		Modules:   s.modules,
		DeviceAPI: s.device,
		Log:       log.With("component", "web"),
	}
	webHandler.Register(s.admin)
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
			return err
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
