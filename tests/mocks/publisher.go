package mocks

import (
	"context"
	"sync"

	"community_service/internal/events"
)

// Publisher captures every published event so tests can assert on the envelope
// and payload, and can be told to fail a number of times to exercise the
// publish-after-commit retry.
type Publisher struct {
	mu        sync.Mutex
	messages  []PublishedMessage
	failures  int
	failEvery bool
}

// PublishedMessage is one captured publish call.
type PublishedMessage struct {
	Topic   string
	Key     string
	Message events.Message
}

// NewPublisher returns an empty capturing publisher.
func NewPublisher() *Publisher { return &Publisher{} }

// FailNext makes the next n publishes fail.
func (p *Publisher) FailNext(count int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failures = count
}

// FailAlways makes every publish fail.
func (p *Publisher) FailAlways() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failEvery = true
}

// Publish records the message, or fails when a failure is armed.
func (p *Publisher) Publish(_ context.Context, topic, key string, message events.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.failEvery {
		return errPublishFailed
	}
	if p.failures > 0 {
		p.failures--
		return errPublishFailed
	}

	p.messages = append(p.messages, PublishedMessage{Topic: topic, Key: key, Message: message})
	return nil
}

// Messages returns a copy of every captured publish call.
func (p *Publisher) Messages() []PublishedMessage {
	p.mu.Lock()
	defer p.mu.Unlock()

	copied := make([]PublishedMessage, len(p.messages))
	copy(copied, p.messages)
	return copied
}

// Reset drops every captured message and armed failure.
func (p *Publisher) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = nil
	p.failures = 0
	p.failEvery = false
}

// Payloads returns the captured payloads whose envelope carries eventType.
func (p *Publisher) Payloads(eventType string) []any {
	payloads := make([]any, 0, len(p.messages))
	for _, message := range p.Messages() {
		if message.Message.Envelope.EventType == eventType {
			payloads = append(payloads, message.Message.Payload)
		}
	}
	return payloads
}

// compile-time assertion that the mock satisfies the port.
var _ events.Publisher = (*Publisher)(nil)
