package integration

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"community_service/internal/models"
)

// kickerRole grants kick_members without ownership.
func kickerRole(communityID uuid.UUID) *models.Role {
	return &models.Role{
		ID:          uuid.New(),
		CommunityID: communityID,
		Name:        "Kicker",
		Position:    20,
		Permissions: []models.Permission{models.PermissionKickMembers},
	}
}

func TestInternalAuthorizationCheckRequiresServiceToken(t *testing.T) {
	t.Parallel()

	rig := newRig(t)
	community, _, _ := seedCommunity(uuid.New())

	path := "/internal/communities/" + community.ID.String() + "/authorization-checks"
	body := map[string]any{"actor_user_id": rig.userID.String(), "permission": models.PermissionKickMembers}

	missing := rig.callWithoutAuth(t, http.MethodPost, path, body)
	assert.Equal(t, http.StatusUnauthorized, missing.Code)
	assert.Equal(t, "application/problem+json", missing.Header().Get("Content-Type"))
	assert.Equal(t, string(models.CodeUnauthorized), errorResponse(t, missing).Error)

	wrongToken := rig.call(t, http.MethodPost, path, body, withBearer("wrong-token"))
	assert.Equal(t, http.StatusUnauthorized, wrongToken.Code)

	userToken := rig.call(t, http.MethodPost, path, body)
	assert.Equal(t, http.StatusUnauthorized, userToken.Code, "a user access token must not open the internal surface")
}

func TestInternalAuthorizationCheckAllowsOwner(t *testing.T) {
	t.Parallel()

	rig := newRig(t)
	community, _, _ := seedCommunity(rig.userID)
	rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)

	response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/authorization-checks", map[string]any{
		"actor_user_id": rig.userID.String(),
		"permission":    models.PermissionKickMembers,
	})

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	body := decode[models.AuthorizationCheckResponse](t, response)
	assert.True(t, body.Allowed)
	assert.Nil(t, body.DenialReason)
	assert.True(t, body.ActorIsOwner)
	assert.True(t, body.ActorIsMember)
	assert.Len(t, body.ActorPermissions, 10)
	assert.Nil(t, body.Target)
}

