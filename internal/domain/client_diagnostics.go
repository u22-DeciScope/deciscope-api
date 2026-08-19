package domain

import "time"

// ClientDiagnosticEventName はブラウザ側から送られてくる診断イベントの種別。
// 議論ツリーが表示中に消える事象を、発生後にAPI側ログだけで時系列追跡できる
// ようにするための最小語彙で、これ以外の名前はAPIが受理しない。
type ClientDiagnosticEventName string

const (
	ClientDiagnosticTreeStoreInitialized    ClientDiagnosticEventName = "tree_store_initialized"
	ClientDiagnosticTreeComponentMounted    ClientDiagnosticEventName = "tree_component_mounted"
	ClientDiagnosticTreeComponentUnmounted  ClientDiagnosticEventName = "tree_component_unmounted"
	ClientDiagnosticSessionHookCreated      ClientDiagnosticEventName = "session_hook_created"
	ClientDiagnosticSessionHookDisposed     ClientDiagnosticEventName = "session_hook_disposed"
	ClientDiagnosticRestFetchStarted        ClientDiagnosticEventName = "rest_fetch_started"
	ClientDiagnosticRestSnapshotReceived    ClientDiagnosticEventName = "rest_snapshot_received"
	ClientDiagnosticWSConnected             ClientDiagnosticEventName = "ws_connected"
	ClientDiagnosticWSDisconnected          ClientDiagnosticEventName = "ws_disconnected"
	ClientDiagnosticWSReconnecting          ClientDiagnosticEventName = "ws_reconnecting"
	ClientDiagnosticWSSnapshotReceived      ClientDiagnosticEventName = "ws_snapshot_received"
	ClientDiagnosticSnapshotAdopted         ClientDiagnosticEventName = "snapshot_adopted"
	ClientDiagnosticSnapshotRejected        ClientDiagnosticEventName = "snapshot_rejected"
	ClientDiagnosticTreeStateChanged        ClientDiagnosticEventName = "tree_state_changed"
	ClientDiagnosticTreeBecameEmpty         ClientDiagnosticEventName = "tree_became_empty"
	ClientDiagnosticTreeRenderState         ClientDiagnosticEventName = "tree_render_state"
	ClientDiagnosticTreeRenderAnomaly       ClientDiagnosticEventName = "tree_render_anomaly"
	ClientDiagnosticTreeRenderRecovery      ClientDiagnosticEventName = "tree_render_recovery"
	ClientDiagnosticTreeVisibilityUnhealthy ClientDiagnosticEventName = "tree_visibility_unhealthy"
	ClientDiagnosticTreeVisibilityRecovered ClientDiagnosticEventName = "tree_visibility_recovered"
	ClientDiagnosticTreeManualViewReset     ClientDiagnosticEventName = "tree_manual_view_reset"
	ClientDiagnosticTreeSwapStarted         ClientDiagnosticEventName = "tree_swap_started"
	ClientDiagnosticTreePendingReady        ClientDiagnosticEventName = "tree_pending_ready"
	ClientDiagnosticTreeSwapCommitted       ClientDiagnosticEventName = "tree_swap_committed"
	ClientDiagnosticTreeSwapFailed          ClientDiagnosticEventName = "tree_swap_failed"
	ClientDiagnosticTreeSwapKeptPrevious    ClientDiagnosticEventName = "tree_swap_kept_previous"
	ClientDiagnosticTreeManualResetStarted  ClientDiagnosticEventName = "tree_manual_reset_started"
	ClientDiagnosticTreeManualResetIgnored  ClientDiagnosticEventName = "tree_manual_reset_ignored_duplicate"
	ClientDiagnosticTreeManualResetComplete ClientDiagnosticEventName = "tree_manual_reset_completed"
	ClientDiagnosticStoreResetRequested     ClientDiagnosticEventName = "store_reset_requested"
	ClientDiagnosticStoreResetExecuted      ClientDiagnosticEventName = "store_reset_executed"
	ClientDiagnosticRouteChanged            ClientDiagnosticEventName = "route_changed"
	ClientDiagnosticReactErrorCaptured      ClientDiagnosticEventName = "react_error_captured"
)

