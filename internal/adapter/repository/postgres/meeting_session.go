package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

type MeetingSessionRepository struct {
	db *sql.DB
}

func NewMeetingSessionRepository(db *sql.DB) *MeetingSessionRepository {
	return &MeetingSessionRepository{db: db}
}

func (r *MeetingSessionRepository) CreateMeetingSession(ctx context.Context, session domain.MeetingSession) (*domain.MeetingSession, error) {
	record := meetingSessionRecordFromDomain(session)
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO meeting_sessions (
			id, join_url, join_url_hash, status, bot_call_id, requested_at,
			command_sent_at, joined_at, ended_at, last_error, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, join_url, join_url_hash, status, COALESCE(bot_call_id, ''), requested_at,
			command_sent_at, joined_at, ended_at, COALESCE(last_error, ''), created_at, updated_at
	`, record.ID, record.JoinURL, record.JoinURLHash, record.Status, nullable(record.BotCallID), record.RequestedAt,
		nullable(record.CommandSentAt), nullable(record.JoinedAt), nullable(record.EndedAt), nullable(record.LastError),
		record.CreatedAt, record.UpdatedAt)
	return scanMeetingSession(row)
}

func (r *MeetingSessionRepository) GetMeetingSession(ctx context.Context, sessionID string) (*domain.MeetingSession, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, join_url, join_url_hash, status, COALESCE(bot_call_id, ''), requested_at,
			command_sent_at, joined_at, ended_at, COALESCE(last_error, ''), created_at, updated_at
		FROM meeting_sessions
		WHERE id = $1
	`, sessionID)
	return scanMeetingSession(row)
}

func (r *MeetingSessionRepository) UpdateMeetingSessionStatus(ctx context.Context, update domain.MeetingSessionStatusUpdate) (*domain.MeetingSession, error) {
	row := r.db.QueryRowContext(ctx, `
		UPDATE meeting_sessions
		SET status = $2,
			bot_call_id = CASE WHEN $3 = '' THEN bot_call_id ELSE $3 END,
			command_sent_at = COALESCE($4, command_sent_at),
			joined_at = COALESCE($5, joined_at),
			ended_at = COALESCE($6, ended_at),
			last_error = $7,
			updated_at = $8
		WHERE id = $1
		RETURNING id, join_url, join_url_hash, status, COALESCE(bot_call_id, ''), requested_at,
			command_sent_at, joined_at, ended_at, COALESCE(last_error, ''), created_at, updated_at
	`, update.SessionID, string(update.Status), update.BotCallID, nullableTimePtr(update.CommandSentAt),
		nullableTimePtr(update.JoinedAt), nullableTimePtr(update.EndedAt), nullable(update.LastError),
		update.UpdatedAt.UTC().Format(time.RFC3339Nano))
	return scanMeetingSession(row)
}

type meetingSessionRecord struct {
	ID            string
	JoinURL       string
	JoinURLHash   string
	Status        string
	BotCallID     string
	RequestedAt   string
	CommandSentAt string
	JoinedAt      string
	EndedAt       string
	LastError     string
	CreatedAt     string
	UpdatedAt     string
}

func meetingSessionRecordFromDomain(session domain.MeetingSession) meetingSessionRecord {
	return meetingSessionRecord{
		ID:            session.ID,
		JoinURL:       session.JoinURL,
		JoinURLHash:   session.JoinURLHash,
		Status:        string(session.Status),
		BotCallID:     session.BotCallID,
		RequestedAt:   formatTime(session.RequestedAt),
		CommandSentAt: formatTime(session.CommandSentAt),
		JoinedAt:      formatTime(session.JoinedAt),
		EndedAt:       formatTime(session.EndedAt),
		LastError:     session.LastError,
		CreatedAt:     formatTime(session.CreatedAt),
		UpdatedAt:     formatTime(session.UpdatedAt),
	}
}

func scanMeetingSession(row interface{ Scan(dest ...any) error }) (*domain.MeetingSession, error) {
	var record meetingSessionRecord
	var commandSentAt, joinedAt, endedAt sql.NullString
	err := row.Scan(
		&record.ID, &record.JoinURL, &record.JoinURLHash, &record.Status, &record.BotCallID, &record.RequestedAt,
		&commandSentAt, &joinedAt, &endedAt, &record.LastError, &record.CreatedAt, &record.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: meeting session not found", domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("query meeting session: %w", err)
	}
	record.CommandSentAt = commandSentAt.String
	record.JoinedAt = joinedAt.String
	record.EndedAt = endedAt.String
	return record.toDomain()
}

func (record meetingSessionRecord) toDomain() (*domain.MeetingSession, error) {
	requestedAt, err := parseRequiredTime("requested_at", record.RequestedAt)
	if err != nil {
		return nil, err
	}
	createdAt, err := parseRequiredTime("created_at", record.CreatedAt)
	if err != nil {
		return nil, err
	}
	updatedAt, err := parseRequiredTime("updated_at", record.UpdatedAt)
	if err != nil {
		return nil, err
	}
	commandSentAt, err := parseOptionalTime("command_sent_at", record.CommandSentAt)
	if err != nil {
		return nil, err
	}
	joinedAt, err := parseOptionalTime("joined_at", record.JoinedAt)
	if err != nil {
		return nil, err
	}
	endedAt, err := parseOptionalTime("ended_at", record.EndedAt)
	if err != nil {
		return nil, err
	}
	return &domain.MeetingSession{
		ID:            record.ID,
		JoinURL:       record.JoinURL,
		JoinURLHash:   record.JoinURLHash,
		Status:        domain.MeetingSessionStatus(record.Status),
		BotCallID:     record.BotCallID,
		RequestedAt:   requestedAt,
		CommandSentAt: commandSentAt,
		JoinedAt:      joinedAt,
		EndedAt:       endedAt,
		LastError:     record.LastError,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}, nil
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func nullableTimePtr(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseRequiredTime(name, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse meeting session %s: %w", name, err)
	}
	return parsed.UTC(), nil
}

func parseOptionalTime(name, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return parseRequiredTime(name, value)
}

var _ application.MeetingSessionRepository = (*MeetingSessionRepository)(nil)
