// Package status polls the EVE Online server status from ESI.
package status

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/eve-online-tools/lib-esi-go/esi/getstatus"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/modules/eveonline"
)

// requestTimeout is when a status request counts as the cluster being
// offline: ESI does not answer /status then, it times out.
const requestTimeout = 15 * time.Second

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

// Module is the eve_status registration.
var Module = module.Define[status, config](
	"eve_status",
	func() *config { return &config{} },
	// ESI caches /status for 30 s; screens refresh far less often.
	module.WithUpdateInterval(5*time.Minute),
	module.WithStaleAfter(30*time.Minute),
	module.WithShared(eveonline.ESI),
	module.WithQuery("server", (*status).server),
)

func (*status) Initialize(context.Context, *config) error { return nil }

func (*status) Enabled(context.Context) bool { return true }

func (*status) Update(ctx context.Context) error {
	st, err := fetch(ctx)
	if err != nil {
		return err
	}
	return module.SaveSnapshot(ctx, st)
}

func fetch(ctx context.Context) (Status, error) {
	rctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	resp, err := getstatus.Request(rctx, eveonline.ESI.Get(ctx))
	if err != nil {
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
