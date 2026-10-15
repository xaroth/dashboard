package service_test

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xaroth/dashboard/service"
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

func newService(tb testing.TB, device, admin string) *service.Service {
	tb.Helper()
	cfg := service.DefaultConfig()
	cfg.Listen = service.Listen{Device: device, Admin: admin}
	s, err := service.New(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		tb.Fatalf("New: %v", err)
	}
	return s
}

func TestHealthz(t *testing.T) {
	t.Parallel()
	s := newService(t, "", "")
	rec := httptest.NewRecorder()
	s.Admin().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
		t.Errorf("got %d %q, want 200 \"ok\\n\"", rec.Code, rec.Body.String())
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	t.Parallel()
	s := newService(t, freeAddr(t), freeAddr(t))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("got %v, want nil", err)
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
	s := newService(t, l.Addr().String(), freeAddr(t))
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
