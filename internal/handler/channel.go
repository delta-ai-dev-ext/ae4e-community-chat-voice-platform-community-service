package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"community_service/internal/models"
)

// CreateChannel handles POST /communities/{community_id}/channels.
func (h *Handler) CreateChannel(ctx *gin.Context) {
	communityID, appErr := pathUUID(ctx, "community_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	var request models.CreateChannelRequest
	if appErr := bindAndValidate(ctx, &request); appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	userID, ok := callerID(ctx)
	if !ok {
		h.respondError(ctx, models.ErrUnauthorized)
		return
	}

	channel, err := h.Channels.Create(ctx.Request.Context(), userID, communityID, traceIDFrom(ctx), requestIDFrom(ctx), &request)
	if err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.JSON(http.StatusCreated, channel)
}

// UpdateChannel handles PATCH /communities/{community_id}/channels/{channel_id}.
func (h *Handler) UpdateChannel(ctx *gin.Context) {
	communityID, appErr := pathUUID(ctx, "community_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}
	channelID, appErr := pathUUID(ctx, "channel_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	var request models.UpdateChannelRequest
	if appErr := bindAndValidate(ctx, &request); appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	userID, ok := callerID(ctx)
	if !ok {
		h.respondError(ctx, models.ErrUnauthorized)
		return
	}

	channel, err := h.Channels.Update(ctx.Request.Context(), userID, communityID, channelID, traceIDFrom(ctx), requestIDFrom(ctx), &request)
	if err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, channel)
}

// DeleteChannel handles DELETE
// /communities/{community_id}/channels/{channel_id}.
func (h *Handler) DeleteChannel(ctx *gin.Context) {
	communityID, appErr := pathUUID(ctx, "community_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}
	channelID, appErr := pathUUID(ctx, "channel_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	userID, ok := callerID(ctx)
	if !ok {
		h.respondError(ctx, models.ErrUnauthorized)
		return
	}

	if err := h.Channels.Delete(ctx.Request.Context(), userID, communityID, channelID, traceIDFrom(ctx), requestIDFrom(ctx)); err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.Status(http.StatusNoContent)
}
