package render_test

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/render"
)

// The test binary doubles as a fake renderer: <binary> fake-renderer <mode>.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "fake-renderer" {
		fakeRenderer(os.Args[2])
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeRenderer speaks the frame protocol. Describe reports its pid as a page
// param, so tests can tell whether the process was restarted. Modes: ok, hang
// (never answers render), hangall (never answers), crash (exits on the first
// request), badid, gate:<fifo> (answers render once the fifo is opened for
// writing and closed).
func fakeRenderer(mode string) {
	mode, gate, _ := strings.Cut(mode, ":")
	in := bufio.NewReader(os.Stdin)
	pid := map[string]render.QueryDescriptor{"pid": {Params: map[string]any{"pid": fmt.Sprint(os.Getpid())}}}
	for {
		var n [4]byte
		if _, err := io.ReadFull(in, n[:]); err != nil {
			return
		}
		header := make([]byte, binary.BigEndian.Uint32(n[:]))
		if _, err := io.ReadFull(in, header); err != nil {
			return
		}
		var req struct {
			ID   int64  `json:"id"`
			Op   string `json:"op"`
			Page string `json:"page"`
		}
		json.Unmarshal(header, &req)

		switch {
		case mode == "crash":
			os.Exit(3)
		case mode == "hang" && req.Op == "render", mode == "hangall":
			fmt.Fprintln(os.Stderr, "hanging")
			time.Sleep(time.Hour)
		case mode == "gate" && req.Op == "render":
			fmt.Fprintln(os.Stderr, "gated")
			f, err := os.Open(gate) // blocks until the test opens it for writing
			if err != nil {
				os.Exit(4)
			}
			io.Copy(io.Discard, f)
			f.Close()
		case mode == "badid":
			req.ID++
		}
		resp := map[string]any{"id": req.ID}
		var body []byte
		switch {
		case req.Op == "describe":
			resp["pages"] = []render.PageInfo{{Name: "hello", Data: pid}}
		case req.Page == "nope":
			resp["error"] = "unknown page nope"
		default:
			body = []byte("fakepng")
			resp["png"] = len(body)
		}
		b, _ := json.Marshal(resp)
		binary.BigEndian.PutUint32(n[:], uint32(len(b)))
		os.Stdout.Write(n[:])
		os.Stdout.Write(b)
		os.Stdout.Write(body)
	}
}

// logSignal is a slog handler that reports each message on a channel.
type logSignal chan string

func (s logSignal) Enabled(context.Context, slog.Level) bool { return true }
func (s logSignal) WithAttrs([]slog.Attr) slog.Handler       { return s }
func (s logSignal) WithGroup(string) slog.Handler            { return s }
func (s logSignal) Handle(_ context.Context, r slog.Record) error {
	select {
	case s <- r.Message:
	default:
	}
	return nil
}

// Response timeouts of the fake process, told apart from backoff waits by length.
const (
	renderTimeout   = time.Hour
	describeTimeout = 2 * time.Hour
)

// fired is a timer that has gone off.
var fired = func() <-chan time.Time {
	c := make(chan time.Time)
	close(c)
	return c
}()

// newFake returns a Process running the fake renderer in mode. A response
// timeout goes off when its channel in timeouts does, and never when absent;
// restart backoff passes at once. opts apply last.
func newFake(tb testing.TB, mode string, timeouts map[time.Duration]<-chan time.Time, opts ...render.ProcessOption) (*render.Process, logSignal) {
	tb.Helper()
	logs := make(logSignal, 16)
	opts = append([]render.ProcessOption{
		render.WithTimeouts(renderTimeout, describeTimeout),
		render.WithClock(time.Now, func(d time.Duration) <-chan time.Time {
			if c, ok := timeouts[d]; ok {
				return c
			}
			if d == renderTimeout || d == describeTimeout {
				return nil
			}
			return fired
		}),
	}, opts...)
	p := render.NewProcess([]string{os.Args[0], "fake-renderer", mode}, "", slog.New(logs), opts...)
	tb.Cleanup(func() { p.Close() })
	return p, logs
}

// pid returns the fake renderer's pid, starting it if needed.
func pid(tb testing.TB, p *render.Process) any {
	tb.Helper()
	pages, err := p.Describe(tb.Context())
	if err != nil {
		tb.Fatalf("Describe: %v", err)
	}
	return pages[0].Data["pid"].Params["pid"]
}

// waitLog blocks until the renderer logs msg.
func waitLog(tb testing.TB, logs logSignal, msg string) {
	tb.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case got := <-logs:
			if got == msg {
				return
			}
		case <-timeout:
			tb.Fatalf("renderer did not log %q", msg)
		}
	}
}

