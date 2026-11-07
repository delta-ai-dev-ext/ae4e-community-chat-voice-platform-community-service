// Package handler is the gin HTTP layer: it binds and validates request bodies,
// delegates to the domain services, and maps typed service errors onto the
// platform error shape. Handlers stay thin — no SQL, no business rules.
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"community_service/internal/models"
	"community_service/internal/obs"
	"community_service/internal/service"
)

// Content types of the documented responses.
const (
	contentTypeJSON    = "application/json"
	contentTypeProblem = "application/problem+json"
)

// ReadinessChecker reports whether this instance can serve traffic. The
// composition root implements it from the PostgreSQL pool, the Kafka clients and
// the draining flag.
type ReadinessChecker interface {
	Ready(ctx context.Context) error
}

// Handler bundles the HTTP dependencies. Every field is injected by the
// composition root so the layer is unit-testable with mocks.
type Handler struct {
	Communities   *service.CommunityService
	Channels      *service.ChannelService
	Invites       *service.InviteService
	Memberships   *service.MembershipService
	Roles         *service.RoleService
	Authorization *service.AuthorizationService
	Tokens        *TokenVerifier
	InternalToken string
	Readiness     ReadinessChecker
	Logger        *zap.Logger
	Metrics       *obs.Metrics
}

// validatorRequest is a request DTO that knows its own business validation.
type validatorRequest interface {
	Validate() *models.Error
}

// bindAndValidate decodes the JSON body and applies the DTO's declarative and
// business validation, returning the documented error codes: 400 for a body that
// cannot be parsed, 422 for a field or business rule that fails.
func bindAndValidate[T validatorRequest](ctx *gin.Context, request T) *models.Error {
	if err := ctx.ShouldBindJSON(request); err != nil {
		return models.Wrap(models.ErrInvalidRequest, err)
	}
	if err := models.ValidateStruct(request); err != nil {
		return models.WithDetail(models.ErrValidationFailed, err.Error())
	}
	if appErr := request.Validate(); appErr != nil {
		return appErr
	}
	return nil
}

// bindStrict decodes a body that rejects unknown properties (the internal
// contracts set `additionalProperties: false`).
func bindStrict[T validatorRequest](ctx *gin.Context, request T) *models.Error {
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(request); err != nil {
		return models.Wrap(models.ErrInvalidRequest, err)
	}
	if appErr := request.Validate(); appErr != nil {
		return appErr
	}
	return nil
}

// pathUUID parses a UUID path parameter.
func pathUUID(ctx *gin.Context, name string) (uuid.UUID, *models.Error) {
	value, err := uuid.Parse(ctx.Param(name))
	if err != nil {
		return uuid.Nil, models.WithDetail(models.ErrInvalidRequest, fmt.Sprintf("%s must be a UUID", name))
	}
	return value, nil
}

// pageQuery reads the documented cursor/limit pagination parameters.
func pageQuery(ctx *gin.Context) (models.PageQuery, *models.Error) {
	var limit *int
	if raw := ctx.Query("limit"); raw != "" {
		parsed, err := parseInt(raw)
		if err != nil {
			return models.PageQuery{}, models.WithDetail(models.ErrInvalidRequest, "limit must be an integer")
		}
		limit = &parsed
	}
	return models.DecodePageQuery(ctx.Query("cursor"), limit)
}

// respondError maps a typed service error onto the platform error shape.
func (h *Handler) respondError(ctx *gin.Context, err error) {
	appErr, ok := models.AsError(err)
	if !ok {
		h.Logger.Error("request failed unexpectedly",
			append(obs.TraceFields(traceIDFrom(ctx), requestIDFrom(ctx), ctx.FullPath()), zap.Error(err))...)
		appErr = models.ErrInternal
	}

	if appErr.HTTPStatus() >= http.StatusInternalServerError {
		h.Logger.Error("request failed",
			append(obs.TraceFields(traceIDFrom(ctx), requestIDFrom(ctx), ctx.FullPath()),
				zap.String("error_code", string(appErr.Code)), zap.Error(appErr))...)
	} else {
		h.Logger.Debug("request rejected",
			append(obs.TraceFields(traceIDFrom(ctx), requestIDFrom(ctx), ctx.FullPath()),
				zap.String("error_code", string(appErr.Code)))...)
	}

	ctx.AbortWithStatusJSON(appErr.HTTPStatus(), models.ErrorResponse{
		Error:  string(appErr.Code),
		Detail: appErr.Detail,
	})
}

// respondInternalError maps an error of an internal contract onto
// `application/problem+json`, as those contracts specify.
func (h *Handler) respondInternalError(ctx *gin.Context, err error) {
	ctx.Header("Content-Type", contentTypeProblem)
	h.respondError(ctx, err)
}

// callerID returns the authenticated caller; the auth middleware guarantees it.
func callerID(ctx *gin.Context) (uuid.UUID, bool) {
	value, ok := ctx.Get(contextUserID)
	if !ok {
		return uuid.Nil, false
	}
	userID, ok := value.(uuid.UUID)
	return userID, ok
}

// idempotencyKey reads the optional Idempotency-Key header. The schema defines no
// idempotency store, so the key is only validated and logged for tracing.
func idempotencyKey(ctx *gin.Context) string {
	key := ctx.GetHeader("Idempotency-Key")
	if len(key) > 255 {
		return ""
	}
	return key
}

// parseInt decodes a decimal query parameter.
func parseInt(raw string) (int, error) {
	return strconv.Atoi(raw)
}