func TestInternalAuthorizationCheckDenialsAreInBand(t *testing.T) {
	t.Parallel()

	t.Run("actor is not a member", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		actorID := uuid.New()
		rig.armAccess(community, nil, nil)

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/authorization-checks", map[string]any{
			"actor_user_id": actorID.String(),
			"permission":    models.PermissionKickMembers,
		})

		require.Equal(t, http.StatusOK, response.Code, "a denial is never an error status")
		body := decode[models.AuthorizationCheckResponse](t, response)
		assert.False(t, body.Allowed)
		require.NotNil(t, body.DenialReason)
		assert.Equal(t, models.DenialActorNotMember, *body.DenialReason)
		assert.False(t, body.ActorIsMember)
	})

	t.Run("actor lacks the permission", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		role := &models.Role{ID: uuid.New(), CommunityID: community.ID, Name: "Reader", Position: 60,
			Permissions: []models.Permission{models.PermissionReviewAppeals}}
		rig.armAccess(community, seedMembership(community.ID, rig.userID, role.ID), []*models.Role{role})

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/authorization-checks", map[string]any{
			"actor_user_id": rig.userID.String(),
			"permission":    models.PermissionKickMembers,
		})

		require.Equal(t, http.StatusOK, response.Code)
		body := decode[models.AuthorizationCheckResponse](t, response)
		assert.False(t, body.Allowed)
		require.NotNil(t, body.DenialReason)
		assert.Equal(t, models.DenialMissingPermission, *body.DenialReason)
		assert.Equal(t, []models.Permission{models.PermissionReviewAppeals}, body.ActorPermissions)
	})

	t.Run("target is the actor", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		role := kickerRole(community.ID)
		membership := seedMembership(community.ID, rig.userID, role.ID)
		rig.armAccess(community, membership, []*models.Role{role})
		rig.repo.On("GetActiveBan", anyContext(), community.ID, rig.userID).Return(nil, nil)

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/authorization-checks", map[string]any{
			"actor_user_id":  rig.userID.String(),
			"permission":     models.PermissionKickMembers,
			"target_user_id": rig.userID.String(),
		})

		require.Equal(t, http.StatusOK, response.Code)
		body := decode[models.AuthorizationCheckResponse](t, response)
		assert.False(t, body.Allowed)
		require.NotNil(t, body.DenialReason)
		assert.Equal(t, models.DenialTargetIsSelf, *body.DenialReason)
		require.NotNil(t, body.Target)
		assert.False(t, body.Target.OutrankedByActor)
	})

	t.Run("target is the owner", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		ownerID := uuid.New()
		community, _, _ := seedCommunity(ownerID)
		role := kickerRole(community.ID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID, role.ID), []*models.Role{role})

		target := seedMembership(community.ID, ownerID)
		rig.repo.On("GetMembership", anyContext(), community.ID, ownerID).Return(target, nil)
		rig.repo.On("GetActiveBan", anyContext(), community.ID, ownerID).Return(nil, nil)

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/authorization-checks", map[string]any{
			"actor_user_id":  rig.userID.String(),
			"permission":     models.PermissionKickMembers,
			"target_user_id": ownerID.String(),
		})

		require.Equal(t, http.StatusOK, response.Code)
		body := decode[models.AuthorizationCheckResponse](t, response)
		assert.False(t, body.Allowed)
		require.NotNil(t, body.DenialReason)
		assert.Equal(t, models.DenialTargetIsOwner, *body.DenialReason)
		require.NotNil(t, body.Target)
		assert.True(t, body.Target.IsOwner)
	})

	t.Run("target outranks the actor", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		junior := kickerRole(community.ID)
		senior := &models.Role{ID: uuid.New(), CommunityID: community.ID, Name: "Senior", Position: 5,
			Permissions: []models.Permission{models.PermissionKickMembers}}

		rig.armAccess(community, seedMembership(community.ID, rig.userID, junior.ID), []*models.Role{junior, senior})

		targetID := uuid.New()
		rig.repo.On("GetMembership", anyContext(), community.ID, targetID).Return(seedMembership(community.ID, targetID, senior.ID), nil)
		rig.repo.On("GetActiveBan", anyContext(), community.ID, targetID).Return(nil, nil)

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/authorization-checks", map[string]any{
			"actor_user_id":  rig.userID.String(),
			"permission":     models.PermissionKickMembers,
			"target_user_id": targetID.String(),
		})

		require.Equal(t, http.StatusOK, response.Code)
		body := decode[models.AuthorizationCheckResponse](t, response)
		assert.False(t, body.Allowed)
		require.NotNil(t, body.DenialReason)
		assert.Equal(t, models.DenialTargetOutranksActor, *body.DenialReason)
		require.NotNil(t, body.Target)
		assert.False(t, body.Target.OutrankedByActor)
		assert.Len(t, body.ActorPermissions, 1)
	})

	t.Run("actor outranks the target", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		senior := &models.Role{ID: uuid.New(), CommunityID: community.ID, Name: "Senior", Position: 5,
			Permissions: []models.Permission{models.PermissionKickMembers}}
		junior := kickerRole(community.ID)

		rig.armAccess(community, seedMembership(community.ID, rig.userID, senior.ID), []*models.Role{senior, junior})

		targetID := uuid.New()
		rig.repo.On("GetMembership", anyContext(), community.ID, targetID).Return(seedMembership(community.ID, targetID, junior.ID), nil)
		rig.repo.On("GetActiveBan", anyContext(), community.ID, targetID).Return(nil, nil)

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/authorization-checks", map[string]any{
			"actor_user_id":  rig.userID.String(),
			"permission":     models.PermissionKickMembers,
			"target_user_id": targetID.String(),
		})

		require.Equal(t, http.StatusOK, response.Code)
		body := decode[models.AuthorizationCheckResponse](t, response)
		assert.True(t, body.Allowed)
		assert.Nil(t, body.DenialReason)
		require.NotNil(t, body.Target)
		assert.True(t, body.Target.OutrankedByActor)
		assert.True(t, body.Target.IsMember)
	})
}

func TestInternalAuthorizationCheckContractErrors(t *testing.T) {
	t.Parallel()

	t.Run("unknown community is 404", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		communityID := uuid.New()
		rig.repo.On("GetCommunity", anyContext(), communityID).Return(nil, models.ErrCommunityNotFound)

		response := rig.callInternal(t, "/internal/communities/"+communityID.String()+"/authorization-checks", map[string]any{
			"actor_user_id": rig.userID.String(),
			"permission":    models.PermissionKickMembers,
		})

		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))
		assert.Equal(t, string(models.CodeCommunityNotFound), errorResponse(t, response).Error)
	})

	t.Run("unknown permission key is 400 not a denial", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/authorization-checks", map[string]any{
			"actor_user_id": rig.userID.String(),
			"permission":    "teleport_members",
		})

		assert.Equal(t, http.StatusBadRequest, response.Code)
		assert.Equal(t, string(models.CodeInvalidRequest), errorResponse(t, response).Error)
	})

	t.Run("missing actor is 400", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/authorization-checks", map[string]any{
			"permission": models.PermissionKickMembers,
		})

		assert.Equal(t, http.StatusBadRequest, response.Code)
	})

	t.Run("unknown field is rejected", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/authorization-checks", map[string]any{
			"actor_user_id": rig.userID.String(),
			"permission":    models.PermissionKickMembers,
			"surprise":      true,
		})

		assert.Equal(t, http.StatusBadRequest, response.Code)
		assert.Equal(t, string(models.CodeInvalidRequest), errorResponse(t, response).Error)
	})
}

