// package main cannot be imported, so its tests are internal.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image/png"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/render"
	"github.com/xaroth/dashboard/series"
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
	cfg, err := parseConfig([]byte("public_url: http://dash\n"), env(nil))
	if err != nil {
		tb.Fatalf("parsing config: %v", err)
	}
	cfg.DataDir = tb.TempDir()
	cfg.Listen = Listen{Device: device, Admin: admin}
	cfg.Devices = map[string]Device{"hallway": {MAC: "AA:BB:CC:DD:EE:FF", Layout: "e1001", Pages: []string{"hello"}}}
	return cfg
}

func newService(tb testing.TB, cfg Config) (*Service, *render.FakeRenderer) {
	tb.Helper()
	stub := render.NewFakeRenderer(tb, render.ExpectPages(render.PageInfo{Name: "e1001/hello"}))
	s, err := New(tb.Context(), cfg, slog.New(slog.DiscardHandler), WithRenderer(stub))
	if err != nil {
		tb.Fatalf("New: %v", err)
	}
	return s, stub
}

func TestNewErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name   string
		mutate func(testing.TB, *Config)
		want   string
	}{
		{"unknown page", func(_ testing.TB, cfg *Config) {
			cfg.Devices["hallway"] = Device{MAC: "AA:BB:CC:DD:EE:FF", Layout: "e1001", Pages: []string{"hello", "nope"}}
		}, `checking pages: devices.hallway: page "nope" does not exist`},
		{"unusable data dir", func(tb testing.TB, cfg *Config) {
			// A file where the data directory should be.
			cfg.DataDir = filepath.Join(tb.TempDir(), "file")
			if err := os.WriteFile(cfg.DataDir, nil, 0o644); err != nil {
				tb.Fatalf("writing file: %v", err)
			}
		}, "opening storage"},
		{"unknown module", func(_ testing.TB, cfg *Config) {
			cfg.Modules = map[string]yaml.Node{"nope": {}}
		}, "no such module"},
		{"invalid device schedule", func(_ testing.TB, cfg *Config) {
			cfg.Schedule.USB = 0
		}, "loading devices: schedule"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConfig(t, "", "")
			testCase.mutate(t, &cfg)
			stub := render.NewFakeRenderer(t, render.ExpectPages(render.PageInfo{Name: "e1001/hello"}))
			_, err := New(t.Context(), cfg, slog.New(slog.DiscardHandler), WithRenderer(stub))
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("got %v, want error containing %q", err, testCase.want)
			}
			if !stub.Closed() {
				t.Error("got renderer open after a failed New, want it closed")
			}
		})
	}
}

func TestNewRejectsReservedAdminPage(t *testing.T) {
	t.Parallel()
	images := module.DefineShared("images", func() *struct{} { return &struct{}{} },
		func(context.Context, *struct{}, []struct{}, module.Env) (loginsPage, error) { return loginsPage{}, nil })
	stub := render.NewFakeRenderer(t, render.ExpectPages(render.PageInfo{Name: "e1001/hello"}))
	_, err := New(t.Context(), testConfig(t, "", ""), slog.New(slog.DiscardHandler),
		WithRenderer(stub), WithRegistrations(adminRegistration("sun", module.WithShared(images))))
	if err == nil || !strings.Contains(err.Error(), `"images" has an admin page at /images/`) {
		t.Errorf("got %v, want the reserved prefix rejected", err)
	}
}

func TestNewCreatesDatabase(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t, "", "")
	newService(t, cfg)
	if _, err := os.Stat(filepath.Join(cfg.DataDir, storage.File)); err != nil {
		t.Errorf("got %v, want the database file", err)
	}
}

