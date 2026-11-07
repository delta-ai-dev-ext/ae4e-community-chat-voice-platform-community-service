package events

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fixedNow is the deterministic producer clock of these tests.
var fixedNow = time.Date(2026, 2, 5, 10, 0, 0, 0, time.UTC)

// TestBuildProducerMessageMirrorsEnvelopeHeaders proves the Kafka record carries
// the JSON envelope as its value, the community id as its key and trace_id plus
// event_type as headers.
func TestBuildProducerMessageMirrorsEnvelopeHeaders(t *testing.T) {
	t.Parallel()

	envelope := NewEnvelope(EventTypeCommunityMemberChanged, EventVersionV1, "", uuid.NewString(), nil, fixedNow)
	message := Message{Envelope: envelope, Payload: CommunityMemberChanged{
		CommunityID:   uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		UserID:        uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		Change:        "joined",
		Status:        "active",
		RoleIDs:       []uuid.UUID{},
		MemberVersion: 1,
	}}

	record, err := buildProducerMessage(CommunityTopic, "11111111-1111-4111-8111-111111111111", message)
	require.NoError(t, err)

	assert.Equal(t, CommunityTopic, record.Topic)
	key, err := record.Key.Encode()
	require.NoError(t, err)
	assert.Equal(t, "11111111-1111-4111-8111-111111111111", string(key), "events are keyed by community_id")
	assert.True(t, record.Timestamp.Equal(fixedNow))

	headers := map[string]string{}
	for _, header := range record.Headers {
		headers[string(header.Key)] = string(header.Value)
	}
	assert.Equal(t, envelope.TraceID, headers[HeaderTraceID])
	assert.Equal(t, EventTypeCommunityMemberChanged, headers[HeaderEventType])

	value, err := record.Value.Encode()
	require.NoError(t, err)

	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(value, &decoded))
	require.Len(t, decoded, 2, "a message is exactly envelope + payload")

	var roundTripped Envelope
	require.NoError(t, json.Unmarshal(decoded["envelope"], &roundTripped))
	assert.Equal(t, envelope, roundTripped)
}

func TestBuildProducerMessageRejectsInvalidEnvelope(t *testing.T) {
	t.Parallel()

	envelope := NewEnvelope(EventTypeCommunityChannelChanged, EventVersionV1, "", uuid.NewString(), nil, fixedNow)
	envelope.TraceID = ""

	_, err := buildProducerMessage(CommunityTopic, "key", Message{Envelope: envelope, Payload: struct{}{}})
	require.Error(t, err, "an envelope without trace_id must never be published")
}

func TestBrokerConfigAppliesSASLAndTLS(t *testing.T) {
	t.Parallel()

	config := newTestSaramaConfig()
	err := BrokerConfig{
		Brokers:       []string{"broker:9092"},
		SASLMechanism: "SCRAM-SHA-512",
		SASLUsername:  "user",
		SASLPassword:  "secret",
		TLSEnabled:    true,
	}.Apply(config, "community_service-producer")

	require.NoError(t, err)
	assert.True(t, config.Net.SASL.Enable)
	assert.Equal(t, "user", config.Net.SASL.User)
	assert.True(t, config.Net.TLS.Enable)
	assert.Equal(t, "community_service-producer", config.ClientID)
}

func TestBrokerConfigRejectsUnknownSASLMechanism(t *testing.T) {
	t.Parallel()

	err := BrokerConfig{SASLMechanism: "KERBEROS-BUT-NOT-REALLY"}.Apply(newTestSaramaConfig(), "test")

	require.Error(t, err)
}

func TestRetryingPublisherRetriesThenSucceeds(t *testing.T) {
	t.Parallel()

	capturing := &recordingPublisher{failures: 2}
	publisher := &RetryingPublisher{
		Publisher: capturing,
		Attempts:  4,
		BaseDelay: time.Millisecond,
		Logger:    zap.NewNop(),
	}

	err := publisher.Publish(context.Background(), CommunityTopic, "key",
		Message{Envelope: NewEnvelope(EventTypeCommunityTopicEvent(), EventVersionV1, "", uuid.NewString(), nil, fixedNow)})

	require.NoError(t, err)
	assert.Equal(t, 3, capturing.calls)
	assert.Equal(t, 1, capturing.succeeded)
}

func TestRetryingPublisherReportsFinalFailure(t *testing.T) {
	t.Parallel()

	capturing := &recordingPublisher{failures: 10}
	var reported bool

	publisher := &RetryingPublisher{
		Publisher: capturing,
		Attempts:  3,
		BaseDelay: time.Millisecond,
		Logger:    zap.NewNop(),
		OnFailure: func(topic, eventType string, err error) {
			reported = true
			assert.Equal(t, CommunityTopic, topic)
			assert.Error(t, err)
		},
	}

	err := publisher.Publish(context.Background(), CommunityTopic, "key",
		Message{Envelope: NewEnvelope(EventTypeCommunityTopicEvent(), EventVersionV1, "", uuid.NewString(), nil, fixedNow)})

	require.Error(t, err)
	assert.True(t, reported)
	assert.Equal(t, 3, capturing.calls)
}

func TestRetryingPublisherStopsWhenContextIsCancelled(t *testing.T) {
	t.Parallel()

	capturing := &recordingPublisher{failures: 10}
	publisher := &RetryingPublisher{Publisher: capturing, Attempts: 5, BaseDelay: time.Hour, Logger: zap.NewNop()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := publisher.Publish(ctx, CommunityTopic, "key",
		Message{Envelope: NewEnvelope(EventTypeCommunityTopicEvent(), EventVersionV1, "", uuid.NewString(), nil, fixedNow)})

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, capturing.calls)
}

// EventTypeCommunityTopicEvent mirrors a produced event type for envelope-only
// assertions.
func EventTypeCommunityTopicEvent() string { return EventTypeCommunityChannelChanged }

// recordingPublisher counts publish attempts and fails a number of them.
type recordingPublisher struct {
	failures  int
	calls     int
	succeeded int
}

// newTestSaramaConfig returns a fresh sarama configuration for BrokerConfig tests.
func newTestSaramaConfig() *sarama.Config { return sarama.NewConfig() }

func (p *recordingPublisher) Publish(context.Context, string, string, Message) error {
	p.calls++
	if p.failures > 0 {
		p.failures--
		return errors.New("broker unavailable")
	}
	p.succeeded++
	return nil
}
