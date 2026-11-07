package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"community_service/internal/models"
)

// ListRoles handles GET /communities/{community_id}/roles.
func (h *Handler) ListRoles(ctx *gin.Context) {
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

	response, err := h.Roles.List(ctx.Request.Context(), userID, communityID)
	if err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, response)
}

// CreateRole handles POST /communities/{community_id}/roles.
func (h *Handler) CreateRole(ctx *gin.Context) {
	communityID, appErr := pathUUID(ctx, "community_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	var request models.CreateRoleRequest
	if appErr := bindAndValidate(ctx, &request); appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	userID, ok := callerID(ctx)
	if !ok {
		h.respondError(ctx, models.ErrUnauthorized)
		return
	}

	role, err := h.Roles.Create(ctx.Request.Context(), userID, communityID, &request)
	if err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.JSON(http.StatusCreated, role)
}

// UpdateRole handles PATCH /communities/{community_id}/roles/{role_id}.
func (h *Handler) UpdateRole(ctx *gin.Context) {
	communityID, appErr := pathUUID(ctx, "community_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}
	roleID, appErr := pathUUID(ctx, "role_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	var request models.UpdateRoleRequest
	if appErr := bindAndValidate(ctx, &request); appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	userID, ok := callerID(ctx)
	if !ok {
		h.respondError(ctx, models.ErrUnauthorized)
		return
	}

	role, err := h.Roles.Update(ctx.Request.Context(), userID, communityID, roleID, traceIDFrom(ctx), requestIDFrom(ctx), &request)
	if err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, role)
}

// DeleteRole handles DELETE /communities/{community_id}/roles/{role_id}.
func (h *Handler) DeleteRole(ctx *gin.Context) {
	communityID, appErr := pathUUID(ctx, "community_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}
	roleID, appErr := pathUUID(ctx, "role_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	userID, ok := callerID(ctx)
	if !ok {
		h.respondError(ctx, models.ErrUnauthorized)
		return
	}

	if err := h.Roles.Delete(ctx.Request.Context(), userID, communityID, roleID, traceIDFrom(ctx), requestIDFrom(ctx)); err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.Status(http.StatusNoContent)
}

// SetMemberRoles handles PUT
// /communities/{community_id}/members/{user_id}/roles.
func (h *Handler) SetMemberRoles(ctx *gin.Context) {
	communityID, appErr := pathUUID(ctx, "community_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}
	targetUserID, appErr := pathUUID(ctx, "user_id")
	if appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	var request models.SetMemberRolesRequest
	if appErr := bindAndValidate(ctx, &request); appErr != nil {
		h.respondError(ctx, appErr)
		return
	}

	actorID, ok := callerID(ctx)
	if !ok {
		h.respondError(ctx, models.ErrUnauthorized)
		return
	}

	member, err := h.Roles.SetMemberRoles(ctx.Request.Context(), actorID, communityID, targetUserID, traceIDFrom(ctx), requestIDFrom(ctx), &request)
	if err != nil {
		h.respondError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, member)
}
