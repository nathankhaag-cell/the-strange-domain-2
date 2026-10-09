package domains

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
)

type fixture struct {
	svc                    *Service
	ctx                    context.Context
	domain                 Domain
	owner, mod, alice, bob User
	roles                  map[string]Role
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &fixture{svc: NewService(st), ctx: ctx, roles: map[string]Role{}}
	mk := func(name string) User {
		u, err := f.svc.CreateUser(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	f.owner, f.mod, f.alice, f.bob = mk("owner"), mk("mod"), mk("alice"), mk("bob")
	if f.domain, err = f.svc.CreateDomain(ctx, f.owner.ID, "Sector 7"); err != nil {
		t.Fatal(err)
	}
	roles, err := f.svc.Roles(ctx, f.domain.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range roles {
		f.roles[r.Name] = r
	}
	inv, err := f.svc.CreateInvite(ctx, f.domain.ID, f.owner.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []User{f.mod, f.alice, f.bob} {
		if _, err := f.svc.JoinByInvite(ctx, inv, u.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.svc.AssignRole(ctx, f.domain.ID, f.owner.ID, f.mod.ID, f.roles["Warden"].ID); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestCreateDomainDefaults(t *testing.T) {
	f := setup(t)
	if len(f.roles) != len(DefaultRoles) {
		t.Fatalf("got %d roles, want %d", len(f.roles), len(DefaultRoles))
	}
	members, err := f.svc.Members(f.ctx, f.domain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 4 || members[0].UserID != f.owner.ID || members[0].Role.Name != "Abbot" {
		t.Fatalf("unexpected members: %+v", members)
	}
	var channels int
	if err := f.svc.st.DB.QueryRow(`SELECT COUNT(*) FROM channels WHERE domain_id = ?`, f.domain.ID).Scan(&channels); err != nil {
		t.Fatal(err)
	}
	if channels != 2 {
		t.Fatalf("got %d channels, want 2", channels)
	}
}

func TestModeratorRules(t *testing.T) {
	f := setup(t)
	// A moderator can kick a member.
	if err := f.svc.Kick(f.ctx, f.domain.ID, f.mod.ID, f.alice.ID); err != nil {
		t.Fatalf("mod kick member: %v", err)
	}
	// A moderator lacks the ban permission by default.
	if err := f.svc.Ban(f.ctx, f.domain.ID, f.mod.ID, f.bob.ID, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("mod ban: got %v, want ErrForbidden", err)
	}
	// Nobody can act on the owner.
	if err := f.svc.Kick(f.ctx, f.domain.ID, f.mod.ID, f.owner.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("kick owner: got %v, want ErrForbidden", err)
	}
	// A member cannot moderate.
	if err := f.svc.SetMuted(f.ctx, f.domain.ID, f.bob.ID, f.mod.ID, true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member mute mod: got %v, want ErrForbidden", err)
	}
	// A moderator cannot promote someone to its own rank or above.
	if err := f.svc.AssignRole(f.ctx, f.domain.ID, f.mod.ID, f.bob.ID, f.roles["Warden"].ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("mod promote to mod: got %v, want ErrForbidden", err)
	}
	// Nobody can hand out the owner role.
	if err := f.svc.AssignRole(f.ctx, f.domain.ID, f.owner.ID, f.bob.ID, f.roles["Abbot"].ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("grant owner role: got %v, want ErrForbidden", err)
	}
}

func TestBanBlocksRejoin(t *testing.T) {
	f := setup(t)
	if err := f.svc.Ban(f.ctx, f.domain.ID, f.owner.ID, f.bob.ID, "spam"); err != nil {
		t.Fatal(err)
	}
	inv, err := f.svc.CreateInvite(f.ctx, f.domain.ID, f.owner.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.JoinByInvite(f.ctx, inv, f.bob.ID); !errors.Is(err, ErrBanned) {
		t.Fatalf("rejoin after ban: got %v, want ErrBanned", err)
	}
	if err := f.svc.Unban(f.ctx, f.domain.ID, f.owner.ID, f.bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.JoinByInvite(f.ctx, inv, f.bob.ID); err != nil {
		t.Fatalf("rejoin after unban: %v", err)
	}
}

func TestInviteLimits(t *testing.T) {
	f := setup(t)
	carol, _ := f.svc.CreateUser(f.ctx, "carol")
	dave, _ := f.svc.CreateUser(f.ctx, "dave")
	inv, err := f.svc.CreateInvite(f.ctx, f.domain.ID, f.owner.ID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.JoinByInvite(f.ctx, inv, carol.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.JoinByInvite(f.ctx, inv, dave.ID); !errors.Is(err, ErrInvalidInvite) {
		t.Fatalf("used-up invite: got %v, want ErrInvalidInvite", err)
	}

	expiring, err := f.svc.CreateInvite(f.ctx, f.domain.ID, f.owner.ID, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	f.svc.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := f.svc.JoinByInvite(f.ctx, expiring, dave.ID); !errors.Is(err, ErrInvalidInvite) {
		t.Fatalf("expired invite: got %v, want ErrInvalidInvite", err)
	}
	if _, err := f.svc.JoinByInvite(f.ctx, "nope", dave.ID); !errors.Is(err, ErrInvalidInvite) {
		t.Fatalf("unknown invite: got %v, want ErrInvalidInvite", err)
	}
}

func TestCreateChannelPermission(t *testing.T) {
	f := setup(t)
	if _, err := f.svc.CreateChannel(f.ctx, f.domain.ID, f.alice.ID, "secret", "text"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member create channel: got %v, want ErrForbidden", err)
	}
	if _, err := f.svc.CreateChannel(f.ctx, f.domain.ID, f.owner.ID, "ops", "voice"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateChannel(f.ctx, f.domain.ID, f.owner.ID, "bad", "video"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad kind: got %v, want ErrInvalidInput", err)
	}
	if _, err := f.svc.CreateChannel(f.ctx, f.domain.ID, f.bob.ID+"x", "x", "text"); !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member: got %v, want ErrNotMember", err)
	}
}
