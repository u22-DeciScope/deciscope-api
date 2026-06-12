package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

type Meeting = domain.Meeting
type Event = domain.Event
type Segment = domain.Segment
type Job = domain.Job
type Report = domain.Report
type Upload = domain.Upload
type TranscriptFinalPayload = domain.TranscriptFinalPayload

var ErrNotFound = domain.ErrNotFound
var NewID = domain.NewID
var IsDurableEventType = domain.IsDurableEventType
var NowMS = domain.NowMS

const EventTranscriptFinal = domain.EventTranscriptFinal
const EventMeetingState = domain.EventMeetingState
const EventTranscriptPartial = domain.EventTranscriptPartial
const EventAnalysisDelta = domain.EventAnalysisDelta

type Store struct {
	db *sql.DB
}

var _ application.MeetingRepository = (*Store)(nil)
var _ application.EventRepository = (*Store)(nil)
var _ application.ReportRepository = (*Store)(nil)
var _ application.JobRepository = (*Store)(nil)
var _ application.UploadRepository = (*Store)(nil)

func Repositories(store *Store) application.Repositories {
	return application.Repositories{
		Meetings: store,
		Events:   store,
		Reports:  store,
		Jobs:     store,
		Uploads:  store,
	}
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) CreateMeeting(ctx context.Context, title, source string) (*Meeting, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled meeting"
	}
	if source == "" {
		source = "fixture_replay"
	}

	now := time.Now().UTC().Format(time.RFC3339)
	meeting := &Meeting{
		ID:        NewID("m"),
		Title:     title,
		Status:    "created",
		Source:    source,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO meetings (id, title, status, source, next_seq, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?)
	`, meeting.ID, meeting.Title, meeting.Status, meeting.Source, meeting.CreatedAt, meeting.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return meeting, nil
}

func (s *Store) ListMeetings(ctx context.Context) ([]Meeting, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, status, source, created_at, updated_at, COALESCE(ended_at, '')
		FROM meetings
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var meetings []Meeting
	for rows.Next() {
		var meeting Meeting
		if err := rows.Scan(&meeting.ID, &meeting.Title, &meeting.Status, &meeting.Source, &meeting.CreatedAt, &meeting.UpdatedAt, &meeting.EndedAt); err != nil {
			return nil, err
		}
		meetings = append(meetings, meeting)
	}
	return meetings, rows.Err()
}

func (s *Store) GetMeeting(ctx context.Context, meetingID string) (*Meeting, error) {
	var meeting Meeting
	err := s.db.QueryRowContext(ctx, `
		SELECT id, title, status, source, created_at, updated_at, COALESCE(ended_at, '')
		FROM meetings
		WHERE id = ?
	`, meetingID).Scan(&meeting.ID, &meeting.Title, &meeting.Status, &meeting.Source, &meeting.CreatedAt, &meeting.UpdatedAt, &meeting.EndedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &meeting, nil
}

func (s *Store) AppendEvent(ctx context.Context, meetingID, eventType string, payload any) (*Event, error) {
	payloadBytes, err := jsonPayload(payload)
	if err != nil {
		return nil, err
	}

	if !IsDurableEventType(eventType) {
		return &Event{
			Type:      eventType,
			MeetingID: meetingID,
			TsMS:      NowMS(),
			Payload:   payloadBytes,
		}, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	tsMS := now.UnixMilli()
	nowText := now.Format(time.RFC3339)

	var seq int64
	if err := tx.QueryRowContext(ctx, `
		UPDATE meetings
		SET next_seq = next_seq + 1, updated_at = ?
		WHERE id = ?
		RETURNING next_seq - 1
	`, nowText, meetingID).Scan(&seq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO meeting_events (meeting_id, seq, type, ts_ms, payload, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, meetingID, seq, eventType, tsMS, string(payloadBytes), nowText); err != nil {
		return nil, err
	}

	if eventType == EventTranscriptFinal {
		if err := insertSegmentFromPayload(ctx, tx, meetingID, seq, payloadBytes, nowText); err != nil {
			return nil, err
		}
	}

	if eventType == EventMeetingState {
		var state struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(payloadBytes, &state); err == nil && state.Status != "" {
			endedAtExpr := sql.NullString{}
			if state.Status == "ended" {
				endedAtExpr = sql.NullString{String: nowText, Valid: true}
			}
			if endedAtExpr.Valid {
				_, err = tx.ExecContext(ctx, `UPDATE meetings SET status = ?, updated_at = ?, ended_at = ? WHERE id = ?`, state.Status, nowText, endedAtExpr.String, meetingID)
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE meetings SET status = ?, updated_at = ? WHERE id = ?`, state.Status, nowText, meetingID)
			}
			if err != nil {
				return nil, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &Event{
		Type:      eventType,
		MeetingID: meetingID,
		Seq:       seq,
		TsMS:      tsMS,
		Payload:   payloadBytes,
	}, nil
}

