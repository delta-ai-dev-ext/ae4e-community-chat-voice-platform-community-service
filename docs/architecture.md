# Architecture — `community_service`

Clean architectural specification for the communities/channels/invites/memberships/roles & permissions service of the Community Chat & Voice Platform. Runtime is **Go**. This document is the source of truth for the Diagram Agent and the Coding Agent; it deliberately contains no library or framework names (see `docs/engineering_guide.md` for the concrete stack).

## 1. Service Responsibilities & Boundaries

`community_service` owns:

1. **Communities** — creation (creator becomes owner; default `general` text + `General` voice channel are created transactionally with the community), configuration (name, description, icon URL). Owner transfer is out of scope at launch.
2. **Channels** — text and voice channels; create, rename, reorder (position), delete; voice channels carry a capacity of 1–10 (default 10).
3. **Invites** — invite codes with optional expiry and maximum uses; preview, accept, revoke.
4. **Memberships** — join (via invite only), leave, member listing; ban-list enforcement on join.
5. **Roles & permissions** — roles with permission sets and an ordered position; role assignment to members. The community owner implicitly holds all permissions.
6. **Authoritative member state** — per member: `active`/`removed`, `muted_until`, ban flag, role ids. This is the single source of truth the rest of the platform learns membership state from.
7. **Authorization oracle for peers** — permission + hierarchy checks (API-001, consumed by `moderation_service`) and voice access checks (API-002, consumed by `voice_service`).

Not owned: message content (messaging), sanctions and appeals (moderation), voice rooms and LiveKit (voice), user accounts and tokens (identity).

### Internal components

