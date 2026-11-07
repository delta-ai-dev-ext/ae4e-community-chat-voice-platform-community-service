package integration

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"community_service/internal/events"
	"community_service/internal/models"
)

// inviterRole grants the invite permission without ownership.
func inviterRole(communityID uuid.UUID) *models.Role {
	return &models.Role{
		ID:          uuid.New(),
		CommunityID: communityID,
		Name:        "Inviter",
		Position:    40,
		Permissions: []models.Permission{models.PermissionCreateInvites},
	}
}

func TestCreateInvite(t *testing.T) {
	t.Parallel()

	t.Run("permitted member mints a code", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		role := inviterRole(community.ID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID, role.ID), []*models.Role{role})

		var created *models.Invite
		rig.repo.On("CreateInvite", anyContext(), mock.AnythingOfType("*models.Invite")).
			Run(func(args mock.Arguments) { created = args.Get(1).(*models.Invite) }).
			Return(nil)

		response := rig.call(t, http.MethodPost, "/communities/"+community.ID.String()+"/invites",
			map[string]any{"expires_in_seconds": 3600, "max_uses": 3}, withHeader("Idempotency-Key", "key-1"))

		require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
		invite := decode[models.Invite](t, response)
		require.NotNil(t, created)
		assert.Equal(t, created.Code, invite.Code)
		assert.Len(t, invite.Code, 10)
		require.NotNil(t, invite.MaxUses)
		assert.Equal(t, 3, *invite.MaxUses)
		require.NotNil(t, invite.ExpiresAt)
		assert.Equal(t, testNow.Add(3600*1e9), *invite.ExpiresAt)
	})

	t.Run("expiry below the minimum is unprocessable", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)

		response := rig.call(t, http.MethodPost, "/communities/"+community.ID.String()+"/invites",
			map[string]any{"expires_in_seconds": 5})

		assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
		assert.Equal(t, string(models.CodeValidationFailed), errorResponse(t, response).Error)
	})

	t.Run("member without create_invites is forbidden", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		role := &models.Role{ID: uuid.New(), CommunityID: community.ID, Name: "Member", Position: 50,
			Permissions: []models.Permission{models.PermissionManageChannels}}
		rig.armAccess(community, seedMembership(community.ID, rig.userID, role.ID), []*models.Role{role})

		response := rig.call(t, http.MethodPost, "/communities/"+community.ID.String()+"/invites", map[string]any{})

		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.Equal(t, string(models.CodeMissingPermission), errorResponse(t, response).Error)
	})
}

func TestRevokeInvite(t *testing.T) {
	t.Parallel()

	rig := newRig(t)
	community, _, _ := seedCommunity(rig.userID)
	rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)
	rig.repo.On("RevokeInvite", anyContext(), community.ID, "abc123", testNow).
		Return(&models.Invite{ID: uuid.New(), CommunityID: community.ID, Code: "abc123"}, nil)

	response := rig.call(t, http.MethodDelete, "/communities/"+community.ID.String()+"/invites/abc123", nil)

	assert.Equal(t, http.StatusNoContent, response.Code)
}

func TestRevokeUnknownInviteIsNotFound(t *testing.T) {
	t.Parallel()

	rig := newRig(t)
	community, _, _ := seedCommunity(rig.userID)
	rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)
	rig.repo.On("RevokeInvite", anyContext(), community.ID, "nope", testNow).Return(nil, models.ErrInviteNotFound)

	response := rig.call(t, http.MethodDelete, "/communities/"+community.ID.String()+"/invites/nope", nil)

	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Equal(t, string(models.CodeInviteNotFound), errorResponse(t, response).Error)
}

