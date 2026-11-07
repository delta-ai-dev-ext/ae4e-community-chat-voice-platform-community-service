package obs

import (
	"fmt"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLogger builds the structured JSON logger described by
// docs/engineering_guide.md: JSON output, service_name, trace_id and request_id
// fields, and no global state.
func NewLogger(level string) (*zap.Logger, error) {
	zapLevel, err := parseLevel(level)
	if err != nil {
		return nil, err
	}

	config := zap.NewProductionConfig()
	config.Level = zap.NewAtomicLevelAt(zapLevel)
	config.Encoding = "json"
	config.OutputPaths = []string{"stdout"}
	config.ErrorOutputPaths = []string{"stderr"}
	config.EncoderConfig.TimeKey = "timestamp"
	config.EncoderConfig.MessageKey = "message"
	config.EncoderConfig.LevelKey = "level"
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	config.InitialFields = map[string]any{"service_name": ServiceName}

	logger, err := config.Build()
	if err != nil {
		return nil, fmt.Errorf("obs: build logger: %w", err)
	}
	return logger, nil
}

// MissingTraceID is the placeholder used when a log line has no trace context,
// so the field is always present and greppable.
const MissingTraceID = "-"

// TraceFields returns the standard trace fields of a log line.
func TraceFields(traceID, requestID, operation string) []zap.Field {
	if traceID == "" {
		traceID = MissingTraceID
	}
	fields := []zap.Field{zap.String("trace_id", traceID)}
	if requestID != "" {
		fields = append(fields, zap.String("request_id", requestID))
	}
	if operation != "" {
		fields = append(fields, zap.String("operation", operation))
	}
	return fields
}

// DurationMillis renders a duration for structured logs.
func DurationMillis(duration time.Duration) zap.Field {
	return zap.Float64("duration_ms", float64(duration.Microseconds())/1000)
}

func parseLevel(level string) (zapcore.Level, error) {
	parsed, err := zapcore.ParseLevel(level)
	if err != nil {
		return zapcore.InfoLevel, fmt.Errorf("obs: invalid log level %q: %w", level, err)
	}
	return parsed, nil
}
