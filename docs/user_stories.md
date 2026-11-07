# User Stories — `community_service`

Derived from the platform system design (`SYSTEM_DESIGN.md` §2.2). `community_service` owns communities, channels, invites, memberships, roles & permissions, is the authoritative source of member state (active/removed, banned, muted, roles), and is the authorization oracle for peer services.

Roles used below: **member** (any signed-in user of the platform), **owner** (creator of a community, implicitly holds all permissions), **moderator** (a member holding roles that grant any of the moderation-related permission keys), **peer service** (`moderation_service`, `voice_service` — internal callers).

Permission keys (closed set): `manage_community`, `manage_channels`, `manage_roles`, `create_invites`, `mute_members`, `kick_members`, `ban_members`, `manage_automod`, `view_audit_log`, `review_appeals`.

---

## US-001: Create a community

**As a** member,
**I want to** create a community with a name and description,
**So that** I have my own space to invite people into.

### Acceptance Criteria
- Given a signed-in user, when they submit a valid name (1–100 chars) and optional description, then a community is created with them as owner, a default `general` text channel and a default `General` voice channel (capacity 10) exist, and the response contains the community, its channels and the caller's effective permissions (all keys).
- Given a request with an empty name or a name longer than 100 characters, when submitted, then the request is rejected with `422 validation_failed` and nothing is created.
- Given an unauthenticated request, when submitted, then it is rejected with `401`.

### Out of Scope
- Community owner transfer (explicitly out of scope at launch).
- Public discovery of communities.

---

## US-002: See the communities I belong to

**As a** member,
**I want to** list the communities I currently belong to,
**So that** my home screen can show my communities or a "join or create" empty state.

### Acceptance Criteria
- Given a signed-in user with memberships, when they request their communities, then only communities where their membership status is `active` are returned, paginated with `cursor`/`limit`.
- Given a signed-in user with no memberships, when they request their communities, then an empty list is returned (not an error).
- Given a user whose membership was removed (kicked/banned), when they list their communities, then that community does not appear.

### Out of Scope
- Suggestions of communities to join.

---

## US-003: Load a community with my permissions

**As a** member,
**I want to** load a community with its channels and my roles and effective permissions,
**So that** the client can render the community and hide actions I cannot perform.

### Acceptance Criteria
- Given an active member, when they fetch a community, then the response contains the community, its channels ordered by position, their role ids and their effective permission set (owner ⇒ all ten keys).
- Given a user who is not an active member, when they fetch a community by id, then `403 not_a_member` is returned.
- Given a community id that does not exist, when fetched, then `404 community_not_found` is returned.

### Out of Scope
- Fetching a community without authentication.

---

## US-004: Configure a community

**As an** owner (or a member with `manage_community`),
**I want to** update the community name, description and icon URL,
**So that** the community stays accurate as it evolves.

### Acceptance Criteria
- Given a member holding `manage_community`, when they PATCH name/description/icon_url, then the updated community is returned.
- Given a member without `manage_community`, when they PATCH, then `403 missing_permission` is returned and nothing changes.
- Given an invalid `icon_url` (not an http(s) URL), when submitted, then `422 validation_failed` is returned.

### Out of Scope
- Uploading icons (an icon is stored as a URL reference).

---

## US-005: Add a channel

**As a** member with `manage_channels`,
**I want to** create a text or voice channel,
**So that** the community has organized spaces to talk.

### Acceptance Criteria
- Given a member holding `manage_channels`, when they create a channel with a name and kind `text`, then the channel is created with the next available position and appears in the community channel list.
- Given a voice channel creation with a capacity between 1 and 10, when submitted, then the channel stores that capacity; when no capacity is given, the default is 10.
- Given a channel name that duplicates another channel in the same community, when submitted, then `409 channel_name_conflict` is returned.
- Given a voice channel creation with capacity 0 or above 10, when submitted, then `422 validation_failed` is returned.
- Given a member without `manage_channels`, when they create a channel, then `403 missing_permission` is returned.

### Out of Scope
- Channel categories/sections.

---

## US-006: Rename, reorder, or change a channel's capacity

**As a** member with `manage_channels`,
**I want to** edit a channel's name, position, and (for voice) capacity,
**So that** channels stay tidy and voice rooms match our needs.

### Acceptance Criteria
- Given a member holding `manage_channels`, when they PATCH a channel's name and/or position, then the channel is updated and returned; positions may be swapped freely.
- Given a voice channel PATCH that sets capacity to a value between 1 and 10, when submitted, then the new capacity is stored and future join decisions use it.
- Given a PATCH that sets a capacity on a text channel, when submitted, then `422 validation_failed` is returned.
- Given a channel id that does not exist in the community, when PATCHed, then `404 channel_not_found` is returned.

### Out of Scope
- Moving channels between communities.

---

## US-007: Delete a channel

**As a** member with `manage_channels`,
**I want to** delete a channel,
**So that** unused spaces disappear.

