package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"community_service/internal/models"
)

// LeaveCommunity handles POST /communities/{community_id}/leave.
func (h *Handler) LeaveCommunity(ctx *gin.Context) {
	communityID, appErr := pathUUID(ctx, "community_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	userID, ok := callerID(ctx)
	if !ok {
		h.respondError(ctx, models.ErrUnauthorized)
		return
	}

	if err := h.Memberships.Leave(ctx.Request.Context(), userID, communityID, traceIDFrom(ctx), requestIDFrom(ctx)); err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.Status(http.StatusNoContent)
}

// ListMembers handles GET /communities/{community_id}/members.
func (h *Handler) ListMembers(ctx *gin.Context) {
	communityID, appErr := pathUUID(ctx, "community_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	userID, ok := callerID(ctx)
	if !ok {
		h.respondError(ctx, models.ErrUnauthorized)
		return
	}

	page, appErr := pageQuery(ctx)
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	response, err := h.Memberships.ListMembers(ctx.Request.Context(), userID, communityID, page)
	if err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, response)
}
