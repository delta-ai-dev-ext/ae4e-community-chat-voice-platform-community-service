package unit

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"community_service/internal/models"
	"community_service/internal/service"
)

// role builds a role with the given position and permissions.
func role(position int, permissions ...models.Permission) *models.Role {
	return &models.Role{
		ID:          uuid.New(),
		Name:        "role",
		Position:    position,
		Permissions: permissions,
	}
}

func TestEffectivePermissionsOwnerHoldsAllTenKeys(t *testing.T) {
	t.Parallel()

	effective := service.EffectivePermissions(true, nil)

	assert.Equal(t, models.AllPermissions, effective)
	assert.Len(t, effective, 10)
}

func TestEffectivePermissionsUnionsRolePermissions(t *testing.T) {
	t.Parallel()

	roles := []*models.Role{
		role(10, models.PermissionKickMembers, models.PermissionManageChannels),
		role(20, models.PermissionKickMembers, models.PermissionReviewAppeals),
		nil,
	}

	effective := service.EffectivePermissions(false, roles)

	assert.Equal(t, []models.Permission{
		models.PermissionManageChannels,
		models.PermissionKickMembers,
		models.PermissionReviewAppeals,
	}, effective)
}

func TestEffectivePermissionsEmptyForRolelessMember(t *testing.T) {
	t.Parallel()

	effective := service.EffectivePermissions(false, nil)

	assert.NotNil(t, effective)
	assert.Empty(t, effective)
}

func TestEffectivePermissionsIgnoresUnknownKeys(t *testing.T) {
	t.Parallel()

	roles := []*models.Role{role(1, "not_a_permission", models.PermissionBanMembers)}

	assert.Equal(t, []models.Permission{models.PermissionBanMembers}, service.EffectivePermissions(false, roles))
}

func TestHasPermissionCoversEveryPermissionKey(t *testing.T) {
	t.Parallel()

	for _, permission := range models.AllPermissions {
		permission := permission
		t.Run(string(permission), func(t *testing.T) {
			t.Parallel()

			assert.True(t, service.HasPermission(true, nil, permission), "owner holds every key")
			assert.True(t, service.HasPermission(false, []*models.Role{role(5, permission)}, permission))
			assert.False(t, service.HasPermission(false, nil, permission))
		})
	}
}

func TestRankPositionUsesLowestPosition(t *testing.T) {
	t.Parallel()

	position, ranked := service.RankPosition([]*models.Role{role(30), role(10), role(20)})
	require.True(t, ranked)
	assert.Equal(t, 10, position)

	_, ranked = service.RankPosition(nil)
	assert.False(t, ranked)
}

func TestOutranksHierarchyMatrix(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		actorOwner  bool
		actorRoles  []*models.Role
		targetOwner bool
		targetRoles []*models.Role
		want        bool
	}{
		{
			name:        "owner outranks every role holder",
			actorOwner:  true,
			targetRoles: []*models.Role{role(0)},
			want:        true,
		},
		{
			name:        "nothing outranks the owner",
			actorRoles:  []*models.Role{role(0)},
			targetOwner: true,
			want:        false,
		},
		{
			name:        "lower position outranks higher position",
			actorRoles:  []*models.Role{role(5)},
			targetRoles: []*models.Role{role(9)},
			want:        true,
		},
		{
			name:        "higher position does not outrank lower position",
			actorRoles:  []*models.Role{role(9)},
			targetRoles: []*models.Role{role(5)},
			want:        false,
		},
		{
			name:        "equal positions cannot act on each other",
			actorRoles:  []*models.Role{role(5)},
			targetRoles: []*models.Role{role(5)},
			want:        false,
		},
		{
			name:        "role holder outranks roleless member",
			actorRoles:  []*models.Role{role(99)},
			targetRoles: nil,
			want:        true,
		},
		{
			name:        "roleless member outranks nobody",
			actorRoles:  nil,
			targetRoles: []*models.Role{role(99)},
			want:        false,
		},
		{
			name:        "rank uses the actor's highest role",
			actorRoles:  []*models.Role{role(50), role(1)},
			targetRoles: []*models.Role{role(20)},
			want:        true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, service.Outranks(tc.actorOwner, tc.actorRoles, tc.targetOwner, tc.targetRoles))
		})
	}
}