| Component | Responsibility |
|---|---|
| HTTP API layer | Public endpoints (JWT-verified locally against identity's public keys) and internal `/internal/*` endpoints (per-edge service token). Thin: bind, validate, delegate. |
| Authorization engine | Computes effective permission sets (role unions + owner implicit grant) and role-hierarchy comparisons (position-based ranking; owner ranks above all). |
| Domain services | Communities, channels, invites, memberships, roles — all business rules and invariants. |
| Event publisher | Builds the mandatory event envelope and publishes `community_member_changed` (EVT-002) and `community_channel_changed` (EVT-003) to `community_events`, partitioned by `community_id`. |
| Sanction event consumer | Consumes `moderation_events` (`member_sanction_changed`, EVT-004), applies member-state changes idempotently, and emits the matching EVT-002. |
| Repository layer | PostgreSQL access via parameterized queries. |

## 2. Request Flows (synchronous HTTP)

### 2.1 Community / channel / role management
1. Client calls with `Authorization: Bearer <access_token>`; the token is verified **locally** using identity's public verification key(s) from deployment configuration (claims used: `sub`, `sid`, `exp`, `iss`, `aud`).
2. The handler resolves the caller's membership and effective permissions in PostgreSQL.
3. Domain service applies the change in a single transaction; for channel and role changes the new `channel_version`/`member_version` is incremented.
4. After commit, an EVT-002/EVT-003 event is published (publish-after-commit; an outbox-style retry makes publication reliable).

### 2.2 Invite accept (join)
1. `POST /invites/{code}/accept` — invite validated in one transaction: not revoked, not expired, uses remaining, caller not on the community ban list.
2. Banned caller → `403 banned`. Invalid/expired/exhausted/revoked invite → `404 invite_not_found` / `410 invite_expired`. Already-active member → existing membership returned without incrementing use count.
3. Membership row created as `active`; invite `use_count` incremented atomically.
4. EVT-002 `joined` emitted, keyed by `community_id`.

### 2.3 Leave
`POST /communities/{id}/leave` — the owner cannot leave (`409 owner_cannot_leave`); otherwise membership → `removed` and EVT-002 `left` is emitted.

### 2.4 Authorization oracle — API-001 (`moderation_service`)
`POST /internal/communities/{community_id}/authorization-checks`
1. Service-token auth (`Authorization: Bearer`, deployment secret); `401` without a valid token.
2. Load actor membership + roles; compute effective permissions (owner ⇒ all ten keys).
3. If a `target_user_id` is present: load the target's membership (even `removed`/banned) and compare hierarchy. Denial reasons: `actor_not_member`, `missing_permission`, `target_outranks_actor`, `target_is_owner`, `target_is_self`.
4. Response is `200` with `allowed: true/false` + `denial_reason` — a denial is **not** an error. `404 community_not_found`, `400` malformed/unknown permission.
5. Read-only, so callers retry safely; must answer p95 < 30 ms.

### 2.5 Voice access oracle — API-002 (`voice_service`)
`POST /internal/communities/{community_id}/channels/{channel_id}/voice-access-checks`
1. Service-token auth; loads membership (ban check: banned → `allowed: false, denial_reason: banned`), the channel (missing → `channel_not_found`, text → `not_a_voice_channel`).
2. Mute does **not** deny access — a muted member is `allowed: true` with future `muted_until` (listen-only join).
3. Returns the requested channel summary and all of the community's voice channels so a full call can suggest alternatives.
4. Read-only, safe to retry; must answer p95 < 30 ms.

## 3. Event Flows (Kafka)

Topic produced/consumed: `community_events` (produce), `moderation_events` (consume). Kafka topics are partitioned by `community_id`, preserving per-community ordering. Delivery is at-least-once: every consumed event is deduplicated on `envelope.event_id` via the processed-event table, and every projection/state change is idempotent (version-guarded, last-writer-wins on `member_version`/`channel_version`).

### 3.1 Producer — EVT-002 `community_member_changed` v1
Emitted on every membership state change: `joined`, `left`, `removed` (kick), `banned`, `unbanned`, `muted`, `unmuted`, `roles_changed`. The payload always carries the full member snapshot (`status`, `is_banned`, `muted_until`, `role_ids`, `member_version`). Sanction-driven changes carry the originating `sanction_id`. Consumers: `messaging_service`, `voice_service`, `realtime_gateway`.

### 3.2 Producer — EVT-003 `community_channel_changed` v1
Emitted on channel `created` / `updated` (name, position, capacity) / `deleted`, including the two default channels created with a community. Consumers: `realtime_gateway`, `messaging_service`, `voice_service`.

### 3.3 Consumer — EVT-004 `member_sanction_changed` v1 (from `moderation_events`)
Applied transactionally; each applied change emits the matching EVT-002 with `causation_id` = the EVT-004 `event_id` and copied `trace_id`/`correlation_id`:

| EVT-004 action | Applied state change | EVT-002 emitted |
|---|---|---|
| `kick` | membership → `removed` | `removed` (with `sanction_id`) |
| `ban` | membership → `removed` (if any) **and** ban recorded (works for non-members too) | `banned` |
| `unban` | ban cleared | `unbanned` |
| `mute` | `muted_until` = `expires_at` | `muted` |
| `unmute` | `muted_until` = null | `unmuted` |

Idempotency: duplicate `event_id` → skipped; a stale `member_version` → ignored (no second EVT-002). A ban of a user who was never a member records only the ban — no membership row is invented.

### 3.4 Envelope
Every message on every topic is `{"envelope": {...}, "payload": {...}}` with the mandatory nine-field envelope (`event_id`, `event_type`, `event_version`, `occurred_at`, `producer`, `trace_id`, `correlation_id`, `causation_id`, `schema_ref`); `trace_id` is never empty — propagated from the originating request, or minted when the flow starts without one. When this service emits an event because of a consumed EVT-004, it copies `trace_id`/`correlation_id` from the causing event and sets `causation_id` to the causing `event_id`. The envelope is mirrored into Kafka headers (`trace_id`, `event_type`); the JSON envelope remains the source of truth.

## Inter-Service Contracts

This section is `community_service`'s slice of the project-level `API_CONTRACTS.md` and `EVENT_CONTRACTS.md`, copied verbatim.

### API-001 — Community authorization check (provider; consumer `moderation_service`, sync)

Used by `moderation_service` before every moderator action (sanction issue/lift, appeal review, automod configuration change, audit-log read). It confirms that the actor holds the required permission and, when the action has a target, that the actor outranks the target in the community role hierarchy. Banned users who are no longer members are still reported through `target.is_banned` so that unbans and appeal reviews work.

| Field | Value |
|---|---|
| Provider | `community_service` |
| Consumers | `moderation_service` |
| Kind | REST |
| Operation | `POST /internal/communities/{community_id}/authorization-checks` |
| Auth | service token, `Authorization: Bearer` |
| Idempotency | not required (read-only check, safe to retry) |

Path parameters: `community_id` (uuid).

**Request** (`application/json`)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["actor_user_id", "permission"],
  "properties": {
    "actor_user_id": {"type": "string", "format": "uuid"},
    "permission": {
      "type": "string",
      "enum": ["manage_community", "manage_channels", "manage_roles", "create_invites", "mute_members", "kick_members", "ban_members", "manage_automod", "view_audit_log", "review_appeals"]
    },
    "target_user_id": {
      "type": ["string", "null"],
      "format": "uuid",
      "description": "Member the action applies to; null or omitted for non-targeted actions (automod config, audit-log read)."
    }
  }
}
```

**Response 200** (`application/json`)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["community_id", "actor_user_id", "permission", "allowed", "denial_reason", "actor_is_member", "actor_is_owner", "actor_permissions", "target"],
  "properties": {
    "community_id": {"type": "string", "format": "uuid"},
    "actor_user_id": {"type": "string", "format": "uuid"},
    "permission": {
      "type": "string",
      "enum": ["manage_community", "manage_channels", "manage_roles", "create_invites", "mute_members", "kick_members", "ban_members", "manage_automod", "view_audit_log", "review_appeals"]
    },
    "allowed": {"type": "boolean", "description": "True only when the actor is an active member, holds the permission, and (if a target is given) outranks the target."},
    "denial_reason": {
      "type": ["string", "null"],
      "enum": ["actor_not_member", "missing_permission", "target_outranks_actor", "target_is_owner", "target_is_self", null]
    },
    "actor_is_member": {"type": "boolean"},
    "actor_is_owner": {"type": "boolean"},
    "actor_permissions": {
      "type": "array",
      "uniqueItems": true,
      "items": {
        "type": "string",
        "enum": ["manage_community", "manage_channels", "manage_roles", "create_invites", "mute_members", "kick_members", "ban_members", "manage_automod", "view_audit_log", "review_appeals"]
      }
    },
    "target": {
      "type": ["object", "null"],
      "description": "Null when the request had no target_user_id.",
      "required": ["user_id", "is_member", "is_banned", "is_owner", "outranked_by_actor", "muted_until"],
      "properties": {
        "user_id": {"type": "string", "format": "uuid"},
        "is_member": {"type": "boolean"},
        "is_banned": {"type": "boolean"},
        "is_owner": {"type": "boolean"},
        "outranked_by_actor": {"type": "boolean"},
        "muted_until": {"type": ["string", "null"], "format": "date-time"}
      }
    }
  }
}
```

