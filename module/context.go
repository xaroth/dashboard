package module

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/xaroth/dashboard/series"
)

type ctxKey int

const (
	keyDatabase ctxKey = iota
	keyLogger
	keyHTTPClient
	keyDialer
	keyClock
	keyState
	keySeries
)

// Database is the module's connection: the writer in Initialize and Update,
// the read-only pool in queries.
func Database(ctx context.Context) *sql.DB { return value[*sql.DB](ctx, keyDatabase) }

// Logger is the module's logger.
func Logger(ctx context.Context) *slog.Logger {
	return value[*slog.Logger](ctx, keyLogger)
}

// HTTPClient is the client for calls to the source; it has a timeout.
func HTTPClient(ctx context.Context) *http.Client {
	return value[*http.Client](ctx, keyHTTPClient)
}

// ContextDialer opens connections; *net.Dialer is one.
type ContextDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Dialer is the dialer for calls to sources that are not HTTP.
func Dialer(ctx context.Context) ContextDialer { return value[ContextDialer](ctx, keyDialer) }

// Clock returns the current time.
func Clock(ctx context.Context) func() time.Time {
	return value[func() time.Time](ctx, keyClock)
}

// value panics outside a context from Set: modules only run inside one.
func value[T any](ctx context.Context, key ctxKey) T {
	v, ok := ctx.Value(key).(T)
	if !ok {
		panic("module: context has no module dependencies")
	}
	return v
}

// SaveSnapshot sets v as the module's snapshot. Called from Update; it is
// stored together with the fetch time when Update returns nil, and dropped
// when Update fails.
func SaveSnapshot(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding snapshot: %w", err)
	}
	return value[*stateStore](ctx, keyState).save(b)
}

// NextUpdate sets when the schedule runs Update next: d after this update,
// instead of the update interval. Called from Update; it applies when Update
// returns nil, to scheduled and manual updates alike, and the last call
// wins. Keep d under the stale_after time, or the data shows as stale while
// the module waits.
func NextUpdate(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return fmt.Errorf("next update in %v: must be positive", d)
	}
	return value[*stateStore](ctx, keyState).setNext(d)
}

// LoadSnapshot reads the module's snapshot into v. ok is false when there is none yet.
func LoadSnapshot(ctx context.Context, v any) (ok bool, err error) {
	b, err := value[*stateStore](ctx, keyState).load(ctx)
	if err != nil || b == nil {
		return false, err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return false, fmt.Errorf("decoding snapshot: %w", err)
	}
	return true, nil
}

// Series is the module's time series, declared with WithSeries.
func Series(ctx context.Context) *SeriesStore { return value[*SeriesStore](ctx, keySeries) }

// SeriesStore reads and writes one module's series by their short names.
type SeriesStore struct {
	store  *series.Store
	module string
	update bool
}

// Add buffers a sample; it is stored within a minute. Called from Update,
// whatever its result.
func (s *SeriesStore) Add(name string, t time.Time, v float64) error {
	if !s.update {
		return errors.New("series are written from Update")
	}
	return s.store.Add(s.module+"."+name, t, v)
}

// Query returns name in buckets of step from from to to; see series.Store.Query.
func (s *SeriesStore) Query(ctx context.Context, name string, from, to time.Time, step time.Duration) ([]series.Point, error) {
	return s.store.Query(ctx, s.module+"."+name, from, to, step)
}