func TestDevicePreview(t *testing.T) {
	t.Parallel()
	s, _ := newService(t, testConfig(t, "", ""))
	rec := httptest.NewRecorder()
	s.Admin().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/preview/hallway/e1001/hello.png?model=reterminal_e1003", nil))
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

func TestSimulatorIsSeparate(t *testing.T) {
	t.Parallel()
	s, _ := newService(t, testConfig(t, "", ""))
	rec := httptest.NewRecorder()
	s.Admin().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/simulate/hallway/wake?source=button&model=reterminal_e1003", nil))
	var log []struct {
		Response map[string]any `json:"response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &log); err != nil || len(log) != 2 {
		t.Fatalf("simulated wake: got %d %s, want setup then display", rec.Code, rec.Body)
	}
	image := "/images/" + log[1].Response["filename"].(string) + ".png"
	if rec := get(t, s.Admin(), image); rec.Code != http.StatusOK {
		t.Errorf("admin listener: got %d for the simulated image, want 200", rec.Code)
	}
	// The device listener has its own image store; the simulator never reaches it.
	if rec := get(t, s.device, image); rec.Code != http.StatusNotFound {
		t.Errorf("device listener: got %d for the simulated image, want 404", rec.Code)
	}

	// The real device still has no reported model or wake.
	if body := get(t, s.Admin(), "/").Body.String(); !strings.Contains(body, "AA:BB:CC:DD:EE:FF · reterminal_e1001") {
		t.Error("got the real device's model changed by the simulated wake, want it unchanged")
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
			t.Error("got renderer open after Run, want it closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

type failingCloseRenderer struct{ *render.FakeRenderer }

func (f failingCloseRenderer) Close() error {
	return errors.Join(f.FakeRenderer.Close(), errCloseRenderer)
}

var errCloseRenderer = errors.New("renderer close failed")

func TestRunReturnsCloseError(t *testing.T) {
	t.Parallel()
	stub := failingCloseRenderer{render.NewFakeRenderer(t, render.ExpectPages(render.PageInfo{Name: "e1001/hello"}))}
	s, err := New(t.Context(), testConfig(t, freeAddr(t), freeAddr(t)), slog.New(slog.DiscardHandler), WithRenderer(stub))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.Run(ctx); !errors.Is(err, errCloseRenderer) {
		t.Errorf("got %v, want the renderer close error", err)
	}
}

// blockingRenderer renders until the request's context ends, and records
// whether it was closed while a render was running.
type blockingRenderer struct {
	*render.FakeRenderer
	started            chan struct{}
	rendering          atomic.Bool
	closedDuringRender atomic.Bool
}

func (b *blockingRenderer) Render(ctx context.Context, _ string, _ render.Props) ([]byte, error) {
	b.rendering.Store(true)
	defer b.rendering.Store(false)
	close(b.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (b *blockingRenderer) Close() error {
	b.closedDuringRender.Store(b.rendering.Load())
	return b.FakeRenderer.Close()
}

func TestRunCancelsRequestsAfterDrain(t *testing.T) {
	t.Parallel()
	admin := freeAddr(t)
	r := &blockingRenderer{FakeRenderer: render.NewFakeRenderer(t, render.ExpectPages(render.PageInfo{Name: "e1001/hello"})), started: make(chan struct{})}
	s, err := New(t.Context(), testConfig(t, freeAddr(t), admin), slog.New(slog.DiscardHandler), WithRenderer(r))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.shutdownGrace = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	// Polls the listener until it is up; the request then blocks in Render.
	go func() {
		for ctx.Err() == nil {
			resp, err := http.Get("http://" + admin + "/preview/page/e1001/hello.png")
			if err == nil {
				resp.Body.Close()
				return
			}
			runtime.Gosched()
		}
	}()
	select {
	case <-r.started:
	case <-time.After(10 * time.Second):
		t.Fatal("render never started")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("got %v, want nil: a drain timeout is not a failure", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return: the running request was not cancelled")
	}
	if r.closedDuringRender.Load() {
		t.Error("got renderer closed while a request was still rendering, want it closed after")
	}
	if !r.Closed() {
		t.Error("got renderer open after Run, want it closed")
	}
}

// drainConfig carries the test's channels, so each test has its own.
type drainConfig struct{ started, release chan struct{} }

// drainModule's update adds a sample, blocks until release, then adds
// another. Its schedule never updates.
type drainModule struct{ cfg *drainConfig }

func (m *drainModule) Initialize(_ context.Context, cfg *drainConfig, _ module.Env) error {
	m.cfg = cfg
	return nil
}

func (*drainModule) Enabled(context.Context, module.Query) (bool, error) { return false, nil }

func (m *drainModule) Update(_ context.Context, u *module.Update) error {
	if err := u.AddSample("power", u.Now().Add(-time.Minute), 1); err != nil {
		return err
	}
	close(m.cfg.started)
	<-m.cfg.release
	return u.AddSample("power", u.Now(), 2)
}

func countSamples(tb testing.TB, db *sql.DB) int {
	tb.Helper()
	var n int
	if err := db.QueryRowContext(tb.Context(), "SELECT count(*) FROM series_raw").Scan(&n); err != nil {
		tb.Fatalf("counting samples: %v", err)
	}
	return n
}

func TestRunFlushesSeriesAfterDrain(t *testing.T) {
	t.Parallel()
	admin := freeAddr(t)
	cfg := testConfig(t, freeAddr(t), admin)
	drain := &drainConfig{started: make(chan struct{}), release: make(chan struct{})}
	stub := render.NewFakeRenderer(t, render.ExpectPages(render.PageInfo{Name: "e1001/hello"}))
	s, err := New(t.Context(), cfg, slog.New(slog.DiscardHandler), WithRenderer(stub),
		WithRegistrations(module.Define[drainModule, drainConfig]("meter", func() *drainConfig { return drain },
			module.WithSeries("power", series.Rate))))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	// Polls the listener until it is up; the update then blocks.
	go func() {
		for ctx.Err() == nil {
			resp, err := http.Post("http://"+admin+"/modules/meter/update", "", nil)
			if err == nil {
				resp.Body.Close()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	select {
	case <-drain.started:
	case <-time.After(10 * time.Second):
		t.Fatal("update never started")
	}
	cancel()
	// The series loop's final flush stores the first sample; the second
	// comes after it.
	for deadline := time.Now().Add(10 * time.Second); countSamples(t, s.db.Reader) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the series loop never flushed")
		}
	}
	close(drain.release)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}

	db, err := storage.Open(t.Context(), cfg.DataDir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	defer db.Close()
	if n := countSamples(t, db.Reader); n != 2 {
		t.Errorf("got %d samples stored, want both of the drained update's", n)
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
