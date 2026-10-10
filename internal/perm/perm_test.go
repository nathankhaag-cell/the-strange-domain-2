package perm

import "testing"

func TestCanActOn(t *testing.T) {
	owner := Actor{Rank: RankOwner, Perms: All, IsOwner: true}
	mod := Actor{Rank: RankModerator, Perms: Kick | Mute}
	cases := []struct {
		name        string
		a           Actor
		want        Permission
		targetRank  int
		targetOwner bool
		ok          bool
	}{
		{"owner kicks mod", owner, Kick, RankModerator, false, true},
		{"nobody acts on owner", owner, Kick, RankOwner, true, false},
		{"mod kicks member", mod, Kick, RankMember, false, true},
		{"mod cannot kick equal rank", mod, Kick, RankModerator, false, false},
		{"mod lacks ban", mod, Ban, RankMember, false, false},
	}
	for _, c := range cases {
		if got := CanActOn(c.a, c.want, c.targetRank, c.targetOwner); got != c.ok {
			t.Errorf("%s: got %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestCanLiftBan(t *testing.T) {
	owner := Actor{Rank: RankOwner, Perms: All, IsOwner: true}
	senior := Actor{Rank: RankSeniorMod, Perms: Ban}
	mod := Actor{Rank: RankModerator, Perms: Ban}
	member := Actor{Rank: RankMember}
	cases := []struct {
		name   string
		a      Actor
		banned int
		ok     bool
	}{
		{"owner lifts anything", owner, RankSeniorMod, true},
		{"senior lifts moderator's ban", senior, RankModerator, true},
		{"mod lifts member's ban", mod, RankMember, true},
		{"mod cannot lift equal rank", mod, RankModerator, false},
		{"mod cannot lift higher rank", mod, RankSeniorMod, false},
		{"member cannot lift", member, RankNewcomer, false},
	}
	for _, c := range cases {
		if got := CanLiftBan(c.a, c.banned); got != c.ok {
			t.Errorf("%s: got %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestCanGrantRank(t *testing.T) {
	owner := Actor{Rank: RankOwner, Perms: All, IsOwner: true}
	senior := Actor{Rank: RankSeniorMod, Perms: ManageRoles}
	if CanGrantRank(owner, RankOwner) {
		t.Error("owner role must not be grantable")
	}
	if !CanGrantRank(senior, RankModerator) || CanGrantRank(senior, RankSeniorMod) {
		t.Error("senior mod may grant only below its own rank")
	}
}
