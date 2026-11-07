package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"community_service/internal/models"
	"community_service/internal/obs"
)

// readinessTimeout bounds the readiness dependency checks so a probe never hangs.
const readinessTimeout = 2 * time.Second

// Healthz handles GET /healthz. Liveness reports only that the process is
// responsive: it must never fail on a PostgreSQL or Kafka hiccup.
func (h *Handler) Healthz(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, models.HealthStatus{Status: "ok"})
}

// Readyz handles GET /readyz. Readiness fails while shutting down and when
// PostgreSQL or Kafka is unreachable.
func (h *Handler) Readyz(ctx *gin.Context) {
	if h.Readiness == nil {
		ctx.JSON(http.StatusOK, models.HealthStatus{Status: "ok"})
		return
	}

	checkCtx, cancel := context.WithTimeout(ctx.Request.Context(), readinessTimeout)
	defer cancel()

	if err := h.Readiness.Ready(checkCtx); err != nil {
		h.Logger.Warn("readiness check failed",
			append(obs.TraceFields(traceIDFrom(ctx), requestIDFrom(ctx), "readyz"), zap.Error(err))...)
		ctx.JSON(http.StatusServiceUnavailable, models.ErrorResponse{
			Error:  string(models.CodeNotReady),
			Detail: models.ErrNotReady.Detail,
		})
		return
	}

	ctx.JSON(http.StatusOK, models.HealthStatus{Status: "ok"})
}

// CheckAuthorization handles POST
// /internal/communities/{community_id}/authorization-checks (API-001). A denial
// is a 200 with `allowed: false`, never an error status.
func (h *Handler) CheckAuthorization(ctx *gin.Context) {
	communityID, appErr := pathUUID(ctx, "community_id")
	if appErr != nil {
		h.respondInternalError(ctx, appErr)
		return
	}

	var request models.AuthorizationCheckRequest
	if appErr := bindStrict(ctx, &request); appErr != nil {
		h.respondInternalError(ctx, appErr)
		return
	}

	response, err := h.Authorization.CheckAuthorization(ctx.Request.Context(), communityID, &request)
	if err != nil {
		h.respondInternalError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, response)
}

// CheckVoiceAccess handles POST
// /internal/communities/{community_id}/channels/{channel_id}/voice-access-checks
// (API-002). A denial is a 200 with `allowed: false`, never an error status.
func (h *Handler) CheckVoiceAccess(ctx *gin.Context) {
	communityID, appErr := pathUUID(ctx, "community_id")
	if appErr != nil {
		h.respondInternalError(ctx, appErr)
		return
	}
	channelID, appErr := pathUUID(ctx, "channel_id")
	if appErr != nil {
		h.respondInternalError(ctx, appErr)
		return
	}

	var request models.VoiceAccessCheckRequest
	if appErr := bindStrict(ctx, &request); appErr != nil {
		h.respondInternalError(ctx, appErr)
		return
	}

	response, err := h.Authorization.CheckVoiceAccess(ctx.Request.Context(), communityID, channelID, &request)
	if err != nil {
		h.respondInternalError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, response)
}
