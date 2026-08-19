package application

import (
	"context"
	"log"
	"time"

	"deciscope-core-api/internal/domain"
)

type TranscriptIngestService struct {
	repository TranscriptSegmentRepository
	publisher  TranscriptSegmentPublisher
	now        func() time.Time
}

func NewTranscriptIngestService(repository TranscriptSegmentRepository, publisher ...TranscriptSegmentPublisher) *TranscriptIngestService {
	var segmentPublisher TranscriptSegmentPublisher
	if len(publisher) > 0 {
		segmentPublisher = publisher[0]
	}
	return &TranscriptIngestService{repository: repository, publisher: segmentPublisher, now: time.Now}
}

func (s *TranscriptIngestService) StoreTranscriptSegment(ctx context.Context, segment domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error) {
	segment = s.normalizeTranscriptSegment(segment)
	segment.IsFinal = true
	result, err := s.repository.SaveTranscriptSegment(ctx, segment)
	if err != nil {
		return result, err
	}
	persistedAt := s.now().UTC()
	log.Printf("Transcript final persisted. event=transcript_final_persisted sessionId=%s callId=%s sequenceNo=%d finalReceivedAt=%s persistedAt=%s persistenceLatencyMs=%d storeStatus=%s",
		segment.SessionID, segment.CallID, segment.SequenceNo,
		segment.ReceivedAtUTC.UTC().Format(time.RFC3339Nano), persistedAt.Format(time.RFC3339Nano),
		durationSince(persistedAt, segment.ReceivedAtUTC).Milliseconds(), result.Status)
	if result.Status == domain.TranscriptSegmentCreated && s.publisher != nil {
		s.publisher.PublishTranscriptSegment(segment)
	}
	return result, nil
}

func (s *TranscriptIngestService) PublishTranscriptPartial(ctx context.Context, segment domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error) {
	_ = ctx
	segment = s.normalizeTranscriptSegment(segment)
	segment.IsFinal = false
	if s.publisher != nil {
		s.publisher.PublishTranscriptSegment(segment)
	}
	return domain.TranscriptSegmentStoreResult{Status: domain.TranscriptSegmentPartialSent, EventID: segment.EventID}, nil
}

func (s *TranscriptIngestService) ListTranscriptSegments(ctx context.Context, callID, sessionID string, limit int) ([]domain.TranscriptSegment, error) {
	return s.repository.ListTranscriptSegments(ctx, callID, sessionID, limit)
}

func (s *TranscriptIngestService) normalizeTranscriptSegment(segment domain.TranscriptSegment) domain.TranscriptSegment {
	segment.RecognizedAtUTC = segment.RecognizedAtUTC.UTC()
	if segment.ReceivedAtUTC.IsZero() {
		segment.ReceivedAtUTC = s.now().UTC()
	} else {
		segment.ReceivedAtUTC = segment.ReceivedAtUTC.UTC()
	}
	return segment
}
