package unit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"community_service/internal/events"
	"community_service/internal/models"
	"community_service/internal/service"
	"community_service/tests/mocks"
)

// frozenNow is the injected clock instant of every unit test.
var frozenNow = time.Date(2026, 2, 5, 10, 0, 0, 0, time.UTC)

func newInviteService() (*service.InviteService, *mocks.Repository, *mocks.Publisher, *mocks.Clock) {
	repository := &mocks.Repository{}
	publisher := mocks.NewPublisher()
	clock := mocks.NewClock(frozenNow)

	return service.NewInviteService(service.Deps{
		Communities: repository,
		Channels:    repository,
		Invites:     repository,
		Memberships: repository,
		Roles:       repository,
		Sanctions:   repository,
		Publisher:   publisher,
		Clock:       clock,
	}), repository, publisher, clock
}

func activeMembership(communityID, userID uuid.UUID) *models.Membership {
	return &models.Membership{
		ID:            uuid.New(),
		CommunityID:   communityID,
		UserID:        userID,
		Status:        models.StatusActive,
		JoinedAt:      frozenNow,
		MemberVersion: 1,
		RoleIDs:       []uuid.UUID{},
	}
}

func TestCheckInviteValidity(t *testing.T) {
	t.Parallel()

	expired := frozenNow.Add(-time.Minute)
	future := frozenNow.Add(time.Hour)
	atCapacity := 5
	atUseCount := 5
	revokedAt := frozenNow.Add(-time.Minute)

	testCases := []struct {
		name   string
		invite *models.Invite
		want   string
	}{
		{
			name:   "usable invite",
			invite: &models.Invite{ExpiresAt: &future, MaxUses: &atCapacity},
			want:   "",
		},
		{
			name:   "non expiring unlimited invite",
			invite: &models.Invite{},
			want:   "",
		},
		{
			name:   "revoked invite",
			invite: &models.Invite{RevokedAt: &revokedAt},
			want:   models.InviteInvalidRevoked,
		},
		{
			name:   "expired invite",
			invite: &models.Invite{ExpiresAt: &expired},
			want:   models.InviteInvalidExpired,
		},
		{
			name:   "exhausted invite",
			invite: &models.Invite{MaxUses: &atUseCount, UseCount: atUseCount},
			want:   models.InviteInvalidExhausted,
		},
		{
			name:   "missing invite fails closed",
			invite: nil,
			want:   models.InviteInvalidExpired,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, models.CheckInvite(tc.invite, frozenNow))
		})
	}
}

func TestAcceptInviteUnknownCodeIsNotFound(t *testing.T) {
	t.Parallel()

	inviteService, repository, publisher, _ := newInviteService()
	repository.On("GetInviteByCode", mock.Anything, "missing").Return(nil, models.ErrInviteNotFound)

	_, err := inviteService.Accept(context.Background(), uuid.New(), "missing", strings.Repeat("a", 32), uuid.NewString())

	require.Error(t, err)
	assert.True(t, errors.Is(err, models.ErrInviteNotFound))
	assert.Empty(t, publisher.Messages())
}

func TestAcceptInviteInvalidCodeIsGone(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	userID := uuid.New()
	expired := frozenNow.Add(-time.Hour)

	inviteService, repository, publisher, _ := newInviteService()
	repository.On("GetInviteByCode", mock.Anything, "expired").Return(&models.Invite{
		ID:          uuid.New(),
		CommunityID: communityID,
		Code:        "expired",
		ExpiresAt:   &expired,
	}, nil)

	_, err := inviteService.Accept(context.Background(), userID, "expired", strings.Repeat("a", 32), uuid.NewString())

	require.Error(t, err)
	assert.True(t, errors.Is(err, models.ErrInviteExpired))
	assert.Equal(t, 410, mustAppError(t, err).HTTPStatus())
	assert.Empty(t, publisher.Messages())
}

