package application

import (
	"context"
	"fmt"
	"time"

	"deciscope-core-api/internal/domain"
)

func (s *Service) CreateMeeting(ctx context.Context, workspaceID, title, source string) (*domain.Meeting, error) {
	meeting, err := s.meetings.CreateMeeting(ctx, workspaceID, title, source)
	if err != nil {
		return nil, err
	}
	if _, err := s.AppendAndPublish(ctx, meeting.ID, domain.EventMeetingState, map[string]any{
		"status": "created", "recording": false, "analyzing": false, "participants": []string{},
	}); err != nil {
		return nil, err
	}
	return s.meetings.GetMeeting(ctx, meeting.ID)
}

func (s *Service) ListMeetings(ctx context.Context, workspaceID string) ([]domain.Meeting, error) {
	return s.meetings.ListMeetings(ctx, workspaceID)
}

func (s *Service) GetMeeting(ctx context.Context, meetingID string) (*domain.Meeting, error) {
	return s.meetings.GetMeeting(ctx, meetingID)
}

func (s *Service) CreateJoinToken(ctx context.Context, meetingID string) (*JoinToken, error) {
	if _, err := s.meetings.GetMeeting(ctx, meetingID); err != nil {
		return nil, err
	}
	expiresAt := time.Now().UTC().Add(2 * time.Hour)
	return &JoinToken{
		Token: fmt.Sprintf("local.%s.%d", meetingID, expiresAt.Unix()), TokenType: "local-dev",
		ExpiresAt: expiresAt.Format(time.RFC3339),
	}, nil
}

func (s *Service) ListEvents(ctx context.Context, meetingID string, afterSeq int64) ([]domain.Event, error) {
	return s.events.ListEvents(ctx, meetingID, afterSeq)
}

func (s *Service) ListSegments(ctx context.Context, meetingID string, afterSeq int64) ([]domain.Segment, error) {
	return s.events.ListSegments(ctx, meetingID, afterSeq)
}

func (s *Service) ResetMeeting(ctx context.Context, meetingID string) error {
	return s.meetings.ResetMeeting(ctx, meetingID)
}

func (s *Service) AppendAndPublish(ctx context.Context, meetingID, eventType string, payload any) (*domain.Event, error) {
	event, err := s.events.AppendEvent(ctx, meetingID, eventType, payload)
	if err != nil {
		return nil, err
	}
	if s.publisher != nil {
		s.publisher.Publish(*event)
	}
	return event, nil
}

func (s *Service) EndMeeting(ctx context.Context, meetingID string) ([]domain.Event, error) {
	if _, err := s.meetings.GetMeeting(ctx, meetingID); err != nil {
		return nil, err
	}

	stateEvent, err := s.AppendAndPublish(ctx, meetingID, domain.EventMeetingState, map[string]any{
		"status": "ended", "recording": false, "analyzing": false, "participants": []string{},
	})
	if err != nil {
		return nil, err
	}
	return []domain.Event{*stateEvent}, nil
}
