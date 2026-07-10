package application

import (
	"testing"

	"deciscope-core-api/internal/domain"
)

// ユーザーが入力したタイトル(user_input)は、Teams/Graph由来のタイトルより優先される。
// Graph由来のタイトルは title には適用されず、graph_title として別途保存される
// (metadataGraphTitle のテストを参照)。
func TestDecideMeetingSessionTitleUpdatePrefersUserInputOverGraph(t *testing.T) {
	previous := &domain.MeetingSession{Title: "ユーザー入力の会議名", TitleSource: "user_input"}

	graph := decideMeetingSessionTitleUpdate(previous, "Teams側の会議名", "graph_online_meeting")
	if graph.ApplyTitle || graph.Decision != "keep_existing" {
		t.Fatalf("graph over user_input = %+v, want keep_existing", graph)
	}

	userInput := decideMeetingSessionTitleUpdate(previous, "新しい入力名", "user_input")
	if !userInput.ApplyTitle || userInput.Decision != "overwrite_with_user_input" {
		t.Fatalf("user_input over user_input = %+v, want overwrite_with_user_input", userInput)
	}
}

// ユーザー入力が無い(フォールバック名の)場合は、従来どおりGraph由来のタイトルで上書きする。
func TestDecideMeetingSessionTitleUpdateGraphOverwritesFallback(t *testing.T) {
	previous := &domain.MeetingSession{Title: "Teams会議", TitleSource: "fallback"}
	decision := decideMeetingSessionTitleUpdate(previous, "Teams側の会議名", "graph_online_meeting")
	if !decision.ApplyTitle || decision.Decision != "overwrite_with_graph" {
		t.Fatalf("decision = %+v, want overwrite_with_graph", decision)
	}
}

func TestMetadataGraphTitleSavesGraphTitleOnly(t *testing.T) {
	// user_input のタイトルを graph_title として保存してはいけない。
	if got := metadataGraphTitle(MeetingSessionMetadataUpdateInput{}, "user_input", "入力名"); got != "" {
		t.Fatalf("metadataGraphTitle(user_input) = %q, want empty", got)
	}
	// Graph由来なら、タイトルとして採用されなくても graph_title として保存する。
	if got := metadataGraphTitle(MeetingSessionMetadataUpdateInput{}, "graph_online_meeting", "Teams側の会議名"); got != "Teams側の会議名" {
		t.Fatalf("metadataGraphTitle(graph) = %q, want Teams側の会議名", got)
	}
}

// 再入室(セッション再利用)時も、ユーザーがタイトルを入力していればGraph由来の
// 既存タイトルより優先して適用する。空入力なら既存タイトルを維持する。
func TestShouldApplyCreateTitleToReusedSessionPrefersUserInput(t *testing.T) {
	session := domain.MeetingSession{TitleSource: "graph_online_meeting", Title: "Teams側の会議名"}
	if !shouldApplyCreateTitleToReusedSession(session, "入力名") {
		t.Fatal("non-empty user title should apply even over graph title")
	}
	if shouldApplyCreateTitleToReusedSession(session, "  ") {
		t.Fatal("blank title should not apply")
	}
}
