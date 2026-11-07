package events

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"go.uber.org/zap"
)

// Publisher publishes one enveloped message to a topic, keyed so that all events
// of one aggregate stay ordered in the same partition.
type Publisher interface {
	Publish(ctx context.Context, topic, key string, message Message) error
}

// BrokerConfig is the Kafka connection configuration shared by the producer and
// the consumer; SASL and TLS stay optional so an unauthenticated dev broker
// works.
type BrokerConfig struct {
	Brokers       []string
	SASLMechanism string
	SASLUsername  string
	SASLPassword  string
	TLSEnabled    bool
}

// buildProducerMessage renders one enveloped message as a Kafka record: the JSON
// envelope stays the source of truth and trace_id/event_type are mirrored into
// the headers for routing and observability.
func buildProducerMessage(topic, key string, message Message) (*sarama.ProducerMessage, error) {
	if err := message.Envelope.Validate(); err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("events: encode message: %w", err)
	}

	return &sarama.ProducerMessage{
		Topic:     topic,
		Key:       sarama.StringEncoder(key),
		Value:     sarama.ByteEncoder(encoded),
		Timestamp: message.Envelope.OccurredAt,
		Headers: []sarama.RecordHeader{
			{Key: []byte(HeaderTraceID), Value: []byte(message.Envelope.TraceID)},
			{Key: []byte(HeaderEventType), Value: []byte(message.Envelope.EventType)},
		},
	}, nil
}

// Apply writes the broker settings into a sarama configuration.
func (c BrokerConfig) Apply(config *sarama.Config, clientID string) error {
	config.ClientID = clientID
	config.Version = sarama.V2_1_0_0
	config.Net.DialTimeout = 10 * time.Second
	config.Net.ReadTimeout = 10 * time.Second
	config.Net.WriteTimeout = 10 * time.Second

	if c.TLSEnabled {
		config.Net.TLS.Enable = true
		config.Net.TLS.Config = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	if c.SASLMechanism == "" {
		return nil
	}

	mechanism := sarama.SASLMechanism(strings.ToUpper(c.SASLMechanism))
	switch mechanism {
	case sarama.SASLTypePlaintext, sarama.SASLTypeSCRAMSHA256, sarama.SASLTypeSCRAMSHA512:
	default:
		return fmt.Errorf("events: unsupported KAFKA_SASL_MECHANISM %q", c.SASLMechanism)
	}

	config.Net.SASL.Enable = true
	config.Net.SASL.Mechanism = mechanism
	config.Net.SASL.User = c.SASLUsername
	config.Net.SASL.Password = c.SASLPassword
	return nil
}

// SaramaProducer is the idempotent Kafka producer of community_events.
type SaramaProducer struct {
	client   sarama.Client
	producer sarama.SyncProducer
	logger   *zap.Logger
}

// errProducerClosed reports a producer that can no longer reach Kafka.
var errProducerClosed = errors.New("events: kafka producer is closed")

// NewProducer builds an idempotent producer (acks=all, one in-flight request) so
// a retried publish cannot duplicate or reorder events in a partition.
func NewProducer(brokers BrokerConfig, logger *zap.Logger) (*SaramaProducer, error) {
	config := sarama.NewConfig()
	if err := brokers.Apply(config, "community_service-producer"); err != nil {
		return nil, err
	}
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Idempotent = true
	config.Producer.Return.Successes = true
	config.Producer.Retry.Max = 5
	config.Net.MaxOpenRequests = 1

	client, err := sarama.NewClient(brokers.Brokers, config)
	if err != nil {
		return nil, fmt.Errorf("events: create kafka client: %w", err)
	}

	producer, err := sarama.NewSyncProducerFromClient(client)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("events: create kafka producer: %w", err)
	}

	return &SaramaProducer{client: client, producer: producer, logger: logger}, nil
}

// Publish writes one enveloped message, mirroring trace_id and event_type into
// the Kafka headers.
func (p *SaramaProducer) Publish(ctx context.Context, topic, key string, message Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	kafkaMessage, err := buildProducerMessage(topic, key, message)
	if err != nil {
		return err
	}

	started := time.Now()
	partition, offset, err := p.producer.SendMessage(kafkaMessage)
	if err != nil {
		if p.logger != nil {
			p.logger.Error("kafka publish failed",
				zap.String("topic", topic),
				zap.String("event_type", message.Envelope.EventType),
				zap.String("trace_id", message.Envelope.TraceID),
				zap.Duration("latency", time.Since(started)),
				zap.Error(err))
		}
		return fmt.Errorf("events: publish to %s: %w", topic, err)
	}

	if p.logger != nil {
		p.logger.Debug("kafka publish succeeded",
			zap.String("topic", topic),
			zap.String("event_type", message.Envelope.EventType),
			zap.String("trace_id", message.Envelope.TraceID),
			zap.Int32("partition", partition),
			zap.Int64("offset", offset),
			zap.Duration("latency", time.Since(started)))
	}
	return nil
}

// Healthy reports whether the producer can still reach the brokers; readiness
// flips to failing when it cannot.
func (p *SaramaProducer) Healthy() error {
	if p == nil || p.client == nil {
		return errProducerClosed
	}
	if p.client.Closed() {
		return errProducerClosed
	}
	if err := p.client.RefreshMetadata(); err != nil {
		return fmt.Errorf("events: kafka metadata refresh: %w", err)
	}
	return nil
}

// Close flushes and releases the producer.
func (p *SaramaProducer) Close() error {
	if p == nil {
		return nil
	}
	if p.producer != nil {
		if err := p.producer.Close(); err != nil {
			return fmt.Errorf("events: close producer: %w", err)
		}
	}
	if p.client != nil && !p.client.Closed() {
		if err := p.client.Close(); err != nil {
			return fmt.Errorf("events: close client: %w", err)
		}
	}
	return nil
}
