package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"

	"community_service/internal/models"
	"community_service/internal/obs"
)

// Invite code shape: long enough to be unguessable, short enough to paste. The
// alphabet omits characters that are easy to confuse when a code is read aloud.
const (
	InviteCodeLength   = 10
	inviteCodeAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

// InviteService owns invite codes and the join flow.
type InviteService struct {
	deps Deps
}

// NewInviteService builds the invite service.
func NewInviteService(deps Deps) *InviteService {
	return &InviteService{deps: deps.withDefaults()}
}

// NewInviteCode mints a cryptographically random invite code.
func NewInviteCode() (string, error) {
	limit := big.NewInt(int64(len(inviteCodeAlphabet)))
	code := make([]byte, InviteCodeLength)

	for index := range code {
		random, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("generate invite code: %w", err)
		}
		code[index] = inviteCodeAlphabet[random.Int64()]
	}
	return string(code), nil
}

// Create mints an invite; it requires `create_invites`.
func (s *InviteService) Create(ctx context.Context, userID, communityID uuid.UUID, request *models.CreateInviteRequest) (*models.Invite, error) {
	resolved, err := s.deps.resolve(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}
	if err := s.deps.requirePermission(userID, resolved, models.PermissionCreateInvites); err != nil {
		return nil, err
	}

	code, err := NewInviteCode()
	if err != nil {
		return nil, models.Wrap(models.ErrInternal, err)
	}

	now := s.deps.Clock.Now()
	invite := &models.Invite{
		ID:          uuid.New(),
		CommunityID: communityID,
		Code:        code,
		CreatedBy:   userID,
		CreatedAt:   now,
		MaxUses:     request.MaxUses,
	}
	if request.ExpiresInSeconds != nil {
		expiresAt := now.Add(time.Duration(*request.ExpiresInSeconds) * time.Second)
		invite.ExpiresAt = &expiresAt
	}

	if err := s.deps.Invites.CreateInvite(ctx, invite); err != nil {
		return nil, err
	}

	s.deps.Metrics.RecordInviteCreated()
	return invite, nil
}

// Revoke revokes an invite code idempotently; it requires `create_invites`.
func (s *InviteService) Revoke(ctx context.Context, userID, communityID uuid.UUID, code string) error {
	resolved, err := s.deps.resolve(ctx, communityID, userID)
	if err != nil {
		return err
	}
	if err := s.deps.requirePermission(userID, resolved, models.PermissionCreateInvites); err != nil {
		return err
	}

	_, err = s.deps.Invites.RevokeInvite(ctx, communityID, code, s.deps.Clock.Now())
	return err
}

// Preview describes the community an invite leads to and whether it is still
// usable. An expired, exhausted or revoked invite is reported in-band with
// `valid: false` and a reason, never as an error.
func (s *InviteService) Preview(ctx context.Context, code string) (*models.InvitePreviewResponse, error) {
	invite, err := s.deps.Invites.GetInviteByCode(ctx, code)
	if err != nil {
		return nil, err
	}

	community, err := s.deps.Communities.GetCommunity(ctx, invite.CommunityID)
	if err != nil {
		return nil, err
	}

	memberCount, err := s.deps.Communities.CountActiveMembers(ctx, invite.CommunityID)
	if err != nil {
		return nil, err
	}

	response := &models.InvitePreviewResponse{
		Valid: true,
		Community: models.InvitePreviewCommunity{
			ID:          community.ID,
			Name:        community.Name,
			Description: community.Description,
			IconURL:     community.IconURL,
			MemberCount: memberCount,
		},
	}

	if reason := models.CheckInvite(invite, s.deps.Clock.Now()); reason != "" {
		response.Valid = false
		response.InvalidReason = &reason
	}
	return response, nil
}

// Accept joins the caller to the invite's community. A second accept by an
// active member is idempotent and does not consume another use of the invite.
func (s *InviteService) Accept(ctx context.Context, userID uuid.UUID, code, traceID, correlationID string) (*models.Membership, error) {
	now := s.deps.Clock.Now()

	invite, err := s.deps.Invites.GetInviteByCode(ctx, code)
	if err != nil {
		if errors.Is(err, models.ErrInviteNotFound) {
			s.deps.Metrics.RecordJoin(obs.JoinResultInvalid)
		}
		return nil, err
	}

	if reason := models.CheckInvite(invite, now); reason != "" {
		s.deps.Metrics.RecordJoin(obs.JoinResultExpired)
		return nil, models.ErrInviteExpired
	}

	ban, err := s.deps.Memberships.GetActiveBan(ctx, invite.CommunityID, userID)
	if err != nil {
		return nil, err
	}
	if ban != nil {
		s.deps.Metrics.RecordJoin(obs.JoinResultBanned)
		return nil, models.ErrBanned
	}

	existing, err := s.deps.Memberships.GetMembership(ctx, invite.CommunityID, userID)
	switch {
	case err == nil && existing.IsActive():
		s.deps.Metrics.RecordJoin(obs.JoinResultJoined)
		return existing, nil
	case err != nil && !errors.Is(err, models.ErrMembershipNotFound):
		return nil, err
	}

	membership, changed, err := s.deps.Invites.JoinWithInvite(ctx, invite.CommunityID, userID, uuid.New(), code, now)
	if err != nil {
		switch {
		case errors.Is(err, models.ErrBanned):
			s.deps.Metrics.RecordJoin(obs.JoinResultBanned)
		case errors.Is(err, models.ErrInviteExpired):
			s.deps.Metrics.RecordJoin(obs.JoinResultExpired)
		case errors.Is(err, models.ErrInviteNotFound):
			s.deps.Metrics.RecordJoin(obs.JoinResultInvalid)
		}
		return nil, err
	}

	if changed {
		s.deps.publishMemberChanged(ctx, memberEnvelope(traceID, correlationID, nil, now), models.ChangeJoined, membership, nil)
	}
	s.deps.Metrics.RecordJoin(obs.JoinResultJoined)

	return membership, nil
}
