package mocks

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"community_service/internal/models"
	"community_service/internal/repository"
)

// errPublishFailed is the injected publish failure of Publisher.
var errPublishFailed = errors.New("mocks: publish failed")

// Repository is a testify mock of every persistence port. Unit tests arm exactly
// the calls they expect, which makes dedupe, stale-version and error-mapping
// branches explicit.
type Repository struct {
	mock.Mock
}

// CreateCommunity records the call and returns the armed error.
func (m *Repository) CreateCommunity(ctx context.Context, community *models.Community, ownerMembership *models.Membership, defaultChannels []*models.Channel) error {
	args := m.Called(ctx, community, ownerMembership, defaultChannels)
	return args.Error(0)
}

// GetCommunity records the call and returns the armed community.
func (m *Repository) GetCommunity(ctx context.Context, communityID uuid.UUID) (*models.Community, error) {
	args := m.Called(ctx, communityID)
	community, _ := args.Get(0).(*models.Community)
	return community, args.Error(1)
}

// UpdateCommunity records the call and returns the armed community.
func (m *Repository) UpdateCommunity(ctx context.Context, communityID uuid.UUID, patch models.CommunityPatch, now time.Time) (*models.Community, error) {
	args := m.Called(ctx, communityID, patch, now)
	community, _ := args.Get(0).(*models.Community)
	return community, args.Error(1)
}

// ListCommunitiesByMember records the call and returns the armed communities.
func (m *Repository) ListCommunitiesByMember(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*models.Community, error) {
	args := m.Called(ctx, userID, limit, offset)
	communities, _ := args.Get(0).([]*models.Community)
	return communities, args.Error(1)
}

// CountActiveMembers records the call and returns the armed count.
func (m *Repository) CountActiveMembers(ctx context.Context, communityID uuid.UUID) (int, error) {
	args := m.Called(ctx, communityID)
	return args.Int(0), args.Error(1)
}

// ListChannels records the call and returns the armed channels.
func (m *Repository) ListChannels(ctx context.Context, communityID uuid.UUID) ([]*models.Channel, error) {
	args := m.Called(ctx, communityID)
	channels, _ := args.Get(0).([]*models.Channel)
	return channels, args.Error(1)
}

// ListVoiceChannels records the call and returns the armed channels.
func (m *Repository) ListVoiceChannels(ctx context.Context, communityID uuid.UUID) ([]*models.Channel, error) {
	args := m.Called(ctx, communityID)
	channels, _ := args.Get(0).([]*models.Channel)
	return channels, args.Error(1)
}

// GetChannel records the call and returns the armed channel.
func (m *Repository) GetChannel(ctx context.Context, communityID, channelID uuid.UUID) (*models.Channel, error) {
	args := m.Called(ctx, communityID, channelID)
	channel, _ := args.Get(0).(*models.Channel)
	return channel, args.Error(1)
}

// CountChannels records the call and returns the armed count.
func (m *Repository) CountChannels(ctx context.Context, communityID uuid.UUID) (int, error) {
	args := m.Called(ctx, communityID)
	return args.Int(0), args.Error(1)
}

// CreateChannel records the call and returns the armed error.
func (m *Repository) CreateChannel(ctx context.Context, channel *models.Channel) error {
	args := m.Called(ctx, channel)
	return args.Error(0)
}

// UpdateChannel records the call and returns the armed channel.
func (m *Repository) UpdateChannel(ctx context.Context, communityID, channelID uuid.UUID, patch models.ChannelPatch, now time.Time) (*models.Channel, error) {
	args := m.Called(ctx, communityID, channelID, patch, now)
	channel, _ := args.Get(0).(*models.Channel)
	return channel, args.Error(1)
}

// DeleteChannel records the call and returns the armed channel.
func (m *Repository) DeleteChannel(ctx context.Context, communityID, channelID uuid.UUID) (*models.Channel, error) {
	args := m.Called(ctx, communityID, channelID)
	channel, _ := args.Get(0).(*models.Channel)
	return channel, args.Error(1)
}

// CreateInvite records the call and returns the armed error.
func (m *Repository) CreateInvite(ctx context.Context, invite *models.Invite) error {
	args := m.Called(ctx, invite)
	return args.Error(0)
}

// GetInviteByCode records the call and returns the armed invite.
func (m *Repository) GetInviteByCode(ctx context.Context, code string) (*models.Invite, error) {
	args := m.Called(ctx, code)
	invite, _ := args.Get(0).(*models.Invite)
	return invite, args.Error(1)
}

// RevokeInvite records the call and returns the armed invite.
func (m *Repository) RevokeInvite(ctx context.Context, communityID uuid.UUID, code string, now time.Time) (*models.Invite, error) {
	args := m.Called(ctx, communityID, code, now)
	invite, _ := args.Get(0).(*models.Invite)
	return invite, args.Error(1)
}

