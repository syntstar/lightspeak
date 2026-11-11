// Package store opens the SQLite database (one writer, a few readers) and
// applies the embedded, versioned migrations.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // CGO-free SQLite driver
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// DB bundles a single-connection writer pool and a small reader pool.
// All writes must go through W so SQLite never sees two concurrent writers.
type DB struct {
	W *sql.DB
	R *sql.DB
}

func dsn(path string, readOnly bool) string {
	q := url.Values{}
	for _, p := range []string{
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"foreign_keys(1)",
		"busy_timeout(5000)",
		"temp_store(MEMORY)",
		"cache_size(-8000)",
		"journal_size_limit(33554432)", // truncate the WAL to 32 MB after checkpoints
	} {
		q.Add("_pragma", p)
	}
	if !readOnly {
		q.Set("_txlock", "immediate")
	} else {
		q.Add("_pragma", "query_only(1)")
	}
	return "file:" + filepath.ToSlash(path) + "?" + q.Encode()
}

// Open opens (and creates) the database at path and runs migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	w, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxIdleTime(0)
	if err := w.PingContext(ctx); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := migrate(ctx, w); err != nil {
		_ = w.Close()
		return nil, err
	}
	r, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	r.SetMaxIdleConns(2)
	r.SetConnMaxIdleTime(5 * time.Minute)
	return &DB{W: w, R: r}, nil
}

func (d *DB) Close() error {
	err1 := d.R.Close()
	err2 := d.W.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		v, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("bad migration name %q", e.Name())
		}
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=?`, v).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, v, time.Now().UnixMilli()); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Tx runs fn in a write transaction.
func (d *DB) Tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Snowflake IDs: milliseconds since 2026-01-01 << 22 | 22-bit sequence.
const snowflakeEpoch = 1767225600000 // 2026-01-01T00:00:00Z in ms

type Snowflake struct {
	mu   sync.Mutex
	last int64
	seq  int64
}

var ids Snowflake

// NewID returns a unique, time-ordered 64-bit ID.
func NewID() int64 { return ids.Next() }

func (s *Snowflake) Next() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli() - snowflakeEpoch
	if now < s.last {
		now = s.last // clock went backwards: keep monotonic
	}
	if now == s.last {
		s.seq = (s.seq + 1) & 0x3FFFFF
		if s.seq == 0 {
			now++ // sequence exhausted in this ms; borrow the next one
		}
	} else {
		s.seq = 0
	}
	s.last = now
	return now<<22 | s.seq
}

// IDTime returns the creation time encoded in a snowflake.
func IDTime(id int64) time.Time { return time.UnixMilli((id >> 22) + snowflakeEpoch) }

// Backup writes a consistent copy of the database to dst (VACUUM INTO).
func (d *DB) Backup(ctx context.Context, dst string) error {
	_, err := d.W.ExecContext(ctx, `VACUUM INTO ?`, dst)
	return err
}
