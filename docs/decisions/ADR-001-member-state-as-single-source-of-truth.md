# ADR-001: Member state as the single source of truth, applied event-driven from moderation

## Status
Accepted

## Date
2026-02-05

## Context
Several services need to know a member's state — active/removed, banned, muted, roles:
- `messaging_service` must reject posts from removed/banned members and from currently muted members, and exempt roles from automod.
- `voice_service` must remove kicked/banned members from live rooms and revoke publishing for muted members.
- `realtime_gateway` must add/drop community scopes on join/leave/kick/ban.

Sanctions are issued by `moderation_service`, which also owns its own sanction lifecycle (expiry sweeps, appeals). If each consumer maintained its own membership state, the copies would drift, and moderation actions would need synchronous fan-out to every consumer.

## Decision
`community_service` is the **authoritative owner of member state**. It consumes `member_sanction_changed` (EVT-004) from `moderation_events` and applies the sanction to its own membership/ban/mute records, then emits `community_member_changed` (EVT-002) — the only membership-state signal the rest of the platform consumes. Peers build their own local projections from EVT-002 (with `member_version`-guarded, idempotent application). Every EVT-002 emitted because of an EVT-004 carries the `sanction_id` and copies the causing event's `trace_id`/`correlation_id`, with `causation_id` set to the EVT-004 `event_id`.

## Alternatives Considered
- **Peer services call `community_service` synchronously on every message/voice action** — rejected: puts this service on the hottest platform paths and couples availability.
- **Peers consume EVT-004 directly from `moderation_events`** — rejected: would duplicate sanction→state semantics (ban of a non-member, rejoin-after-unban) in three services and drift.
- **Shared database / read model** — rejected: violates service boundaries and couples schemas.

## Consequences
- One place implements membership semantics (ban list, mute, roles); peers stay simple version-guarded projections.
- Member state propagates asynchronously: messaging/voice react within the end-to-end SLO (sanction applied within 2 s, `kafka_consumer_lag` p95 < 1 s), not instantly.
- At-least-once delivery requires dedupe on `envelope.event_id` and stale-version rejection — implemented via the `processed_events` table and `member_version` bumps.
- This service's consumer of `moderation_events` is on the critical moderation path; consumer lag and DLQ handling are first-class observability concerns.
