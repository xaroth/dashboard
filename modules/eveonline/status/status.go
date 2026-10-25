// Package status polls the EVE Online server status from ESI.
package status

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
	"time"

	"github.com/eve-online-tools/lib-esi-go/esi/getstatus"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/modules/eveonline/esi"
	"github.com/xaroth/dashboard/series"
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
	module.WithShared(esi.Shared),
	module.WithMigrations(migrations),
	module.WithQuery("server", (*status).server),
	module.WithQuery("history", (*status).history),
	module.WithSeries("players", series.Rate),
)

func (*status) Initialize(context.Context, *config) error { return nil }

func (*status) Enabled(context.Context) (bool, error) { return true, nil }

func (*status) Update(ctx context.Context) error {
	st, err := fetch(ctx)
	if err != nil {
		return err
	}
	// Offline steps stay empty, so charts show downtime as a gap.
	if st.Online {
		if err := module.Series(ctx).Add("players", module.Clock(ctx)(), float64(st.Players)); err != nil {
			return err
		}
	}
	return module.SaveSnapshot(ctx, st)
}

func fetch(ctx context.Context) (Status, error) {
	var connected atomic.Bool
	tctx := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { connected.Store(true) },
	})
	resp, err := getstatus.Request(tctx, esi.Shared.Get(ctx))
	if err != nil {
		// ESI does not answer /status while the cluster is offline: the
		// request hits lib-esi-go's timeout, which matches ESI's own. A
		// timeout before connecting is our network, not the cluster.
		var ne net.Error
		if ctx.Err() == nil && connected.Load() && (errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout()) {
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

// history returns the average player count per step over the last hours,
// null for steps the server was offline or not polled throughout.
func (*status) history(ctx context.Context, p series.HistoryParams) ([]series.Point, error) {
	from, to, step, err := p.Range(module.Clock(ctx)())
	if err != nil {
		return nil, err
	}
	return module.Series(ctx).Query(ctx, "players", from, to, step)
}