func TestInternalVoiceAccessCheck(t *testing.T) {
	t.Parallel()

	t.Run("member is allowed into a voice channel", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, voice := seedCommunity(uuid.New())
		rig.repo.On("GetCommunity", anyContext(), community.ID).Return(community, nil)
		rig.repo.On("GetActiveBan", anyContext(), community.ID, rig.userID).Return(nil, nil)
		rig.repo.On("GetMembership", anyContext(), community.ID, rig.userID).Return(seedMembership(community.ID, rig.userID), nil)
		rig.repo.On("GetChannel", anyContext(), community.ID, voice.ID).Return(voice, nil)
		rig.repo.On("ListVoiceChannels", anyContext(), community.ID).Return([]*models.Channel{voice}, nil)

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/channels/"+voice.ID.String()+"/voice-access-checks",
			map[string]any{"user_id": rig.userID.String()})

		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		body := decode[models.VoiceAccessCheckResponse](t, response)
		assert.True(t, body.Allowed)
		assert.Nil(t, body.DenialReason)
		assert.Nil(t, body.MutedUntil)
		require.NotNil(t, body.Channel)
		assert.Equal(t, voice.ID, body.Channel.ChannelID)
		assert.Equal(t, models.DefaultVoiceCapacity, body.Channel.Capacity)
		require.Len(t, body.VoiceChannels, 1)
	})

	t.Run("muted member is allowed listen-only", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, voice := seedCommunity(uuid.New())
		mutedUntil := testNow.Add(30 * 60 * 1e9)
		membership := seedMembership(community.ID, rig.userID)
		membership.MutedUntil = &mutedUntil

		rig.repo.On("GetCommunity", anyContext(), community.ID).Return(community, nil)
		rig.repo.On("GetActiveBan", anyContext(), community.ID, rig.userID).Return(nil, nil)
		rig.repo.On("GetMembership", anyContext(), community.ID, rig.userID).Return(membership, nil)
		rig.repo.On("GetChannel", anyContext(), community.ID, voice.ID).Return(voice, nil)
		rig.repo.On("ListVoiceChannels", anyContext(), community.ID).Return([]*models.Channel{voice}, nil)

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/channels/"+voice.ID.String()+"/voice-access-checks",
			map[string]any{"user_id": rig.userID.String()})

		require.Equal(t, http.StatusOK, response.Code)
		body := decode[models.VoiceAccessCheckResponse](t, response)
		assert.True(t, body.Allowed, "a mute restricts publishing, never channel access")
		require.NotNil(t, body.MutedUntil)
		assert.Equal(t, mutedUntil, *body.MutedUntil)
	})

	t.Run("expired mute does not restrict", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, voice := seedCommunity(uuid.New())
		mutedUntil := testNow.Add(-30 * 60 * 1e9)
		membership := seedMembership(community.ID, rig.userID)
		membership.MutedUntil = &mutedUntil

		rig.repo.On("GetCommunity", anyContext(), community.ID).Return(community, nil)
		rig.repo.On("GetActiveBan", anyContext(), community.ID, rig.userID).Return(nil, nil)
		rig.repo.On("GetMembership", anyContext(), community.ID, rig.userID).Return(membership, nil)
		rig.repo.On("GetChannel", anyContext(), community.ID, voice.ID).Return(voice, nil)
		rig.repo.On("ListVoiceChannels", anyContext(), community.ID).Return([]*models.Channel{voice}, nil)

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/channels/"+voice.ID.String()+"/voice-access-checks",
			map[string]any{"user_id": rig.userID.String()})

		require.Equal(t, http.StatusOK, response.Code)
		body := decode[models.VoiceAccessCheckResponse](t, response)
		assert.True(t, body.Allowed)
		assert.Nil(t, body.MutedUntil)
	})

	t.Run("banned user is denied in band", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, voice := seedCommunity(uuid.New())
		rig.repo.On("GetCommunity", anyContext(), community.ID).Return(community, nil)
		rig.repo.On("GetActiveBan", anyContext(), community.ID, rig.userID).
			Return(&models.Ban{ID: uuid.New(), CommunityID: community.ID, UserID: rig.userID, BannedAt: testNow}, nil)

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/channels/"+voice.ID.String()+"/voice-access-checks",
			map[string]any{"user_id": rig.userID.String()})

		require.Equal(t, http.StatusOK, response.Code, "a denial is never an error status")
		body := decode[models.VoiceAccessCheckResponse](t, response)
		assert.False(t, body.Allowed)
		require.NotNil(t, body.DenialReason)
		assert.Equal(t, models.DenialBanned, *body.DenialReason)
	})

	t.Run("non member is denied in band", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, voice := seedCommunity(uuid.New())
		rig.repo.On("GetCommunity", anyContext(), community.ID).Return(community, nil)
		rig.repo.On("GetActiveBan", anyContext(), community.ID, rig.userID).Return(nil, nil)
		rig.repo.On("GetMembership", anyContext(), community.ID, rig.userID).Return(nil, models.ErrMembershipNotFound)

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/channels/"+voice.ID.String()+"/voice-access-checks",
			map[string]any{"user_id": rig.userID.String()})

		require.Equal(t, http.StatusOK, response.Code)
		body := decode[models.VoiceAccessCheckResponse](t, response)
		assert.False(t, body.Allowed)
		require.NotNil(t, body.DenialReason)
		assert.Equal(t, models.DenialNotAMember, *body.DenialReason)
	})

	t.Run("text channel is not joinable", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, text, _ := seedCommunity(uuid.New())
		rig.repo.On("GetCommunity", anyContext(), community.ID).Return(community, nil)
		rig.repo.On("GetActiveBan", anyContext(), community.ID, rig.userID).Return(nil, nil)
		rig.repo.On("GetMembership", anyContext(), community.ID, rig.userID).Return(seedMembership(community.ID, rig.userID), nil)
		rig.repo.On("GetChannel", anyContext(), community.ID, text.ID).Return(text, nil)

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/channels/"+text.ID.String()+"/voice-access-checks",
			map[string]any{"user_id": rig.userID.String()})

		require.Equal(t, http.StatusOK, response.Code)
		body := decode[models.VoiceAccessCheckResponse](t, response)
		assert.False(t, body.Allowed)
		require.NotNil(t, body.DenialReason)
		assert.Equal(t, models.DenialNotAVoiceChannel, *body.DenialReason)
	})

	t.Run("unknown channel is denied in band", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		channelID := uuid.New()
		rig.repo.On("GetCommunity", anyContext(), community.ID).Return(community, nil)
		rig.repo.On("GetActiveBan", anyContext(), community.ID, rig.userID).Return(nil, nil)
		rig.repo.On("GetMembership", anyContext(), community.ID, rig.userID).Return(seedMembership(community.ID, rig.userID), nil)
		rig.repo.On("GetChannel", anyContext(), community.ID, channelID).Return(nil, models.ErrChannelNotFound)

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/channels/"+channelID.String()+"/voice-access-checks",
			map[string]any{"user_id": rig.userID.String()})

		require.Equal(t, http.StatusOK, response.Code)
		body := decode[models.VoiceAccessCheckResponse](t, response)
		assert.False(t, body.Allowed)
		require.NotNil(t, body.DenialReason)
		assert.Equal(t, models.DenialChannelNotFound, *body.DenialReason)
	})

	t.Run("unknown community is 404", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		communityID := uuid.New()
		channelID := uuid.New()
		rig.repo.On("GetCommunity", anyContext(), communityID).Return(nil, models.ErrCommunityNotFound)

		response := rig.callInternal(t, "/internal/communities/"+communityID.String()+"/channels/"+channelID.String()+"/voice-access-checks",
			map[string]any{"user_id": rig.userID.String()})

		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Equal(t, "application/problem+json", response.Header().Get("Content-Type"))
	})

	t.Run("missing user id is 400", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, voice := seedCommunity(uuid.New())

		response := rig.callInternal(t, "/internal/communities/"+community.ID.String()+"/channels/"+voice.ID.String()+"/voice-access-checks",
			map[string]any{})

		assert.Equal(t, http.StatusBadRequest, response.Code)
		assert.Equal(t, string(models.CodeInvalidRequest), errorResponse(t, response).Error)
	})
}

// TestServiceTokenComparisonIsConstantTime guards the internal middleware against
// a token that is a prefix or a different length.
func TestServiceTokenComparisonIsConstantTime(t *testing.T) {
	t.Parallel()

	rig := newRig(t)
	community, _, _ := seedCommunity(uuid.New())

	for _, candidate := range []string{"", "internal-service-toke", "internal-service-token-longer"} {
		response := rig.call(t, http.MethodPost,
			"/internal/communities/"+community.ID.String()+"/authorization-checks",
			map[string]any{"actor_user_id": rig.userID.String(), "permission": models.PermissionKickMembers},
			withBearer(candidate))

		assert.Equal(t, http.StatusUnauthorized, response.Code, "candidate %q must be rejected", candidate)
	}

	rig.repo.AssertNotCalled(t, "GetCommunity", mock.Anything, mock.Anything)
}