func TestAcceptInviteBannedCallerIsForbidden(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	userID := uuid.New()

	inviteService, repository, publisher, _ := newInviteService()
	repository.On("GetInviteByCode", mock.Anything, "valid").Return(&models.Invite{
		ID:          uuid.New(),
		CommunityID: communityID,
		Code:        "valid",
	}, nil)
	repository.On("GetActiveBan", mock.Anything, communityID, userID).Return(&models.Ban{ID: uuid.New()}, nil)

	_, err := inviteService.Accept(context.Background(), userID, "valid", strings.Repeat("a", 32), uuid.NewString())

	require.Error(t, err)
	assert.True(t, errors.Is(err, models.ErrBanned))
	assert.Equal(t, 403, mustAppError(t, err).HTTPStatus())
	repository.AssertNotCalled(t, "JoinWithInvite", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	assert.Empty(t, publisher.Messages())
}

func TestAcceptInviteAlreadyActiveIsIdempotent(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	userID := uuid.New()
	existing := activeMembership(communityID, userID)

	inviteService, repository, publisher, _ := newInviteService()
	repository.On("GetInviteByCode", mock.Anything, "valid").Return(&models.Invite{
		ID:          uuid.New(),
		CommunityID: communityID,
		Code:        "valid",
	}, nil)
	repository.On("GetActiveBan", mock.Anything, communityID, userID).Return(nil, nil)
	repository.On("GetMembership", mock.Anything, communityID, userID).Return(existing, nil)

	membership, err := inviteService.Accept(context.Background(), userID, "valid", strings.Repeat("a", 32), uuid.NewString())

	require.NoError(t, err)
	assert.Equal(t, existing.ID, membership.ID)
	repository.AssertNotCalled(t, "JoinWithInvite", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	assert.Empty(t, publisher.Messages(), "an idempotent accept must not emit a second joined event")
}

func TestAcceptInviteJoinsAndEmitsEvent(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	userID := uuid.New()
	joined := activeMembership(communityID, userID)
	traceID := strings.Repeat("b", 32)
	correlationID := uuid.NewString()

	inviteService, repository, publisher, _ := newInviteService()
	repository.On("GetInviteByCode", mock.Anything, "valid").Return(&models.Invite{
		ID:          uuid.New(),
		CommunityID: communityID,
		Code:        "valid",
	}, nil)
	repository.On("GetActiveBan", mock.Anything, communityID, userID).Return(nil, nil)
	repository.On("GetMembership", mock.Anything, communityID, userID).Return(nil, models.ErrMembershipNotFound)
	repository.On("JoinWithInvite", mock.Anything, communityID, userID, mock.Anything, "valid", frozenNow).Return(joined, true, nil)

	membership, err := inviteService.Accept(context.Background(), userID, "valid", traceID, correlationID)

	require.NoError(t, err)
	assert.Equal(t, models.StatusActive, membership.Status)

	messages := publisher.Messages()
	require.Len(t, messages, 1)
	assert.Equal(t, events.CommunityTopic, messages[0].Topic)
	assert.Equal(t, communityID.String(), messages[0].Key, "the partition key must be community_id")
	assert.Equal(t, events.EventTypeCommunityMemberChanged, messages[0].Message.Envelope.EventType)
	assert.Equal(t, traceID, messages[0].Message.Envelope.TraceID)
	assert.Equal(t, correlationID, messages[0].Message.Envelope.CorrelationID)
	assert.Nil(t, messages[0].Message.Envelope.CausationID)

	payload, ok := messages[0].Message.Payload.(events.CommunityMemberChanged)
	require.True(t, ok)
	assert.Equal(t, models.ChangeJoined, payload.Change)
	assert.Equal(t, models.StatusActive, payload.Status)
	assert.Equal(t, int64(1), payload.MemberVersion)
	assert.Nil(t, payload.SanctionID)
}

func TestAcceptInviteRaceWithoutStateChangeEmitsNothing(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	userID := uuid.New()

	inviteService, repository, publisher, _ := newInviteService()
	repository.On("GetInviteByCode", mock.Anything, "valid").Return(&models.Invite{
		ID:          uuid.New(),
		CommunityID: communityID,
		Code:        "valid",
	}, nil)
	repository.On("GetActiveBan", mock.Anything, communityID, userID).Return(nil, nil)
	repository.On("GetMembership", mock.Anything, communityID, userID).Return(nil, models.ErrMembershipNotFound)
	repository.On("JoinWithInvite", mock.Anything, communityID, userID, mock.Anything, "valid", frozenNow).
		Return(activeMembership(communityID, userID), false, nil)

	_, err := inviteService.Accept(context.Background(), userID, "valid", strings.Repeat("a", 32), uuid.NewString())

	require.NoError(t, err)
	assert.Empty(t, publisher.Messages())
}

func TestAcceptInvitePropagatesRepositoryFailure(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	userID := uuid.New()
	failure := errors.New("database is on fire")

	inviteService, repository, _, _ := newInviteService()
	repository.On("GetInviteByCode", mock.Anything, "valid").Return(&models.Invite{
		ID:          uuid.New(),
		CommunityID: communityID,
		Code:        "valid",
	}, nil)
	repository.On("GetActiveBan", mock.Anything, communityID, userID).Return(nil, nil)
	repository.On("GetMembership", mock.Anything, communityID, userID).Return(nil, models.ErrMembershipNotFound)
	repository.On("JoinWithInvite", mock.Anything, communityID, userID, mock.Anything, "valid", frozenNow).Return(nil, false, failure)

	_, err := inviteService.Accept(context.Background(), userID, "valid", strings.Repeat("a", 32), uuid.NewString())

	require.Error(t, err)
	assert.ErrorIs(t, err, failure)
}

func TestPreviewInviteReportsInBandInvalidity(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	expired := frozenNow.Add(-time.Hour)

	inviteService, repository, _, _ := newInviteService()
	repository.On("GetInviteByCode", mock.Anything, "expired").Return(&models.Invite{
		ID:          uuid.New(),
		CommunityID: communityID,
		Code:        "expired",
		ExpiresAt:   &expired,
	}, nil)
	repository.On("GetCommunity", mock.Anything, communityID).Return(&models.Community{ID: communityID, Name: "Go Gophers"}, nil)
	repository.On("CountActiveMembers", mock.Anything, communityID).Return(12, nil)

	preview, err := inviteService.Preview(context.Background(), "expired")

	require.NoError(t, err)
	assert.False(t, preview.Valid)
	require.NotNil(t, preview.InvalidReason)
	assert.Equal(t, models.InviteInvalidExpired, *preview.InvalidReason)
	assert.Equal(t, "Go Gophers", preview.Community.Name)
	assert.Equal(t, 12, preview.Community.MemberCount)
}

func TestPreviewInviteReturnsValidCommunity(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()

	inviteService, repository, _, _ := newInviteService()
	repository.On("GetInviteByCode", mock.Anything, "valid").Return(&models.Invite{
		ID:          uuid.New(),
		CommunityID: communityID,
		Code:        "valid",
	}, nil)
	repository.On("GetCommunity", mock.Anything, communityID).Return(&models.Community{ID: communityID, Name: "Go Gophers"}, nil)
	repository.On("CountActiveMembers", mock.Anything, communityID).Return(2, nil)

	preview, err := inviteService.Preview(context.Background(), "valid")

	require.NoError(t, err)
	assert.True(t, preview.Valid)
	assert.Nil(t, preview.InvalidReason)
}

func TestCreateInviteRequiresPermission(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	userID := uuid.New()
	community := &models.Community{ID: communityID, OwnerUserID: uuid.New()}

	inviteService, repository, _, _ := newInviteService()
	repository.On("GetCommunity", mock.Anything, communityID).Return(community, nil)
	repository.On("GetMembership", mock.Anything, communityID, userID).
		Return(activeMembership(communityID, userID), nil)
	repository.On("ListRoles", mock.Anything, communityID).Return(nil, nil)

	_, err := inviteService.Create(context.Background(), userID, communityID, &models.CreateInviteRequest{})

	require.Error(t, err)
	assert.True(t, errors.Is(err, models.ErrMissingPermission))
	assert.Equal(t, 403, mustAppError(t, err).HTTPStatus())
}

func TestCreateInviteForOwnerMintsCode(t *testing.T) {
	t.Parallel()

	communityID := uuid.New()
	ownerID := uuid.New()
	community := &models.Community{ID: communityID, OwnerUserID: ownerID}
	expiresIn := 3600
	maxUses := 5

	var created *models.Invite

	inviteService, repository, _, _ := newInviteService()
	repository.On("GetCommunity", mock.Anything, communityID).Return(community, nil)
	repository.On("GetMembership", mock.Anything, communityID, ownerID).Return(activeMembership(communityID, ownerID), nil)
	repository.On("ListRoles", mock.Anything, communityID).Return(nil, nil)
	repository.On("CreateInvite", mock.Anything, mock.AnythingOfType("*models.Invite")).
		Run(func(args mock.Arguments) { created = args.Get(1).(*models.Invite) }).
		Return(nil)

	invite, err := inviteService.Create(context.Background(), ownerID, communityID, &models.CreateInviteRequest{
		ExpiresInSeconds: &expiresIn,
		MaxUses:          &maxUses,
	})

	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, created.Code, invite.Code)
	assert.Equal(t, service.InviteCodeLength, len(invite.Code))
	assert.Equal(t, maxUses, *invite.MaxUses)
	require.NotNil(t, invite.ExpiresAt)
	assert.Equal(t, frozenNow.Add(time.Hour), *invite.ExpiresAt)
}

func TestInviteCodesAreUnambiguousAndUnique(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, 200)
	for i := 0; i < 200; i++ {
		code, err := service.NewInviteCode()
		require.NoError(t, err)
		require.Len(t, code, service.InviteCodeLength)

		for _, char := range code {
			assert.NotContains(t, "01lIO", string(char), "codes must avoid easily confused characters")
		}

		_, duplicate := seen[code]
		assert.False(t, duplicate, "codes must be unique")
		seen[code] = struct{}{}
	}
}

// mustAppError extracts the platform error shape of a service error.
func mustAppError(t *testing.T, err error) *models.Error {
	t.Helper()

	appErr, ok := models.AsError(err)
	require.True(t, ok, "expected a typed platform error, got %v", err)
	return appErr
}
