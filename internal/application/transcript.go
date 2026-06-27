package application

import (
	"context"
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
	segment.RecognizedAtUTC = segment.RecognizedAtUTC.UTC()
	if segment.ReceivedAtUTC.IsZero() {
		segment.ReceivedAtUTC = s.now().UTC()
	} else {
		segment.ReceivedAtUTC = segment.ReceivedAtUTC.UTC()
	}
	result, err := s.repository.SaveTranscriptSegment(ctx, segment)
	if err != nil {
		return result, err
	}
	if result.Status == domain.TranscriptSegmentCreated && s.publisher != nil {
		s.publisher.PublishTranscriptSegment(segment)
	}
	return result, nil
}

func (s *TranscriptIngestService) ListTranscriptSegments(ctx context.Context, callID string, limit int) ([]domain.TranscriptSegment, error) {
	return s.repository.ListTranscriptSegments(ctx, callID, limit)
}
