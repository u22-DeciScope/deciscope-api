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

type MeetingTreeAuditRepository struct {
	db *sql.DB
}

func NewMeetingTreeAuditRepository(db *sql.DB) *MeetingTreeAuditRepository {
	return &MeetingTreeAuditRepository{db: db}
}

func (r *MeetingTreeAuditRepository) CheckMeetingTreeAuditRepository(ctx context.Context) error {
	var table, activeClaim sql.NullString
	if err := r.db.QueryRowContext(ctx, `SELECT to_regclass('public.meeting_tree_audit_runs')::text`).Scan(&table); err != nil {
		return fmt.Errorf("check meeting tree audit table: %w", err)
	}
	if !table.Valid || table.String == "" {
		return fmt.Errorf("%w: meeting_tree_audit_runs", application.ErrMeetingTreeAuditMigrationMissing)
	}
	if err := r.db.QueryRowContext(ctx, `SELECT to_regclass('public.idx_meeting_tree_audit_runs_active_claim')::text`).Scan(&activeClaim); err != nil {
		return fmt.Errorf("check meeting tree audit active claim index: %w", err)
	}
	if !activeClaim.Valid || activeClaim.String == "" {
		return fmt.Errorf("%w: idx_meeting_tree_audit_runs_active_claim", application.ErrMeetingTreeAuditMigrationMissing)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT trigger_class, disposition, suppression_reason, provider_called,
			meeting_elapsed_seconds, input_payload, raw_response, error_code, error_message,
			result_classification, consecutive_unapplied_runs, operations_proposed,
			operations_canonicalized, operations_valid, operations_applied,
			operations_rejected, rejection_reasons
		FROM meeting_tree_audit_runs
		LIMIT 0
	`)
	if err != nil {
		return fmt.Errorf("%w: incomplete meeting_tree_audit_runs schema: %v", application.ErrMeetingTreeAuditMigrationMissing, err)
	}
	return rows.Close()
}

func (r *MeetingTreeAuditRepository) TryStartMeetingTreeAuditRun(ctx context.Context, run domain.MeetingTreeAuditRun) (bool, error) {
	return writeMeetingTreeAuditRun(ctx, r.db, run, true)
}

func (r *MeetingTreeAuditRepository) SaveMeetingTreeAuditRun(ctx context.Context, run domain.MeetingTreeAuditRun) error {
	_, err := writeMeetingTreeAuditRun(ctx, r.db, run, false)
	return err
}

func (r *MeetingTreeAuditRepository) GetLatestMeetingTreeAuditRun(ctx context.Context, sessionID string) (*domain.MeetingTreeAuditRun, error) {
	row := r.db.QueryRowContext(ctx, meetingTreeAuditSelect+`
		WHERE session_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, sessionID)
	return scanMeetingTreeAuditRun(row)
}

func (r *MeetingTreeAuditRepository) CountMeetingTreeAuditProviderCalls(ctx context.Context, sessionID string, triggerClass domain.MeetingTreeAuditTriggerClass, since time.Time) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM meeting_tree_audit_runs
		WHERE session_id = $1
			AND task <> 'final_tree_review'
			AND provider_called
			AND ($2 = '' OR trigger_class = $2)
			AND ($3::timestamptz IS NULL OR created_at >= $3)
	`, sessionID, string(triggerClass), nullableTime(since)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count meeting tree audit provider calls: %w", err)
	}
	return count, nil
}

func (r *MeetingTreeAuditRepository) ApplyMeetingTreeAudit(ctx context.Context, run domain.MeetingTreeAuditRun, expectedVersion int64, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, bool, error) {
	payload, err := nullableJSONPayload(analysis.Payload)
	if err != nil {
		return nil, false, fmt.Errorf("encode audited live analysis payload: %w", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin meeting tree audit apply: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	updatedAt := analysis.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	row := tx.QueryRowContext(ctx, `
		UPDATE meeting_session_ai_analyses
		SET status=$1, version=$2, payload=$3, model=$4,
			segment_count=$5, input_chars=$6, last_error=$7, updated_at=$8
		WHERE session_id=$9 AND analysis_type='live' AND version=$10
		RETURNING session_id, analysis_type, status, version, payload,
			COALESCE(model, ''), segment_count, input_chars,
			COALESCE(last_error, ''), created_at, updated_at
	`, string(analysis.Status), analysis.Version, payload, nullable(analysis.Model),
		analysis.SegmentCount, analysis.InputChars, nullable(analysis.LastError), updatedAt.UTC(),
		analysis.SessionID, expectedVersion)
	saved, err := scanMeetingAIAnalysis(row)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("compare-and-swap audited live analysis: %w", err)
	}
	if _, err := writeMeetingTreeAuditRun(ctx, tx, run, false); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit meeting tree audit apply: %w", err)
	}
	return saved, true, nil
}

const meetingTreeAuditSelect = `
	SELECT id, session_id, based_on_tree_version,
		COALESCE(resulting_tree_version, 0), trigger_reason, trigger_class, task,
		COALESCE(deployment, ''), COALESCE(model, ''), prompt_version,
		snapshot_hash, status, result, disposition, result_classification,
		consecutive_unapplied_runs, operations_proposed, operations_canonicalized,
		operations_valid, operations_applied, operations_rejected, rejection_reasons,
		suppression_reason,
		provider_called, meeting_elapsed_seconds, input_summary, input_payload,
		raw_response, findings, operations,
		validator_result, prompt_tokens, completion_tokens, elapsed_ms,
		error_code, error_message, created_at, completed_at
	FROM meeting_tree_audit_runs