**Errors** — `400` (malformed body / unknown permission), `401` (missing or invalid service token), `404` (`community_not_found`), `500`, `503`, all `application/problem+json` referencing `shared:ErrorResponse`. A denied check is **not** an error: it returns `200` with `allowed: false` and a `denial_reason`.

### API-002 — Voice channel access check (provider; consumer `voice_service`, sync)

Used by `voice_service` when a user asks to join a voice channel. It confirms active membership, that the channel exists and is a voice channel, and returns the channel capacity, the member's mute state (a muted member joins listen-only), and the community's voice channels so a full call can suggest an alternative.

| Field | Value |
|---|---|
| Provider | `community_service` |
| Consumers | `voice_service` |
| Kind | REST |
| Operation | `POST /internal/communities/{community_id}/channels/{channel_id}/voice-access-checks` |
| Auth | service token, `Authorization: Bearer` |
| Idempotency | not required (read-only check, safe to retry) |

Path parameters: `community_id` (uuid), `channel_id` (uuid).

**Request** (`application/json`)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["user_id"],
  "properties": {
    "user_id": {"type": "string", "format": "uuid"}
  }
}
```

**Response 200** (`application/json`)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["community_id", "user_id", "allowed", "denial_reason", "muted_until", "voice_channels"],
  "properties": {
    "community_id": {"type": "string", "format": "uuid"},
    "user_id": {"type": "string", "format": "uuid"},
    "allowed": {"type": "boolean", "description": "True when the user is an active, non-banned member and the channel is a voice channel. Mute does not deny access; it restricts publishing."},
    "denial_reason": {
      "type": ["string", "null"],
      "enum": ["not_a_member", "banned", "channel_not_found", "not_a_voice_channel", null]
    },
    "channel": {
      "$ref": "shared:VoiceChannelSummary",
      "description": "The requested voice channel; omitted when denial_reason is channel_not_found or not_a_voice_channel."
    },
    "muted_until": {
      "type": ["string", "null"],
      "format": "date-time",
      "description": "Non-null and in the future when the member is currently muted (listen-only)."
    },
    "voice_channels": {
      "type": "array",
      "description": "All voice channels of the community (including the requested one); empty when allowed is false.",
      "items": {"$ref": "shared:VoiceChannelSummary"}
    }
  }
}
```

