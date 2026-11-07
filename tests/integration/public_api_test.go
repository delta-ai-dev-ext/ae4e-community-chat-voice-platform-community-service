package integration

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"community_service/internal/events"
	"community_service/internal/models"
	"community_service/internal/obs"
)

// documentedRoutes is every operation of docs/openapi_spec.yaml plus the three
// operational endpoints.
var documentedRoutes = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/healthz"},
	{http.MethodGet, "/readyz"},
	{http.MethodGet, "/metrics"},
	{http.MethodPost, "/communities"},
	{http.MethodGet, "/communities/mine"},
	{http.MethodGet, "/communities/:community_id"},
	{http.MethodPatch, "/communities/:community_id"},
	{http.MethodPost, "/communities/:community_id/channels"},
	{http.MethodPatch, "/communities/:community_id/channels/:channel_id"},
	{http.MethodDelete, "/communities/:community_id/channels/:channel_id"},
	{http.MethodPost, "/communities/:community_id/invites"},
	{http.MethodDelete, "/communities/:community_id/invites/:code"},
	{http.MethodGet, "/invites/:code"},
	{http.MethodPost, "/invites/:code/accept"},
	{http.MethodPost, "/communities/:community_id/leave"},
	{http.MethodGet, "/communities/:community_id/members"},
	{http.MethodPut, "/communities/:community_id/members/:user_id/roles"},
	{http.MethodGet, "/communities/:community_id/roles"},
	{http.MethodPost, "/communities/:community_id/roles"},
	{http.MethodPatch, "/communities/:community_id/roles/:role_id"},
	{http.MethodDelete, "/communities/:community_id/roles/:role_id"},
	{http.MethodPost, "/internal/communities/:community_id/authorization-checks"},
	{http.MethodPost, "/internal/communities/:community_id/channels/:channel_id/voice-access-checks"},
}

func TestRouterRegistersEveryDocumentedRoute(t *testing.T) {
	t.Parallel()

	engine := newRig(t).engine

	registered := make(map[string]struct{}, len(engine.Routes()))
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = struct{}{}
	}

	for _, expected := range documentedRoutes {
		_, found := registered[expected.method+" "+expected.path]
		assert.True(t, found, "missing route %s %s", expected.method, expected.path)
	}
}

func TestProbesSeparateLivenessFromReadiness(t *testing.T) {
	t.Parallel()

	rig := newRig(t)
	rig.readiness.err = assert.AnError

	live := rig.call(t, http.MethodGet, "/healthz", nil)
	assert.Equal(t, http.StatusOK, live.Code)
	assert.Equal(t, "ok", decode[models.HealthStatus](t, live).Status)

	notReady := rig.call(t, http.MethodGet, "/readyz", nil)
	assert.Equal(t, http.StatusServiceUnavailable, notReady.Code)
	assert.Equal(t, string(models.CodeNotReady), errorResponse(t, notReady).Error)

	rig.readiness.err = nil
	ready := rig.call(t, http.MethodGet, "/readyz", nil)
	assert.Equal(t, http.StatusOK, ready.Code)
}

func TestMetricsEndpointExposesDocumentedMetricFamilies(t *testing.T) {
	t.Parallel()

	rig := newRig(t)
	rig.metrics.RecordCommunityCreated()
	rig.metrics.RecordJoin(obs.JoinResultJoined)
	rig.metrics.RecordLeave(obs.LeaveReasonKicked)
	rig.metrics.RecordConsumerLag(events.ModerationTopic, "community_service", 3)
	rig.metrics.RecordDeadLetter(events.EventTypeMemberSanctionChanged)

	// One completed request so the RED metrics carry a label set at scrape time.
	rig.call(t, http.MethodGet, "/healthz", nil)

	response := rig.call(t, http.MethodGet, "/metrics", nil)

	require.Equal(t, http.StatusOK, response.Code)
	body := response.Body.String()
	for _, family := range []string{
		obs.MetricCommunityCreated,
		obs.MetricInvitesCreated,
		obs.MetricJoins,
		obs.MetricLeaves,
		obs.MetricRoleAssignments,
		obs.MetricRequests,
		obs.MetricRequestDuration,
		obs.MetricKafkaConsumerLag,
		obs.MetricKafkaConsumerError,
		obs.MetricKafkaDeadLetter,
		obs.MetricPublishFailures,
	} {
		assert.Contains(t, body, family)
	}
	assert.Contains(t, body, `community_joins_total{result="joined"} 1`)
	assert.Contains(t, body, `community_leaves_total{reason="kicked"} 1`)
}

