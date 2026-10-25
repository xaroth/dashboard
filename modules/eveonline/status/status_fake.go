package status

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// Fake answers ESI's /status with a player count, or 503 while offline,
// for moduletest.HTTPClient.
type Fake struct {
	mu      sync.Mutex
	players int64
}

// FakeOption sets up a Fake.
type FakeOption func(testing.TB, *Fake)

// NewFake returns an offline Fake, then opts applied.
func NewFake(tb testing.TB, opts ...FakeOption) *Fake {
	tb.Helper()
	f := &Fake{}
	for _, opt := range opts {
		opt(tb, f)
	}
	return f
}

// ExpectPlayers brings the server online with n players; 0 is offline.
func ExpectPlayers(n int64) FakeOption {
	return func(_ testing.TB, f *Fake) { f.players = n }
}

// Set applies opts to a serving Fake.
func (f *Fake) Set(tb testing.TB, opts ...FakeOption) {
	tb.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, opt := range opts {
		opt(tb, f)
	}
}

func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path != "/status" {
		http.NotFound(w, r)
		return
	}
	if f.players == 0 {
		http.Error(w, `{"error":"unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	fmt.Fprintf(w, `{"players":%d,"server_version":"1","start_time":"2000-01-01T00:00:00Z"}`, f.players)
}
