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
	"syscall"
	"time"
)

// Raw renderer output; generous, the RGBA PNG is reduced before it is served.
const maxPNG = 64 << 20

const (
	minBackoff = 500 * time.Millisecond
	maxBackoff = 30 * time.Second

	// A response that takes longer means the renderer hangs; it is killed so
	// the requests queued behind it get a working one. Describe may bundle.
	renderTimeout   = 30 * time.Second
	describeTimeout = 60 * time.Second
)

var ErrClosed = errors.New("renderer closed")

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

// Process runs the Bun renderer and talks to it one request at a time. A dead,
// hung or desynced process is replaced on the next request, with backoff
// between starts.
type Process struct {
	command         []string
	dir             string
	log             *slog.Logger
	renderTimeout   time.Duration
	describeTimeout time.Duration
	now             func() time.Time
	after           func(time.Duration) <-chan time.Time

	turn      chan struct{} // held by the one request in flight; select-able unlike a mutex
	closing   chan struct{}
	closeOnce sync.Once

	// Guarded by turn.
	nextID   int64
	failures int
	lastExit time.Time

	// Written with turn held; read by Close without it.
	curMu sync.Mutex
	cur   *proc
}

type proc struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   *bufio.Reader
	exited   chan struct{}
	exitedAt time.Time // set before exited is closed
}

// NewProcess returns a Process that runs command in dir. The process starts
// on the first request.
func NewProcess(command []string, dir string, log *slog.Logger) *Process {
	return &Process{
		command:         command,
		dir:             dir,
		log:             log,
		renderTimeout:   renderTimeout,
		describeTimeout: describeTimeout,
		now:             time.Now,
		after:           time.After,
		turn:            make(chan struct{}, 1),
		closing:         make(chan struct{}),
	}
}

// Describe returns the bundled pages.
func (p *Process) Describe(ctx context.Context) ([]PageInfo, error) {
	resp, _, err := p.call(ctx, map[string]any{"op": "describe"}, p.describeTimeout)
	if err != nil {
		return nil, err
	}
	var pages []PageInfo
	if err := json.Unmarshal(resp.Pages, &pages); err != nil {
		return nil, fmt.Errorf("decoding describe: %w", err)
	}
	return pages, nil
}

// Render renders page with props, at props.Viewport, and returns the renderer's PNG.
func (p *Process) Render(ctx context.Context, page string, props Props) ([]byte, error) {
	_, body, err := p.call(ctx, map[string]any{"op": "render", "page": page, "props": props}, p.renderTimeout)
	if err != nil {
		return nil, err
	}
	if body == nil {
		return nil, errors.New("render response without png")
	}
	return body, nil
}

// Close stops the process, also while a request is in flight; later calls fail.
func (p *Process) Close() error {
	p.closeOnce.Do(func() { close(p.closing) })
	p.curMu.Lock()
	cur := p.cur
	p.curMu.Unlock()
	if cur != nil {
		cur.kill()
		<-cur.exited
	}
	return nil
}

// call sends req and waits up to timeout for its response. A caller that
// leaves early returns at once while the response is drained in the
// background, with turn held, so the process stays in step without a restart.
func (p *Process) call(ctx context.Context, req map[string]any, timeout time.Duration) (response, []byte, error) {
	select {
	case p.turn <- struct{}{}:
	case <-ctx.Done():
		return response{}, nil, fmt.Errorf("renderer: %w", ctx.Err())
	case <-p.closing:
		return response{}, nil, ErrClosed
	}
	draining := false
	defer func() {
		if !draining {
			<-p.turn
		}
	}()
	select {
	case <-p.closing:
		return response{}, nil, ErrClosed
	default:
	}
	if err := ctx.Err(); err != nil {
		return response{}, nil, fmt.Errorf("renderer: %w", err)
	}

	p.nextID++
	id := p.nextID
	req["id"] = id
	// Marshal before touching the process: bad props are the caller's error,
	// not a reason to restart the renderer.
	header, err := json.Marshal(req)
	if err != nil {
		return response{}, nil, fmt.Errorf("encoding request: %w", err)
	}
	cur, err := p.ensure(ctx)
	if err != nil {
		return response{}, nil, err
	}

	ch := make(chan result, 1)
	go func() {
		if err := writeFrame(cur.stdin, header); err != nil {
			ch <- result{err: fmt.Errorf("writing request: %w", err)}
			return
		}
		resp, body, err := readFrame(cur.stdout)
		ch <- result{resp, body, err}
	}()

	expired := p.after(timeout)
	select {
	case <-p.closing:
		return response{}, nil, ErrClosed
	case <-expired:
		p.timedOut(req["op"], timeout)
		return response{}, nil, fmt.Errorf("renderer: no response within %v: %w", timeout, context.DeadlineExceeded)
	case r := <-ch:
		return p.finish(r, id)
	case <-ctx.Done():
		draining = true
		go func() {
			defer func() { <-p.turn }()
			select {
			case <-p.closing:
			case <-expired:
				p.timedOut(req["op"], timeout)
			case r := <-ch:
				p.finish(r, id)
			}
		}()
		return response{}, nil, fmt.Errorf("renderer: %w", ctx.Err())
	}
}