func TestPublicRoutesRejectMissingOrInvalidToken(t *testing.T) {
	t.Parallel()

	rig := newRig(t)

	missing := rig.callWithoutAuth(t, http.MethodGet, "/communities/mine", nil)
	assert.Equal(t, http.StatusUnauthorized, missing.Code)
	assert.Equal(t, string(models.CodeUnauthorized), errorResponse(t, missing).Error)

	malformed := rig.call(t, http.MethodGet, "/communities/mine", nil, withBearer("not-a-token"))
	assert.Equal(t, http.StatusUnauthorized, malformed.Code)

	foreignKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	wrongSignature := rig.call(t, http.MethodGet, "/communities/mine", nil, withBearer(mintToken(t, foreignKey, uuid.New())))
	assert.Equal(t, http.StatusUnauthorized, wrongSignature.Code, "a token signed by an unknown key is rejected")

	wrongAudience := rig.call(t, http.MethodGet, "/communities/mine", nil, withBearer("eyJhbGciOiJSUzI1NiJ9.e30.signature"))
	assert.Equal(t, http.StatusUnauthorized, wrongAudience.Code)
	assert.NotEmpty(t, missing.Header().Get("X-Trace-Id"), "every response carries the trace id")
}

func TestUnexpectedRepositoryFailureIsInternalError(t *testing.T) {
	t.Parallel()

	rig := newRig(t)
	community, _, _ := seedCommunity(uuid.New())
	rig.repo.On("GetCommunity", anyContext(), community.ID).Return(nil, errors.New("connection reset by peer"))

	response := rig.call(t, http.MethodGet, "/communities/"+community.ID.String(), nil)

	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Equal(t, string(models.CodeInternal), errorResponse(t, response).Error)
	assert.NotContains(t, response.Body.String(), "connection reset", "internal details must not leak")
}

func TestListMyCommunitiesReturnsCallerCommunities(t *testing.T) {
	t.Parallel()

	rig := newRig(t)
	community, _, _ := seedCommunity(uuid.New())
	other := &models.Community{ID: uuid.New(), Name: "Rustaceans", OwnerUserID: uuid.New(), CreatedAt: testNow, UpdatedAt: testNow}

	rig.repo.On("ListCommunitiesByMember", anyContext(), rig.userID, 2, 0).
		Return([]*models.Community{community, other}, nil)

	response := rig.call(t, http.MethodGet, "/communities/mine?limit=1", nil)

	require.Equal(t, http.StatusOK, response.Code)
	body := decode[models.CommunityListResponse](t, response)
	require.Len(t, body.Communities, 1)
	assert.Equal(t, community.ID, body.Communities[0].ID)
	require.NotNil(t, body.NextCursor)
	assert.NotEmpty(t, *body.NextCursor)
}

func TestListMyCommunitiesRejectsInvalidPagination(t *testing.T) {
	t.Parallel()

	rig := newRig(t)

	outOfRange := rig.call(t, http.MethodGet, "/communities/mine?limit=1000", nil)
	assert.Equal(t, http.StatusBadRequest, outOfRange.Code)
	assert.Equal(t, string(models.CodeInvalidRequest), errorResponse(t, outOfRange).Error)

	badCursor := rig.call(t, http.MethodGet, "/communities/mine?cursor=%21%21%21", nil)
	assert.Equal(t, http.StatusBadRequest, badCursor.Code)
}

