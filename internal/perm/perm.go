// Package perm defines domain permissions and the rank rules that decide
// whether one member may act on another.
package perm

// Permission is a bit set of actions a role allows inside a domain.
type Permission uint64

const (
	ManageDomain Permission = 1 << iota
	ManageChannels
	ManageRoles
	CreateInvite
	Kick
	Ban
	Mute
	DeleteMessages
	PinMessages
	SendMessages
	JoinVoice
)

// All is every permission; only the owner role holds it by default.
const All Permission = ManageDomain | ManageChannels | ManageRoles | CreateInvite |
	Kick | Ban | Mute | DeleteMessages | PinMessages | SendMessages | JoinVoice

// Has reports whether p includes every bit in want.
func (p Permission) Has(want Permission) bool { return p&want == want }

// Rank values for the default roles. Higher rank outranks lower rank.
const (
	RankOwner     = 1000
	RankSeniorMod = 500
	RankModerator = 300
	RankMember    = 100
	RankNewcomer  = 10
)

// Actor is the acting member's effective authority.
type Actor struct {
	Rank    int
	Perms   Permission
	IsOwner bool
}

// CanActOn reports whether the actor holds want and outranks the target.
// The owner can act on anyone except another owner; nobody can act on the owner.
func CanActOn(a Actor, want Permission, targetRank int, targetIsOwner bool) bool {
	if targetIsOwner {
		return false
	}
	if a.IsOwner {
		return true
	}
	return a.Perms.Has(want) && a.Rank > targetRank
}

// CanGrantRank reports whether the actor may assign a role of the given rank:
// non-owners may only hand out roles strictly below their own.
func CanGrantRank(a Actor, roleRank int) bool {
	if a.IsOwner {
		return roleRank < RankOwner
	}
	return a.Perms.Has(ManageRoles) && roleRank < a.Rank
}

// CanLiftBan reports whether the actor may lift a ban early. The actor needs
// the ban permission and must outrank the banned person's rank at the time
// of the ban; the owner may lift any ban.
func CanLiftBan(a Actor, bannedRank int) bool {
	if a.IsOwner {
		return true
	}
	return a.Perms.Has(Ban) && a.Rank > bannedRank
}
