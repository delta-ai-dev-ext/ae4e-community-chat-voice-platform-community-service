// Package obs holds the observability primitives of the service: the structured
// JSON logger and the Prometheus metrics required by docs/architecture.md §7.
package obs

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ServiceName is attached to every log line and metric as the service identity.
const ServiceName = "community_service"

// Metric names from docs/architecture.md §7.
const (
	MetricCommunityCreated   = "community_created_total"
	MetricInvitesCreated     = "community_invites_created_total"
	MetricJoins              = "community_joins_total"
	MetricLeaves             = "community_leaves_total"
	MetricRoleAssignments    = "community_role_assignments_total"
	MetricRequests           = "http_requests_total"
	MetricRequestDuration    = "http_request_duration_seconds"
	MetricKafkaConsumerLag   = "kafka_consumer_lag"
	MetricKafkaConsumerError = "kafka_consumer_errors_total"
	MetricKafkaDeadLetter    = "kafka_dead_letter_total"
	MetricPublishFailures    = "kafka_publish_failures_total"
)

// Join results recorded in community_joins_total{result}.
const (
	JoinResultJoined  = "joined"
	JoinResultBanned  = "banned"
	JoinResultExpired = "expired"
	JoinResultInvalid = "invalid"
)

// Leave reasons recorded in community_leaves_total{reason}.
const (
	LeaveReasonLeft   = "left"
	LeaveReasonKicked = "kicked"
	LeaveReasonBanned = "banned"
)

// Metrics is the service metric registry. All labels are low cardinality: route
// templates, methods, status classes and outcomes — never identifiers.
type Metrics struct {
	registry *prometheus.Registry

	communityCreated prometheus.Counter
	invitesCreated   prometheus.Counter
	joins            *prometheus.CounterVec
	leaves           *prometheus.CounterVec
	roleAssignments  prometheus.Counter
	requests         *prometheus.CounterVec
	requestDuration  *prometheus.HistogramVec
	kafkaLag         *prometheus.GaugeVec
	kafkaErrors      prometheus.Counter
	kafkaDeadLetters *prometheus.CounterVec
	publishFailures  prometheus.Counter
}

// NewMetrics builds and registers every metric in a fresh registry.
func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	metrics := &Metrics{
		registry: registry,
		communityCreated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: MetricCommunityCreated,
			Help: "Communities created by this instance.",
		}),
		invitesCreated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: MetricInvitesCreated,
			Help: "Invite codes created by this instance.",
		}),
		joins: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: MetricJoins,
			Help: "Invite accept attempts by result.",
		}, []string{"result"}),
		leaves: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: MetricLeaves,
			Help: "Membership removals by reason.",
		}, []string{"reason"}),
		roleAssignments: prometheus.NewCounter(prometheus.CounterOpts{
			Name: MetricRoleAssignments,
			Help: "Role assignment replacements applied.",
		}),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: MetricRequests,
			Help: "HTTP requests by route template, method and status class.",
		}, []string{"route", "method", "status_class"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    MetricRequestDuration,
			Help:    "HTTP request latency by route template and method.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.15, 0.25, 0.5, 1, 2.5},
		}, []string{"route", "method"}),
		kafkaLag: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: MetricKafkaConsumerLag,
			Help: "Observed consumer lag per partition of the consumed topic.",
		}, []string{"topic", "consumer_group"}),
		kafkaErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: MetricKafkaConsumerError,
			Help: "Consumer-group errors.",
		}),
		kafkaDeadLetters: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: MetricKafkaDeadLetter,
			Help: "Consumed events that exhausted their retry budget.",
		}, []string{"event_type"}),
		publishFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: MetricPublishFailures,
			Help: "Events that could not be published after every retry.",
		}),
	}

	registry.MustRegister(
		metrics.communityCreated,
		metrics.invitesCreated,
		metrics.joins,
		metrics.leaves,
		metrics.roleAssignments,
		metrics.requests,
		metrics.requestDuration,
		metrics.kafkaLag,
		metrics.kafkaErrors,
		metrics.kafkaDeadLetters,
		metrics.publishFailures,
	)

	// Initialise the closed label sets so a dashboard shows 0 instead of "no
	// data" before the first event of each outcome.
	for _, result := range []string{JoinResultJoined, JoinResultBanned, JoinResultExpired, JoinResultInvalid} {
		metrics.joins.WithLabelValues(result)
	}
	for _, reason := range []string{LeaveReasonLeft, LeaveReasonKicked, LeaveReasonBanned} {
		metrics.leaves.WithLabelValues(reason)
	}

	return metrics
}

// Registry exposes the registry backing the /metrics endpoint.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// RecordCommunityCreated counts one created community.
func (m *Metrics) RecordCommunityCreated() { m.communityCreated.Inc() }

// RecordInviteCreated counts one created invite.
func (m *Metrics) RecordInviteCreated() { m.invitesCreated.Inc() }

// RecordJoin counts one invite accept attempt result.
func (m *Metrics) RecordJoin(result string) { m.joins.WithLabelValues(result).Inc() }

// RecordLeave counts one membership removal reason.
func (m *Metrics) RecordLeave(reason string) { m.leaves.WithLabelValues(reason).Inc() }

// RecordRoleAssignments counts applied role-set replacements.
func (m *Metrics) RecordRoleAssignments() { m.roleAssignments.Inc() }

// RecordRequest counts one HTTP request and its latency, keyed by the resolved
// route template so cardinality stays bounded.
func (m *Metrics) RecordRequest(route, method string, status int, duration time.Duration) {
	if route == "" {
		route = "unmatched"
	}
	m.requests.WithLabelValues(route, method, statusClass(status)).Inc()
	m.requestDuration.WithLabelValues(route, method).Observe(duration.Seconds())
}

// RecordConsumerLag publishes the observed lag of the consumed topic.
func (m *Metrics) RecordConsumerLag(topic, consumerGroup string, lag int64) {
	m.kafkaLag.WithLabelValues(topic, consumerGroup).Set(float64(lag))
}

// RecordConsumerError counts one consumer-group error.
func (m *Metrics) RecordConsumerError() { m.kafkaErrors.Inc() }

// RecordDeadLetter counts one dead-lettered event.
func (m *Metrics) RecordDeadLetter(eventType string) {
	m.kafkaDeadLetters.WithLabelValues(eventType).Inc()
}

// RecordPublishFailure counts one event that survived no publish attempt.
func (m *Metrics) RecordPublishFailure() { m.publishFailures.Inc() }

// Handler serves the Prometheus exposition format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func statusClass(status int) string {
	switch {
	case status >= 100 && status < 200:
		return "1xx"
	case status >= 200 && status < 300:
		return "2xx"
	case status >= 300 && status < 400:
		return "3xx"
	case status >= 400 && status < 500:
		return "4xx"
	case status >= 500:
		return "5xx"
	default:
		return "unknown"
	}
}

// StatusClass is exported for the HTTP middleware.
func StatusClass(status int) string { return statusClass(status) }

// FormatStatusCode renders a status code for logs without importing net/http at
// call sites.
func FormatStatusCode(status int) string { return strconv.Itoa(status) }
