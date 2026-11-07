package handler

import (
	"crypto/subtle"

	"github.com/gin-gonic/gin"
)

// NewRouter builds the gin engine with every documented route of the service.
func (h *Handler) NewRouter(middleware *Middleware) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	engine := gin.New()
	engine.Use(middleware.Recovery(), middleware.Trace(), middleware.Observe())

	engine.GET("/healthz", h.Healthz)
	engine.GET("/readyz", h.Readyz)
	engine.GET("/metrics", gin.WrapH(h.Metrics.Handler()))

	// Internal oracle surface: per-edge service token, never a user token.
	internal := engine.Group("/internal", middleware.RequireServiceToken())
	internal.POST("/communities/:community_id/authorization-checks", h.CheckAuthorization)
	internal.POST("/communities/:community_id/channels/:channel_id/voice-access-checks", h.CheckVoiceAccess)

	// Public surface: identity access token verified locally.
	public := engine.Group("", middleware.Authenticate())
	public.POST("/communities", h.CreateCommunity)
	public.GET("/communities/mine", h.ListMyCommunities)
	public.GET("/communities/:community_id", h.GetCommunity)
	public.PATCH("/communities/:community_id", h.UpdateCommunity)

	public.POST("/communities/:community_id/channels", h.CreateChannel)
	public.PATCH("/communities/:community_id/channels/:channel_id", h.UpdateChannel)
	public.DELETE("/communities/:community_id/channels/:channel_id", h.DeleteChannel)

	public.POST("/communities/:community_id/invites", h.CreateInvite)
	public.DELETE("/communities/:community_id/invites/:code", h.RevokeInvite)
	public.GET("/invites/:code", h.PreviewInvite)
	public.POST("/invites/:code/accept", h.AcceptInvite)

	public.POST("/communities/:community_id/leave", h.LeaveCommunity)
	public.GET("/communities/:community_id/members", h.ListMembers)
	public.PUT("/communities/:community_id/members/:user_id/roles", h.SetMemberRoles)

	public.GET("/communities/:community_id/roles", h.ListRoles)
	public.POST("/communities/:community_id/roles", h.CreateRole)
	public.PATCH("/communities/:community_id/roles/:role_id", h.UpdateRole)
	public.DELETE("/communities/:community_id/roles/:role_id", h.DeleteRole)

	return engine
}

// subtleCompare compares two credentials in constant time.
func subtleCompare(a, b string) int {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b))
}
