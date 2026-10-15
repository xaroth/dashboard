package modules

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
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
		err := s.runUpdate(ctx, inst)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			failures = 0
			phase, delay = PhaseWaiting, inst.interval
			continue
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

// Update runs one update of the named module now and records the result,
// as the schedule does. It does not ask Enabled.
func (s *Set) Update(ctx context.Context, name string) error {
	for _, inst := range s.instances {
		if inst.reg.name != name {
			continue
		}
		if inst.initErr != nil {
			return fmt.Errorf("%s: initialize failed: %w", name, inst.initErr)
		}
		return s.runUpdate(ctx, inst)
	}
	return fmt.Errorf("%w %q", ErrUnknownModule, name)
}

// runUpdate calls Update with a timeout and records success, with the
// snapshot it saved, or failure.
func (s *Set) runUpdate(ctx context.Context, inst *instance) error {
	inst.updating.Lock()
	defer inst.updating.Unlock()
	state := &stateStore{q: storage.New(s.db.Writer), module: inst.reg.name, update: true}
	uctx, cancel := context.WithTimeout(WithState(s.context(ctx, inst, s.db.Writer), state), updateTimeout)
	defer cancel()
	err := inst.reg.update(uctx, inst.module)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	at := s.now()
	q := storage.New(s.db.Writer)
	if err == nil {
		// The update itself worked; a failed write here is not a source failure to back off from.
		if err := q.RecordSuccess(ctx, storage.RecordSuccessParams{Module: inst.reg.name, At: &at, Data: state.pending}); err != nil {
			inst.log.Error("recording update", "err", err)
		}
		return nil
	}
	msg := err.Error()
	if rerr := q.RecordFailure(ctx, storage.RecordFailureParams{Module: inst.reg.name, At: &at, Error: &msg}); rerr != nil {
		inst.log.Error("recording update failure", "err", rerr)
	}
	return err
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
	if err != nil {
		return nil, err
	}
	byName := map[string]storage.ModuleState{}
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
	row, err := storage.New(s.db.Reader).GetState(ctx, inst.reg.name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Status{}, err
	}
	return s.status(inst, row), nil
}

func (s *Set) status(inst *instance, row storage.ModuleState) Status {
	inst.mu.Lock()
	st := Status{Name: inst.reg.name, Phase: inst.phase, Interval: inst.interval, NextRun: inst.nextRun}
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
