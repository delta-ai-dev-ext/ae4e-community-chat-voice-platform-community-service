package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"community_service/internal/models"
	"community_service/internal/obs"
)

// CreateInvite handles POST /communities/{community_id}/invites.
func (h *Handler) CreateInvite(ctx *gin.Context) {
	communityID, appErr := pathUUID(ctx, "community_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	var request models.CreateInviteRequest
	if appErr := bindAndValidate(ctx, &request); appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	userID, ok := callerID(ctx)
	if !ok {
		h.respondError(ctx, models.ErrUnauthorized)
		return
	}

	// The schema defines no idempotency store, so the client-supplied key is
	// recorded for tracing only; invite creation stays additive.
	if key := idempotencyKey(ctx); key != "" {
		h.Logger.Info("idempotency key supplied for invite creation",
			append(obs.TraceFields(traceIDFrom(ctx), requestIDFrom(ctx), "create_invite"), zap.String("idempotency_key", key))...)
	}

	invite, err := h.Invites.Create(ctx.Request.Context(), userID, communityID, &request)
	if err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.JSON(http.StatusCreated, invite)
}

// RevokeInvite handles DELETE /communities/{community_id}/invites/{code}.
func (h *Handler) RevokeInvite(ctx *gin.Context) {
	communityID, appErr := pathUUID(ctx, "community_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	code := ctx.Param("code")
	if code == "" {
		h.respondError(ctx, models.WithDetail(models.ErrInvalidRequest, "code is required"))
		return
	}

	userID, ok := callerID(ctx)
	if !ok {
		h.respondError(ctx, models.ErrUnauthorized)
		return
	}

	if err := h.Invites.Revoke(ctx.Request.Context(), userID, communityID, code); err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.Status(http.StatusNoContent)
}

// PreviewInvite handles GET /invites/{code}.
func (h *Handler) PreviewInvite(ctx *gin.Context) {
	code := ctx.Param("code")
	if code == "" {
		h.respondError(ctx, models.WithDetail(models.ErrInvalidRequest, "code is required"))
		return
	}

	preview, err := h.Invites.Preview(ctx.Request.Context(), code)
	if err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, preview)
}

// AcceptInvite handles POST /invites/{code}/accept.
func (h *Handler) AcceptInvite(ctx *gin.Context) {
	code := ctx.Param("code")
	if code == "" {
		h.respondError(ctx, models.WithDetail(models.ErrInvalidRequest, "code is required"))
		return
	}

	userID, ok := callerID(ctx)
	if !ok {
		h.respondError(ctx, models.ErrUnauthorized)
		return
	}

	if key := idempotencyKey(ctx); key != "" {
		h.Logger.Info("idempotency key supplied for invite accept",
			append(obs.TraceFields(traceIDFrom(ctx), requestIDFrom(ctx), "accept_invite"), zap.String("idempotency_key", key))...)
	}

	membership, err := h.Invites.Accept(ctx.Request.Context(), userID, code, traceIDFrom(ctx), requestIDFrom(ctx))
	if err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.JSON(http.StatusCreated, membership)
}
