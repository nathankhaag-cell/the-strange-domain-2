package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nathankhaag-cell/the-strange-domain-2/internal/domains"
	"github.com/nathankhaag-cell/the-strange-domain-2/internal/perm"
)

// CallAccess is what a user may do in a call.
type CallAccess struct {
	Relay    bool     // a domain Voice Relay; false: a Conclave call
	CanSpeak bool     // false: may listen only
	Audience []string // users told when the call changes
}

// CallAccess decides whether userID, on deviceID, may be in the call for
// groupID. Calls happen in Voice Relays (domain members whose role has
// JoinVoice; members muted in the domain may listen but not speak) and in
// Conclaves (any member). Chapels have no calls. The device must still
// belong to the user (revoked devices are dropped from calls).
func (s *Service) CallAccess(ctx context.Context, groupID, userID, deviceID string) (CallAccess, error) {
	var n int
	if err := s.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices WHERE id = ? AND user_id = ?`, deviceID, userID).Scan(&n); err != nil {
		return CallAccess{}, err
	}
	if n == 0 {
		return CallAccess{}, domains.ErrForbidden
	}
	var domainID, kind string
	err := s.st.DB.QueryRowContext(ctx, `SELECT domain_id, kind FROM channels WHERE id = ?`, groupID).Scan(&domainID, &kind)
	if err == nil {
		if kind != "voice" {
			return CallAccess{}, fmt.Errorf("%w: calls are only in Voice Relays", domains.ErrInvalidInput)
		}
		var p int64
		var muted bool
		var ownerID string
		err := s.st.DB.QueryRowContext(ctx,
			`SELECT r.permissions, m.muted, d.owner_id FROM members m
			   JOIN roles r ON r.id = m.role_id JOIN domains d ON d.id = m.domain_id
			  WHERE m.domain_id = ? AND m.user_id = ?`, domainID, userID).Scan(&p, &muted, &ownerID)
		if errors.Is(err, sql.ErrNoRows) {
			return CallAccess{}, domains.ErrNotMember
		}
		if err != nil {
			return CallAccess{}, err
		}
		owner := ownerID == userID
		if !owner && !perm.Permission(p).Has(perm.JoinVoice) {
			return CallAccess{}, fmt.Errorf("%w: your role cannot join Voice Relays", domains.ErrForbidden)
		}
		audience, err := s.userList(ctx, `SELECT user_id FROM members WHERE domain_id = ?`, domainID)
		return CallAccess{Relay: true, CanSpeak: owner || !muted, Audience: audience}, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CallAccess{}, err
	}
	audience, err := s.access(ctx, groupID, userID, false) // Conclave membership
	if err != nil {
		return CallAccess{}, err
	}
	return CallAccess{CanSpeak: true, Audience: audience}, nil
}
