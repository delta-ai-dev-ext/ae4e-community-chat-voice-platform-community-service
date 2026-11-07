package unit

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"community_service/internal/models"
)

func TestCreateCommunityRequestValidation(t *testing.T) {
	t.Parallel()

	tooLong := string(make([]rune, 501))

	testCases := []struct {
		name    string
		request models.CreateCommunityRequest
		wantErr bool
	}{
		{name: "valid name", request: models.CreateCommunityRequest{Name: "Go Gophers"}},
		{name: "blank name", request: models.CreateCommunityRequest{Name: "   "}, wantErr: true},
		{name: "description within bounds", request: models.CreateCommunityRequest{Name: "x", Description: pointer("hello")}},
		{name: "description too long", request: models.CreateCommunityRequest{Name: "x", Description: &tooLong}, wantErr: true},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.request.Validate()
			if tc.wantErr {
				require.NotNil(t, err)
				assert.Equal(t, models.CodeValidationFailed, err.Code)
				return
			}
			assert.Nil(t, err)
		})
	}
}

func TestUpdateCommunityRequestValidation(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		request models.UpdateCommunityRequest
		wantErr bool
	}{
		{name: "empty patch", request: models.UpdateCommunityRequest{}, wantErr: true},
		{name: "name only", request: models.UpdateCommunityRequest{Name: pointer("Renamed")}},
		{
			name:    "clearing the description",
			request: models.UpdateCommunityRequest{Description: models.OptionalString{Set: true}},
		},
		{
			name:    "null icon url",
			request: models.UpdateCommunityRequest{IconURL: models.OptionalString{Set: true}},
		},
		{
			name:    "relative icon url",
			request: models.UpdateCommunityRequest{IconURL: models.OptionalString{Set: true, Value: pointer("/icon.png")}},
			wantErr: true,
		},
		{
			name:    "blank name",
			request: models.UpdateCommunityRequest{Name: pointer("  ")},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.request.Validate()
			if tc.wantErr {
				require.NotNil(t, err)
				assert.Equal(t, models.CodeValidationFailed, err.Code)
				return
			}
			assert.Nil(t, err)
		})
	}
}

func TestCreateChannelRequestValidation(t *testing.T) {
	t.Parallel()

	capacity := func(value int) *int { return &value }

	testCases := []struct {
		name    string
		request models.CreateChannelRequest
		wantErr bool
	}{
		{name: "text channel", request: models.CreateChannelRequest{Name: "general", Kind: "text"}},
		{name: "voice channel with capacity", request: models.CreateChannelRequest{Name: "Stage", Kind: "voice", Capacity: capacity(4)}},
		{name: "voice channel at the bounds", request: models.CreateChannelRequest{Name: "Stage", Kind: "voice", Capacity: capacity(10)}},
		{name: "unknown kind", request: models.CreateChannelRequest{Name: "x", Kind: "video"}, wantErr: true},
		{name: "blank name", request: models.CreateChannelRequest{Name: " ", Kind: "text"}, wantErr: true},
		{name: "capacity on text", request: models.CreateChannelRequest{Name: "x", Kind: "text", Capacity: capacity(3)}, wantErr: true},
		{name: "capacity too large", request: models.CreateChannelRequest{Name: "x", Kind: "voice", Capacity: capacity(11)}, wantErr: true},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.request.Validate()
			if tc.wantErr {
				require.NotNil(t, err)
				return
			}
			assert.Nil(t, err)
		})
	}
}

func TestUpdateChannelRequestValidation(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		request models.UpdateChannelRequest
		wantErr bool
	}{
		{name: "empty patch", request: models.UpdateChannelRequest{}, wantErr: true},
		{name: "rename", request: models.UpdateChannelRequest{Name: pointer("renamed")}},
		{name: "reorder to zero", request: models.UpdateChannelRequest{Position: pointer(0)}},
		{name: "negative position", request: models.UpdateChannelRequest{Position: pointer(-1)}, wantErr: true},
		{name: "capacity out of range", request: models.UpdateChannelRequest{Capacity: models.OptionalInt{Set: true, Value: pointer(0)}}, wantErr: true},
		{name: "null capacity passes the boundary check", request: models.UpdateChannelRequest{Capacity: models.OptionalInt{Set: true}}},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.request.Validate()
			if tc.wantErr {
				require.NotNil(t, err)
				return
			}
			assert.Nil(t, err)
		})
	}
}

