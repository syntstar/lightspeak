package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrateRoles: a database created before roles existed keeps its admins – they
// move into the new "Admin" role of their space.
func TestMigrateRoles(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		t.Fatal(err)
	}
	body, err := migrationFS.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		string(body),
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`,
		`INSERT INTO schema_migrations VALUES (1, 0)`,
		`INSERT INTO users(id, username, display_name, password_hash, created_at) VALUES (1,'o','O','x',0),(2,'a','A','x',0),(3,'m','M','x',0)`,
		`INSERT INTO spaces(id, name, owner_id, created_at) VALUES (100,'S',1,5),(101,'T',1,5)`,
		`INSERT INTO space_members(space_id, user_id, role, joined_at) VALUES (100,1,'owner',0),(100,2,'admin',0),(100,3,'member',0),(101,1,'owner',0)`,
	} {
		if _, err := old.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	_ = old.Close()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var roles, perms, admins, adminRows int
	if err := db.R.QueryRow(`SELECT COUNT(*), MIN(permissions) FROM roles WHERE name='Admin' AND position=1`).Scan(&roles, &perms); err != nil {
		t.Fatal(err)
	}
	if roles != 2 || perms != 512 {
		t.Fatalf("roles %d perms %d", roles, perms)
	}
	if err := db.R.QueryRow(`SELECT COUNT(*) FROM member_roles mr JOIN roles r ON r.id=mr.role_id WHERE mr.user_id=2 AND r.space_id=100`).Scan(&admins); err != nil {
		t.Fatal(err)
	}
	_ = db.R.QueryRow(`SELECT COUNT(*) FROM space_members WHERE role='admin'`).Scan(&adminRows)
	if admins != 1 || adminRows != 0 {
		t.Fatalf("admin moved: %d, legacy rows left: %d", admins, adminRows)
	}
	// kicking a member drops their roles
	if _, err := db.W.Exec(`DELETE FROM space_members WHERE space_id=100 AND user_id=2`); err != nil {
		t.Fatal(err)
	}
	_ = db.R.QueryRow(`SELECT COUNT(*) FROM member_roles`).Scan(&admins)
	if admins != 0 {
		t.Fatal("member_roles must cascade")
	}
}
