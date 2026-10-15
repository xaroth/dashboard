package render

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"time"
)

// Raw renderer output; generous, the RGBA PNG is reduced before it is served.
const maxPNG = 64 << 20

const (
	minBackoff = 500 * time.Millisecond
	maxBackoff = 30 * time.Second
)

// Viewport is the size a page is rendered at, after rotation.
type Viewport struct {
	Width    int  `json:"width"`
	Height   int  `json:"height"`
	Portrait bool `json:"portrait"`
}

// PageInfo is a page as reported by describe.
type PageInfo struct {
	Name string                     `json:"name"`
	Data map[string]QueryDescriptor `json:"data"`
}

type QueryDescriptor struct {
	Module string         `json:"module"`
	Query  string         `json:"query"`
	Params map[string]any `json:"params"`
}

// Process runs the Bun renderer and talks to it one request at a time. A dead
// or desynced process is replaced on the next request, with backoff between starts.
type Process struct {
	Command []string
	Dir     string
	Log     *slog.Logger

	mu       sync.Mutex
	cur      *proc
	nextID   int64
	hash     string
	failures int
	lastExit time.Time
	closed   bool
}

type proc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	exited chan struct{}
}

// Describe returns the bundled pages and the bundle hash.
func (p *Process) Describe(ctx context.Context) ([]PageInfo, string, error) {
	resp, _, err := p.call(ctx, map[string]any{"op": "describe"})
	if err != nil {
		return nil, "", err
	}
	var pages []PageInfo
	if err := json.Unmarshal(resp.Pages, &pages); err != nil {
		return nil, "", fmt.Errorf("decoding describe: %w", err)
	}
	return pages, resp.Hash, nil
}

// Render renders page with props and returns the renderer's PNG.
func (p *Process) Render(ctx context.Context, page string, vp Viewport, props any) ([]byte, error) {
	_, body, err := p.call(ctx, map[string]any{"op": "render", "page": page, "viewport": vp, "props": props})
	if err != nil {
		return nil, err
	}
	if body == nil {
		return nil, errors.New("render response without png")
	}
	return body, nil
}

// Hash is the bundle hash from the latest response; it changes after a rebuild.
func (p *Process) Hash() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hash
}

// Close stops the process; later calls fail.
func (p *Process) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.stop()
	return nil
}

func (p *Process) call(ctx context.Context, req map[string]any) (response, []byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return response{}, nil, errors.New("renderer closed")
	}
	if err := p.ensure(ctx); err != nil {
		return response{}, nil, err
	}
	p.nextID++
	id := p.nextID
	req["id"] = id

	type result struct {
		resp response
		body []byte
		err  error
	}
	cur := p.cur
	ch := make(chan result, 1)
	go func() {
		if err := writeFrame(cur.stdin, req); err != nil {
			ch <- result{err: fmt.Errorf("writing request: %w", err)}
			return
		}
		resp, body, err := readFrame(cur.stdout)
		ch <- result{resp, body, err}
	}()

	select {
	case <-ctx.Done():
		// The response may still arrive; the stream is no longer in step.
		p.stop()
		return response{}, nil, fmt.Errorf("renderer: %w", ctx.Err())
	case r := <-ch:
		if r.err != nil {
			p.stop()
			return response{}, nil, fmt.Errorf("renderer: %w", r.err)
		}
		if r.resp.ID != id {
			p.stop()
			return response{}, nil, fmt.Errorf("renderer: response id %d, want %d", r.resp.ID, id)
		}
		p.failures = 0
		if r.resp.Hash != "" {
			p.hash = r.resp.Hash
		}
		if r.resp.Error != "" {
			return r.resp, nil, fmt.Errorf("renderer: %s", r.resp.Error)
		}
		return r.resp, r.body, nil
	}
}

// ensure starts the process if none is running, waiting out the backoff
// after earlier failures. Called with mu held.
func (p *Process) ensure(ctx context.Context) error {
	if p.cur != nil {
		select {
		case <-p.cur.exited:
			p.Log.Warn("renderer exited", "err", p.cur.cmd.ProcessState)
			p.stop()
		default:
			return nil
		}
	}
	if p.failures > 0 {
		wait := min(minBackoff<<min(p.failures-1, 10), maxBackoff) - time.Since(p.lastExit)
		if wait > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("renderer: waiting to restart: %w", ctx.Err())
			case <-time.After(wait):
			}
		}
	}

	cmd := exec.Command(p.Command[0], p.Command[1:]...)
	cmd.Dir = p.Dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		p.failures++
		p.lastExit = time.Now()
		return fmt.Errorf("starting renderer: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		s := bufio.NewScanner(stderr)
		s.Buffer(make([]byte, 64<<10), 1<<20)
		for s.Scan() {
			p.Log.Info(s.Text(), "source", "renderer")
		}
		// An overlong line stops the scanner; keep draining so the child never blocks.
		io.Copy(io.Discard, stderr)
		// Wait after stderr is drained, as os/exec requires.
		cmd.Wait()
		close(exited)
	}()
	p.cur = &proc{cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdout, 1<<20), exited: exited}
	return nil
}

// stop kills the current process and counts it as a failure for backoff.
// Called with mu held.
func (p *Process) stop() {
	if p.cur == nil {
		return
	}
	p.cur.stdin.Close()
	p.cur.cmd.Process.Kill()
	<-p.cur.exited
	p.cur = nil
	p.failures++
	p.lastExit = time.Now()
}