func TestProcessDescribeAndRender(t *testing.T) {
	t.Parallel()
	p, _ := newFake(t, "ok", nil)
	pages, err := p.Describe(t.Context())
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	want := []render.PageInfo{{Name: "hello", Data: map[string]render.QueryDescriptor{
		"pid": {Params: map[string]any{"pid": pid(t, p)}},
	}}}
	if diff := cmp.Diff(want, pages); diff != "" {
		t.Errorf("pages (-want +got):\n%s", diff)
	}

	png, err := p.Render(t.Context(), "hello", render.Props{Viewport: render.Viewport{Width: 800, Height: 480}})
	if err != nil || string(png) != "fakepng" {
		t.Fatalf("got Render %q, %v, want fakepng", png, err)
	}
}

func TestProcessErrorResponseKeepsProcess(t *testing.T) {
	t.Parallel()
	p, _ := newFake(t, "ok", nil)
	before := pid(t, p)
	if _, err := p.Render(t.Context(), "nope", render.Props{}); err == nil || !strings.Contains(err.Error(), "unknown page") {
		t.Errorf("got %v, want unknown page error", err)
	}
	if _, err := p.Render(t.Context(), "hello", render.Props{}); err != nil {
		t.Fatalf("Render after error: %v", err)
	}
	if after := pid(t, p); after != before {
		t.Errorf("got pid %v, want %v: renderer restarted", after, before)
	}
}

func TestProcessBadPropsKeepProcess(t *testing.T) {
	t.Parallel()
	p, _ := newFake(t, "ok", nil)
	before := pid(t, p)
	bad := render.Props{Data: map[string]render.Result{"x": {Value: func() {}}}}
	if _, err := p.Render(t.Context(), "hello", bad); err == nil {
		t.Fatal("got nil error for unencodable props, want one")
	}
	if _, err := p.Render(t.Context(), "hello", render.Props{}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if after := pid(t, p); after != before {
		t.Errorf("got pid %v, want %v: renderer restarted", after, before)
	}
}

func TestProcessRenderTimeoutRestarts(t *testing.T) {
	t.Parallel()
	p, _ := newFake(t, "hang", map[time.Duration]<-chan time.Time{renderTimeout: fired})
	before := pid(t, p)
	// The caller has no deadline: the renderer's own timeout ends the wait.
	if _, err := p.Render(t.Context(), "hello", render.Props{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want deadline exceeded", err)
	}
	if pid(t, p) == before {
		t.Error("got the same pid after a timeout, want a new process")
	}
}

func TestProcessDescribeTimeout(t *testing.T) {
	t.Parallel()
	p, _ := newFake(t, "hangall", map[time.Duration]<-chan time.Time{describeTimeout: fired})
	if _, err := p.Describe(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("got %v, want deadline exceeded", err)
	}
}

func TestProcessCallerLeavingDrainsResponse(t *testing.T) {
	t.Parallel()
	gate := filepath.Join(t.TempDir(), "gate")
	if err := syscall.Mkfifo(gate, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	p, logs := newFake(t, "gate:"+gate, nil)
	before := pid(t, p)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := p.Render(ctx, "hello", render.Props{})
		done <- err
	}()
	waitLog(t, logs, "gated")
	cancel()
	// The caller returns while the renderer still works on its response.
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("got %v, want canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Render did not return after its caller left")
	}

	f, err := os.OpenFile(gate, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening gate: %v", err)
	}
	f.Close()
	if after := pid(t, p); after != before {
		t.Errorf("got pid %v, want %v: the renderer was restarted for a caller leaving", after, before)
	}
}

func TestProcessCallerLeavingHungRenderRestarts(t *testing.T) {
	t.Parallel()
	expire := make(chan time.Time)
	p, logs := newFake(t, "hang", map[time.Duration]<-chan time.Time{renderTimeout: expire})
	before := pid(t, p)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := p.Render(ctx, "hello", render.Props{})
		done <- err
	}()
	waitLog(t, logs, "hanging")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want canceled", err)
	}
	// The drain times out after the caller left.
	close(expire)
	if pid(t, p) == before {
		t.Error("got the same pid after the drain timed out, want a new process")
	}
}

