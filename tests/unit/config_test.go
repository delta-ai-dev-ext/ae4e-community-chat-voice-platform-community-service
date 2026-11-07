package unit

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"community_service/internal/config"
)

// env builds a map-backed config.Getenv so tests never touch the real process
// environment.
func env(values map[string]string) config.Getenv {
	return func(key string) string { return values[key] }
}

// requiredEnv is the minimal valid environment; individual tests mutate copies.
func requiredEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":           "postgres://user:pass@localhost:5432/community?sslmode=disable",
		"KAFKA_BROKERS":          "broker-1:9092",
		"IDENTITY_JWKS":          `{"keys":[]}`,
		"IDENTITY_ISSUER":        "https://identity.example.com",
		"IDENTITY_AUDIENCE":      "community_service",
		"INTERNAL_SERVICE_TOKEN": "service-token",
	}
}

func TestLoadAppliesDocumentedDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(env(requiredEnv()))
	require.NoError(t, err)

	assert.Equal(t, config.DefaultPort, cfg.Port)
	assert.Equal(t, config.DefaultKafkaConsumerGroup, cfg.KafkaConsumerGroup)
	assert.Equal(t, config.DefaultLogLevel, cfg.LogLevel)
	assert.Equal(t, config.DefaultShutdownTimeoutSeconds, int(cfg.ShutdownTimeout.Seconds()))
	assert.False(t, cfg.KafkaTLSEnabled)
	assert.Len(t, cfg.KafkaBrokers, 1)
}

func TestLoadParsesOptionalSettings(t *testing.T) {
	t.Parallel()

	values := requiredEnv()
	values["KAFKA_BROKERS"] = " a:9092 , b:9092 ,,"
	values["KAFKA_CONSUMER_GROUP"] = "custom-group"
	values["KAFKA_SASL_MECHANISM"] = "SCRAM-SHA-512"
	values["KAFKA_SASL_USERNAME"] = "user"
	values["KAFKA_SASL_PASSWORD"] = "secret"
	values["KAFKA_TLS_ENABLED"] = "TRUE"
	values["PORT"] = "9090"
	values["SHUTDOWN_TIMEOUT_SECONDS"] = "45"
	values["LOG_LEVEL"] = "DEBUG"
	values["IDENTITY_JWKS_PATH"] = "/var/run/identity/jwks.json"

	cfg, err := config.Load(env(values))
	require.NoError(t, err)

	assert.Equal(t, []string{"a:9092", "b:9092"}, cfg.KafkaBrokers)
	assert.Equal(t, "custom-group", cfg.KafkaConsumerGroup)
	assert.Equal(t, "SCRAM-SHA-512", cfg.KafkaSASLMechanism)
	assert.True(t, cfg.KafkaTLSEnabled)
	assert.Equal(t, 9090, cfg.Port)
	assert.Equal(t, 45, int(cfg.ShutdownTimeout.Seconds()))
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, "/var/run/identity/jwks.json", cfg.IdentityJWKSPath)
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		mutate  func(map[string]string)
		wantMsg string
	}{
		{
			name:    "missing database url",
			mutate:  func(v map[string]string) { delete(v, "DATABASE_URL") },
			wantMsg: "DATABASE_URL is required",
		},
		{
			name:    "missing kafka brokers",
			mutate:  func(v map[string]string) { v["KAFKA_BROKERS"] = " " },
			wantMsg: "KAFKA_BROKERS is required",
		},
		{
			name: "missing identity keys",
			mutate: func(v map[string]string) {
				delete(v, "IDENTITY_JWKS")
			},
			wantMsg: "IDENTITY_JWKS_PATH or IDENTITY_JWKS is required",
		},
		{
			name:    "missing issuer",
			mutate:  func(v map[string]string) { delete(v, "IDENTITY_ISSUER") },
			wantMsg: "IDENTITY_ISSUER is required",
		},
		{
			name:    "missing audience",
			mutate:  func(v map[string]string) { delete(v, "IDENTITY_AUDIENCE") },
			wantMsg: "IDENTITY_AUDIENCE is required",
		},
		{
			name:    "missing service token",
			mutate:  func(v map[string]string) { delete(v, "INTERNAL_SERVICE_TOKEN") },
			wantMsg: "INTERNAL_SERVICE_TOKEN is required",
		},
		{
			name:    "non numeric port",
			mutate:  func(v map[string]string) { v["PORT"] = "eighty" },
			wantMsg: "PORT must be an integer",
		},
		{
			name:    "port out of range",
			mutate:  func(v map[string]string) { v["PORT"] = "70000" },
			wantMsg: "PORT must be between 1 and 65535",
		},
		{
			name:    "non numeric shutdown timeout",
			mutate:  func(v map[string]string) { v["SHUTDOWN_TIMEOUT_SECONDS"] = "soon" },
			wantMsg: "SHUTDOWN_TIMEOUT_SECONDS must be an integer",
		},
		{
			name:    "zero shutdown timeout",
			mutate:  func(v map[string]string) { v["SHUTDOWN_TIMEOUT_SECONDS"] = "0" },
			wantMsg: "SHUTDOWN_TIMEOUT_SECONDS must be between 1 and 600",
		},
		{
			name:    "invalid tls flag",
			mutate:  func(v map[string]string) { v["KAFKA_TLS_ENABLED"] = "maybe" },
			wantMsg: "KAFKA_TLS_ENABLED must be a boolean",
		},
		{
			name: "sasl without credentials",
			mutate: func(v map[string]string) {
				v["KAFKA_SASL_MECHANISM"] = "PLAIN"
			},
			wantMsg: "KAFKA_SASL_USERNAME and KAFKA_SASL_PASSWORD are required",
		},
		{
			name:    "invalid log level",
			mutate:  func(v map[string]string) { v["LOG_LEVEL"] = "verbose" },
			wantMsg: "LOG_LEVEL must be one of debug, info, warn, error",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			values := requiredEnv()
			tc.mutate(values)

			cfg, err := config.Load(env(values))
			require.Error(t, err)
			assert.Nil(t, cfg)
			assert.Contains(t, err.Error(), tc.wantMsg)

			var validationErr *config.ValidationError
			require.ErrorAs(t, err, &validationErr)
			assert.NotEmpty(t, validationErr.Problems)
		})
	}
}

func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	t.Parallel()

	_, err := config.Load(env(map[string]string{}))
	require.Error(t, err)

	var validationErr *config.ValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.GreaterOrEqual(t, len(validationErr.Problems), 6)
	assert.True(t, strings.Contains(err.Error(), "DATABASE_URL is required"))
}

func TestLoadRejectsNilGetenv(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(nil)
	require.Error(t, err)
	assert.Nil(t, cfg)
}
