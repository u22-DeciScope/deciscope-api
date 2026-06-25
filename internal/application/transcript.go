package application

import (
	"context"
	"time"

	"deciscope-core-api/internal/domain"
)

type TranscriptIngestService struct {
	repository TranscriptSegmentRepository
	now        func() time.Time
}

func NewTranscriptIngestService(repository TranscriptSegmentRepository) *TranscriptIngestService {
	return &TranscriptIngestService{repository: repository, now: time.Now}
}

func (s *TranscriptIngestService) StoreTranscriptSegment(ctx context.Context, segment domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error) {
	segment.RecognizedAtUTC = segment.RecognizedAtUTC.UTC()
	if segment.ReceivedAtUTC.IsZero() {
		segment.ReceivedAtUTC = s.now().UTC()
	} else {
		segment.ReceivedAtUTC = segment.ReceivedAtUTC.UTC()
	}
	return s.repository.SaveTranscriptSegment(ctx, segment)
}
