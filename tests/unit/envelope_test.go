package unit

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"community_service/internal/events"
)

func TestNewEnvelopeCarriesAllNineFields(t *testing.T) {
	t.Parallel()

	envelope := events.NewEnvelope(events.EventTypeCommunityMemberChanged, events.EventVersionV1,
		"", uuid.NewString(), nil, frozenNow)

	envelopeType := reflect.TypeOf(envelope)
	require.Equal(t, 9, envelopeType.NumField(), "the envelope is the mandated nine-field struct")
	assert.Equal(t, events.ProducerName, envelope.Producer)
	assert.NotEmpty(t, envelope.EventID)
	assert.NotEmpty(t, envelope.CorrelationID)
	assert.NotEmpty(t, envelope.TraceID)
	assert.Nil(t, envelope.CausationID)
	assert.Equal(t, events.EventVersionV1, envelope.EventVersion)
	assert.Equal(t, "EVENT_CONTRACTS.md#community_member_changed-v1", envelope.SchemaRef)
	assert.True(t, envelope.OccurredAt.Equal(frozenNow))
	assert.Equal(t, time.UTC, envelope.OccurredAt.Location())
	assert.NoError(t, envelope.Validate())
}

func TestNewEnvelopeMintsTraceWhenAbsent(t *testing.T) {
	t.Parallel()

	first := events.NewEnvelope(events.EventTypeCommunityChannelChanged, events.EventVersionV1, "", "", nil, frozenNow)
	second := events.NewEnvelope(events.EventTypeCommunityChannelChanged, events.EventVersionV1, "", "", nil, frozenNow)

	assert.Len(t, first.TraceID, 32, "a minted trace_id is 32 lowercase hex characters")
	assert.True(t, events.ValidTraceID(first.TraceID))
	assert.NotEqual(t, first.TraceID, second.TraceID, "each flow gets its own minted trace id")
	assert.NotEqual(t, first.EventID, second.EventID, "every event id is unique")
	assert.NoError(t, first.Validate())
}

func TestNewEnvelopePreservesCausation(t *testing.T) {
	t.Parallel()

	causingEventID := uuid.NewString()
	traceID := "0123456789abcdef0123456789abcdef"
	correlationID := uuid.NewString()

	envelope := events.NewEnvelope(events.EventTypeCommunityMemberChanged, events.EventVersionV1, traceID, correlationID, &causingEventID, frozenNow)

	assert.Equal(t, traceID, envelope.TraceID)
	assert.Equal(t, correlationID, envelope.CorrelationID)
	require.NotNil(t, envelope.CausationID)
	assert.Equal(t, causingEventID, *envelope.CausationID)
}

func TestEnvelopeValidationRejectsBrokenMessages(t *testing.T) {
	t.Parallel()

	base := func() events.Envelope {
		return events.NewEnvelope(events.EventTypeCommunityMemberChanged, events.EventVersionV1, "", uuid.NewString(), nil, frozenNow)
	}

	testCases := []struct {
		name   string
		mutate func(*events.Envelope)
	}{
		{name: "missing event id", mutate: func(e *events.Envelope) { e.EventID = "" }},
		{name: "malformed event id", mutate: func(e *events.Envelope) { e.EventID = "not-a-uuid" }},
		{name: "missing event type", mutate: func(e *events.Envelope) { e.EventType = "" }},
		{name: "zero version", mutate: func(e *events.Envelope) { e.EventVersion = 0 }},
		{name: "missing occurred at", mutate: func(e *events.Envelope) { e.OccurredAt = time.Time{} }},
		{name: "empty trace id", mutate: func(e *events.Envelope) { e.TraceID = "" }},
		{name: "uppercase trace id", mutate: func(e *events.Envelope) { e.TraceID = "0123456789ABCDEF0123456789abcdef" }},
		{name: "malformed correlation id", mutate: func(e *events.Envelope) { e.CorrelationID = "nope" }},
		{name: "malformed causation id", mutate: func(e *events.Envelope) { e.CausationID = pointer("nope") }},
		{name: "schema ref must follow the contract", mutate: func(e *events.Envelope) { e.SchemaRef = "EVENT_CONTRACTS.md#something-else-v1" }},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			envelope := base()
			tc.mutate(&envelope)
			assert.Error(t, envelope.Validate())
		})
	}
}

func TestPayloadsNeverCarryEnvelopeFields(t *testing.T) {
	t.Parallel()

	envelopeFields := map[string]struct{}{
		"event_id": {}, "event_type": {}, "event_version": {}, "occurred_at": {},
		"producer": {}, "trace_id": {}, "correlation_id": {}, "causation_id": {}, "schema_ref": {},
	}

	payloads := []any{
		events.CommunityMemberChanged{},
		events.CommunityChannelChanged{},
		events.MemberSanctionChanged{},
	}

	for _, payload := range payloads {
		payloadType := reflect.TypeOf(payload)
		for index := 0; index < payloadType.NumField(); index++ {
			tag := payloadType.Field(index).Tag.Get("json")
			assert.NotContains(t, envelopeFields, tag, "%s must not redeclare an envelope field", payloadType.Name())
		}
	}
}

func TestMessageShapeHasExactlyEnvelopeAndPayload(t *testing.T) {
	t.Parallel()

	message := events.Message{
		Envelope: events.NewEnvelope(events.EventTypeCommunityChannelChanged, events.EventVersionV1, "", uuid.NewString(), nil, frozenNow),
		Payload:  events.CommunityChannelChanged{Name: "general"},
	}

	encoded, err := json.Marshal(message)
	require.NoError(t, err)

	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Len(t, decoded, 2)
	assert.Contains(t, decoded, "envelope")
	assert.Contains(t, decoded, "payload")

	var envelope struct {
		EventID     string  `json:"event_id"`
		TraceID     string  `json:"trace_id"`
		CausationID *string `json:"causation_id"`
		Producer    string  `json:"producer"`
		SchemaRef   string  `json:"schema_ref"`
	}
	require.NoError(t, json.Unmarshal(decoded["envelope"], &envelope))
	assert.NotEmpty(t, envelope.EventID)
	assert.True(t, events.ValidTraceID(envelope.TraceID))
	assert.Nil(t, envelope.CausationID)
	assert.Equal(t, events.ProducerName, envelope.Producer)
}

func TestValidTraceID(t *testing.T) {
	t.Parallel()

	assert.True(t, events.ValidTraceID("0123456789abcdef0123456789abcdef"))
	assert.False(t, events.ValidTraceID("0123456789abcdef0123456789abcde"))
	assert.False(t, events.ValidTraceID("0123456789abcdef0123456789abcdeg"))
	assert.False(t, events.ValidTraceID(""))
}

// pointer returns a pointer to the provided value.
func pointer[T any](value T) *T { return &value }