func TestPreviewInvite(t *testing.T) {
	t.Parallel()

	t.Run("usable invite previews the community", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		rig.repo.On("GetInviteByCode", anyContext(), "abc123").
			Return(&models.Invite{ID: uuid.New(), CommunityID: community.ID, Code: "abc123"}, nil)
		rig.repo.On("GetCommunity", anyContext(), community.ID).Return(community, nil)
		rig.repo.On("CountActiveMembers", anyContext(), community.ID).Return(4, nil)

		response := rig.call(t, http.MethodGet, "/invites/abc123", nil)

		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		preview := decode[models.InvitePreviewResponse](t, response)
		assert.True(t, preview.Valid)
		assert.Nil(t, preview.InvalidReason)
		assert.Equal(t, community.Name, preview.Community.Name)
		assert.Equal(t, 4, preview.Community.MemberCount)
	})

	t.Run("revoked invite reports invalidity in band", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		revokedAt := testNow.Add(-1)
		rig.repo.On("GetInviteByCode", anyContext(), "abc123").
			Return(&models.Invite{ID: uuid.New(), CommunityID: community.ID, Code: "abc123", RevokedAt: &revokedAt}, nil)
		rig.repo.On("GetCommunity", anyContext(), community.ID).Return(community, nil)
		rig.repo.On("CountActiveMembers", anyContext(), community.ID).Return(1, nil)

		response := rig.call(t, http.MethodGet, "/invites/abc123", nil)

		require.Equal(t, http.StatusOK, response.Code)
		preview := decode[models.InvitePreviewResponse](t, response)
		assert.False(t, preview.Valid)
		require.NotNil(t, preview.InvalidReason)
		assert.Equal(t, models.InviteInvalidRevoked, *preview.InvalidReason)
	})

	t.Run("unknown code is 404", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		rig.repo.On("GetInviteByCode", anyContext(), "nope").Return(nil, models.ErrInviteNotFound)

		response := rig.call(t, http.MethodGet, "/invites/nope", nil)

		assert.Equal(t, http.StatusNotFound, response.Code)
	})
}

func TestAcceptInviteHTTPStatuses(t *testing.T) {
	t.Parallel()

	t.Run("successful join is 201 with the membership", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		joined := seedMembership(community.ID, rig.userID)

		rig.repo.On("GetInviteByCode", anyContext(), "abc123").
			Return(&models.Invite{ID: uuid.New(), CommunityID: community.ID, Code: "abc123"}, nil)
		rig.repo.On("GetActiveBan", anyContext(), community.ID, rig.userID).Return(nil, nil)
		rig.repo.On("GetMembership", anyContext(), community.ID, rig.userID).Return(nil, models.ErrMembershipNotFound)
		rig.repo.On("JoinWithInvite", anyContext(), community.ID, rig.userID, mock.Anything, "abc123", testNow).
			Return(joined, true, nil)

		response := rig.call(t, http.MethodPost, "/invites/abc123/accept", nil)

		require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
		membership := decode[models.Membership](t, response)
		assert.Equal(t, community.ID, membership.CommunityID)
		assert.Equal(t, models.StatusActive, membership.Status)

		published := rig.publisher.Payloads(events.EventTypeCommunityMemberChanged)
		require.Len(t, published, 1)
		assert.Equal(t, models.ChangeJoined, published[0].(events.CommunityMemberChanged).Change)
	})

	t.Run("banned caller is 403", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		rig.repo.On("GetInviteByCode", anyContext(), "abc123").
			Return(&models.Invite{ID: uuid.New(), CommunityID: community.ID, Code: "abc123"}, nil)
		rig.repo.On("GetActiveBan", anyContext(), community.ID, rig.userID).
			Return(&models.Ban{ID: uuid.New(), CommunityID: community.ID, UserID: rig.userID, BannedAt: testNow}, nil)

		response := rig.call(t, http.MethodPost, "/invites/abc123/accept", nil)

		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.Equal(t, string(models.CodeBanned), errorResponse(t, response).Error)
	})

	t.Run("expired invite is 410", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		expired := testNow.Add(-1)
		rig.repo.On("GetInviteByCode", anyContext(), "abc123").
			Return(&models.Invite{ID: uuid.New(), CommunityID: community.ID, Code: "abc123", ExpiresAt: &expired}, nil)

		response := rig.call(t, http.MethodPost, "/invites/abc123/accept", nil)

		assert.Equal(t, http.StatusGone, response.Code)
		assert.Equal(t, string(models.CodeInviteExpired), errorResponse(t, response).Error)
	})

	t.Run("unknown invite is 404", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		rig.repo.On("GetInviteByCode", anyContext(), "nope").Return(nil, models.ErrInviteNotFound)

		response := rig.call(t, http.MethodPost, "/invites/nope/accept", nil)

		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Empty(t, rig.publisher.Messages())
	})
}

