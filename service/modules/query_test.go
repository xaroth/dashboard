package modules_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/service/modules"
	"github.com/xaroth/dashboard/service/storage"
)

type countParams struct {
	Limit  int              `json:"limit"`
	Window modules.Duration `json:"window"`
}

type countResult struct {
	Updates int    `json:"updates"`
	Limit   int    `json:"limit"`
	Window  string `json:"window"`
}

// count reads the snapshot on the query connection.
func (m *testModule) count(ctx context.Context, p countParams) (countResult, error) {
	var snap map[string]int
	if _, err := modules.LoadSnapshot(ctx, &snap); err != nil {
		return countResult{}, err
	}
	if p.Limit < 0 {
		return countResult{}, errors.New("limit must not be negative")
	}
	return countResult{Updates: snap["updates"], Limit: p.Limit, Window: time.Duration(p.Window).String()}, nil
}

// boom panics.
func (m *testModule) boom(context.Context, struct{}) (int, error) { panic("kaboom") }

// write tries to write through the query connection.
func (m *testModule) write(ctx context.Context, _ struct{}) (int, error) {
	_, err := modules.Database(ctx).ExecContext(ctx, "INSERT INTO test_items (id) VALUES (1)")
	return 0, err
}

func queryRegistration() modules.Registration {
	return modules.Define[testModule, testConfig]("test", func() *testConfig { return &testConfig{} },
		modules.WithUpdateInterval(10*time.Minute),
		modules.WithMigrations(migrations),
		modules.WithQuery("count", (*testModule).count),
		modules.WithQuery("write", (*testModule).write),
		modules.WithQuery("boom", (*testModule).boom),
	)
}

func TestCheck(t *testing.T) {
	t.Parallel()
	set, _ := loadWith(t.Context(), t, "test: {key: k}", queryRegistration())
	testCases := []struct {
		name    string
		module  string
		query   string
		params  map[string]any
		wantErr error
		wantMsg string
	}{
		{"valid", "test", "count", map[string]any{"limit": 3, "window": "24h"}, nil, ""},
		{"no params", "test", "count", nil, nil, ""},
		{"unknown module", "nope", "count", nil, modules.ErrUnknownModule, ""},
		{"unknown query", "test", "nope", nil, modules.ErrUnknownQuery, "boom, count, write"},
		{"null duration", "test", "count", map[string]any{"window": nil}, nil, ""},
		{"unknown param", "test", "count", map[string]any{"bogus": 1}, nil, "bogus"},
		{"bad duration", "test", "count", map[string]any{"window": "soon"}, nil, "soon"},
		{"wrong type", "test", "count", map[string]any{"limit": "three"}, nil, "limit"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			err := set.Check(testCase.module, testCase.query, testCase.params)
			wantErr := testCase.wantErr != nil || testCase.wantMsg != ""
			if (err != nil) != wantErr {
				t.Fatalf("got %v, want error %v", err, wantErr)
			}
			if testCase.wantErr != nil && !errors.Is(err, testCase.wantErr) {
				t.Errorf("got %v, want %v", err, testCase.wantErr)
			}
			if testCase.wantMsg != "" && !strings.Contains(err.Error(), testCase.wantMsg) {
				t.Errorf("got %v, want it to mention %q", err, testCase.wantMsg)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		set, _ := loadWith(ctx, t, "test: {key: k}", queryRegistration())
		params := map[string]any{"limit": 3, "window": "1h"}

		before := set.Resolve(ctx, "test", "count", params)
		if diff := cmp.Diff(modules.Result{Value: countResult{Limit: 3, Window: "1h0m0s"}, Stale: true}, before); diff != "" {
			t.Errorf("before any update (-want +got):\n%s", diff)
		}

		done := make(chan struct{})
		go func() {
			set.Run(ctx)
			close(done)
		}()
		waitFirstAttempt(t, set)
		after := set.Resolve(ctx, "test", "count", params)
		if after.FetchedAt == nil || after.Stale || after.Error != "" {
			t.Errorf("after an update: got %+v, want fresh", after)
		}
		if diff := cmp.Diff(countResult{Updates: 1, Limit: 3, Window: "1h0m0s"}, after.Value); diff != "" {
			t.Errorf("value (-want +got):\n%s", diff)
		}
		cancel()
		<-done
	})
}

func TestResolveStateUnreadable(t *testing.T) {
	t.Parallel()
	set, _ := loadWith(t.Context(), t, "test: {key: k}", queryRegistration())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got := set.Resolve(ctx, "test", "count", nil)
	if !got.Stale || got.Error == "" {
		t.Errorf("got %+v, want stale with an error", got)
	}
}

func TestResolveErrors(t *testing.T) {
	t.Parallel()
	set, db := loadWith(t.Context(), t, "test: {key: k}", queryRegistration())
	testCases := []struct {
		name   string
		module string
		query  string
		params map[string]any
		want   string
	}{
		{"query error", "test", "count", map[string]any{"limit": -1}, "must not be negative"},
		{"bad params", "test", "count", map[string]any{"bogus": 1}, "bogus"},
		{"unknown module", "nope", "count", nil, "unknown module"},
		{"read only", "test", "write", nil, "readonly"},
		{"panic", "test", "boom", nil, "query panicked: kaboom"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got := set.Resolve(t.Context(), testCase.module, testCase.query, testCase.params)
			if got.Value != nil || !strings.Contains(strings.ToLower(got.Error), testCase.want) {
				t.Errorf("got %+v, want no value and an error containing %q", got, testCase.want)
			}
		})
	}
	var n int
	if err := db.Reader.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM test_items").Scan(&n); err != nil || n != 0 {
		t.Errorf("got %d rows (%v), want the query's write rejected", n, err)
	}
}

type otherModule struct{ testModule }

func (m *otherModule) count(context.Context, struct{}) (int, error) { return 0, nil }

func TestDefineRejectsForeignQuery(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("Define accepted a query method of another module type")
		}
	}()
	modules.Define[testModule, testConfig]("test", func() *testConfig { return &testConfig{} },
		modules.WithQuery("count", (*otherModule).count))
}

func loadWith(ctx context.Context, tb testing.TB, src string, regs ...modules.Registration) (*modules.Set, *storage.DB) {
	tb.Helper()
	return loadRegs(ctx, tb, src, regs)
}
