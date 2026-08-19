package application

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

type publicationCapture struct {
	mu       sync.Mutex
	analyses []domain.MeetingAIAnalysis
}

func (p *publicationCapture) PublishMeetingAIAnalysis(analysis domain.MeetingAIAnalysis) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.analyses = append(p.analyses, analysis)
}

func (p *publicationCapture) last(t *testing.T) domain.MeetingAIAnalysis {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.analyses) == 0 {
		t.Fatal("no analysis was published")
	}
	return p.analyses[len(p.analyses)-1]
}

func TestPublicationCanonicalTreeHashCoversSemanticFieldsAndIgnoresOrdering(t *testing.T) {
	base := &liveAnalysisTree{
		Nodes: []liveAnalysisTreeNode{
			{ID: "root", Kind: "topic", Label: "会議全体"},
			{ID: "fact-1", Kind: "fact", ParentID: "root", Label: "接続障害", Description: "3階で発生", Status: "open", AgendaRefs: []string{"agenda-impact"}},
		},
		Edges:     []liveAnalysisTreeEdge{{Source: "root", Target: "fact-1"}},
		Relations: []liveAnalysisTreeRelation{{ID: "r1", Source: "fact-1", Target: "root", Kind: "supported_by", EvidenceSequenceNos: []int64{3, 2}}},
	}
	reordered := &liveAnalysisTree{
		Nodes:     []liveAnalysisTreeNode{base.Nodes[1], base.Nodes[0]},
		Edges:     []liveAnalysisTreeEdge{{Source: "root", Target: "fact-1"}},
		Relations: []liveAnalysisTreeRelation{{ID: "r1", Source: "fact-1", Target: "root", Kind: "supported_by", EvidenceSequenceNos: []int64{2, 3}}},
	}
	if got, want := liveTreeHash(reordered), liveTreeHash(base); got != want {
		t.Fatalf("order-only hash changed: got=%s want=%s", got, want)
	}
	changed := *base
	changed.Nodes = append([]liveAnalysisTreeNode(nil), base.Nodes...)
	changed.Nodes[1].Description = "2階でも遅延"
	if liveTreeHash(&changed) == liveTreeHash(base) {
		t.Fatal("semantic node description change did not change canonical tree hash")
	}
	relationChanged := *base
	relationChanged.Relations = append([]liveAnalysisTreeRelation(nil), base.Relations...)
	relationChanged.Relations[0].Kind = "limits"
	if liveTreeHash(&relationChanged) == liveTreeHash(base) {
		t.Fatal("semantic relation change did not change canonical tree hash")
	}
}

func TestPublicationNoOpTreeKeepsTreeVersion(t *testing.T) {
	previous := liveAnalysisPayload{
		Summary: "前回", TreeVersion: 4,
		Items: []liveAnalysisItem{{ID: "fact-1", Kind: "fact", Severity: "medium", Title: "接続を確認", Body: "接続を確認した", Status: "open", ClassificationStatus: classificationAssigned}},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"},
			{ID: "fact-1", Kind: "fact", ParentID: treeRootNodeID, Label: "接続を確認"},
		}, Edges: []liveAnalysisTreeEdge{{Source: treeRootNodeID, Target: "fact-1"}}},
	}
	state := previous
	state.Summary = "証拠のみ更新"
	stampCanonicalTreeRevision(&state, previous, 5, nil)
	if state.TreeVersion != 4 {
		t.Fatalf("no-op treeVersion=%d, want 4", state.TreeVersion)
	}
	if state.TreeChanges != nil {
		t.Fatalf("no-op treeChanges=%+v, want nil", state.TreeChanges)
	}
}

