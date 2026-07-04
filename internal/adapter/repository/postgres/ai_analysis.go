package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

// MeetingAIAnalysisRepository persists one row per (session, analysis type)
// in meeting_session_ai_analyses. There is no history: every update is an
// upsert in place.
type MeetingAIAnalysisRepository struct {
	db *sql.DB
}

func NewMeetingAIAnalysisRepository(db *sql.DB) *MeetingAIAnalysisRepository {
	return &MeetingAIAnalysisRepository{db: db}
}

func (r *MeetingAIAnalysisRepository) UpsertMeetingAIAnalysis(ctx context.Context, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, error) {
	payload, err := nullableJSONPayload(analysis.Payload)
	if err != nil {
		return nil, fmt.Errorf("encode meeting ai analysis payload: %w", err)
	}
	updatedAt := analysis.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO meeting_session_ai_analyses (
			session_id, analysis_type, status, version, payload, model, segment_count, input_chars, last_error, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (session_id, analysis_type) DO UPDATE SET
			status = EXCLUDED.status,
			version = EXCLUDED.version,
			payload = EXCLUDED.payload,
			model = EXCLUDED.model,
			segment_count = EXCLUDED.segment_count,
			input_chars = EXCLUDED.input_chars,
			last_error = EXCLUDED.last_error,
			updated_at = EXCLUDED.updated_at
		RETURNING session_id, analysis_type, status, version, payload, COALESCE(model, ''), segment_count, input_chars, COALESCE(last_error, ''), created_at, updated_at
	`, analysis.SessionID, string(analysis.Type), string(analysis.Status), analysis.Version, payload,
		nullable(analysis.Model), analysis.SegmentCount, analysis.InputChars, nullable(analysis.LastError), updatedAt.UTC())
	return scanMeetingAIAnalysis(row)
}

func (r *MeetingAIAnalysisRepository) GetMeetingAIAnalysis(ctx context.Context, sessionID string, analysisType domain.MeetingAIAnalysisType) (*domain.MeetingAIAnalysis, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT session_id, analysis_type, status, version, payload, COALESCE(model, ''), segment_count, input_chars, COALESCE(last_error, ''), created_at, updated_at
		FROM meeting_session_ai_analyses
		WHERE session_id = $1 AND analysis_type = $2
	`, sessionID, string(analysisType))
	return scanMeetingAIAnalysis(row)
}

func nullableJSONPayload(payload json.RawMessage) (any, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	if !json.Valid(payload) {
		return nil, fmt.Errorf("meeting ai analysis payload is not valid json")
	}
	return []byte(payload), nil
}

type meetingAIAnalysisScanner interface {
	Scan(dest ...any) error
}

func scanMeetingAIAnalysis(row meetingAIAnalysisScanner) (*domain.MeetingAIAnalysis, error) {
	var (
		sessionID, analysisType, status, model, lastError string
		version                                           int64
		segmentCount, inputChars                          int
		payload                                           []byte
		createdAt, updatedAt                              time.Time
	)
	err := row.Scan(&sessionID, &analysisType, &status, &version, &payload, &model, &segmentCount, &inputChars, &lastError, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: meeting ai analysis not found", domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("query meeting ai analysis: %w", err)
	}
	var rawPayload json.RawMessage
	if len(payload) > 0 {
		rawPayload = append(json.RawMessage(nil), payload...)
	}
	return &domain.MeetingAIAnalysis{
		SessionID:    sessionID,
		Type:         domain.MeetingAIAnalysisType(analysisType),
		Status:       domain.MeetingAIAnalysisStatus(status),
		Version:      version,
		Payload:      rawPayload,
		Model:        model,
		SegmentCount: segmentCount,
		InputChars:   inputChars,
		LastError:    lastError,
		CreatedAt:    createdAt.UTC(),
		UpdatedAt:    updatedAt.UTC(),
	}, nil
}

var _ application.MeetingAIAnalysisRepository = (*MeetingAIAnalysisRepository)(nil)
