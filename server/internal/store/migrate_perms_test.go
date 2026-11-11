package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrateChannelPerms: channels with the older stage/announce modes keep their behaviour as
// permission overrides, and @everyone gets the new channel bits.
func TestMigrateChannelPerms(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`}
	for v, f := range []string{"0001_init.sql", "0002_roles.sql", "0003_channel_modes.sql"} {
		body, err := migrationFS.ReadFile("migrations/" + f)
		if err != nil {
			t.Fatal(err)
		}
		stmts = append(stmts, string(body), `INSERT INTO schema_migrations VALUES (`+string(rune('1'+v))+`, 0)`)
	}
	stmts = append(stmts,
		`INSERT INTO users(id, username, display_name, password_hash, created_at) VALUES (1,'o','O','x',0)`,
		`INSERT INTO spaces(id, name, owner_id, everyone_perms, created_at) VALUES (100,'S',1,8,5)`,
		`INSERT INTO roles(id, space_id, name, position, permissions, created_at) VALUES (7,100,'Host',1,0,0)`,
		`INSERT INTO channels(id, space_id, kind, name, position, created_at, mode, locked) VALUES
			(10,100,'text','news',1,0,'announce',0),
			(11,100,'voice','stage',2,0,'stage',1),
			(12,100,'voice','vip',3,0,'open',1),
			(13,100,'voice','afk',4,0,'away',1),
			(14,100,'text','chat',5,0,'open',0)`,
		`INSERT INTO channel_roles(channel_id, kind, role_id) VALUES (10,'talk',7),(11,'talk',7),(11,'join',7),(12,'join',7),(13,'join',7)`,
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
	var everyone int64
	if err := db.R.QueryRow(`SELECT everyone_perms FROM spaces WHERE id=100`).Scan(&everyone); err != nil || everyone != 8|64512|65536 {
		t.Fatalf("everyone_perms %d %v", everyone, err)
	}
	type o struct{ allow, deny int64 }
	got := map[[2]int64]o{}
	rows, err := db.R.Query(`SELECT channel_id, role_id, allow, deny FROM channel_overrides`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c, r, a, d int64
		if err := rows.Scan(&c, &r, &a, &d); err != nil {
			t.Fatal(err)
		}
		got[[2]int64{c, r}] = o{a, d}
	}
	_ = rows.Close()
	want := map[[2]int64]o{
		{10, 100}: {0, 8192}, {10, 7}: {8192, 0}, // announce
		{11, 100}: {0, 6144 | 1024}, {11, 7}: {6144 | 1024, 0}, // stage + locked
		{12, 100}: {0, 1024}, {12, 7}: {1024, 0}, // locked
	}
	if len(got) != len(want) {
		t.Fatalf("overrides %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("override %v = %v, want %v (all: %v)", k, got[k], v, got)
		}
	}
	var modes string
	if err := db.R.QueryRow(`SELECT group_concat(mode || ':' || audio, ',') FROM (SELECT mode, audio FROM channels WHERE kind<>'category' ORDER BY id)`).Scan(&modes); err != nil {
		t.Fatal(err)
	}
	if modes != "open:speech,open:speech,open:speech,away:speech,open:speech" {
		t.Fatalf("modes %s", modes)
	}
	// deleting a role drops its overrides; deleting a channel too
	if _, err := db.W.Exec(`DELETE FROM roles WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.Exec(`DELETE FROM channels WHERE id=12`); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.R.QueryRow(`SELECT COUNT(*) FROM channel_overrides`).Scan(&n)
	if n != 2 {
		t.Fatalf("overrides left: %d", n)
	}
}
