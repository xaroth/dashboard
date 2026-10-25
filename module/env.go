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

// Env is what every module and shared value call gets.
type Env struct {
	Log *slog.Logger
	// HTTP is the client for calls to sources. It has a timeout, and its
	// calls are traced as children of the update or query.
	HTTP *http.Client
	// Dialer opens connections to sources that are not HTTP.
	Dialer ContextDialer
	Now    func() time.Time
}

// ContextDialer opens connections; *net.Dialer is one.
type ContextDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Query reads a module's data. Queries, Enabled, admin pages and
// Versioned.Version get one; Update gets one as part of *Update.
type Query struct {
	Env
	// DB is the module's connection: the read-only pool in queries, Enabled
	// and Version, the writer in Update and admin pages.
	DB *sql.DB

	name   string
	series *series.Store
	shared map[sharedDef]any
}

// LoadSnapshot reads the module's snapshot into v. ok is false when there
// is none yet.
func (q Query) LoadSnapshot(ctx context.Context, v any) (ok bool, err error) {
	st, err := New(q.DB).GetState(ctx, q.name)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && st.Data == nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading snapshot: %w", err)
	}
	if err := json.Unmarshal([]byte(*st.Data), v); err != nil {
		return false, fmt.Errorf("decoding snapshot: %w", err)
	}
	return true, nil
}

// QuerySeries returns the module's series name, declared with WithSeries, in
// buckets of step from from to to; see series.Store.Query.
func (q Query) QuerySeries(ctx context.Context, name string, from, to time.Time, step time.Duration) ([]series.Point, error) {
	return q.series.Query(ctx, q.name+"."+name, from, to, step)
}

// Update is what Update gets: Query on the writer, and the writes only
// Update may do.
type Update struct {
	Query
	// snapshot is what SaveSnapshot set, stored with the success record.
	snapshot *string
	// next is the delay NextUpdate set; zero means the update interval.
	next time.Duration
}

// SaveSnapshot sets v as the module's snapshot. It is stored together with
// the fetch time when Update returns nil, and dropped when Update fails.
func (u *Update) SaveSnapshot(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding snapshot: %w", err)
	}
	s := string(b)
	u.snapshot = &s
	return nil
}

// NextUpdate sets when the schedule runs Update next: d after this update,
// instead of the update interval. It applies when Update returns nil, to
// scheduled and manual updates alike, and the last call wins. Keep d under
// the stale_after time, or the data shows as stale while the module waits.
func (u *Update) NextUpdate(d time.Duration) error {
	if d <= 0 {
		return fmt.Errorf("next update in %v: must be positive", d)
	}
	u.next = d
	return nil
}

// AddSample buffers a sample of the module's series name, declared with
// WithSeries. It is stored within a minute, whatever Update returns.
func (u *Update) AddSample(name string, t time.Time, v float64) error {
	return u.series.Add(u.name+"."+name, t, v)
}