func TestLeaveCommunity(t *testing.T) {
	t.Parallel()

	t.Run("member leaves and an EVT-002 left is emitted", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		membership := seedMembership(community.ID, rig.userID)
		rig.armAccess(community, membership, nil)

		removed := *membership
		removed.Status = models.StatusRemoved
		removed.MemberVersion = 2
		rig.repo.On("RemoveMembership", anyContext(), community.ID, rig.userID, testNow).Return(&removed, nil)

		response := rig.call(t, http.MethodPost, "/communities/"+community.ID.String()+"/leave", nil)

		require.Equal(t, http.StatusNoContent, response.Code)

		published := rig.publisher.Payloads(events.EventTypeCommunityMemberChanged)
		require.Len(t, published, 1)
		payload := published[0].(events.CommunityMemberChanged)
		assert.Equal(t, models.ChangeLeft, payload.Change)
		assert.Equal(t, models.StatusRemoved, payload.Status)
		assert.Equal(t, int64(2), payload.MemberVersion)
	})

	t.Run("owner cannot leave", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)

		response := rig.call(t, http.MethodPost, "/communities/"+community.ID.String()+"/leave", nil)

		assert.Equal(t, http.StatusConflict, response.Code)
		assert.Equal(t, string(models.CodeOwnerCannotLeave), errorResponse(t, response).Error)
	})

	t.Run("non member cannot leave", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		rig.armAccess(community, nil, nil)

		response := rig.call(t, http.MethodPost, "/communities/"+community.ID.String()+"/leave", nil)

		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Equal(t, string(models.CodeMembershipNotFound), errorResponse(t, response).Error)
	})
}

func TestListMembers(t *testing.T) {
	t.Parallel()

	t.Run("member pages active members", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)

		first := seedMembership(community.ID, uuid.New())
		second := seedMembership(community.ID, uuid.New())
		rig.repo.On("ListMembers", anyContext(), community.ID, 2, 0).Return([]*models.Membership{first, second}, nil)

		response := rig.call(t, http.MethodGet, "/communities/"+community.ID.String()+"/members?limit=1", nil)

		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		body := decode[models.MemberListResponse](t, response)
		require.Len(t, body.Members, 1)
		assert.Equal(t, first.UserID, body.Members[0].UserID)
		require.NotNil(t, body.NextCursor)
	})

	t.Run("non member is forbidden", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		rig.armAccess(community, nil, nil)

		response := rig.call(t, http.MethodGet, "/communities/"+community.ID.String()+"/members", nil)

		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.Equal(t, string(models.CodeNotAMember), errorResponse(t, response).Error)
	})
}

