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
			id, workspace_id, created_by_user_id, meeting_id,
			join_url, join_url_hash, title, title_source, title_updated_at, user_provided_title, graph_title, provider,
			external_meeting_id, join_meeting_id, join_web_url, canonical_join_web_url, thread_id,
			organizer_id, organizer_name, organizer_email, scheduled_start_at, scheduled_end_at,
			title_resolution_error_code, title_resolution_error_message, title_resolved_at,
			status, bot_call_id, requested_at, command_sent_at, joined_at, ended_at, end_reason,
			last_bot_status_at, last_error, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33, $34, $35, $36)
		RETURNING id, COALESCE(workspace_id, ''), COALESCE(created_by_user_id, ''), COALESCE(meeting_id, ''),
			join_url, join_url_hash, COALESCE(title, ''), COALESCE(title_source, ''),
			title_updated_at, COALESCE(user_provided_title, ''), COALESCE(graph_title, ''),
			COALESCE(provider, ''), COALESCE(external_meeting_id, ''), COALESCE(join_meeting_id, ''),
			COALESCE(join_web_url, ''), COALESCE(canonical_join_web_url, ''),
			COALESCE(thread_id, ''), COALESCE(organizer_id, ''), COALESCE(organizer_name, ''), COALESCE(organizer_email, ''),
			scheduled_start_at, scheduled_end_at, COALESCE(title_resolution_error_code, ''),
			COALESCE(title_resolution_error_message, ''), title_resolved_at, COALESCE(purpose, ''), COALESCE(context, ''), COALESCE(agenda, ''), COALESCE(decision_points, ''), COALESCE(concerns, ''), COALESCE(expected_output, ''), COALESCE(custom_instruction, ''), status,
			COALESCE(bot_call_id, ''), requested_at, command_sent_at, joined_at, ended_at,
			COALESCE(end_reason, ''), last_bot_status_at, COALESCE(last_error, ''), created_at, updated_at
	`, record.ID, nullable(record.WorkspaceID), nullable(record.CreatedByUserID), nullable(record.MeetingID),
		record.JoinURL, record.JoinURLHash, record.Title, record.TitleSource, nullable(record.TitleUpdatedAt),
		nullable(record.UserProvidedTitle), nullable(record.GraphTitle), nullable(record.Provider), nullable(record.ExternalMeetingID),
		nullable(record.JoinMeetingID), nullable(record.JoinWebURL), nullable(record.CanonicalJoinWebURL), nullable(record.ThreadID),
		nullable(record.OrganizerID), nullable(record.OrganizerName), nullable(record.OrganizerEmail), nullable(record.ScheduledStartAt),
		nullable(record.ScheduledEndAt), nullable(record.TitleResolutionErrorCode), nullable(record.TitleResolutionErrorMessage),
		nullable(record.TitleResolvedAt),
		record.Status, nullable(record.BotCallID), record.RequestedAt,
		nullable(record.CommandSentAt), nullable(record.JoinedAt), nullable(record.EndedAt), nullable(record.EndReason),
		nullable(record.LastBotStatusAt), nullable(record.LastError), record.CreatedAt, record.UpdatedAt)
	return scanMeetingSession(row)
}

func (r *MeetingSessionRepository) CreateOrReuseMeetingSession(ctx context.Context, session domain.MeetingSession) (*domain.MeetingSession, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin meeting session create: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `LOCK TABLE meeting_sessions IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return nil, false, fmt.Errorf("lock meeting sessions: %w", err)
	}
	existing, err := findReusableMeetingSessionByWorkspaceJoinURLHash(ctx, tx, session.WorkspaceID, session.JoinURLHash)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit meeting session reuse: %w", err)
		}
		return existing, false, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, false, err
	}

	record := meetingSessionRecordFromDomain(session)
	row := tx.QueryRowContext(ctx, `
		INSERT INTO meeting_sessions (
			id, workspace_id, created_by_user_id, meeting_id,
			join_url, join_url_hash, title, title_source, title_updated_at, user_provided_title, graph_title, provider,
			external_meeting_id, join_meeting_id, join_web_url, canonical_join_web_url, thread_id,
			organizer_id, organizer_name, organizer_email, scheduled_start_at, scheduled_end_at,
			title_resolution_error_code, title_resolution_error_message, title_resolved_at,
			status, bot_call_id, requested_at, command_sent_at, joined_at, ended_at, end_reason,
			last_bot_status_at, last_error, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33, $34, $35, $36)
		RETURNING id, COALESCE(workspace_id, ''), COALESCE(created_by_user_id, ''), COALESCE(meeting_id, ''),
			join_url, join_url_hash, COALESCE(title, ''), COALESCE(title_source, ''),
			title_updated_at, COALESCE(user_provided_title, ''), COALESCE(graph_title, ''),
			COALESCE(provider, ''), COALESCE(external_meeting_id, ''), COALESCE(join_meeting_id, ''),
			COALESCE(join_web_url, ''), COALESCE(canonical_join_web_url, ''),
			COALESCE(thread_id, ''), COALESCE(organizer_id, ''), COALESCE(organizer_name, ''), COALESCE(organizer_email, ''),
			scheduled_start_at, scheduled_end_at, COALESCE(title_resolution_error_code, ''),
			COALESCE(title_resolution_error_message, ''), title_resolved_at, COALESCE(purpose, ''), COALESCE(context, ''), COALESCE(agenda, ''), COALESCE(decision_points, ''), COALESCE(concerns, ''), COALESCE(expected_output, ''), COALESCE(custom_instruction, ''), status,
			COALESCE(bot_call_id, ''), requested_at, command_sent_at, joined_at, ended_at,
			COALESCE(end_reason, ''), last_bot_status_at, COALESCE(last_error, ''), created_at, updated_at
	`, record.ID, nullable(record.WorkspaceID), nullable(record.CreatedByUserID), nullable(record.MeetingID),
		record.JoinURL, record.JoinURLHash, record.Title, record.TitleSource, nullable(record.TitleUpdatedAt),
		nullable(record.UserProvidedTitle), nullable(record.GraphTitle), nullable(record.Provider), nullable(record.ExternalMeetingID),
		nullable(record.JoinMeetingID), nullable(record.JoinWebURL), nullable(record.CanonicalJoinWebURL), nullable(record.ThreadID),
		nullable(record.OrganizerID), nullable(record.OrganizerName), nullable(record.OrganizerEmail), nullable(record.ScheduledStartAt),
		nullable(record.ScheduledEndAt), nullable(record.TitleResolutionErrorCode), nullable(record.TitleResolutionErrorMessage),
		nullable(record.TitleResolvedAt),
		record.Status, nullable(record.BotCallID), record.RequestedAt,
		nullable(record.CommandSentAt), nullable(record.JoinedAt), nullable(record.EndedAt), nullable(record.EndReason),
		nullable(record.LastBotStatusAt), nullable(record.LastError), record.CreatedAt, record.UpdatedAt)
	created, err := scanMeetingSession(row)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit meeting session create: %w", err)
	}
	return created, true, nil
}