func TestPublicationCompletedContractMakesOnlyMaterializedItemsStable(t *testing.T) {
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{
			{ID: "fact-stable", Kind: "fact", Status: "open", ClassificationStatus: classificationAssigned},
			{ID: "issue-pending", Kind: "issue", Status: "open", ClassificationStatus: classificationTentative},
		},
		TreeVersion: 7,
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic"},
			{ID: "fact-stable", Kind: "fact", ParentID: treeRootNodeID},
		}},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	completedAt := time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC)
	completed, err := finalizeCompletedLiveProjection(raw, nil, 9, 10, completedAt)
	if err != nil {
		t.Fatal(err)
	}
	got := previousLiveAnalysisState(completed)
	if got.AnalysisVersion != 9 || got.AIAssistantAnalysisVersion != 9 || got.TreeAnalysisVersion != 9 ||
		got.ItemProjectionVersion != 9 || got.TreeProjectionVersion != 7 ||
		!got.ItemProjectionCompleted || !got.TreeProjectionCompleted || got.HighestAvailableFinalSequenceNo != 10 {
		t.Fatalf("completed projection contract=%+v", got)
	}
	if got.Items[0].ProjectionStatus != "stable" || got.Items[1].ProjectionStatus != "pending_tree_projection" || got.PendingTreeProjectionItemCount != 1 {
		t.Fatalf("projection statuses=%+v pending=%d", got.Items, got.PendingTreeProjectionItemCount)
	}
}

func TestPublicationNoOpBroadcastOmitsTreeArrays(t *testing.T) {
	tree := &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{{ID: treeRootNodeID, Kind: "topic"}, {ID: "fact-1", Kind: "fact", ParentID: treeRootNodeID}}, Edges: []liveAnalysisTreeEdge{{Source: treeRootNodeID, Target: "fact-1"}}}
	previousState := liveAnalysisPayload{Tree: tree, TreeVersion: 4}
	previousRaw, _ := json.Marshal(previousState)
	currentState := previousState
	currentState.AnalysisVersion = 5
	currentState.AIAssistantAnalysisVersion = 5
	currentState.TreeAnalysisVersion = 5
	currentState.TreeProjectionDisposition = "no_op"
	currentRaw, _ := json.Marshal(currentState)
	publisher := &publicationCapture{}
	service := &MeetingAnalysisService{
		publisher: publisher,
		sessions:  make(map[string]*liveAnalysisSessionState),
		now:       func() time.Time { return time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC) },
	}
	service.publishCompletedLiveAnalysis(domain.MeetingAIAnalysis{
		SessionID: "session-publication", Type: domain.MeetingAIAnalysisLive,
		Status: domain.MeetingAIAnalysisCompleted, Version: 5, Payload: currentRaw,
	}, previousRaw)
	published := previousLiveAnalysisState(publisher.last(t).Payload)
	if published.Tree != nil || published.TreePayloadState != "unchanged" || published.PayloadKind != "projection_update" || published.TreeVersion != 4 {
		t.Fatalf("no-op broadcast payload=%+v", published)
	}
}

func TestSchedulerCompleteShortFactUsesNormalDebounce(t *testing.T) {
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	service := &MeetingAnalysisService{config: MeetingAnalysisConfig{
		LiveDebounce: 2 * time.Second, LiveCooldown: 8 * time.Second,
		LiveMaxWait: 18 * time.Second, LiveMinChars: 80,
	}}
	state := &liveAnalysisSessionState{
		pending:      []domain.TranscriptSegment{{SequenceNo: 7, Text: "ルーターとファイアウォールには異常がありませんでした。", IsFinal: true}},
		pendingChars: 28, oldestPendingFinalAt: now, latestPendingFinalAt: now,
	}
	scheduledFor, reason := service.nextLiveAnalysisTimeLocked(state, now)
	if reason == liveAnalysisDeferredBelowMinimumInput || scheduledFor.Sub(now) > service.config.LiveDebounce {
		t.Fatalf("complete fact scheduledFor=%s delay=%s reason=%s", scheduledFor, scheduledFor.Sub(now), reason)
	}
}