### Acceptance Criteria
- Given a member holding `manage_channels`, when they DELETE a channel, then `204` is returned and the channel no longer appears in the community.
- Given a deletion of the last remaining channel of the community, when submitted, then `409 last_channel` is returned (a community must keep at least one channel).
- Given a channel id that does not exist, when DELETEd, then `404 channel_not_found` is returned.

### Out of Scope
- Restoring deleted channels; migration of channel message history (owned by `messaging_service`).

---

## US-008: Create an invite

**As a** member with `create_invites`,
**I want to** create an invite code with an optional expiry and maximum uses,
**So that** I can bring new people into the community under controlled conditions.

### Acceptance Criteria
- Given a member holding `create_invites`, when they create an invite with optional `expires_in_seconds` and `max_uses`, then an invite with a unique code is returned.
- Given an invite created with `max_uses = 5`, when the invite has already been accepted 5 times, then further accept attempts fail with `410 invite_expired`.
- Given an invite created with `expires_in_seconds = 3600`, when it is previewed or accepted after expiry, then it reports as expired.

### Out of Scope
- Per-invite role grants ("invite grants the X role").

---

## US-009: Revoke an invite

**As a** member with `create_invites`,
**I want to** revoke an invite code,
**So that** a leaked or no-longer-wanted link stops working.

### Acceptance Criteria
- Given a member holding `create_invites`, when they DELETE an invite code of their community, then `204` is returned and subsequent previews/accepts fail.
- Given an already-revoked invite, when DELETEd again, then `204` is returned (idempotent).

### Out of Scope
- Bulk invite revocation.

---

## US-010: Preview an invite

**As a** member (including a logged-out visitor, if the platform allows),
**I want to** preview what community an invite code leads to and whether it is still valid,
**So that** I can decide whether to join.

### Acceptance Criteria
- Given a valid, unexpired, non-revoked invite with uses remaining, when its code is fetched, then the response shows the community name, description and icon and `valid: true`.
- Given an expired, exhausted, or revoked invite, when its code is fetched, then the response shows `valid: false` with the reason (`invite_expired`, `invite_exhausted`, `invite_revoked`).
- Given an unknown code, when fetched, then `404 invite_not_found` is returned.

### Out of Scope
- Listing who created an invite to non-members.

---

## US-011: Accept an invite and join a community

**As a** member,
**I want to** accept an invite code and join the community,
**So that** I can chat and call with its members.

### Acceptance Criteria
- Given a valid invite and a user who is not banned, when they accept, then an `active` membership is created, the invite use count increments, and the response contains the membership.
- Given a user on the community ban list, when they accept any invite of that community, then `403 banned` is returned and no membership is created.
- Given an expired, exhausted, or revoked invite, when accepted, then `410 invite_expired` is returned.
- Given an unknown code, when accepted, then `404 invite_not_found` is returned.
- Given a user who already has an `active` membership, when they accept again, then the existing membership is returned (idempotent) and the invite use count does not increment twice for the same user.

### Out of Scope
- Joining without an invite (no public discovery at launch).

---

## US-012: Leave a community

**As a** member,
**I want to** leave a community,
**So that** I stop receiving its content and no longer appear in its member list.

### Acceptance Criteria
- Given an active member who is not the owner, when they POST leave, then `204` is returned and their membership becomes `removed`.
- Given the owner, when they attempt to leave, then `409 owner_cannot_leave` is returned (owner transfer is out of scope, so the owner cannot leave).
- Given a user who is not an active member, when they POST leave, then `404 membership_not_found` is returned.

### Out of Scope
- Owner transfer; deleting a community.

---

## US-013: List community members

**As a** member,
**I want to** list the members of my community with their role ids and mute status,
**So that** I can see who is here, who is muted, and find people to befriend.

### Acceptance Criteria
- Given an active member, when they request the member list, then active members are returned with `user_id`, `role_ids`, `muted_until` and `joined_at`, paginated with `cursor`/`limit`.
- Given a member who was removed or banned, when the list is fetched, then they do not appear.
- Given a user who is not an active member of that community, when they request the member list, then `403 not_a_member` is returned.

### Out of Scope
- Member search by name (clients filter locally; display names are resolved via `identity_service`).

---

## US-014: Create, edit, and delete roles

**As an** owner (or a member with `manage_roles`),
**I want to** define roles with a permission set and an ordered position,
**So that** moderators have exactly the powers the community intends.

### Acceptance Criteria
- Given a member holding `manage_roles`, when they create a role with a name, a subset of the ten permission keys and a position, then the role is created and returned.
- Given a role update changing permissions or position, when PATCHed, then the role reflects the change.
- Given a role deletion, when DELETEd, then `204` is returned and any member assignments of that role are removed.
- Given a member without `manage_roles`, when they create/edit/delete a role, then `403 missing_permission` is returned.
- Given a role creation with a permission key outside the closed set, when submitted, then `422 validation_failed` is returned.

