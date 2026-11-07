package service

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"community_service/internal/models"
)

// Default channels created together with every community.
const (
	DefaultTextChannelName  = "general"
	DefaultVoiceChannelName = "General"
)

// CommunityService owns the community lifecycle: creation with its default
// channels, configuration, and the caller's view of a community.
type CommunityService struct {
	deps Deps
}

// NewCommunityService builds the community service.
func NewCommunityService(deps Deps) *CommunityService {
	return &CommunityService{deps: deps.withDefaults()}
}

// Create creates a community owned by the caller, with the default `general`
// text channel, the default `General` voice channel and the owner's active
// membership, all in one transaction.
func (s *CommunityService) Create(ctx context.Context, userID uuid.UUID, traceID, correlationID string, request *models.CreateCommunityRequest) (*models.CommunityDetailResponse, error) {
	now := s.deps.Clock.Now()

	community := &models.Community{
		ID:          uuid.New(),
		Name:        strings.TrimSpace(request.Name),
		Description: request.Description,
		OwnerUserID: userID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	textChannel := &models.Channel{
		ID:             uuid.New(),
		CommunityID:    community.ID,
		Kind:           models.ChannelKindText,
		Name:           DefaultTextChannelName,
		Position:       0,
		ChannelVersion: 1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	voiceCapacity := models.DefaultVoiceCapacity
	voiceChannel := &models.Channel{
		ID:             uuid.New(),
		CommunityID:    community.ID,
		Kind:           models.ChannelKindVoice,
		Name:           DefaultVoiceChannelName,
		Position:       1,
		Capacity:       &voiceCapacity,
		ChannelVersion: 1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	ownerMembership := &models.Membership{
		ID:            uuid.New(),
		CommunityID:   community.ID,
		UserID:        userID,
		Status:        models.StatusActive,
		JoinedAt:      now,
		MemberVersion: 1,
	}

	channels := []*models.Channel{textChannel, voiceChannel}
	if err := s.deps.Communities.CreateCommunity(ctx, community, ownerMembership, channels); err != nil {
		return nil, err
	}

	for _, channel := range channels {
		s.deps.publishChannelChanged(ctx, channelEnvelope(traceID, correlationID, now), models.ChannelChangeCreated, channel)
	}

	s.deps.Metrics.RecordCommunityCreated()

	return &models.CommunityDetailResponse{
		Community: community,
		Channels:  channels,
		Caller: models.CallerContext{
			IsMember:    true,
			IsOwner:     true,
			RoleIDs:     []uuid.UUID{},
			Permissions: EffectivePermissions(true, nil),
		},
	}, nil
}

// Get loads a community with its channels and the caller's roles and effective
// permissions.
func (s *CommunityService) Get(ctx context.Context, userID, communityID uuid.UUID) (*models.CommunityDetailResponse, error) {
	resolved, err := s.deps.resolve(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}
	if err := s.deps.requireMember(userID, resolved); err != nil {
		return nil, err
	}

	channels, err := s.deps.Channels.ListChannels(ctx, communityID)
	if err != nil {
		return nil, err
	}

	isOwner := resolved.isOwner(userID)
	return &models.CommunityDetailResponse{
		Community: resolved.Community,
		Channels:  channels,
		Caller: models.CallerContext{
			IsMember:    resolved.isMember(),
			IsOwner:     isOwner,
			RoleIDs:     RoleIDs(resolved.Membership),
			Permissions: models.PermissionsOrEmpty(EffectivePermissions(isOwner, resolved.Roles)),
		},
	}, nil
}

// Update applies a partial community update; it requires `manage_community`.
func (s *CommunityService) Update(ctx context.Context, userID, communityID uuid.UUID, request *models.UpdateCommunityRequest) (*models.Community, error) {
	resolved, err := s.deps.resolve(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}
	if err := s.deps.requirePermission(userID, resolved, models.PermissionManageCommunity); err != nil {
		return nil, err
	}

	patch := models.CommunityPatch{
		Description: request.Description,
		IconURL:     request.IconURL,
	}
	if request.Name != nil {
		trimmed := strings.TrimSpace(*request.Name)
		patch.Name = &trimmed
	}

	return s.deps.Communities.UpdateCommunity(ctx, communityID, patch, s.deps.Clock.Now())
}

// ListMine returns the communities where the caller has an active membership.
func (s *CommunityService) ListMine(ctx context.Context, userID uuid.UUID, page models.PageQuery) (*models.CommunityListResponse, error) {
	communities, err := s.deps.Communities.ListCommunitiesByMember(ctx, userID, page.Limit+1, page.Offset)
	if err != nil {
		return nil, err
	}

	var nextCursor *string
	if len(communities) > page.Limit {
		communities = communities[:page.Limit]
		cursor := models.EncodeCursor(page.Offset + page.Limit)
		nextCursor = &cursor
	}

	return &models.CommunityListResponse{
		Communities: communities,
		NextCursor:  nextCursor,
	}, nil
}

// RoleIDs returns the member's role ids, or an empty slice when the caller has no
// membership.
func RoleIDs(membership *models.Membership) []uuid.UUID {
	if membership == nil {
		return []uuid.UUID{}
	}
	return models.RoleIDsOrEmpty(membership.RoleIDs)
}
