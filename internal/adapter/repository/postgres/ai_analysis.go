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

func (r *MeetingAIAnalysisRepository) CompareAndSwapMeetingAIAnalysis(ctx context.Context, expectedVersion int64, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, bool, error) {
	payload, err := nullableJSONPayload(analysis.Payload)
	if err != nil {
		return nil, false, fmt.Errorf("encode meeting ai analysis CAS payload: %w", err)
	}
	updatedAt := analysis.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	row := r.db.QueryRowContext(ctx, `
		UPDATE meeting_session_ai_analyses
		SET status=$1, version=$2, payload=$3, model=$4,
			segment_count=$5, input_chars=$6, last_error=$7, updated_at=$8
		WHERE session_id=$9 AND analysis_type=$10 AND version=$11
		RETURNING session_id, analysis_type, status, version, payload,
			COALESCE(model, ''), segment_count, input_chars,
			COALESCE(last_error, ''), created_at, updated_at
	`, string(analysis.Status), analysis.Version, payload, nullable(analysis.Model),
		analysis.SegmentCount, analysis.InputChars, nullable(analysis.LastError), updatedAt.UTC(),
		analysis.SessionID, string(analysis.Type), expectedVersion)
	saved, err := scanMeetingAIAnalysis(row)
	if err == nil {
		return saved, true, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, false, fmt.Errorf("compare-and-swap meeting ai analysis: %w", err)
	}
	if expectedVersion != 0 {
		return nil, false, nil
	}
	row = r.db.QueryRowContext(ctx, `
		INSERT INTO meeting_session_ai_analyses (
			session_id, analysis_type, status, version, payload, model,
			segment_count, input_chars, last_error, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (session_id, analysis_type) DO NOTHING
		RETURNING session_id, analysis_type, status, version, payload,
			COALESCE(model, ''), segment_count, input_chars,
			COALESCE(last_error, ''), created_at, updated_at
	`, analysis.SessionID, string(analysis.Type), string(analysis.Status), analysis.Version,
		payload, nullable(analysis.Model), analysis.SegmentCount, analysis.InputChars,
		nullable(analysis.LastError), updatedAt.UTC())
	saved, err = scanMeetingAIAnalysis(row)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("insert meeting ai analysis CAS: %w", err)
	}
	return saved, true, nil
}

func (r *MeetingAIAnalysisRepository) GetMeetingAIAnalysis(ctx context.Context, sessionID string, analysisType domain.MeetingAIAnalysisType) (*domain.MeetingAIAnalysis, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT session_id, analysis_type, status, version, payload, COALESCE(model, ''), segment_count, input_chars, COALESCE(last_error, ''), created_at, updated_at
		FROM meeting_session_ai_analyses
		WHERE session_id = $1 AND analysis_type = $2
	`, sessionID, string(analysisType))
	return scanMeetingAIAnalysis(row)
}

func (r *MeetingAIAnalysisRepository) ListMeetingAIAnalysesForSessions(ctx context.Context, sessionIDs []string, analysisType domain.MeetingAIAnalysisType) ([]domain.MeetingAIAnalysis, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT session_id, analysis_type, status, version, payload, COALESCE(model, ''), segment_count, input_chars, COALESCE(last_error, ''), created_at, updated_at
		FROM meeting_session_ai_analyses
		WHERE analysis_type = $1 AND session_id = ANY($2)
	`, string(analysisType), sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("list meeting ai analyses for sessions: %w", err)
	}
	defer rows.Close()

	items := make([]domain.MeetingAIAnalysis, 0, len(sessionIDs))
	for rows.Next() {
		analysis, err := scanMeetingAIAnalysis(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *analysis)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate meeting ai analyses for sessions: %w", err)
	}
	return items, nil
}

// AppendLiveAnalysisHistory records a completed live analysis version in the
// durable history table. It is idempotent: re-appending the same
// (session_id, version) is a no-op, since a stale retry or duplicate publish
// must not fail the live analysis flow.
func (r *MeetingAIAnalysisRepository) AppendLiveAnalysisHistory(ctx context.Context, analysis domain.MeetingAIAnalysis) error {
	payload, err := nullableJSONPayload(analysis.Payload)
	if err != nil {
		return fmt.Errorf("encode meeting ai analysis live history payload: %w", err)
	}
	updatedAt := analysis.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO meeting_session_ai_analysis_live_history (session_id, version, payload, model, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (session_id, version) DO NOTHING
	`, analysis.SessionID, analysis.Version, payload, nullable(analysis.Model), updatedAt.UTC())
	if err != nil {
		return fmt.Errorf("append meeting ai analysis live history: %w", err)
	}
	return nil
}

// ListLiveAnalysisHistory returns up to limit of the most recent completed
// live analysis versions for sessionID, ordered oldest to newest (version
// ascending) so callers can replay the progression in order.
func (r *MeetingAIAnalysisRepository) ListLiveAnalysisHistory(ctx context.Context, sessionID string, limit int) ([]domain.MeetingAIAnalysis, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT session_id, version, payload, COALESCE(model, ''), updated_at
		FROM (
			SELECT session_id, version, payload, model, updated_at
			FROM meeting_session_ai_analysis_live_history
			WHERE session_id = $1
			ORDER BY version DESC
			LIMIT $2
		) sub
		ORDER BY version ASC
	`, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("list meeting ai analysis live history: %w", err)
	}
	defer rows.Close()

	items := make([]domain.MeetingAIAnalysis, 0, limit)
	for rows.Next() {
		var (
			rowSessionID, model string
			version             int64
			payload             []byte
			updatedAt           time.Time
		)
		if err := rows.Scan(&rowSessionID, &version, &payload, &model, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan meeting ai analysis live history: %w", err)
		}
		var rawPayload json.RawMessage
		if len(payload) > 0 {
			rawPayload = append(json.RawMessage(nil), payload...)
		}
		items = append(items, domain.MeetingAIAnalysis{
			SessionID: rowSessionID,
			Type:      domain.MeetingAIAnalysisLive,
			Status:    domain.MeetingAIAnalysisCompleted,
			Version:   version,
			Payload:   rawPayload,
			Model:     model,
			UpdatedAt: updatedAt.UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate meeting ai analysis live history: %w", err)
	}
	return items, nil
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
var _ application.MeetingAIAnalysisCompareAndSwapRepository = (*MeetingAIAnalysisRepository)(nil)