func TestCreateCommunityCreatesDefaultsAndEmitsChannelEvents(t *testing.T) {
	t.Parallel()

	rig := newRig(t)
	var created *models.Community
	var createdChannels []*models.Channel
	var ownerMembership *models.Membership

	rig.repo.On("CreateCommunity", anyContext(), mock.AnythingOfType("*models.Community"), mock.AnythingOfType("*models.Membership"), mock.Anything).
		Run(func(args mock.Arguments) {
			created = args.Get(1).(*models.Community)
			ownerMembership = args.Get(2).(*models.Membership)
			createdChannels = args.Get(3).([]*models.Channel)
		}).
		Return(nil)

	response := rig.call(t, http.MethodPost, "/communities", map[string]any{"name": "  Go Gophers  "})

	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())

	body := decode[models.CommunityDetailResponse](t, response)
	require.NotNil(t, body.Community)
	assert.Equal(t, "Go Gophers", body.Community.Name)
	assert.Equal(t, rig.userID, body.Community.OwnerUserID)
	assert.True(t, body.Caller.IsOwner)
	assert.True(t, body.Caller.IsMember)
	assert.Len(t, body.Caller.Permissions, 10)

	require.Len(t, body.Channels, 2)
	assert.Equal(t, models.ChannelKindText, body.Channels[0].Kind)
	assert.Equal(t, "general", body.Channels[0].Name)
	assert.Equal(t, models.ChannelKindVoice, body.Channels[1].Kind)
	require.NotNil(t, body.Channels[1].Capacity)
	assert.Equal(t, models.DefaultVoiceCapacity, *body.Channels[1].Capacity)

	require.NotNil(t, created)
	require.NotNil(t, ownerMembership, "the creator becomes an active member in the same transaction")
	assert.Equal(t, models.StatusActive, ownerMembership.Status)
	require.Len(t, createdChannels, 2)

	published := rig.publisher.Payloads(events.EventTypeCommunityChannelChanged)
	require.Len(t, published, 2, "each default channel emits an EVT-003 created event")
	for _, payload := range published {
		channelPayload, ok := payload.(events.CommunityChannelChanged)
		require.True(t, ok)
		assert.Equal(t, models.ChannelChangeCreated, channelPayload.Change)
		assert.Equal(t, created.ID, channelPayload.CommunityID)
	}
}

func TestCreateCommunityValidation(t *testing.T) {
	t.Parallel()

	rig := newRig(t)

	empty := rig.call(t, http.MethodPost, "/communities", map[string]any{"name": "   "})
	assert.Equal(t, http.StatusUnprocessableEntity, empty.Code)
	assert.Equal(t, string(models.CodeValidationFailed), errorResponse(t, empty).Error)

	tooLong := rig.call(t, http.MethodPost, "/communities", map[string]any{"name": string(make([]rune, 101))})
	assert.Equal(t, http.StatusUnprocessableEntity, tooLong.Code)

	broken := rig.call(t, http.MethodPost, "/communities", "not-an-object")
	assert.Equal(t, http.StatusBadRequest, broken.Code)
	assert.Equal(t, string(models.CodeInvalidRequest), errorResponse(t, broken).Error)

	rig.repo.AssertNotCalled(t, "CreateCommunity", anyContext(), mock.Anything, mock.Anything, mock.Anything)
}

func TestGetCommunityErrorCodes(t *testing.T) {
	t.Parallel()

	t.Run("unknown community is 404", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		communityID := uuid.New()
		rig.repo.On("GetCommunity", anyContext(), communityID).Return(nil, models.ErrCommunityNotFound)

		response := rig.call(t, http.MethodGet, "/communities/"+communityID.String(), nil)

		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Equal(t, string(models.CodeCommunityNotFound), errorResponse(t, response).Error)
	})

	t.Run("non member is 403", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		rig.armAccess(community, nil, nil)

		response := rig.call(t, http.MethodGet, "/communities/"+community.ID.String(), nil)

		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.Equal(t, string(models.CodeNotAMember), errorResponse(t, response).Error)
	})

	t.Run("member sees channels and effective permissions", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, text, voice := seedCommunity(uuid.New())
		moderator := &models.Role{
			ID:          uuid.New(),
			CommunityID: community.ID,
			Name:        "Moderator",
			Position:    10,
			Permissions: []models.Permission{models.PermissionKickMembers, models.PermissionManageRoles},
		}
		membership := seedMembership(community.ID, rig.userID, moderator.ID)

		rig.armAccess(community, membership, []*models.Role{moderator})
		rig.armCommunityReads(community.ID, text, voice)

		response := rig.call(t, http.MethodGet, "/communities/"+community.ID.String(), nil)

		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		body := decode[models.CommunityDetailResponse](t, response)
		assert.Len(t, body.Channels, 2)
		assert.False(t, body.Caller.IsOwner)
		assert.True(t, body.Caller.IsMember)
		assert.Equal(t, []uuid.UUID{moderator.ID}, body.Caller.RoleIDs)
		assert.ElementsMatch(t, []models.Permission{models.PermissionKickMembers, models.PermissionManageRoles}, body.Caller.Permissions)
	})

	t.Run("malformed community id is 400", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)

		response := rig.call(t, http.MethodGet, "/communities/not-a-uuid", nil)

		assert.Equal(t, http.StatusBadRequest, response.Code)
		assert.Equal(t, string(models.CodeInvalidRequest), errorResponse(t, response).Error)
	})
}