func TestDecideAuthorizationDenialReasons(t *testing.T) {
	t.Parallel()

	actorID := uuid.New()
	targetID := uuid.New()

	managerRole := role(10, models.PermissionKickMembers)
	juniorRole := role(20, models.PermissionKickMembers)
	moderatorRole := role(30, models.PermissionManageCommunity)

	testCases := []struct {
		name      string
		input     service.AuthorizationInput
		wantAllow bool
		wantDeny  string
	}{
		{
			name: "actor is not a member",
			input: service.AuthorizationInput{
				ActorUserID: actorID,
				Permission:  models.PermissionKickMembers,
			},
			wantDeny: models.DenialActorNotMember,
		},
		{
			name: "actor lacks the permission",
			input: service.AuthorizationInput{
				ActorUserID:   actorID,
				ActorIsMember: true,
				ActorRoles:    []*models.Role{moderatorRole},
				Permission:    models.PermissionKickMembers,
			},
			wantDeny: models.DenialMissingPermission,
		},
		{
			name: "non targeted action is allowed with the permission",
			input: service.AuthorizationInput{
				ActorUserID:   actorID,
				ActorIsMember: true,
				ActorRoles:    []*models.Role{moderatorRole},
				Permission:    models.PermissionManageCommunity,
			},
			wantAllow: true,
		},
		{
			name: "actor targeting themselves",
			input: service.AuthorizationInput{
				ActorUserID:   actorID,
				ActorIsMember: true,
				ActorRoles:    []*models.Role{managerRole},
				Permission:    models.PermissionKickMembers,
				Target:        &service.AuthorizationTarget{UserID: actorID, IsMember: true},
			},
			wantDeny: models.DenialTargetIsSelf,
		},
		{
			name: "target is the owner",
			input: service.AuthorizationInput{
				ActorUserID:   actorID,
				ActorIsMember: true,
				ActorRoles:    []*models.Role{managerRole},
				Permission:    models.PermissionKickMembers,
				Target:        &service.AuthorizationTarget{UserID: targetID, IsOwner: true},
			},
			wantDeny: models.DenialTargetIsOwner,
		},
		{
			name: "target outranks actor",
			input: service.AuthorizationInput{
				ActorUserID:   actorID,
				ActorIsMember: true,
				ActorRoles:    []*models.Role{juniorRole},
				Permission:    models.PermissionKickMembers,
				Target:        &service.AuthorizationTarget{UserID: targetID, IsMember: true, Roles: []*models.Role{managerRole}},
			},
			wantDeny: models.DenialTargetOutranksActor,
		},
		{
			name: "actor outranks target and holds the permission",
			input: service.AuthorizationInput{
				ActorUserID:   actorID,
				ActorIsMember: true,
				ActorRoles:    []*models.Role{managerRole},
				Permission:    models.PermissionKickMembers,
				Target:        &service.AuthorizationTarget{UserID: targetID, IsMember: true, Roles: []*models.Role{juniorRole}},
			},
			wantAllow: true,
		},
		{
			name: "owner may act on anyone but themselves",
			input: service.AuthorizationInput{
				ActorUserID:  actorID,
				ActorIsOwner: true,
				Permission:   models.PermissionBanMembers,
				Target:       &service.AuthorizationTarget{UserID: targetID, IsMember: true, Roles: []*models.Role{managerRole}},
			},
			wantAllow: true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			allowed, denial := service.DecideAuthorization(tc.input)
			assert.Equal(t, tc.wantAllow, allowed)
			if tc.wantDeny == "" {
				assert.Nil(t, denial)
			} else {
				require.NotNil(t, denial)
				assert.Equal(t, tc.wantDeny, *denial)
			}
		})
	}
}