func TestInviteAndRoleRequestValidation(t *testing.T) {
	t.Parallel()

	position := 10
	expiresIn := 60
	tooShort := 59
	maxUses := 1
	badMaxUses := 0

	assert.Nil(t, (&models.CreateInviteRequest{ExpiresInSeconds: &expiresIn, MaxUses: &maxUses}).Validate())
	assert.NotNil(t, (&models.CreateInviteRequest{ExpiresInSeconds: &tooShort}).Validate())
	assert.NotNil(t, (&models.CreateInviteRequest{MaxUses: &badMaxUses}).Validate())

	assert.Nil(t, (&models.CreateRoleRequest{Name: "Moderator", Permissions: []string{"kick_members"}, Position: &position}).Validate())
	assert.NotNil(t, (&models.CreateRoleRequest{Name: "x", Permissions: nil, Position: &position}).Validate())
	assert.NotNil(t, (&models.CreateRoleRequest{Name: "x", Permissions: []string{"nope"}, Position: &position}).Validate())
	assert.NotNil(t, (&models.CreateRoleRequest{Name: "x", Permissions: []string{}, Position: nil}).Validate())

	negative := -1
	assert.Nil(t, (&models.UpdateRoleRequest{Permissions: &[]string{"ban_members"}}).Validate())
	assert.NotNil(t, (&models.UpdateRoleRequest{Position: &negative}).Validate())
	assert.NotNil(t, (&models.UpdateRoleRequest{}).Validate())

	assert.Nil(t, (&models.SetMemberRolesRequest{RoleIDs: []uuid.UUID{uuid.New()}}).Validate())
	assert.Nil(t, (&models.SetMemberRolesRequest{RoleIDs: []uuid.UUID{}}).Validate())
	assert.NotNil(t, (&models.SetMemberRolesRequest{}).Validate())
	assert.NotNil(t, (&models.SetMemberRolesRequest{RoleIDs: []uuid.UUID{uuid.Nil}}).Validate())
}

func TestInternalContractValidation(t *testing.T) {
	t.Parallel()

	actorID := uuid.New()
	targetID := uuid.New()

	assert.Nil(t, (&models.AuthorizationCheckRequest{ActorUserID: actorID, Permission: models.PermissionKickMembers}).Validate())
	assert.Nil(t, (&models.AuthorizationCheckRequest{ActorUserID: actorID, Permission: models.PermissionKickMembers, TargetUserID: &targetID}).Validate())

	unknown := &models.AuthorizationCheckRequest{ActorUserID: actorID, Permission: "teleport"}
	appErr := unknown.Validate()
	require.NotNil(t, appErr)
	assert.Equal(t, models.CodeInvalidRequest, appErr.Code, "an unknown permission is a 400, never a denial")

	assert.NotNil(t, (&models.AuthorizationCheckRequest{Permission: models.PermissionKickMembers}).Validate())

	nilUser := &models.AuthorizationCheckRequest{ActorUserID: actorID, Permission: models.PermissionKickMembers, TargetUserID: &uuid.Nil}
	assert.NotNil(t, nilUser.Validate())

	assert.Nil(t, (&models.VoiceAccessCheckRequest{UserID: actorID}).Validate())
	assert.NotNil(t, (&models.VoiceAccessCheckRequest{}).Validate())
}

func TestOptionalFieldsDistinguishAbsentFromNull(t *testing.T) {
	t.Parallel()

	type patch struct {
		Name        models.OptionalString `json:"name"`
		Position    models.OptionalInt    `json:"position"`
		Description *string               `json:"description"`
	}

	var decoded patch
	require.NoError(t, json.Unmarshal([]byte(`{"name":"renamed","position":null}`), &decoded))

	assert.True(t, decoded.Name.Set)
	require.NotNil(t, decoded.Name.Value)
	assert.Equal(t, "renamed", *decoded.Name.Value)

	assert.True(t, decoded.Position.Set)
	assert.Nil(t, decoded.Position.Value)

	empty := patch{}
	require.NoError(t, json.Unmarshal([]byte(`{}`), &empty))
	assert.False(t, empty.Name.Set)
	assert.False(t, empty.Position.Set)

	assert.Error(t, json.Unmarshal([]byte(`{"name":42}`), &empty))
	assert.Error(t, json.Unmarshal([]byte(`{"position":"seven"}`), &empty))

	encoded, err := json.Marshal(models.OptionalString{Set: true, Value: pointer("value")})
	require.NoError(t, err)
	assert.JSONEq(t, `"value"`, string(encoded))
}