func TestSchedulerInFlightCatchUpBypassesCooldownAndMaxWait(t *testing.T) {
	now := time.Date(2026, 8, 3, 10, 0, 10, 0, time.UTC)
	service := &MeetingAnalysisService{config: MeetingAnalysisConfig{
		LiveDebounce: 2 * time.Second, LiveCooldown: 8 * time.Second,
		LiveMaxWait: 18 * time.Second, LiveMinChars: 80,
	}}
	state := &liveAnalysisSessionState{
		pending:      []domain.TranscriptSegment{{SequenceNo: 9, Text: "正確には、トランク設定は入っていました。", IsFinal: true}},
		pendingChars: 22, oldestPendingFinalAt: now, latestPendingFinalAt: now,
		lastAnalysisCompletedAt: now, rerunRequested: true,
		lastDeferredReason: liveAnalysisDeferredAnalysisRunning,
	}
	scheduledFor, reason := service.nextLiveAnalysisTimeLocked(state, now)
	if scheduledFor.After(now) {
		t.Fatalf("catch-up waited %s with reason=%s", scheduledFor.Sub(now), reason)
	}
}

func TestSchedulerDelayMetricsKeepOldestGreaterThanLatest(t *testing.T) {
	now := time.Date(2026, 8, 3, 10, 0, 10, 0, time.UTC)
	delays := livePendingFinalDelays(now, now.Add(-9*time.Second), now.Add(-2*time.Second))
	if delays.FromOldest != 9*time.Second || delays.FromLatest != 2*time.Second {
		t.Fatalf("delay labels were reversed: %+v", delays)
	}
	if delays.FromOldest < delays.FromLatest {
		t.Fatalf("oldest delay must be >= latest delay: %+v", delays)
	}
}

func TestSegmentStitchingMakesAdjacentFragmentAvailableToBothEvidenceRows(t *testing.T) {
	segments := []domain.TranscriptSegment{
		{SequenceNo: 2, SpeakerName: "山下", Text: "本日午前9時20分ごろ、名古屋支社の3階で。", IsFinal: true},
		{SequenceNo: 3, SpeakerName: "山下", Text: "中心に社内ネットワークへ接続できないという問題がありました。", IsFinal: true},
	}
	scope := livePromptEvidenceScope(nil, segments)
	for _, sequenceNo := range []int64{2, 3} {
		text := scope.TranscriptText[sequenceNo]
		if !strings.Contains(text, "3階") || !strings.Contains(text, "接続できない") {
			t.Fatalf("sequence %d was not stitched for analysis evidence: %q", sequenceNo, text)
		}
	}
}

func TestSegmentStitchingKeepsGroundedFactWithBothEvidenceRows(t *testing.T) {
	segments := []domain.TranscriptSegment{
		{SequenceNo: 2, SpeakerName: "山下", Text: "本日午前9時20分ごろ、名古屋支社の3階で。", IsFinal: true},
		{SequenceNo: 3, SpeakerName: "山下", Text: "中心に社内ネットワークへ接続できないという問題がありました。", IsFinal: true},
	}
	scope := livePromptEvidenceScope(nil, segments)
	response := `{"summary":"3階障害","currentTopic":"障害の発生・影響範囲","items":[{"clientKey":"third-floor","kind":"fact","severity":"high","title":"名古屋支社3階を中心に社内ネットワーク接続障害が発生","body":"本日午前9時20分ごろ名古屋支社3階を中心に社内ネットワークへ接続できない障害が発生した","status":"open","evidenceSequenceNos":[2,3],"evidenceSnippets":["本日午前9時20分ごろ、名古屋支社の3階で","中心に社内ネットワークへ接続できないという問題がありました"]}],"newTopics":[],"assignments":[]}`
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(response, nil, &meetingContext{}, 1, []int64{2, 3}, scope, TreeClassificationConfig{})
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	for _, item := range state.Items {
		if containsInt64(item.EvidenceSequenceNos, 2) && containsInt64(item.EvidenceSequenceNos, 3) {
			return
		}
	}
	t.Fatalf("stitched fact missing; items=%+v scope=%v", state.Items, scope.TranscriptText)
}
