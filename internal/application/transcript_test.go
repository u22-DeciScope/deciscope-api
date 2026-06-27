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

func TestTranscriptIngestServicePublishesOnlyCreatedSegments(t *testing.T) {
	repository := &fakeTranscriptSegmentRepository{}
	publisher := &fakeTranscriptSegmentPublisher{}
	service := application.NewTranscriptIngestService(repository, publisher)

	segment := domain.TranscriptSegment{
		EventID: "event-1", CallID: "call-1", SequenceNo: 1,
		RecognizedAtUTC: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC),
		Text:            "hello",
	}
	if _, err := service.StoreTranscriptSegment(context.Background(), segment); err != nil {
		t.Fatalf("StoreTranscriptSegment() error = %v", err)
	}
	if len(publisher.segments) != 1 || publisher.segments[0].EventID != "event-1" {
		t.Fatalf("published segments = %+v", publisher.segments)
	}

	repository.status = domain.TranscriptSegmentAlreadyExists
	if _, err := service.StoreTranscriptSegment(context.Background(), segment); err != nil {
		t.Fatalf("StoreTranscriptSegment(duplicate) error = %v", err)
	}
	if len(publisher.segments) != 1 {
		t.Fatalf("published duplicate segment, segments = %+v", publisher.segments)
	}
}

type fakeTranscriptSegmentRepository struct {
	status  domain.TranscriptSegmentStoreStatus
	segment domain.TranscriptSegment
}

func (f *fakeTranscriptSegmentRepository) SaveTranscriptSegment(_ context.Context, segment domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error) {
	f.segment = segment
	if f.status == "" {
		f.status = domain.TranscriptSegmentCreated
	}
	return domain.TranscriptSegmentStoreResult{Status: f.status, EventID: segment.EventID}, nil
}

func (f *fakeTranscriptSegmentRepository) ListTranscriptSegments(context.Context, string, int) ([]domain.TranscriptSegment, error) {
	return nil, nil
}

type fakeTranscriptSegmentPublisher struct {
	segments []domain.TranscriptSegment
}

func (f *fakeTranscriptSegmentPublisher) PublishTranscriptSegment(segment domain.TranscriptSegment) {
	f.segments = append(f.segments, segment)
}
