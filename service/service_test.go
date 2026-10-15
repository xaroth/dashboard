package service

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func newTestService(t *testing.T, device, admin string) *Service {
	cfg := DefaultConfig()
	cfg.Listen = Listen{Device: device, Admin: admin}
	s, err := New(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHealthz(t *testing.T) {
	s := newTestService(t, "", "")
	rec := httptest.NewRecorder()
	s.admin.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 || rec.Body.String() != "ok\n" {
		t.Errorf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	admin := freeAddr(t)
	s := newTestService(t, freeAddr(t), admin)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	var resp *http.Response
	var err error
	for range 50 {
		if resp, err = http.Get("http://" + admin + "/healthz"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestRunFailsOnBindError(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	s := newTestService(t, l.Addr().String(), freeAddr(t))
	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("Run = nil, want bind error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestNormalizeMAC(t *testing.T) {
	for in, want := range map[string]string{
		"aa:bb:cc:dd:ee:ff": "AA:BB:CC:DD:EE:FF",
		"AA-BB-CC-DD-EE-FF": "AA:BB:CC:DD:EE:FF",
		"aabb.ccdd.eeff":    "AA:BB:CC:DD:EE:FF",
	} {
		if got, err := NormalizeMAC(in); err != nil || got != want {
			t.Errorf("NormalizeMAC(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "nope", "aa:bb:cc:dd:ee:ff:00:11"} {
		if _, err := NormalizeMAC(in); err == nil {
			t.Errorf("NormalizeMAC(%q) succeeded", in)
		}
	}
}
