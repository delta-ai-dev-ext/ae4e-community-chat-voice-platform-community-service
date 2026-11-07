package events

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
)

// DefaultRetryAttempts is the publish retry budget of a committed state change.
const DefaultRetryAttempts = 4

// RetryingPublisher decorates a Publisher with the publish-after-commit retry
// from docs/architecture.md §6: a state change is already committed, so a
// broker hiccup must not drop its event. Republication is safe because consumers
// are version-guarded and deduplicate on envelope.event_id.
type RetryingPublisher struct {
	Publisher Publisher
	Attempts  int
	BaseDelay time.Duration
	Logger    *zap.Logger
	OnFailure func(topic, eventType string, err error)
	Sleep     func(ctx context.Context, delay time.Duration) error
}

// Publish retries with exponential back-off and reports the final failure
// through OnFailure without failing the already-committed request.
func (p *RetryingPublisher) Publish(ctx context.Context, topic, key string, message Message) error {
	attempts := p.Attempts
	if attempts <= 0 {
		attempts = DefaultRetryAttempts
	}
	delay := p.BaseDelay
	if delay <= 0 {
		delay = 25 * time.Millisecond
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		lastErr = p.Publisher.Publish(ctx, topic, key, message)
		if lastErr == nil {
			return nil
		}
		if p.Logger != nil {
			p.Logger.Warn("event publish failed; retrying",
				zap.String("topic", topic),
				zap.String("event_type", message.Envelope.EventType),
				zap.String("trace_id", message.Envelope.TraceID),
				zap.Int("attempt", attempt),
				zap.Int("attempts", attempts),
				zap.Error(lastErr))
		}
		if attempt == attempts {
			break
		}
		if err := p.sleep(ctx, delay); err != nil {
			return err
		}
		delay *= 2
	}

	if p.OnFailure != nil {
		p.OnFailure(topic, message.Envelope.EventType, lastErr)
	}
	return lastErr
}

func (p *RetryingPublisher) sleep(ctx context.Context, delay time.Duration) error {
	if p.Sleep != nil {
		return p.Sleep(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// errConsumerClosed reports a consumer that was closed while running.
var errConsumerClosed = errors.New("events: consumer group closed")