func TestUpdateCommunityRequiresPermissionAndValidatesIconURL(t *testing.T) {
	t.Parallel()

	t.Run("member without manage_community is forbidden", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		role := &models.Role{ID: uuid.New(), CommunityID: community.ID, Name: "Member", Position: 50,
			Permissions: []models.Permission{models.PermissionCreateInvites}}
		membership := seedMembership(community.ID, rig.userID, role.ID)
		rig.armAccess(community, membership, []*models.Role{role})

		response := rig.call(t, http.MethodPatch, "/communities/"+community.ID.String(), map[string]any{"name": "Renamed"})

		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.Equal(t, string(models.CodeMissingPermission), errorResponse(t, response).Error)
	})

	t.Run("invalid icon url is unprocessable", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)

		response := rig.call(t, http.MethodPatch, "/communities/"+community.ID.String(), map[string]any{"icon_url": "ftp://example.test/icon.png"})

		assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
		assert.Equal(t, string(models.CodeValidationFailed), errorResponse(t, response).Error)
	})

	t.Run("owner renames and clears the description", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)

		updated := *community
		updated.Name = "Renamed"
		rig.repo.On("UpdateCommunity", anyContext(), community.ID, mock.MatchedBy(func(patch models.CommunityPatch) bool {
			return patch.Name != nil && *patch.Name == "Renamed" && patch.Description.Set && patch.Description.Value == nil
		}), testNow).Return(&updated, nil)

		response := rig.call(t, http.MethodPatch, "/communities/"+community.ID.String(), map[string]any{
			"name":        "Renamed",
			"description": nil,
		})

		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, "Renamed", decode[models.Community](t, response).Name)
	})

	t.Run("empty patch is unprocessable", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)

		response := rig.call(t, http.MethodPatch, "/communities/"+community.ID.String(), map[string]any{})

		assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
	})
}

func TestCreateChannelRules(t *testing.T) {
	t.Parallel()

	t.Run("voice channel defaults to capacity and emits an event", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)

		rig.repo.On("CreateChannel", anyContext(), mock.AnythingOfType("*models.Channel")).Return(nil)

		response := rig.call(t, http.MethodPost, "/communities/"+community.ID.String()+"/channels",
			map[string]any{"name": "Stage", "kind": "voice"})

		require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
		channel := decode[models.Channel](t, response)
		assert.Equal(t, models.ChannelKindVoice, channel.Kind)
		require.NotNil(t, channel.Capacity)
		assert.Equal(t, models.DefaultVoiceCapacity, *channel.Capacity)

		published := rig.publisher.Payloads(events.EventTypeCommunityChannelChanged)
		require.Len(t, published, 1)
		payload := published[0].(events.CommunityChannelChanged)
		assert.Equal(t, models.ChannelChangeCreated, payload.Change)
		assert.Equal(t, "Stage", payload.Name)
		assert.Equal(t, int64(1), payload.ChannelVersion, "a new channel starts at channel_version 1")
	})

	t.Run("capacity on a text channel is unprocessable", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)

		response := rig.call(t, http.MethodPost, "/communities/"+community.ID.String()+"/channels",
			map[string]any{"name": "offtopic", "kind": "text", "capacity": 5})

		assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
	})

	t.Run("unknown kind is unprocessable", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)

		response := rig.call(t, http.MethodPost, "/communities/"+community.ID.String()+"/channels",
			map[string]any{"name": "video", "kind": "video"})

		assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
	})

	t.Run("duplicate channel name is a conflict", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)
		rig.repo.On("CreateChannel", anyContext(), mock.AnythingOfType("*models.Channel")).Return(models.ErrChannelNameConflict)

		response := rig.call(t, http.MethodPost, "/communities/"+community.ID.String()+"/channels",
			map[string]any{"name": "general", "kind": "text"})

		assert.Equal(t, http.StatusConflict, response.Code)
		assert.Equal(t, string(models.CodeChannelNameConflict), errorResponse(t, response).Error)
		assert.Empty(t, rig.publisher.Messages(), "a rejected write must not emit an event")
	})
}

