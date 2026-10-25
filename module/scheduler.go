package module

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	updateTimeout  = 2 * time.Minute
	enabledTimeout = 30 * time.Second
	maxJitter      = 30 * time.Second
	minBackoff     = 30 * time.Second
	maxBackoff     = 6 * time.Hour
	recordTimeout  = 10 * time.Second
)

// Run runs every module's schedule and the series store until ctx is
// cancelled.
func (s *Set) Run(ctx context.Context) {
	// The series loop stops after the updates, so its final flush has
	// their samples.
	sctx, stopSeries := context.WithCancel(context.WithoutCancel(ctx))
	var series sync.WaitGroup
	if s.series.Declared() {
		series.Go(func() { s.series.Run(sctx) })
	}
	defer func() {
		stopSeries()
		series.Wait()
	}()
	var wg sync.WaitGroup
	for _, inst := range s.instances {
		if inst.initErr != nil {
			continue
		}
		wg.Go(func() { s.schedule(ctx, inst) })
	}
	wg.Wait()
}

// schedule runs one module: jittered start, then Update every interval, or
// the delay it set with NextUpdate, while enabled. Failures back off
// exponentially from 30 s, not capped at the interval; ErrAuth pauses the
// schedule. A successful manual update resets the backoff and resumes a
// paused schedule, as a scheduled one would.
func (s *Set) schedule(ctx context.Context, inst *instance) {
	delay := rand.N(min(inst.interval, maxJitter))
	failures := 0
	phase := PhaseWaiting
	for {
		var timer <-chan time.Time // nil while paused: wait for a kick
		if phase == PhaseAuth {
			inst.set(phase, time.Time{})
		} else {
			inst.set(phase, s.now().Add(delay))
			timer = time.After(delay)
		}
		select {
		case <-ctx.Done():
			return
		case <-inst.kick:
			// The latest success wins, if a scheduled update ran after
			// the manual one that kicked.
			inst.mu.Lock()
			next := inst.next
			inst.mu.Unlock()
			failures = 0
			phase, delay = PhaseWaiting, next
			continue
		case <-timer:
		}

		on, next, err := s.scheduledUpdate(ctx, inst)
		if err == nil && !on {
			phase, delay = PhaseDisabled, inst.interval
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			failures = 0
			phase, delay = PhaseWaiting, next
			continue
		}
		if errors.Is(err, ErrAuth) {
			inst.log.Error("credentials rejected; no retries until a manual update succeeds", "err", err)
			phase = PhaseAuth
			continue
		}
		failures++
		phase, delay = PhaseFailed, backoff(failures)
		inst.log.Warn("update failed", "err", err, "retry_in", delay)
	}
}

// scheduledUpdate asks Enabled and, if on, runs an update. It skips both
// when an update succeeded while it waited for a manual one, as that was
// the update this one would repeat.
func (s *Set) scheduledUpdate(ctx context.Context, inst *instance) (on bool, next time.Duration, err error) {
	seen := inst.successCount()
	if err := inst.lock(ctx); err != nil {
		return false, 0, err
	}
	defer inst.unlock()
	inst.mu.Lock()
	skip, next := inst.successes != seen, inst.next
	inst.mu.Unlock()
	if skip {
		return true, next, nil
	}
	ectx, _ := s.context(ctx, inst, s.writer, false)
	ectx, cancel := context.WithTimeout(ectx, enabledTimeout)
	defer cancel()
	on, err = inst.reg.enabled(ectx, inst.module)
	if err != nil {
		err = fmt.Errorf("enabled: %w", err)
		s.recordFailure(ctx, inst, err)
		return false, 0, err
	}
	if !on {
		return false, 0, nil
	}
	inst.set(PhaseUpdating, time.Time{})
	next, err = s.runUpdate(ctx, inst)
	return true, next, err
}

// FlushSeries stores the buffered series samples now, as Run does every
// minute.
func (s *Set) FlushSeries(ctx context.Context) error { return s.series.Flush(ctx) }

// Update runs one update of the named module now and records the result,
// as the schedule does. It does not ask Enabled. On success the schedule
// starts over from it.
func (s *Set) Update(ctx context.Context, name string) error {
	for _, inst := range s.instances {
		if inst.reg.name != name {
			continue
		}
		if inst.initErr != nil {
			return fmt.Errorf("%s: initialize failed: %w", name, inst.initErr)
		}
		if err := inst.lock(ctx); err != nil {
			return err
		}
		_, err := s.runUpdate(ctx, inst)
		inst.unlock()
		if err != nil {
			return err
		}
		select {
		case inst.kick <- struct{}{}:
		default: // a kick is already pending; it reads the latest delay
		}
		return nil
	}
	return fmt.Errorf("%w %q", ErrUnknownModule, name)
}