func (r *MeetingSessionRepository) GetMeetingSession(ctx context.Context, sessionID string) (*domain.MeetingSession, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(workspace_id, ''), COALESCE(created_by_user_id, ''), COALESCE(meeting_id, ''),
			join_url, join_url_hash, COALESCE(title, ''), COALESCE(title_source, ''),
			title_updated_at, COALESCE(user_provided_title, ''), COALESCE(graph_title, ''),
			COALESCE(provider, ''), COALESCE(external_meeting_id, ''), COALESCE(join_meeting_id, ''),
			COALESCE(join_web_url, ''), COALESCE(canonical_join_web_url, ''),
			COALESCE(thread_id, ''), COALESCE(organizer_id, ''), COALESCE(organizer_name, ''), COALESCE(organizer_email, ''),
			scheduled_start_at, scheduled_end_at, COALESCE(title_resolution_error_code, ''),
			COALESCE(title_resolution_error_message, ''), title_resolved_at, COALESCE(purpose, ''), COALESCE(context, ''), COALESCE(agenda, ''), COALESCE(decision_points, ''), COALESCE(concerns, ''), COALESCE(expected_output, ''), COALESCE(custom_instruction, ''), status,
			COALESCE(bot_call_id, ''), requested_at, command_sent_at, joined_at, ended_at,
			COALESCE(end_reason, ''), last_bot_status_at, COALESCE(last_error, ''), created_at, updated_at
		FROM meeting_sessions
		WHERE id = $1
	`, sessionID)
	return scanMeetingSession(row)
}

func (r *MeetingSessionRepository) ListMeetingSessions(ctx context.Context, workspaceID string, limit int) ([]domain.MeetingSession, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, COALESCE(workspace_id, ''), COALESCE(created_by_user_id, ''), COALESCE(meeting_id, ''),
			join_url, join_url_hash, COALESCE(title, ''), COALESCE(title_source, ''),
			title_updated_at, COALESCE(user_provided_title, ''), COALESCE(graph_title, ''),
			COALESCE(provider, ''), COALESCE(external_meeting_id, ''), COALESCE(join_meeting_id, ''),
			COALESCE(join_web_url, ''), COALESCE(canonical_join_web_url, ''),
			COALESCE(thread_id, ''), COALESCE(organizer_id, ''), COALESCE(organizer_name, ''), COALESCE(organizer_email, ''),
			scheduled_start_at, scheduled_end_at, COALESCE(title_resolution_error_code, ''),
			COALESCE(title_resolution_error_message, ''), title_resolved_at, COALESCE(purpose, ''), COALESCE(context, ''), COALESCE(agenda, ''), COALESCE(decision_points, ''), COALESCE(concerns, ''), COALESCE(expected_output, ''), COALESCE(custom_instruction, ''), status,
			COALESCE(bot_call_id, ''), requested_at, command_sent_at, joined_at, ended_at,
			COALESCE(end_reason, ''), last_bot_status_at, COALESCE(last_error, ''), created_at, updated_at
		FROM meeting_sessions
		WHERE workspace_id = $1
		ORDER BY updated_at DESC, created_at DESC
		LIMIT $2
	`, workspaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("list meeting sessions: %w", err)
	}
	return scanMeetingSessionRows(rows)
}