// JoinWithInvite records the call and returns the armed membership.
func (m *Repository) JoinWithInvite(ctx context.Context, communityID, userID, membershipID uuid.UUID, code string, now time.Time) (*models.Membership, bool, error) {
	args := m.Called(ctx, communityID, userID, membershipID, code, now)
	membership, _ := args.Get(0).(*models.Membership)
	return membership, args.Bool(1), args.Error(2)
}

// GetMembership records the call and returns the armed membership.
func (m *Repository) GetMembership(ctx context.Context, communityID, userID uuid.UUID) (*models.Membership, error) {
	args := m.Called(ctx, communityID, userID)
	membership, _ := args.Get(0).(*models.Membership)
	return membership, args.Error(1)
}

// ListMembers records the call and returns the armed memberships.
func (m *Repository) ListMembers(ctx context.Context, communityID uuid.UUID, limit, offset int) ([]*models.Membership, error) {
	args := m.Called(ctx, communityID, limit, offset)
	memberships, _ := args.Get(0).([]*models.Membership)
	return memberships, args.Error(1)
}

// RemoveMembership records the call and returns the armed membership.
func (m *Repository) RemoveMembership(ctx context.Context, communityID, userID uuid.UUID, now time.Time) (*models.Membership, error) {
	args := m.Called(ctx, communityID, userID, now)
	membership, _ := args.Get(0).(*models.Membership)
	return membership, args.Error(1)
}

// GetActiveBan records the call and returns the armed ban.
func (m *Repository) GetActiveBan(ctx context.Context, communityID, userID uuid.UUID) (*models.Ban, error) {
	args := m.Called(ctx, communityID, userID)
	ban, _ := args.Get(0).(*models.Ban)
	return ban, args.Error(1)
}

// ListRoles records the call and returns the armed roles.
func (m *Repository) ListRoles(ctx context.Context, communityID uuid.UUID) ([]*models.Role, error) {
	args := m.Called(ctx, communityID)
	roles, _ := args.Get(0).([]*models.Role)
	return roles, args.Error(1)
}

// GetRole records the call and returns the armed role.
func (m *Repository) GetRole(ctx context.Context, communityID, roleID uuid.UUID) (*models.Role, error) {
	args := m.Called(ctx, communityID, roleID)
	role, _ := args.Get(0).(*models.Role)
	return role, args.Error(1)
}

// CreateRole records the call and returns the armed error.
func (m *Repository) CreateRole(ctx context.Context, role *models.Role) error {
	args := m.Called(ctx, role)
	return args.Error(0)
}

// UpdateRole records the call and returns the armed role and affected members.
func (m *Repository) UpdateRole(ctx context.Context, communityID, roleID uuid.UUID, patch models.RolePatch, now time.Time) (*models.Role, []*models.Membership, error) {
	args := m.Called(ctx, communityID, roleID, patch, now)
	role, _ := args.Get(0).(*models.Role)
	affected, _ := args.Get(1).([]*models.Membership)
	return role, affected, args.Error(2)
}

// DeleteRole records the call and returns the armed role and affected members.
func (m *Repository) DeleteRole(ctx context.Context, communityID, roleID uuid.UUID) (*models.Role, []*models.Membership, error) {
	args := m.Called(ctx, communityID, roleID)
	role, _ := args.Get(0).(*models.Role)
	affected, _ := args.Get(1).([]*models.Membership)
	return role, affected, args.Error(2)
}

// SetMemberRoles records the call and returns the armed membership.
func (m *Repository) SetMemberRoles(ctx context.Context, communityID, userID uuid.UUID, roleIDs []uuid.UUID, now time.Time) (*models.Membership, error) {
	args := m.Called(ctx, communityID, userID, roleIDs, now)
	membership, _ := args.Get(0).(*models.Membership)
	return membership, args.Error(1)
}

// ApplySanctionApplication records the call and returns the armed membership.
func (m *Repository) ApplySanctionApplication(ctx context.Context, application *models.SanctionApplication) (*models.Membership, error) {
	args := m.Called(ctx, application)
	membership, _ := args.Get(0).(*models.Membership)
	return membership, args.Error(1)
}

// MarkEventProcessed records the call and returns the armed error.
func (m *Repository) MarkEventProcessed(ctx context.Context, eventID uuid.UUID, eventType string, now time.Time) error {
	args := m.Called(ctx, eventID, eventType, now)
	return args.Error(0)
}

// Ping records the call and returns the armed error.
func (m *Repository) Ping(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// compile-time assertion that the mock satisfies every port.
var _ repository.Repository = (*Repository)(nil)