func TestRoleEndpoints(t *testing.T) {
	t.Parallel()

	t.Run("create role returns 201", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)
		rig.repo.On("CreateRole", anyContext(), mock.AnythingOfType("*models.Role")).Return(nil)

		response := rig.call(t, http.MethodPost, "/communities/"+community.ID.String()+"/roles", map[string]any{
			"name":        "Moderator",
			"permissions": []string{"kick_members", "manage_roles"},
			"position":    10,
		})

		require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
		role := decode[models.Role](t, response)
		assert.Equal(t, "Moderator", role.Name)
		assert.Equal(t, 10, role.Position)
		assert.ElementsMatch(t, []models.Permission{models.PermissionKickMembers, models.PermissionManageRoles}, role.Permissions)
	})

	t.Run("unknown permission key is unprocessable", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)

		response := rig.call(t, http.MethodPost, "/communities/"+community.ID.String()+"/roles", map[string]any{
			"name":        "Wizard",
			"permissions": []string{"delete_everything"},
			"position":    10,
		})

		assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
		assert.Equal(t, string(models.CodeValidationFailed), errorResponse(t, response).Error)
	})

	t.Run("duplicate position is a conflict", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)
		rig.repo.On("CreateRole", anyContext(), mock.AnythingOfType("*models.Role")).Return(models.ErrRolePositionConflict)

		response := rig.call(t, http.MethodPost, "/communities/"+community.ID.String()+"/roles", map[string]any{
			"name":        "Clashing",
			"permissions": []string{},
			"position":    10,
		})

		assert.Equal(t, http.StatusConflict, response.Code)
		assert.Equal(t, string(models.CodeRolePositionConflict), errorResponse(t, response).Error)
	})

	t.Run("list roles requires membership", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		rig.armAccess(community, nil, nil)

		response := rig.call(t, http.MethodGet, "/communities/"+community.ID.String()+"/roles", nil)

		assert.Equal(t, http.StatusForbidden, response.Code)
	})

	t.Run("list roles returns rank order", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		membership := seedMembership(community.ID, rig.userID)
		roles := []*models.Role{
			{ID: uuid.New(), CommunityID: community.ID, Name: "Admin", Position: 10},
			{ID: uuid.New(), CommunityID: community.ID, Name: "Member", Position: 50},
		}
		rig.armAccess(community, membership, roles)

		response := rig.call(t, http.MethodGet, "/communities/"+community.ID.String()+"/roles", nil)

		require.Equal(t, http.StatusOK, response.Code)
		body := decode[models.RoleListResponse](t, response)
		require.Len(t, body.Roles, 2)
		assert.Equal(t, 10, body.Roles[0].Position)
	})

	t.Run("update role emits roles_changed for holders", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)

		role := &models.Role{ID: uuid.New(), CommunityID: community.ID, Name: "Moderator", Position: 10,
			Permissions: []models.Permission{models.PermissionKickMembers}}
		holder := seedMembership(community.ID, uuid.New(), role.ID)
		holder.MemberVersion = 5

		rig.repo.On("UpdateRole", anyContext(), community.ID, role.ID, mock.Anything, testNow).
			Return(role, []*models.Membership{holder}, nil)

		response := rig.call(t, http.MethodPatch, "/communities/"+community.ID.String()+"/roles/"+role.ID.String(),
			map[string]any{"permissions": []string{"kick_members", "ban_members"}})

		require.Equal(t, http.StatusOK, response.Code, response.Body.String())

		published := rig.publisher.Payloads(events.EventTypeCommunityMemberChanged)
		require.Len(t, published, 1)
		payload := published[0].(events.CommunityMemberChanged)
		assert.Equal(t, models.ChangeRolesChanged, payload.Change)
		assert.Equal(t, holder.UserID, payload.UserID)
	})

	t.Run("delete role returns 204", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)

		role := &models.Role{ID: uuid.New(), CommunityID: community.ID, Name: "Moderator", Position: 10}
		rig.repo.On("DeleteRole", anyContext(), community.ID, role.ID).Return(role, []*models.Membership{}, nil)

		response := rig.call(t, http.MethodDelete, "/communities/"+community.ID.String()+"/roles/"+role.ID.String(), nil)

		assert.Equal(t, http.StatusNoContent, response.Code)
	})

	t.Run("delete unknown role is 404", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(rig.userID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID), nil)
		roleID := uuid.New()
		rig.repo.On("DeleteRole", anyContext(), community.ID, roleID).Return(nil, nil, models.ErrRoleNotFound)

		response := rig.call(t, http.MethodDelete, "/communities/"+community.ID.String()+"/roles/"+roleID.String(), nil)

		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Equal(t, string(models.CodeRoleNotFound), errorResponse(t, response).Error)
	})
}

