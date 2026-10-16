package storage_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/xaroth/dashboard/storage"
)

func open(tb testing.TB) *storage.DB {
	tb.Helper()
	db, err := storage.Open(tb.Context(), tb.TempDir())
	if err != nil {
		tb.Fatalf("Open: %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	return db
}

func TestOpenPragmas(t *testing.T) {
	t.Parallel()
	db := open(t)
	testCases := []struct {
		name   string
		conn   *sql.DB
		pragma string
		want   string
	}{
		{"wal", db.Writer, "journal_mode", "wal"},
		{"writer foreign keys", db.Writer, "foreign_keys", "1"},
		{"reader foreign keys", db.Reader, "foreign_keys", "1"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var got string
			if err := testCase.conn.QueryRowContext(t.Context(), "PRAGMA "+testCase.pragma).Scan(&got); err != nil {
				t.Fatalf("PRAGMA %s: %v", testCase.pragma, err)
			}
			if got != testCase.want {
				t.Errorf("got %s, want %s", got, testCase.want)
			}
		})
	}
}

func TestOpenReopens(t *testing.T) {
	t.Parallel()
	// Characters that mean something in a URL.
	dir := filepath.Join(t.TempDir(), "a?b#c%d")
	for range 2 {
		db, err := storage.Open(t.Context(), dir)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		var size int
		if err := db.Reader.QueryRowContext(t.Context(), "PRAGMA page_size").Scan(&size); err != nil {
			t.Fatalf("page_size: %v", err)
		}
		if size != 16384 {
			t.Errorf("got page size %d, want 16384", size)
		}
		db.Close()
	}
	// The file is where the path says, not under an escaped name.
	if _, err := os.Stat(filepath.Join(dir, storage.File)); err != nil {
		t.Errorf("got %v, want the database file at the unescaped path", err)
	}
}

func TestReaderCannotWrite(t *testing.T) {
	t.Parallel()
	db := open(t)
	_, err := db.Reader.ExecContext(t.Context(), "CREATE TABLE test_notes (id INTEGER PRIMARY KEY)")
	if err == nil {
		t.Error("got nil error writing through the reader, want one")
	}
}

func TestMigrate(t *testing.T) {
	t.Parallel()
	good := fstest.MapFS{"sql/migrations/00001_notes.sql": {Data: []byte(
		"-- +goose Up\nCREATE TABLE test_notes (id INTEGER PRIMARY KEY);\n-- +goose Down\nDROP TABLE test_notes;\n")}}
	bad := fstest.MapFS{"sql/migrations/00001_broken.sql": {Data: []byte("-- +goose Up\nCREATE TABLE test_broken (;\n")}}
	testCases := []struct {
		name    string
		owner   string
		fsys    fstest.MapFS
		dir     string
		wantErr bool
	}{
		{"applies", "test", good, "sql/migrations", false},
		{"bad sql", "test", bad, "sql/migrations", true},
		{"missing dir", "test", good, "nope", true},
		{"bad owner", "x; DROP TABLE module_state", good, "sql/migrations", true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			db := open(t)
			err := db.Migrate(t.Context(), testCase.owner, testCase.fsys, testCase.dir)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("got error %v, want error %v", err, testCase.wantErr)
			}
			if testCase.wantErr {
				return
			}
			// Idempotent, and tracked in the owner's own table.
			if err := db.Migrate(t.Context(), testCase.owner, testCase.fsys, testCase.dir); err != nil {
				t.Fatalf("second Migrate: %v", err)
			}
			var version int
			if err := db.Reader.QueryRowContext(t.Context(), "SELECT MAX(version_id) FROM goose_test").Scan(&version); err != nil {
				t.Fatalf("reading goose_test: %v", err)
			}
			if version != 1 {
				t.Errorf("got version %d, want 1", version)
			}
		})
	}
}