type result struct {
	resp response
	body []byte
	err  error
}

// finish checks the response to request id and stops a process that is out
// of step. Called with turn held.
func (p *Process) finish(r result, id int64) (response, []byte, error) {
	if r.err != nil {
		p.stop()
		return response{}, nil, fmt.Errorf("renderer: %w", r.err)
	}
	if r.resp.ID != id {
		p.stop()
		return response{}, nil, fmt.Errorf("renderer: response id %d, want %d", r.resp.ID, id)
	}
	p.failures = 0
	if r.resp.Error != "" {
		return r.resp, nil, fmt.Errorf("renderer: %s", r.resp.Error)
	}
	return r.resp, r.body, nil
}

// timedOut replaces a renderer that did not answer in time. Called with turn held.
func (p *Process) timedOut(op any, timeout time.Duration) {
	p.log.Warn("renderer did not respond; restarting", "op", op, "timeout", timeout)
	p.stop()
}

// ensure returns the running process, starting one if needed after waiting
// out the backoff from earlier failures. Called with turn held.
func (p *Process) ensure(ctx context.Context) (*proc, error) {
	if p.cur != nil {
		select {
		case <-p.cur.exited:
			p.log.Warn("renderer exited", "state", p.cur.cmd.ProcessState)
			p.failures++
			p.lastExit = p.cur.exitedAt
			p.setCur(nil)
		default:
			return p.cur, nil
		}
	}
	if p.failures > 0 {
		wait := min(minBackoff<<min(p.failures-1, 10), maxBackoff) - p.now().Sub(p.lastExit)
		if wait > 0 {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("renderer: waiting to restart: %w", ctx.Err())
			case <-p.closing:
				return nil, ErrClosed
			case <-p.after(wait):
			}
		}
	}
	if len(p.command) == 0 {
		return nil, errors.New("renderer: no command")
	}

	cmd := exec.Command(p.command[0], p.command[1:]...)
	cmd.Dir = p.dir
	// Own process group, so a kill also reaches anything the renderer started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("renderer stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("renderer stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("renderer stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		p.failures++
		p.lastExit = p.now()
		return nil, fmt.Errorf("starting renderer: %w", err)
	}
	cur := &proc{cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdout, 1<<20), exited: make(chan struct{})}
	go func() {
		s := bufio.NewScanner(stderr)
		s.Buffer(make([]byte, 64<<10), 1<<20)
		for s.Scan() {
			p.log.Info(s.Text(), "source", "renderer")
		}
		// An overlong line stops the scanner; keep draining so the child never blocks.
		io.Copy(io.Discard, stderr)
		// Wait after stderr is drained, as os/exec requires.
		cmd.Wait()
		cur.exitedAt = p.now()
		close(cur.exited)
	}()
	p.setCur(cur)
	// Close reads cur after closing; checking closing after setting cur means
	// one of the two always sees the other.
	select {
	case <-p.closing:
		cur.kill()
		<-cur.exited
		return nil, ErrClosed
	default:
	}
	return cur, nil
}

// stop kills the current process and counts it as a failure for backoff.
// Called with turn held.
func (p *Process) stop() {
	cur := p.cur
	if cur == nil {
		return
	}
	p.setCur(nil)
	cur.kill()
	<-cur.exited
	p.failures++
	p.lastExit = p.now()
}

func (p *Process) setCur(c *proc) {
	p.curMu.Lock()
	p.cur = c
	p.curMu.Unlock()
}

func (c *proc) kill() {
	c.stdin.Close()
	select {
	case <-c.exited:
		// Reaped: the pid may already belong to someone else.
	default:
		syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
	}
}
