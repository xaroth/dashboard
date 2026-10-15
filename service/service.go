// Package service wires the dashboard's parts together and runs its listeners.
package service

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"
)

type Service struct {
	cfg    Config
	log    *slog.Logger
	device *http.ServeMux
	admin  *http.ServeMux
}

func New(cfg Config, log *slog.Logger) (*Service, error) {
	s := &Service{
		cfg:    cfg,
		log:    log,
		device: http.NewServeMux(),
		admin:  http.NewServeMux(),
	}
	s.admin.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	return s, nil
}

// Device is the handler for the device listener.
func (s *Service) Device() http.Handler { return s.device }

// Admin is the handler for the admin listener.
func (s *Service) Admin() http.Handler { return s.admin }

// Run serves both listeners until ctx is cancelled or one of them fails.
func (s *Service) Run(ctx context.Context) error {
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
