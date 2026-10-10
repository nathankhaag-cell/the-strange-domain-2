// Package domains implements user-created domains (like Discord servers):
// ownership, roles, membership, moderation, channels and invites.
package domains

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/perm"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/store"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrNotMember     = errors.New("not a member of this domain")
	ErrForbidden     = errors.New("not allowed")
	ErrBanned        = errors.New("banned from this domain")
	ErrAlreadyMember = errors.New("already a member")
	ErrInvalidInvite = errors.New("invite is invalid or expired")
	ErrInvalidInput  = errors.New("invalid input")
)

// DefaultRole describes a role created with every new domain.
type DefaultRole struct {
	Name        string
	Rank        int
	Permissions perm.Permission
	IsDefault   bool // given to people who join by invite
}

// DefaultRoles are the roles every new domain starts with, using the role
// names Nathan approved. Owners can rename roles in their own domain.
var DefaultRoles = []DefaultRole{
	{Name: "Abbot", Rank: perm.RankOwner, Permissions: perm.All},
	{Name: "Bishop", Rank: perm.RankSeniorMod, Permissions: perm.ManageChannels | perm.ManageRoles |
		perm.CreateInvite | perm.Kick | perm.Ban | perm.Mute | perm.DeleteMessages | perm.PinMessages |
		perm.SendMessages | perm.JoinVoice},
	{Name: "Warden", Rank: perm.RankModerator, Permissions: perm.CreateInvite | perm.Kick | perm.Ban | perm.Mute |
		perm.DeleteMessages | perm.PinMessages | perm.SendMessages | perm.JoinVoice},
	{Name: "Brother / Sister", Rank: perm.RankMember, Permissions: perm.CreateInvite | perm.SendMessages | perm.JoinVoice,
		IsDefault: true},
	{Name: "Postulant", Rank: perm.RankNewcomer, Permissions: perm.SendMessages},
}

type User struct {
	ID       string
	Callsign string
}

type Domain struct {
	ID      string
	Name    string
	OwnerID string
}

type Role struct {
	ID          string
	DomainID    string
	Name        string
	Rank        int
	Permissions perm.Permission
}

type Member struct {
	UserID   string
	Callsign string
	Role     Role
	Muted    bool
}

type Channel struct {
	ID       string
	DomainID string
	Name     string
	Kind     string
}

// Service applies the domain rules on top of the store.
type Service struct {
	st  *store.Store
	now func() time.Time
}

func NewService(st *store.Store) *Service {
	return &Service{st: st, now: time.Now}
}

func (s *Service) CreateUser(ctx context.Context, callsign string) (User, error) {
	callsign = strings.TrimSpace(callsign)
	if callsign == "" || len(callsign) > 32 {
		return User{}, fmt.Errorf("%w: callsign must be 1-32 characters", ErrInvalidInput)
	}
	u := User{ID: store.NewID(), Callsign: callsign}
	_, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO users (id, callsign, created_at) VALUES (?, ?, ?)`, u.ID, u.Callsign, s.now().Unix())
	if err != nil {
		return User{}, err
	}
	return u, nil
}

// CreateDomain creates a domain owned by ownerID with the default roles
// and one text and one voice channel.
func (s *Service) CreateDomain(ctx context.Context, ownerID, name string) (Domain, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return Domain{}, fmt.Errorf("%w: domain name must be 1-64 characters", ErrInvalidInput)
	}
	d := Domain{ID: store.NewID(), Name: name, OwnerID: ownerID}
	now := s.now().Unix()
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO domains (id, name, owner_id, created_at) VALUES (?, ?, ?, ?)`,
			d.ID, d.Name, ownerID, now); err != nil {
			return err
		}
		var ownerRole string
		for _, r := range DefaultRoles {
			id := store.NewID()
			if r.Rank == perm.RankOwner {
				ownerRole = id
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO roles (id, domain_id, name, rank, permissions, is_default) VALUES (?, ?, ?, ?, ?, ?)`,
				id, d.ID, r.Name, r.Rank, int64(r.Permissions), r.IsDefault); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO members (domain_id, user_id, role_id, joined_at) VALUES (?, ?, ?, ?)`,
			d.ID, ownerID, ownerRole, now); err != nil {
			return err
		}
		for i, ch := range []struct{ name, kind string }{{"general", "text"}, {"voice", "voice"}} {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO channels (id, domain_id, name, kind, position, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
				store.NewID(), d.ID, ch.name, ch.kind, i, now); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, d.ID, ownerID, "domain.create", "", d.Name)
	})
	if err != nil {
		return Domain{}, err
	}
	return d, nil
}

