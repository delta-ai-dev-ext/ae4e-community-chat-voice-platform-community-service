// Package events owns the mandatory event envelope, the payload contracts this
// service produces and consumes, the Kafka producer and the EVT-004 consumer.
package events

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ProducerName is the registered producer of every event this service emits.
const ProducerName = "community_service"

// Event types produced and consumed by this service.
const (
	EventTypeCommunityMemberChanged  = "community_member_changed"
	EventTypeCommunityChannelChanged = "community_channel_changed"
	EventTypeMemberSanctionChanged   = "member_sanction_changed"
)

// Kafka topics.
const (
	// CommunityTopic carries the events this service produces.
	CommunityTopic = "community_events"
	// ModerationTopic carries the sanction events this service consumes.
	ModerationTopic = "moderation_events"
)

// EventVersionV1 is the only contract version in scope at launch.
const EventVersionV1 = 1

// Kafka headers mirroring the JSON envelope for routing and observability.
const (
	HeaderTraceID   = "trace_id"
	HeaderEventType = "event_type"
)

// envelopeFieldCount is the mandated number of envelope fields.
const envelopeFieldCount = 9

// Envelope is the single reusable message envelope of the platform contract.
// Every message on every topic carries all nine fields; payloads never repeat
// them.
type Envelope struct {
	EventID       string    `json:"event_id"`
	EventType     string    `json:"event_type"`
	EventVersion  int       `json:"event_version"`
	OccurredAt    time.Time `json:"occurred_at"`
	Producer      string    `json:"producer"`
	TraceID       string    `json:"trace_id"`
	CorrelationID string    `json:"correlation_id"`
	CausationID   *string   `json:"causation_id"`
	SchemaRef     string    `json:"schema_ref"`
}

// Message is the wire shape of every event: exactly two top-level keys.
type Message struct {
	Envelope Envelope `json:"envelope"`
	Payload  any      `json:"payload"`
}

// SchemaRef renders the schema reference of one event type and version.
func SchemaRef(eventType string, version int) string {
	return fmt.Sprintf("EVENT_CONTRACTS.md#%s-v%d", eventType, version)
}

// NewEnvelope builds the one envelope shape used everywhere. trace_id is never
// empty (it is minted when the flow has no originating request),
// correlation_id is minted when absent, and causation_id stays nil only for the
// first event of a flow.
func NewEnvelope(eventType string, version int, traceID, correlationID string, causationID *string, now time.Time) Envelope {
	if traceID == "" {
		traceID = NewTraceID()
	}
	if correlationID == "" {
		correlationID = uuid.NewString()
	}

	return Envelope{
		EventID:       uuid.NewString(),
		EventType:     eventType,
		EventVersion:  version,
		OccurredAt:    now.UTC(),
		Producer:      ProducerName,
		TraceID:       traceID,
		CorrelationID: correlationID,
		CausationID:   causationID,
		SchemaRef:     SchemaRef(eventType, version),
	}
}

// Validate checks the envelope invariants that consumers depend on.
func (e Envelope) Validate() error {
	if e.EventID == "" {
		return fmt.Errorf("envelope: event_id is required")
	}
	if _, err := uuid.Parse(e.EventID); err != nil {
		return fmt.Errorf("envelope: event_id must be a uuid: %w", err)
	}
	if e.EventType == "" {
		return fmt.Errorf("envelope: event_type is required")
	}
	if e.EventVersion < 1 {
		return fmt.Errorf("envelope: event_version must be >= 1")
	}
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("envelope: occurred_at is required")
	}
	if e.Producer != ProducerName {
		if e.Producer == "" {
			return fmt.Errorf("envelope: producer is required")
		}
	}
	if !ValidTraceID(e.TraceID) {
		return fmt.Errorf("envelope: trace_id must be 32 lowercase hex characters")
	}
	if _, err := uuid.Parse(e.CorrelationID); err != nil {
		return fmt.Errorf("envelope: correlation_id must be a uuid: %w", err)
	}
	if e.CausationID != nil {
		if _, err := uuid.Parse(*e.CausationID); err != nil {
			return fmt.Errorf("envelope: causation_id must be a uuid or null: %w", err)
		}
	}
	if e.SchemaRef != SchemaRef(e.EventType, e.EventVersion) {
		return fmt.Errorf("envelope: schema_ref must be %q", SchemaRef(e.EventType, e.EventVersion))
	}
	return nil
}

// NewTraceID mints a 32-character lowercase hex trace id.
func NewTraceID() string {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		// A failing CSPRNG must still never leave trace_id empty.
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer[:])
}

// ValidTraceID reports whether candidate is 32 lowercase hex characters.
func ValidTraceID(candidate string) bool {
	if len(candidate) != 32 {
		return false
	}
	for _, char := range candidate {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
