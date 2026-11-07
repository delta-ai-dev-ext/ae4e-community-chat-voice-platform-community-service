package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"community_service/internal/events"
	"community_service/internal/models"
	"community_service/internal/obs"
)

// Context keys of request-scoped values.
const (
	contextTraceID   = "trace_id"
	contextRequestID = "request_id"
	contextUserID    = "user_id"
	contextSessionID = "session_id"
)

// Trace propagation headers.
const (
	traceParentHeader = "traceparent"
	traceIDHeader     = "X-Trace-Id"
	requestIDHeader   = "X-Request-Id"
)

// Middleware groups the cross-cutting HTTP concerns: tracing, RED metrics, panic
// recovery and the two authentication schemes of the public and internal surface.
type Middleware struct {
	Tokens        *TokenVerifier
	InternalToken string
	Logger        *zap.Logger
	Metrics       *obs.Metrics
}

// Trace assigns a trace id to the request — propagated from the inbound
// `traceparent`/`X-Trace-Id` header, minted when absent — and echoes it back on
// the response so clients can correlate logs.
func (m *Middleware) Trace() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		traceID := traceFromRequest(ctx)

		requestID := ctx.GetHeader(requestIDHeader)
		if requestID == "" {
			requestID = uuid.NewString()
		}

		ctx.Set(contextTraceID, traceID)
		ctx.Set(contextRequestID, requestID)
		ctx.Writer.Header().Set(traceIDHeader, traceID)
		ctx.Next()
	}
}

// Observe records the RED metrics and one structured access log line per request.
func (m *Middleware) Observe() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		started := time.Now()
		ctx.Next()

		route := ctx.FullPath()
		status := ctx.Writer.Status()

		if m.Metrics != nil {
			m.Metrics.RecordRequest(route, ctx.Request.Method, status, time.Since(started))
		}
		if m.Logger != nil {
			m.Logger.Info("http request",
				append(obs.TraceFields(traceIDFrom(ctx), requestIDFrom(ctx), route),
					zap.String("method", ctx.Request.Method),
					zap.Int("status", status),
					obs.DurationMillis(time.Since(started)))...)
		}
	}
}

// Recovery turns a panic into the platform error shape instead of a bare 500.
func (m *Middleware) Recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(ctx *gin.Context, recovered any) {
		if m.Logger != nil {
			m.Logger.Error("panic recovered",
				append(obs.TraceFields(traceIDFrom(ctx), requestIDFrom(ctx), ctx.FullPath()), zap.Any("panic", recovered))...)
		}
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:  string(models.CodeInternal),
			Detail: models.ErrInternal.Detail,
		})
	})
}

// Authenticate verifies the identity access token locally and stores the caller.
func (m *Middleware) Authenticate() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if m.Tokens == nil {
			abortWithError(ctx, models.ErrUnauthorized)
			return
		}

		identity, err := m.Tokens.Verify(bearerToken(ctx))
		if err != nil {
			if m.Logger != nil {
				m.Logger.Debug("access token rejected",
					append(obs.TraceFields(traceIDFrom(ctx), requestIDFrom(ctx), ctx.FullPath()), zap.Error(err))...)
			}
			abortWithError(ctx, models.ErrUnauthorized)
			return
		}

		ctx.Set(contextUserID, identity.UserID)
		ctx.Set(contextSessionID, identity.SessionID)
		ctx.Next()
	}
}

// RequireServiceToken enforces the per-edge service token on /internal/* routes
// with a constant-time comparison, so a wrong token cannot be timing-probed.
func (m *Middleware) RequireServiceToken() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		provided := bearerToken(ctx)
		expected := m.InternalToken

		if expected == "" || len(provided) != len(expected) || subtleCompare(provided, expected) != 1 {
			ctx.Header("Content-Type", contentTypeProblem)
			abortWithError(ctx, models.ErrUnauthorized)
			return
		}
		ctx.Next()
	}
}

// abortWithError writes the platform error shape and stops the middleware chain.
func abortWithError(ctx *gin.Context, appErr *models.Error) {
	ctx.AbortWithStatusJSON(appErr.HTTPStatus(), models.ErrorResponse{
		Error:  string(appErr.Code),
		Detail: appErr.Detail,
	})
}

// bearerToken extracts the bearer credential from the Authorization header.
func bearerToken(ctx *gin.Context) string {
	header := strings.TrimSpace(ctx.GetHeader("Authorization"))
	scheme, value, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(value)
}

// traceFromRequest reads the trace id from the W3C traceparent header or the
// legacy X-Trace-Id header and mints one when neither carries a valid value, so
// trace_id is never empty.
func traceFromRequest(ctx *gin.Context) string {
	if traceParent := ctx.GetHeader(traceParentHeader); traceParent != "" {
		parts := strings.Split(traceParent, "-")
		if len(parts) >= 3 && events.ValidTraceID(parts[1]) {
			return parts[1]
		}
	}
	if candidate := strings.ToLower(strings.TrimSpace(ctx.GetHeader(traceIDHeader))); events.ValidTraceID(candidate) {
		return candidate
	}
	return events.NewTraceID()
}

// traceIDFrom returns the request trace id.
func traceIDFrom(ctx *gin.Context) string {
	if value, ok := ctx.Get(contextTraceID); ok {
		if traceID, ok := value.(string); ok {
			return traceID
		}
	}
	return ""
}

// requestIDFrom returns the request id, which also serves as the correlation id
// of every event the request emits.
func requestIDFrom(ctx *gin.Context) string {
	if value, ok := ctx.Get(contextRequestID); ok {
		if requestID, ok := value.(string); ok {
			return requestID
		}
	}
	return ""
}

// sessionIDFrom returns the identity session id of the caller, if present.
func sessionIDFrom(ctx *gin.Context) string {
	if value, ok := ctx.Get(contextSessionID); ok {
		if sessionID, ok := value.(string); ok {
			return sessionID
		}
	}
	return ""
}
