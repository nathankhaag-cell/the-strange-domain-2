package store

import (
	"context"
	"testing"
)

// Migration 6 gives existing default Warden roles the ban permission and adds
// ban expiry; other roles keep their permissions.
func TestMigrationWardenCanBan(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	all := migrations
	migrations = all[:5]
	st, err := Open(ctx, dir)
	migrations = all
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users (id, callsign, created_at) VALUES ('u', 'u', 0)`,
		`INSERT INTO domains (id, name, owner_id, created_at) VALUES ('d', 'd', 'u', 0)`,
		`INSERT INTO roles (id, domain_id, name, rank, permissions) VALUES ('w', 'd', 'Warden', 300, 16)`,
		`INSERT INTO roles (id, domain_id, name, rank, permissions) VALUES ('m', 'd', 'Brother / Sister', 100, 8)`,
		`INSERT INTO bans (domain_id, user_id, banned_by, banned_at) VALUES ('d', 'u', 'u', 0)`,
	} {
		if _, err := st.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	if st, err = Open(ctx, dir); err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	perms := map[string]int64{}
	rows, err := st.DB.Query(`SELECT id, permissions FROM roles`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		var p int64
		rows.Scan(&id, &p)
		perms[id] = p
	}
	rows.Close()
	if perms["w"] != 16|32 || perms["m"] != 8 {
		t.Fatalf("permissions after migration = %v", perms)
	}
	var expires, rank int64
	if err := st.DB.QueryRow(`SELECT expires_at, target_rank FROM bans`).Scan(&expires, &rank); err != nil {
		t.Fatal(err)
	}
	if expires != 0 || rank != 0 {
		t.Fatalf("old ban became expires=%d rank=%d, want a permanent ban", expires, rank)
	}
}