func TestSetMemberRolesHierarchy(t *testing.T) {
	t.Parallel()

	managerRole := func(communityID uuid.UUID) *models.Role {
		return &models.Role{ID: uuid.New(), CommunityID: communityID, Name: "Manager", Position: 10,
			Permissions: []models.Permission{models.PermissionManageRoles}}
	}
	juniorRole := func(communityID uuid.UUID) *models.Role {
		return &models.Role{ID: uuid.New(), CommunityID: communityID, Name: "Junior", Position: 40,
			Permissions: []models.Permission{models.PermissionManageRoles}}
	}

	t.Run("assigning roles returns the updated member", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		manager := managerRole(community.ID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID, manager.ID), []*models.Role{manager})

		targetID := uuid.New()
		target := seedMembership(community.ID, targetID)
		rig.repo.On("GetMembership", anyContext(), community.ID, targetID).Return(target, nil)

		updated := seedMembership(community.ID, targetID, manager.ID)
		updated.MemberVersion = 2
		rig.repo.On("SetMemberRoles", anyContext(), community.ID, targetID, []uuid.UUID{manager.ID}, testNow).Return(updated, nil)
		rig.repo.On("ListRoles", anyContext(), community.ID).Return([]*models.Role{manager}, nil)

		response := rig.call(t, http.MethodPut,
			"/communities/"+community.ID.String()+"/members/"+targetID.String()+"/roles",
			map[string]any{"role_ids": []string{manager.ID.String()}})

		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		member := decode[models.Member](t, response)
		assert.Equal(t, targetID, member.UserID)
		assert.Equal(t, []uuid.UUID{manager.ID}, member.RoleIDs)

		published := rig.publisher.Payloads(events.EventTypeCommunityMemberChanged)
		require.Len(t, published, 1)
		assert.Equal(t, models.ChangeRolesChanged, published[0].(events.CommunityMemberChanged).Change)
	})

	t.Run("acting on yourself is forbidden", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		manager := managerRole(community.ID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID, manager.ID), []*models.Role{manager})

		response := rig.call(t, http.MethodPut,
			"/communities/"+community.ID.String()+"/members/"+rig.userID.String()+"/roles",
			map[string]any{"role_ids": []string{}})

		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.Equal(t, string(models.CodeTargetIsSelf), errorResponse(t, response).Error)
	})

	t.Run("acting on the owner is forbidden", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		ownerID := uuid.New()
		community, _, _ := seedCommunity(ownerID)
		manager := managerRole(community.ID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID, manager.ID), []*models.Role{manager})

		response := rig.call(t, http.MethodPut,
			"/communities/"+community.ID.String()+"/members/"+ownerID.String()+"/roles",
			map[string]any{"role_ids": []string{}})

		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.Equal(t, string(models.CodeTargetIsOwner), errorResponse(t, response).Error)
	})

	t.Run("acting on a higher ranked member is forbidden", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		junior := juniorRole(community.ID)
		senior := managerRole(community.ID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID, junior.ID), []*models.Role{junior, senior})

		targetID := uuid.New()
		target := seedMembership(community.ID, targetID, senior.ID)
		rig.repo.On("GetMembership", anyContext(), community.ID, targetID).Return(target, nil)

		response := rig.call(t, http.MethodPut,
			"/communities/"+community.ID.String()+"/members/"+targetID.String()+"/roles",
			map[string]any{"role_ids": []string{junior.ID.String()}})

		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.Equal(t, string(models.CodeTargetOutranksActor), errorResponse(t, response).Error)
	})

	t.Run("unknown member is 404", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		manager := managerRole(community.ID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID, manager.ID), []*models.Role{manager})

		targetID := uuid.New()
		rig.repo.On("GetMembership", anyContext(), community.ID, targetID).Return(nil, models.ErrMembershipNotFound)

		response := rig.call(t, http.MethodPut,
			"/communities/"+community.ID.String()+"/members/"+targetID.String()+"/roles",
			map[string]any{"role_ids": []string{}})

		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Equal(t, string(models.CodeMemberNotFound), errorResponse(t, response).Error)
	})

	t.Run("missing role_ids is unprocessable", func(t *testing.T) {
		t.Parallel()

		rig := newRig(t)
		community, _, _ := seedCommunity(uuid.New())
		manager := managerRole(community.ID)
		rig.armAccess(community, seedMembership(community.ID, rig.userID, manager.ID), []*models.Role{manager})

		response := rig.call(t, http.MethodPut,
			"/communities/"+community.ID.String()+"/members/"+uuid.New().String()+"/roles",
			map[string]any{})

		assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
		assert.Equal(t, string(models.CodeValidationFailed), errorResponse(t, response).Error)
	})
}
