package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestOpenMigrateBackup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "t.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var fk, n int
	if err := db.W.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign keys: %d %v", fk, err)
	}
	var mode string
	_ = db.W.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
	if mode != "wal" {
		t.Fatalf("journal mode %s", mode)
	}
	if _, err := db.R.Exec(`CREATE TABLE x(a)`); err == nil {
		t.Fatal("reader pool must be read-only")
	}
	_ = db.W.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n)
	if n == 0 {
		t.Fatal("no migrations recorded")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// re-open is idempotent
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// FTS triggers work
	if _, err := db.W.Exec(`INSERT INTO users(id, username, display_name, password_hash, created_at) VALUES (1,'a','A','x',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.Exec(`INSERT INTO channels(id, kind, created_at) VALUES (1,'dm',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.Exec(`INSERT INTO messages(id, channel_id, author_id, content, created_at) VALUES (1,1,1,'Grüße aus Köln',0)`); err != nil {
		t.Fatal(err)
	}
	_ = db.R.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'koln'`).Scan(&n)
	if n != 1 {
		t.Fatal("fts with diacritics folding")
	}
	if _, err := db.W.Exec(`UPDATE messages SET content='other' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	_ = db.R.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'koln'`).Scan(&n)
	if n != 0 {
		t.Fatal("fts update trigger")
	}
	dst := filepath.Join(dir, "backup.db")
	if err := db.Backup(ctx, dst); err != nil {
		t.Fatal(err)
	}
	b, err := Open(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	_ = b.R.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&n)
	b.Close()
	if n != 1 {
		t.Fatal("backup content")
	}
}

func TestSnowflake(t *testing.T) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[int64]bool{}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				id := NewID()
				mu.Lock()
				if seen[id] {
					t.Errorf("duplicate id %d", id)
				}
				seen[id] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	a, b := NewID(), NewID()
	if b <= a {
		t.Fatal("ids must increase")
	}
	if d := time.Since(IDTime(b)); d < 0 || d > time.Second {
		t.Fatalf("IDTime off by %v", d)
	}
}
