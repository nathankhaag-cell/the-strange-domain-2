package blobs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
)

type fixture struct {
	ctx    context.Context
	st     *store.Store
	s      *Service
	user   string
	chapel string
	clock  time.Time
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dom := domains.NewService(st)
	u, err := dom.CreateUser(ctx, "abbot")
	if err != nil {
		t.Fatal(err)
	}
	d, err := dom.CreateDomain(ctx, u.ID, "Sector 7")
	if err != nil {
		t.Fatal(err)
	}
	chs, _ := dom.Channels(ctx, d.ID)
	f := &fixture{ctx: ctx, st: st, s: NewService(st), user: u.ID, chapel: chs[0].ID, clock: time.Unix(1_800_000_000, 0)}
	f.s.now = func() time.Time { return f.clock }
	return f
}

func (f *fixture) message(t *testing.T, deleted bool) int64 {
	t.Helper()
	f.st.DB.Exec(`INSERT OR IGNORE INTO mls_groups (id) VALUES (?)`, f.chapel)
	res, err := f.st.DB.Exec(`INSERT INTO mls_messages (group_id, sender_user, sender_device, kind, epoch, data, deleted, created_at)
		VALUES (?, ?, 'd', 'application', 0, x'00', ?, 0)`, f.chapel, f.user, deleted)
	if err != nil {
		t.Fatal(err)
	}
	seq, _ := res.LastInsertId()
	return seq
}

func TestSweepRemovesOrphans(t *testing.T) {
	f := setup(t)
	put := func() Blob {
		b, err := f.s.Put(f.ctx, f.user, f.chapel, bytes.NewReader([]byte("ciphertext")))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	pendingOld := put()
	attached := put()
	attachedToDeleted := put()
	f.s.Attach(f.ctx, f.user, f.chapel, f.message(t, false), []string{attached.ID})
	f.s.Attach(f.ctx, f.user, f.chapel, f.message(t, true), []string{attachedToDeleted.ID})

	f.clock = f.clock.Add(PendingTTL + time.Minute)
	pendingNew := put()
	if err := f.s.Sweep(f.ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		b    Blob
		kept bool
	}{{pendingOld, false}, {attached, true}, {attachedToDeleted, false}, {pendingNew, true}} {
		_, err := f.s.Get(f.ctx, c.b.ID)
		_, ferr := os.Stat(f.s.path(c.b.ID))
		if kept := err == nil && ferr == nil; kept != c.kept {
			t.Errorf("blob kept=%v, want %v (row err %v, file err %v)", kept, c.kept, err, ferr)
		}
	}
}

func TestPendingLimit(t *testing.T) {
	f := setup(t)
	for i := 0; i < MaxPending; i++ {
		if _, err := f.s.Put(f.ctx, f.user, f.chapel, bytes.NewReader([]byte("x"))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.s.Put(f.ctx, f.user, f.chapel, bytes.NewReader([]byte("x"))); !errors.Is(err, ErrTooPending) {
		t.Fatalf("got %v, want ErrTooPending", err)
	}
}

func TestBadIDs(t *testing.T) {
	f := setup(t)
	for _, id := range []string{"", "../../node.db", "ABCDEF0123456789ABCDEF0123456789", "zz"} {
		if _, err := f.s.Get(f.ctx, id); !errors.Is(err, domains.ErrNotFound) {
			t.Errorf("Get(%q) = %v", id, err)
		}
	}
}
