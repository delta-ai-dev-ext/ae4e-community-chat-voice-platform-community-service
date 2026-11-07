package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/IBM/sarama"
	"go.uber.org/zap"
)

// DefaultConsumerAttempts is the retry budget of one consumed event before it is
// dead-lettered.
const DefaultConsumerAttempts = 3

// Handler consumes decoded moderation events. Returning an error asks the
// consumer to retry and, once the budget is spent, to dead-letter the message.
type Handler interface {
	HandleEvent(ctx context.Context, message RawMessage) error
}

// ConsumerSettings configures the moderation_events consumer group.
type ConsumerSettings struct {
	Brokers        BrokerConfig
	GroupID        string
	Topic          string
	MaxAttempts    int
	RetryBaseDelay time.Duration
}

// Consumer runs one shared consumer group over moderation_events. Partitions are
// distributed across replicas, so per-community ordering is preserved without
// duplicating work.
type Consumer struct {
	group    sarama.ConsumerGroup
	settings ConsumerSettings
	handler  Handler
	logger   *zap.Logger

	// OnLag reports the observed lag of a partition.
	OnLag func(topic, consumerGroup string, lag int64)
	// OnError reports a consumer-group failure.
	OnError func(err error)
	// OnDeadLetter reports a message that exhausted its retry budget.
	OnDeadLetter func(topic string, err error)
}

// NewConsumer builds a sarama consumer group with manual offset marking, so an
// offset only advances after the state change was committed.
func NewConsumer(settings ConsumerSettings, handler Handler, logger *zap.Logger) (*Consumer, error) {
	if settings.MaxAttempts <= 0 {
		settings.MaxAttempts = DefaultConsumerAttempts
	}
	if settings.RetryBaseDelay <= 0 {
		settings.RetryBaseDelay = 100 * time.Millisecond
	}
	if settings.Topic == "" {
		settings.Topic = ModerationTopic
	}

	config := sarama.NewConfig()
	if err := settings.Brokers.Apply(config, "community_service-consumer"); err != nil {
		return nil, err
	}
	config.Consumer.Return.Errors = true
	config.Consumer.Offsets.Initial = sarama.OffsetOldest
	config.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRoundRobin()}

	group, err := sarama.NewConsumerGroup(settings.Brokers.Brokers, settings.GroupID, config)
	if err != nil {
		return nil, fmt.Errorf("events: create consumer group: %w", err)
	}

	return &Consumer{group: group, settings: settings, handler: handler, logger: logger}, nil
}

// Run consumes until the context is cancelled, rejoining the group with back-off
// after a rebalance or broker hiccup.
func (c *Consumer) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		if err := c.group.Consume(ctx, []string{c.settings.Topic}, c); err != nil {
			if errors.Is(err, sarama.ErrClosedConsumerGroup) {
				return nil
			}
			if c.logger != nil {
				c.logger.Error("kafka consume failed",
					zap.String("topic", c.settings.Topic),
					zap.String("consumer_group", c.settings.GroupID),
					zap.Error(err))
			}
			if c.OnError != nil {
				c.OnError(err)
			}
			if err := wait(ctx, c.settings.RetryBaseDelay); err != nil {
				return nil
			}
		}
	}
}

// Close leaves the consumer group.
func (c *Consumer) Close() error {
	if c == nil || c.group == nil {
		return nil
	}
	if err := c.group.Close(); err != nil && !errors.Is(err, sarama.ErrClosedConsumerGroup) {
		return fmt.Errorf("events: close consumer group: %w", err)
	}
	return nil
}

// Setup implements sarama.ConsumerGroupHandler.
func (c *Consumer) Setup(sarama.ConsumerGroupSession) error { return nil }

// Cleanup implements sarama.ConsumerGroupHandler.
func (c *Consumer) Cleanup(sarama.ConsumerGroupSession) error { return nil }

// ConsumeClaim processes messages of one partition in order, marking each offset
// only after it was handled or dead-lettered.
func (c *Consumer) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case <-session.Context().Done():
			return nil
		case message, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			c.process(session, claim, message)
		}
	}
}

func (c *Consumer) process(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim, message *sarama.ConsumerMessage) {
	defer session.MarkMessage(message, "")

	if c.OnLag != nil {
		if lag := claim.HighWaterMarkOffset() - message.Offset - 1; lag >= 0 {
			c.OnLag(claim.Topic(), c.settings.GroupID, lag)
		}
	}

	raw, err := decode(message)
	if err != nil {
		c.deadLetter(claim.Topic(), err)
		return
	}
	if raw.Envelope.EventType != EventTypeMemberSanctionChanged {
		if c.logger != nil {
			c.logger.Debug("skipping event of another contract",
				zap.String("event_type", raw.Envelope.EventType),
				zap.String("trace_id", raw.Envelope.TraceID))
		}
		return
	}

	delay := c.settings.RetryBaseDelay
	for attempt := 1; attempt <= c.settings.MaxAttempts; attempt++ {
		handleErr := c.handler.HandleEvent(session.Context(), raw)
		if handleErr == nil {
			return
		}
		if c.logger != nil {
			c.logger.Warn("sanction event processing failed",
				zap.String("event_id", raw.Envelope.EventID),
				zap.String("trace_id", raw.Envelope.TraceID),
				zap.Int("attempt", attempt),
				zap.Int("attempts", c.settings.MaxAttempts),
				zap.Error(handleErr))
		}
		if attempt == c.settings.MaxAttempts {
			c.deadLetter(claim.Topic(), handleErr)
			return
		}
		if err := wait(session.Context(), delay); err != nil {
			return
		}
		delay *= 2
	}
}

func (c *Consumer) deadLetter(topic string, err error) {
	if c.logger != nil {
		c.logger.Error("sanction event dead-lettered",
			zap.String("topic", topic),
			zap.String("consumer_group", c.settings.GroupID),
			zap.Error(err))
	}
	if c.OnDeadLetter != nil {
		c.OnDeadLetter(topic, err)
	}
}

// decode validates the envelope and keeps the payload encoded for the handler.
func decode(message *sarama.ConsumerMessage) (RawMessage, error) {
	var raw RawMessage
	if err := json.Unmarshal(message.Value, &raw); err != nil {
		return RawMessage{}, fmt.Errorf("decode event message: %w", err)
	}
	if err := raw.Envelope.Validate(); err != nil {
		return RawMessage{}, err
	}
	return raw, nil
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
