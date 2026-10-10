package domains

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
)

// ladder is a domain with one person in each default role, plus spare
// members to act on.
type ladder struct {
	svc                                             *Service
	ctx                                             context.Context
	domain                                          Domain
	abbot, bishop, warden, warden2, brother, sister User
	postulant                                       User
	roles                                           map[string]Role
	clock                                           time.Time
	invite                                          string
	t                                               *testing.T
}

func newLadder(t *testing.T) *ladder {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	l := &ladder{svc: NewService(st), ctx: ctx, roles: map[string]Role{}, clock: time.Unix(1_800_000_000, 0), t: t}
	l.svc.now = func() time.Time { return l.clock }
	mk := func(name string) User {
		u, err := l.svc.CreateUser(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	l.abbot = mk("abbot")
	if l.domain, err = l.svc.CreateDomain(ctx, l.abbot.ID, "Ladder"); err != nil {
		t.Fatal(err)
	}
	roles, _ := l.svc.Roles(ctx, l.domain.ID)
	for _, r := range roles {
		l.roles[r.Name] = r
	}
	if l.invite, err = l.svc.CreateInvite(ctx, l.domain.ID, l.abbot.ID, 0, 0); err != nil {
		t.Fatal(err)
	}
	join := func(name, role string) User {
		u := mk(name)
		if _, err := l.svc.JoinByInvite(ctx, l.invite, u.ID); err != nil {
			t.Fatal(err)
		}
		if role != "Brother / Sister" {
			if err := l.svc.AssignRole(ctx, l.domain.ID, l.abbot.ID, u.ID, l.roles[role].ID); err != nil {
				t.Fatalf("set %s to %s: %v", name, role, err)
			}
		}
		return u
	}
	l.bishop = join("bishop", "Bishop")
	l.warden = join("warden", "Warden")
	l.warden2 = join("warden2", "Warden")
	l.brother = join("brother", "Brother / Sister")
	l.sister = join("sister", "Brother / Sister")
	l.postulant = join("postulant", "Postulant")
	return l
}

func (l *ladder) roleOf(u User) string {
	l.t.Helper()
	ms, err := l.svc.Members(l.ctx, l.domain.ID)
	if err != nil {
		l.t.Fatal(err)
	}
	for _, m := range ms {
		if m.UserID == u.ID {
			return m.Role.Name
		}
	}
	return ""
}

func (l *ladder) lastAudit() (actor, action, target, detail string) {
	l.t.Helper()
	err := l.svc.st.DB.QueryRow(
		`SELECT actor_id, action, target, detail FROM audit_log WHERE domain_id = ? ORDER BY id DESC LIMIT 1`,
		l.domain.ID).Scan(&actor, &action, &target, &detail)
	if err != nil {
		l.t.Fatal(err)
	}
	return
}

func TestRoleAssignmentRules(t *testing.T) {
	l := newLadder(t)
	type tc struct {
		name          string
		actor, target User
		role          string
		ok            bool
	}
	cases := []tc{
		// The Abbot may appoint anyone below Abbot to any role below Abbot.
		{"abbot makes brother a bishop", l.abbot, l.brother, "Bishop", true},
		{"abbot demotes that bishop to warden", l.abbot, l.brother, "Warden", true},
		{"abbot sets warden back to brother", l.abbot, l.brother, "Brother / Sister", true},
		{"abbot makes brother a postulant", l.abbot, l.brother, "Postulant", true},
		{"abbot cannot grant abbot", l.abbot, l.brother, "Abbot", false},
		{"abbot cannot change own role", l.abbot, l.abbot, "Bishop", false},
		// A Bishop may set people below Bishop to Warden or lower.
		{"bishop makes postulant a warden", l.bishop, l.postulant, "Warden", true},
		{"bishop demotes that warden", l.bishop, l.postulant, "Postulant", true},
		{"bishop demotes a warden to brother", l.bishop, l.warden2, "Brother / Sister", true},
		{"bishop promotes brother to warden", l.bishop, l.warden2, "Warden", true},
		{"bishop cannot make a bishop", l.bishop, l.sister, "Bishop", false},
		{"bishop cannot make an abbot", l.bishop, l.sister, "Abbot", false},
		{"bishop cannot act on abbot", l.bishop, l.abbot, "Warden", false},
		// Wardens cannot change roles at all.
		{"warden cannot promote to warden", l.warden, l.sister, "Warden", false},
		{"warden cannot demote a member", l.warden, l.sister, "Postulant", false},
		{"warden cannot demote a warden", l.warden, l.warden2, "Brother / Sister", false},
		{"warden cannot act on bishop", l.warden, l.bishop, "Brother / Sister", false},
		// Members cannot change roles.
		{"brother cannot promote", l.sister, l.postulant, "Brother / Sister", false},
	}
	for _, c := range cases {
		before := l.roleOf(c.target)
		err := l.svc.AssignRole(l.ctx, l.domain.ID, c.actor.ID, c.target.ID, l.roles[c.role].ID)
		if c.ok {
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if got := l.roleOf(c.target); got != c.role {
				t.Fatalf("%s: role is %q, want %q", c.name, got, c.role)
			}
			actor, action, target, detail := l.lastAudit()
			if actor != c.actor.ID || action != "member.role" || target != c.target.ID || detail == "" {
				t.Fatalf("%s: audit = %q %q %q %q", c.name, actor, action, target, detail)
			}
			continue
		}
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s: got %v, want ErrForbidden", c.name, err)
		}
		if got := l.roleOf(c.target); got != before {
			t.Fatalf("%s: role changed to %q", c.name, got)
		}
	}
}

func TestMuteRules(t *testing.T) {
	l := newLadder(t)
	ok := []struct {
		name          string
		actor, target User
	}{
		{"warden mutes brother", l.warden, l.brother},
		{"warden mutes postulant", l.warden, l.postulant},
		{"bishop mutes warden", l.bishop, l.warden2},
		{"abbot mutes bishop", l.abbot, l.bishop},
	}
	for _, c := range ok {
		if err := l.svc.SetMuted(l.ctx, l.domain.ID, c.actor.ID, c.target.ID, true); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if _, action, _, _ := l.lastAudit(); action != "member.mute" {
			t.Fatalf("%s: audit action %q", c.name, action)
		}
		if err := l.svc.SetMuted(l.ctx, l.domain.ID, c.actor.ID, c.target.ID, false); err != nil {
			t.Fatalf("%s (unmute): %v", c.name, err)
		}
	}
	denied := []struct {
		name          string
		actor, target User
	}{
		{"warden cannot mute warden", l.warden, l.warden2},
		{"warden cannot mute bishop", l.warden, l.bishop},
		{"warden cannot mute abbot", l.warden, l.abbot},
		{"bishop cannot mute abbot", l.bishop, l.abbot},
		{"brother cannot mute postulant", l.brother, l.postulant},
	}
	for _, c := range denied {
		if err := l.svc.SetMuted(l.ctx, l.domain.ID, c.actor.ID, c.target.ID, true); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s: got %v, want ErrForbidden", c.name, err)
		}
	}
}

