package application_test

import (
	"context"
	"testing"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

func TestTranscriptIngestServiceNormalizesTimesAndStoresThroughPort(t *testing.T) {
	repository := &fakeTranscriptSegmentRepository{}
	service := application.NewTranscriptIngestService(repository)

	recognized := time.Date(2026, 6, 25, 13, 20, 1, 123456700, time.FixedZone("UTC", 0))
	result, err := service.StoreTranscriptSegment(context.Background(), domain.TranscriptSegment{
		EventID:         "event:1",
		CallID:          "event",
		SequenceNo:      1,
		RecognizedAtUTC: recognized,
		OffsetTicks:     0,
		DurationTicks:   10000000,
		Text:            "保存テストです。",
	})
	if err != nil {
		t.Fatalf("StoreTranscriptSegment() error = %v", err)
	}
	if result.Status != domain.TranscriptSegmentCreated {
		t.Fatalf("result = %+v", result)
	}
	if repository.segment.RecognizedAtUTC.Location() != time.UTC {
		t.Fatalf("recognized location = %v, want UTC", repository.segment.RecognizedAtUTC.Location())
	}
	if repository.segment.ReceivedAtUTC.IsZero() || repository.segment.ReceivedAtUTC.Location() != time.UTC {
		t.Fatalf("receivedAtUTC = %v, want non-zero UTC", repository.segment.ReceivedAtUTC)
	}
}

type fakeTranscriptSegmentRepository struct {
	segment domain.TranscriptSegment
}

func (f *fakeTranscriptSegmentRepository) SaveTranscriptSegment(_ context.Context, segment domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error) {
	f.segment = segment
	return domain.TranscriptSegmentStoreResult{Status: domain.TranscriptSegmentCreated, EventID: segment.EventID}, nil
}
