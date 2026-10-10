package relay

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
)

type recorder struct {
	mu     sync.Mutex
	events map[string][]Event
}

func (r *recorder) Notify(users []string, ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range users {
		r.events[u] = append(r.events[u], ev)
	}
}

type fixture struct {
	ctx                      context.Context
	st                       *store.Store
	dom                      *domains.Service
	rl                       *Service
	rec                      *recorder
	domain                   domains.Domain
	chapel                   string
	abbot, brother, outsider domains.User
	devAbbot, devBrother     string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &fixture{ctx: ctx, st: st, dom: domains.NewService(st), rec: &recorder{events: map[string][]Event{}}}
	f.rl = NewService(st, f.rec)
	mk := func(name string) (domains.User, string) {
		u, err := f.dom.CreateUser(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		dev := store.NewID()
		if _, err := st.DB.Exec(`INSERT INTO devices (id, user_id, name, identity_pk, created_at) VALUES (?, ?, 'laptop', ?, 0)`,
			dev, u.ID, []byte(dev)); err != nil {
			t.Fatal(err)
		}
		return u, dev
	}
	f.abbot, f.devAbbot = mk("abbot")
	f.brother, f.devBrother = mk("brother")
	f.outsider, _ = mk("outsider")
	if f.domain, err = f.dom.CreateDomain(ctx, f.abbot.ID, "Sector 7"); err != nil {
		t.Fatal(err)
	}
	inv, _ := f.dom.CreateInvite(ctx, f.domain.ID, f.abbot.ID, 0, 0)
	if _, err := f.dom.JoinByInvite(ctx, inv, f.brother.ID); err != nil {
		t.Fatal(err)
	}
	chs, _ := f.dom.Channels(ctx, f.domain.ID)
	f.chapel = chs[0].ID
	return f
}

func TestCommitOrdering(t *testing.T) {
	f := setup(t)
	if _, err := f.rl.Send(f.ctx, f.chapel, f.abbot.ID, f.devAbbot, KindCommit, 0, []byte("c0")); err != nil {
		t.Fatal(err)
	}
	// A second commit for epoch 0 loses the race.
	if _, err := f.rl.Send(f.ctx, f.chapel, f.brother.ID, f.devBrother, KindCommit, 0, []byte("c0b")); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("racing commit: got %v, want ErrStaleEpoch", err)
	}
	if _, err := f.rl.Send(f.ctx, f.chapel, f.brother.ID, f.devBrother, KindApplication, 1, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if e, _ := f.rl.Epoch(f.ctx, f.chapel, f.brother.ID); e != 1 {
		t.Fatalf("epoch %d, want 1", e)
	}
	msgs, err := f.rl.Fetch(f.ctx, f.chapel, f.abbot.ID, 0, 0)
	if err != nil || len(msgs) != 2 || string(msgs[1].Data) != "hello" {
		t.Fatalf("fetch: %v %+v", err, msgs)
	}
	after, _ := f.rl.Fetch(f.ctx, f.chapel, f.abbot.ID, msgs[0].Seq, 0)
	if len(after) != 1 {
		t.Fatalf("fetch after: %d", len(after))
	}
	if n := len(f.rec.events[f.brother.ID]); n != 2 {
		t.Fatalf("brother got %d events, want 2", n)
	}
}

func TestChannelAccess(t *testing.T) {
	f := setup(t)
	if _, err := f.rl.Fetch(f.ctx, f.chapel, f.outsider.ID, 0, 0); !errors.Is(err, domains.ErrNotMember) {
		t.Fatalf("outsider read: %v", err)
	}
	if err := f.dom.SetMuted(f.ctx, f.domain.ID, f.abbot.ID, f.brother.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rl.Send(f.ctx, f.chapel, f.brother.ID, f.devBrother, KindApplication, 0, []byte("x")); !errors.Is(err, domains.ErrForbidden) {
		t.Fatalf("muted send: %v", err)
	}
	// Muted members can still commit to keep MLS state in sync.
	if _, err := f.rl.Send(f.ctx, f.chapel, f.brother.ID, f.devBrother, KindCommit, 0, []byte("c")); err != nil {
		t.Fatalf("muted commit: %v", err)
	}
	if err := f.dom.Kick(f.ctx, f.domain.ID, f.abbot.ID, f.brother.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rl.Fetch(f.ctx, f.chapel, f.brother.ID, 0, 0); !errors.Is(err, domains.ErrNotMember) {
		t.Fatalf("kicked read: %v", err)
	}
}

func TestDeleteRules(t *testing.T) {
	f := setup(t)
	m, err := f.rl.Send(f.ctx, f.chapel, f.abbot.ID, f.devAbbot, KindApplication, 0, []byte("abbot says"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.rl.Delete(f.ctx, f.chapel, m.Seq, f.brother.ID); !errors.Is(err, domains.ErrForbidden) {
		t.Fatalf("member deletes abbot: %v", err)
	}
	b, _ := f.rl.Send(f.ctx, f.chapel, f.brother.ID, f.devBrother, KindApplication, 0, []byte("brother says"))
	if err := f.rl.Delete(f.ctx, f.chapel, b.Seq, f.abbot.ID); err != nil {
		t.Fatalf("abbot deletes member: %v", err)
	}
	msgs, _ := f.rl.Fetch(f.ctx, f.chapel, f.abbot.ID, 0, 0)
	if !msgs[1].Deleted || msgs[1].Data != nil {
		t.Fatalf("tombstone not applied: %+v", msgs[1])
	}
}

func TestModeratorDeleteByRank(t *testing.T) {
	f := setup(t)
	roles, _ := f.dom.Roles(f.ctx, f.domain.ID)
	roleID := map[string]string{}
	for _, r := range roles {
		roleID[r.Name] = r.ID
	}
	inv, _ := f.dom.CreateInvite(f.ctx, f.domain.ID, f.abbot.ID, 0, 0)
	join := func(name, role string) (domains.User, string) {
		u, _ := f.dom.CreateUser(f.ctx, name)
		dev := store.NewID()
		f.st.DB.Exec(`INSERT INTO devices (id, user_id, name, identity_pk, created_at) VALUES (?, ?, 'x', ?, 0)`, dev, u.ID, []byte(dev))
		if _, err := f.dom.JoinByInvite(f.ctx, inv, u.ID); err != nil {
			t.Fatal(err)
		}
		if err := f.dom.AssignRole(f.ctx, f.domain.ID, f.abbot.ID, u.ID, roleID[role]); err != nil {
			t.Fatal(err)
		}
		return u, dev
	}
	bishop, devBishop := join("bishop", "Bishop")
	warden, devWarden := join("warden", "Warden")
	warden2, devWarden2 := join("warden2", "Warden")
	send := func(u domains.User, dev, text string) int64 {
		m, err := f.rl.Send(f.ctx, f.chapel, u.ID, dev, KindApplication, 0, []byte(text))
		if err != nil {
			t.Fatal(err)
		}
		return m.Seq
	}
	// A Warden deletes a member's message; it is audited without content.
	seq := send(f.brother, f.devBrother, "brother says")
	if err := f.rl.Delete(f.ctx, f.chapel, seq, warden.ID); err != nil {
		t.Fatalf("warden deletes member: %v", err)
	}
	var actor, action, target, detail string
	if err := f.st.DB.QueryRow(`SELECT actor_id, action, target, detail FROM audit_log WHERE domain_id = ? ORDER BY id DESC LIMIT 1`,
		f.domain.ID).Scan(&actor, &action, &target, &detail); err != nil {
		t.Fatal(err)
	}
	if actor != warden.ID || action != "message.delete" || target != f.brother.ID || detail == "" {
		t.Fatalf("audit = %q %q %q %q", actor, action, target, detail)
	}
	// Every member is told, so all clients show it as removed.
	for _, u := range []string{f.abbot.ID, f.brother.ID, bishop.ID, warden2.ID} {
		evs := f.rec.events[u]
		if len(evs) == 0 || evs[len(evs)-1].Type != "deleted" || evs[len(evs)-1].Seq != seq {
			t.Fatalf("user %s not told about the delete: %+v", u, evs)
		}
	}
	// A Warden cannot delete an equal or higher rank's message.
	for name, s := range map[string]int64{
		"warden2": send(warden2, devWarden2, "w2"),
		"bishop":  send(bishop, devBishop, "b"),
		"abbot":   send(f.abbot, f.devAbbot, "a"),
	} {
		if err := f.rl.Delete(f.ctx, f.chapel, s, warden.ID); !errors.Is(err, domains.ErrForbidden) {
			t.Fatalf("warden deletes %s: got %v, want ErrForbidden", name, err)
		}
	}
	// A Bishop can delete a Warden's message; the Warden can delete their own.
	if err := f.rl.Delete(f.ctx, f.chapel, send(warden2, devWarden2, "w2 again"), bishop.ID); err != nil {
		t.Fatalf("bishop deletes warden: %v", err)
	}
	if err := f.rl.Delete(f.ctx, f.chapel, send(warden, devWarden, "mine"), warden.ID); err != nil {
		t.Fatalf("warden deletes own: %v", err)
	}
	// Rank is checked at delete time: once demoted, a former Warden's message is fair game.
	s := send(warden2, devWarden2, "before demotion")
	if err := f.dom.AssignRole(f.ctx, f.domain.ID, f.abbot.ID, warden2.ID, roleID["Brother / Sister"]); err != nil {
		t.Fatal(err)
	}
	if err := f.rl.Delete(f.ctx, f.chapel, s, warden.ID); err != nil {
		t.Fatalf("warden deletes demoted member: %v", err)
	}
	// Members cannot delete others' messages.
	if err := f.rl.Delete(f.ctx, f.chapel, send(warden2, devWarden2, "x"), f.brother.ID); !errors.Is(err, domains.ErrForbidden) {
		t.Fatalf("member deletes member: got %v, want ErrForbidden", err)
	}
}

func TestConclaveAndKeyPackages(t *testing.T) {
	f := setup(t)
	if _, err := f.rl.CreateConclave(f.ctx, f.abbot.ID, []string{f.outsider.ID}); !errors.Is(err, domains.ErrForbidden) {
		t.Fatalf("conclave with stranger: %v", err)
	}
	cid, err := f.rl.CreateConclave(f.ctx, f.abbot.ID, []string{f.brother.ID})
	if err != nil {
		t.Fatal(err)
	}

	if err := f.rl.PublishKeyPackages(f.ctx, f.devBrother, [][]byte{[]byte("kp1"), []byte("kp2")}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rl.ClaimKeyPackages(f.ctx, f.outsider.ID, f.brother.ID); !errors.Is(err, domains.ErrForbidden) {
		t.Fatalf("outsider claims: %v", err)
	}
	kps, err := f.rl.ClaimKeyPackages(f.ctx, f.abbot.ID, f.brother.ID)
	if err != nil || len(kps) != 1 || string(kps[0].KeyPackage) != "kp1" {
		t.Fatalf("claim: %v %+v", err, kps)
	}

	if err := f.rl.SendWelcome(f.ctx, cid, f.abbot.ID, f.devBrother, []byte("welcome")); err != nil {
		t.Fatal(err)
	}
	ws, err := f.rl.TakeWelcomes(f.ctx, f.devBrother)
	if err != nil || len(ws) != 1 || ws[0].GroupID != cid {
		t.Fatalf("welcomes: %v %+v", err, ws)
	}
	if again, _ := f.rl.TakeWelcomes(f.ctx, f.devBrother); len(again) != 0 {
		t.Fatal("welcomes should be consumed")
	}

	if _, err := f.rl.Send(f.ctx, cid, f.brother.ID, f.devBrother, KindApplication, 0, []byte("confession")); err != nil {
		t.Fatal(err)
	}
	// Nobody moderates a Conclave: the abbot can't delete the brother's message there.
	if err := f.rl.Delete(f.ctx, cid, 1, f.abbot.ID); !errors.Is(err, domains.ErrForbidden) {
		t.Fatalf("delete in conclave: %v", err)
	}
	if err := f.rl.LeaveConclave(f.ctx, cid, f.brother.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rl.Fetch(f.ctx, cid, f.brother.ID, 0, 0); !errors.Is(err, domains.ErrNotMember) {
		t.Fatalf("read after leaving: %v", err)
	}
}

func TestHubDropsSlowClient(t *testing.T) {
	h := NewHub()
	ch, unsub := h.Subscribe("u")
	defer unsub()
	for i := 0; i < 100; i++ {
		h.Notify([]string{"u"}, Event{Type: "message", Seq: int64(i)})
	}
	n := 0
	for range ch {
		n++
	}
	if n != 64 {
		t.Fatalf("got %d buffered events before close, want 64", n)
	}
}
