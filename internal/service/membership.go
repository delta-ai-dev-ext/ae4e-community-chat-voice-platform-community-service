package service

import (
	"context"

	"github.com/google/uuid"

	"community_service/internal/models"
	"community_service/internal/obs"
)

// MembershipService owns membership listing and leaving.
type MembershipService struct {
	deps Deps
}

// NewMembershipService builds the membership service.
func NewMembershipService(deps Deps) *MembershipService {
	return &MembershipService{deps: deps.withDefaults()}
}

// Leave removes the caller's membership. The owner cannot leave because owner
// transfer is out of scope at launch.
func (s *MembershipService) Leave(ctx context.Context, userID, communityID uuid.UUID, traceID, correlationID string) error {
	resolved, err := s.deps.resolve(ctx, communityID, userID)
	if err != nil {
		return err
	}

	if resolved.isOwner(userID) {
		return models.ErrOwnerCannotLeave
	}
	if !resolved.isMember() {
		return models.ErrMembershipNotFound
	}

	now := s.deps.Clock.Now()
	removed, err := s.deps.Memberships.RemoveMembership(ctx, communityID, userID, now)
	if err != nil {
		return err
	}

	s.deps.publishMemberChanged(ctx, memberEnvelope(traceID, correlationID, nil, now), models.ChangeLeft, removed, nil)
	s.deps.Metrics.RecordLeave(obs.LeaveReasonLeft)
	return nil
}

// ListMembers returns one page of active members; it requires an active
// membership.
func (s *MembershipService) ListMembers(ctx context.Context, userID, communityID uuid.UUID, page models.PageQuery) (*models.MemberListResponse, error) {
	resolved, err := s.deps.resolve(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}
	if err := s.deps.requireMember(userID, resolved); err != nil {
		return nil, err
	}

	memberships, err := s.deps.Memberships.ListMembers(ctx, communityID, page.Limit+1, page.Offset)
	if err != nil {
		return nil, err
	}

	var nextCursor *string
	if len(memberships) > page.Limit {
		memberships = memberships[:page.Limit]
		cursor := models.EncodeCursor(page.Offset + page.Limit)
		nextCursor = &cursor
	}

	members := make([]models.Member, 0, len(memberships))
	for _, membership := range memberships {
		members = append(members, membership.AsMember())
	}

	return &models.MemberListResponse{Members: members, NextCursor: nextCursor}, nil
}