// runUpdate calls Update with a timeout and records success, with the
// snapshot it saved, or failure. It returns the delay to the next update. A
// scheduled update starts its own trace; a manual one is part of the
// request's. The caller holds inst.updating.
func (s *Set) runUpdate(ctx context.Context, inst *instance) (next time.Duration, err error) {
	ctx, span := s.tracer.Start(ctx, inst.reg.name+".update", trace.WithAttributes(attribute.String("module", inst.reg.name)))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()
	uctx, state := s.context(ctx, inst, s.writer, true)
	uctx, cancel := context.WithTimeout(uctx, cmp.Or(inst.timeout, updateTimeout))
	defer cancel()
	start := time.Now()
	err = inst.reg.update(uctx, inst.module)
	if err != nil && ctx.Err() != nil {
		return 0, ctx.Err()
	}
	// The module already wrote its tables: record the result even if the
	// caller went away meanwhile, so stored state matches.
	rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer rcancel()
	s.metrics.updateDuration.WithLabelValues(inst.reg.name).Observe(time.Since(start).Seconds())
	if err != nil {
		result := "failure"
		if errors.Is(err, ErrAuth) {
			result = "auth"
		}
		s.metrics.updatesTotal.WithLabelValues(inst.reg.name, result).Inc()
		s.recordFailure(rctx, inst, err)
		return 0, err
	}
	s.metrics.updatesTotal.WithLabelValues(inst.reg.name, "success").Inc()
	at := s.now()
	s.metrics.lastSuccess.WithLabelValues(inst.reg.name).Set(float64(at.Unix()))
	// The update itself worked; a failed write here is not a source failure to back off from.
	if err := New(s.writer).RecordSuccess(rctx, RecordSuccessParams{Module: inst.reg.name, At: &at, Data: state.pending}); err != nil {
		inst.log.Error("recording update", "err", err)
	}
	next = cmp.Or(state.next, inst.interval)
	inst.mu.Lock()
	inst.next = next
	inst.successes++
	inst.mu.Unlock()
	return next, nil
}

func (s *Set) recordFailure(ctx context.Context, inst *instance, err error) {
	at := s.now()
	msg := err.Error()
	if rerr := New(s.writer).RecordFailure(ctx, RecordFailureParams{Module: inst.reg.name, At: &at, Error: &msg}); rerr != nil {
		inst.log.Error("recording update failure", "err", rerr)
	}
}

func backoff(failures int) time.Duration {
	return min(minBackoff<<min(failures-1, 20), maxBackoff)
}

// lock takes updating, unless ctx ends first.
func (i *instance) lock(ctx context.Context) error {
	select {
	case i.updating <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (i *instance) unlock() { <-i.updating }

func (i *instance) successCount() uint64 {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.successes
}

func (i *instance) set(p Phase, next time.Time) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.phase, i.nextRun = p, next
}

// Status is a module's state for the admin UI and the screens.
type Status struct {
	Name string
	// Page is whether the module has an admin page under /<name>/.
	Page        bool
	Phase       Phase
	Interval    time.Duration
	NextRun     time.Time // zero while updating or stopped
	AttemptedAt *time.Time
	FetchedAt   *time.Time
	Error       string
	Stale       bool
}

// Statuses returns every module's status, sorted by name.
func (s *Set) Statuses(ctx context.Context) ([]Status, error) {
	states, err := New(s.reader).ListStates(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading module states: %w", err)
	}
	byName := map[string]ModuleState{}
	for _, st := range states {
		byName[st.Module] = st
	}
	out := make([]Status, 0, len(s.instances))
	for _, inst := range s.instances {
		out = append(out, s.status(inst, byName[inst.reg.name]))
	}
	return out, nil
}

// StaleModules returns the names of modules whose data is stale.
func (s *Set) StaleModules(ctx context.Context) ([]string, error) {
	st, err := s.Statuses(ctx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, m := range st {
		if m.Stale {
			out = append(out, m.Name)
		}
	}
	return out, nil
}

func (s *Set) state(ctx context.Context, inst *instance) (Status, error) {
	row, err := New(s.reader).GetState(ctx, inst.reg.name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Status{}, fmt.Errorf("reading %s state: %w", inst.reg.name, err)
	}
	return s.status(inst, row), nil
}

func (s *Set) status(inst *instance, row ModuleState) Status {
	inst.mu.Lock()
	st := Status{Name: inst.reg.name, Phase: inst.phase, Interval: inst.interval, NextRun: inst.nextRun}
	_, st.Page = inst.page()
	inst.mu.Unlock()
	st.AttemptedAt, st.FetchedAt = row.AttemptedAt, row.FetchedAt
	st.Error = cmp.Or(errString(inst.initErr), deref(row.Error))
	st.Stale = row.FetchedAt == nil || s.now().Sub(*row.FetchedAt) > inst.staleAfter
	return st
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