// CreateInvite returns an invite token. maxUses 0 means unlimited; ttl 0 means no expiry.
func (s *Service) CreateInvite(ctx context.Context, domainID, actorID string, maxUses int, ttl time.Duration) (string, error) {
	token := store.NewToken()
	err := s.tx(ctx, func(tx *sql.Tx) error {
		a, err := s.actor(ctx, tx, domainID, actorID)
		if err != nil {
			return err
		}
		if !a.IsOwner && !a.Perms.Has(perm.CreateInvite) {
			return ErrForbidden
		}
		var expires int64
		if ttl > 0 {
			expires = s.now().Add(ttl).Unix()
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO invites (token, domain_id, created_by, max_uses, expires_at) VALUES (?, ?, ?, ?, ?)`,
			token, domainID, actorID, maxUses, expires); err != nil {
			return err
		}
		return s.audit(ctx, tx, domainID, actorID, "invite.create", "", "")
	})
	return token, err
}

// JoinByInvite adds userID to the invite's domain with the domain's default role.
func (s *Service) JoinByInvite(ctx context.Context, token, userID string) (Domain, error) {
	var d Domain
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var maxUses, uses int
		var expires int64
		err := tx.QueryRowContext(ctx,
			`SELECT i.domain_id, i.max_uses, i.uses, i.expires_at, d.name, d.owner_id
			   FROM invites i JOIN domains d ON d.id = i.domain_id WHERE i.token = ?`, token).
			Scan(&d.ID, &maxUses, &uses, &expires, &d.Name, &d.OwnerID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidInvite
		}
		if err != nil {
			return err
		}
		if (maxUses > 0 && uses >= maxUses) || (expires > 0 && s.now().Unix() >= expires) {
			return ErrInvalidInvite
		}
		banned, err := s.activeBan(ctx, tx, d.ID, userID)
		if err != nil {
			return err
		}
		if banned {
			return ErrBanned
		}
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM members WHERE domain_id = ? AND user_id = ?`, d.ID, userID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrAlreadyMember
		}
		var roleID string
		if err := tx.QueryRowContext(ctx,
			`SELECT id FROM roles WHERE domain_id = ? AND is_default = 1 ORDER BY rank LIMIT 1`, d.ID).
			Scan(&roleID); err != nil {
			return fmt.Errorf("domain has no default role: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO members (domain_id, user_id, role_id, joined_at) VALUES (?, ?, ?, ?)`,
			d.ID, userID, roleID, s.now().Unix()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE invites SET uses = uses + 1 WHERE token = ?`, token); err != nil {
			return err
		}
		return s.audit(ctx, tx, d.ID, userID, "member.join", userID, "")
	})
	return d, err
}

// AssignRole gives targetID the role roleID. The actor needs ManageRoles (the
// owner always has it) and must outrank both the target's current role and
// the new role. With the default roles: the Abbot may set anyone else to
// Bishop, Warden, Brother / Sister or Postulant; a Bishop may set people
// below Bishop to Warden, Brother / Sister or Postulant; Wardens and below
// cannot change roles. Nobody can hand out the owner role.
func (s *Service) AssignRole(ctx context.Context, domainID, actorID, targetID, roleID string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		a, err := s.actor(ctx, tx, domainID, actorID)
		if err != nil {
			return err
		}
		t, err := s.actor(ctx, tx, domainID, targetID)
		if err != nil {
			return err
		}
		var rank int
		var name string
		err = tx.QueryRowContext(ctx, `SELECT rank, name FROM roles WHERE id = ? AND domain_id = ?`, roleID, domainID).
			Scan(&rank, &name)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !perm.CanActOn(a, perm.ManageRoles, t.Rank, t.IsOwner) || !perm.CanGrantRank(a, rank) {
			return ErrForbidden
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE members SET role_id = ? WHERE domain_id = ? AND user_id = ?`, roleID, domainID, targetID); err != nil {
			return err
		}
		return s.audit(ctx, tx, domainID, actorID, "member.role", targetID, fmt.Sprintf("%s (%s)", name, roleID))
	})
}

// Kick removes targetID from the domain; they can rejoin with a new invite.
func (s *Service) Kick(ctx context.Context, domainID, actorID, targetID string) error {
	return s.moderate(ctx, domainID, actorID, targetID, perm.Kick, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM members WHERE domain_id = ? AND user_id = ?`, domainID, targetID)
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, domainID, actorID, "member.kick", targetID, "")
	})
}

// Ban removes targetID (like a kick) and blocks them from rejoining. A
// duration of 0 makes the ban permanent; otherwise it lifts itself once the
// duration has passed. The actor needs the ban permission and must outrank
// the target.
func (s *Service) Ban(ctx context.Context, domainID, actorID, targetID, reason string, duration time.Duration) error {
	if duration < 0 || len(reason) > 200 {
		return fmt.Errorf("%w: ban needs a duration of 0 (permanent) or more and a reason of up to 200 characters", ErrInvalidInput)
	}
	return s.moderate(ctx, domainID, actorID, targetID, perm.Ban, func(tx *sql.Tx) error {
		t, err := s.actor(ctx, tx, domainID, targetID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM members WHERE domain_id = ? AND user_id = ?`, domainID, targetID); err != nil {
			return err
		}
		now := s.now()
		var expires int64
		detail := "permanent"
		if duration > 0 {
			expires = now.Add(duration).Unix()
			detail = "until " + time.Unix(expires, 0).UTC().Format(time.RFC3339)
		}
		if reason != "" {
			detail += "; " + reason
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO bans (domain_id, user_id, banned_by, reason, banned_at, expires_at, target_rank)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			domainID, targetID, actorID, reason, now.Unix(), expires, t.Rank); err != nil {
			return err
		}
		return s.audit(ctx, tx, domainID, actorID, "member.ban", targetID, detail)
	})
}

