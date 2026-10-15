// Package storage owns the SQLite database: connections, per-owner
// migrations, and the service's own tables.
package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

// File is the database file name inside the data directory.
const File = "dashboard.sqlite"

// Matches ZFS recordsize=16K on the data dataset; see docs/design.md, Write load.
const pageSize = 16384

var (
	readerPragmas = []string{
		"busy_timeout(5000)",
		"foreign_keys(ON)",
		"cache_size(-32768)",
		"mmap_size(268435456)",
		"query_only(1)",
	}
	writerPragmas = []string{
		"busy_timeout(5000)",
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"foreign_keys(ON)",
		"cache_size(-32768)",
		"mmap_size(268435456)",
	}
)

//go:embed sql/migrations/*.sql
var migrations embed.FS

// DB is the database: one writer connection and a read-only pool.
type DB struct {
	// Writer is the only connection that writes.
	Writer *sql.DB
	// Reader is query_only; writes through it fail.
	Reader *sql.DB
}

// Open opens or creates the database in dir and runs the storage migrations.
func Open(ctx context.Context, dir string) (*DB, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating data dir: %w", err)
	}
	path := filepath.Join(dir, File)
	if err := bootstrap(ctx, path); err != nil {
		return nil, err
	}

	writer, err := sql.Open("sqlite", dsn(path, writerPragmas))
	if err != nil {
		return nil, err
	}
	writer.SetMaxOpenConns(1)
	db := &DB{Writer: writer}
	if err := db.Migrate(ctx, "storage", migrations, "sql/migrations"); err != nil {
		writer.Close()
		return nil, err
	}

	// Opened after the migrations, so readers never see a file without tables.
	reader, err := sql.Open("sqlite", dsn(path, readerPragmas))
	if err != nil {
		writer.Close()
		return nil, err
	}
	reader.SetMaxOpenConns(4)
	db.Reader = reader
	return db, nil
}

// bootstrap sets the page size on a new database. The driver applies DSN
// pragmas in alphabetical order, so journal_mode would fix the page size
// before page_size ran; here it runs first and the WAL switch writes it.
func bootstrap(ctx context.Context, path string) error {
	conn, err := sql.Open("sqlite", "file:"+path+"?_pragma="+url.QueryEscape(fmt.Sprintf("page_size(%d)", pageSize)))
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("enabling WAL: %w", err)
	}
	return nil
}

func dsn(path string, pragmas []string) string {
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	// Times as UTC text SQLite's date functions read, not Go's Time.String.
	q.Set("_time_format", "sqlite")
	q.Set("_timezone", "UTC")
	return "file:" + path + "?" + q.Encode()
}

// Migrate applies owner's goose migrations, the .sql files in dir of fsys,
// tracking them in the table goose_<owner>.
func (db *DB) Migrate(ctx context.Context, owner string, fsys fs.FS, dir string) error {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		return fmt.Errorf("migrations for %s: %w", owner, err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db.Writer, sub,
		goose.WithTableName("goose_"+owner),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return fmt.Errorf("migrations for %s: %w", owner, err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrating %s: %w", owner, err)
	}
	return nil
}

func (db *DB) Close() error {
	return errors.Join(db.Reader.Close(), db.Writer.Close())
}

// Queries returns the sqlc queries for the service's tables on the writer.
func (db *DB) Queries() *Queries { return New(db.Writer) }
