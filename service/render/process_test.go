package render

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as a fake renderer when this variable is set.
const fakeEnv = "DASHBOARD_FAKE_RENDERER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeEnv); mode != "" {
		fakeRenderer(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeRenderer(mode string) {
	in := bufio.NewReader(os.Stdin)
	for {
		var n [4]byte
		if _, err := in.Read(n[:1]); err != nil {
			return
		}
		if _, err := in.Read(n[1:]); err != nil {
			return
		}
		header := make([]byte, binary.BigEndian.Uint32(n[:]))
		if _, err := ioReadFull(in, header); err != nil {
			return
		}
		var req map[string]any
		json.Unmarshal(header, &req)
		id := int64(req["id"].(float64))

		switch {
		case mode == "crash":
			os.Exit(3)
		case mode == "hang" && req["op"] == "render":
			time.Sleep(time.Hour)
		case mode == "badid":
			id++
		}
		resp := map[string]any{"id": id, "hash": "abc123abc123"}
		var body []byte
		switch req["op"] {
		case "describe":
			resp["pages"] = []PageInfo{{Name: "hello", Data: map[string]QueryDescriptor{}}}
		case "render":
			if req["page"] == "nope" {
				resp["error"] = "unknown page nope"
				break
			}
			body = []byte("fakepng")
			resp["png"] = len(body)
		}
		writeFrame(os.Stdout, resp)
		os.Stdout.Write(body)
	}
}

func ioReadFull(r *bufio.Reader, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := r.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func fake(t *testing.T, mode string) *Process {
	t.Helper()
	t.Setenv(fakeEnv, mode)
	p := &Process{Command: []string{os.Args[0], "-test.run=^$"}, Log: slog.New(slog.DiscardHandler)}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestProcessDescribeAndRender(t *testing.T) {
	p := fake(t, "ok")
	ctx := context.Background()
	pages, hash, err := p.Describe(ctx)
	if err != nil || len(pages) != 1 || pages[0].Name != "hello" || hash != "abc123abc123" {
		t.Fatalf("Describe = %+v, %q, %v", pages, hash, err)
	}
	png, err := p.Render(ctx, "hello", Viewport{800, 480, false}, map[string]any{})
	if err != nil || string(png) != "fakepng" {
		t.Fatalf("Render = %q, %v", png, err)
	}
	if _, err := p.Render(ctx, "nope", Viewport{}, nil); err == nil || !strings.Contains(err.Error(), "unknown page") {
		t.Errorf("Render(nope) = %v", err)
	}
	// An error response leaves the process usable.
	if _, err := p.Render(ctx, "hello", Viewport{}, nil); err != nil {
		t.Errorf("Render after error = %v", err)
	}
	if p.Hash() != "abc123abc123" {
		t.Errorf("Hash = %q", p.Hash())
	}
}

func TestProcessTimeoutRestarts(t *testing.T) {
	p := fake(t, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := p.Render(ctx, "hello", Viewport{}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Render = %v, want deadline exceeded", err)
	}
	// The hung process was killed; a new one answers describe.
	if _, _, err := p.Describe(context.Background()); err != nil {
		t.Errorf("Describe after timeout = %v", err)
	}
}

func TestProcessCrashAndBackoff(t *testing.T) {
	p := fake(t, "crash")
	for range 2 {
		if _, _, err := p.Describe(context.Background()); err == nil {
			t.Fatal("Describe succeeded against a crashing renderer")
		}
	}
	// Two failures: the next start waits about 1s, longer than this deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, _, err := p.Describe(ctx); !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "waiting to restart") {
		t.Errorf("Describe during backoff = %v", err)
	}
}

func TestProcessResponseIDMismatch(t *testing.T) {
	p := fake(t, "badid")
	if _, _, err := p.Describe(context.Background()); err == nil || !strings.Contains(err.Error(), "response id") {
		t.Errorf("Describe = %v", err)
	}
}

func TestProcessClosed(t *testing.T) {
	p := fake(t, "ok")
	p.Close()
	if _, _, err := p.Describe(context.Background()); err == nil {
		t.Error("Describe after Close succeeded")
	}
}

// TestBunRenderer runs the real renderer when bun is available.
func TestBunRenderer(t *testing.T) {
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
	p := &Process{
		Command: []string{bun, "renderer/main.ts", "--pages", "pages", "--fonts", "fonts"},
		Dir:     root,
		Log:     slog.New(slog.DiscardHandler),
	}
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pages, _, err := p.Describe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) == 0 {
		t.Fatal("no pages")
	}
	vp := ViewportFor(Panels["reterminal_e1003"], 90)
	png, err := p.Render(ctx, "hello", vp, map[string]any{
		"data":     map[string]any{},
		"device":   map[string]any{"name": "test", "model": "reterminal_e1003", "rotation": 90},
		"system":   map[string]any{"staleModules": []string{}},
		"now":      "2026-10-16T05:30:00Z",
		"timezone": "Europe/Amsterdam",
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Finish(png, 90)
	if err != nil {
		t.Fatal(err)
	}
	if w, h := binary.BigEndian.Uint32(out[16:]), binary.BigEndian.Uint32(out[20:]); w != 1404 || h != 1872 {
		t.Errorf("size = %dx%d, want 1404x1872", w, h)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
