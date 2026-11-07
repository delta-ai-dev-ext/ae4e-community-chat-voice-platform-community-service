package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"community_service/internal/models"
)

// AuthorizationService serves the two internal oracles: the moderator-action
// check (API-001) for moderation_service and the voice access check (API-002)
// for voice_service. Both are read-only, answer in-band with allowed/denial_reason
// and never turn a denial into an error status.
type AuthorizationService struct {
	deps Deps
}

// NewAuthorizationService builds the authorization oracle service.
func NewAuthorizationService(deps Deps) *AuthorizationService {
	return &AuthorizationService{deps: deps.withDefaults()}
}

// CheckAuthorization implements API-001.
func (s *AuthorizationService) CheckAuthorization(ctx context.Context, communityID uuid.UUID, request *models.AuthorizationCheckRequest) (*models.AuthorizationCheckResponse, error) {
	community, err := s.deps.Communities.GetCommunity(ctx, communityID)
	if err != nil {
		return nil, err
	}

	now := s.deps.Clock.Now()

	actorMembership, err := s.membershipOrNil(ctx, communityID, request.ActorUserID)
	if err != nil {
		return nil, err
	}

	actorRoles, err := s.deps.roleSet(ctx, communityID, roleIDsOf(actorMembership))
	if err != nil {
		return nil, err
	}

	actorIsOwner := community.OwnerUserID == request.ActorUserID
	actorIsMember := actorMembership != nil && actorMembership.IsActive()

	input := AuthorizationInput{
		ActorUserID:   request.ActorUserID,
		ActorIsMember: actorIsMember,
		ActorIsOwner:  actorIsOwner,
		ActorRoles:    actorRoles,
		Permission:    request.Permission,
	}

	var targetResponse *models.AuthorizationTarget
	if request.TargetUserID != nil {
		targetID := *request.TargetUserID

		targetMembership, err := s.membershipOrNil(ctx, communityID, targetID)
		if err != nil {
			return nil, err
		}

		targetRoles, err := s.deps.roleSet(ctx, communityID, roleIDsOf(targetMembership))
		if err != nil {
			return nil, err
		}

		ban, err := s.deps.Memberships.GetActiveBan(ctx, communityID, targetID)
		if err != nil {
			return nil, err
		}

		targetIsOwner := community.OwnerUserID == targetID
		isBanned := ban != nil || (targetMembership != nil && targetMembership.IsBanned)

		input.Target = &AuthorizationTarget{
			UserID:   targetID,
			IsMember: targetMembership != nil && targetMembership.IsActive(),
			IsBanned: isBanned,
			IsOwner:  targetIsOwner,
			Roles:    targetRoles,
		}

		var mutedUntil *time.Time
		if targetMembership != nil && models.MutedNow(targetMembership, now) {
			mutedUntil = targetMembership.MutedUntil
		}

		targetResponse = &models.AuthorizationTarget{
			UserID:           targetID,
			IsMember:         input.Target.IsMember,
			IsBanned:         isBanned,
			IsOwner:          targetIsOwner,
			OutrankedByActor: Outranks(actorIsOwner, actorRoles, targetIsOwner, targetRoles),
			MutedUntil:       mutedUntil,
		}
	}

	allowed, denial := DecideAuthorization(input)

	return &models.AuthorizationCheckResponse{
		CommunityID:      communityID,
		ActorUserID:      request.ActorUserID,
		Permission:       request.Permission,
		Allowed:          allowed,
		DenialReason:     denial,
		ActorIsMember:    actorIsMember,
		ActorIsOwner:     actorIsOwner,
		ActorPermissions: models.PermissionsOrEmpty(EffectivePermissions(actorIsOwner, actorRoles)),
		Target:           targetResponse,
	}, nil
}

// CheckVoiceAccess implements API-002. Mute never denies access: a muted member
// is allowed in with a future muted_until (listen-only join).
func (s *AuthorizationService) CheckVoiceAccess(ctx context.Context, communityID, channelID uuid.UUID, request *models.VoiceAccessCheckRequest) (*models.VoiceAccessCheckResponse, error) {
	if _, err := s.deps.Communities.GetCommunity(ctx, communityID); err != nil {
		return nil, err
	}

	now := s.deps.Clock.Now()

	response := &models.VoiceAccessCheckResponse{
		CommunityID:   communityID,
		UserID:        request.UserID,
		VoiceChannels: []models.VoiceChannelSummary{},
	}

	ban, err := s.deps.Memberships.GetActiveBan(ctx, communityID, request.UserID)
	if err != nil {
		return nil, err
	}
	if ban != nil {
		response.DenialReason = pointer(models.DenialBanned)
		return response, nil
	}

	membership, err := s.membershipOrNil(ctx, communityID, request.UserID)
	if err != nil {
		return nil, err
	}
	if membership == nil || !membership.IsActive() {
		response.DenialReason = pointer(models.DenialNotAMember)
		return response, nil
	}

	channel, err := s.deps.Channels.GetChannel(ctx, communityID, channelID)
	if err != nil {
		if errors.Is(err, models.ErrChannelNotFound) {
			response.DenialReason = pointer(models.DenialChannelNotFound)
			return response, nil
		}
		return nil, err
	}
	if channel.Kind != models.ChannelKindVoice {
		response.DenialReason = pointer(models.DenialNotAVoiceChannel)
		return response, nil
	}

	response.Allowed = true
	if models.MutedNow(membership, now) {
		response.MutedUntil = membership.MutedUntil
	}

	capacity := models.DefaultVoiceCapacity
	if channel.Capacity != nil {
		capacity = *channel.Capacity
	}
	response.Channel = &models.VoiceChannelSummary{
		ChannelID: channel.ID,
		Name:      channel.Name,
		Capacity:  capacity,
	}

	voiceChannels, err := s.deps.Channels.ListVoiceChannels(ctx, communityID)
	if err != nil {
		return nil, err
	}
	for _, voiceChannel := range voiceChannels {
		voiceCapacity := models.DefaultVoiceCapacity
		if voiceChannel.Capacity != nil {
			voiceCapacity = *voiceChannel.Capacity
		}
		response.VoiceChannels = append(response.VoiceChannels, models.VoiceChannelSummary{
			ChannelID: voiceChannel.ID,
			Name:      voiceChannel.Name,
			Capacity:  voiceCapacity,
		})
	}

	return response, nil
}

// membershipOrNil loads a membership, treating "never joined" as a nil result.
func (s *AuthorizationService) membershipOrNil(ctx context.Context, communityID, userID uuid.UUID) (*models.Membership, error) {
	membership, err := s.deps.Memberships.GetMembership(ctx, communityID, userID)
	if err != nil {
		if errors.Is(err, models.ErrMembershipNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return membership, nil
}

func roleIDsOf(membership *models.Membership) []uuid.UUID {
	if membership == nil {
		return nil
	}
	return membership.RoleIDs
}
