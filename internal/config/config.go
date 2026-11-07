// Package config loads and validates the service configuration from the
// environment once at startup, so a misconfigured instance fails fast instead of
// serving traffic with partial settings.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultKafkaConsumerGroup is the shared consumer group used for
	// moderation_events when KAFKA_CONSUMER_GROUP is not set.
	DefaultKafkaConsumerGroup = "community_service"
	// DefaultPort is the HTTP listen port used when PORT is not set.
	DefaultPort = 8080
	// DefaultShutdownTimeoutSeconds is the drain budget used when
	// SHUTDOWN_TIMEOUT_SECONDS is not set.
	DefaultShutdownTimeoutSeconds = 20
	// DefaultLogLevel is the zap level used when LOG_LEVEL is not set.
	DefaultLogLevel = "info"

	maxPort                   = 65535
	maxShutdownTimeoutSeconds = 600
)

// ValidLogLevels is the closed set of accepted LOG_LEVEL values.
var ValidLogLevels = []string{"debug", "info", "warn", "error"}

// Getenv is the environment accessor injected into Load; production passes
// os.Getenv, tests pass a map-backed implementation.
type Getenv func(key string) string

// Config is the fully validated runtime configuration of the service.
type Config struct {
	DatabaseURL          string
	KafkaBrokers         []string
	KafkaConsumerGroup   string
	KafkaSASLMechanism   string
	KafkaSASLUsername    string
	KafkaSASLPassword    string
	KafkaTLSEnabled      bool
	IdentityJWKSPath     string
	IdentityJWKS         string
	IdentityIssuer       string
	IdentityAudience     string
	InternalServiceToken string
	Port                 int
	ShutdownTimeout      time.Duration
	LogLevel             string

	// problems carries parse failures detected while decoding the environment
	// so Validate reports them together with the semantic checks.
	problems []string
}

// ValidationError reports every configuration problem found at startup rather
// than only the first one, so an operator can fix them in one pass.
type ValidationError struct {
	Problems []string
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	return "invalid configuration: " + strings.Join(e.Problems, "; ")
}

// Load reads the environment through getenv and returns a validated Config.
func Load(getenv Getenv) (*Config, error) {
	if getenv == nil {
		return nil, errors.New("config: getenv must not be nil")
	}

	cfg := &Config{
		DatabaseURL:          strings.TrimSpace(getenv("DATABASE_URL")),
		KafkaConsumerGroup:   valueOrDefault(getenv("KAFKA_CONSUMER_GROUP"), DefaultKafkaConsumerGroup),
		KafkaSASLMechanism:   strings.TrimSpace(getenv("KAFKA_SASL_MECHANISM")),
		KafkaSASLUsername:    getenv("KAFKA_SASL_USERNAME"),
		KafkaSASLPassword:    getenv("KAFKA_SASL_PASSWORD"),
		IdentityJWKSPath:     strings.TrimSpace(getenv("IDENTITY_JWKS_PATH")),
		IdentityJWKS:         strings.TrimSpace(getenv("IDENTITY_JWKS")),
		IdentityIssuer:       strings.TrimSpace(getenv("IDENTITY_ISSUER")),
		IdentityAudience:     strings.TrimSpace(getenv("IDENTITY_AUDIENCE")),
		InternalServiceToken: strings.TrimSpace(getenv("INTERNAL_SERVICE_TOKEN")),
		LogLevel:             strings.ToLower(valueOrDefault(strings.TrimSpace(getenv("LOG_LEVEL")), DefaultLogLevel)),
	}

	for _, broker := range strings.Split(getenv("KAFKA_BROKERS"), ",") {
		if trimmed := strings.TrimSpace(broker); trimmed != "" {
			cfg.KafkaBrokers = append(cfg.KafkaBrokers, trimmed)
		}
	}

	port, portErr := strconv.Atoi(valueOrDefault(strings.TrimSpace(getenv("PORT")), strconv.Itoa(DefaultPort)))
	if portErr == nil {
		cfg.Port = port
	}

	shutdownSeconds, shutdownErr := strconv.Atoi(valueOrDefault(strings.TrimSpace(getenv("SHUTDOWN_TIMEOUT_SECONDS")), strconv.Itoa(DefaultShutdownTimeoutSeconds)))
	if shutdownErr == nil {
		cfg.ShutdownTimeout = time.Duration(shutdownSeconds) * time.Second
	}

	tlsEnabled, tlsErr := parseBool(getenv("KAFKA_TLS_ENABLED"))
	if tlsErr == nil {
		cfg.KafkaTLSEnabled = tlsEnabled
	}

	var problems []string
	if portErr != nil {
		problems = append(problems, fmt.Sprintf("PORT must be an integer: %v", portErr))
	}
	if shutdownErr != nil {
		problems = append(problems, fmt.Sprintf("SHUTDOWN_TIMEOUT_SECONDS must be an integer: %v", shutdownErr))
	}
	if tlsErr != nil {
		problems = append(problems, fmt.Sprintf("KAFKA_TLS_ENABLED must be a boolean: %v", tlsErr))
	}
	cfg.problems = problems

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate checks every mandatory setting and rejects out-of-range values.
func (c *Config) Validate() error {
	problems := append([]string{}, c.problems...)

	if c.DatabaseURL == "" {
		problems = append(problems, "DATABASE_URL is required")
	}
	if len(c.KafkaBrokers) == 0 {
		problems = append(problems, "KAFKA_BROKERS is required")
	}
	if c.KafkaConsumerGroup == "" {
		problems = append(problems, "KAFKA_CONSUMER_GROUP must not be empty")
	}
	if c.KafkaSASLMechanism != "" && (c.KafkaSASLUsername == "" || c.KafkaSASLPassword == "") {
		problems = append(problems, "KAFKA_SASL_USERNAME and KAFKA_SASL_PASSWORD are required when KAFKA_SASL_MECHANISM is set")
	}
	if c.IdentityJWKSPath == "" && c.IdentityJWKS == "" {
		problems = append(problems, "IDENTITY_JWKS_PATH or IDENTITY_JWKS is required")
	}
	if c.IdentityIssuer == "" {
		problems = append(problems, "IDENTITY_ISSUER is required")
	}
	if c.IdentityAudience == "" {
		problems = append(problems, "IDENTITY_AUDIENCE is required")
	}
	if c.InternalServiceToken == "" {
		problems = append(problems, "INTERNAL_SERVICE_TOKEN is required")
	}
	if c.Port < 1 || c.Port > maxPort {
		problems = append(problems, fmt.Sprintf("PORT must be between 1 and %d", maxPort))
	}
	if c.ShutdownTimeout <= 0 || c.ShutdownTimeout > maxShutdownTimeoutSeconds*time.Second {
		problems = append(problems, fmt.Sprintf("SHUTDOWN_TIMEOUT_SECONDS must be between 1 and %d", maxShutdownTimeoutSeconds))
	}
	if !isValidLogLevel(c.LogLevel) {
		problems = append(problems, fmt.Sprintf("LOG_LEVEL must be one of %s", strings.Join(ValidLogLevels, ", ")))
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

func isValidLogLevel(level string) bool {
	for _, valid := range ValidLogLevels {
		if level == valid {
			return true
		}
	}
	return false
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func parseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "false", "0", "no", "off":
		return false, nil
	case "true", "1", "yes", "on":
		return true, nil
	default:
		return false, fmt.Errorf("unrecognised boolean %q", value)
	}
}