// Unban lifts a ban early. The actor needs the ban permission and must
// outrank the banned person's rank at the time of the ban; the owner may
// lift any ban. Bans whose time is up count as already lifted.
func (s *Service) Unban(ctx context.Context, domainID, actorID, targetID string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		a, err := s.actor(ctx, tx, domainID, actorID)
		if err != nil {
			return err
		}
		if !a.IsOwner && !a.Perms.Has(perm.Ban) {
			return ErrForbidden
		}
		var rank int
		var expires int64
		err = tx.QueryRowContext(ctx, `SELECT target_rank, expires_at FROM bans WHERE domain_id = ? AND user_id = ?`,
			domainID, targetID).Scan(&rank, &expires)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if expires > 0 && s.now().Unix() >= expires {
			if err := s.liftExpired(ctx, tx, domainID, targetID); err != nil {
				return err
			}
			return ErrNotFound
		}
		if !perm.CanLiftBan(a, rank) {
			return ErrForbidden
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM bans WHERE domain_id = ? AND user_id = ?`, domainID, targetID); err != nil {
			return err
		}
		return s.audit(ctx, tx, domainID, actorID, "member.unban", targetID, "")
	})
}

// Ban is one active ban in a domain.
type Ban struct {
	UserID     string
	Callsign   string
	BannedBy   string // callsign of who banned them
	Reason     string
	BannedAt   int64
	ExpiresAt  int64 // Unix time; 0 means permanent
	FormerRank int   // the banned person's rank when banned
}

// Bans lists a domain's active bans, soonest to expire first and permanent
// ones last. Only members with the ban permission may see them.
func (s *Service) Bans(ctx context.Context, domainID, actorID string) ([]Ban, error) {
	out := []Ban{}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		a, err := s.actor(ctx, tx, domainID, actorID)
		if err != nil {
			return err
		}
		if !a.IsOwner && !a.Perms.Has(perm.Ban) {
			return ErrForbidden
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT b.user_id, u.callsign, COALESCE(bu.callsign, ''), b.reason, b.banned_at, b.expires_at, b.target_rank
			   FROM bans b JOIN users u ON u.id = b.user_id LEFT JOIN users bu ON bu.id = b.banned_by
			  WHERE b.domain_id = ? AND (b.expires_at = 0 OR b.expires_at > ?)
			  ORDER BY b.expires_at = 0, b.expires_at, u.callsign`, domainID, s.now().Unix())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b Ban
			if err := rows.Scan(&b.UserID, &b.Callsign, &b.BannedBy, &b.Reason, &b.BannedAt, &b.ExpiresAt, &b.FormerRank); err != nil {
				return err
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	return out, err
}

// SweepExpiredBans deletes bans whose time is up, records each in its
// domain's audit log, and returns the domains that had one.
func (s *Service) SweepExpiredBans(ctx context.Context) ([]string, error) {
	var domainIDs []string
	err := s.tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT domain_id, user_id FROM bans WHERE expires_at > 0 AND expires_at <= ? ORDER BY domain_id`, s.now().Unix())
		if err != nil {
			return err
		}
		var expired [][2]string
		for rows.Next() {
			var k [2]string
			if err := rows.Scan(&k[0], &k[1]); err != nil {
				rows.Close()
				return err
			}
			expired = append(expired, k)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, k := range expired {
			if err := s.liftExpired(ctx, tx, k[0], k[1]); err != nil {
				return err
			}
			if len(domainIDs) == 0 || domainIDs[len(domainIDs)-1] != k[0] {
				domainIDs = append(domainIDs, k[0])
			}
		}
		return nil
	})
	return domainIDs, err
}

// activeBan reports whether userID is banned from domainID right now. A ban
// whose time is up is lifted (and audited) here instead.
func (s *Service) activeBan(ctx context.Context, tx *sql.Tx, domainID, userID string) (bool, error) {
	var expires int64
	err := tx.QueryRowContext(ctx,
		`SELECT expires_at FROM bans WHERE domain_id = ? AND user_id = ?`, domainID, userID).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if expires == 0 || s.now().Unix() < expires {
		return true, nil
	}
	return false, s.liftExpired(ctx, tx, domainID, userID)
}

// liftExpired removes a ban whose time is up. The audit entry has no actor.
func (s *Service) liftExpired(ctx context.Context, tx *sql.Tx, domainID, userID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM bans WHERE domain_id = ? AND user_id = ?`, domainID, userID); err != nil {
		return err
	}
	return s.audit(ctx, tx, domainID, "", "member.ban_expired", userID, "")
}