var clientDiagnosticEventNames = map[ClientDiagnosticEventName]struct{}{
	ClientDiagnosticTreeStoreInitialized:    {},
	ClientDiagnosticTreeComponentMounted:    {},
	ClientDiagnosticTreeComponentUnmounted:  {},
	ClientDiagnosticSessionHookCreated:      {},
	ClientDiagnosticSessionHookDisposed:     {},
	ClientDiagnosticRestFetchStarted:        {},
	ClientDiagnosticRestSnapshotReceived:    {},
	ClientDiagnosticWSConnected:             {},
	ClientDiagnosticWSDisconnected:          {},
	ClientDiagnosticWSReconnecting:          {},
	ClientDiagnosticWSSnapshotReceived:      {},
	ClientDiagnosticSnapshotAdopted:         {},
	ClientDiagnosticSnapshotRejected:        {},
	ClientDiagnosticTreeStateChanged:        {},
	ClientDiagnosticTreeBecameEmpty:         {},
	ClientDiagnosticTreeRenderState:         {},
	ClientDiagnosticTreeRenderAnomaly:       {},
	ClientDiagnosticTreeRenderRecovery:      {},
	ClientDiagnosticTreeVisibilityUnhealthy: {},
	ClientDiagnosticTreeVisibilityRecovered: {},
	ClientDiagnosticTreeManualViewReset:     {},
	ClientDiagnosticTreeSwapStarted:         {},
	ClientDiagnosticTreePendingReady:        {},
	ClientDiagnosticTreeSwapCommitted:       {},
	ClientDiagnosticTreeSwapFailed:          {},
	ClientDiagnosticTreeSwapKeptPrevious:    {},
	ClientDiagnosticTreeManualResetStarted:  {},
	ClientDiagnosticTreeManualResetIgnored:  {},
	ClientDiagnosticTreeManualResetComplete: {},
	ClientDiagnosticStoreResetRequested:     {},
	ClientDiagnosticStoreResetExecuted:      {},
	ClientDiagnosticRouteChanged:            {},
	ClientDiagnosticReactErrorCaptured:      {},
}

// IsKnownClientDiagnosticEvent は既知のイベント名かどうかを返す。
func IsKnownClientDiagnosticEvent(name string) bool {
	_, ok := clientDiagnosticEventNames[ClientDiagnosticEventName(name)]
	return ok
}

// ClientDiagnosticEventNames は既知イベント名の一覧を返す(エラーメッセージ用)。
func ClientDiagnosticEventNames() []string {
	names := make([]string, 0, len(clientDiagnosticEventNames))
	for name := range clientDiagnosticEventNames {
		names = append(names, string(name))
	}
	return names
}

// IsCriticalClientDiagnosticEvent は高頻度抑制の対象外イベントを判定する。
// 検出した異常に加え、ライフサイクル系(store生成・component mount/unmount・
// hook生成/破棄・reset)を含める。これらは本来まれな事象であり、短時間に
// 繰り返されること自体が議論ツリー消失の兆候なので間引いてはならない。
// クライアント側の NEVER_THROTTLED_DIAGNOSTIC_EVENTS と一致させること。
func IsCriticalClientDiagnosticEvent(name string) bool {
	switch ClientDiagnosticEventName(name) {
	case ClientDiagnosticTreeBecameEmpty,
		ClientDiagnosticTreeRenderAnomaly,
		ClientDiagnosticTreeRenderRecovery,
		ClientDiagnosticTreeVisibilityUnhealthy,
		ClientDiagnosticTreeVisibilityRecovered,
		ClientDiagnosticTreeManualViewReset,
		ClientDiagnosticTreeSwapFailed,
		ClientDiagnosticTreeSwapKeptPrevious,
		ClientDiagnosticTreeManualResetComplete,
		ClientDiagnosticReactErrorCaptured,
		ClientDiagnosticTreeStoreInitialized,
		ClientDiagnosticTreeComponentMounted,
		ClientDiagnosticTreeComponentUnmounted,
		ClientDiagnosticSessionHookCreated,
		ClientDiagnosticSessionHookDisposed,
		ClientDiagnosticStoreResetRequested,
		ClientDiagnosticStoreResetExecuted:
		return true
	default:
		return false
	}
}

// ClientDiagnosticEvent はサニタイズ済みの1件の診断イベント。
// 認証トークン・Authorizationヘッダー・文字起こし本文・メールアドレスなどの
// 機密情報はこの型に到達する前にアプリケーション層で除去される。
type ClientDiagnosticEvent struct {
	// Timestamp はブラウザが記録した発生時刻。
	Timestamp time.Time `json:"timestamp"`
	// ReceivedAt はAPIが受信した時刻。ブラウザ時計のずれを補正して読むために残す。
	ReceivedAt           time.Time      `json:"receivedAt"`
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
	Details              map[string]any `json:"details,omitempty"`
	// UserID は認証済みセッションからサーバー側で付与する。ブラウザ入力は使わない。
	UserID string `json:"userId,omitempty"`
	// Sequence はブラウザタブ内での連番。欠落・順序逆転の検出に使う。
	Sequence int64 `json:"sequence,omitempty"`
}