func TestTemporaryBan(t *testing.T) {
	l := newLadder(t)
	// A Warden bans a member for an hour: they leave the domain.
	if err := l.svc.Ban(l.ctx, l.domain.ID, l.warden.ID, l.brother.ID, "spam", time.Hour); err != nil {
		t.Fatal(err)
	}
	if l.roleOf(l.brother) != "" {
		t.Fatal("banned member still in the domain")
	}
	actor, action, target, detail := l.lastAudit()
	if actor != l.warden.ID || action != "member.ban" || target != l.brother.ID || detail == "" {
		t.Fatalf("audit = %q %q %q %q", actor, action, target, detail)
	}
	bans, err := l.svc.Bans(l.ctx, l.domain.ID, l.warden.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bans) != 1 || bans[0].UserID != l.brother.ID || bans[0].ExpiresAt != l.clock.Add(time.Hour).Unix() ||
		bans[0].FormerRank != l.roles["Brother / Sister"].Rank || bans[0].BannedBy != "warden" || bans[0].Reason != "spam" {
		t.Fatalf("bans = %+v", bans)
	}
	// Members cannot see the bans list.
	if _, err := l.svc.Bans(l.ctx, l.domain.ID, l.sister.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member lists bans: got %v, want ErrForbidden", err)
	}
	// Still banned just before it ends.
	l.clock = l.clock.Add(59 * time.Minute)
	if _, err := l.svc.JoinByInvite(l.ctx, l.invite, l.brother.ID); !errors.Is(err, ErrBanned) {
		t.Fatalf("join during ban: got %v, want ErrBanned", err)
	}
	// Lifted once it ends: joining works and the lift is audited.
	l.clock = l.clock.Add(time.Minute)
	if _, err := l.svc.JoinByInvite(l.ctx, l.invite, l.brother.ID); err != nil {
		t.Fatalf("join after ban ended: %v", err)
	}
	var n int
	l.svc.st.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'member.ban_expired' AND target = ?`, l.brother.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("ban_expired audit entries = %d", n)
	}
	if bans, _ := l.svc.Bans(l.ctx, l.domain.ID, l.warden.ID); len(bans) != 0 {
		t.Fatalf("bans after expiry = %+v", bans)
	}
}

func TestBanRules(t *testing.T) {
	l := newLadder(t)
	denied := []struct {
		name          string
		actor, target User
	}{
		{"warden cannot ban warden", l.warden, l.warden2},
		{"warden cannot ban bishop", l.warden, l.bishop},
		{"warden cannot ban abbot", l.warden, l.abbot},
		{"bishop cannot ban abbot", l.bishop, l.abbot},
		{"brother cannot ban postulant", l.brother, l.postulant},
	}
	for _, c := range denied {
		if err := l.svc.Ban(l.ctx, l.domain.ID, c.actor.ID, c.target.ID, "", time.Hour); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s: got %v, want ErrForbidden", c.name, err)
		}
	}
	if err := l.svc.Ban(l.ctx, l.domain.ID, l.warden.ID, l.postulant.ID, "", -time.Second); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative duration: got %v, want ErrInvalidInput", err)
	}
	// Permanent ban never ends.
	if err := l.svc.Ban(l.ctx, l.domain.ID, l.warden.ID, l.postulant.ID, "", 0); err != nil {
		t.Fatal(err)
	}
	l.clock = l.clock.Add(100 * 365 * 24 * time.Hour)
	if _, err := l.svc.JoinByInvite(l.ctx, l.invite, l.postulant.ID); !errors.Is(err, ErrBanned) {
		t.Fatalf("join after permanent ban: got %v, want ErrBanned", err)
	}
	if ids, err := l.svc.SweepExpiredBans(l.ctx); err != nil || len(ids) != 0 {
		t.Fatalf("sweep lifted a permanent ban: %v %v", ids, err)
	}
}

func TestUnbanRules(t *testing.T) {
	l := newLadder(t)
	// The Abbot bans a Warden; a Warden cannot lift it (equal rank), a Bishop can.
	if err := l.svc.Ban(l.ctx, l.domain.ID, l.abbot.ID, l.warden2.ID, "", 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := l.svc.Unban(l.ctx, l.domain.ID, l.warden.ID, l.warden2.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("warden lifts warden's ban: got %v, want ErrForbidden", err)
	}
	if err := l.svc.Unban(l.ctx, l.domain.ID, l.brother.ID, l.warden2.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member lifts ban: got %v, want ErrForbidden", err)
	}
	if err := l.svc.Unban(l.ctx, l.domain.ID, l.bishop.ID, l.warden2.ID); err != nil {
		t.Fatalf("bishop lifts warden's ban: %v", err)
	}
	if actor, action, target, _ := l.lastAudit(); actor != l.bishop.ID || action != "member.unban" || target != l.warden2.ID {
		t.Fatalf("audit = %q %q %q", actor, action, target)
	}
	// A Warden may lift a member's ban, even one the Abbot set.
	if err := l.svc.Ban(l.ctx, l.domain.ID, l.abbot.ID, l.sister.ID, "", 0); err != nil {
		t.Fatal(err)
	}
	if err := l.svc.Unban(l.ctx, l.domain.ID, l.warden.ID, l.sister.ID); err != nil {
		t.Fatalf("warden lifts member's ban: %v", err)
	}
	if _, err := l.svc.JoinByInvite(l.ctx, l.invite, l.sister.ID); err != nil {
		t.Fatalf("rejoin after unban: %v", err)
	}
	// Unknown ban.
	if err := l.svc.Unban(l.ctx, l.domain.ID, l.warden.ID, l.sister.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unban non-banned: got %v, want ErrNotFound", err)
	}
}

func TestSweepExpiredBans(t *testing.T) {
	l := newLadder(t)
	if err := l.svc.Ban(l.ctx, l.domain.ID, l.warden.ID, l.brother.ID, "", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := l.svc.Ban(l.ctx, l.domain.ID, l.warden.ID, l.sister.ID, "", 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if ids, err := l.svc.SweepExpiredBans(l.ctx); err != nil || len(ids) != 0 {
		t.Fatalf("early sweep: %v %v", ids, err)
	}
	l.clock = l.clock.Add(2 * time.Hour)
	ids, err := l.svc.SweepExpiredBans(l.ctx)
	if err != nil || len(ids) != 1 || ids[0] != l.domain.ID {
		t.Fatalf("sweep: %v %v", ids, err)
	}
	bans, _ := l.svc.Bans(l.ctx, l.domain.ID, l.abbot.ID)
	if len(bans) != 1 || bans[0].UserID != l.sister.ID {
		t.Fatalf("bans after sweep = %+v", bans)
	}
	if actor, action, target, _ := l.lastAudit(); actor != "" || action != "member.ban_expired" || target != l.brother.ID {
		t.Fatalf("audit = %q %q %q", actor, action, target)
	}
}
