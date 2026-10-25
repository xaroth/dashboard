package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHealthzCommand(t *testing.T) {
	t.Parallel()
	var status atomic.Int32
	status.Store(http.StatusOK)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(int(status.Load()))
	}))
	addr := strings.TrimPrefix(srv.URL, "http://")

	if err := healthz(t.Context(), addr); err != nil {
		t.Errorf("healthy: got %v, want nil", err)
	}
	status.Store(http.StatusServiceUnavailable)
	if err := healthz(t.Context(), addr); err == nil {
		t.Error("unhealthy: got nil, want an error")
	}
	srv.Close()
	if err := healthz(t.Context(), addr); err == nil {
		t.Error("down: got nil, want an error")
	}
}
