package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// TestMigrateDefaultCategories: uncategorised channels move into
// real "Textkanäle"/"Sprachkanäle" categories that sort before the others.
func TestMigrateDefaultCategories(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`}
	for v, f := range []string{"0001_init.sql", "0002_roles.sql", "0003_channel_modes.sql", "0004_channel_perms.sql", "0005_channel_view.sql", "0006_account_delete.sql"} {
		body, err := migrationFS.ReadFile("migrations/" + f)
		if err != nil {
			t.Fatal(err)
		}
		stmts = append(stmts, string(body), fmt.Sprintf(`INSERT INTO schema_migrations VALUES (%d, 0)`, v+1))
	}
	stmts = append(stmts,
		`INSERT INTO users(id, username, display_name, password_hash, created_at) VALUES (1,'o','O','x',0)`,
		`INSERT INTO spaces(id, name, owner_id, created_at) VALUES (100,'A',1,5),(200,'B',1,5),(300,'C',1,5)`,
		`INSERT INTO channels(id, space_id, kind, name, position, created_at, parent_id) VALUES
			(10,100,'text','general',0,0,NULL),
			(11,100,'voice','lounge',1,0,NULL),
			(12,100,'category','Projects',2,0,NULL),
			(13,100,'text','dev',3,0,12),
			(20,200,'voice','only voice',0,0,NULL),
			(30,300,'category','X',0,0,NULL),
			(31,300,'text','in x',1,0,30),
			(40,NULL,'dm','',0,0,NULL)`,
	)
	for _, q := range stmts {
		if _, err := old.ExecContext(ctx, q); err != nil {
			t.Fatal(q[:min(len(q), 60)], err)
		}
	}
	_ = old.Close()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	parent := func(id int64) (name string, pos int64) {
		t.Helper()
		err := db.R.QueryRow(`SELECT p.name, p.position FROM channels c JOIN channels p ON p.id=c.parent_id WHERE c.id=?`, id).Scan(&name, &pos)
		if err != nil {
			t.Fatalf("parent of %d: %v", id, err)
		}
		return
	}
	for id, want := range map[int64]string{10: "Textkanäle", 11: "Sprachkanäle", 13: "Projects", 20: "Sprachkanäle", 31: "X"} {
		if got, _ := parent(id); got != want {
			t.Fatalf("channel %d in %q, want %q", id, got, want)
		}
	}
	if _, pos := parent(10); pos != -2 {
		t.Fatalf("text category position %d", pos)
	}
	var n int
	_ = db.R.QueryRow(`SELECT COUNT(*) FROM channels WHERE kind='category'`).Scan(&n)
	if n != 5 { // A: text, voice, Projects · B: voice · C: X
		t.Fatalf("categories: %d", n)
	}
	var dmParent sql.NullInt64
	_ = db.R.QueryRow(`SELECT parent_id FROM channels WHERE id=40`).Scan(&dmParent)
	if dmParent.Valid {
		t.Fatal("DM got a parent")
	}
}
