package service_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xaroth/dashboard/service"
	"github.com/xaroth/dashboard/service/render"
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

// stubRenderer has one page, hello, and renders a white image.
type stubRenderer struct{ closed atomic.Bool }

func (s *stubRenderer) Describe(context.Context) ([]render.PageInfo, string, error) {
	return []render.PageInfo{{Name: "hello"}}, "v1", nil
}

func (s *stubRenderer) Render(_ context.Context, _ string, vp render.Viewport, _ any) ([]byte, error) {
	var buf bytes.Buffer
	err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, vp.Width, vp.Height)))
	return buf.Bytes(), err
}

func (s *stubRenderer) Hash() string { return "v1" }

func (s *stubRenderer) Close() error {
	s.closed.Store(true)
	return nil
}

func testConfig(device, admin string) service.Config {
	cfg := service.DefaultConfig()
	cfg.Listen = service.Listen{Device: device, Admin: admin}
	cfg.Devices = map[string]service.Device{"hallway": {MAC: "aa:bb:cc:dd:ee:ff", Pages: []string{"hello"}}}
	return cfg
}

func newService(tb testing.TB, cfg service.Config) (*service.Service, *stubRenderer) {
	tb.Helper()
	stub := &stubRenderer{}
	s, err := service.New(tb.Context(), cfg, slog.New(slog.DiscardHandler), service.WithRenderer(stub))
	if err != nil {
		tb.Fatalf("New: %v", err)
	}
	return s, stub
}

func TestNewRejectsUnknownPage(t *testing.T) {
	t.Parallel()
	cfg := testConfig("", "")
	cfg.Devices["hallway"] = service.Device{MAC: "aa:bb:cc:dd:ee:ff", Pages: []string{"hello", "nope"}}
	stub := &stubRenderer{}
	_, err := service.New(t.Context(), cfg, slog.New(slog.DiscardHandler), service.WithRenderer(stub))
	if err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("got %v, want error naming the missing page", err)
	}
	if !stub.closed.Load() {
		t.Error("renderer not closed after failed New")
	}
}

func TestDevicePreview(t *testing.T) {
	t.Parallel()
	s, _ := newService(t, testConfig("", ""))
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
	s, _ := newService(t, testConfig("", ""))
	rec := httptest.NewRecorder()
	s.Admin().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
		t.Errorf("got %d %q, want 200 \"ok\\n\"", rec.Code, rec.Body.String())
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	t.Parallel()
	s, stub := newService(t, testConfig(freeAddr(t), freeAddr(t)))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("got %v, want nil", err)
		}
		if !stub.closed.Load() {
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
	s, _ := newService(t, testConfig(l.Addr().String(), freeAddr(t)))
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

func TestNormalizeMAC(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"colons", "aa:bb:cc:dd:ee:ff", "AA:BB:CC:DD:EE:FF", false},
		{"dashes", "AA-BB-CC-DD-EE-FF", "AA:BB:CC:DD:EE:FF", false},
		{"dots", "aabb.ccdd.eeff", "AA:BB:CC:DD:EE:FF", false},
		{"empty", "", "", true},
		{"garbage", "nope", "", true},
		{"eui-64", "aa:bb:cc:dd:ee:ff:00:11", "", true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got, err := service.NormalizeMAC(testCase.in)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("got error %v, want error %v", err, testCase.wantErr)
			}
			if got != testCase.want {
				t.Errorf("got %q, want %q", got, testCase.want)
			}
		})
	}
}
