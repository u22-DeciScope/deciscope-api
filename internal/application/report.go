package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"deciscope-core-api/internal/domain"
)

func (s *Service) GetOrCreateReport(ctx context.Context, meetingID string) (*domain.Report, error) {
	report, err := s.reports.LatestReport(ctx, meetingID)
	if err == nil {
		return report, nil
	}
	if err != domain.ErrNotFound {
		return nil, err
	}
	content, err := s.BuildMarkdownReport(ctx, meetingID)
	if err != nil {
		return nil, err
	}
	return s.reports.SaveReport(ctx, meetingID, content)
}

func (s *Service) BuildMarkdownReport(ctx context.Context, meetingID string) (string, error) {
	meeting, err := s.meetings.GetMeeting(ctx, meetingID)
	if err != nil {
		return "", err
	}
	segments, err := s.events.ListSegments(ctx, meetingID, 0)
	if err != nil {
		return "", err
	}
	events, err := s.events.ListEvents(ctx, meetingID, 0)
	if err != nil {
		return "", err
	}

	var cards []analysisCard
	for _, event := range events {
		if event.Type != domain.EventAnalysisDelta {
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
				item.Item.Kind, item.Item.Subtype, item.Item.Status, _ = normalizeSemanticClassification(item.Item.Kind, item.Item.Subtype, item.Item.Status)
				cards = append(cards, item.Item)
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", meeting.Title)
	fmt.Fprintf(&b, "- Meeting ID: `%s`\n- Status: `%s`\n- Segments: %d\n\n", meeting.ID, meeting.Status, len(segments))
	b.WriteString("## Summary\n\n")
	if len(segments) == 0 {
		b.WriteString("No transcript segments were captured.\n\n")
	} else {
		fmt.Fprintf(&b, "This mock report was generated from %d final transcript segments. It is deterministic and does not call any external LLM or cloud service.\n\n", len(segments))
	}
	b.WriteString("## Decisions\n\n")
	writeCardsByKind(&b, cards, "decision")
	if len(cards) == 0 {
		b.WriteString("- No structured analysis cards were generated yet.\n")
	}
	b.WriteString("\n## Risks And Open Questions\n\n")
	wrote := writeOpenCardsByKind(&b, cards, "risk")
	wrote = writeOpenCardsByKind(&b, cards, "issue") || wrote
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
	Subtype  string `json:"subtype,omitempty"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Status   string `json:"status"`
}

func writeCardsByKind(b *strings.Builder, cards []analysisCard, kind string) bool {
	wrote := false
	for _, card := range cards {
		if card.Kind == kind {
			writeAnalysisCard(b, card)
			wrote = true
		}
	}
	return wrote
}

func writeOpenCardsByKind(b *strings.Builder, cards []analysisCard, kind string) bool {
	wrote := false
	for _, card := range cards {
		if card.Kind == kind && card.Status != "resolved" {
			writeAnalysisCard(b, card)
			wrote = true
		}
	}
	return wrote
}

func writeAnalysisCard(b *strings.Builder, card analysisCard) {
	classification := card.Kind
	if card.Subtype != "" {
		classification += "/" + card.Subtype
	}
	fmt.Fprintf(b, "- **%s** (%s/%s): %s\n", card.Title, classification, card.Severity, card.Body)
}