func TestUpdateAndDeleteChannel(t *testing.T) {
	t.Parallel()

	t.Run("deleting the last channel is a conflict", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, text, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)
		rig.repo.On("DeleteChannel", anyContext(), community.ID, text.ID).Return(nil, models.ErrLastChannel)

		response := rig.call(t, http.MethodDelete, "/communities/"+community.ID.String()+"/channels/"+text.ID.String(), nil)

		assert.Equal(t, http.StatusConflict, response.Code)
		assert.Equal(t, string(models.CodeLastChannel), errorResponse(t, response).Error)
	})

	t.Run("deleted channel emits a deleted event", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, voice := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)
		rig.repo.On("DeleteChannel", anyContext(), community.ID, voice.ID).Return(voice, nil)

		response := rig.call(t, http.MethodDelete, "/communities/"+community.ID.String()+"/channels/"+voice.ID.String(), nil)

		require.Equal(t, http.StatusNoContent, response.Code)
		assert.Empty(t, response.Body.String())

		published := rig.publisher.Payloads(events.EventTypeCommunityChannelChanged)
		require.Len(t, published, 1)
		assert.Equal(t, models.ChannelChangeDeleted, published[0].(events.CommunityChannelChanged).Change)
	})

	t.Run("capacity on a text channel is unprocessable", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, text, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)
		rig.repo.On("GetChannel", anyContext(), community.ID, text.ID).Return(text, nil)

		response := rig.call(t, http.MethodPatch, "/communities/"+community.ID.String()+"/channels/"+text.ID.String(),
			map[string]any{"capacity": 3})

		assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
	})

	t.Run("unknown channel is 404", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(rig.userID)
		channelID := uuid.New()
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)
		rig.repo.On("GetChannel", anyContext(), community.ID, channelID).Return(nil, models.ErrChannelNotFound)

		response := rig.call(t, http.MethodPatch, "/communities/"+community.ID.String()+"/channels/"+channelID.String(),
			map[string]any{"name": "renamed"})

		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Equal(t, string(models.CodeChannelNotFound), errorResponse(t, response).Error)
	})

	t.Run("reorder emits the bumped channel version", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, text, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)
		rig.repo.On("GetChannel", anyContext(), community.ID, text.ID).Return(text, nil)

		moved := *text
		moved.Position = 7
		moved.ChannelVersion = 2
		rig.repo.On("UpdateChannel", anyContext(), community.ID, text.ID, mock.MatchedBy(func(patch models.ChannelPatch) bool {
			return patch.Position != nil && *patch.Position == 7
		}), testNow).Return(&moved, nil)

		response := rig.call(t, http.MethodPatch, "/communities/"+community.ID.String()+"/channels/"+text.ID.String(),
			map[string]any{"position": 7})

		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Equal(t, 7, decode[models.Channel](t, response).Position)

		published := rig.publisher.Payloads(events.EventTypeCommunityChannelChanged)
		require.Len(t, published, 1)
		payload := published[0].(events.CommunityChannelChanged)
		assert.Equal(t, models.ChannelChangeUpdated, payload.Change)
		assert.Equal(t, int64(2), payload.ChannelVersion, "an update bumps channel_version")
		assert.Equal(t, 7, payload.Position)
	})
}
