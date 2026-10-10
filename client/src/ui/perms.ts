import { Perm, RANK_MEMBER, RANK_OWNER, type Role } from "../api";
import type { AppState } from "../app";

// ---- permission helpers (mirror internal/perm) ----

export interface Actor {
  rank: number;
  perms: number;
  isOwner: boolean;
}

export function actorFor(app: AppState, domainId: string, userId: string): Actor | undefined {
  const domain = app.domains.find((d) => d.id === domainId);
  const detail = app.details[domainId];
  const m = detail?.members.find((x) => x.user_id === userId);
  if (!domain || !detail || !m) return undefined;
  const role = detail.roles.find((r) => r.id === m.role_id);
  return { rank: role?.rank ?? 0, perms: role?.permissions ?? 0, isOwner: domain.owner_id === userId };
}

export const has = (a: Actor, p: number) => a.isOwner || (a.perms & p) === p;

export function canActOn(a: Actor, p: number, target: Actor): boolean {
  if (target.isOwner) return false;
  if (a.isOwner) return true;
  return (a.perms & p) === p && a.rank > target.rank;
}

/** Roles the actor may give the target (internal/perm CanGrantRank): strictly below the actor's own rank. */
export function grantableRoles(a: Actor, target: Actor, roles: Role[], currentRoleId: string): Role[] {
  if (!canActOn(a, Perm.ManageRoles, target)) return [];
  return roles.filter((r) => r.rank < (a.isOwner ? RANK_OWNER : a.rank) && r.id !== currentRoleId);
}

/** Whether the actor may lift a ban early (internal/perm CanLiftBan). */
export function canLiftBan(a: Actor, formerRank: number): boolean {
  if (a.isOwner) return true;
  return (a.perms & Perm.Ban) === Perm.Ban && a.rank > formerRank;
}

/** The badge shown for a member's role. Brother / Sister is each person's own choice. */
export function roleBadge(app: AppState, role: Role | undefined, userId: string): string {
  if (!role) return "";
  if (role.rank === RANK_MEMBER && userId === app.me?.user_id && app.honorific) return app.honorific.toUpperCase();
  return role.name.toUpperCase();
}