func (r *MeetingSessionRepository) MarkStaleMeetingSessions(ctx context.Context, staleBefore time.Time, updatedAt time.Time) ([]domain.MeetingSession, error) {
	rows, err := r.db.QueryContext(ctx, `
		UPDATE meeting_sessions
		SET status = 'stale',
			ended_at = COALESCE(ended_at, $2),
			end_reason = CASE
				WHEN COALESCE(end_reason, '') = '' THEN 'session marked stale because no update was received before cutoff'
				ELSE end_reason
			END,
			last_error = CASE
				WHEN COALESCE(last_error, '') = '' THEN 'session marked stale because no update was received before cutoff'
				ELSE last_error
			END,
			updated_at = $2
		WHERE status IN ('requested', 'pending_join', 'command_sent', 'joining', 'joined', 'active', 'recording', 'speech_error', 'speech_throttled')
			AND updated_at < $1
		RETURNING id, COALESCE(workspace_id, ''), COALESCE(created_by_user_id, ''), COALESCE(meeting_id, ''),
			join_url, join_url_hash, COALESCE(title, ''), COALESCE(title_source, ''),
			title_updated_at, COALESCE(user_provided_title, ''), COALESCE(graph_title, ''),
			COALESCE(provider, ''), COALESCE(external_meeting_id, ''), COALESCE(join_meeting_id, ''),
			COALESCE(join_web_url, ''), COALESCE(canonical_join_web_url, ''),
			COALESCE(thread_id, ''), COALESCE(organizer_id, ''), COALESCE(organizer_name, ''), COALESCE(organizer_email, ''),
			scheduled_start_at, scheduled_end_at, COALESCE(title_resolution_error_code, ''),
			COALESCE(title_resolution_error_message, ''), title_resolved_at, COALESCE(purpose, ''), COALESCE(context, ''), COALESCE(agenda, ''), COALESCE(decision_points, ''), COALESCE(concerns, ''), COALESCE(expected_output, ''), COALESCE(custom_instruction, ''), status,
			COALESCE(bot_call_id, ''), requested_at, command_sent_at, joined_at, ended_at,
			COALESCE(end_reason, ''), last_bot_status_at, COALESCE(last_error, ''), created_at, updated_at
	`, staleBefore.UTC().Format(time.RFC3339Nano), updatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("mark stale meeting sessions: %w", err)
	}
	return scanMeetingSessionRows(rows)
}

