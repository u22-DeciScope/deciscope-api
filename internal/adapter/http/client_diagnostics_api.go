package httpadapter

import (
	"context"
	"log"
	"net/http"
	"strings"

	authmiddleware "deciscope-core-api/internal/adapter/http/middleware"
	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
)

// clientDiagnosticsBodyLimitBytes は1リクエストのサイズ上限。
// tree_became_empty は直前100件を添付するため、他APIより大きめに取る。
const clientDiagnosticsBodyLimitBytes int64 = 1024 * 1024

// ClientDiagnosticsRecorder は検証済みバッチの記録先。
type ClientDiagnosticsRecorder interface {
	Record(ctx context.Context, batch application.ClientDiagnosticsBatchInput) (application.ClientDiagnosticsResult, error)
}

// ClientDiagnosticsSessionLookup は sessionId が実在し、どのワークスペースに
// 属するかを解決する。
type ClientDiagnosticsSessionLookup interface {
	GetMeetingSession(ctx context.Context, sessionID string) (*domain.MeetingSession, error)
}

// ClientDiagnosticsAPI はブラウザからの診断イベント受け口。
// 認証済みユーザーのみが、自分がアクセスできるワークスペースの、
// そのワークスペースに属する会議セッションについてのみ記録できる。
type ClientDiagnosticsAPI struct {
	recorder  ClientDiagnosticsRecorder
	workspace WorkspaceAccessUseCases
	sessions  ClientDiagnosticsSessionLookup
}

func NewClientDiagnosticsAPI(
	recorder ClientDiagnosticsRecorder,
	workspace WorkspaceAccessUseCases,
	sessions ClientDiagnosticsSessionLookup,
) *ClientDiagnosticsAPI {
	return &ClientDiagnosticsAPI{recorder: recorder, workspace: workspace, sessions: sessions}
}

type clientDiagnosticsRequest struct {
	WorkspaceID          string                         `json:"workspaceId"`
	SessionID            string                         `json:"sessionId"`
	TabID                string                         `json:"tabId"`
	FrontendBuildVersion string                         `json:"frontendBuildVersion"`
	Events               []clientDiagnosticEventRequest `json:"events"`
}

type clientDiagnosticEventRequest struct {
	Timestamp            string         `json:"timestamp"`
	Event                string         `json:"event"`
	SessionID            string         `json:"sessionId"`
	WorkspaceID          string         `json:"workspaceId"`
	TabID                string         `json:"tabId"`
	Route                string         `json:"route"`
	FrontendBuildVersion string         `json:"frontendBuildVersion"`
	TreeVersion          *int64         `json:"treeVersion"`
	AnalysisVersion      *int64         `json:"analysisVersion"`
	UpdatedAt            string         `json:"updatedAt"`
	NodeCount            *int64         `json:"nodeCount"`
	RootNodeID           string         `json:"rootNodeId"`
	SessionStatus        string         `json:"sessionStatus"`
	SnapshotSource       string         `json:"snapshotSource"`
	Sequence             int64          `json:"sequence"`
	Details              map[string]any `json:"details"`
}

// Ingest は診断イベントのバッチを受け取る。
func (api *ClientDiagnosticsAPI) Ingest(w http.ResponseWriter, r *http.Request) {
	if api == nil || api.recorder == nil {
		writeError(w, http.StatusServiceUnavailable, "client_diagnostics_disabled", "client diagnostics is disabled")
		return
	}
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return
	}
	session, _ := authmiddleware.SessionFromContext(r.Context())
	if session == nil || session.User == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}

	var request clientDiagnosticsRequest
	if !decodeLimitedJSONAllowUnknown(w, r, clientDiagnosticsBodyLimitBytes, &request) {
		return
	}

	workspaceID := strings.TrimSpace(request.WorkspaceID)
	sessionID := strings.TrimSpace(request.SessionID)
	if workspaceID == "" || sessionID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "workspaceId and sessionId are required")
		return
	}
	if len(request.Events) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "events must not be empty")
		return
	}

	if !api.authorize(w, r, session.User.ID, workspaceID, sessionID) {
		return
	}

	batch := application.ClientDiagnosticsBatchInput{
		WorkspaceID:          workspaceID,
		SessionID:            sessionID,
		TabID:                strings.TrimSpace(request.TabID),
		FrontendBuildVersion: strings.TrimSpace(request.FrontendBuildVersion),
		UserID:               session.User.ID,
		Events:               clientDiagnosticEventInputs(request.Events),
	}
	result, err := api.recorder.Record(r.Context(), batch)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":   result.Accepted,
		"rejected":   result.Rejected,
		"suppressed": result.Suppressed,
		"reasons":    result.Reasons,
	})
}

// authorize はワークスペース所属と、sessionIdがそのワークスペースの会議セッション
// であることを確認する。存在しない・別ワークスペースのsessionIdは404にする
// (存在有無を漏らさないため、既存の workspaceMeetingSession と同じ扱い)。
func (api *ClientDiagnosticsAPI) authorize(
	w http.ResponseWriter,
	r *http.Request,
	userID, workspaceID, sessionID string,
) bool {
	if api.workspace == nil {
		writeError(w, http.StatusServiceUnavailable, "client_diagnostics_disabled", "client diagnostics is disabled")
		return false
	}
	if _, err := api.workspace.GetWorkspace(r.Context(), userID, workspaceID); err != nil {
		writeStoreError(w, err)
		return false
	}
	if api.sessions == nil {
		writeError(w, http.StatusServiceUnavailable, "client_diagnostics_disabled", "client diagnostics is disabled")
		return false
	}
	meetingSession, err := api.sessions.GetMeetingSession(r.Context(), sessionID)
	if err != nil {
		writeMeetingSessionError(w, err)
		return false
	}
	if meetingSession.WorkspaceID != workspaceID {
		log.Printf("Client diagnostics workspace/session mismatch rejected. requestedWorkspaceId=%s sessionWorkspaceId=%s sessionId=%s", workspaceID, meetingSession.WorkspaceID, meetingSession.ID)
		writeError(w, http.StatusNotFound, "not_found", "meeting session not found")
		return false
	}
	return true
}

func clientDiagnosticEventInputs(events []clientDiagnosticEventRequest) []application.ClientDiagnosticEventInput {
	inputs := make([]application.ClientDiagnosticEventInput, 0, len(events))
	for _, event := range events {
		inputs = append(inputs, application.ClientDiagnosticEventInput{
			Timestamp:            event.Timestamp,
			Event:                event.Event,
			SessionID:            event.SessionID,
			WorkspaceID:          event.WorkspaceID,
			TabID:                event.TabID,
			Route:                event.Route,
			FrontendBuildVersion: event.FrontendBuildVersion,
			TreeVersion:          event.TreeVersion,
			AnalysisVersion:      event.AnalysisVersion,
			UpdatedAt:            event.UpdatedAt,
			NodeCount:            event.NodeCount,
			RootNodeID:           event.RootNodeID,
			SessionStatus:        event.SessionStatus,
			SnapshotSource:       event.SnapshotSource,
			Sequence:             event.Sequence,
			Details:              event.Details,
		})
	}
	return inputs
}
