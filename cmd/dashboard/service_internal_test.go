// package main cannot be imported, so its tests are internal.
package main

import (
	"context"
	"errors"
	"image/png"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/xaroth/dashboard/render"
	"github.com/xaroth/dashboard/storage"
)

// freeAddr returns an address that was free a moment ago.
func freeAddr(tb testing.TB) string {
	tb.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listening: %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}

func testConfig(tb testing.TB, device, admin string) Config {
	tb.Helper()
	cfg := DefaultConfig()
	cfg.DataDir = tb.TempDir()
	cfg.Listen = Listen{Device: device, Admin: admin}
	cfg.Devices = map[string]Device{"hallway": {MAC: "aa:bb:cc:dd:ee:ff", Pages: []string{"hello"}}}
	return cfg
}

func newService(tb testing.TB, cfg Config) (*Service, *render.FakeRenderer) {
	tb.Helper()
	stub := render.NewFakeRenderer(render.PageInfo{Name: "hello"})
	s, err := New(tb.Context(), cfg, slog.New(slog.DiscardHandler), WithRenderer(stub))
	if err != nil {
		tb.Fatalf("New: %v", err)
	}
	return s, stub
}

func TestNewRejectsUnknownPage(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t, "", "")
	cfg.Devices["hallway"] = Device{MAC: "aa:bb:cc:dd:ee:ff", Pages: []string{"hello", "nope"}}
	stub := render.NewFakeRenderer(render.PageInfo{Name: "hello"})
	_, err := New(t.Context(), cfg, slog.New(slog.DiscardHandler), WithRenderer(stub))
	if err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("got %v, want error naming the missing page", err)
	}
	if !stub.Closed() {
		t.Error("renderer not closed after failed New")
	}
}

func TestNewCreatesDatabase(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t, "", "")
	newService(t, cfg)
	if _, err := os.Stat(filepath.Join(cfg.DataDir, storage.File)); err != nil {
		t.Errorf("database file: %v", err)
	}
}

func TestNewFailsOnUnusableDataDir(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t, "", "")
	// A file where the data directory should be.
	cfg.DataDir = filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(cfg.DataDir, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stub := render.NewFakeRenderer(render.PageInfo{Name: "hello"})
	if _, err := New(t.Context(), cfg, slog.New(slog.DiscardHandler), WithRenderer(stub)); err == nil {
		t.Error("got nil error, want one")
	}
	if !stub.Closed() {
		t.Error("renderer not closed after failed New")
	}
}

func TestNewRejectsUnknownModule(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t, "", "")
	cfg.Modules = map[string]yaml.Node{"nope": {}}
	stub := render.NewFakeRenderer(render.PageInfo{Name: "hello"})
	_, err := New(t.Context(), cfg, slog.New(slog.DiscardHandler), WithRenderer(stub))
	if err == nil || !strings.Contains(err.Error(), "no such module") {
		t.Errorf("got %v, want an unknown module error", err)
	}
	if !stub.Closed() {
		t.Error("renderer not closed after failed New")
	}
}

func TestDevicePreview(t *testing.T) {
	t.Parallel()
	s, _ := newService(t, testConfig(t, "", ""))
	rec := httptest.NewRecorder()
	s.Admin().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/preview/hallway/hello.png?model=reterminal_e1003", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s, want 200", rec.Code, rec.Body)
	}
	cfg, err := png.DecodeConfig(rec.Body)
	if err != nil {
		t.Fatalf("decoding png: %v", err)
	}
	if cfg.Width != 1872 || cfg.Height != 1404 {
		t.Errorf("got %dx%d, want 1872x1404", cfg.Width, cfg.Height)
	}
}

func TestHealthz(t *testing.T) {
	t.Parallel()
	s, _ := newService(t, testConfig(t, "", ""))
	rec := httptest.NewRecorder()
	s.Admin().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
		t.Errorf("got %d %q, want 200 \"ok\\n\"", rec.Code, rec.Body.String())
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	t.Parallel()
	s, stub := newService(t, testConfig(t, freeAddr(t), freeAddr(t)))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("got %v, want nil", err)
		}
		if !stub.Closed() {
			t.Error("renderer not closed after Run")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRunFailsOnBindError(t *testing.T) {
	t.Parallel()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	defer l.Close()
	s, _ := newService(t, testConfig(t, l.Addr().String(), freeAddr(t)))
	done := make(chan error, 1)
	go func() { done <- s.Run(t.Context()) }()
	select {
	case err := <-done:
		if _, ok := errors.AsType[*net.OpError](err); !ok {
			t.Errorf("got %v, want a bind error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}
