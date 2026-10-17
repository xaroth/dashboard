// Package status polls the EVE Online server status from ESI.
package status

import (
	"cmp"
	"context"
	"embed"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/eve-online-tools/lib-esi-go/esi/getstatus"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/modules/eveonline"
)

//go:embed sql/migrations/*.sql
var migrations embed.FS

// Status is the snapshot one update stores and the server query returns.
type Status struct {
	Online        bool       `json:"online"`
	Players       int64      `json:"players"`
	ServerVersion string     `json:"serverVersion"`
	StartTime     *time.Time `json:"startTime"` // null while offline
	VIP           bool       `json:"vip"`
}

type config struct{}

type status struct{}

// Module is the eveonline_status registration.
var Module = module.Define[status, config](
	"eveonline_status",
	func() *config { return &config{} },
	// ESI caches /status for 30 s; screens refresh far less often.
	module.WithUpdateInterval(5*time.Minute),
	module.WithStaleAfter(30*time.Minute),
	module.WithShared(eveonline.ESI),
	module.WithMigrations(migrations),
	module.WithQuery("server", (*status).server),
	module.WithQuery("history", (*status).history),
)

func (*status) Initialize(context.Context, *config) error { return nil }

func (*status) Enabled(context.Context) bool { return true }

func (*status) Update(ctx context.Context) error {
	st, err := fetch(ctx)
	if err != nil {
		return err
	}
	err = New(module.Database(ctx)).SaveSample(ctx, SaveSampleParams{
		At: module.Clock(ctx)().Unix(), Online: st.Online, Players: st.Players,
	})
	if err != nil {
		return fmt.Errorf("saving sample: %w", err)
	}
	return module.SaveSnapshot(ctx, st)
}

func fetch(ctx context.Context) (Status, error) {
	resp, err := getstatus.Request(ctx, eveonline.ESI.Get(ctx))
	if err != nil {
		// ESI does not answer /status while the cluster is offline: the
		// request hits lib-esi-go's timeout, which matches ESI's own.
		var ne net.Error
		if ctx.Err() == nil && (errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout()) {
			return Status{}, nil
		}
		return Status{}, fmt.Errorf("requesting status: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusServiceUnavailable, resp.StatusCode == http.StatusGatewayTimeout:
		return Status{}, nil
	case resp.StatusCode >= http.StatusBadRequest:
		msg := resp.Status
		if resp.ErrorData != nil {
			msg += ": " + resp.ErrorData.Error()
		}
		return Status{}, fmt.Errorf("requesting status: %s", msg)
	}
	d := resp.Data
	return Status{Online: true, Players: d.Players, ServerVersion: d.ServerVersion, StartTime: &d.StartTime, VIP: d.Vip}, nil
}

type serverParams struct{}

// server returns the last stored status.
func (*status) server(ctx context.Context, _ serverParams) (*Status, error) {
	var st Status
	ok, err := module.LoadSnapshot(ctx, &st)
	if err != nil || !ok {
		return nil, err
	}
	return &st, nil
}

const (
	maxHours  = 90 * 24
	maxPoints = 2000
)

type historyParams struct {
	// Hours of history up to now; default 24, at most 2160 (90 days).
	Hours int `json:"hours"`
	// StepMinutes is the width of one point; default 5.
	StepMinutes int `json:"stepMinutes"`
}

// Point is the average player count while online over one step. Players is
// null when the server was offline or not polled throughout the step.
type Point struct {
	Time    time.Time `json:"time"` // start of the step
	Players *int64    `json:"players"`
}

// history returns one point per step over the last Hours, oldest first.
// Steps align to multiples of the step; the last one holds now.
func (*status) history(ctx context.Context, p historyParams) ([]Point, error) {
	if p.Hours < 0 || p.StepMinutes < 0 {
		return nil, errors.New("hours and stepMinutes must not be negative")
	}
	hours, step := cmp.Or(p.Hours, 24), time.Duration(cmp.Or(p.StepMinutes, 5))*time.Minute
	if hours > maxHours {
		return nil, fmt.Errorf("hours must be at most %d, got %d", maxHours, hours)
	}
	n := int(time.Duration(hours) * time.Hour / step)
	if n > maxPoints || n < 1 {
		return nil, fmt.Errorf("hours and stepMinutes give %d points, want 1 to %d", n, maxPoints)
	}
	end := module.Clock(ctx)().Truncate(step).Add(step)
	start := end.Add(-time.Duration(n) * step)
	rows, err := New(module.Database(ctx)).ListSamples(ctx, start.Unix())
	if err != nil {
		return nil, fmt.Errorf("reading samples: %w", err)
	}
	sums, counts := make([]int64, n), make([]int64, n)
	for _, r := range rows {
		i := int((r.At - start.Unix()) / int64(step/time.Second))
		if !r.Online || i >= n {
			continue
		}
		sums[i] += r.Players
		counts[i]++
	}
	out := make([]Point, n)
	for i := range out {
		out[i].Time = start.Add(time.Duration(i) * step).UTC()
		if counts[i] > 0 {
			avg := (sums[i] + counts[i]/2) / counts[i]
			out[i].Players = &avg
		}
	}
	return out, nil
}