The `shared:VoiceChannelSummary` schema referenced above:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["channel_id", "name", "capacity"],
  "properties": {
    "channel_id": {"type": "string", "format": "uuid"},
    "name": {"type": "string", "minLength": 1, "maxLength": 100},
    "capacity": {"type": "integer", "minimum": 1, "maximum": 10}
  }
}
```

**Errors** — `400` (malformed body), `401` (missing or invalid service token), `404` (`community_not_found`), `500`, `503`, all `application/problem+json` referencing `shared:ErrorResponse`. A denied access returns `200` with `allowed: false`.

### Shared schemas (`shared:ErrorResponse`, referenced by both internal contracts)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["error"],
  "properties": {
    "error": {"type": "string", "description": "Stable machine-readable error code, snake_case."},
    "detail": {"type": "string", "description": "Human-readable explanation."}
  }
}
```

### Event envelope (mandatory, every event produced or consumed)

Every message on every topic is a JSON object with exactly two top-level keys: `envelope` and `payload`.

| Field | Type | Required | Rule |
|---|---|---|---|
| `event_id` | string (uuid) | yes | Unique per event. Consumer deduplication key. |
| `event_type` | string (snake_case) | yes | Must match a registered contract. |
| `event_version` | integer | yes | >= 1. A breaking payload change bumps it. |
| `occurred_at` | string (RFC3339 UTC) | yes | Producer clock at emit time. |
| `producer` | string (snake_case) | yes | Must equal the registered producer (`community_service`). |
| `trace_id` | string (32 lowercase hex) | yes | **Never empty.** Propagated from the originating request; minted at the edge when absent. |
| `correlation_id` | string (uuid) | yes | Groups every event of one business flow. |
| `causation_id` | string (uuid) or null | yes | `event_id` of the causing event. `null` only for the flow's first event. |
| `schema_ref` | string | yes | `EVENT_CONTRACTS.md#<event_type>-v<version>`. |

Rules:
- The envelope is not optional and not per-topic. A topic needing a field the envelope lacks adds it to `payload`, never to the envelope.
- A payload schema MUST NOT declare an envelope field name at its top level.
- When a consumer emits an event because of another event (this service emits `community_member_changed` after applying `member_sanction_changed`), it copies `trace_id` and `correlation_id` from the causing event and sets `causation_id` to the causing `event_id`.
- The envelope is also mirrored into Kafka message headers (`trace_id`, `event_type`) for routing and observability. The JSON envelope remains the source of truth.
- Delivery is at-least-once. Consumers dedupe on `envelope.event_id` and apply changes idempotently. Projections keep an entity version and ignore stale versions.

