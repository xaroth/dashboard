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
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/service/render"
)

// The test binary doubles as a fake renderer: <binary> fake-renderer <mode>.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "fake-renderer" {
		fakeRenderer(os.Args[2])
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeRenderer speaks the frame protocol. Its bundle hash is its pid, so tests
// can tell whether the process was restarted. Modes: ok, hang (never answers
// render), crash (exits on the first request), badid.
func fakeRenderer(mode string) {
	in := bufio.NewReader(os.Stdin)
	hash := fmt.Sprint(os.Getpid())
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
		case mode == "hang" && req.Op == "render":
			fmt.Fprintln(os.Stderr, "hanging")
			time.Sleep(time.Hour)
		case mode == "badid":
			req.ID++
		}
		resp := map[string]any{"id": req.ID, "hash": hash}
		var body []byte
		switch {
		case req.Op == "describe":
			resp["pages"] = []render.PageInfo{{Name: "hello", Data: map[string]render.QueryDescriptor{}}}
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
func (s logSignal) WithAttrs([]slog.Attr) slog.Handler        { return s }
func (s logSignal) WithGroup(string) slog.Handler             { return s }
func (s logSignal) Handle(_ context.Context, r slog.Record) error {
	select {
	case s <- r.Message:
	default:
	}
	return nil
}

func newFake(tb testing.TB, mode string) (*render.Process, logSignal) {
	tb.Helper()
	logs := make(logSignal, 16)
	p := render.NewProcess([]string{os.Args[0], "fake-renderer", mode}, "", slog.New(logs))
	tb.Cleanup(func() { p.Close() })
	return p, logs
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
	p, _ := newFake(t, "ok")
	pages, hash, err := p.Describe(t.Context())
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	want := []render.PageInfo{{Name: "hello", Data: map[string]render.QueryDescriptor{}}}
	if diff := cmp.Diff(want, pages); diff != "" {
		t.Errorf("pages (-want +got):\n%s", diff)
	}
	if p.Hash() != hash {
		t.Errorf("got Hash %q, want %q", p.Hash(), hash)
	}

	png, err := p.Render(t.Context(), "hello", render.Viewport{Width: 800, Height: 480}, map[string]any{})
	if err != nil || string(png) != "fakepng" {
		t.Fatalf("got Render %q, %v, want fakepng", png, err)
	}
}

func TestProcessErrorResponseKeepsProcess(t *testing.T) {
	t.Parallel()
	p, _ := newFake(t, "ok")
	if _, err := p.Render(t.Context(), "nope", render.Viewport{}, nil); err == nil || !strings.Contains(err.Error(), "unknown page") {
		t.Errorf("got %v, want unknown page error", err)
	}
	before := p.Hash()
	if _, err := p.Render(t.Context(), "hello", render.Viewport{}, nil); err != nil {
		t.Fatalf("Render after error: %v", err)
	}
	if p.Hash() != before {
		t.Errorf("got pid %s, want %s: renderer restarted", p.Hash(), before)
	}
}

func TestProcessBadPropsKeepProcess(t *testing.T) {
	t.Parallel()
	p, _ := newFake(t, "ok")
	if _, _, err := p.Describe(t.Context()); err != nil {
		t.Fatalf("Describe: %v", err)
	}
	before := p.Hash()
	if _, err := p.Render(t.Context(), "hello", render.Viewport{}, map[string]any{"x": func() {}}); err == nil {
		t.Fatal("got nil error for unencodable props, want one")
	}
	if _, err := p.Render(t.Context(), "hello", render.Viewport{}, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if p.Hash() != before {
		t.Errorf("got pid %s, want %s: renderer restarted", p.Hash(), before)
	}
}

func TestProcessTimeoutRestarts(t *testing.T) {
	t.Parallel()
	p, _ := newFake(t, "hang")
	if _, _, err := p.Describe(t.Context()); err != nil {
		t.Fatalf("Describe: %v", err)
	}
	before := p.Hash()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if _, err := p.Render(ctx, "hello", render.Viewport{}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want deadline exceeded", err)
	}
	if _, _, err := p.Describe(t.Context()); err != nil {
		t.Fatalf("Describe after timeout: %v", err)
	}
	if p.Hash() == before {
		t.Error("got the same pid after a timeout, want a new process")
	}
}

func TestProcessCrashBacksOff(t *testing.T) {
	t.Parallel()
	p, _ := newFake(t, "crash")
	for range 2 {
		if _, _, err := p.Describe(t.Context()); err == nil {
			t.Fatal("got nil error from a crashing renderer, want one")
		}
	}
	// After two failures the next start waits about 1 s, longer than this deadline.
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, _, err := p.Describe(ctx); !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "waiting to restart") {
		t.Errorf("got %v, want deadline exceeded while waiting to restart", err)
	}
}

func TestProcessResponseIDMismatch(t *testing.T) {
	t.Parallel()
	p, _ := newFake(t, "badid")
	if _, _, err := p.Describe(t.Context()); err == nil || !strings.Contains(err.Error(), "response id") {
		t.Errorf("got %v, want response id error", err)
	}
}

func TestProcessCloseDuringHungRender(t *testing.T) {
	t.Parallel()
	p, logs := newFake(t, "hang")
	done := make(chan error, 1)
	go func() {
		_, err := p.Render(t.Context(), "hello", render.Viewport{}, nil)
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
	if _, _, err := p.Describe(t.Context()); !errors.Is(err, render.ErrClosed) {
		t.Errorf("got %v after Close, want ErrClosed", err)
	}
}

func TestProcessQueuedCallerHonorsContext(t *testing.T) {
	t.Parallel()
	p, logs := newFake(t, "hang")
	go p.Render(t.Context(), "hello", render.Viewport{}, nil)
	waitLog(t, logs, "hanging")

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, _, err := p.Describe(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("got %v, want deadline exceeded", err)
	}
}

func TestProcessConcurrent(t *testing.T) {
	t.Parallel()
	p, _ := newFake(t, "ok")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 10 {
				if _, err := p.Render(t.Context(), "hello", render.Viewport{}, nil); err != nil {
					t.Errorf("Render: %v", err)
				}
				p.Hash()
			}
		})
	}
	wg.Wait()
}

// TestBunRenderer runs the real renderer when bun is installed.
func TestBunRenderer(t *testing.T) {
	t.Parallel()
	bun, err := exec.LookPath("bun")
	if err != nil {
		home, _ := os.UserHomeDir()
		if bun = filepath.Join(home, ".bun", "bin", "bun"); !exists(bun) {
			t.Skip("bun not installed")
		}
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	if !exists(filepath.Join(root, "node_modules")) {
		t.Skip("node_modules missing; run bun install")
	}
	p := render.NewProcess([]string{bun, "renderer/main.ts", "--pages", "pages", "--fonts", "fonts"}, root, slog.New(slog.DiscardHandler))
	t.Cleanup(func() { p.Close() })

	pages, _, err := p.Describe(t.Context())
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("got no pages")
	}
	vp := render.ViewportFor(render.Panels["reterminal_e1003"], 90)
	raw, err := p.Render(t.Context(), "hello", vp, map[string]any{
		"data":     map[string]any{},
		"device":   map[string]any{"name": "test", "model": "reterminal_e1003", "rotation": 90},
		"system":   map[string]any{"staleModules": []string{}},
		"now":      "2026-10-16T05:30:00Z",
		"timezone": "Europe/Amsterdam",
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