// SetMuted mutes or unmutes targetID in the domain.
func (s *Service) SetMuted(ctx context.Context, domainID, actorID, targetID string, muted bool) error {
	return s.moderate(ctx, domainID, actorID, targetID, perm.Mute, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE members SET muted = ? WHERE domain_id = ? AND user_id = ?`, muted, domainID, targetID); err != nil {
			return err
		}
		action := "member.unmute"
		if muted {
			action = "member.mute"
		}
		return s.audit(ctx, tx, domainID, actorID, action, targetID, "")
	})
}

// CreateChannel adds a text or voice channel.
func (s *Service) CreateChannel(ctx context.Context, domainID, actorID, name, kind string) (Channel, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 || (kind != "text" && kind != "voice") {
		return Channel{}, fmt.Errorf("%w: channel needs a 1-64 character name and kind text or voice", ErrInvalidInput)
	}
	c := Channel{ID: store.NewID(), DomainID: domainID, Name: name, Kind: kind}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		a, err := s.actor(ctx, tx, domainID, actorID)
		if err != nil {
			return err
		}
		if !a.IsOwner && !a.Perms.Has(perm.ManageChannels) {
			return ErrForbidden
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO channels (id, domain_id, name, kind, position, created_at)
			 VALUES (?, ?, ?, ?, (SELECT COALESCE(MAX(position), -1) + 1 FROM channels WHERE domain_id = ?), ?)`,
			c.ID, domainID, name, kind, domainID, s.now().Unix()); err != nil {
			return err
		}
		return s.audit(ctx, tx, domainID, actorID, "channel.create", c.ID, name)
	})
	return c, err
}