func TestProcessCrashBacksOff(t *testing.T) {
	t.Parallel()
	// A frozen clock: each restart waits its full backoff.
	now := time.Unix(1792087200, 0)
	var waits []time.Duration
	p, _ := newFake(t, "crash", nil, render.WithClock(func() time.Time { return now }, func(d time.Duration) <-chan time.Time {
		if d == renderTimeout || d == describeTimeout {
			return nil
		}
		waits = append(waits, d)
		return fired
	}))
	for range 9 {
		if _, err := p.Describe(t.Context()); err == nil {
			t.Fatal("got nil error from a crashing renderer, want one")
		}
	}
	want := []time.Duration{
		500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second,
		8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second,
	}
	if diff := cmp.Diff(want, waits); diff != "" {
		t.Errorf("restart waits (-want +got):\n%s", diff)
	}
}

func TestProcessRestartWaitHonorsContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	now := time.Unix(1792087200, 0)
	p, _ := newFake(t, "crash", nil, render.WithClock(func() time.Time { return now }, func(d time.Duration) <-chan time.Time {
		if d == renderTimeout || d == describeTimeout {
			return nil
		}
		// The caller leaves while the restart waits.
		cancel()
		return nil
	}))
	if _, err := p.Describe(t.Context()); err == nil {
		t.Fatal("got nil error from a crashing renderer, want one")
	}
	if _, err := p.Describe(ctx); !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "waiting to restart") {
		t.Errorf("got %v, want canceled while waiting to restart", err)
	}
}

func TestProcessResponseIDMismatch(t *testing.T) {
	t.Parallel()
	p, _ := newFake(t, "badid", nil)
	if _, err := p.Describe(t.Context()); err == nil || !strings.Contains(err.Error(), "response id") {
		t.Errorf("got %v, want response id error", err)
	}
}

func TestProcessCloseDuringHungRender(t *testing.T) {
	t.Parallel()
	p, logs := newFake(t, "hang", nil)
	done := make(chan error, 1)
	go func() {
		_, err := p.Render(t.Context(), "hello", render.Props{})
		done <- err
	}()
	waitLog(t, logs, "hanging")
	p.Close()
	select {
	case err := <-done:
		if !errors.Is(err, render.ErrClosed) {
			t.Errorf("got %v, want ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Render still blocked after Close")
	}
	if _, err := p.Describe(t.Context()); !errors.Is(err, render.ErrClosed) {
		t.Errorf("got %v after Close, want ErrClosed", err)
	}
}

func TestProcessQueuedCallerHonorsContext(t *testing.T) {
	t.Parallel()
	p, logs := newFake(t, "hang", nil)
	go p.Render(t.Context(), "hello", render.Props{})
	waitLog(t, logs, "hanging")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.Describe(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want canceled", err)
	}
}

func TestProcessConcurrent(t *testing.T) {
	t.Parallel()
	p, _ := newFake(t, "ok", nil)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 10 {
				if _, err := p.Render(t.Context(), "hello", render.Props{}); err != nil {
					t.Errorf("Render: %v", err)
				}
			}
		})
	}
	wg.Wait()
}

// newBun returns a Process running the real renderer, skipping tb when bun
// or node_modules is missing.
func newBun(tb testing.TB) *render.Process {
	tb.Helper()
	bun, err := exec.LookPath("bun")
	if err != nil {
		home, _ := os.UserHomeDir()
		if bun = filepath.Join(home, ".bun", "bin", "bun"); !exists(bun) {
			tb.Skip("bun not installed")
		}
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..")
	if !exists(filepath.Join(root, "node_modules")) {
		tb.Skip("node_modules missing; run bun install")
	}
	p := render.NewProcess([]string{bun, "renderer/main.ts", "--pages", "pages"}, root, slog.New(slog.DiscardHandler))
	tb.Cleanup(func() { p.Close() })
	return p
}

// TestBunRenderer runs the real renderer when bun is installed.
func TestBunRenderer(t *testing.T) {
	t.Parallel()
	p := newBun(t)

	pages, err := p.Describe(t.Context())
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("got no pages, want some")
	}
	panel, _ := render.PanelFor("reterminal_e1003")
	raw, err := p.Render(t.Context(), "e1001/hello", render.Props{
		Data:     map[string]render.Result{},
		Device:   render.DeviceProps{Name: "test", Model: "reterminal_e1003", Rotation: 90},
		Viewport: render.ViewportFor(panel, 90),
		System:   render.SystemProps{StaleModules: []string{}},
		Now:      "2026-10-16T05:30:00Z",
		Timezone: "Europe/Amsterdam",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	out, err := render.Finish(raw, 90)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if w, h := pngSize(out); w != 1404 || h != 1872 {
		t.Errorf("got %dx%d, want 1404x1872", w, h)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
