# ADR-002: Position-based role hierarchy with implicit owner supremacy

## Status
Accepted

## Date
2026-02-05

## Context
The platform authorization model requires that a moderator can only act on members whose highest role ranks below their own, and that the community owner implicitly holds all ten permission keys. The permission keys are a closed set (`manage_community`, `manage_channels`, `manage_roles`, `create_invites`, `mute_members`, `kick_members`, `ban_members`, `manage_automod`, `view_audit_log`, `review_appeals`), and `moderation_service` relies on API-001 returning a reliable hierarchy verdict for every sanction.

## Decision
- Roles carry an integer `position`; **lower position = higher rank**. A member's rank is their highest (lowest-position) role.
- An actor may act on a target only when the actor's rank is strictly above the target's (or the actor is the owner). Violations surface as `target_outranks_actor`.
- The owner is not a role row: supremacy is implicit via `communities.owner_user_id`, granting all permissions and outranking every role. This avoids a special "owner" role that could be assigned, deleted, or ranked incorrectly.
- Self-targeting is denied (`target_is_self`) for role assignment, preventing self-promotion loops.
- Effective permissions = union of the member's role permissions, plus all keys when the caller is the owner.

## Alternatives Considered
- **Parent/child role DAG** — rejected: expressive but far harder to reason about and to serve deterministically in the p95 < 30 ms oracle path; a total order by position is sufficient for communities.
- **Owner as a built-in role row** — rejected: role CRUD would need special cases to protect it; implicit ownership keeps roles a pure configuration object.

## Consequences
- Hierarchy checks are a single comparison of two positions (plus an owner flag) — cheap and deterministic.
- Reordering roles (PATCH position) can silently change who can act on whom; role edits are therefore audited as configuration changes by `moderation_service` and emit `roles_changed` events for affected members.
- A community cannot express circular or partial rank relationships — accepted, since community hierarchies in scope are flat moderator/admin stacks.