`

type meetingTreeAuditQueryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func writeMeetingTreeAuditRun(ctx context.Context, queryer meetingTreeAuditQueryer, run domain.MeetingTreeAuditRun, claim bool) (bool, error) {
	input, err := nullableAuditJSON(run.InputSummary)
	if err != nil {
		return false, fmt.Errorf("encode tree audit input summary: %w", err)
	}
	inputPayload, err := nullableAuditJSON(run.InputPayload)
	if err != nil {
		return false, fmt.Errorf("encode tree audit input payload: %w", err)
	}
	findings, err := nullableAuditJSON(run.Findings)
	if err != nil {
		return false, fmt.Errorf("encode tree audit findings: %w", err)
	}
	operations, err := nullableAuditJSON(run.Operations)
	if err != nil {
		return false, fmt.Errorf("encode tree audit operations: %w", err)
	}
	validator, err := nullableAuditJSON(run.ValidatorResult)
	if err != nil {
		return false, fmt.Errorf("encode tree audit validator result: %w", err)
	}
	rejectionReasons, err := nullableAuditJSON(run.RejectionReasons)
	if err != nil {
		return false, fmt.Errorf("encode tree audit rejection reasons: %w", err)
	}
	createdAt := run.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	var resultingVersion any
	if run.ResultingTreeVersion > 0 {
		resultingVersion = run.ResultingTreeVersion
	}
	conflictClause := `ON CONFLICT (id) DO UPDATE SET
			resulting_tree_version=EXCLUDED.resulting_tree_version,
			status=EXCLUDED.status, result=EXCLUDED.result,
			disposition=EXCLUDED.disposition,
			result_classification=EXCLUDED.result_classification,
			consecutive_unapplied_runs=EXCLUDED.consecutive_unapplied_runs,
			operations_proposed=EXCLUDED.operations_proposed,
			operations_canonicalized=EXCLUDED.operations_canonicalized,
			operations_valid=EXCLUDED.operations_valid,
			operations_applied=EXCLUDED.operations_applied,
			operations_rejected=EXCLUDED.operations_rejected,
			rejection_reasons=EXCLUDED.rejection_reasons,
			suppression_reason=EXCLUDED.suppression_reason,
			provider_called=EXCLUDED.provider_called,
			meeting_elapsed_seconds=EXCLUDED.meeting_elapsed_seconds,
			input_summary=EXCLUDED.input_summary,
			input_payload=EXCLUDED.input_payload,
			raw_response=EXCLUDED.raw_response,
			findings=EXCLUDED.findings, operations=EXCLUDED.operations,
			validator_result=EXCLUDED.validator_result,
			prompt_tokens=EXCLUDED.prompt_tokens,
			completion_tokens=EXCLUDED.completion_tokens,
			elapsed_ms=EXCLUDED.elapsed_ms,
			error_code=EXCLUDED.error_code,
			error_message=EXCLUDED.error_message,
			completed_at=EXCLUDED.completed_at,
			deployment=EXCLUDED.deployment, model=EXCLUDED.model`
	if claim {
		conflictClause = `ON CONFLICT (session_id, task, based_on_tree_version, snapshot_hash, prompt_version, deployment) WHERE status = 'running' DO NOTHING`
	}
	result, err := queryer.ExecContext(ctx, `
		INSERT INTO meeting_tree_audit_runs (
			id, session_id, based_on_tree_version, resulting_tree_version,
			trigger_reason, trigger_class, task, deployment, model, prompt_version,
			snapshot_hash, status, result, disposition, result_classification,
			consecutive_unapplied_runs, operations_proposed, operations_canonicalized,
			operations_valid, operations_applied, operations_rejected, rejection_reasons,
			suppression_reason,
			provider_called, meeting_elapsed_seconds, input_summary, input_payload,
			raw_response, findings, operations, validator_result,
			prompt_tokens, completion_tokens, elapsed_ms, error_code, error_message,
			created_at, completed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38)
	`+conflictClause, run.ID, run.SessionID, run.BasedOnTreeVersion, resultingVersion,
		run.TriggerReason, string(run.TriggerClass), run.Task,
		run.Deployment, run.Model, run.PromptVersion, run.SnapshotHash, string(run.Status),
		run.Result, run.Disposition, string(run.ResultClassification), run.ConsecutiveUnappliedRuns,
		run.OperationsProposed, run.OperationsCanonicalized, run.OperationsValid,
		run.OperationsApplied, run.OperationsRejected, rejectionReasons, run.SuppressionReason,
		run.ProviderCalled, run.MeetingElapsedSeconds, input, inputPayload, run.RawResponse,
		findings, operations, validator, run.PromptTokens, run.CompletionTokens,
		run.ElapsedMilliseconds, run.ErrorCode, run.ErrorMessage, createdAt.UTC(), run.CompletedAt)
	if err != nil {
		return false, fmt.Errorf("save meeting tree audit run: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect meeting tree audit write: %w", err)
	}
	return rows > 0, nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullableAuditJSON(value json.RawMessage) (any, error) {
	if len(value) == 0 {
		return nil, nil
	}
	if !json.Valid(value) {
		return nil, fmt.Errorf("invalid json")
	}
	return []byte(value), nil
}

type meetingTreeAuditScanner interface {
	Scan(...any) error
}

func scanMeetingTreeAuditRun(row meetingTreeAuditScanner) (*domain.MeetingTreeAuditRun, error) {
	var run domain.MeetingTreeAuditRun
	var status, triggerClass, resultClassification string
	var input, inputPayload, findings, operations, validator, rejectionReasons []byte
	var completedAt sql.NullTime
	err := row.Scan(&run.ID, &run.SessionID, &run.BasedOnTreeVersion,
		&run.ResultingTreeVersion, &run.TriggerReason, &triggerClass,
		&run.Task, &run.Deployment, &run.Model, &run.PromptVersion, &run.SnapshotHash,
		&status, &run.Result, &run.Disposition, &resultClassification,
		&run.ConsecutiveUnappliedRuns, &run.OperationsProposed, &run.OperationsCanonicalized,
		&run.OperationsValid, &run.OperationsApplied, &run.OperationsRejected, &rejectionReasons,
		&run.SuppressionReason,
		&run.ProviderCalled, &run.MeetingElapsedSeconds, &input, &inputPayload,
		&run.RawResponse, &findings, &operations, &validator,
		&run.PromptTokens, &run.CompletionTokens, &run.ElapsedMilliseconds,
		&run.ErrorCode, &run.ErrorMessage, &run.CreatedAt, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: meeting tree audit run not found", domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("query meeting tree audit run: %w", err)
	}
	run.TriggerClass = domain.MeetingTreeAuditTriggerClass(triggerClass)
	run.Status = domain.MeetingTreeAuditStatus(status)
	run.ResultClassification = domain.MeetingTreeAuditResultClassification(resultClassification)
	run.InputSummary = append(json.RawMessage(nil), input...)
	run.InputPayload = append(json.RawMessage(nil), inputPayload...)
	run.Findings = append(json.RawMessage(nil), findings...)
	run.Operations = append(json.RawMessage(nil), operations...)
	run.ValidatorResult = append(json.RawMessage(nil), validator...)
	run.RejectionReasons = append(json.RawMessage(nil), rejectionReasons...)
	run.CreatedAt = run.CreatedAt.UTC()
	if completedAt.Valid {
		completed := completedAt.Time.UTC()
		run.CompletedAt = &completed
	}
	return &run, nil
}

var _ application.MeetingTreeAuditRepository = (*MeetingTreeAuditRepository)(nil)
