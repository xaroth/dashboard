package modules

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/xaroth/dashboard/service/storage"
)

const (
	updateTimeout = 2 * time.Minute
	maxJitter     = 30 * time.Second
	minBackoff    = 30 * time.Second
	maxBackoff    = 6 * time.Hour
)

// Run runs every module's schedule until ctx is cancelled.
func (s *Set) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, inst := range s.instances {
		if inst.initErr != nil {
			continue
		}
		wg.Go(func() { s.schedule(ctx, inst) })
	}
	wg.Wait()
}

// schedule runs one module: jittered start, then Update every interval while
// enabled. Failures back off exponentially from 30 s, not capped at the
// interval; ErrAuth stops the schedule.
func (s *Set) schedule(ctx context.Context, inst *instance) {
	delay := rand.N(min(inst.interval, maxJitter))
	failures := 0
	phase := PhaseWaiting
	for {
		inst.set(phase, s.now().Add(delay))
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		uctx := s.context(ctx, inst, s.db.Writer)
		if !inst.reg.enabled(uctx, inst.module) {
			phase, delay = PhaseDisabled, inst.interval
			continue
		}

		inst.set(PhaseUpdating, time.Time{})
		err := s.update(uctx, inst)
		if ctx.Err() != nil {
			return
		}
		at := s.now()
		q := storage.New(s.db.Writer)
		if err == nil {
			if err := q.RecordSuccess(ctx, storage.RecordSuccessParams{Module: inst.reg.name, At: &at}); err != nil {
				inst.log.Error("recording update", "err", err)
			}
			failures = 0
			phase, delay = PhaseWaiting, inst.interval
			continue
		}

		msg := err.Error()
		if err := q.RecordFailure(ctx, storage.RecordFailureParams{Module: inst.reg.name, At: &at, Error: &msg}); err != nil {
			inst.log.Error("recording update failure", "err", err)
		}
		if errors.Is(err, ErrAuth) {
			inst.log.Error("credentials rejected; no retries until restart", "err", err)
			inst.set(PhaseAuth, time.Time{})
			return
		}
		failures++
		phase, delay = PhaseFailed, backoff(failures)
		inst.log.Warn("update failed", "err", err, "retry_in", delay)
	}
}

func (s *Set) update(ctx context.Context, inst *instance) error {
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	return inst.reg.update(ctx, inst.module)
}

func backoff(failures int) time.Duration {
	return min(minBackoff<<min(failures-1, 20), maxBackoff)
}

func (i *instance) set(p Phase, next time.Time) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.phase, i.nextRun = p, next
}

// Status is a module's state for the admin UI and the screens.
type Status struct {
	Name        string
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
	states, err := storage.New(s.db.Reader).ListStates(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	byName := map[string]storage.ModuleState{}
	for _, st := range states {
		byName[st.Module] = st
	}
	now := s.now()
	out := make([]Status, 0, len(s.instances))
	for _, inst := range s.instances {
		inst.mu.Lock()
		st := Status{Name: inst.reg.name, Phase: inst.phase, Interval: inst.interval, NextRun: inst.nextRun}
		inst.mu.Unlock()
		row := byName[inst.reg.name]
		st.AttemptedAt, st.FetchedAt = row.AttemptedAt, row.FetchedAt
		st.Error = cmp.Or(errString(inst.initErr), deref(row.Error))
		st.Stale = row.FetchedAt == nil || now.Sub(*row.FetchedAt) > inst.staleAfter
		out = append(out, st)
	}
	return out, nil
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