func TestPaginationCursors(t *testing.T) {
	t.Parallel()

	limit := 25
	page, appErr := models.DecodePageQuery("", &limit)
	require.Nil(t, appErr)
	assert.Equal(t, 25, page.Limit)
	assert.Equal(t, 0, page.Offset)

	defaultPage, appErr := models.DecodePageQuery("", nil)
	require.Nil(t, appErr)
	assert.Equal(t, models.DefaultPageLimit, defaultPage.Limit)

	cursor := models.EncodeCursor(50)
	decoded, appErr := models.DecodePageQuery(cursor, nil)
	require.Nil(t, appErr)
	assert.Equal(t, 50, decoded.Offset)

	tooSmall := 0
	_, appErr = models.DecodePageQuery("", &tooSmall)
	require.NotNil(t, appErr)
	assert.Equal(t, models.CodeInvalidRequest, appErr.Code)

	tooLarge := models.MaxPageLimit + 1
	_, appErr = models.DecodePageQuery("", &tooLarge)
	require.NotNil(t, appErr)

	_, appErr = models.DecodePageQuery("not-base64!!", nil)
	require.NotNil(t, appErr)

	negativeCursor := base64.RawURLEncoding.EncodeToString([]byte("v1:-5"))
	_, appErr = models.DecodePageQuery(negativeCursor, nil)
	require.NotNil(t, appErr)
}

func TestErrorHelpers(t *testing.T) {
	t.Parallel()

	cause := errors.New("socket closed")

	wrapped := models.Wrap(models.ErrCommunityNotFound, cause)
	assert.Equal(t, models.CodeCommunityNotFound, wrapped.Code)
	assert.Equal(t, 404, wrapped.HTTPStatus())
	assert.ErrorIs(t, wrapped, cause)
	assert.ErrorIs(t, wrapped, models.ErrCommunityNotFound, "sentinels match by code")
	assert.NotSame(t, models.ErrCommunityNotFound, wrapped, "the shared sentinel is never mutated")

	detailed := models.WithDetail(models.ErrValidationFailed, "name is required")
	assert.Equal(t, models.CodeValidationFailed, detailed.Code)
	assert.Equal(t, "name is required", detailed.Detail)
	assert.ErrorIs(t, detailed, models.ErrValidationFailed, "a detailed copy still matches its sentinel")
	assert.NotErrorIs(t, detailed, models.ErrCommunityNotFound)

	plain := errors.New("plain")
	_, ok := models.AsError(plain)
	assert.False(t, ok)

	extracted, ok := models.AsError(detailed)
	require.True(t, ok)
	assert.Equal(t, models.CodeValidationFailed, extracted.Code)

	assert.Equal(t, 500, (&models.Error{}).HTTPStatus())
	assert.Contains(t, wrapped.Error(), "socket closed")
	assert.Contains(t, models.ErrInternal.Error(), "internal_error")
}

func TestMutedNow(t *testing.T) {
	t.Parallel()

	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)

	assert.False(t, models.MutedNow(nil, time.Now()))
	assert.False(t, models.MutedNow(&models.Membership{}, time.Now()))
	assert.True(t, models.MutedNow(&models.Membership{MutedUntil: &future}, time.Now()))
	assert.False(t, models.MutedNow(&models.Membership{MutedUntil: &past}, time.Now()))
}

func TestPermissionKeySet(t *testing.T) {
	t.Parallel()

	assert.Len(t, models.AllPermissions, 10)
	for _, permission := range models.AllPermissions {
		assert.True(t, models.ValidPermission(permission))
	}
	assert.False(t, models.ValidPermission("god_mode"))
	assert.Equal(t, []models.Permission{"god_mode"}, models.InvalidPermissions([]models.Permission{models.PermissionKickMembers, "god_mode"}))
	assert.Empty(t, models.InvalidPermissions(models.AllPermissions))
}

func TestMemberSnapshotHelpers(t *testing.T) {
	t.Parallel()

	active := &models.Membership{Status: models.StatusActive}
	removed := &models.Membership{Status: models.StatusRemoved}

	assert.True(t, active.IsActive())
	assert.False(t, removed.IsActive())

	assert.Equal(t, []uuid.UUID{}, models.RoleIDsOrEmpty(nil))
	assert.Empty(t, models.PermissionsOrEmpty(nil))

	member := active.AsMember()
	assert.Equal(t, active.UserID, member.UserID)
	assert.Equal(t, active.Status, member.Status)
	assert.Equal(t, []uuid.UUID{}, member.RoleIDs)
}