### EVT-002 — `community_member_changed` v1 (produced; topic `community_events`, key `community_id`)

Emitted whenever a membership's state changes. The authoritative member state (active/removed, banned, mute, roles) is always included as a snapshot. Sanction-driven changes carry the originating `sanction_id`. Consumers: `messaging_service`, `voice_service`, `realtime_gateway`. Delivery: at-least-once; consumers dedupe on `envelope.event_id`; apply only if `member_version` is newer.

**Payload** (the `payload` object)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["community_id", "user_id", "change", "status", "is_banned", "muted_until", "role_ids", "sanction_id", "member_version"],
  "properties": {
    "community_id": {"type": "string", "format": "uuid"},
    "user_id": {"type": "string", "format": "uuid"},
    "change": {"type": "string", "enum": ["joined", "left", "removed", "banned", "unbanned", "muted", "unmuted", "roles_changed"]},
    "status": {"type": "string", "enum": ["active", "removed"]},
    "is_banned": {"type": "boolean"},
    "muted_until": {"type": ["string", "null"], "format": "date-time"},
    "role_ids": {"type": "array", "items": {"type": "string", "format": "uuid"}, "uniqueItems": true},
    "sanction_id": {"type": ["string", "null"], "format": "uuid"},
    "member_version": {"type": "integer", "minimum": 1}
  }
}
```

### EVT-003 — `community_channel_changed` v1 (produced; topic `community_events`, key `community_id`)

Emitted when a text or voice channel is created, updated (name, position, capacity), or deleted. Consumers: `realtime_gateway`, `messaging_service`, `voice_service`. Delivery: at-least-once; consumers dedupe on `envelope.event_id`; apply only if `channel_version` is newer.

**Payload** (the `payload` object)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["community_id", "channel_id", "change", "kind", "name", "position", "capacity", "channel_version"],
  "properties": {
    "community_id": {"type": "string", "format": "uuid"},
    "channel_id": {"type": "string", "format": "uuid"},
    "change": {"type": "string", "enum": ["created", "updated", "deleted"]},
    "kind": {"type": "string", "enum": ["text", "voice"]},
    "name": {"type": "string", "minLength": 1, "maxLength": 100},
    "position": {"type": "integer", "minimum": 0},
    "capacity": {"type": ["integer", "null"], "minimum": 1, "maximum": 10, "description": "Voice channels only; null for text channels."},
    "channel_version": {"type": "integer", "minimum": 1}
  }
}
```

### EVT-004 — `member_sanction_changed` v1 (consumed; topic `moderation_events`, key `community_id`, producer `moderation_service`)

Emitted for every sanction issued or lifted. Sources: a moderator action, automatic expiry of a timed mute, or an appeal that was overturned. `community_service` applies it to member state. `notification_service` sends the moderation notice to the affected member. Delivery: at-least-once; consumers dedupe on `envelope.event_id`.

