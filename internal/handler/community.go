package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"community_service/internal/models"
)

// CreateCommunity handles POST /communities.
func (h *Handler) CreateCommunity(ctx *gin.Context) {
	var request models.CreateCommunityRequest
	if appErr := bindAndValidate(ctx, &request); appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	userID, ok := callerID(ctx)
	if !ok {
		h.respondError(ctx, models.ErrUnauthorized)
		return
	}

	response, err := h.Communities.Create(ctx.Request.Context(), userID, traceIDFrom(ctx), requestIDFrom(ctx), &request)
	if err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.JSON(http.StatusCreated, response)
}

// ListMyCommunities handles GET /communities/mine.
func (h *Handler) ListMyCommunities(ctx *gin.Context) {
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

	response, err := h.Communities.ListMine(ctx.Request.Context(), userID, page)
	if err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, response)
}

// GetCommunity handles GET /communities/{community_id}.
func (h *Handler) GetCommunity(ctx *gin.Context) {
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

	response, err := h.Communities.Get(ctx.Request.Context(), userID, communityID)
	if err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, response)
}

// UpdateCommunity handles PATCH /communities/{community_id}.
func (h *Handler) UpdateCommunity(ctx *gin.Context) {
	communityID, appErr := pathUUID(ctx, "community_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	var request models.UpdateCommunityRequest
	if appErr := bindAndValidate(ctx, &request); appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	userID, ok := callerID(ctx)
	if !ok {
		h.respondError(ctx, models.ErrUnauthorized)
		return
	}

	community, err := h.Communities.Update(ctx.Request.Context(), userID, communityID, &request)
	if err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, community)
}