### Out of Scope
- A built-in "admin" role object (the owner's powers are implicit, not role-based).

---

## US-015: Assign roles to a member

**As an** owner (or a member with `manage_roles`),
**I want to** set the complete role set of a member,
**So that** I can promote and demote moderators.

### Acceptance Criteria
- Given a member holding `manage_roles`, when they PUT the role ids of a target member, then the member's roles are replaced and the response contains the member's new role ids.
- Given an actor whose highest role does not outrank the target's highest role, when they attempt to assign roles, then `403 target_outranks_actor` is returned and nothing changes.
- Given an actor targeting themselves, when they attempt the assignment, then `403 target_is_self` is returned (self-promotion guard).
- Given the owner as target, when a non-owner attempts the assignment, then `403 target_outranks_actor` is returned (the owner outranks everyone).
- Given a role id that does not belong to the community, when submitted, then `422 validation_failed` is returned.

### Out of Scope
- Granting/revoking the ownership itself.

---

## US-016: Authorize a moderator action (authorization oracle, API-001)

**As the** `moderation_service` (peer service),
**I want to** check, before any moderator action, that the actor holds the required permission and outranks the target,
**So that** sanctions are never issued by members without the power to issue them.

### Acceptance Criteria
- Given an actor who is an active member holding the requested permission and (when a target is given) outranking the target, when a check is posted, then the response is `200` with `allowed: true`, `actor_is_member`, `actor_is_owner`, `actor_permissions` and the target's member/ban/mute snapshot.
- Given an actor missing the permission, or not a member, or not outranking the target, when a check is posted, then the response is `200` with `allowed: false` and the matching `denial_reason` (`actor_not_member`, `missing_permission`, `target_outranks_actor`, `target_is_owner`, `target_is_self`).
- Given a target who is banned and no longer a member, when a check is posted, then the target is still reported (`is_member: false`, `is_banned: true`) so unbans and appeal reviews work.
- Given a request without a valid internal service token, when posted, then `401` is returned; given an unknown community id, then `404 community_not_found` is returned; given an unknown permission key, then `400` is returned.

### Out of Scope
- Rate limiting or audit logging of internal checks (moderation owns the audit log).

---

## US-017: Authorize a voice channel join (voice access oracle, API-002)

**As the** `voice_service` (peer service),
**I want to** check, at join time, that a user is an active member and the channel is a voice channel, and receive the mute state and the community's voice channels,
**So that** only members drop into voice, muted members join listen-only, and full calls can suggest alternatives.

### Acceptance Criteria
- Given an active, non-banned member and an existing voice channel, when a check is posted, then the response is `200` with `allowed: true`, the channel summary (id, name, capacity), `muted_until` (null when not muted) and all the community's voice channels.
- Given a muted member, when a check is posted, then `allowed` remains `true` with a future `muted_until` (mute restricts publishing, not joining).
- Given a user who is not a member or is banned, then `allowed: false` with `denial_reason` `not_a_member` or `banned`; given a text channel id, then `allowed: false` with `not_a_voice_channel`; given an unknown channel, then `allowed: false` with `channel_not_found`.
- Given a request without a valid internal service token, then `401`; given an unknown community id, then `404 community_not_found`.

### Out of Scope
- Seat reservation and LiveKit token minting (owned by `voice_service`).

---

## US-018: Apply moderation sanctions to member state (EVT-004 consumer)

**As a** moderator relying on the platform,
**I want to** kick, ban, mute, unmute, and unban decisions made in `moderation_service` to take effect on membership automatically,
**So that** member state stays consistent across all services from one source of truth.

### Acceptance Criteria
- Given a `member_sanction_changed` event with action `kick`, when processed, then the target's membership becomes `removed` and a `community_member_changed` event with change `removed` is emitted carrying the `sanction_id`.
- Given action `ban`, when processed, then the membership (if any) becomes `removed`, the ban is recorded on the community ban list, and a `community_member_changed` event with change `banned` is emitted.
- Given action `unban` for a banned user, when processed, then the ban is cleared and a `community_member_changed` event with change `unbanned` is emitted (the user may rejoin afterwards).
- Given actions `mute`/`unmute`, when processed, then the member's `muted_until` is set/cleared and matching `muted`/`unmuted` change events are emitted.
- Given a duplicate event (same `event_id`) or a stale member state (older version than currently stored), when processed, then the event is deduplicated/ignored and no second change event is emitted.
- Given a sanction event for a user who was never a member (ban of an outsider), when processed, then only the ban record is created — no membership row is invented.

### Out of Scope
- Issuing sanctions (owned by `moderation_service`); sending moderation notices (owned by `notification_service`).

---

## US-019: Health and readiness

**As a** platform operator,
**I want to** probe liveness and readiness,
**So that** traffic is routed only to healthy, dependency-ready instances.

### Acceptance Criteria
- Given a running instance, when `GET /healthz` is called, then `200` is returned as long as the process is responsive (independent of PostgreSQL/Kafka health).
- Given PostgreSQL or Kafka unreachable, when `GET /readyz` is called, then `503` is returned; given all dependencies reachable, then `200` is returned.
- Given a `SIGTERM` during shutdown, when `/readyz` is called, then it returns `503` while in-flight requests drain.

### Out of Scope
- Deep dependency diagnostics in probe responses.