**Payload** (the `payload` object)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["sanction_id", "community_id", "target_user_id", "actor_user_id", "action", "source", "reason", "expires_at", "appealable"],
  "properties": {
    "sanction_id": {"type": "string", "format": "uuid"},
    "community_id": {"type": "string", "format": "uuid"},
    "target_user_id": {"type": "string", "format": "uuid"},
    "actor_user_id": {"type": ["string", "null"], "format": "uuid", "description": "Null when source is expiry."},
    "action": {"type": "string", "enum": ["mute", "unmute", "kick", "ban", "unban"]},
    "source": {"type": "string", "enum": ["moderator", "expiry", "appeal"]},
    "reason": {"type": ["string", "null"], "maxLength": 1000},
    "expires_at": {"type": ["string", "null"], "format": "date-time", "description": "Mute end time; null for permanent bans, kicks, and lift actions."},
    "appealable": {"type": "boolean", "description": "True for active mute and ban sanctions the member can appeal."}
  }
}
```

## 5. PostgreSQL Database Schema

Schema is owned and defined here; migrations apply it. All timestamps are `timestamptz` (UTC). All identifiers are UUID v4 generated by the service.

### `communities`
| Column | Type | Constraints |
|---|---|---|
| `id` | uuid | PK |
| `name` | varchar(100) | NOT NULL |
| `description` | varchar(500) | NULL |
| `icon_url` | text | NULL (must be http(s) URL when set) |
| `owner_user_id` | uuid | NOT NULL |
| `created_at` / `updated_at` | timestamptz | NOT NULL |

### `channels`
| Column | Type | Constraints |
|---|---|---|
| `id` | uuid | PK |
| `community_id` | uuid | NOT NULL, FK → `communities(id)` ON DELETE CASCADE |
| `kind` | text | NOT NULL, CHECK `kind IN ('text','voice')` |
| `name` | varchar(100) | NOT NULL |
| `position` | integer | NOT NULL DEFAULT 0 |
| `capacity` | integer | NULL, CHECK `capacity BETWEEN 1 AND 10` (NULL for text channels) |
| `channel_version` | bigint | NOT NULL DEFAULT 1 (bumped on every change; monotonic per channel) |
| `created_at` / `updated_at` | timestamptz | NOT NULL |

Constraints: UNIQUE `(community_id, name)`. Index `(community_id, position)` for ordered listing.

### `invites`
| Column | Type | Constraints |
|---|---|---|
| `id` | uuid | PK |
| `community_id` | uuid | NOT NULL, FK → `communities(id)` ON DELETE CASCADE |
| `code` | varchar(32) | NOT NULL, UNIQUE (cryptographically random) |
| `created_by` | uuid | NOT NULL |
| `expires_at` | timestamptz | NULL (never expires when NULL) |
| `max_uses` | integer | NULL CHECK (`max_uses >= 1`) (unlimited when NULL) |
| `use_count` | integer | NOT NULL DEFAULT 0 |
| `revoked_at` | timestamptz | NULL |
| `created_at` | timestamptz | NOT NULL |

Index `(community_id)`.

### `memberships`
| Column | Type | Constraints |
|---|---|---|
| `id` | uuid | PK |
| `community_id` | uuid | NOT NULL, FK → `communities(id)` ON DELETE CASCADE |
| `user_id` | uuid | NOT NULL |
| `status` | text | NOT NULL, CHECK `status IN ('active','removed')` |
| `joined_at` | timestamptz | NOT NULL |
| `left_at` | timestamptz | NULL (set when status becomes `removed`) |
| `muted_until` | timestamptz | NULL |
| `member_version` | bigint | NOT NULL DEFAULT 1 (bumped on every change; monotonic per member) |

Constraints: UNIQUE `(community_id, user_id)` (a rejoin reactivates the same row with a new `member_version`). Partial index on `(community_id) WHERE status = 'active'` for member listing and oracle checks; index `(user_id)` for "my communities".

### `community_bans`
| Column | Type | Constraints |
|---|---|---|
| `id` | uuid | PK |
| `community_id` | uuid | NOT NULL, FK → `communities(id)` ON DELETE CASCADE |
| `user_id` | uuid | NOT NULL |
| `reason` | varchar(1000) | NULL |
| `banned_at` | timestamptz | NOT NULL |
| `lifted_at` | timestamptz | NULL |

Constraints: partial UNIQUE index `(community_id, user_id) WHERE lifted_at IS NULL` — at most one active ban per user per community. Join flow checks this table.

### `roles`
| Column | Type | Constraints |
|---|---|---|
| `id` | uuid | PK |
| `community_id` | uuid | NOT NULL, FK → `communities(id)` ON DELETE CASCADE |
| `name` | varchar(100) | NOT NULL |
| `permissions` | text[] | NOT NULL DEFAULT '{}' (values restricted to the ten permission keys) |
| `position` | integer | NOT NULL (lower = higher rank) |
| `created_at` / `updated_at` | timestamptz | NOT NULL |

Constraints: UNIQUE `(community_id, name)`, UNIQUE `(community_id, position)`.

### `member_roles`
| Column | Type | Constraints |
|---|---|---|
| `community_id` | uuid | PK part, FK → `communities(id)` ON DELETE CASCADE |
| `user_id` | uuid | PK part |
| `role_id` | uuid | PK part, FK → `roles(id)` ON DELETE CASCADE |
| `assigned_at` | timestamptz | NOT NULL |

PK `(community_id, user_id, role_id)`.

### `processed_events`
| Column | Type | Constraints |
|---|---|---|
| `event_id` | uuid | PK (deduplication key from the envelope) |
| `event_type` | text | NOT NULL |
| `processed_at` | timestamptz | NOT NULL |

Insert-once per consumed event in the same transaction as the state change; a PK conflict means "already applied — skip".

### Data invariants enforced transactionally
- Community creation inserts `communities` + two `channels` rows in one transaction.
- Invite accept: lock invite row `FOR UPDATE`, check expiry/uses/ban, insert-or-reactivate membership, increment `use_count`.
- Role assignment (PUT): replace the target's `member_roles` rows and bump the member's `member_version` in one transaction; hierarchy check precedes it.
- EVT-004 application: dedupe insert + membership/ban/mute change + `member_version` bump in one transaction; the follow-up EVT-002 is published after commit.

## 6. Stateless Container Operational Model

- **Statelessness:** every request and consumer batch is served from PostgreSQL + Kafka; no local disk or in-memory session state. Safe to run with `replicas > 1`; the consumer runs as one shared consumer group so partitions are distributed, not duplicated.
- **Liveness `/healthz`:** returns `200` while the process is responsive; never fails on PostgreSQL/Kafka hiccups.
- **Readiness `/readyz`:** checks PostgreSQL connectivity and Kafka producer/consumer health; returns `503` once shutdown begins or a dependency is unreachable.
- **Graceful shutdown:** on `SIGTERM`/`SIGINT` — (1) readiness flips to failing, (2) the HTTP listener stops accepting and Kafka consumption pauses/commits offsets, (3) in-flight requests and the in-flight event batch drain within a bounded timeout, (4) the PostgreSQL pool and Kafka clients close, exit code `0`.
- **Failure modes & resilience:**
  - *EVT-004 consumer error* (poison payload or DB failure): the batch is retried with back-off; after the bounded retry budget the message goes to a dead-letter path with an error log and a metric — processing continues with later messages for other partitions only after the offset is handled, preserving per-community ordering.
  - *Publish-after-commit failure:* the EVT-002/EVT-003 publish is retried with exponential back-off from the outbox; events are never dropped after a committed state change (consumers are version-guarded, so republication is safe).
  - *Internal oracle overload:* both internal checks are single-query reads served from PostgreSQL with a small bounded timeout; callers apply a 2 s timeout with one retry — denial is expressed in-band (`allowed: false`), never as an error status, so `503` only ever means unavailability.
  - *Duplicate delivery:* at-least-once everywhere; dedupe on `envelope.event_id` and version-guarded application make redelivery a no-op.

## 7. Observability

Business metrics (counters unless noted; low-cardinality labels only):
- `community_created_total`
- `community_invites_created_total`
- `community_joins_total{result}` (`joined|banned|expired|invalid`)
- `community_leaves_total{reason}` (`left|kicked|banned`)
- `community_role_assignments_total`

Technical metrics:
- RED metrics: `http_requests_total{route,method,status_class}`, `http_request_duration_seconds{route,method}`.
- `kafka_consumer_lag{topic="moderation_events",consumer_group}` — SLO: p95 < 1 s, so a sanction is applied within 2 s end-to-end.
- Consumer error/`dead-letter` counters for EVT-004 processing failures.

SLOs: public API p95 < 150 ms; internal check endpoints p95 < 30 ms with availability ≥ 99.9 %.

Tracing: `traceparent` / `X-Trace-Id` is read from inbound HTTP calls, attached to every log line, continued on any outbound call, and written into the event envelope `trace_id` (copied from the causing event for sanction-driven EVT-002 emissions).