func (s *Store) ListEvents(ctx context.Context, meetingID string, afterSeq int64) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT type, meeting_id, seq, ts_ms, payload
		FROM meeting_events
		WHERE meeting_id = ? AND seq > ?
		ORDER BY seq ASC
	`, meetingID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var event Event
		var payload string
		if err := rows.Scan(&event.Type, &event.MeetingID, &event.Seq, &event.TsMS, &payload); err != nil {
			return nil, err
		}
		event.Payload = json.RawMessage(payload)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) ListSegments(ctx context.Context, meetingID string, afterSeq int64) ([]Segment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT meeting_id, seq, segment_id, speaker_label, text, start_ms, end_ms, created_at
		FROM meeting_segments
		WHERE meeting_id = ? AND seq > ?
		ORDER BY seq ASC
	`, meetingID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var segments []Segment
	for rows.Next() {
		var segment Segment
		if err := rows.Scan(&segment.MeetingID, &segment.Seq, &segment.SegmentID, &segment.SpeakerLabel, &segment.Text, &segment.StartMS, &segment.EndMS, &segment.CreatedAt); err != nil {
			return nil, err
		}
		segments = append(segments, segment)
	}
	return segments, rows.Err()
}

func (s *Store) ResetMeeting(ctx context.Context, meetingID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM meeting_reports WHERE meeting_id = ?`, meetingID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM meeting_segments WHERE meeting_id = ?`, meetingID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM meeting_events WHERE meeting_id = ?`, meetingID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE meetings SET status = 'created', next_seq = 1, ended_at = NULL, updated_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), meetingID)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) CreateJob(ctx context.Context, jobType, meetingID, status string) (*Job, error) {
	if status == "" {
		status = "queued"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	job := &Job{
		ID:        NewID("job"),
		Type:      jobType,
		Status:    status,
		MeetingID: meetingID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO jobs (id, type, status, meeting_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, job.ID, job.Type, job.Status, nullable(job.MeetingID), job.CreatedAt, job.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return job, nil
}

func (s *Store) CompleteJob(ctx context.Context, jobID string, result any) error {
	resultBytes, err := jsonPayload(result)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET status = 'completed', result = ?, error = NULL, updated_at = ? WHERE id = ?
	`, string(resultBytes), time.Now().UTC().Format(time.RFC3339), jobID)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) FailJob(ctx context.Context, jobID, message string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET status = 'failed', error = ?, updated_at = ? WHERE id = ?
	`, message, time.Now().UTC().Format(time.RFC3339), jobID)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetJob(ctx context.Context, jobID string) (*Job, error) {
	var job Job
	var result, errText sql.NullString
	var meetingID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, type, status, meeting_id, result, error, created_at, updated_at
		FROM jobs
		WHERE id = ?
	`, jobID).Scan(&job.ID, &job.Type, &job.Status, &meetingID, &result, &errText, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	job.MeetingID = meetingID.String
	if result.Valid && result.String != "" {
		job.Result = json.RawMessage(result.String)
	}
	job.Error = errText.String
	return &job, nil
}

func (s *Store) SaveReport(ctx context.Context, meetingID, content string) (*Report, error) {
	report := &Report{
		ArtifactID: NewID("art"),
		MeetingID:  meetingID,
		Format:     "markdown",
		Content:    content,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO meeting_reports (artifact_id, meeting_id, format, content, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, report.ArtifactID, report.MeetingID, report.Format, report.Content, report.CreatedAt)
	if err != nil {
		return nil, err
	}
	return report, nil
}

func (s *Store) LatestReport(ctx context.Context, meetingID string) (*Report, error) {
	var report Report
	err := s.db.QueryRowContext(ctx, `
		SELECT artifact_id, meeting_id, format, content, created_at
		FROM meeting_reports
		WHERE meeting_id = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, meetingID).Scan(&report.ArtifactID, &report.MeetingID, &report.Format, &report.Content, &report.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &report, nil
}

func (s *Store) SaveUpload(ctx context.Context, filename, mediaType, path, jobID string) (*Upload, error) {
	upload := &Upload{
		ID:        NewID("upl"),
		Filename:  filename,
		MediaType: mediaType,
		Path:      path,
		JobID:     jobID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO uploads (id, filename, media_type, path, job_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, upload.ID, upload.Filename, upload.MediaType, upload.Path, upload.JobID, upload.CreatedAt)
	if err != nil {
		return nil, err
	}
	return upload, nil
}

func insertSegmentFromPayload(ctx context.Context, tx *sql.Tx, meetingID string, seq int64, payload []byte, nowText string) error {
	var segment TranscriptFinalPayload
	if err := json.Unmarshal(payload, &segment); err != nil {
		return fmt.Errorf("parse transcript.final payload: %w", err)
	}
	if segment.SegmentID == "" {
		segment.SegmentID = fmt.Sprintf("seg_%06d", seq)
	}
	if segment.SpeakerLabel == "" {
		segment.SpeakerLabel = "Speaker"
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO meeting_segments (meeting_id, seq, segment_id, speaker_label, text, start_ms, end_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(meeting_id, segment_id) DO UPDATE SET
			seq = excluded.seq,
			speaker_label = excluded.speaker_label,
			text = excluded.text,
			start_ms = excluded.start_ms,
			end_ms = excluded.end_ms
	`, meetingID, seq, segment.SegmentID, segment.SpeakerLabel, segment.Text, segment.StartMS, segment.EndMS, nowText)
	return err
}

func jsonPayload(payload any) (json.RawMessage, error) {
	switch p := payload.(type) {
	case nil:
		return json.RawMessage(`{}`), nil
	case json.RawMessage:
		if len(p) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return p, nil
	case []byte:
		if len(p) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return json.RawMessage(p), nil
	default:
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		return b, nil
	}
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