// Roles lists a domain's roles, highest rank first.
func (s *Service) Roles(ctx context.Context, domainID string) ([]Role, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT id, domain_id, name, rank, permissions FROM roles WHERE domain_id = ? ORDER BY rank DESC`, domainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var r Role
		var p int64
		if err := rows.Scan(&r.ID, &r.DomainID, &r.Name, &r.Rank, &p); err != nil {
			return nil, err
		}
		r.Permissions = perm.Permission(p)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Members lists a domain's members, highest rank first.
func (s *Service) Members(ctx context.Context, domainID string) ([]Member, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT u.id, u.callsign, r.id, r.domain_id, r.name, r.rank, r.permissions, m.muted
		   FROM members m JOIN users u ON u.id = m.user_id JOIN roles r ON r.id = m.role_id
		  WHERE m.domain_id = ? ORDER BY r.rank DESC, u.callsign`, domainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		var p int64
		if err := rows.Scan(&m.UserID, &m.Callsign, &m.Role.ID, &m.Role.DomainID, &m.Role.Name,
			&m.Role.Rank, &p, &m.Muted); err != nil {
			return nil, err
		}
		m.Role.Permissions = perm.Permission(p)
		out = append(out, m)
	}
	return out, rows.Err()
}

// moderate runs fn after checking the actor may apply want to the target.
func (s *Service) moderate(ctx context.Context, domainID, actorID, targetID string, want perm.Permission, fn func(*sql.Tx) error) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		a, err := s.actor(ctx, tx, domainID, actorID)
		if err != nil {
			return err
		}
		t, err := s.actor(ctx, tx, domainID, targetID)
		if err != nil {
			return err
		}
		if !perm.CanActOn(a, want, t.Rank, t.IsOwner) {
			return ErrForbidden
		}
		return fn(tx)
	})
}

func (s *Service) actor(ctx context.Context, tx *sql.Tx, domainID, userID string) (perm.Actor, error) {
	var a perm.Actor
	var p int64
	var ownerID string
	err := tx.QueryRowContext(ctx,
		`SELECT r.rank, r.permissions, d.owner_id
		   FROM members m JOIN roles r ON r.id = m.role_id JOIN domains d ON d.id = m.domain_id
		  WHERE m.domain_id = ? AND m.user_id = ?`, domainID, userID).Scan(&a.Rank, &p, &ownerID)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotMember
	}
	if err != nil {
		return a, err
	}
	a.Perms = perm.Permission(p)
	a.IsOwner = ownerID == userID
	return a, nil
}

func (s *Service) audit(ctx context.Context, tx *sql.Tx, domainID, actorID, action, target, detail string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO audit_log (domain_id, actor_id, action, target, detail, at) VALUES (?, ?, ?, ?, ?, ?)`,
		domainID, actorID, action, target, detail, s.now().Unix())
	return err
}

func (s *Service) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// CheckInvite reports whether token is a usable invite without consuming it.
func (s *Service) CheckInvite(ctx context.Context, token string) error {
	var maxUses, uses int
	var expires int64
	err := s.st.DB.QueryRowContext(ctx,
		`SELECT max_uses, uses, expires_at FROM invites WHERE token = ?`, token).Scan(&maxUses, &uses, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidInvite
	}
	if err != nil {
		return err
	}
	if (maxUses > 0 && uses >= maxUses) || (expires > 0 && s.now().Unix() >= expires) {
		return ErrInvalidInvite
	}
	return nil
}

// DomainsForUser lists the domains userID belongs to.
func (s *Service) DomainsForUser(ctx context.Context, userID string) ([]Domain, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT d.id, d.name, d.owner_id FROM domains d JOIN members m ON m.domain_id = d.id
		  WHERE m.user_id = ? ORDER BY d.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		var d Domain
		if err := rows.Scan(&d.ID, &d.Name, &d.OwnerID); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Channels lists a domain's channels in display order.
func (s *Service) Channels(ctx context.Context, domainID string) ([]Channel, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT id, domain_id, name, kind FROM channels WHERE domain_id = ? ORDER BY position`, domainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ID, &c.DomainID, &c.Name, &c.Kind); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RequireMember returns ErrNotMember unless userID belongs to domainID.
func (s *Service) RequireMember(ctx context.Context, domainID, userID string) error {
	var n int
	if err := s.st.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM members WHERE domain_id = ? AND user_id = ?`, domainID, userID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotMember
	}
	return nil
}
