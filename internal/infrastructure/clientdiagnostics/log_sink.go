package clientdiagnostics

import (
	"context"
	"io"
	"log/slog"

	"deciscope-core-api/internal/domain"
)

// LogSink は診断イベントをAPIの構造化標準ログ(JSON 1行)へ出す。
// JSONLファイルが参照できない環境(コンテナログしか見られない等)でも
// 同じ内容を時系列で追えるようにするための第2の出力先。
type LogSink struct {
	logger *slog.Logger
}

func NewLogSink(writer io.Writer) *LogSink {
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo})
	return &LogSink{logger: slog.New(handler)}
}

func (s *LogSink) WriteClientDiagnosticEvent(event domain.ClientDiagnosticEvent) error {
	attributes := []any{
		slog.String("component", "client_diagnostics"),
		slog.Time("eventTimestamp", event.Timestamp),
		slog.Time("receivedAt", event.ReceivedAt),
		slog.String("event", event.Event),
		slog.String("sessionId", event.SessionID),
		slog.String("workspaceId", event.WorkspaceID),
		slog.String("tabId", event.TabID),
		slog.String("route", event.Route),
		slog.String("frontendBuildVersion", event.FrontendBuildVersion),
		slog.String("updatedAt", event.UpdatedAt),
		slog.String("rootNodeId", event.RootNodeID),
		slog.String("sessionStatus", event.SessionStatus),
		slog.String("snapshotSource", event.SnapshotSource),
		slog.Int64("sequence", event.Sequence),
	}
	attributes = appendOptionalInt64(attributes, "treeVersion", event.TreeVersion)
	attributes = appendOptionalInt64(attributes, "analysisVersion", event.AnalysisVersion)
	attributes = appendOptionalInt64(attributes, "nodeCount", event.NodeCount)
	if len(event.Details) > 0 {
		attributes = append(attributes, slog.Any("details", event.Details))
	}
	s.logger.LogAttrs(context.Background(), slog.LevelInfo, "client diagnostic event", toAttrs(attributes)...)
	return nil
}

func appendOptionalInt64(attributes []any, key string, value *int64) []any {
	if value == nil {
		return append(attributes, slog.Any(key, nil))
	}
	return append(attributes, slog.Int64(key, *value))
}

func toAttrs(values []any) []slog.Attr {
	attributes := make([]slog.Attr, 0, len(values))
	for _, value := range values {
		if attribute, ok := value.(slog.Attr); ok {
			attributes = append(attributes, attribute)
		}
	}
	return attributes
}
