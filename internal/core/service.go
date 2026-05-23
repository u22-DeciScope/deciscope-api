package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type Publisher interface {
	Publish(event Event)
}

type Service struct {
	store     *Store
	publisher Publisher
}

func NewService(store *Store, publisher Publisher) *Service {
	return &Service{store: store, publisher: publisher}
}

func (s *Service) Store() *Store {
	return s.store
}

func (s *Service) CreateMeeting(ctx context.Context, title, source string) (*Meeting, error) {
	meeting, err := s.store.CreateMeeting(ctx, title, source)
	if err != nil {
		return nil, err
	}
	event, err := s.AppendAndPublish(ctx, meeting.ID, EventMeetingState, map[string]any{
		"status":       "created",
		"recording":    false,
		"analyzing":    false,
		"participants": []string{},
	})
	if err != nil {
		return nil, err
	}
	_ = event
	return s.store.GetMeeting(ctx, meeting.ID)
}

func (s *Service) AppendAndPublish(ctx context.Context, meetingID, eventType string, payload any) (*Event, error) {
	event, err := s.store.AppendEvent(ctx, meetingID, eventType, payload)
	if err != nil {
		return nil, err
	}
	if s.publisher != nil {
		s.publisher.Publish(*event)
	}
	return event, nil
}

func (s *Service) EndMeeting(ctx context.Context, meetingID string) (*Report, []Event, error) {
	if _, err := s.store.GetMeeting(ctx, meetingID); err != nil {
		return nil, nil, err
	}

	var events []Event
	stateEvent, err := s.AppendAndPublish(ctx, meetingID, EventMeetingState, map[string]any{
		"status":       "ended",
		"recording":    false,
		"analyzing":    false,
		"participants": []string{},
	})
	if err != nil {
		return nil, nil, err
	}
	events = append(events, *stateEvent)

	job, err := s.store.CreateJob(ctx, "report.final", meetingID, "running")
	if err != nil {
		return nil, events, err
	}
	content, err := s.BuildMarkdownReport(ctx, meetingID)
	if err != nil {
		_ = s.store.FailJob(ctx, job.ID, err.Error())
		return nil, events, err
	}
	report, err := s.store.SaveReport(ctx, meetingID, content)
	if err != nil {
		_ = s.store.FailJob(ctx, job.ID, err.Error())
		return nil, events, err
	}
	if err := s.store.CompleteJob(ctx, job.ID, map[string]any{"artifact_id": report.ArtifactID}); err != nil {
		return nil, events, err
	}
	readyEvent, err := s.AppendAndPublish(ctx, meetingID, EventReportReady, map[string]any{
		"artifact_id": report.ArtifactID,
		"format":      report.Format,
	})
	if err != nil {
		return nil, events, err
	}
	events = append(events, *readyEvent)
	return report, events, nil
}

func (s *Service) BuildMarkdownReport(ctx context.Context, meetingID string) (string, error) {
	meeting, err := s.store.GetMeeting(ctx, meetingID)
	if err != nil {
		return "", err
	}
	segments, err := s.store.ListSegments(ctx, meetingID, 0)
	if err != nil {
		return "", err
	}
	events, err := s.store.ListEvents(ctx, meetingID, 0)
	if err != nil {
		return "", err
	}

	var cards []analysisCard
	for _, event := range events {
		if event.Type != EventAnalysisDelta {
			continue
		}
		var delta struct {
			Items []struct {
				Op   string       `json:"op"`
				Item analysisCard `json:"item"`
			} `json:"items"`
		}
		if err := json.Unmarshal(event.Payload, &delta); err != nil {
			continue
		}
		for _, item := range delta.Items {
			if item.Op == "add" || item.Op == "update" {
				cards = append(cards, item.Item)
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", meeting.Title)
	fmt.Fprintf(&b, "- Meeting ID: `%s`\n", meeting.ID)
	fmt.Fprintf(&b, "- Status: `%s`\n", meeting.Status)
	fmt.Fprintf(&b, "- Segments: %d\n\n", len(segments))

	b.WriteString("## Summary\n\n")
	if len(segments) == 0 {
		b.WriteString("No transcript segments were captured.\n\n")
	} else {
		fmt.Fprintf(&b, "This mock report was generated from %d final transcript segments. It is deterministic and does not call any external LLM or cloud service.\n\n", len(segments))
	}

	b.WriteString("## Decisions\n\n")
	writeCardsByKind(&b, cards, "issue")
	if len(cards) == 0 {
		b.WriteString("- No structured analysis cards were generated yet.\n")
	}
	b.WriteString("\n## Risks And Open Questions\n\n")
	wrote := writeCardsByKind(&b, cards, "risk")
	wrote = writeCardsByKind(&b, cards, "question") || wrote
	if !wrote {
		b.WriteString("- No risks or questions were detected in the local fixture.\n")
	}

	b.WriteString("\n## Transcript\n\n")
	for _, segment := range segments {
		fmt.Fprintf(&b, "- `%s` **%s**: %s\n", segment.SegmentID, segment.SpeakerLabel, segment.Text)
	}
	return b.String(), nil
}

type analysisCard struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Status   string `json:"status"`
}

func writeCardsByKind(b *strings.Builder, cards []analysisCard, kind string) bool {
	wrote := false
	for _, card := range cards {
		if card.Kind != kind {
			continue
		}
		fmt.Fprintf(b, "- **%s** (%s/%s): %s\n", card.Title, card.Kind, card.Severity, card.Body)
		wrote = true
	}
	return wrote
}
