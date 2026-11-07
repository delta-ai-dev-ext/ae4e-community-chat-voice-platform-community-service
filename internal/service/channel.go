package service

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"community_service/internal/models"
)

// ChannelService owns channel lifecycle: creation, reordering/capacity changes
// and deletion, each of which emits EVT-003.
type ChannelService struct {
	deps Deps
}

// NewChannelService builds the channel service.
func NewChannelService(deps Deps) *ChannelService {
	return &ChannelService{deps: deps.withDefaults()}
}

// Create adds a text or voice channel. A text channel has no capacity, a voice
// channel defaults to the maximum capacity of 10, and the channel is appended
// after the last one.
func (s *ChannelService) Create(ctx context.Context, userID, communityID uuid.UUID, traceID, correlationID string, request *models.CreateChannelRequest) (*models.Channel, error) {
	resolved, err := s.deps.resolve(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}
	if err := s.deps.requirePermission(userID, resolved, models.PermissionManageChannels); err != nil {
		return nil, err
	}

	now := s.deps.Clock.Now()
	channel := &models.Channel{
		ID:             uuid.New(),
		CommunityID:    communityID,
		Kind:           models.ChannelKind(request.Kind),
		Name:           strings.TrimSpace(request.Name),
		Position:       -1,
		ChannelVersion: 1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if channel.Kind == models.ChannelKindVoice {
		capacity := models.DefaultVoiceCapacity
		if request.Capacity != nil {
			capacity = *request.Capacity
		}
		channel.Capacity = &capacity
	}

	if err := s.deps.Channels.CreateChannel(ctx, channel); err != nil {
		return nil, err
	}

	s.deps.publishChannelChanged(ctx, channelEnvelope(traceID, correlationID, now), models.ChannelChangeCreated, channel)
	return channel, nil
}

// Update renames, reorders or re-caps a channel. Capacity is a voice-channel
// property, so setting it on a text channel is a business-rule violation.
func (s *ChannelService) Update(ctx context.Context, userID, communityID, channelID uuid.UUID, traceID, correlationID string, request *models.UpdateChannelRequest) (*models.Channel, error) {
	resolved, err := s.deps.resolve(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}
	if err := s.deps.requirePermission(userID, resolved, models.PermissionManageChannels); err != nil {
		return nil, err
	}

	existing, err := s.deps.Channels.GetChannel(ctx, communityID, channelID)
	if err != nil {
		return nil, err
	}

	if request.Capacity.Set {
		if existing.Kind != models.ChannelKindVoice {
			return nil, models.WithDetail(models.ErrValidationFailed, "capacity applies to voice channels only")
		}
		if request.Capacity.Value == nil {
			return nil, models.WithDetail(models.ErrValidationFailed, "a voice channel requires a capacity between 1 and 10")
		}
	}

	patch := models.ChannelPatch{
		Position: request.Position,
		Capacity: request.Capacity,
	}
	if request.Name != nil {
		trimmed := strings.TrimSpace(*request.Name)
		patch.Name = &trimmed
	}

	now := s.deps.Clock.Now()
	updated, err := s.deps.Channels.UpdateChannel(ctx, communityID, channelID, patch, now)
	if err != nil {
		return nil, err
	}

	s.deps.publishChannelChanged(ctx, channelEnvelope(traceID, correlationID, now), models.ChannelChangeUpdated, updated)
	return updated, nil
}

// Delete removes a channel unless it is the last one of the community.
func (s *ChannelService) Delete(ctx context.Context, userID, communityID, channelID uuid.UUID, traceID, correlationID string) error {
	resolved, err := s.deps.resolve(ctx, communityID, userID)
	if err != nil {
		return err
	}
	if err := s.deps.requirePermission(userID, resolved, models.PermissionManageChannels); err != nil {
		return err
	}

	deleted, err := s.deps.Channels.DeleteChannel(ctx, communityID, channelID)
	if err != nil {
		return err
	}

	s.deps.publishChannelChanged(ctx, channelEnvelope(traceID, correlationID, s.deps.Clock.Now()), models.ChannelChangeDeleted, deleted)
	return nil
}