func (r *MeetingSessionRepository) ListMeetingSessionDebug(ctx context.Context, limit int) ([]domain.MeetingSessionDebug, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT ms.id, COALESCE(ms.workspace_id, ''), COALESCE(ms.created_by_user_id, ''), COALESCE(ms.meeting_id, ''),
			ms.join_url, ms.join_url_hash, COALESCE(ms.title, ''), COALESCE(ms.title_source, ''),
			ms.title_updated_at, COALESCE(ms.user_provided_title, ''), COALESCE(ms.graph_title, ''),
			COALESCE(ms.provider, ''), COALESCE(ms.external_meeting_id, ''), COALESCE(ms.join_meeting_id, ''),
			COALESCE(ms.join_web_url, ''), COALESCE(ms.canonical_join_web_url, ''),
			COALESCE(ms.thread_id, ''), COALESCE(ms.organizer_id, ''), COALESCE(ms.organizer_name, ''),
			COALESCE(ms.organizer_email, ''), ms.scheduled_start_at, ms.scheduled_end_at,
			COALESCE(ms.title_resolution_error_code, ''), COALESCE(ms.title_resolution_error_message, ''),
			ms.title_resolved_at, COALESCE(ms.purpose, ''), COALESCE(ms.context, ''), COALESCE(ms.agenda, ''),
			COALESCE(ms.decision_points, ''), COALESCE(ms.concerns, ''), COALESCE(ms.expected_output, ''),
			COALESCE(ms.custom_instruction, ''), ms.status, COALESCE(ms.bot_call_id, ''), ms.requested_at,
			ms.command_sent_at, ms.joined_at, ms.ended_at, COALESCE(ms.end_reason, ''), ms.last_bot_status_at,
			COALESCE(ms.last_error, ''), ms.created_at, ms.updated_at,
			MAX(ts.received_at_utc)
		FROM meeting_sessions ms
		LEFT JOIN transcript_segments ts ON ts.session_id = ms.id
		GROUP BY ms.id, ms.workspace_id, ms.created_by_user_id, ms.meeting_id, ms.join_url, ms.join_url_hash, ms.title, ms.title_source, ms.title_updated_at,
			ms.user_provided_title, ms.graph_title, ms.provider, ms.external_meeting_id, ms.join_meeting_id,
			ms.join_web_url, ms.canonical_join_web_url, ms.thread_id, ms.organizer_id,
			ms.organizer_name, ms.organizer_email, ms.scheduled_start_at, ms.scheduled_end_at,
			ms.title_resolution_error_code, ms.title_resolution_error_message, ms.title_resolved_at,
			ms.purpose, ms.context, ms.agenda, ms.decision_points, ms.concerns, ms.expected_output, ms.custom_instruction,
			ms.status, ms.bot_call_id,
			ms.requested_at, ms.command_sent_at, ms.joined_at, ms.ended_at, ms.end_reason,
			ms.last_bot_status_at, ms.last_error, ms.created_at, ms.updated_at
		ORDER BY ms.updated_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list meeting session debug: %w", err)
	}
	defer rows.Close()

	items := make([]domain.MeetingSessionDebug, 0)
	for rows.Next() {
		var record meetingSessionRecord
		var titleUpdatedAt, scheduledStartAt, scheduledEndAt, titleResolvedAt sql.NullString
		var commandSentAt, joinedAt, endedAt, lastBotStatusAt, lastTranscriptAt sql.NullString
		if err := rows.Scan(
			&record.ID, &record.WorkspaceID, &record.CreatedByUserID, &record.MeetingID,
			&record.JoinURL, &record.JoinURLHash, &record.Title, &record.TitleSource,
			&titleUpdatedAt, &record.UserProvidedTitle, &record.GraphTitle, &record.Provider,
			&record.ExternalMeetingID, &record.JoinMeetingID, &record.JoinWebURL, &record.CanonicalJoinWebURL,
			&record.ThreadID, &record.OrganizerID, &record.OrganizerName, &record.OrganizerEmail,
			&scheduledStartAt, &scheduledEndAt, &record.TitleResolutionErrorCode,
			&record.TitleResolutionErrorMessage, &titleResolvedAt,
			&record.Purpose, &record.Context, &record.Agenda, &record.DecisionPoints,
			&record.Concerns, &record.ExpectedOutput, &record.CustomInstruction,
			&record.Status, &record.BotCallID, &record.RequestedAt,
			&commandSentAt, &joinedAt, &endedAt, &record.EndReason, &lastBotStatusAt,
			&record.LastError, &record.CreatedAt, &record.UpdatedAt,
			&lastTranscriptAt,
		); err != nil {
			return nil, fmt.Errorf("scan meeting session debug: %w", err)
		}
		record.TitleUpdatedAt = titleUpdatedAt.String
		record.ScheduledStartAt = scheduledStartAt.String
		record.ScheduledEndAt = scheduledEndAt.String
		record.TitleResolvedAt = titleResolvedAt.String
		record.CommandSentAt = commandSentAt.String
		record.JoinedAt = joinedAt.String
		record.EndedAt = endedAt.String
		record.LastBotStatusAt = lastBotStatusAt.String
		session, err := record.toDomain()
		if err != nil {
			return nil, err
		}
		parsedLastTranscriptAt, err := parseOptionalTime("last_transcript_at", lastTranscriptAt.String)
		if err != nil {
			return nil, err
		}
		items = append(items, domain.MeetingSessionDebug{
			MeetingSession:   *session,
			LastTranscriptAt: parsedLastTranscriptAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate meeting session debug: %w", err)
	}
	return items, nil
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
			updated_at = $8,
			title = CASE WHEN $9 = '' THEN title ELSE $9 END,
			title_updated_at = CASE WHEN $9 = '' THEN title_updated_at ELSE $8 END,
			title_source = CASE WHEN $10 = '' THEN title_source ELSE $10 END,
			end_reason = CASE WHEN $11 = '' THEN end_reason ELSE $11 END,
			last_bot_status_at = COALESCE($12, last_bot_status_at)
		WHERE id = $1
		RETURNING id, COALESCE(workspace_id, ''), COALESCE(created_by_user_id, ''), COALESCE(meeting_id, ''),
			join_url, join_url_hash, COALESCE(title, ''), COALESCE(title_source, ''),
			title_updated_at, COALESCE(user_provided_title, ''), COALESCE(graph_title, ''),
			COALESCE(provider, ''), COALESCE(external_meeting_id, ''), COALESCE(join_meeting_id, ''),
			COALESCE(join_web_url, ''), COALESCE(canonical_join_web_url, ''),
			COALESCE(thread_id, ''), COALESCE(organizer_id, ''), COALESCE(organizer_name, ''), COALESCE(organizer_email, ''),
			scheduled_start_at, scheduled_end_at, COALESCE(title_resolution_error_code, ''),
			COALESCE(title_resolution_error_message, ''), title_resolved_at, COALESCE(purpose, ''), COALESCE(context, ''), COALESCE(agenda, ''), COALESCE(decision_points, ''), COALESCE(concerns, ''), COALESCE(expected_output, ''), COALESCE(custom_instruction, ''), status,
			COALESCE(bot_call_id, ''), requested_at, command_sent_at, joined_at, ended_at,
			COALESCE(end_reason, ''), last_bot_status_at, COALESCE(last_error, ''), created_at, updated_at
	`, update.SessionID, string(update.Status), update.BotCallID, nullableTimePtr(update.CommandSentAt),
		nullableTimePtr(update.JoinedAt), nullableTimePtr(update.EndedAt), nullable(update.LastError),
		update.UpdatedAt.UTC().Format(time.RFC3339Nano), update.Title, update.TitleSource, update.EndReason,
		nullableTimePtr(update.LastBotStatusAt))
	return scanMeetingSession(row)
}

func (r *MeetingSessionRepository) UpdateMeetingSessionMetadata(ctx context.Context, update domain.MeetingSessionMetadataUpdate) (*domain.MeetingSession, error) {
	row := r.db.QueryRowContext(ctx, `
		UPDATE meeting_sessions
		SET title = CASE WHEN $2 = '' THEN title ELSE $2 END,
			title_source = CASE WHEN $3 = '' THEN title_source ELSE $3 END,
			title_updated_at = CASE WHEN $2 = '' THEN title_updated_at ELSE $21 END,
			user_provided_title = CASE WHEN $4 = '' THEN user_provided_title ELSE $4 END,
			graph_title = CASE WHEN $5 = '' THEN graph_title ELSE $5 END,
			provider = CASE WHEN $6 = '' THEN provider ELSE $6 END,
			external_meeting_id = CASE WHEN $7 = '' THEN external_meeting_id ELSE $7 END,
			join_meeting_id = CASE WHEN $8 = '' THEN join_meeting_id ELSE $8 END,
			join_web_url = CASE WHEN $9 = '' THEN join_web_url ELSE $9 END,
			canonical_join_web_url = CASE WHEN $10 = '' THEN canonical_join_web_url ELSE $10 END,
			thread_id = CASE WHEN $11 = '' THEN thread_id ELSE $11 END,
			organizer_id = CASE WHEN $12 = '' THEN organizer_id ELSE $12 END,
			organizer_name = CASE WHEN $13 = '' THEN organizer_name ELSE $13 END,
			organizer_email = CASE WHEN $14 = '' THEN organizer_email ELSE $14 END,
			scheduled_start_at = COALESCE($15, scheduled_start_at),
			scheduled_end_at = COALESCE($16, scheduled_end_at),
			title_resolution_error_code = CASE WHEN $17 = '' THEN title_resolution_error_code ELSE $17 END,
			title_resolution_error_message = CASE WHEN $18 = '' THEN title_resolution_error_message ELSE $18 END,
			title_resolved_at = COALESCE($19, title_resolved_at),
			purpose = CASE WHEN $22 = '' THEN purpose ELSE $22 END,
			context = CASE WHEN $23 = '' THEN context ELSE $23 END,
			agenda = CASE WHEN $24 = '' THEN agenda ELSE $24 END,
			decision_points = CASE WHEN $25 = '' THEN decision_points ELSE $25 END,
			concerns = CASE WHEN $26 = '' THEN concerns ELSE $26 END,
			expected_output = CASE WHEN $27 = '' THEN expected_output ELSE $27 END,
			custom_instruction = CASE WHEN $28 = '' THEN custom_instruction ELSE $28 END,
			updated_at = $20
		WHERE id = $1
		RETURNING id, COALESCE(workspace_id, ''), COALESCE(created_by_user_id, ''), COALESCE(meeting_id, ''),
			join_url, join_url_hash, COALESCE(title, ''), COALESCE(title_source, ''),
			title_updated_at, COALESCE(user_provided_title, ''), COALESCE(graph_title, ''),
			COALESCE(provider, ''), COALESCE(external_meeting_id, ''), COALESCE(join_meeting_id, ''),
			COALESCE(join_web_url, ''), COALESCE(canonical_join_web_url, ''),
			COALESCE(thread_id, ''), COALESCE(organizer_id, ''), COALESCE(organizer_name, ''), COALESCE(organizer_email, ''),
			scheduled_start_at, scheduled_end_at, COALESCE(title_resolution_error_code, ''),
			COALESCE(title_resolution_error_message, ''), title_resolved_at, COALESCE(purpose, ''), COALESCE(context, ''), COALESCE(agenda, ''), COALESCE(decision_points, ''), COALESCE(concerns, ''), COALESCE(expected_output, ''), COALESCE(custom_instruction, ''), status,
			COALESCE(bot_call_id, ''), requested_at, command_sent_at, joined_at, ended_at,
			COALESCE(end_reason, ''), last_bot_status_at, COALESCE(last_error, ''), created_at, updated_at
	`, update.SessionID, update.Title, update.TitleSource, update.UserProvidedTitle, update.GraphTitle, update.Provider,
		update.ExternalMeetingID, update.JoinMeetingID, update.JoinWebURL, update.CanonicalJoinWebURL, update.ThreadID,
		update.OrganizerID, update.OrganizerName, update.OrganizerEmail, nullableTimePtr(update.ScheduledStartAt),
		nullableTimePtr(update.ScheduledEndAt), update.TitleResolutionErrorCode, update.TitleResolutionErrorMessage,
		nullableTimePtr(update.TitleResolvedAt), update.UpdatedAt.UTC().Format(time.RFC3339Nano),
		update.UpdatedAt.UTC().Format(time.RFC3339Nano), update.Purpose, update.Context, update.Agenda,
		update.DecisionPoints, update.Concerns, update.ExpectedOutput, update.CustomInstruction)
	return scanMeetingSession(row)
}

type meetingSessionQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func findReusableMeetingSessionByWorkspaceJoinURLHash(ctx context.Context, queryer meetingSessionQueryer, workspaceID, joinURLHash string) (*domain.MeetingSession, error) {
	row := queryer.QueryRowContext(ctx, `
		SELECT id, COALESCE(workspace_id, ''), COALESCE(created_by_user_id, ''), COALESCE(meeting_id, ''),
			join_url, join_url_hash, COALESCE(title, ''), COALESCE(title_source, ''),
			title_updated_at, COALESCE(user_provided_title, ''), COALESCE(graph_title, ''),
			COALESCE(provider, ''), COALESCE(external_meeting_id, ''), COALESCE(join_meeting_id, ''),
			COALESCE(join_web_url, ''), COALESCE(canonical_join_web_url, ''),
			COALESCE(thread_id, ''), COALESCE(organizer_id, ''), COALESCE(organizer_name, ''), COALESCE(organizer_email, ''),
			scheduled_start_at, scheduled_end_at, COALESCE(title_resolution_error_code, ''),
			COALESCE(title_resolution_error_message, ''), title_resolved_at, COALESCE(purpose, ''), COALESCE(context, ''), COALESCE(agenda, ''), COALESCE(decision_points, ''), COALESCE(concerns, ''), COALESCE(expected_output, ''), COALESCE(custom_instruction, ''), status,
			COALESCE(bot_call_id, ''), requested_at, command_sent_at, joined_at, ended_at,
			COALESCE(end_reason, ''), last_bot_status_at, COALESCE(last_error, ''), created_at, updated_at
		FROM meeting_sessions
		WHERE join_url_hash = $1
			AND (($2 = '' AND workspace_id IS NULL) OR workspace_id = $2)
			AND status IN ('requested', 'pending_join', 'command_sent', 'joining', 'joined', 'active', 'recording', 'speech_error', 'speech_throttled')
		ORDER BY updated_at DESC, created_at DESC
		LIMIT 1
	`, joinURLHash, workspaceID)
	return scanMeetingSession(row)
}

type meetingSessionRecord struct {
	ID                          string
	WorkspaceID                 string
	CreatedByUserID             string
	MeetingID                   string
	JoinURL                     string
	JoinURLHash                 string
	Title                       string
	TitleSource                 string
	TitleUpdatedAt              string
	UserProvidedTitle           string
	GraphTitle                  string
	Provider                    string
	ExternalMeetingID           string
	JoinMeetingID               string
	JoinWebURL                  string
	CanonicalJoinWebURL         string
	ThreadID                    string
	OrganizerID                 string
	OrganizerName               string
	OrganizerEmail              string
	ScheduledStartAt            string
	ScheduledEndAt              string
	TitleResolutionErrorCode    string
	TitleResolutionErrorMessage string
	TitleResolvedAt             string
	Purpose                     string
	Context                     string
	Agenda                      string
	DecisionPoints              string
	Concerns                    string
	ExpectedOutput              string
	CustomInstruction           string
	Status                      string
	BotCallID                   string
	RequestedAt                 string
	CommandSentAt               string
	JoinedAt                    string
	EndedAt                     string
	EndReason                   string
	LastBotStatusAt             string
	LastError                   string
	CreatedAt                   string
	UpdatedAt                   string
}

func meetingSessionRecordFromDomain(session domain.MeetingSession) meetingSessionRecord {
	return meetingSessionRecord{
		ID:                          session.ID,
		WorkspaceID:                 session.WorkspaceID,
		CreatedByUserID:             session.CreatedByUserID,
		MeetingID:                   session.MeetingID,
		JoinURL:                     session.JoinURL,
		JoinURLHash:                 session.JoinURLHash,
		Title:                       session.Title,
		TitleSource:                 session.TitleSource,
		TitleUpdatedAt:              formatTime(session.TitleUpdatedAt),
		UserProvidedTitle:           session.UserProvidedTitle,
		GraphTitle:                  session.GraphTitle,
		Provider:                    session.Provider,
		ExternalMeetingID:           session.ExternalMeetingID,
		JoinMeetingID:               session.JoinMeetingID,
		JoinWebURL:                  session.JoinWebURL,
		CanonicalJoinWebURL:         session.CanonicalJoinWebURL,
		ThreadID:                    session.ThreadID,
		OrganizerID:                 session.OrganizerID,
		OrganizerName:               session.OrganizerName,
		OrganizerEmail:              session.OrganizerEmail,
		ScheduledStartAt:            formatTime(session.ScheduledStartAt),
		ScheduledEndAt:              formatTime(session.ScheduledEndAt),
		TitleResolutionErrorCode:    session.TitleResolutionErrorCode,
		TitleResolutionErrorMessage: session.TitleResolutionErrorMessage,
		TitleResolvedAt:             formatTime(session.TitleResolvedAt),
		Purpose:                     session.Purpose,
		Context:                     session.Context,
		Agenda:                      session.Agenda,
		DecisionPoints:              session.DecisionPoints,
		Concerns:                    session.Concerns,
		ExpectedOutput:              session.ExpectedOutput,
		CustomInstruction:           session.CustomInstruction,
		Status:                      string(session.Status),
		BotCallID:                   session.BotCallID,
		RequestedAt:                 formatTime(session.RequestedAt),
		CommandSentAt:               formatTime(session.CommandSentAt),
		JoinedAt:                    formatTime(session.JoinedAt),
		EndedAt:                     formatTime(session.EndedAt),
		EndReason:                   session.EndReason,
		LastBotStatusAt:             formatTime(session.LastBotStatusAt),
		LastError:                   session.LastError,
		CreatedAt:                   formatTime(session.CreatedAt),
		UpdatedAt:                   formatTime(session.UpdatedAt),
	}
}

func scanMeetingSession(row interface{ Scan(dest ...any) error }) (*domain.MeetingSession, error) {
	var record meetingSessionRecord
	var titleUpdatedAt, scheduledStartAt, scheduledEndAt, titleResolvedAt sql.NullString
	var commandSentAt, joinedAt, endedAt, lastBotStatusAt sql.NullString
	err := row.Scan(
		&record.ID, &record.WorkspaceID, &record.CreatedByUserID, &record.MeetingID,
		&record.JoinURL, &record.JoinURLHash, &record.Title, &record.TitleSource,
		&titleUpdatedAt, &record.UserProvidedTitle, &record.GraphTitle, &record.Provider,
		&record.ExternalMeetingID, &record.JoinMeetingID, &record.JoinWebURL, &record.CanonicalJoinWebURL,
		&record.ThreadID, &record.OrganizerID, &record.OrganizerName, &record.OrganizerEmail,
		&scheduledStartAt, &scheduledEndAt, &record.TitleResolutionErrorCode,
		&record.TitleResolutionErrorMessage, &titleResolvedAt,
		&record.Purpose, &record.Context, &record.Agenda, &record.DecisionPoints,
		&record.Concerns, &record.ExpectedOutput, &record.CustomInstruction,
		&record.Status, &record.BotCallID, &record.RequestedAt,
		&commandSentAt, &joinedAt, &endedAt, &record.EndReason, &lastBotStatusAt,
		&record.LastError, &record.CreatedAt, &record.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: meeting session not found", domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("query meeting session: %w", err)
	}
	record.TitleUpdatedAt = titleUpdatedAt.String
	record.ScheduledStartAt = scheduledStartAt.String
	record.ScheduledEndAt = scheduledEndAt.String
	record.TitleResolvedAt = titleResolvedAt.String
	record.CommandSentAt = commandSentAt.String
	record.JoinedAt = joinedAt.String
	record.EndedAt = endedAt.String
	record.LastBotStatusAt = lastBotStatusAt.String
	return record.toDomain()
}

func scanMeetingSessionRows(rows *sql.Rows) ([]domain.MeetingSession, error) {
	defer rows.Close()
	sessions := make([]domain.MeetingSession, 0)
	for rows.Next() {
		session, err := scanMeetingSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, *session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate meeting sessions: %w", err)
	}
	return sessions, nil
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
	titleUpdatedAt, err := parseOptionalTime("title_updated_at", record.TitleUpdatedAt)
	if err != nil {
		return nil, err
	}
	scheduledStartAt, err := parseOptionalTime("scheduled_start_at", record.ScheduledStartAt)
	if err != nil {
		return nil, err
	}
	scheduledEndAt, err := parseOptionalTime("scheduled_end_at", record.ScheduledEndAt)
	if err != nil {
		return nil, err
	}
	titleResolvedAt, err := parseOptionalTime("title_resolved_at", record.TitleResolvedAt)
	if err != nil {
		return nil, err
	}
	lastBotStatusAt, err := parseOptionalTime("last_bot_status_at", record.LastBotStatusAt)
	if err != nil {
		return nil, err
	}
	return &domain.MeetingSession{
		ID:                          record.ID,
		WorkspaceID:                 record.WorkspaceID,
		CreatedByUserID:             record.CreatedByUserID,
		MeetingID:                   record.MeetingID,
		JoinURL:                     record.JoinURL,
		JoinURLHash:                 record.JoinURLHash,
		Title:                       record.Title,
		TitleSource:                 record.TitleSource,
		TitleUpdatedAt:              titleUpdatedAt,
		UserProvidedTitle:           record.UserProvidedTitle,
		GraphTitle:                  record.GraphTitle,
		Provider:                    record.Provider,
		ExternalMeetingID:           record.ExternalMeetingID,
		JoinMeetingID:               record.JoinMeetingID,
		JoinWebURL:                  record.JoinWebURL,
		CanonicalJoinWebURL:         record.CanonicalJoinWebURL,
		ThreadID:                    record.ThreadID,
		OrganizerID:                 record.OrganizerID,
		OrganizerName:               record.OrganizerName,
		OrganizerEmail:              record.OrganizerEmail,
		ScheduledStartAt:            scheduledStartAt,
		ScheduledEndAt:              scheduledEndAt,
		TitleResolutionErrorCode:    record.TitleResolutionErrorCode,
		TitleResolutionErrorMessage: record.TitleResolutionErrorMessage,
		TitleResolvedAt:             titleResolvedAt,
		Purpose:                     record.Purpose,
		Context:                     record.Context,
		Agenda:                      record.Agenda,
		DecisionPoints:              record.DecisionPoints,
		Concerns:                    record.Concerns,
		ExpectedOutput:              record.ExpectedOutput,
		CustomInstruction:           record.CustomInstruction,
		Status:                      domain.MeetingSessionStatus(record.Status),
		BotCallID:                   record.BotCallID,
		RequestedAt:                 requestedAt,
		CommandSentAt:               commandSentAt,
		JoinedAt:                    joinedAt,
		EndedAt:                     endedAt,
		EndReason:                   record.EndReason,
		LastBotStatusAt:             lastBotStatusAt,
		LastError:                   record.LastError,
		CreatedAt:                   createdAt,
		UpdatedAt:                   updatedAt,
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
