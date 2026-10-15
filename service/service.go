// Package service wires the dashboard's parts together and runs its listeners.
package service

import (
	"cmp"
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

	"github.com/xaroth/dashboard/service/render"
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
	device   *http.ServeMux
	admin    *http.ServeMux
}

type Option func(*Service)

// WithRenderer replaces the Bun render process.
func WithRenderer(r Renderer) Option {
	return func(s *Service) { s.renderer = r }
}

// New builds the service and loads the page bundle. Pages named by devices
// must exist.
func New(ctx context.Context, cfg Config, log *slog.Logger, opts ...Option) (*Service, error) {
	s := &Service{
		cfg:    cfg,
		log:    log,
		device: http.NewServeMux(),
		admin:  http.NewServeMux(),
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.renderer == nil {
		cmd := []string{cfg.Renderer.Bun, "renderer/main.ts", "--pages", "pages", "--fonts", "fonts"}
		if cfg.Renderer.Watch {
			cmd = append(cmd, "--watch")
		}
		s.renderer = render.NewProcess(cmd, cfg.Renderer.Dir, log.With("component", "renderer"))
	}

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("loading timezone: %w", err)
	}
	pipeline := render.NewPipeline(s.renderer, loc, time.Now)
	if err := pipeline.Load(ctx); err != nil {
		s.renderer.Close()
		return nil, err
	}
	pages := pipeline.Pages()
	for name, d := range cfg.Devices {
		for _, page := range d.Pages {
			if !slices.Contains(pages, page) {
				s.renderer.Close()
				return nil, fmt.Errorf("devices.%s: page %q does not exist (have %v)", name, page, pages)
			}
		}
	}

	s.admin.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	webHandler := &web.Handler{
		Pipeline: pipeline,
		Devices:  s.webDevices(),
		Target:   s.target,
		Log:      log.With("component", "web"),
	}
	webHandler.Register(s.admin)
	return s, nil
}

func (s *Service) webDevices() []web.Device {
	var out []web.Device
	for name, d := range s.cfg.Devices {
		out = append(out, web.Device{Name: name, Pages: d.Pages, Rotation: d.Rotation})
	}
	slices.SortFunc(out, func(a, b web.Device) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// target is how a configured device is rendered. Until devices report their
// model, the E1001 is assumed.
func (s *Service) target(name string) (render.Target, bool) {
	d, ok := s.cfg.Devices[name]
	if !ok {
		return render.Target{}, false
	}
	return render.Target{Name: name, Model: "reterminal_e1001", Rotation: d.Rotation}, true
}

// Device is the handler for the device listener.
func (s *Service) Device() http.Handler { return s.device }

// Admin is the handler for the admin listener.
func (s *Service) Admin() http.Handler { return s.admin }

// Run serves both listeners until ctx is cancelled or one of them fails, then
// stops the renderer.
func (s *Service) Run(ctx context.Context) error {
	defer s.renderer.Close()
	g, ctx := errgroup.WithContext(ctx)
	s.serve(ctx, g, "device", s.cfg.Listen.Device, s.device)
	s.serve(ctx, g, "admin", s.cfg.Listen.Admin, s.admin)
	return g.Wait()
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
