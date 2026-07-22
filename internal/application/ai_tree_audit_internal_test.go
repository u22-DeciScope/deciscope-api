package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

func TestDeterministicTreeAuditPrecheckReplaysTargetSessionAnomalies(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	findings := deterministicTreeAuditPrecheck(state, mc, roles, TreeAuditConfig{})

	assertAuditFindingForNode(t, findings, TreeAuditSubjectMismatch, "item-risk-rare-plants")
	assertAuditFindingForNode(t, findings, TreeAuditCrossAgendaContamination, "item-risk-rare-plants")
	assertAuditFindingForNode(t, findings, TreeAuditCandidateMixedSubjects, "item-todo-wind-standard")
	assertAuditFindingForNode(t, findings, TreeAuditCandidateShouldFoldIntoTopic, "item-todo-wind-standard")
	assertAuditFindingForNode(t, findings, TreeAuditFloatingTentativeCandidate, "item-todo-wind-standard")
	assertAuditFindingForNode(t, findings, TreeAuditCandidateFragmentation, "item-risk-rare-plants")

	for _, finding := range findings {
		if (finding.Type == TreeAuditSubjectMismatch || finding.Type == TreeAuditCrossAgendaContamination) && containsExactString(finding.NodeIDs, "item-decision-public-web") {
			t.Fatalf("correct public-information item was flagged: %+v", finding)
		}
	}
	if roles[28] != treeAuditEvidenceReference {
		t.Fatalf("sequence 28 role = %q, want reference", roles[28])
	}
	if roles[22] != treeAuditEvidencePrimary {
		t.Fatalf("sequence 22 role = %q, want primary", roles[22])
	}
	integrity := validateTreeIntegrity(state.Tree, state.Items, mc)
	if !integrity.Valid {
		t.Fatalf("fixture integrity = %+v", integrity)
	}
	byType := make(map[TreeAuditFindingType]int)
	for _, finding := range findings {
		byType[finding.Type]++
	}
	t.Logf("target replay: treeVersion=%d findings=%d byType=%v nodes=%d edges=%d coverage=%d integrityValid=%t", state.TreeVersion, len(findings), byType, len(state.Tree.Nodes), len(state.Tree.Edges), state.CoveredThroughSequenceNo, integrity.Valid)
}

// TestClassifyTreeAuditEvidenceKeepsPersistedPrimaryUtterance covers H1: a
// primary utterance must not be demoted to "reference" by
// looksLikeTreeAuditReference's heuristic just because it also happens to
// resemble the very item it was extracted from, or contains a generic
// status-review word ("確認"), when the deterministic timeline and the
// item's own persisted EvidenceRoles both already say primary/correction.
// This reproduces session_7e10430ec0ac3b82's seq14: a real "作業者とは別の
// 担当者が設定内容を確認するダブルチェックを必須にします。また、…疎通確認を
// 実施する…" utterance contains 確認 twice and closely resembles both the
// decision item it produced and a label-derived topic, yet remains the
// utterance's own primary evidence, not a reference to something else.
func TestClassifyTreeAuditEvidenceKeepsPersistedPrimaryUtterance(t *testing.T) {
	// ケース1: 永続化済みprimaryの本編発話は、自己参照・statusReviewの混同
	// 要因があってもprimaryのまま。
	seq14Text := "まず、ネットワーク機器を交換する際は、作業者とは別の担当者が設定内容を確認するダブルチェックを必須にします。また、交換前後でブイランごとの疎通確認を実施するチェックリストを作成します。"
	primaryState := liveAnalysisPayload{
		Items: []liveAnalysisItem{
			{ID: "item-decision-doublecheck", Kind: "decision", Title: "ダブルチェックを必須にします", Body: seq14Text, Status: "open",
				EvidenceSequenceNos: []int64{14}, EvidenceRoles: []liveEvidenceRoleRef{{SequenceNo: 14, Role: liveEvidencePrimary}}},
		},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "root", Origin: topicOriginSystem},
			// ラベル由来topic(同じ発話から生まれた話題): matchedTopicsを
			// 誤って稼働させうる混同要因として同席させる。
			{ID: "candidate-doublecheck", Kind: "topic", ParentID: treeRootNodeID, Label: "ダブルチェックとチェックリスト", Description: "設定内容の確認運用", Origin: topicOriginDynamic},
		}},
	}
	primarySegments := []domain.TranscriptSegment{{SequenceNo: 14, Text: seq14Text, IsFinal: true}}
	primaryRoles := classifyTreeAuditEvidence(primaryState, primarySegments)
	if primaryRoles[14] != treeAuditEvidencePrimary {
		t.Fatalf("roles[14] = %q, want primary (persisted-primary utterance must survive the self-referential/status-review heuristic)", primaryRoles[14])
	}

	// ケース2: 純粋なrecap発話(永続化済みreference_recap)はreferenceのまま
	// (L722-728の優先ルールは変更していないことの回帰確認)。
	recapState := liveAnalysisPayload{
		Items: []liveAnalysisItem{
			{ID: "item-decision-doublecheck-recap", Kind: "decision", Title: "ダブルチェックの再掲", Body: "再発防止として、設定のダブルチェックとvランごとの疎通確認を必須にします。", Status: "open",
				EvidenceSequenceNos: []int64{27}, EvidenceRoles: []liveEvidenceRoleRef{{SequenceNo: 27, Role: liveEvidenceReferenceRecap}}},
		},
	}
	recapSegments := []domain.TranscriptSegment{{SequenceNo: 27, Text: "再発防止として、設定のダブルチェックとvランごとの疎通確認を必須にします。", IsFinal: true}}
	recapRoles := classifyTreeAuditEvidence(recapState, recapSegments)
	if recapRoles[27] != treeAuditEvidenceReference {
		t.Fatalf("roles[27] = %q, want reference (persisted reference_recap must still win)", recapRoles[27])
	}

	// ケース3: 同一item内でprimaryとrecapの両evidenceが混在する場合
	// ([9,29]型)、9はprimaryのまま、29はreferenceになる。
	mixedState := liveAnalysisPayload{
		Items: []liveAnalysisItem{
			{ID: "item-issue-investigation-cause", Kind: "issue", Subtype: issueSubtypeInvestigation, Title: "2階通信遅延の原因調査", Body: "2階の通信遅延の原因はvラン設定だけで説明できるか確認できていない", Status: "open",
				EvidenceSequenceNos: []int64{9, 29},
				EvidenceRoles:       []liveEvidenceRoleRef{{SequenceNo: 9, Role: liveEvidencePrimary}, {SequenceNo: 29, Role: liveEvidenceReferenceRecap}}},
		},
	}
	mixedSegments := []domain.TranscriptSegment{
		{SequenceNo: 9, Text: "2階の通信遅延の原因はvラン設定だけで説明できるか確認できていません。", IsFinal: true},
		{SequenceNo: 29, Text: "2階の通信遅延の原因と監視アラートの条件は、未解決事項として残します。", IsFinal: true},
	}
	mixedRoles := classifyTreeAuditEvidence(mixedState, mixedSegments)
	if mixedRoles[9] != treeAuditEvidencePrimary {
		t.Fatalf("roles[9] = %q, want primary", mixedRoles[9])
	}
	if mixedRoles[29] != treeAuditEvidenceReference {
		t.Fatalf("roles[29] = %q, want reference", mixedRoles[29])
	}
}

func TestTreeAuditPatchValidatorAllowsOnlySafeSemanticImprovement(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-move-plant", Type: TreeAuditMoveItem,
		TargetCanonicalItemID: "item-risk-rare-plants", FromParentCanonicalNodeID: "candidate-info-public",
		ToParentCanonicalNodeID: "candidate-plant-study", Confidence: 0.97,
		Reason: "湿地・希少植物subjectへ戻す", EvidenceSequenceNos: []int64{22},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-1", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 || !result.TreeIntegrityValid {
		t.Fatalf("validator result = %+v", result)
	}
	if node := treeNodeByID(dry.Tree, operation.TargetCanonicalItemID); node == nil || node.ParentID != operation.ToParentCanonicalNodeID || node.LastParentChangeSource != "tree_auditor" {
		t.Fatalf("moved node = %+v", node)
	}
	if dry.ChangeSource != "tree_auditor" || dry.TreeChanges == nil || dry.TreeChanges.Source != "tree_auditor" {
		t.Fatalf("audit provenance missing: %+v %+v", dry.ChangeSource, dry.TreeChanges)
	}
}

func TestTreeIntegrityLayerRejectsInvalidKindAndHardDepth(t *testing.T) {
	payload, _, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Tree.Nodes = append(state.Tree.Nodes,
		liveAnalysisTreeNode{ID: "group-depth-1", Kind: "group", ParentID: stableAgendaTopicID("agenda-1", 0), Label: "深さ1"},
		liveAnalysisTreeNode{ID: "group-depth-2", Kind: "group", ParentID: "group-depth-1", Label: "深さ2"},
		liveAnalysisTreeNode{ID: "group-depth-3", Kind: "group", ParentID: "group-depth-2", Label: "深さ3"},
		liveAnalysisTreeNode{ID: "group-depth-4", Kind: "group", ParentID: "group-depth-3", Label: "深さ4"},
		liveAnalysisTreeNode{ID: "invalid-depth-node", Kind: "alien", ParentID: "group-depth-4", Label: "invalid"},
	)
	rebuildTreeAuditEdges(state.Tree)
	integrity := validateTreeIntegrity(state.Tree, state.Items, mc)
	if integrity.Valid || !containsExactString(integrity.InvalidKindNodeIDs, "invalid-depth-node") || !containsExactString(integrity.HardDepthNodeIDs, "invalid-depth-node") {
		t.Fatalf("integrity = %+v", integrity)
	}
}

func TestTreeAuditPrecheckDetectsStrongWindAndMeetingDateUnderPlantTopic(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	for index := range state.Tree.Nodes {
		if state.Tree.Nodes[index].ID == "item-todo-wind-standard" {
			state.Tree.Nodes[index].ParentID = "candidate-plant-study"
		}
	}
	for index := range state.Items {
		if state.Items[index].ID == "item-todo-wind-standard" {
			state.Items[index].ClassificationStatus = classificationAssigned
			state.Items[index].CandidateTopicID = ""
		}
	}
	state.Items = append(state.Items, liveAnalysisItem{
		ID: "item-question-meeting-date", Kind: "question", Title: "住民説明会の開催日程",
		Body: "自治会から候補日を受け取った後に確定", Status: "open",
		ClassificationStatus: classificationAssigned, AssignmentConfidence: .6,
		EvidenceSequenceNos: []int64{20},
	})
	state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
		ID: "item-question-meeting-date", Kind: "question", ParentID: "candidate-plant-study",
		Label: "住民説明会の開催日程", Status: "open",
	})
	rebuildTreeAuditEdges(state.Tree)
	segments = append(segments, domain.TranscriptSegment{SessionID: "session_26959b9519c5f880", SequenceNo: 20, Text: "住民説明会の開催日は自治会から候補日を受け取った後に確定します。", IsFinal: true})
	roles := classifyTreeAuditEvidence(state, segments)
	findings := deterministicTreeAuditPrecheck(state, mc, roles, TreeAuditConfig{})
	assertAuditFindingForNode(t, findings, TreeAuditSubjectMismatch, "item-todo-wind-standard")
	assertAuditFindingForNode(t, findings, TreeAuditSubjectMismatch, "item-question-meeting-date")
}

func TestTreeAuditPatchValidatorRejectsWeakReferenceAndInvalidDetailTargets(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	operations := []treeAuditOperation{
		{OperationID: "weak", Type: TreeAuditMoveItem, TargetCanonicalItemID: "item-decision-public-web", FromParentCanonicalNodeID: "candidate-info-public", ToParentCanonicalNodeID: "candidate-plant-study", Confidence: 0.99, EvidenceSequenceNos: []int64{17}},
		{OperationID: "reference", Type: TreeAuditMoveItem, TargetCanonicalItemID: "item-todo-wind-standard", FromParentCanonicalNodeID: treeUnclassifiedTopicID, ToParentCanonicalNodeID: stableAgendaTopicID("agenda-2", 0), Confidence: 0.99, EvidenceSequenceNos: []int64{28}},
		{OperationID: "fixed", Type: TreeAuditMoveItem, TargetCanonicalItemID: stableAgendaTopicID("agenda-2", 0), FromParentCanonicalNodeID: treeRootNodeID, ToParentCanonicalNodeID: "candidate-plant-study", Confidence: 1, EvidenceSequenceNos: []int64{13}},
		{OperationID: "self", Type: TreeAuditMoveItem, TargetCanonicalItemID: "item-risk-rare-plants", FromParentCanonicalNodeID: "candidate-info-public", ToParentCanonicalNodeID: "item-risk-rare-plants", Confidence: 1, EvidenceSequenceNos: []int64{22}},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, operations, segments, mc, roles, TreeAuditConfig{}, "audit-1", 13, true)
	if result.OperationsValid != 0 || result.OperationsRejected != len(operations) {
		t.Fatalf("validator result = %+v", result)
	}
	reasons := make(map[string]string)
	for _, evaluation := range result.Evaluations {
		reasons[evaluation.OperationID] = evaluation.Reason
	}
	if reasons["weak"] != "parent_stickiness_margin" || reasons["reference"] != "reference_evidence_only" || reasons["fixed"] != "unknown_target_node" || reasons["self"] != "self_parent" {
		t.Fatalf("rejection reasons = %#v", reasons)
	}
}

// TestTreeAuditAppliesWhenEnabledAndPersistsRunDetails replaces the former
// shadow-mode test now that the mode switch is gone: an enabled tree audit
// always takes the single apply path, so a valid move_item operation must be
// applied to the live tree, published, and its full replay payload persisted.
func TestTreeAuditAppliesWhenEnabledAndPersistsRunDetails(t *testing.T) {
	service, analysisRepo, auditRepo, publisher, completer, payload := newTreeAuditRunnerFixture(t, false)
	execution, err := service.runTreeAudit(context.Background(), "session_26959b9519c5f880", "test", aiTaskTreeAudit, payload, 12, false)
	if err != nil {
		t.Fatalf("runTreeAudit() error = %v", err)
	}
	if execution.Result != "applied" || !execution.Applied || execution.Version != 13 {
		t.Fatalf("execution = %+v", execution)
	}
	if got := analysisRepo.version("session_26959b9519c5f880"); got != 13 {
		t.Fatalf("live version = %d, want 13", got)
	}
	if len(publisher.snapshot()) != 1 {
		t.Fatal("an enabled tree audit must publish the changed tree")
	}
	if run := auditRepo.latest(); run == nil || run.Result != "applied" || len(run.Findings) == 0 || len(run.Operations) == 0 {
		t.Fatalf("saved audit run = %+v", run)
	} else if len(run.InputPayload) == 0 || !json.Valid(run.InputPayload) || run.RawResponse == "" || !run.ProviderCalled {
		t.Fatalf("audit replay payload is incomplete: %+v", run)
	}
	if completer.callCount() != 1 {
		t.Fatalf("completer calls = %d, want 1", completer.callCount())
	}
}

func TestTreeAuditFakeProviderCleansDiscourseItemAndPreventsResurrection(t *testing.T) {
	const sessionID = "session_tree_audit_discourse_cleanup"
	state := liveAnalysisPayload{
		Summary: "VPN証明書対応", CurrentTopic: "VPN証明書の期限切れ対応", TreeVersion: 4,
		CoveredThroughSequenceNo: 3,
		Items: []liveAnalysisItem{
			{ID: "item-risk-vpn", Kind: "risk", Severity: "high", Title: "VPN証明書が来月末に期限切れ", Body: "リモート接続不能の可能性", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{2}},
			{ID: "item-todo-vpn", Kind: "todo", Severity: "high", Title: "VPN証明書の更新手順を確認", Body: "高橋さんが今週中に確認する", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{3}},
			{ClientKey: "legacy-discourse-alias", ID: "item-discourse", Kind: "fact", Severity: "medium", Title: "別の問題の存在を確認", Body: "アジェンダ外の別問題があるとの紹介", Status: "open", ClassificationStatus: classificationUnclassified, AssignmentConfidence: .4, EvidenceSequenceNos: []int64{1}},
		},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "障害振り返り", Origin: topicOriginSystem},
			{ID: "topic-impact", Kind: "topic", ParentID: treeRootNodeID, Label: "影響範囲", Origin: topicOriginDynamic},
			{ID: "topic-vpn", Kind: "topic", ParentID: treeRootNodeID, Label: "VPN証明書の期限切れ対応", Origin: topicOriginDynamic},
			{ID: "group-additional", Kind: "group", ParentID: treeRootNodeID, Label: "追加論点", Origin: "rule"},
			{ID: "item-risk-vpn", Kind: "risk", ParentID: "topic-vpn", Label: "VPN証明書が来月末に期限切れ", Status: "open"},
			{ID: "item-todo-vpn", Kind: "todo", ParentID: "topic-vpn", Label: "VPN証明書の更新手順を確認", Status: "open"},
			{ID: "item-discourse", Kind: "fact", ParentID: "group-additional", Label: "別の問題の存在を確認", Status: "open"},
		}},
	}
	rebuildTreeAuditEdges(state.Tree)
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	segments := []domain.TranscriptSegment{
		{SessionID: sessionID, SequenceNo: 1, Text: "ここで、アジェンダにはなかった別の問題があります。", IsFinal: true},
		{SessionID: sessionID, SequenceNo: 2, Text: "VPN証明書が来月末に期限切れで、リモート接続ができなくなる可能性があります。", IsFinal: true},
		{SessionID: sessionID, SequenceNo: 3, Text: "高橋さんに今週中に更新手順を確認してもらいます。", IsFinal: true},
	}
	analysisRepo := &internalAuditAnalysisRepository{store: map[string]domain.MeetingAIAnalysis{
		sessionID: {SessionID: sessionID, Type: domain.MeetingAIAnalysisLive, Status: domain.MeetingAIAnalysisCompleted, Version: 4, Payload: payload, SegmentCount: len(segments)},
	}}
	auditRepo := &internalAuditRepository{analysis: analysisRepo}
	response := `{
  "basedOnTreeVersion":4,
  "summary":"談話的な導入だけのitemを除去",
  "findings":[{"findingId":"finding-discourse","type":"discourse_only_item","severity":"high","nodeIds":["legacy-discourse-alias"],"currentParentIds":["group-additional"],"relatedNodeIds":[],"evidenceSequenceNos":[1],"reason":"独立命題を持たない話題転換","confidence":0.99}],
  "operations":[{"operationId":"deactivate-discourse","type":"deactivate_item","targetCanonicalItemId":"legacy-discourse-alias","targetCanonicalNodeId":"","targetCanonicalItemIds":[],"targetCandidateId":"","fromParentCanonicalNodeId":"","toParentCanonicalNodeId":"","label":"","reason":"discourse_only_item","confidence":0.99,"evidenceSequenceNos":[1],"dependsOnOperationIds":[]}]
}`
	completer := &internalAuditCompleter{content: response}
	service := NewMeetingAnalysisService(analysisRepo, internalAuditTranscriptRepository{segments: segments}, nil, completer, MeetingAnalysisConfig{
		Enabled: true, LiveEnabled: true, Model: "shared", TaskModels: AITaskModels{TreeAudit: "tree-audit-mini"},
		TreeAudit: TreeAuditConfig{Enabled: true, MinInterval: time.Millisecond, Timeout: time.Second, UnappliedWarningThreshold: 2},
	})
	service.SetMeetingTreeAuditRepository(auditRepo)
	execution, err := service.runTreeAudit(context.Background(), sessionID, "manual_replay", aiTaskTreeAudit, payload, 4, false)
	if err != nil {
		t.Fatalf("runTreeAudit() error = %v", err)
	}
	if !execution.Applied || execution.Version != 5 {
		t.Fatalf("execution = %+v", execution)
	}
	audited := previousLiveAnalysisState(execution.Payload)
	if treeNodeByID(audited.Tree, "item-discourse") != nil || treeNodeByID(audited.Tree, "group-additional") != nil {
		t.Fatalf("discourse cleanup did not remove item/group: %+v", audited.Tree.Nodes)
	}
	if treeNodeByID(audited.Tree, "topic-vpn") == nil || treeNodeByID(audited.Tree, "item-risk-vpn") == nil || treeNodeByID(audited.Tree, "item-todo-vpn") == nil || treeNodeByID(audited.Tree, "topic-impact") == nil {
		t.Fatalf("valid VPN/fixed nodes were removed: %+v", audited.Tree.Nodes)
	}
	if len(audited.ItemTombstones) == 0 || audited.ItemTombstones[0].Reason != "discourse_only" {
		t.Fatalf("tombstones = %+v", audited.ItemTombstones)
	}
	run := auditRepo.latest()
	if run == nil || run.ResultClassification != domain.MeetingTreeAuditResultApplied || run.OperationsProposed != 1 || run.OperationsCanonicalized != 1 || run.OperationsValid != 1 || run.OperationsApplied != 1 || run.ResultingTreeVersion != 5 {
		t.Fatalf("classified audit run = %+v", run)
	}

	resurrection := `{
  "summary":"VPN証明書対応","currentTopic":"VPN証明書の期限切れ対応","resolvedIds":[],"resolutionUpdates":[],
  "utteranceRoles":[{"sequenceNo":1,"role":"discourse_transition"}],
  "items":[{"clientKey":"same-discourse-item","kind":"fact","severity":"medium","title":"別の問題の存在を確認","body":"アジェンダ外の別問題があるとの紹介","status":"open","evidenceSequenceNos":[1]}],
  "newTopics":[{"id":"topic-additional","label":"追加論点","description":"別件"}],
  "assignments":[{"nodeId":"same-discourse-item","parentTopicId":"topic-additional","confidence":0.8,"reason":"別件"}]
}`
	scope := evidenceScopeFromTexts(map[int64]string{1: segments[0].Text, 2: segments[1].Text, 3: segments[2].Text}, 1, 2, 3)
	stats := &liveAnalysisTreeMergeStats{}
	nextPayload, err := parseAndMergeLiveAnalysisPayloadWithEvidence(resurrection, execution.Payload, nil, 6, []int64{1}, scope, TreeClassificationConfig{}, stats)
	if err != nil {
		t.Fatalf("merge resurrection round: %v", err)
	}
	next := previousLiveAnalysisState(nextPayload)
	if treeNodeByID(next.Tree, "item-discourse") != nil {
		t.Fatalf("deactivated item returned to the active tree: items=%+v nodes=%+v", next.Items, next.Tree.Nodes)
	}
	retained := itemByID(next.Items, "item-discourse")
	if retained == nil || !retained.Inactive {
		t.Fatalf("deactivated item audit history was not retained as inactive: items=%+v", next.Items)
	}
	for _, node := range next.Tree.Nodes {
		if node.Label == "追加論点" {
			t.Fatalf("empty cleanup container resurrected: %+v", node)
		}
	}
	for _, candidate := range next.EmergingTopics {
		if candidate.Label == "追加論点" {
			t.Fatalf("cleanup candidate resurrected: %+v", candidate)
		}
	}
	if stats.ItemResurrectionPrevented != 1 {
		t.Fatalf("tombstone did not block the next live-version resurrection: %+v", stats)
	}
}

// partialSuccessAuditResponse (design brief D5/14.6) returns a v4 response
// with two structurally valid operations (a known-good move_item and a
// known-good deactivate_candidate) plus one operation whose target item ID
// does not exist in the tree at all. The bogus ID never resolves during
// canonicalization, so it is dropped from response.Operations and recorded
// as a "operation"-scoped ParseRejection instead of reaching the per-
// operation validator - runTreeAudit folds that rejection back into the
// same validator.Evaluations/OperationsRejected accounting (with a
// "parser_"-prefixed reason), so the run-level result is still exactly the
// 2-applied/1-rejected partial_success this test asserts.
func partialSuccessAuditResponse() string {
	response := treeAuditResponse{
		BasedOnTreeVersion: 12,
		Summary:            "partial success replay: two safe operations plus one unresolvable target",
		Findings:           []treeAuditFinding{},
		Operations: []treeAuditOperation{
			{
				OperationID: "op-valid-move", Type: TreeAuditMoveItem,
				TargetCanonicalItemID: "item-risk-rare-plants", FromParentCanonicalNodeID: "candidate-info-public",
				ToParentCanonicalNodeID: "candidate-plant-study", Confidence: 0.97,
				Reason: "湿地・希少植物subjectへ戻す", EvidenceSequenceNos: []int64{22},
			},
			{
				OperationID: "op-valid-deactivate", Type: TreeAuditDeactivateCandidate,
				TargetCandidateID: "candidate-plant-video", Confidence: 0.97,
				Reason: "単発candidateのため非活性化",
			},
			{
				OperationID: "op-bogus-target", Type: TreeAuditMoveItem,
				TargetCanonicalItemID: "item-does-not-exist", FromParentCanonicalNodeID: "candidate-info-public",
				ToParentCanonicalNodeID: "candidate-plant-study", Confidence: 0.99,
				Reason: "存在しないitemへの操作", EvidenceSequenceNos: []int64{22},
			},
		},
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// TestTreeAuditPartialSuccessAppliesValidOperationsAndRejectsInvalidTarget
// covers design brief D5/14.6: exactly one unresolvable operation among
// otherwise-safe ones must not block the safe operations from applying. The
// run must still reach result="partial_success", advance the live tree
// version, persist the rejection reason, and keep tree integrity valid.
func TestTreeAuditPartialSuccessAppliesValidOperationsAndRejectsInvalidTarget(t *testing.T) {
	service, analysisRepo, auditRepo, publisher, completer, payload := newTreeAuditRunnerFixture(t, false)
	completer.content = partialSuccessAuditResponse()
	execution, err := service.runTreeAudit(context.Background(), "session_26959b9519c5f880", "test", aiTaskTreeAudit, payload, 12, false)
	if err != nil {
		t.Fatalf("runTreeAudit() error = %v", err)
	}
	if execution.Result != "partial_success" || !execution.Applied || execution.Version != 13 {
		t.Fatalf("execution = %+v", execution)
	}
	if got := analysisRepo.version("session_26959b9519c5f880"); got != 13 {
		t.Fatalf("live version = %d, want 13", got)
	}
	if len(publisher.snapshot()) != 1 {
		t.Fatal("a partial_success tree audit must still publish the changed tree")
	}
	run := auditRepo.latest()
	if run == nil || run.Result != "partial_success" || run.Disposition != "applied" || run.ResultingTreeVersion != 13 {
		t.Fatalf("saved audit run = %+v", run)
	}
	var validator treeAuditValidatorResult
	if err := json.Unmarshal(run.ValidatorResult, &validator); err != nil {
		t.Fatalf("unmarshal validator result: %v", err)
	}
	if validator.OperationsProposed != 3 || validator.OperationsApplied != 2 || validator.OperationsRejected != 1 || !validator.TreeIntegrityValid {
		t.Fatalf("validator result = %+v", validator)
	}
	foundBogus := false
	for _, evaluation := range validator.Evaluations {
		if evaluation.OperationID != "op-bogus-target" {
			if !evaluation.Valid || !evaluation.Applied {
				t.Fatalf("expected operation %s to be applied: %+v", evaluation.OperationID, evaluation)
			}
			continue
		}
		foundBogus = true
		if evaluation.Valid || evaluation.Reason == "" {
			t.Fatalf("expected op-bogus-target rejected with a reason: %+v", evaluation)
		}
	}
	if !foundBogus {
		t.Fatalf("op-bogus-target rejection was not persisted in validator evaluations: %+v", validator.Evaluations)
	}
	if completer.callCount() != 1 {
		t.Fatalf("completer calls = %d, want 1", completer.callCount())
	}
}

func TestTreeAuditDoesNotBlockLiveExtraction(t *testing.T) {
	service, _, _, _, completer, payload := newTreeAuditRunnerFixture(t, false)
	service.config.LiveMinChars = 1
	completer.block = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.scheduleTreeAudit(ctx, "session_26959b9519c5f880", "semantic_anomaly", payload, 12)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 1 })

	service.mu.Lock()
	state := service.sessionStateLocked("session_26959b9519c5f880")
	state.pending = []domain.TranscriptSegment{{SessionID: "session_26959b9519c5f880", SequenceNo: 30, Text: "ライブ抽出は継続する", IsFinal: true}}
	state.pendingChars = 20
	service.mu.Unlock()
	service.tick(ctx)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 2 })
	service.mu.Lock()
	running, auditRunning := state.running, state.auditRunning
	service.mu.Unlock()
	if !running || !auditRunning {
		t.Fatalf("audit concurrency liveRunning=%t auditRunning=%t", running, auditRunning)
	}
}

func TestTreeAuditTimeoutDoesNotBlockLiveExtraction(t *testing.T) {
	service, _, _, _, completer, payload := newTreeAuditRunnerFixture(t, false)
	service.config.LiveMinChars = 1
	service.config.TreeAudit.Timeout = 20 * time.Millisecond
	completer.block = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.scheduleTreeAudit(ctx, "session_26959b9519c5f880", "semantic_anomaly", payload, 12)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 1 })
	service.mu.Lock()
	state := service.sessionStateLocked("session_26959b9519c5f880")
	state.pending = []domain.TranscriptSegment{{SessionID: "session_26959b9519c5f880", SequenceNo: 30, Text: "timeout中のライブ抽出", IsFinal: true}}
	state.pendingChars = 20
	service.mu.Unlock()
	service.tick(ctx)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 2 })
	waitForInternalAudit(t, time.Second, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return !state.auditRunning && state.running
	})
}

func TestTreeAuditPanicReleasesSingleFlight(t *testing.T) {
	service, _, _, _, completer, payload := newTreeAuditRunnerFixture(t, false)
	completer.panicOnCall = true
	service.scheduleTreeAudit(context.Background(), "session_26959b9519c5f880", "test", payload, 12)
	waitForInternalAudit(t, time.Second, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return completer.callCount() == 1 && !service.sessionStateLocked("session_26959b9519c5f880").auditRunning
	})
}

func TestAuditRepositoryFailureDoesNotBlockLiveExtraction(t *testing.T) {
	service, _, auditRepo, _, completer, payload := newTreeAuditRunnerFixture(t, false)
	service.config.LiveMinChars = 1
	auditRepo.tryStartErr = errors.New(`relation "meeting_tree_audit_runs" does not exist`)
	service.scheduleTreeAudit(context.Background(), "session_26959b9519c5f880", "test", payload, 12)
	waitForInternalAudit(t, time.Second, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return !service.sessionStateLocked("session_26959b9519c5f880").auditRunning
	})
	completer.block = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.mu.Lock()
	state := service.sessionStateLocked("session_26959b9519c5f880")
	state.pending = []domain.TranscriptSegment{{SessionID: "session_26959b9519c5f880", SequenceNo: 30, Text: "repository障害後も分析", IsFinal: true}}
	state.pendingChars = 20
	service.mu.Unlock()
	service.tick(ctx)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 1 })
}

func TestTreeAuditRatePolicyPreservesLateMeetingAndHighSeverityCapacity(t *testing.T) {
	service, _, auditRepo, _, _, _ := newTreeAuditRunnerFixture(t, false)
	service.config.TreeAudit.MinInterval = 5 * time.Minute
	service.config.TreeAudit.MaxRunsPerHour = 12
	service.config.TreeAudit.MaxRunsPerSession = 20
	base := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)
	lastAudit := time.Time{}
	for elapsed := time.Duration(0); elapsed < time.Hour; elapsed += 10 * time.Second {
		now := base.Add(elapsed)
		if !lastAudit.IsZero() && now.Sub(lastAudit) < service.config.TreeAudit.MinInterval {
			continue
		}
		reason, err := service.treeAuditSuppressionReason(context.Background(), "session_26959b9519c5f880", domain.MeetingTreeAuditTriggerNormal, false, now)
		if err != nil {
			t.Fatal(err)
		}
		if reason != "" {
			continue
		}
		auditRepo.runs = append(auditRepo.runs, domain.MeetingTreeAuditRun{
			ID: domain.NewID("rate"), SessionID: "session_26959b9519c5f880", Task: string(aiTaskTreeAudit),
			TriggerClass: domain.MeetingTreeAuditTriggerNormal, ProviderCalled: true, CreatedAt: now,
		})
		lastAudit = now
	}
	if len(auditRepo.runs) != 12 || lastAudit.Before(base.Add(55*time.Minute)) {
		t.Fatalf("60-minute schedule calls=%d last=%s", len(auditRepo.runs), lastAudit.Sub(base))
	}

	for len(auditRepo.runs) < 20 {
		auditRepo.runs = append(auditRepo.runs, domain.MeetingTreeAuditRun{
			ID: domain.NewID("normal"), SessionID: "session_26959b9519c5f880", Task: string(aiTaskTreeAudit),
			TriggerClass: domain.MeetingTreeAuditTriggerNormal, ProviderCalled: true, CreatedAt: base.Add(-2 * time.Hour),
		})
	}
	normalReason, err := service.treeAuditSuppressionReason(context.Background(), "session_26959b9519c5f880", domain.MeetingTreeAuditTriggerNormal, false, base.Add(2*time.Hour))
	if err != nil || normalReason != "normal_session_limit" {
		t.Fatalf("normal suppression=%q err=%v", normalReason, err)
	}
	highReason, err := service.treeAuditSuppressionReason(context.Background(), "session_26959b9519c5f880", domain.MeetingTreeAuditTriggerHigh, false, base.Add(2*time.Hour))
	if err != nil || highReason != "" {
		t.Fatalf("high severity was suppressed by normal cap: %q err=%v", highReason, err)
	}
	for index := 0; index < service.config.TreeAudit.HighSeverityMaxRunsPerHour; index++ {
		auditRepo.runs = append(auditRepo.runs, domain.MeetingTreeAuditRun{
			ID: domain.NewID("high"), SessionID: "session_26959b9519c5f880", Task: string(aiTaskTreeAudit),
			TriggerClass: domain.MeetingTreeAuditTriggerHigh, ProviderCalled: true, CreatedAt: base.Add(2*time.Hour + time.Duration(index)*time.Minute),
		})
	}
	highReason, err = service.treeAuditSuppressionReason(context.Background(), "session_26959b9519c5f880", domain.MeetingTreeAuditTriggerHigh, false, base.Add(2*time.Hour+10*time.Minute))
	if err != nil || highReason != "high_severity_hourly_limit" {
		t.Fatalf("high severity suppression=%q err=%v", highReason, err)
	}
}

// TestTreeAuditRejectsEveryUnsupportedOperationType locks the Phase C
// unsupported set (the 8 operation types with no applier at all). Every one
// of them must be rejected with reason "unsupported_operation" and
// category "unsupported", regardless of model confidence.
func TestTreeAuditRejectsEveryUnsupportedOperationType(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	types := []TreeAuditOperationType{
		TreeAuditMergeCandidates, TreeAuditPromoteCandidate, TreeAuditMarkCandidateTentative,
		TreeAuditMergeDynamicTopics, TreeAuditCreateGroup, TreeAuditMoveItemsToGroup,
		TreeAuditSplitCandidate, TreeAuditMergeFragmentedUtterances,
	}
	operations := make([]treeAuditOperation, 0, len(types))
	for index, operationType := range types {
		operations = append(operations, treeAuditOperation{OperationID: fmt.Sprintf("op-%d", index), Type: operationType, Confidence: 1})
	}
	_, validator := validateAndDryRunTreeAuditOperations(state, operations, segments, mc, roles, TreeAuditConfig{}, "audit-unsupported", 13, true)
	if validator.OperationsValid != 0 || validator.OperationsRejected != len(types) {
		t.Fatalf("unsupported operation validator = %+v", validator)
	}
	for _, evaluation := range validator.Evaluations {
		if evaluation.Reason != "unsupported_operation" || evaluation.Category != "unsupported" {
			t.Fatalf("operation %s reason=%q category=%q", evaluation.Type, evaluation.Reason, evaluation.Category)
		}
	}
}

// TestTreeAuditOperationSupportedMatchesApplicableSet locks the Phase C
// applicable set to the 18 implemented appliers; the remaining 8 operation
// types must stay unsupported until a dedicated applier and safety tests
// exist for them.
func TestTreeAuditOperationSupportedMatchesApplicableSet(t *testing.T) {
	supported := []TreeAuditOperationType{
		TreeAuditMoveItem, TreeAuditRestorePreviousParent, TreeAuditMoveNode, TreeAuditMergeItems,
		TreeAuditRewriteItem, TreeAuditRewriteItemTitle, TreeAuditRewriteItemDescription,
		TreeAuditReclassifyKind, TreeAuditReclassifySubtype,
		TreeAuditDeactivateItem, TreeAuditAssignItemToCandidate, TreeAuditChangeEvidenceRole,
		TreeAuditCreateTopicFromCandidate, TreeAuditFoldCandidateIntoTopic,
		TreeAuditDeactivateCandidate, TreeAuditRenameGroup, TreeAuditRenameTopic, TreeAuditRemoveEmptyGroup,
	}
	for _, operationType := range supported {
		if !treeAuditOperationSupported(operationType) {
			t.Fatalf("%s must be supported", operationType)
		}
		if treeAuditOperationClassification(operationType) != treeAuditOperationApplicable {
			t.Fatalf("%s classification = %q, want applicable", operationType, treeAuditOperationClassification(operationType))
		}
	}
	unsupported := []TreeAuditOperationType{
		TreeAuditMergeCandidates, TreeAuditPromoteCandidate, TreeAuditMarkCandidateTentative,
		TreeAuditMergeDynamicTopics, TreeAuditCreateGroup, TreeAuditMoveItemsToGroup,
		TreeAuditSplitCandidate, TreeAuditMergeFragmentedUtterances,
	}
	for _, operationType := range unsupported {
		if treeAuditOperationSupported(operationType) {
			t.Fatalf("%s must remain unsupported", operationType)
		}
		if treeAuditOperationClassification(operationType) != treeAuditOperationUnsupported {
			t.Fatalf("%s classification = %q, want unsupported", operationType, treeAuditOperationClassification(operationType))
		}
	}
	if len(supported)+len(unsupported) != 26 {
		t.Fatalf("applicable+unsupported = %d, want 26 (every TreeAuditOperationType)", len(supported)+len(unsupported))
	}
}

func TestTreeAuditV8FindingTypesAreAccepted(t *testing.T) {
	for _, findingType := range []TreeAuditFindingType{
		TreeAuditGenericTopicLabel, TreeAuditGenericCandidateLabel,
		TreeAuditTopicLabelNotDerivedFromChildren, TreeAuditSingleChildGenericTopic,
		TreeAuditRiskTodoSubjectFragmentation, TreeAuditRelatedActionOutsideRiskTopic,
		TreeAuditLeadingParticleFragment, TreeAuditAnaphoraTargetMissing,
		TreeAuditIncompleteSTTSegmentItem, TreeAuditDecisionMissingObject,
		TreeAuditNoAgendaFalsePositiveFromModifier,
	} {
		if !validTreeAuditFindingType(findingType) {
			t.Fatalf("finding type %q is not registered", findingType)
		}
	}
}

func TestTreeAuditRenameTopicPreservesIdentityReferencesAndChildren(t *testing.T) {
	state := liveAnalysisPayload{TreeVersion: 7, Items: []liveAnalysisItem{
		{ID: "risk-vpn", Kind: "risk", Title: "VPN証明書が来月末に期限切れとなるリスク", Body: "リモート接続不能の可能性", Status: "open", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{23}},
		{ID: "todo-vpn", Kind: "todo", Title: "VPN証明書の更新手順と作業可能日を確認", Body: "小林さんが今週中に確認する", Status: "open", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{25, 26}},
	}}
	state.Tree = &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "会議", Origin: topicOriginSystem},
		{ID: "topic-vpn", Kind: "topic", ParentID: treeRootNodeID, Label: "追加論点", Origin: topicOriginMixed, AgendaRefs: []string{"agenda-vpn"}, Materialized: true},
		{ID: "risk-vpn", Kind: "risk", ParentID: "topic-vpn", Label: state.Items[0].Title},
		{ID: "todo-vpn", Kind: "todo", ParentID: "topic-vpn", Label: state.Items[1].Title},
	}}
	rebuildTreeAuditEdges(state.Tree)
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-vpn", Title: "追加論点", Order: 1, Role: agendaRolePrimary}}}
	operation := treeAuditOperation{OperationID: "rename-vpn", Type: TreeAuditRenameTopic, TargetCanonicalNodeID: "topic-vpn", Label: "VPN証明書の更新対応", Confidence: 1, Reason: "child subject"}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, nil, mc, nil, TreeAuditConfig{}, "audit-rename-topic", 8, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 || !result.TreeIntegrityValid {
		t.Fatalf("rename result=%+v", result)
	}
	topic := treeNodeByID(dry.Tree, "topic-vpn")
	if topic == nil || topic.Label != "VPN証明書の更新対応" || topic.ParentID != treeRootNodeID || len(topic.AgendaRefs) != 1 || topic.AgendaRefs[0] != "agenda-vpn" {
		t.Fatalf("renamed topic=%+v", topic)
	}
	for _, childID := range []string{"risk-vpn", "todo-vpn"} {
		if child := treeNodeByID(dry.Tree, childID); child == nil || child.ParentID != "topic-vpn" {
			t.Fatalf("child %s=%+v", childID, child)
		}
	}
	if dry.TreeChanges == nil || !containsExactString(dry.TreeChanges.UpdatedNodeIDs, "topic-vpn") {
		t.Fatalf("treeChanges=%+v", dry.TreeChanges)
	}

	manual := state
	manual.Tree = &liveAnalysisTree{Nodes: append([]liveAnalysisTreeNode(nil), state.Tree.Nodes...), Edges: append([]liveAnalysisTreeEdge(nil), state.Tree.Edges...)}
	for i := range manual.Tree.Nodes {
		if manual.Tree.Nodes[i].ID == "topic-vpn" {
			manual.Tree.Nodes[i].LastParentChangeSource = "manual"
		}
	}
	_, rejected := validateAndDryRunTreeAuditOperations(manual, []treeAuditOperation{operation}, nil, mc, nil, TreeAuditConfig{}, "audit-rename-topic-manual", 8, true)
	if rejected.OperationsValid != 0 || len(rejected.Evaluations) != 1 || rejected.Evaluations[0].Reason != "manual_edit_protected" {
		t.Fatalf("manual rename rejection=%+v", rejected)
	}
}

func TestTreeAuditPrecheckDetectsGenericFragmentedAndIncompleteSubjects(t *testing.T) {
	state := liveAnalysisPayload{TreeVersion: 4, Items: []liveAnalysisItem{
		{ID: "risk-vpn", Kind: "risk", Title: "VPN証明書期限切れでリモート接続不能になる", Body: "VPN証明書が来月末に期限切れ", Status: "open", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{23}},
		{ID: "todo-vpn", Kind: "todo", Title: "VPN証明書の更新手順と作業日を確認する", Body: "小林さんが今週中に確認する", Status: "open", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{25, 26}},
		{ID: "decision-fragment", Kind: "decision", Title: "の運用を次回から適用する", Body: "の運用を次回から適用することにします", Status: "open", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{16}},
		{ID: "decision-anaphora", Kind: "decision", Title: "その対応を実施する", Body: "その対応を実施することにします", Status: "open", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{17}},
	}, EmergingTopics: []emergingTopicCandidate{{ID: "candidate-generic", Label: "別件", EvidenceItemIDs: []string{"todo-vpn"}, RoundCount: 1}}}
	state.Tree = &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "会議", Origin: topicOriginSystem},
		{ID: "topic-vpn", Kind: "topic", ParentID: treeRootNodeID, Label: "追加論点", Origin: topicOriginDynamic},
		{ID: "topic-actions", Kind: "topic", ParentID: treeRootNodeID, Label: "更新作業", Origin: topicOriginDynamic},
		{ID: "risk-vpn", Kind: "risk", ParentID: "topic-vpn", Label: state.Items[0].Title},
		{ID: "todo-vpn", Kind: "todo", ParentID: "topic-actions", Label: state.Items[1].Title},
		{ID: "decision-fragment", Kind: "decision", ParentID: "topic-vpn", Label: state.Items[2].Title},
		{ID: "decision-anaphora", Kind: "decision", ParentID: "topic-vpn", Label: state.Items[3].Title},
	}}
	rebuildTreeAuditEdges(state.Tree)
	findings := deterministicTreeAuditPrecheck(state, nil, map[int64]treeAuditEvidenceRole{16: treeAuditEvidencePrimary, 17: treeAuditEvidencePrimary, 23: treeAuditEvidencePrimary, 25: treeAuditEvidencePrimary, 26: treeAuditEvidencePrimary}, TreeAuditConfig{})
	for _, expected := range []struct {
		typeName TreeAuditFindingType
		nodeID   string
	}{
		{TreeAuditGenericTopicLabel, "topic-vpn"},
		{TreeAuditTopicLabelNotDerivedFromChildren, "topic-vpn"},
		{TreeAuditGenericCandidateLabel, "todo-vpn"},
		{TreeAuditRiskTodoSubjectFragmentation, "risk-vpn"},
		{TreeAuditRelatedActionOutsideRiskTopic, "todo-vpn"},
		{TreeAuditLeadingParticleFragment, "decision-fragment"},
		{TreeAuditIncompleteSTTSegmentItem, "decision-fragment"},
		{TreeAuditDecisionMissingObject, "decision-fragment"},
		{TreeAuditAnaphoraTargetMissing, "decision-anaphora"},
	} {
		assertAuditFindingForNode(t, findings, expected.typeName, expected.nodeID)
	}
}

func TestTreeAuditPrecheckDetectsNoAgendaFalsePositiveFromModifier(t *testing.T) {
	item := liveAnalysisItem{ID: "todo-double-check", Kind: "todo", Title: "機器交換時に別の担当者が設定をダブルチェックする", Body: "交換前後のVLAN疎通確認も行う", Status: "open", ClassificationStatus: classificationAssigned, AssignmentSource: assignmentSourceNoAgendaSpan, EvidenceSequenceNos: []int64{13, 14}}
	state := liveAnalysisPayload{TreeVersion: 3, Items: []liveAnalysisItem{item}, Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic", Label: "ネットワーク障害の再発防止", Origin: topicOriginSystem},
		{ID: "topic-prevention", Kind: "topic", ParentID: treeRootNodeID, Label: "機器交換時のダブルチェックとVLAN疎通確認", Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-3"}, Materialized: true},
		{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: treeUnclassifiedTopicLabel, Origin: topicOriginSystem},
		{ID: item.ID, Kind: item.Kind, ParentID: treeUnclassifiedTopicID, Label: item.Title},
	}}}
	rebuildTreeAuditEdges(state.Tree)
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-3", Title: "機器交換時のダブルチェックとVLAN疎通確認", Order: 3, Role: agendaRolePrimary}}}
	findings := deterministicTreeAuditPrecheck(state, mc, map[int64]treeAuditEvidenceRole{13: treeAuditEvidencePrimary, 14: treeAuditEvidencePrimary}, TreeAuditConfig{})
	assertAuditFindingForNode(t, findings, TreeAuditNoAgendaFalsePositiveFromModifier, item.ID)
}

// TestTreeAuditSnapshotUsesV3FieldNamesAndMarksPromotedCandidates covers
// design brief 14.2(a): the v3 snapshot renames node/candidate ID fields and
// adds agendaIds/validParentCanonicalNodeIds plus a promotedNodeId marker so
// the model can tell a live candidate apart from one that already has a
// tree node under the same ID.
func TestTreeAuditSnapshotUsesV3FieldNamesAndMarksPromotedCandidates(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	// A candidate that has already been promoted to a dynamic topic node
	// keeps its own ID as the node ID. Promotion normally removes the
	// candidate from EmergingTopics tracking; this fixture keeps it listed
	// defensively to exercise the promotedNodeId marker.
	state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
		ID: "candidate-promoted-marker", Kind: "topic", ParentID: treeRootNodeID,
		Label: "昇格済み動的topic", Origin: topicOriginDynamic,
	})
	state.EmergingTopics = append(state.EmergingTopics, emergingTopicCandidate{
		ID: "candidate-promoted-marker", Label: "昇格済み動的topic",
	})
	rebuildTreeAuditEdges(state.Tree)
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	build, err := buildTreeAuditSnapshot("session_26959b9519c5f880", encoded, segments, mc, TreeAuditConfig{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := build.Snapshot
	var risk *treeAuditSnapshotNode
	for index := range snapshot.Nodes {
		if snapshot.Nodes[index].CanonicalNodeID == "item-risk-rare-plants" {
			risk = &snapshot.Nodes[index]
		}
	}
	if risk == nil || risk.NodeType != "risk" || risk.ParentCanonicalNodeID != "candidate-info-public" {
		t.Fatalf("risk node = %+v", risk)
	}
	var promoted, unpromoted *treeAuditSnapshotCandidate
	for index := range snapshot.Candidates {
		switch snapshot.Candidates[index].ID {
		case "candidate-promoted-marker":
			promoted = &snapshot.Candidates[index]
		case "candidate-plant-video":
			unpromoted = &snapshot.Candidates[index]
		}
	}
	if promoted == nil || promoted.PromotedNodeID != "candidate-promoted-marker" {
		t.Fatalf("promoted candidate = %+v", promoted)
	}
	if unpromoted == nil || unpromoted.PromotedNodeID != "" {
		t.Fatalf("unpromoted candidate = %+v", unpromoted)
	}
	if !containsExactString(snapshot.AgendaIDs, "agenda-1") || !containsExactString(snapshot.AgendaIDs, "agenda-2") || !containsExactString(snapshot.AgendaIDs, "agenda-3") {
		t.Fatalf("agendaIds = %v", snapshot.AgendaIDs)
	}
	if containsExactString(snapshot.AgendaIDs, treeRootNodeID) {
		t.Fatalf("agendaIds unexpectedly include root: %v", snapshot.AgendaIDs)
	}
	if !containsExactString(snapshot.ValidParentCanonicalNodeIDs, "candidate-plant-study") || !containsExactString(snapshot.ValidParentCanonicalNodeIDs, "candidate-promoted-marker") {
		t.Fatalf("validParentCanonicalNodeIds = %v", snapshot.ValidParentCanonicalNodeIDs)
	}
	// root is a valid move_node destination (Phase C), so it belongs in this
	// advisory list even though move_item's own applier independently
	// rejects root as a destination.
	if !containsExactString(snapshot.ValidParentCanonicalNodeIDs, treeRootNodeID) {
		t.Fatalf("validParentCanonicalNodeIds must include root for move_node: %v", snapshot.ValidParentCanonicalNodeIDs)
	}
	if containsExactString(snapshot.ValidParentCanonicalNodeIDs, "item-risk-rare-plants") {
		t.Fatalf("validParentCanonicalNodeIds unexpectedly include a detail item: %v", snapshot.ValidParentCanonicalNodeIDs)
	}
	raw := string(build.InputJSON)
	for _, key := range []string{`"canonicalNodeId"`, `"nodeType"`, `"parentCanonicalNodeId"`, `"candidateId"`, `"promotedNodeId"`, `"agendaIds"`, `"validParentCanonicalNodeIds"`} {
		if !strings.Contains(raw, key) {
			t.Fatalf("snapshot JSON missing v3 key %s: %s", key, raw)
		}
	}
}

// TestTreeAuditCanonicalizeResolvesPromotedCandidateToParent covers 14.2(b):
// an already-promoted candidate ID used as a move_item toParent must
// canonicalize to the (identically-IDed) tree node and apply normally.
func TestTreeAuditCanonicalizeResolvesPromotedCandidateToParent(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
		ID: "candidate-plant-promoted", Kind: "topic", ParentID: treeRootNodeID,
		Label: "植物調査", Description: "湿地・希少植物の生態系調査", Origin: topicOriginDynamic,
	})
	rebuildTreeAuditEdges(state.Tree)
	roles := classifyTreeAuditEvidence(state, segments)
	response := &treeAuditResponse{Operations: []treeAuditOperation{
		{OperationID: "op-canon-node", Type: TreeAuditMoveItem, TargetCanonicalItemID: "item-risk-rare-plants",
			FromParentCanonicalNodeID: "candidate-info-public", ToParentCanonicalNodeID: "candidate-plant-promoted",
			Confidence: 0.97, EvidenceSequenceNos: []int64{22}},
	}}
	canonicalizeTreeAuditResponse(response, state)
	if len(response.ParseRejections) != 0 || len(response.Operations) != 1 {
		t.Fatalf("canonicalized response = %+v", response)
	}
	if got := response.Operations[0].ToParentCanonicalNodeID; got != "candidate-plant-promoted" {
		t.Fatalf("toParentCanonicalNodeId = %q", got)
	}
	_, validator := validateAndDryRunTreeAuditOperations(state, response.Operations, segments, mc, roles, TreeAuditConfig{}, "audit-b", 13, true)
	if validator.OperationsValid != 1 || validator.OperationsApplied != 1 {
		t.Fatalf("validator = %+v", validator)
	}
}

// TestTreeAuditCanonicalizeResolvesClientKeyAliasToItemID covers 14.2(c): a
// model round-local ClientKey alias must resolve to the item's real ID when
// it uniquely identifies one item.
func TestTreeAuditCanonicalizeResolvesClientKeyAliasToItemID(t *testing.T) {
	payload, _, _ := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	for index := range state.Items {
		if state.Items[index].ID == "item-risk-rare-plants" {
			state.Items[index].ClientKey = "model-ref-1"
		}
	}
	response := &treeAuditResponse{Operations: []treeAuditOperation{
		{OperationID: "op-alias", Type: TreeAuditMoveItem, TargetCanonicalItemID: "model-ref-1",
			FromParentCanonicalNodeID: "candidate-info-public", ToParentCanonicalNodeID: "candidate-plant-study",
			Confidence: 0.97, EvidenceSequenceNos: []int64{22}},
	}}
	canonicalizeTreeAuditResponse(response, state)
	if len(response.ParseRejections) != 0 || len(response.Operations) != 1 {
		t.Fatalf("canonicalized response = %+v", response)
	}
	if got := response.Operations[0].TargetCanonicalItemID; got != "item-risk-rare-plants" {
		t.Fatalf("targetCanonicalItemId = %q, want item-risk-rare-plants", got)
	}
	if response.CanonicalizationCount != 1 {
		t.Fatalf("canonicalizationCount = %d, want 1", response.CanonicalizationCount)
	}
}

// TestTreeAuditCanonicalizeRejectsAmbiguousClientKeyAlias covers 14.2(d): a
// ClientKey shared by two items cannot be resolved and must reject only the
// operation that used it.
func TestTreeAuditCanonicalizeRejectsAmbiguousClientKeyAlias(t *testing.T) {
	payload, _, _ := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	for index := range state.Items {
		if state.Items[index].ID == "item-risk-rare-plants" || state.Items[index].ID == "item-todo-plant-survey" {
			state.Items[index].ClientKey = "model-ref-dup"
		}
	}
	response := &treeAuditResponse{Operations: []treeAuditOperation{
		{OperationID: "ambiguous-move", Type: TreeAuditMoveItem, TargetCanonicalItemID: "model-ref-dup",
			FromParentCanonicalNodeID: "candidate-info-public", ToParentCanonicalNodeID: "candidate-plant-study", Confidence: 0.97},
		{OperationID: "unrelated-rename", Type: TreeAuditRenameGroup, TargetCanonicalNodeID: "candidate-plant-study", Label: "植物調査班", Confidence: 1},
	}}
	canonicalizeTreeAuditResponse(response, state)
	if len(response.Operations) != 1 || response.Operations[0].OperationID != "unrelated-rename" {
		t.Fatalf("operations = %+v", response.Operations)
	}
	if len(response.ParseRejections) != 1 || response.ParseRejections[0].ElementID != "ambiguous-move" || response.ParseRejections[0].Reason != "ambiguous_alias" {
		t.Fatalf("rejections = %+v", response.ParseRejections)
	}
}

// TestTreeAuditCanonicalizeRejectsUnresolvedUnpromotedCandidateTarget covers
// 14.2(e): a move_item toParent pointing at a still-unpromoted candidate
// cannot resolve to a node (design rule 4) and must be rejected without
// discarding other valid operations in the same response.
func TestTreeAuditCanonicalizeRejectsUnresolvedUnpromotedCandidateTarget(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	response := &treeAuditResponse{Operations: []treeAuditOperation{
		{OperationID: "valid-move", Type: TreeAuditMoveItem, TargetCanonicalItemID: "item-risk-rare-plants",
			FromParentCanonicalNodeID: "candidate-info-public", ToParentCanonicalNodeID: "candidate-plant-study",
			Confidence: 0.97, EvidenceSequenceNos: []int64{22}},
		{OperationID: "invalid-move", Type: TreeAuditMoveItem, TargetCanonicalItemID: "item-todo-plant-survey",
			FromParentCanonicalNodeID: "candidate-plant-study", ToParentCanonicalNodeID: "candidate-plant-video",
			Confidence: 0.97, EvidenceSequenceNos: []int64{23}},
	}}
	canonicalizeTreeAuditResponse(response, state)
	if len(response.Operations) != 1 || response.Operations[0].OperationID != "valid-move" {
		t.Fatalf("operations = %+v", response.Operations)
	}
	if len(response.ParseRejections) != 1 || response.ParseRejections[0].ElementID != "invalid-move" || response.ParseRejections[0].Reason != "unresolved_canonical_id" {
		t.Fatalf("rejections = %+v", response.ParseRejections)
	}
	_, validator := validateAndDryRunTreeAuditOperations(state, response.Operations, segments, mc, roles, TreeAuditConfig{}, "audit-e", 13, true)
	if validator.OperationsValid != 1 || validator.OperationsApplied != 1 {
		t.Fatalf("validator = %+v", validator)
	}
}

// TestTreeAuditCanonicalizeNeverProducesLegacyNonCanonicalNodeIDReason covers
// 14.2(f): the retired v2 parser reason "non_canonical_node_id" (surfaced as
// "parser_non_canonical_node_id" once runTreeAudit prefixes it) must never be
// produced by the v3 canonicalizer, even across the distinct rejection paths
// (unresolved item, unresolved node-context alias, wrong resolved type).
func TestTreeAuditCanonicalizeNeverProducesLegacyNonCanonicalNodeIDReason(t *testing.T) {
	payload, _, _ := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	response := &treeAuditResponse{Operations: []treeAuditOperation{
		{OperationID: "unpromoted-to-parent", Type: TreeAuditMoveItem, TargetCanonicalItemID: "item-todo-plant-survey",
			FromParentCanonicalNodeID: "candidate-plant-study", ToParentCanonicalNodeID: "candidate-plant-video", Confidence: 1},
		{OperationID: "unknown-item-target", Type: TreeAuditMoveItem, TargetCanonicalItemID: "does-not-exist",
			FromParentCanonicalNodeID: "candidate-plant-study", ToParentCanonicalNodeID: "candidate-info-public", Confidence: 1},
		{OperationID: "node-target-is-item", Type: TreeAuditRenameGroup, TargetCanonicalNodeID: "item-risk-rare-plants", Label: "x", Confidence: 1},
	}}
	canonicalizeTreeAuditResponse(response, state)
	if len(response.Operations) != 0 {
		t.Fatalf("expected every operation rejected, got %+v", response.Operations)
	}
	if len(response.ParseRejections) != 3 {
		t.Fatalf("rejections = %+v", response.ParseRejections)
	}
	for _, rejection := range response.ParseRejections {
		if rejection.Reason == "non_canonical_node_id" || rejection.Reason == "non_canonical_candidate_id" || rejection.Reason == "parser_non_canonical_node_id" {
			t.Fatalf("legacy rejection reason resurfaced: %+v", rejection)
		}
	}
}

// TestTreeAuditMoveNodeAppliesAcrossDynamicTopics covers the Phase C
// move_node applier's success path: a group misfiled under one dynamic
// topic, whose own text (plus its child's) is more cohesive with a
// different dynamic topic, is reparented and the child follows via the
// unchanged existing ParentID chain.
func TestTreeAuditMoveNodeAppliesAcrossDynamicTopics(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items, liveAnalysisItem{
		ID: "item-todo-plant-permit", Kind: "todo", Title: "植物の許可申請を進める",
		Body: "関係機関へ植物の許可申請を提出", Status: "open",
		ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{22},
	})
	state.Tree.Nodes = append(state.Tree.Nodes,
		liveAnalysisTreeNode{ID: "group-x", Kind: "group", ParentID: "candidate-info-public", Label: "植物許可申請の整理"},
		liveAnalysisTreeNode{ID: "item-todo-plant-permit", Kind: "todo", ParentID: "group-x", Label: "植物の許可申請を進める", Status: "open"},
	)
	rebuildTreeAuditEdges(state.Tree)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-move-node", Type: TreeAuditMoveNode,
		TargetCanonicalNodeID: "group-x", FromParentCanonicalNodeID: "candidate-info-public",
		ToParentCanonicalNodeID: "candidate-plant-study", Confidence: 0.97,
		Reason: "植物許可申請を植物調査topicへ", EvidenceSequenceNos: []int64{22},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-move-node", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 || !result.TreeIntegrityValid {
		t.Fatalf("validator result = %+v", result)
	}
	node := treeNodeByID(dry.Tree, "group-x")
	if node == nil || node.ParentID != "candidate-plant-study" || node.LastParentChangeSource != "tree_auditor" {
		t.Fatalf("moved node = %+v", node)
	}
	if child := treeNodeByID(dry.Tree, "item-todo-plant-permit"); child == nil || child.ParentID != "group-x" {
		t.Fatalf("child node should remain attached to the moved group: %+v", child)
	}
}

// TestTreeAuditMoveNodeRejectsCycleIntoOwnDescendant covers the move_node
// cycle guard: a container cannot be reparented under one of its own
// descendants.
func TestTreeAuditMoveNodeRejectsCycleIntoOwnDescendant(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items, liveAnalysisItem{
		ID: "item-todo-plant-permit2", Kind: "todo", Title: "植物許可の追加確認",
		Body: "許可申請の追加確認", Status: "open",
		ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{22},
	})
	state.Tree.Nodes = append(state.Tree.Nodes,
		liveAnalysisTreeNode{ID: "group-x", Kind: "group", ParentID: "candidate-info-public", Label: "植物許可申請の整理"},
		liveAnalysisTreeNode{ID: "group-y", Kind: "group", ParentID: "group-x", Label: "植物許可申請の詳細"},
		liveAnalysisTreeNode{ID: "item-todo-plant-permit2", Kind: "todo", ParentID: "group-y", Label: "植物許可の追加確認", Status: "open"},
	)
	rebuildTreeAuditEdges(state.Tree)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-cycle", Type: TreeAuditMoveNode,
		TargetCanonicalNodeID: "group-x", FromParentCanonicalNodeID: "candidate-info-public",
		ToParentCanonicalNodeID: "group-y", Confidence: 1, EvidenceSequenceNos: []int64{22},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-cycle", 13, true)
	if result.OperationsValid != 0 || len(result.Evaluations) != 1 || result.Evaluations[0].Reason != "cycle_target_descendant" || result.Evaluations[0].Category != "moderate" {
		t.Fatalf("cycle rejection = %+v", result.Evaluations)
	}
}

// TestTreeAuditMoveNodeRejectsHardDepthViolation covers the move_node depth
// guard: moving a container (plus its subtree height) under a destination
// already near treeHardMaxDepth must be rejected.
func TestTreeAuditMoveNodeRejectsHardDepthViolation(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items,
		liveAnalysisItem{ID: "item-todo-plant-permit", Kind: "todo", Title: "植物の許可申請を進める", Body: "関係機関へ植物の許可申請を提出", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{22}},
		liveAnalysisItem{ID: "item-depth-leaf", Kind: "todo", Title: "深さ用ダミー", Body: "深さ検証用のダミーitem", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1},
	)
	state.Tree.Nodes = append(state.Tree.Nodes,
		liveAnalysisTreeNode{ID: "group-x", Kind: "group", ParentID: "candidate-info-public", Label: "植物許可申請の整理"},
		liveAnalysisTreeNode{ID: "item-todo-plant-permit", Kind: "todo", ParentID: "group-x", Label: "植物の許可申請を進める", Status: "open"},
		liveAnalysisTreeNode{ID: "group-depth-2", Kind: "group", ParentID: stableAgendaTopicID("agenda-1", 0), Label: "深さ2"},
		liveAnalysisTreeNode{ID: "group-depth-3", Kind: "group", ParentID: "group-depth-2", Label: "深さ3"},
		liveAnalysisTreeNode{ID: "group-depth-4", Kind: "group", ParentID: "group-depth-3", Label: "深さ4"},
		liveAnalysisTreeNode{ID: "group-depth-5", Kind: "group", ParentID: "group-depth-4", Label: "深さ5"},
		liveAnalysisTreeNode{ID: "item-depth-leaf", Kind: "todo", ParentID: "group-depth-5", Label: "深さ用ダミー", Status: "open"},
	)
	rebuildTreeAuditEdges(state.Tree)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-depth", Type: TreeAuditMoveNode,
		TargetCanonicalNodeID: "group-x", FromParentCanonicalNodeID: "candidate-info-public",
		ToParentCanonicalNodeID: "group-depth-5", Confidence: 1, EvidenceSequenceNos: []int64{22},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-depth", 13, true)
	if result.OperationsValid != 0 || len(result.Evaluations) != 1 || result.Evaluations[0].Reason != "hard_depth_limit" {
		t.Fatalf("depth rejection = %+v", result.Evaluations)
	}
}

// Materialized agenda topics are mutable containers; their logical agenda
// anchor remains unchanged when the topic is reparented.
func TestTreeAuditMoveNodeAllowsMaterializedAgendaTarget(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-fixed-node", Type: TreeAuditMoveNode,
		TargetCanonicalNodeID: stableAgendaTopicID("agenda-2", 0), FromParentCanonicalNodeID: treeRootNodeID,
		ToParentCanonicalNodeID: "candidate-plant-study", Confidence: 1, EvidenceSequenceNos: []int64{22},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-fixed-node", 13, true)
	if result.OperationsValid != 1 || len(result.Evaluations) != 1 || !result.Evaluations[0].Applied {
		t.Fatalf("materialized agenda move = %+v", result.Evaluations)
	}
}

// TestTreeAuditMoveNodeRejectsStaleFromParentMismatch covers the move_node
// stale-fromParent guard: the operation must restate the node's actual
// current parent, or it is rejected as stale (the client/model may be
// working off an outdated snapshot).
func TestTreeAuditMoveNodeRejectsStaleFromParentMismatch(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items, liveAnalysisItem{
		ID: "item-todo-plant-permit", Kind: "todo", Title: "植物の許可申請を進める",
		Body: "関係機関へ植物の許可申請を提出", Status: "open",
		ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{22},
	})
	state.Tree.Nodes = append(state.Tree.Nodes,
		liveAnalysisTreeNode{ID: "group-x", Kind: "group", ParentID: "candidate-info-public", Label: "植物許可申請の整理"},
		liveAnalysisTreeNode{ID: "item-todo-plant-permit", Kind: "todo", ParentID: "group-x", Label: "植物の許可申請を進める", Status: "open"},
	)
	rebuildTreeAuditEdges(state.Tree)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-stale-from", Type: TreeAuditMoveNode,
		// group-x's actual current parent is candidate-info-public; this
		// operation claims a different (stale) fromParent.
		TargetCanonicalNodeID: "group-x", FromParentCanonicalNodeID: "candidate-plant-study",
		ToParentCanonicalNodeID: stableAgendaTopicID("agenda-1", 0), Confidence: 1, EvidenceSequenceNos: []int64{22},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-stale-from", 13, true)
	if result.OperationsValid != 0 || len(result.Evaluations) != 1 || result.Evaluations[0].Reason != "from_parent_mismatch" {
		t.Fatalf("stale fromParent rejection = %+v", result.Evaluations)
	}
}

// TestTreeAuditMoveItemRedundantGroupFlattenExemptsStickinessMargin covers
// design brief D5/9.1's move_item margin exemption: a group whose own parent
// is the operation's destination, and whose label/description is
// essentially synonymous with that destination (a group that only restates
// its parent topic under an extra node), is exempt from the stickiness
// margin. The item's own text here shares no vocabulary with anything (both
// currentScore and newScore are 0), so without the exemption this move would
// always fail on parent_stickiness_margin; the test isolates the exemption
// itself as the only thing that can let it through.
func TestTreeAuditMoveItemRedundantGroupFlattenExemptsStickinessMargin(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items,
		liveAnalysisItem{
			ID: "item-todo-public-dup", Kind: "todo", Title: "無関係な話題の記録",
			Body: "今回の議論とは別の話題", Status: "open",
			ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{16},
		},
		liveAnalysisItem{
			ID: "item-todo-public-dup-sibling", Kind: "todo", Title: "情報公開資料の管理に残る項目",
			Body: "情報公開資料の管理を続ける", Status: "open",
			ClassificationStatus: classificationAssigned, AssignmentConfidence: 1,
		},
	)
	state.Tree.Nodes = append(state.Tree.Nodes,
		// group-flatten-dup keeps a second, unmoved child so removing
		// item-todo-public-dup leaves it a single-child (tolerated) group
		// rather than an empty one - remove_empty_group's own applier and
		// integrity check are covered separately; this test isolates only
		// the stickiness-margin exemption.
		liveAnalysisTreeNode{ID: "group-flatten-dup", Kind: "group", ParentID: "candidate-info-public", Label: "情報公開資料の管理"},
		liveAnalysisTreeNode{ID: "item-todo-public-dup", Kind: "todo", ParentID: "group-flatten-dup", Label: "無関係な話題の記録", Status: "open"},
		liveAnalysisTreeNode{ID: "item-todo-public-dup-sibling", Kind: "todo", ParentID: "group-flatten-dup", Label: "情報公開資料の管理に残る項目", Status: "open"},
	)
	rebuildTreeAuditEdges(state.Tree)
	segments = append(segments, domain.TranscriptSegment{SessionID: "session_26959b9519c5f880", SequenceNo: 16, Text: "無関係な話題を短く記録します。", IsFinal: true})
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-flatten", Type: TreeAuditMoveItem,
		TargetCanonicalItemID: "item-todo-public-dup", FromParentCanonicalNodeID: "group-flatten-dup",
		ToParentCanonicalNodeID: "candidate-info-public", Confidence: 0.97, EvidenceSequenceNos: []int64{16},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-flatten", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 {
		t.Fatalf("validator result = %+v", result)
	}
	if node := treeNodeByID(dry.Tree, "item-todo-public-dup"); node == nil || node.ParentID != "candidate-info-public" {
		t.Fatalf("moved node = %+v", node)
	}
}

// TestTreeAuditMoveItemRedundantGroupFlattenExemptionRequiresSynonymousGroup
// covers the negative case: when the current group's label/description is
// not synonymous with the destination (no shared subject term and low
// similarity), the exemption must not fire and the ordinary stickiness
// margin applies - here rejecting the otherwise-identical move.
func TestTreeAuditMoveItemRedundantGroupFlattenExemptionRequiresSynonymousGroup(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items, liveAnalysisItem{
		ID: "item-todo-public-dup2", Kind: "todo", Title: "無関係な話題の記録２",
		Body: "今回の議論とは別の話題２", Status: "open",
		ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{16},
	})
	state.Tree.Nodes = append(state.Tree.Nodes,
		liveAnalysisTreeNode{ID: "group-unrelated", Kind: "group", ParentID: "candidate-info-public", Label: "資材発注メモ"},
		liveAnalysisTreeNode{ID: "item-todo-public-dup2", Kind: "todo", ParentID: "group-unrelated", Label: "無関係な話題の記録２", Status: "open"},
	)
	rebuildTreeAuditEdges(state.Tree)
	segments = append(segments, domain.TranscriptSegment{SessionID: "session_26959b9519c5f880", SequenceNo: 16, Text: "無関係な話題を短く記録します。", IsFinal: true})
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-flatten-neg", Type: TreeAuditMoveItem,
		TargetCanonicalItemID: "item-todo-public-dup2", FromParentCanonicalNodeID: "group-unrelated",
		ToParentCanonicalNodeID: "candidate-info-public", Confidence: 0.97, EvidenceSequenceNos: []int64{16},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-flatten-neg", 13, true)
	if result.OperationsValid != 0 || len(result.Evaluations) != 1 || result.Evaluations[0].Reason != "parent_stickiness_margin" {
		t.Fatalf("expected parent_stickiness_margin without a synonymous group, got = %+v", result.Evaluations)
	}
}

// TestTreeAuditRemoveEmptyGroupAppliesToEmptyPromotedDynamicTopic covers
// design brief D5/9.2's remove_empty_group extension: a promoted dynamic
// topic (kind=topic, origin=dynamic) that has ended up with zero children is
// just as removable as an empty group.
func TestTreeAuditRemoveEmptyGroupAppliesToEmptyPromotedDynamicTopic(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
		ID: "candidate-empty-promoted", Kind: "topic", ParentID: treeRootNodeID,
		Label: "空になった動的topic", Origin: topicOriginDynamic,
	})
	rebuildTreeAuditEdges(state.Tree)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-remove-empty-topic", Type: TreeAuditRemoveEmptyGroup,
		TargetCanonicalNodeID: "candidate-empty-promoted", Confidence: 0.97, Reason: "子itemが全て移動済みの空topic",
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-remove-empty-topic", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 || !result.TreeIntegrityValid {
		t.Fatalf("validator result = %+v", result)
	}
	if node := treeNodeByID(dry.Tree, "candidate-empty-promoted"); node != nil {
		t.Fatalf("empty dynamic topic must be removed: %+v", node)
	}
}

// TestTreeAuditRemoveEmptyGroupRejectsImmutableOrNonEmptyContainers covers
// the remove_empty_group extension's guard rails: a fixed agenda topic
// cannot be removed even though it happens to have no children in this
// fixture, and a dynamic topic that still has children cannot be removed
// either.
func TestTreeAuditRemoveEmptyGroupRejectsImmutableOrNonEmptyContainers(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	operations := []treeAuditOperation{
		{OperationID: "op-fixed-agenda", Type: TreeAuditRemoveEmptyGroup, TargetCanonicalNodeID: stableAgendaTopicID("agenda-1", 0), Confidence: 1},
		{OperationID: "op-non-empty-dynamic", Type: TreeAuditRemoveEmptyGroup, TargetCanonicalNodeID: "candidate-plant-study", Confidence: 1},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, operations, segments, mc, roles, TreeAuditConfig{}, "audit-remove-guard", 13, true)
	if result.OperationsValid != 0 || result.OperationsRejected != len(operations) {
		t.Fatalf("validator result = %+v", result)
	}
	reasons := make(map[string]string)
	for _, evaluation := range result.Evaluations {
		reasons[evaluation.OperationID] = evaluation.Reason
	}
	if reasons["op-fixed-agenda"] != "unknown_or_immutable_container" {
		t.Fatalf("fixed agenda rejection = %q, want unknown_or_immutable_container", reasons["op-fixed-agenda"])
	}
	if reasons["op-non-empty-dynamic"] != "group_not_empty" {
		t.Fatalf("non-empty dynamic topic rejection = %q, want group_not_empty", reasons["op-non-empty-dynamic"])
	}
}

// TestTreeAuditMoveItemFixedAgendaReturnExemptsStickinessMargin covers the
// fixed-agenda-return margin exemption: an item under a dynamic topic (no
// fixed agenda ancestor at all) whose text shares no vocabulary with either
// its current parent or the destination is, by construction,
// currentParentGeneric (currentScore 0 < CohesionThreshold). Moving it to a
// fixed agenda whose bare label has almost no bigram surface either would
// normally fail parent_stickiness_margin (newScore-currentScore ~= 0 <
// margin); the exemption must let it through anyway.
func TestTreeAuditMoveItemFixedAgendaReturnExemptsStickinessMargin(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items, liveAnalysisItem{
		ID: "item-todo-agenda-return", Kind: "todo", Title: "無関係な話題の記録3",
		Body: "今回の議論とは別の話題3", Status: "open",
		ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{18},
	})
	state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
		ID: "item-todo-agenda-return", Kind: "todo", ParentID: "candidate-plant-study", Label: "無関係な話題の記録3", Status: "open",
	})
	rebuildTreeAuditEdges(state.Tree)
	segments = append(segments, domain.TranscriptSegment{SessionID: "session_26959b9519c5f880", SequenceNo: 18, Text: "無関係な話題を短く記録します。", IsFinal: true})
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-agenda-return", Type: TreeAuditMoveItem,
		TargetCanonicalItemID: "item-todo-agenda-return", FromParentCanonicalNodeID: "candidate-plant-study",
		ToParentCanonicalNodeID: stableAgendaTopicID("agenda-2", 0), Confidence: 0.97, EvidenceSequenceNos: []int64{18},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-agenda-return", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 {
		t.Fatalf("validator result = %+v", result)
	}
	if node := treeNodeByID(dry.Tree, "item-todo-agenda-return"); node == nil || node.ParentID != stableAgendaTopicID("agenda-2", 0) {
		t.Fatalf("moved node = %+v", node)
	}
}

// TestTreeAuditMoveItemFixedAgendaReturnExemptionRequiresGenericOrLowCohesionCurrentParent
// covers the negative case: an item already sitting under a cohesive,
// non-generic current parent (currentScore >= CohesionThreshold) is not
// currentParentGeneric, so the fixed-agenda-return exemption must not fire
// even though the destination is a fixed agenda - the ordinary stickiness
// margin still protects this otherwise-correct placement.
func TestTreeAuditMoveItemFixedAgendaReturnExemptionRequiresGenericOrLowCohesionCurrentParent(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		// item-todo-plant-survey is genuinely cohesive with its current
		// parent candidate-plant-study (both concern 植物/希少植物調査);
		// agenda-1 (渡り鳥の調査計画) is an unrelated fixed agenda.
		OperationID: "op-agenda-return-neg", Type: TreeAuditMoveItem,
		TargetCanonicalItemID: "item-todo-plant-survey", FromParentCanonicalNodeID: "candidate-plant-study",
		ToParentCanonicalNodeID: stableAgendaTopicID("agenda-1", 0), Confidence: 0.97, EvidenceSequenceNos: []int64{23},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-agenda-return-neg", 13, true)
	if result.OperationsValid != 0 || len(result.Evaluations) != 1 || result.Evaluations[0].Reason != "parent_stickiness_margin" {
		t.Fatalf("expected parent_stickiness_margin for a cohesive current parent, got = %+v", result.Evaluations)
	}
}

// TestTreeAuditFixedAgendaReturnExemptsHeuristicNonWorseningGate covers the
// third general rule (design brief D5 second addendum / §8.2): a
// fixed-agenda-return-exempt move whose own moved item still carries a
// subject_mismatch/cross_agenda_contamination finding against the
// destination's low-bigram-surface fixed-agenda label (because some other
// container in the tree coincidentally scores higher for it) must still
// apply - the raw, unfiltered deterministic defect count goes up purely from
// this self-referential finding, but the symmetric exclusion sees past it.
func TestTreeAuditFixedAgendaReturnExemptsHeuristicNonWorseningGate(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items, liveAnalysisItem{
		ID: "item-todo-agenda-worsen", Kind: "todo", Title: "植物性の記録メモ",
		Body: "植物性に関する短い記録", Status: "open",
		ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{18},
	})
	state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
		ID: "item-todo-agenda-worsen", Kind: "todo", ParentID: treeUnclassifiedTopicID, Label: "植物性の記録メモ", Status: "open",
	})
	rebuildTreeAuditEdges(state.Tree)
	segments = append(segments, domain.TranscriptSegment{SessionID: "session_26959b9519c5f880", SequenceNo: 18, Text: "植物性について短く記録を残します。", IsFinal: true})
	roles := classifyTreeAuditEvidence(state, segments)
	cfg := TreeAuditConfig{}.normalized()

	// Confirm the fixture assumption this test depends on: moving the item
	// from topic-unclassified to agenda-2 (騒音測定の実施方法, a bare fixed
	// agenda label with no description) raises the raw, unfiltered
	// deterministic defect count, because candidate-plant-study (an
	// unrelated dynamic topic that happens to share "植物" vocabulary)
	// coincidentally out-scores agenda-2's own bare label for this item once
	// it sits under a fixed agenda topic, doubling into a self-only
	// subject_mismatch + cross_agenda_contamination pair.
	beforeFindings := deterministicTreeAuditPrecheck(state, mc, roles, cfg)
	beforeQuality := auditHeuristicDefectCount(beforeFindings)
	moved := cloneLiveAnalysisPayload(state)
	for index := range moved.Tree.Nodes {
		if moved.Tree.Nodes[index].ID == "item-todo-agenda-worsen" {
			moved.Tree.Nodes[index].ParentID = stableAgendaTopicID("agenda-2", 0)
		}
	}
	rebuildTreeAuditEdges(moved.Tree)
	afterFindings := deterministicTreeAuditPrecheck(moved, mc, roles, cfg)
	afterQuality := auditHeuristicDefectCount(afterFindings)
	if afterQuality <= beforeQuality {
		t.Fatalf("fixture assumption violated: raw defect count did not worsen (before=%d after=%d) - this test needs a case where the plain gate would reject", beforeQuality, afterQuality)
	}

	operation := treeAuditOperation{
		OperationID: "op-agenda-worsen", Type: TreeAuditMoveItem,
		TargetCanonicalItemID: "item-todo-agenda-worsen", FromParentCanonicalNodeID: treeUnclassifiedTopicID,
		ToParentCanonicalNodeID: stableAgendaTopicID("agenda-2", 0), Confidence: 0.97, EvidenceSequenceNos: []int64{18},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-agenda-worsen", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 || !result.TreeIntegrityValid {
		t.Fatalf("validator result = %+v", result)
	}
	if node := treeNodeByID(dry.Tree, "item-todo-agenda-worsen"); node == nil || node.ParentID != stableAgendaTopicID("agenda-2", 0) {
		t.Fatalf("moved node = %+v", node)
	}
}

// TestTreeAuditExcludeSelfSubjectFindingsOnlyRemovesFindingsAboutTheMovedItemAlone
// directly locks in treeAuditExcludeSelfSubjectFindings's exact filtering
// semantics (design brief D5 second addendum / §8.2): only a
// subject_mismatch/cross_agenda_contamination finding whose NodeIDs name the
// moved item and no one else is excluded. A finding about a different node,
// a finding that also names another node alongside the moved item, and a
// finding of any other type must all survive - this is what keeps the
// exclusion from hiding an operation's side effects on the rest of the tree.
func TestTreeAuditExcludeSelfSubjectFindingsOnlyRemovesFindingsAboutTheMovedItemAlone(t *testing.T) {
	findings := []treeAuditPrecheckFinding{
		{Type: TreeAuditSubjectMismatch, NodeIDs: []string{"item-moved"}},
		{Type: TreeAuditCrossAgendaContamination, NodeIDs: []string{"item-moved"}},
		{Type: TreeAuditSubjectMismatch, NodeIDs: []string{"item-other"}},
		{Type: TreeAuditCrossAgendaContamination, NodeIDs: []string{"item-moved", "item-other"}},
		{Type: TreeAuditCandidateMixedSubjects, NodeIDs: []string{"item-moved"}},
		{Type: TreeAuditTopicOutlier, NodeIDs: []string{"item-moved"}},
	}
	filtered := treeAuditExcludeSelfSubjectFindings(findings, "item-moved")
	if len(filtered) != 4 {
		t.Fatalf("filtered findings = %+v, want 4 (only the two self-only subject_mismatch/cross_agenda_contamination entries removed)", filtered)
	}
	for _, finding := range filtered {
		if (finding.Type == TreeAuditSubjectMismatch || finding.Type == TreeAuditCrossAgendaContamination) &&
			len(finding.NodeIDs) == 1 && finding.NodeIDs[0] == "item-moved" {
			t.Fatalf("a self-only finding survived filtering: %+v", finding)
		}
	}
	foundOther := false
	foundMultiNode := false
	foundOtherTypes := 0
	for _, finding := range filtered {
		switch {
		case finding.Type == TreeAuditSubjectMismatch && len(finding.NodeIDs) == 1 && finding.NodeIDs[0] == "item-other":
			foundOther = true
		case finding.Type == TreeAuditCrossAgendaContamination && len(finding.NodeIDs) == 2:
			foundMultiNode = true
		case finding.Type == TreeAuditCandidateMixedSubjects || finding.Type == TreeAuditTopicOutlier:
			foundOtherTypes++
		}
	}
	if !foundOther {
		t.Fatal("a finding about a different node (item-other) must survive filtering")
	}
	if !foundMultiNode {
		t.Fatal("a multi-node finding naming the moved item alongside another node must survive filtering (conservative: not assumed to be about the moved item alone)")
	}
	if foundOtherTypes != 2 {
		t.Fatalf("findings of other types must survive filtering unconditionally, got %d, want 2", foundOtherTypes)
	}
}

// TestTreeAuditFixedAgendaReturnExemptRejectsNonMoveTypesAndNonFixedAgendaDestinations
// covers treeAuditFixedAgendaReturnExempt's own guard conditions directly:
// it must report false for operation types other than move_item/
// restore_previous_parent, and false when the destination has no fixed
// agenda ancestor at all (so the heuristic gate's symmetric exclusion never
// applies outside the exact fixed-agenda-return shape).
func TestTreeAuditFixedAgendaReturnExemptRejectsNonMoveTypesAndNonFixedAgendaDestinations(t *testing.T) {
	payload, segments, _ := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	segmentText := make(map[int64]string, len(segments))
	for _, segment := range segments {
		segmentText[segment.SequenceNo] = segment.Text
	}
	cfg := TreeAuditConfig{}.normalized()

	nonMoveType := treeAuditOperation{
		OperationID: "op-non-move", Type: TreeAuditDeactivateCandidate, TargetCandidateID: "candidate-plant-video", Confidence: 1,
	}
	if treeAuditFixedAgendaReturnExempt(nonMoveType, state, roles, segmentText, cfg) {
		t.Fatal("a non-move-type operation must never be fixed-agenda-return exempt")
	}

	nonFixedAgendaDestination := treeAuditOperation{
		OperationID: "op-non-fixed", Type: TreeAuditMoveItem,
		TargetCanonicalItemID: "item-risk-rare-plants", FromParentCanonicalNodeID: "candidate-info-public",
		ToParentCanonicalNodeID: "candidate-plant-study", Confidence: 1, EvidenceSequenceNos: []int64{22},
	}
	if treeAuditFixedAgendaReturnExempt(nonFixedAgendaDestination, state, roles, segmentText, cfg) {
		t.Fatal("a move whose destination has no fixed agenda ancestor must never be fixed-agenda-return exempt")
	}
}

// TestTreeAuditCascadePruneRemovesEmptyGroupAndCascadesToParentTopic covers
// the empty-container cascade: moving both children out of a 2-item group
// empties the group after the second move, which must be pruned (rather
// than rejecting that second move on tree_integrity_rejected); since the
// group was its own dynamic-topic parent's only child, the parent topic
// becomes empty in turn and must be pruned too in the same pass.
func TestTreeAuditCascadePruneRemovesEmptyGroupAndCascadesToParentTopic(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items,
		liveAnalysisItem{
			ID: "item-cascade-a", Kind: "todo", Title: "希少植物の追加確認A",
			Body: "希少植物の生態系の追加調査A", Status: "open",
			ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{18},
		},
		liveAnalysisItem{
			ID: "item-cascade-b", Kind: "todo", Title: "希少植物の追加確認B",
			Body: "希少植物の生態系の追加調査B", Status: "open",
			ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{19},
		},
	)
	state.Tree.Nodes = append(state.Tree.Nodes,
		liveAnalysisTreeNode{ID: "candidate-cascade-topic", Kind: "topic", ParentID: treeRootNodeID, Label: "会議運営メモ", Origin: topicOriginDynamic},
		liveAnalysisTreeNode{ID: "group-cascade", Kind: "group", ParentID: "candidate-cascade-topic", Label: "会議運営メモの整理"},
		liveAnalysisTreeNode{ID: "item-cascade-a", Kind: "todo", ParentID: "group-cascade", Label: "希少植物の追加確認A", Status: "open"},
		liveAnalysisTreeNode{ID: "item-cascade-b", Kind: "todo", ParentID: "group-cascade", Label: "希少植物の追加確認B", Status: "open"},
	)
	rebuildTreeAuditEdges(state.Tree)
	segments = append(segments,
		domain.TranscriptSegment{SessionID: "session_26959b9519c5f880", SequenceNo: 18, Text: "希少植物の生態系について追加調査Aを行います。", IsFinal: true},
		domain.TranscriptSegment{SessionID: "session_26959b9519c5f880", SequenceNo: 19, Text: "希少植物の生態系について追加調査Bを行います。", IsFinal: true},
	)
	roles := classifyTreeAuditEvidence(state, segments)
	operations := []treeAuditOperation{
		{
			OperationID: "op-cascade-a", Type: TreeAuditMoveItem,
			TargetCanonicalItemID: "item-cascade-a", FromParentCanonicalNodeID: "group-cascade",
			ToParentCanonicalNodeID: "candidate-plant-study", Confidence: 0.97, EvidenceSequenceNos: []int64{18},
		},
		{
			OperationID: "op-cascade-b", Type: TreeAuditMoveItem,
			TargetCanonicalItemID: "item-cascade-b", FromParentCanonicalNodeID: "group-cascade",
			ToParentCanonicalNodeID: "candidate-plant-study", Confidence: 0.97, EvidenceSequenceNos: []int64{19},
		},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, operations, segments, mc, roles, TreeAuditConfig{}, "audit-cascade", 13, true)
	if result.OperationsValid != 2 || result.OperationsApplied != 2 || !result.TreeIntegrityValid {
		t.Fatalf("validator result = %+v", result)
	}
	if node := treeNodeByID(dry.Tree, "item-cascade-a"); node == nil || node.ParentID != "candidate-plant-study" {
		t.Fatalf("item-cascade-a = %+v", node)
	}
	if node := treeNodeByID(dry.Tree, "item-cascade-b"); node == nil || node.ParentID != "candidate-plant-study" {
		t.Fatalf("item-cascade-b = %+v", node)
	}
	if node := treeNodeByID(dry.Tree, "group-cascade"); node != nil {
		t.Fatalf("group-cascade must be pruned once both children move out: %+v", node)
	}
	if node := treeNodeByID(dry.Tree, "candidate-cascade-topic"); node != nil {
		t.Fatalf("candidate-cascade-topic must cascade-prune once its only child (group-cascade) is pruned: %+v", node)
	}
}

// TestTreeAuditCascadePruneRemovesEmptyTopicUnclassified verifies that the
// synthetic unclassified bucket is removed after its final child moves to a
// grounded topic. Fixed agenda and root nodes remain protected elsewhere.
func TestTreeAuditCascadePruneRemovesEmptyTopicUnclassified(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	// item-todo-wind-standard is topic-unclassified's only child in the base
	// fixture; agenda-2 (騒音測定の実施方法) is a strong subject match for it.
	operation := treeAuditOperation{
		OperationID: "op-empty-unclassified", Type: TreeAuditMoveItem,
		TargetCanonicalItemID: "item-todo-wind-standard", FromParentCanonicalNodeID: treeUnclassifiedTopicID,
		ToParentCanonicalNodeID: stableAgendaTopicID("agenda-2", 0), Confidence: 0.97, EvidenceSequenceNos: []int64{13},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-empty-unclassified", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 || !result.TreeIntegrityValid {
		t.Fatalf("validator result = %+v", result)
	}
	unclassified := treeNodeByID(dry.Tree, treeUnclassifiedTopicID)
	if unclassified != nil {
		t.Fatalf("empty topic-unclassified must be pruned by the cascade: %+v", unclassified)
	}
	for _, node := range dry.Tree.Nodes {
		if node.ParentID == treeUnclassifiedTopicID {
			t.Fatalf("topic-unclassified unexpectedly still has a child: %+v", node)
		}
	}
}

// TestTreeAuditMergeItemsAppliesEvidenceUnionAndSurvivor covers the
// merge_items success path: two same-kind duplicate items connect via
// sameKindSemanticDuplicate, the first target survives, evidence/next
// actions union, and the companion's "resolved" status is not lost.
func TestTreeAuditMergeItemsAppliesEvidenceUnionAndSurvivor(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items,
		liveAnalysisItem{ID: "item-todo-vpn-cert-a", Kind: "todo", Title: "VPN証明書の更新", Body: "来月までにVPN証明書を更新する", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{22, 24}},
		liveAnalysisItem{ID: "item-todo-vpn-cert-b", Kind: "todo", Title: "VPN証明書の更新", Body: "VPN証明書の更新対応を進める", Status: "resolved", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{24}, NextActions: []string{"担当者へ申請書を提出する"}},
	)
	state.Tree.Nodes = append(state.Tree.Nodes,
		liveAnalysisTreeNode{ID: "item-todo-vpn-cert-a", Kind: "todo", ParentID: "candidate-info-public", Label: "VPN証明書の更新", Status: "open"},
		liveAnalysisTreeNode{ID: "item-todo-vpn-cert-b", Kind: "todo", ParentID: "candidate-info-public", Label: "VPN証明書の更新", Status: "open"},
	)
	rebuildTreeAuditEdges(state.Tree)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-merge", Type: TreeAuditMergeItems,
		TargetCanonicalItemIDs: []string{"item-todo-vpn-cert-a", "item-todo-vpn-cert-b"},
		Confidence:             0.97, Reason: "同一VPN証明書更新の重複統合", EvidenceSequenceNos: []int64{22},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-merge", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 || !result.TreeIntegrityValid {
		t.Fatalf("validator result = %+v", result)
	}
	if node := treeNodeByID(dry.Tree, "item-todo-vpn-cert-b"); node != nil {
		t.Fatalf("merged-away node must be removed from tree: %+v", node)
	}
	survivor := findItemByID(dry.Items, "item-todo-vpn-cert-a")
	companion := findItemByID(dry.Items, "item-todo-vpn-cert-b")
	if survivor == nil || companion == nil {
		t.Fatalf("both items must remain in Items[]: survivor=%v companion=%v", survivor, companion)
	}
	if companion.MergedIntoID != "item-todo-vpn-cert-a" {
		t.Fatalf("companion.mergedIntoId = %q, want item-todo-vpn-cert-a", companion.MergedIntoID)
	}
	if !containsInt64(survivor.EvidenceSequenceNos, 22) || !containsInt64(survivor.EvidenceSequenceNos, 24) {
		t.Fatalf("survivor evidence union incomplete: %v", survivor.EvidenceSequenceNos)
	}
	if survivor.Status != "resolved" {
		t.Fatalf("survivor status = %q, want resolved (kept from companion)", survivor.Status)
	}
	found := false
	for _, action := range survivor.NextActions {
		if action == "担当者へ申請書を提出する" {
			found = true
		}
	}
	if !found {
		t.Fatalf("survivor next actions missing companion's action: %v", survivor.NextActions)
	}
}

// TestTreeAuditMergeItemsRejectsDecisionAndUndecidedMismatch covers the
// merge_items decision/non-decision guard: sameCanonicalProposition never
// matches a decision-kind item, and sameKindSemanticDuplicate requires the
// same kind, so an unrelated decision+todo pair cannot connect.
func TestTreeAuditMergeItemsRejectsDecisionAndUndecidedMismatch(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-merge-mismatch", Type: TreeAuditMergeItems,
		TargetCanonicalItemIDs: []string{"item-decision-public-web", "item-todo-plant-survey"},
		Confidence:             0.97, Reason: "誤統合の試み", EvidenceSequenceNos: []int64{17},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-merge-bad", 13, true)
	// item-decision-public-web is kind "decision", so this merge_items
	// operation is escalated from its moderate baseline to destructive
	// (treeAuditEffectiveRiskClass) regardless of the unrelated rejection
	// reason below.
	if result.OperationsValid != 0 || len(result.Evaluations) != 1 || result.Evaluations[0].Reason != "items_not_connected_duplicates" || result.Evaluations[0].Category != "destructive" {
		t.Fatalf("mismatch rejection = %+v", result.Evaluations)
	}
}

// TestTreeAuditRewriteItemTitleAppliesWhenSubjectPreserved covers the
// rewrite_item_title success path: the new label shares a subject term with
// the old title/body, so both item.Title and node.Label are rewritten.
func TestTreeAuditRewriteItemTitleAppliesWhenSubjectPreserved(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-rewrite-title", Type: TreeAuditRewriteItemTitle,
		TargetCanonicalItemID: "item-risk-rare-plants", Label: "希少植物の生態調査",
		Confidence: 0.97, Reason: "簡潔な表現へ整理", EvidenceSequenceNos: []int64{22},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-rewrite", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 {
		t.Fatalf("validator result = %+v", result)
	}
	item := findItemByID(dry.Items, "item-risk-rare-plants")
	node := treeNodeByID(dry.Tree, "item-risk-rare-plants")
	if item == nil || item.Title != "希少植物の生態調査" || node == nil || node.Label != "希少植物の生態調査" {
		t.Fatalf("rewritten item/node = %+v %+v", item, node)
	}
}

// TestTreeAuditRewriteItemTitleRejectsSubjectChange covers the rewrite_item*
// anti-fabrication guard: a new label sharing no vocabulary with the old
// title/body or the op's own evidence text must be rejected.
func TestTreeAuditRewriteItemTitleRejectsSubjectChange(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-rewrite-bad", Type: TreeAuditRewriteItemTitle,
		TargetCanonicalItemID: "item-decision-public-web", Label: "駐車場整備計画",
		Confidence: 0.97, Reason: "無関係な書き換え", EvidenceSequenceNos: []int64{17},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-rewrite-bad", 13, true)
	if result.OperationsValid != 0 || len(result.Evaluations) != 1 || result.Evaluations[0].Reason != "subject_not_preserved" || result.Evaluations[0].Category != "safe" {
		t.Fatalf("subject-change rejection = %+v", result.Evaluations)
	}
}

func TestTreeAuditReclassifyIssueSubtypeAppliesWithPrimaryEvidence(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items, liveAnalysisItem{
		ID: "item-issue-wind", Kind: "issue", Subtype: issueSubtypeDiscussion,
		Title: "強風日の風速基準が未確定", Body: "どの風速を基準にするか", Status: "open",
		ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{13},
	})
	state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
		ID: "item-issue-wind", Kind: "issue", Subtype: issueSubtypeDiscussion,
		ParentID: stableAgendaTopicID("agenda-2", 0), Label: "強風日の風速基準が未確定", Status: "open", CreatedAtVersion: 8,
	})
	rebuildTreeAuditEdges(state.Tree)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-reclassify-subtype", Type: TreeAuditReclassifySubtype,
		TargetCanonicalItemID: "item-issue-wind", Subtype: issueSubtypeQuestion,
		Confidence: 0.97, Reason: "発言は回答を求める問い", EvidenceSequenceNos: []int64{13},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-reclassify", 13, true)
	if result.OperationsValid != 1 || result.ReclassificationsApplied != 1 {
		t.Fatalf("validator result=%+v", result)
	}
	item, node := findItemByID(dry.Items, "item-issue-wind"), treeNodeByID(dry.Tree, "item-issue-wind")
	if item == nil || node == nil || item.Subtype != issueSubtypeQuestion || node.Subtype != issueSubtypeQuestion || item.Kind != "issue" || node.Kind != "issue" {
		t.Fatalf("reclassified item=%+v node=%+v", item, node)
	}
}

// TestTreeAuditDeactivateItemPrefersMergeOnDuplicateGrounds covers the
// repair-priority gate: a connected duplicate must be merged, not hidden.
func TestTreeAuditDeactivateItemPrefersMergeOnDuplicateGrounds(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items,
		liveAnalysisItem{ID: "item-fact-dup-a", Kind: "fact", Title: "VPN証明書の更新", Body: "VPN証明書を更新する", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{22}},
		liveAnalysisItem{ID: "item-fact-dup-b", Kind: "fact", Title: "VPN証明書の更新", Body: "VPN証明書を新しくする", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{24}},
	)
	state.Tree.Nodes = append(state.Tree.Nodes,
		liveAnalysisTreeNode{ID: "item-fact-dup-a", Kind: "fact", ParentID: "candidate-info-public", Label: "VPN証明書の更新", Status: "open"},
		liveAnalysisTreeNode{ID: "item-fact-dup-b", Kind: "fact", ParentID: "candidate-info-public", Label: "VPN証明書の更新", Status: "open"},
	)
	rebuildTreeAuditEdges(state.Tree)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-deactivate", Type: TreeAuditDeactivateItem,
		TargetCanonicalItemID: "item-fact-dup-b", Confidence: 0.97, Reason: "item-fact-dup-aと重複",
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-deactivate", 13, true)
	if result.OperationsValid != 0 || len(result.Evaluations) != 1 || result.Evaluations[0].Reason != "merge_preferred" {
		t.Fatalf("validator result = %+v", result)
	}
	if node := treeNodeByID(dry.Tree, "item-fact-dup-b"); node == nil {
		t.Fatal("duplicate must remain visible until a merge operation is validated")
	}
	item := findItemByID(dry.Items, "item-fact-dup-b")
	if item == nil || item.Inactive {
		t.Fatalf("duplicate item was hidden: %+v", item)
	}
}

func TestTreeAuditDeactivateItemRejectsManualEditEvenWithDuplicateGrounds(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items,
		liveAnalysisItem{ID: "item-manual-dup-a", Kind: "todo", Title: "VPN証明書の更新", Body: "VPN証明書を更新する", Status: "open", EvidenceSequenceNos: []int64{22}},
		liveAnalysisItem{ID: "item-manual-dup-b", Kind: "todo", Title: "VPN証明書の更新", Body: "VPN証明書を新しくする", Status: "open", EvidenceSequenceNos: []int64{24}},
	)
	state.Tree.Nodes = append(state.Tree.Nodes,
		liveAnalysisTreeNode{ID: "item-manual-dup-a", Kind: "todo", ParentID: "candidate-info-public", Label: "VPN証明書の更新", Status: "open"},
		liveAnalysisTreeNode{ID: "item-manual-dup-b", Kind: "todo", ParentID: "candidate-info-public", Label: "VPN証明書の更新", Status: "open", LastParentChangeSource: "manual"},
	)
	rebuildTreeAuditEdges(state.Tree)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{OperationID: "op-manual-deactivate", Type: TreeAuditDeactivateItem, TargetCanonicalItemID: "item-manual-dup-b", Confidence: 0.99, Reason: "duplicate_item"}
	_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-manual-deactivate", 13, true)
	if result.OperationsValid != 0 || len(result.Evaluations) != 1 || result.Evaluations[0].Reason != "manual_edit_protected" {
		t.Fatalf("manual edit rejection = %+v", result.Evaluations)
	}
}

// TestTreeAuditDeactivateItemRejectsWithoutVerifiedGrounds covers the
// deactivate_item safety gate: none of the four server-verifiable grounds
// hold, so the operation must be rejected rather than trusting the model's
// bare assertion.
func TestTreeAuditDeactivateItemProtectsTodoAndDecision(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	for _, target := range []string{"item-todo-plant-survey", "item-decision-public-web"} {
		operation := treeAuditOperation{
			OperationID: "op-deactivate-protected", Type: TreeAuditDeactivateItem,
			TargetCanonicalItemID: target, Confidence: 0.97, Reason: "low_information",
		}
		_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-deactivate-protected", 13, true)
		if result.OperationsValid != 0 || len(result.Evaluations) != 1 || result.Evaluations[0].Reason != "protected_semantic_kind" {
			t.Fatalf("protected target %s rejection = %+v", target, result.Evaluations)
		}
	}
}

func TestTreeAuditDeactivateItemProtectsNewTentativeIssue(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items, liveAnalysisItem{
		ID: "item-issue-tentative", Kind: "issue", Subtype: issueSubtypeDiscussion,
		Title: "この点は確認が必要", Status: "open", InformationStatus: informationStatusTentative,
		ClassificationStatus: classificationTentative, EvidenceSequenceNos: []int64{25},
	})
	state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
		ID: "item-issue-tentative", Kind: "issue", Subtype: issueSubtypeDiscussion,
		ParentID: treeUnclassifiedTopicID, Label: "この点は確認が必要", Status: "open", CreatedAtVersion: 12,
	})
	rebuildTreeAuditEdges(state.Tree)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-deactivate-tentative", Type: TreeAuditDeactivateItem,
		TargetCanonicalItemID: "item-issue-tentative", Confidence: 0.99, Reason: "low_information",
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-tentative", 13, true)
	if result.OperationsValid != 0 || len(result.Evaluations) != 1 || result.Evaluations[0].Reason != "tentative_item_protected" || treeNodeByID(dry.Tree, "item-issue-tentative") == nil {
		t.Fatalf("tentative protection result=%+v", result)
	}
}

// TestTreeAuditAssignItemToCandidateApplies covers the
// assign_item_to_candidate success path: the item becomes tentative under
// the candidate and the candidate's evidence list picks it up.
func TestTreeAuditAssignItemToCandidateApplies(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-assign", Type: TreeAuditAssignItemToCandidate,
		TargetCanonicalItemID: "item-todo-plant-survey", TargetCandidateID: "candidate-plant-video",
		Confidence: 0.97, Reason: "植物関連資料・動画候補との関連",
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-assign", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 {
		t.Fatalf("validator result = %+v", result)
	}
	item := findItemByID(dry.Items, "item-todo-plant-survey")
	if item == nil || item.CandidateTopicID != "candidate-plant-video" || item.ClassificationStatus != classificationTentative {
		t.Fatalf("assigned item = %+v", item)
	}
	var candidate *emergingTopicCandidate
	for i := range dry.EmergingTopics {
		if dry.EmergingTopics[i].ID == "candidate-plant-video" {
			candidate = &dry.EmergingTopics[i]
		}
	}
	if candidate == nil || !containsExactString(candidate.EvidenceItemIDs, "item-todo-plant-survey") {
		t.Fatalf("candidate evidence not updated: %+v", candidate)
	}
}

// TestTreeAuditChangeEvidenceRoleDowngradesToReferenceRecap covers the
// change_evidence_role success path, including that the downgrade is
// visible to a fresh classifyTreeAuditEvidence pass (the next snapshot must
// honor the audit's own correction). Sequence 24 ("植物の種類を確認するため、
// 専門家による予備調査を検討します。") is classifyTreeAuditEvidence's own
// primary classification since H1: it is item-todo-plant-survey's own
// genuine supplementary evidence (not a reference to something else), which
// looksLikeTreeAuditReference now recognizes correctly by excluding the
// item's own self-similarity from matchedItems (previously this fixture's
// assumption relied on that very self-match bug demoting it to reference
// already, before any explicit operation ran). The point of this test is
// that an explicit, server-owned change_evidence_role correction can still
// downgrade it deliberately and have that downgrade persist across a fresh
// classification pass -- regardless of what the heuristic alone would have
// classified it as.
func TestTreeAuditChangeEvidenceRoleDowngradesToReferenceRecap(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	if roles[24] != treeAuditEvidencePrimary {
		t.Fatalf("fixture assumption violated: sequence 24 role = %q, want primary", roles[24])
	}
	operation := treeAuditOperation{
		OperationID: "op-downgrade", Type: TreeAuditChangeEvidenceRole,
		TargetCanonicalItemID: "item-todo-plant-survey", Confidence: 0.97,
		Reason: "24は補足発言のため格下げ", EvidenceSequenceNos: []int64{24},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-downgrade", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 {
		t.Fatalf("validator result = %+v", result)
	}
	item := findItemByID(dry.Items, "item-todo-plant-survey")
	if item == nil {
		t.Fatal("item missing after downgrade")
	}
	found := false
	for _, ref := range item.EvidenceRoles {
		if ref.SequenceNo == 24 && ref.Role == liveEvidenceReferenceRecap {
			found = true
		}
	}
	if !found {
		t.Fatalf("evidence role override missing: %+v", item.EvidenceRoles)
	}
	updatedRoles := classifyTreeAuditEvidence(dry, segments)
	if updatedRoles[24] != treeAuditEvidenceReference {
		t.Fatalf("classifyTreeAuditEvidence did not honor the override: roles[24]=%q", updatedRoles[24])
	}
}

// TestTreeAuditCreateTopicFromCandidateAppliesAndPromotes covers the
// create_topic_from_candidate success path: a fresh, non-recap-only,
// non-duplicate candidate is promoted to a topic node under root and its
// evidence item is reparented under it, mirroring the live promotion
// pipeline's side effects (candidate removed from EmergingTopics).
func TestTreeAuditCreateTopicFromCandidateAppliesAndPromotes(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.EmergingTopics = append(state.EmergingTopics, emergingTopicCandidate{
		ID: "candidate-parking-plan", Label: "工事車両の駐車計画", Description: "工事車両の駐車場所を確保する",
		EvidenceItemIDs: []string{"item-todo-parking"}, FirstRound: 12, LastRound: 12, RoundCount: 1,
	})
	state.Items = append(state.Items, liveAnalysisItem{
		ID: "item-todo-parking", Kind: "todo", Title: "工事車両の駐車場所を確保する", Body: "近隣に迷惑がかからないよう駐車計画を立てる",
		Status: "open", ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-parking-plan",
		AssignmentConfidence: .8, EvidenceSequenceNos: []int64{22},
	})
	state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
		ID: "item-todo-parking", Kind: "todo", ParentID: treeUnclassifiedTopicID, Label: "工事車両の駐車場所を確保する", Status: "open",
	})
	rebuildTreeAuditEdges(state.Tree)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-create-topic", Type: TreeAuditCreateTopicFromCandidate,
		TargetCandidateID: "candidate-parking-plan", Confidence: 0.97,
		Reason: "駐車計画を独立topicへ", EvidenceSequenceNos: []int64{22},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-create-topic", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 || !result.TreeIntegrityValid {
		t.Fatalf("validator result = %+v", result)
	}
	topicNode := treeNodeByID(dry.Tree, "candidate-parking-plan")
	if topicNode == nil || topicNode.Kind != "topic" || topicNode.ParentID != treeRootNodeID || topicNode.Origin != topicOriginDynamic {
		t.Fatalf("promoted topic node = %+v", topicNode)
	}
	childNode := treeNodeByID(dry.Tree, "item-todo-parking")
	if childNode == nil || childNode.ParentID != "candidate-parking-plan" {
		t.Fatalf("evidence item not reparented under new topic: %+v", childNode)
	}
	item := findItemByID(dry.Items, "item-todo-parking")
	if item == nil || item.ClassificationStatus != classificationAssigned || item.CandidateTopicID != "" {
		t.Fatalf("evidence item classification not updated: %+v", item)
	}
	for _, candidate := range dry.EmergingTopics {
		if candidate.ID == "candidate-parking-plan" {
			t.Fatalf("promoted candidate must be removed from EmergingTopics: %+v", candidate)
		}
	}
}

// TestTreeAuditCreateTopicFromCandidateRejectsRecapOnlyEvidence covers the
// create_topic_from_candidate recap-only guard: a candidate whose only
// evidence item is itself grounded solely in reference-role evidence must
// not be promoted to a topic.
func TestTreeAuditCreateTopicFromCandidateRejectsRecapOnlyEvidence(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items, liveAnalysisItem{
		ID: "item-todo-recap-echo", Kind: "todo", Title: "住民説明会の開催日確認", Body: "recapで触れられた開催日の確認",
		Status: "open", ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-recap-only",
		AssignmentConfidence: .6, EvidenceSequenceNos: []int64{28},
	})
	state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
		ID: "item-todo-recap-echo", Kind: "todo", ParentID: treeUnclassifiedTopicID, Label: "住民説明会の開催日確認", Status: "open",
	})
	state.EmergingTopics = append(state.EmergingTopics, emergingTopicCandidate{
		ID: "candidate-recap-only", Label: "recapのみの候補", Description: "まとめ発言由来の候補",
		EvidenceItemIDs: []string{"item-todo-recap-echo"}, FirstRound: 12, LastRound: 12, RoundCount: 1,
	})
	rebuildTreeAuditEdges(state.Tree)
	roles := classifyTreeAuditEvidence(state, segments)
	if roles[28] != treeAuditEvidenceReference {
		t.Fatalf("fixture assumption violated: role[28]=%q", roles[28])
	}
	operation := treeAuditOperation{
		OperationID: "op-recap-only", Type: TreeAuditCreateTopicFromCandidate,
		TargetCandidateID: "candidate-recap-only", Confidence: 0.97, Reason: "recapのみ", EvidenceSequenceNos: []int64{28},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-recap-only", 13, true)
	if result.OperationsValid != 0 || len(result.Evaluations) != 1 || result.Evaluations[0].Reason != "recap_only_candidate" {
		t.Fatalf("recap-only rejection = %+v", result.Evaluations)
	}
}

// TestTreeAuditCreateTopicFromCandidateRejectsFixedAgendaDuplicate covers
// the create_topic_from_candidate fixed-agenda guard: a candidate whose
// label closely matches an existing fixed agenda title should fold into
// that agenda instead of becoming a new dynamic topic.
func TestTreeAuditCreateTopicFromCandidateRejectsFixedAgendaDuplicate(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items, liveAnalysisItem{
		ID: "item-todo-agenda-echo", Kind: "todo", Title: "住民説明資料の作成方法", Body: "住民説明資料をどう作成するか検討する",
		Status: "open", ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-agenda-echo",
		AssignmentConfidence: .8, EvidenceSequenceNos: []int64{22},
	})
	state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
		ID: "item-todo-agenda-echo", Kind: "todo", ParentID: treeUnclassifiedTopicID, Label: "住民説明資料の作成方法", Status: "open",
	})
	state.EmergingTopics = append(state.EmergingTopics, emergingTopicCandidate{
		ID: "candidate-agenda-echo", Label: "住民説明資料の作成", Description: "住民説明資料をどう作るか",
		EvidenceItemIDs: []string{"item-todo-agenda-echo"}, FirstRound: 12, LastRound: 12, RoundCount: 1,
	})
	rebuildTreeAuditEdges(state.Tree)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-agenda-dup", Type: TreeAuditCreateTopicFromCandidate,
		TargetCandidateID: "candidate-agenda-echo", Confidence: 0.97, Reason: "固定agendaと重複", EvidenceSequenceNos: []int64{22},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-agenda-dup", 13, true)
	if result.OperationsValid != 0 || len(result.Evaluations) != 1 || result.Evaluations[0].Reason != "should_fold_into_fixed_agenda" || result.Evaluations[0].Category != "moderate" {
		t.Fatalf("fixed-agenda duplicate rejection = %+v", result.Evaluations)
	}
}

// TestTreeAuditEffectiveConfidenceHighConfidenceThresholdDefaultUnchanged
// covers design D4's guardrail that effective-confidence bonuses never lower
// the gate itself: HighConfidenceThreshold's normalized default must stay at
// 0.90.
func TestTreeAuditEffectiveConfidenceHighConfidenceThresholdDefaultUnchanged(t *testing.T) {
	cfg := TreeAuditConfig{}.normalized()
	if cfg.HighConfidenceThreshold != 0.90 {
		t.Fatalf("HighConfidenceThreshold = %v, want 0.90", cfg.HighConfidenceThreshold)
	}
}

// TestTreeAuditEffectiveConfidenceAppliesFixedAgendaMatchFromUnclassified
// covers design D4 14.4(a)/(d): an item under topic-unclassified whose text
// matches a fixed agenda topic, which the deterministic precheck already
// flagged as a subject mismatch pointing at that same agenda, is applied
// even though the model only reported confidence 0.80 - the three
// structural bonuses (unclassified current parent, precheck agreement,
// fixed-agenda match) push the effective confidence to 0.90+ without the
// HighConfidenceThreshold itself changing.
func TestTreeAuditEffectiveConfidenceAppliesFixedAgendaMatchFromUnclassified(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items, liveAnalysisItem{
		ID: "item-todo-noise-echo", Kind: "todo", Title: "騒音測定の実施方法の追加確認",
		Body: "現地機材の設置に関する追加確認", Status: "open",
		ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{40},
	})
	state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
		ID: "item-todo-noise-echo", Kind: "todo", ParentID: treeUnclassifiedTopicID, Label: "騒音測定の実施方法の追加確認", Status: "open",
	})
	rebuildTreeAuditEdges(state.Tree)
	segments = append(segments, domain.TranscriptSegment{SessionID: "session_26959b9519c5f880", SequenceNo: 40, Text: "現地機材の設置について追加で確認します。", IsFinal: true})
	roles := map[int64]treeAuditEvidenceRole{40: treeAuditEvidencePrimary}
	operation := treeAuditOperation{
		OperationID: "op-fixed-agenda", Type: TreeAuditMoveItem,
		TargetCanonicalItemID: "item-todo-noise-echo", FromParentCanonicalNodeID: treeUnclassifiedTopicID,
		ToParentCanonicalNodeID: stableAgendaTopicID("agenda-2", 0), Confidence: 0.80,
		Reason: "騒音測定の実施方法へ", EvidenceSequenceNos: []int64{40},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-fixed-agenda", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 || !result.TreeIntegrityValid {
		t.Fatalf("validator result = %+v", result)
	}
	if len(result.Evaluations) != 1 || result.Evaluations[0].ModelConfidence != 0.80 || result.Evaluations[0].EffectiveConfidence < 0.90 {
		t.Fatalf("evaluation = %+v", result.Evaluations)
	}
	if node := treeNodeByID(dry.Tree, "item-todo-noise-echo"); node == nil || node.ParentID != stableAgendaTopicID("agenda-2", 0) {
		t.Fatalf("moved node = %+v", node)
	}
}

// TestTreeAuditEffectiveConfidenceRejectsHighConfidenceWithoutStructuralSupport
// covers design D4 14.4(b)/(e): an item that already sits under its correct,
// high-cohesion parent, with no deterministic precheck support for a
// different destination, must still be rejected even at modelConfidence
// 0.95 - no structural bonus fires, so effective confidence stays at 0.95
// (well above the gate), and the operation is instead caught by the
// (unhalved, since the current parent is not generic/unclassified/low
// cohesion) parent-stickiness margin. This proves raising modelConfidence
// alone cannot bypass the structural safety net.
func TestTreeAuditEffectiveConfidenceRejectsHighConfidenceWithoutStructuralSupport(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-no-support", Type: TreeAuditMoveItem,
		TargetCanonicalItemID: "item-decision-public-web", FromParentCanonicalNodeID: "candidate-info-public",
		ToParentCanonicalNodeID: "candidate-plant-study", Confidence: 0.95, EvidenceSequenceNos: []int64{17},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-no-support", 13, true)
	if result.OperationsValid != 0 || len(result.Evaluations) != 1 {
		t.Fatalf("validator result = %+v", result)
	}
	evaluation := result.Evaluations[0]
	if evaluation.Reason != "parent_stickiness_margin" {
		t.Fatalf("reason = %q, want parent_stickiness_margin (confidence alone must not bypass stickiness)", evaluation.Reason)
	}
	if evaluation.ModelConfidence != 0.95 || evaluation.EffectiveConfidence < 0.90 {
		t.Fatalf("evaluation confidence fields = %+v, want effective >= threshold since no penalty applies", evaluation)
	}
}

// TestTreeAuditEffectiveConfidenceAppliesModerateConfidenceWithStrongServerEvidence
// covers design D4 14.4(c), replaying the target session's actual shape: a
// risk item misfiled under an unrelated topic, which the deterministic
// precheck already flags (subject_mismatch/cross_agenda_contamination
// pointing at the same destination) and whose current-parent cohesion is
// weak, is applied at modelConfidence 0.85 - below the target session's
// observed 0.90 cutoff that caused this exact move to be rejected before
// D4.
func TestTreeAuditEffectiveConfidenceAppliesModerateConfidenceWithStrongServerEvidence(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-move-plant-moderate", Type: TreeAuditMoveItem,
		TargetCanonicalItemID: "item-risk-rare-plants", FromParentCanonicalNodeID: "candidate-info-public",
		ToParentCanonicalNodeID: "candidate-plant-study", Confidence: 0.85,
		Reason: "湿地・希少植物subjectへ戻す", EvidenceSequenceNos: []int64{22},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-moderate", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 || !result.TreeIntegrityValid {
		t.Fatalf("validator result = %+v", result)
	}
	if len(result.Evaluations) != 1 || result.Evaluations[0].ModelConfidence != 0.85 || result.Evaluations[0].EffectiveConfidence < 0.90 {
		t.Fatalf("evaluation = %+v", result.Evaluations)
	}
	if node := treeNodeByID(dry.Tree, "item-risk-rare-plants"); node == nil || node.ParentID != "candidate-plant-study" {
		t.Fatalf("moved node = %+v", node)
	}
}

// TestTreeAuditRecentParentChangeStickyExemptForReferenceEvidenceReparent
// covers design D4 14.4(f): recent_parent_change_sticky is exempted when the
// deterministic precheck already flagged the node's latest reparent as
// reference/recap-evidence-only. Both items here share the same
// (non-generic, non-unclassified, high-cohesion) current parent and the
// same recent LastParentChangeVersion, isolating the finding itself as the
// only variable. item-sticky-plain has ordinary primary evidence and no
// such finding, so its move is blocked by recent_parent_change_sticky.
// item-sticky-recap's own stored evidence is entirely reference-role
// (satisfying deterministicTreeAuditPrecheck's reference_evidence_reparent
// condition), so its move is NOT blocked by the sticky guard - it instead
// fails on reference_evidence_only, an unrelated and expected gate, since a
// node whose only bound evidence is reference-role can never supply the
// primary evidence move_item independently requires. The distinct failure
// reason on item-sticky-recap (anything other than
// recent_parent_change_sticky) is the proof the sticky guard was bypassed.
func TestTreeAuditRecentParentChangeStickyExemptForReferenceEvidenceReparent(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items,
		liveAnalysisItem{ID: "item-sticky-plain", Kind: "todo", Title: "植物調査の追跡確認", Body: "湿地・希少植物の生態系調査の追跡",
			Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{60}},
		liveAnalysisItem{ID: "item-sticky-recap", Kind: "todo", Title: "植物調査の追跡確認２", Body: "湿地・希少植物の生態系調査の追跡２",
			Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{61}},
	)
	state.Tree.Nodes = append(state.Tree.Nodes,
		liveAnalysisTreeNode{ID: "item-sticky-plain", Kind: "todo", ParentID: "candidate-plant-study", Label: "植物調査の追跡確認", Status: "open", LastParentChangeVersion: 12},
		liveAnalysisTreeNode{ID: "item-sticky-recap", Kind: "todo", ParentID: "candidate-plant-study", Label: "植物調査の追跡確認２", Status: "open", LastParentChangeVersion: 12},
	)
	state.TreeChanges = &liveAnalysisTreeChanges{ReparentedNodeIDs: []string{"item-sticky-recap"}}
	rebuildTreeAuditEdges(state.Tree)
	segments = append(segments,
		domain.TranscriptSegment{SessionID: "session_26959b9519c5f880", SequenceNo: 60, Text: "植物調査の追跡について現地から報告がありました。", IsFinal: true},
		domain.TranscriptSegment{SessionID: "session_26959b9519c5f880", SequenceNo: 61, Text: "以上、植物調査の追跡状況をまとめます。", IsFinal: true},
	)
	roles := map[int64]treeAuditEvidenceRole{60: treeAuditEvidencePrimary, 61: treeAuditEvidenceReference}
	operations := []treeAuditOperation{
		{OperationID: "op-plain", Type: TreeAuditMoveItem, TargetCanonicalItemID: "item-sticky-plain",
			FromParentCanonicalNodeID: "candidate-plant-study", ToParentCanonicalNodeID: "candidate-info-public",
			Confidence: 0.97, EvidenceSequenceNos: []int64{60}},
		{OperationID: "op-recap", Type: TreeAuditMoveItem, TargetCanonicalItemID: "item-sticky-recap",
			FromParentCanonicalNodeID: "candidate-plant-study", ToParentCanonicalNodeID: "candidate-info-public",
			Confidence: 0.97, EvidenceSequenceNos: []int64{61}},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, operations, segments, mc, roles, TreeAuditConfig{}, "audit-sticky-exempt", 13, true)
	reasons := make(map[string]string)
	for _, evaluation := range result.Evaluations {
		reasons[evaluation.OperationID] = evaluation.Reason
	}
	if reasons["op-plain"] != "recent_parent_change_sticky" {
		t.Fatalf("op-plain reason = %q, want recent_parent_change_sticky", reasons["op-plain"])
	}
	// op-recap's own bound evidence (sequence 61) is entirely reference-role,
	// so once the sticky guard is bypassed it fails on
	// reference_evidence_only instead - an unrelated, expected gate that
	// confirms (rather than undermines) the exemption: the operation reached
	// a later check than the sticky guard at all.
	if reasons["op-recap"] != "reference_evidence_only" {
		t.Fatalf("op-recap reason = %q, want reference_evidence_only (proof the sticky guard was bypassed)", reasons["op-recap"])
	}
}

// TestTreeAuditEffectiveConfidenceRecapContaminationPenaltyBlocksOtherwiseValidMove
// covers design D4 14.4(g): reusing the fixed-agenda-match scenario (see
// TestTreeAuditEffectiveConfidenceAppliesFixedAgendaMatchFromUnclassified),
// adding a second, reference-role evidence sequence to the same operation
// mixes reference evidence into otherwise-primary evidence and must apply
// recapContaminationPenalty (-0.10). move_item is risk class "moderate"
// (treeAuditOperationRiskClass), so its gate is
// HighConfidenceThreshold-0.10 = 0.80, not the flat 0.90 this test used
// before per-risk-class thresholds existed: modelConfidence 0.70 is chosen
// so that without the penalty it would clear the 0.80 gate (0.70 + 0.15
// bonus = 0.85) but with the penalty it does not (0.70 + 0.15 - 0.10 =
// 0.75 < 0.80), isolating recapContaminationPenalty as the one variable
// that flips the outcome.
func TestTreeAuditEffectiveConfidenceRecapContaminationPenaltyBlocksOtherwiseValidMove(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items, liveAnalysisItem{
		ID: "item-todo-noise-echo", Kind: "todo", Title: "騒音測定の実施方法の追加確認",
		Body: "現地機材の設置に関する追加確認", Status: "open",
		ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{40, 41},
	})
	state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
		ID: "item-todo-noise-echo", Kind: "todo", ParentID: treeUnclassifiedTopicID, Label: "騒音測定の実施方法の追加確認", Status: "open",
	})
	rebuildTreeAuditEdges(state.Tree)
	segments = append(segments,
		domain.TranscriptSegment{SessionID: "session_26959b9519c5f880", SequenceNo: 40, Text: "現地機材の設置について追加で確認します。", IsFinal: true},
		domain.TranscriptSegment{SessionID: "session_26959b9519c5f880", SequenceNo: 41, Text: "以上、騒音測定の実施方法をまとめます。", IsFinal: true},
	)
	roles := map[int64]treeAuditEvidenceRole{40: treeAuditEvidencePrimary, 41: treeAuditEvidenceReference}
	operation := treeAuditOperation{
		OperationID: "op-fixed-agenda-contaminated", Type: TreeAuditMoveItem,
		TargetCanonicalItemID: "item-todo-noise-echo", FromParentCanonicalNodeID: treeUnclassifiedTopicID,
		ToParentCanonicalNodeID: stableAgendaTopicID("agenda-2", 0), Confidence: 0.70,
		Reason: "騒音測定の実施方法へ", EvidenceSequenceNos: []int64{40, 41},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-contaminated", 13, true)
	if result.OperationsValid != 0 || len(result.Evaluations) != 1 {
		t.Fatalf("validator result = %+v", result)
	}
	evaluation := result.Evaluations[0]
	if evaluation.Reason != "below_effective_confidence_threshold" {
		t.Fatalf("reason = %q, want below_effective_confidence_threshold", evaluation.Reason)
	}
	if evaluation.EffectiveConfidence >= 0.80 {
		t.Fatalf("effectiveConfidence = %v, want < 0.80 (move_item's moderate-risk gate) once recapContaminationPenalty applies", evaluation.EffectiveConfidence)
	}
}

// TestTreeAuditCanonicalizeNormalizesMoveItemRedundantTargetNodeID
// reproduces the session_5e4da9dc40d50940 anomaly: the model correctly set
// targetCanonicalItemId, but also redundantly copied the same item ID into
// targetCanonicalNodeId - a field move_item never reads. Before
// normalizeTreeAuditOperationFields existed, resolving that unused field
// with requireContainer=true rejected the whole operation on
// target_not_node. It must now survive canonicalization (with the unused
// field cleared) and reach the validator's own evaluation instead of
// falling out at the parser/canonicalization layer.
func TestTreeAuditCanonicalizeNormalizesMoveItemRedundantTargetNodeID(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	response := &treeAuditResponse{Operations: []treeAuditOperation{
		{OperationID: "op-redundant-node-id", Type: TreeAuditMoveItem,
			TargetCanonicalItemID:     "item-risk-rare-plants",
			TargetCanonicalNodeID:     "item-risk-rare-plants", // redundant; move_item never reads this field
			FromParentCanonicalNodeID: "candidate-info-public", ToParentCanonicalNodeID: "candidate-plant-study",
			Confidence: 0.7, EvidenceSequenceNos: []int64{22},
		},
	}}
	canonicalizeTreeAuditResponse(response, state)
	if len(response.ParseRejections) != 0 || len(response.Operations) != 1 {
		t.Fatalf("redundant targetCanonicalNodeId must not be rejected at canonicalization: rejections=%+v operations=%+v", response.ParseRejections, response.Operations)
	}
	if got := response.Operations[0].TargetCanonicalNodeID; got != "" {
		t.Fatalf("targetCanonicalNodeId must be cleared for move_item, got %q", got)
	}
	_, result := validateAndDryRunTreeAuditOperations(state, response.Operations, segments, mc, roles, TreeAuditConfig{}, "audit-redundant-node-id", 13, true)
	if len(result.Evaluations) != 1 {
		t.Fatalf("validator result = %+v", result)
	}
	if reason := result.Evaluations[0].Reason; reason == "target_not_node" || reason == "unresolved_canonical_id" || reason == "ambiguous_alias" {
		t.Fatalf("operation must reach validator evaluation, not fail on a canonicalization-layer reason: %+v", result.Evaluations[0])
	}
}

// TestTreeAuditEffectiveConfidenceEpsilonAcceptsModerateThresholdTie
// reproduces the final-review op1 float-comparison bug (W7.1):
// modelConfidence 0.7 plus two treeAuditConfidenceBonusStep bonuses (0.05
// each, from a generic FROM parent and a precheck-agreeing move) sums in
// float64 to 0.7999999999999999, not exactly 0.8. Before the epsilon guard,
// `effectiveConfidence < threshold` rejected this purely from float addition
// error even though the operation is exactly at the moderate-risk threshold
// (HighConfidenceThreshold-0.10 = 0.80); it must now be accepted.
func TestTreeAuditEffectiveConfidenceEpsilonAcceptsModerateThresholdTie(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID:               "op-epsilon",
		Type:                      TreeAuditMoveItem,
		TargetCanonicalItemID:     "item-risk-rare-plants",
		FromParentCanonicalNodeID: "candidate-info-public",
		ToParentCanonicalNodeID:   "candidate-plant-study",
		Confidence:                0.7,
		EvidenceSequenceNos:       []int64{22},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-epsilon", 13, true)
	if len(result.Evaluations) != 1 {
		t.Fatalf("validator result = %+v", result)
	}
	evaluation := result.Evaluations[0]
	if evaluation.EffectiveConfidence != 0.7999999999999999 {
		t.Fatalf("effectiveConfidence = %v, want the float64 sum 0.7+0.05+0.05 (0.7999999999999999) to reproduce the epsilon case", evaluation.EffectiveConfidence)
	}
	if evaluation.Category != "moderate" {
		t.Fatalf("category = %q, want moderate", evaluation.Category)
	}
	if result.OperationsValid != 1 || result.OperationsApplied != 1 || evaluation.Reason == "below_effective_confidence_threshold" {
		t.Fatalf("evaluation = %+v, want accepted at the moderate threshold (0.80) despite the float64 tie", evaluation)
	}
}

// TestTreeAuditRewriteItemTitleAppliesAtSafeRiskThreshold covers the
// rewrite_item_title risk-class gate: rewrite_item_title is risk class
// "safe" (treeAuditOperationRiskClass), so its threshold is
// HighConfidenceThreshold-0.20 = 0.70, not the old flat 0.90 gate that
// rejected most of the model's real-world rewrite proposals (typically
// reported around 0.75-0.85).
func TestTreeAuditRewriteItemTitleAppliesAtSafeRiskThreshold(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	operation := treeAuditOperation{
		OperationID: "op-rewrite-title-safe", Type: TreeAuditRewriteItemTitle,
		TargetCanonicalItemID: "item-risk-rare-plants", Label: "希少植物の生態調査",
		Confidence: 0.75, Reason: "簡潔な表現へ整理", EvidenceSequenceNos: []int64{22},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-rewrite-safe", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 || len(result.Evaluations) != 1 {
		t.Fatalf("validator result = %+v", result)
	}
	if got := result.Evaluations[0].Category; got != "safe" {
		t.Fatalf("category = %q, want safe", got)
	}
	item := findItemByID(dry.Items, "item-risk-rare-plants")
	if item == nil || item.Title != "希少植物の生態調査" {
		t.Fatalf("rewritten item = %+v", item)
	}
}

// TestTreeAuditDeactivateItemRejectsBelowDestructiveThreshold covers the
// deactivate_item risk-class gate: deactivate_item is the sole
// "destructive" risk class operation, so its threshold stays at the full
// HighConfidenceThreshold (0.90) even though every other applicable
// operation type's gate was lowered. modelConfidence 0.85 must still be
// rejected on the confidence gate itself, for a plain target and for
// decision/todo/risk targets alike (the confidence gate is evaluated
// before applyOneTreeAuditOperation's own protected_semantic_kind check,
// so every target here fails on the same, confidence-only reason).
func TestTreeAuditDeactivateItemRejectsBelowDestructiveThreshold(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	for _, target := range []string{"item-todo-plant-survey", "item-decision-public-web", "item-risk-rare-plants"} {
		operation := treeAuditOperation{
			OperationID: "op-deactivate-below-threshold", Type: TreeAuditDeactivateItem,
			TargetCanonicalItemID: target, Confidence: 0.85, Reason: "low_information",
		}
		_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-deactivate-below", 13, true)
		if result.OperationsValid != 0 || len(result.Evaluations) != 1 {
			t.Fatalf("target %s validator result = %+v", target, result)
		}
		evaluation := result.Evaluations[0]
		if evaluation.Reason != "below_effective_confidence_threshold" || evaluation.Category != "destructive" {
			t.Fatalf("target %s deactivate_item at confidence 0.85 = %+v, want below_effective_confidence_threshold/destructive", target, evaluation)
		}
	}
}

// TestTreeAuditMergeItemsAppliesAtModerateThresholdRejectsDecisionAtSameConfidence
// covers the merge_items risk-class gate and its decision-item escalation
// rule. Two duplicate issue-kind siblings under the same parent merge at
// modelConfidence 0.82 - above merge_items' moderate threshold
// (HighConfidenceThreshold-0.10 = 0.80) but below the old flat 0.90 gate.
// The same confidence targeting a pair that includes a decision-kind item
// is rejected instead: treeAuditEffectiveRiskClass escalates any
// merge_items touching a decision item to destructive (threshold = 0.90).
func TestTreeAuditMergeItemsAppliesAtModerateThresholdRejectsDecisionAtSameConfidence(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items,
		liveAnalysisItem{ID: "item-issue-noise-a", Kind: "issue", Subtype: issueSubtypeDiscussion, Title: "騒音基準の未確定", Body: "騒音基準がまだ決まっていない", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{22, 24}},
		liveAnalysisItem{ID: "item-issue-noise-b", Kind: "issue", Subtype: issueSubtypeDiscussion, Title: "騒音基準の未確定", Body: "騒音基準の決定がまだ", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{24}},
		liveAnalysisItem{ID: "item-decision-dup-a", Kind: "decision", Title: "調査結果を図付きでウェブ公開", Body: "住民説明資料の公開方針", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{17}},
	)
	state.Tree.Nodes = append(state.Tree.Nodes,
		liveAnalysisTreeNode{ID: "item-issue-noise-a", Kind: "issue", Subtype: issueSubtypeDiscussion, ParentID: "candidate-info-public", Label: "騒音基準の未確定", Status: "open"},
		liveAnalysisTreeNode{ID: "item-issue-noise-b", Kind: "issue", Subtype: issueSubtypeDiscussion, ParentID: "candidate-info-public", Label: "騒音基準の未確定", Status: "open"},
		liveAnalysisTreeNode{ID: "item-decision-dup-a", Kind: "decision", ParentID: "candidate-info-public", Label: "調査結果を図付きでウェブ公開", Status: "open"},
	)
	rebuildTreeAuditEdges(state.Tree)
	roles := classifyTreeAuditEvidence(state, segments)

	mergeIssues := treeAuditOperation{
		OperationID: "op-merge-issue-moderate", Type: TreeAuditMergeItems,
		TargetCanonicalItemIDs: []string{"item-issue-noise-a", "item-issue-noise-b"},
		Confidence:             0.82, Reason: "同一issueの重複統合", EvidenceSequenceNos: []int64{22},
	}
	_, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{mergeIssues}, segments, mc, roles, TreeAuditConfig{}, "audit-merge-issue", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 || len(result.Evaluations) != 1 || result.Evaluations[0].Category != "moderate" {
		t.Fatalf("issue merge at confidence 0.82 = %+v", result)
	}

	mergeDecision := treeAuditOperation{
		OperationID: "op-merge-decision-escalated", Type: TreeAuditMergeItems,
		TargetCanonicalItemIDs: []string{"item-decision-dup-a", "item-decision-public-web"},
		Confidence:             0.82, Reason: "同一decisionの重複統合", EvidenceSequenceNos: []int64{17},
	}
	_, result2 := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{mergeDecision}, segments, mc, roles, TreeAuditConfig{}, "audit-merge-decision", 13, true)
	if result2.OperationsValid != 0 || len(result2.Evaluations) != 1 {
		t.Fatalf("decision merge at confidence 0.82 = %+v", result2)
	}
	evaluation := result2.Evaluations[0]
	if evaluation.Reason != "below_effective_confidence_threshold" || evaluation.Category != "destructive" {
		t.Fatalf("decision merge evaluation = %+v, want below_effective_confidence_threshold/destructive", evaluation)
	}
}

// TestTreeAuditCanonicalizeMergeItemsRescuesSingularTargetIntoPlural covers
// the merge_items canonicalization rescue: a second merge target left in
// the singular targetCanonicalItemId field (alongside a single-element
// targetCanonicalItemIds) is folded into a two-element
// targetCanonicalItemIds before targetCanonicalItemId is cleared, instead
// of being silently dropped.
func TestTreeAuditCanonicalizeMergeItemsRescuesSingularTargetIntoPlural(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	state.Items = append(state.Items,
		liveAnalysisItem{ID: "item-todo-vpn-cert-a", Kind: "todo", Title: "VPN証明書の更新", Body: "来月までにVPN証明書を更新する", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{22, 24}},
		liveAnalysisItem{ID: "item-todo-vpn-cert-b", Kind: "todo", Title: "VPN証明書の更新", Body: "VPN証明書の更新対応を進める", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{24}},
	)
	state.Tree.Nodes = append(state.Tree.Nodes,
		liveAnalysisTreeNode{ID: "item-todo-vpn-cert-a", Kind: "todo", ParentID: "candidate-info-public", Label: "VPN証明書の更新", Status: "open"},
		liveAnalysisTreeNode{ID: "item-todo-vpn-cert-b", Kind: "todo", ParentID: "candidate-info-public", Label: "VPN証明書の更新", Status: "open"},
	)
	rebuildTreeAuditEdges(state.Tree)
	roles := classifyTreeAuditEvidence(state, segments)
	response := &treeAuditResponse{Operations: []treeAuditOperation{
		{OperationID: "op-merge-rescue", Type: TreeAuditMergeItems,
			TargetCanonicalItemID: "item-todo-vpn-cert-a", TargetCanonicalItemIDs: []string{"item-todo-vpn-cert-b"},
			Confidence: 0.97, Reason: "同一VPN証明書更新の重複統合", EvidenceSequenceNos: []int64{22},
		},
	}}
	canonicalizeTreeAuditResponse(response, state)
	if len(response.ParseRejections) != 0 || len(response.Operations) != 1 {
		t.Fatalf("canonicalized response = %+v", response)
	}
	got := response.Operations[0]
	if got.TargetCanonicalItemID != "" {
		t.Fatalf("targetCanonicalItemId must be cleared after the merge rescue, got %q", got.TargetCanonicalItemID)
	}
	if len(got.TargetCanonicalItemIDs) != 2 || got.TargetCanonicalItemIDs[0] != "item-todo-vpn-cert-a" || got.TargetCanonicalItemIDs[1] != "item-todo-vpn-cert-b" {
		t.Fatalf("targetCanonicalItemIds = %v, want [item-todo-vpn-cert-a item-todo-vpn-cert-b]", got.TargetCanonicalItemIDs)
	}
	_, result := validateAndDryRunTreeAuditOperations(state, response.Operations, segments, mc, roles, TreeAuditConfig{}, "audit-merge-rescue", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 {
		t.Fatalf("validator result = %+v", result)
	}
}

// TestTreeAuditCanonicalizeFoldCandidateComplementsDestinationField covers
// the fold_candidate_into_topic destination-field complement: both
// applyOneTreeAuditOperation and treeAuditEffectiveConfidence read
// toParentCanonicalNodeId (never targetCanonicalNodeId) as the fold
// destination. When the model instead puts the destination in
// targetCanonicalNodeId and leaves toParentCanonicalNodeId blank - the
// exact shape observed in session_5e4da9dc40d50940, where the destination
// went unread and no fixedAgendaMatchBonus could fire - the value is
// copied over before targetCanonicalNodeId is cleared.
func TestTreeAuditCanonicalizeFoldCandidateComplementsDestinationField(t *testing.T) {
	payload, segments, mc := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	roles := classifyTreeAuditEvidence(state, segments)
	destination := stableAgendaTopicID("agenda-2", 0)
	response := &treeAuditResponse{Operations: []treeAuditOperation{
		{OperationID: "op-fold-complement", Type: TreeAuditFoldCandidateIntoTopic,
			TargetCandidateID:     "candidate-plant-video",
			TargetCanonicalNodeID: destination, // destination given in the wrong field; toParentCanonicalNodeId left blank
			Confidence:            0.97, Reason: "強風日の風速基準を騒音測定topicへ統合", EvidenceSequenceNos: []int64{13},
		},
	}}
	canonicalizeTreeAuditResponse(response, state)
	if len(response.ParseRejections) != 0 || len(response.Operations) != 1 {
		t.Fatalf("canonicalized response = %+v", response)
	}
	got := response.Operations[0]
	if got.TargetCanonicalNodeID != "" {
		t.Fatalf("targetCanonicalNodeId must be cleared after the fold destination complement, got %q", got.TargetCanonicalNodeID)
	}
	if got.ToParentCanonicalNodeID != destination {
		t.Fatalf("toParentCanonicalNodeId = %q, want %q (complemented from targetCanonicalNodeId)", got.ToParentCanonicalNodeID, destination)
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, response.Operations, segments, mc, roles, TreeAuditConfig{}, "audit-fold-complement", 13, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 || !result.TreeIntegrityValid {
		t.Fatalf("validator result = %+v", result)
	}
	if node := treeNodeByID(dry.Tree, "item-todo-wind-standard"); node == nil || node.ParentID != destination {
		t.Fatalf("folded item parent = %+v, want parent %q", node, destination)
	}
}

// TestTreeAuditCanonicalizeStillRejectsGenuinelyInvalidRequiredField covers
// the negative case for field normalization: a field the operation's
// applier *does* use must still be rejected with its original reason when
// it genuinely fails to resolve, even after normalization runs. Normalizing
// unused fields never loosens resolution of the fields that remain in use.
func TestTreeAuditCanonicalizeStillRejectsGenuinelyInvalidRequiredField(t *testing.T) {
	payload, _, _ := targetTreeAuditFixture(t)
	state := previousLiveAnalysisState(payload)
	response := &treeAuditResponse{Operations: []treeAuditOperation{
		{OperationID: "op-rename-bad-target", Type: TreeAuditRenameGroup,
			TargetCanonicalNodeID: "item-risk-rare-plants", // a real ID, but a detail item, not a container
			Label:                 "植物調査班", Confidence: 1,
		},
		{OperationID: "op-move-unknown-item", Type: TreeAuditMoveItem,
			TargetCanonicalItemID:     "does-not-exist",
			FromParentCanonicalNodeID: "candidate-info-public", ToParentCanonicalNodeID: "candidate-plant-study",
			Confidence: 1,
		},
	}}
	canonicalizeTreeAuditResponse(response, state)
	if len(response.Operations) != 0 {
		t.Fatalf("both operations must still be rejected at canonicalization, got %+v", response.Operations)
	}
	if len(response.ParseRejections) != 2 {
		t.Fatalf("rejections = %+v", response.ParseRejections)
	}
	byID := make(map[string]string, len(response.ParseRejections))
	for _, rejection := range response.ParseRejections {
		byID[rejection.ElementID] = rejection.Reason
	}
	if byID["op-rename-bad-target"] != "target_not_node" {
		t.Fatalf("op-rename-bad-target reason = %q, want target_not_node", byID["op-rename-bad-target"])
	}
	if byID["op-move-unknown-item"] != "unresolved_canonical_id" {
		t.Fatalf("op-move-unknown-item reason = %q, want unresolved_canonical_id", byID["op-move-unknown-item"])
	}
}

func TestEndingSessionDiscardsDelayedLiveAuditApply(t *testing.T) {
	service, analysisRepo, auditRepo, publisher, completer, payload := newTreeAuditRunnerFixture(t, false)
	completer.block = make(chan struct{})
	service.scheduleTreeAudit(context.Background(), "session_26959b9519c5f880", "test", payload, 12)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 1 })
	service.mu.Lock()
	state := service.sessionStateLocked("session_26959b9519c5f880")
	state.finalizing = true
	state.auditClosed = true
	service.mu.Unlock()
	close(completer.block)
	waitForInternalAudit(t, time.Second, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return !state.auditRunning
	})
	if analysisRepo.version("session_26959b9519c5f880") != 12 || len(publisher.snapshot()) != 0 {
		t.Fatal("live audit updated or published after ending")
	}
	if run := auditRepo.latest(); run == nil || run.Result != "session_ending_discarded" || run.Disposition != "stale" {
		t.Fatalf("ending audit run = %+v", run)
	}
}

func TestFinalTreeReviewIgnoresLateProviderResponseAfterTimeout(t *testing.T) {
	service, analysisRepo, _, publisher, completer, payload := newTreeAuditRunnerFixture(t, false)
	service.config.TreeAudit.Timeout = 10 * time.Millisecond
	completer.block = make(chan struct{})
	completer.ignoreContext = true
	type result struct {
		execution treeAuditExecution
		err       error
	}
	done := make(chan result, 1)
	go func() {
		execution, err := service.runFinalTreeReview(context.Background(), "session_26959b9519c5f880", payload, 12)
		done <- result{execution: execution, err: err}
	}()
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 1 })
	time.Sleep(20 * time.Millisecond)
	close(completer.block)
	got := <-done
	if !errors.Is(got.err, context.DeadlineExceeded) || got.execution.Applied {
		t.Fatalf("late final review result = %+v err=%v", got.execution, got.err)
	}
	if analysisRepo.version("session_26959b9519c5f880") != 12 || len(publisher.snapshot()) != 0 {
		t.Fatal("late final review response changed the tree")
	}
}

func TestFinalTreeReviewUsesSeparateFlightFromCanceledLiveAudit(t *testing.T) {
	service, _, _, _, completer, payload := newTreeAuditRunnerFixture(t, false)
	completer.block = make(chan struct{})
	service.scheduleTreeAudit(context.Background(), "session_26959b9519c5f880", "test", payload, 12)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 1 })
	service.mu.Lock()
	state := service.sessionStateLocked("session_26959b9519c5f880")
	state.auditClosed = true
	service.mu.Unlock()
	done := make(chan struct{})
	go func() {
		_, _ = service.runFinalTreeReview(context.Background(), "session_26959b9519c5f880", payload, 12)
		close(done)
	}()
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 2 })
	close(completer.block)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("final review did not complete")
	}
}

func TestTreeAuditApplyUsesVersionCASAndPublishes(t *testing.T) {
	service, analysisRepo, auditRepo, publisher, _, payload := newTreeAuditRunnerFixture(t, false)
	execution, err := service.runTreeAudit(context.Background(), "session_26959b9519c5f880", "test", aiTaskTreeAudit, payload, 12, false)
	if err != nil {
		t.Fatalf("runTreeAudit() error = %v", err)
	}
	if !execution.Applied || execution.Version != 13 || analysisRepo.version("session_26959b9519c5f880") != 13 {
		t.Fatalf("execution = %+v liveVersion=%d", execution, analysisRepo.version("session_26959b9519c5f880"))
	}
	if len(publisher.snapshot()) != 1 {
		t.Fatalf("publish count = %d, want 1", len(publisher.snapshot()))
	}
	if run := auditRepo.latest(); run == nil || run.Result != "applied" || run.ResultingTreeVersion != 13 {
		t.Fatalf("saved audit run = %+v", run)
	}
}

func TestTreeAuditDurableClaimSuppressesDuplicateSnapshot(t *testing.T) {
	service, _, auditRepo, _, completer, payload := newTreeAuditRunnerFixture(t, false)
	first, err := service.runTreeAudit(context.Background(), "session_26959b9519c5f880", "test", aiTaskTreeAudit, payload, 12, false)
	if err != nil || first.Result != "applied" {
		t.Fatalf("first audit=%+v err=%v", first, err)
	}
	second, err := service.runTreeAudit(context.Background(), "session_26959b9519c5f880", "test", aiTaskTreeAudit, payload, 12, false)
	if err != nil || second.Result != "duplicate_snapshot" {
		t.Fatalf("second audit=%+v err=%v", second, err)
	}
	if completer.callCount() != 1 || len(auditRepo.runs) != 1 {
		t.Fatalf("provider calls=%d runs=%d", completer.callCount(), len(auditRepo.runs))
	}
}

func TestTreeAuditStaleCASRejectsOperations(t *testing.T) {
	service, analysisRepo, auditRepo, publisher, _, payload := newTreeAuditRunnerFixture(t, true)
	execution, err := service.runTreeAudit(context.Background(), "session_26959b9519c5f880", "test", aiTaskTreeAudit, payload, 12, false)
	if err != nil {
		t.Fatalf("runTreeAudit() error = %v", err)
	}
	if execution.Result != "stale_tree_version" || execution.Applied || analysisRepo.version("session_26959b9519c5f880") != 12 {
		t.Fatalf("execution = %+v", execution)
	}
	if len(publisher.snapshot()) != 0 {
		t.Fatal("stale audit must not publish")
	}
	var validator treeAuditValidatorResult
	if run := auditRepo.latest(); run == nil || json.Unmarshal(run.ValidatorResult, &validator) != nil || validator.StaleOperationsRejected != 1 {
		t.Fatalf("stale validator result = %+v run=%+v", validator, run)
	}
}

func TestTreeAuditSchedulerCoalescesSingleFlight(t *testing.T) {
	service, _, _, _, completer, payload := newTreeAuditRunnerFixture(t, false)
	completer.block = make(chan struct{})
	service.config.TreeAudit.MinInterval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service.scheduleTreeAudit(ctx, "session_26959b9519c5f880", "first", payload, 12)
	waitForInternalAudit(t, time.Second, func() bool { return completer.callCount() == 1 })
	service.scheduleTreeAudit(ctx, "session_26959b9519c5f880", "second", payload, 12)
	service.mu.Lock()
	state := service.sessionStateLocked("session_26959b9519c5f880")
	pending, running := state.auditPending, state.auditRunning
	service.mu.Unlock()
	if !pending || !running {
		t.Fatalf("single-flight state pending=%t running=%t", pending, running)
	}
	close(completer.block)
	waitForInternalAudit(t, time.Second, func() bool {
		service.mu.Lock()
		defer service.mu.Unlock()
		return !service.sessionStateLocked("session_26959b9519c5f880").auditRunning
	})
	if completer.callCount() != 1 {
		t.Fatalf("coalesced audit calls = %d, want 1", completer.callCount())
	}
}

func TestFinalTreeReviewTimeoutKeepsLastKnownGoodTree(t *testing.T) {
	service, _, _, _, completer, payload := newTreeAuditRunnerFixture(t, false)
	completer.block = make(chan struct{})
	service.config.TreeAudit.Timeout = 10 * time.Millisecond
	execution, err := service.runFinalTreeReview(context.Background(), "session_26959b9519c5f880", payload, 12)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runFinalTreeReview() error = %v, want deadline exceeded", err)
	}
	if execution.Version != 12 || string(execution.Payload) != string(payload) {
		t.Fatalf("fallback execution changed last-known-good tree: %+v", execution)
	}
}

func TestTaskModelRoutingIncludesTreeAuditAndFinalTreeReview(t *testing.T) {
	config := MeetingAnalysisConfig{Model: "shared", TaskModels: AITaskModels{LiveExtraction: "nano", TreeAudit: "mini-audit", FinalTreeReview: "mini-final"}}
	if got := config.modelNameFor(aiTaskLiveExtraction); got != "nano" {
		t.Fatalf("live deployment = %q", got)
	}
	if got := config.modelNameFor(aiTaskTreeAudit); got != "mini-audit" {
		t.Fatalf("tree audit deployment = %q", got)
	}
	if got := config.modelNameFor(aiTaskFinalTreeReview); got != "mini-final" {
		t.Fatalf("final tree review deployment = %q", got)
	}
	if got := config.modelNameFor(aiTaskFinalSummary); got != "shared" {
		t.Fatalf("final summary fallback deployment = %q", got)
	}
}

func TestTreeAuditAndFinalReviewSendConfiguredDeployments(t *testing.T) {
	service, _, _, _, completer, payload := newTreeAuditRunnerFixture(t, false)
	service.config.TaskModels.FinalTreeReview = "final-review-mini"
	if _, err := service.runTreeAudit(context.Background(), "session_26959b9519c5f880", "test", aiTaskTreeAudit, payload, 12, false); err != nil {
		t.Fatalf("runTreeAudit() error = %v", err)
	}
	if _, err := service.runFinalTreeReview(context.Background(), "session_26959b9519c5f880", payload, 12); err != nil {
		t.Fatalf("runFinalTreeReview() error = %v", err)
	}
	requests := completer.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(requests))
	}
	if requests[0].Deployment != "tree-audit-mini" || requests[1].Deployment != "final-review-mini" {
		t.Fatalf("provider deployments = %q, %q", requests[0].Deployment, requests[1].Deployment)
	}
}

func targetTreeAuditFixture(t *testing.T) (json.RawMessage, []domain.TranscriptSegment, *meetingContext) {
	t.Helper()
	mc := &meetingContext{Title: "沿岸部風力発電計画", Agenda: []agendaItem{
		{ID: "agenda-1", Title: "渡り鳥の調査計画", Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "騒音測定の実施方法", Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "住民説明資料の作成", Order: 3, Role: agendaRolePrimary},
	}}
	state := liveAnalysisPayload{
		Summary: "対象セッションfixture", TreeVersion: 12, CoveredThroughSequenceNo: 29,
		Items: []liveAnalysisItem{
			{ID: "item-risk-rare-plants", Kind: "risk", Title: "建設予定地近傍の湿地・希少植物の可能性の調査", Body: "湿地評価と希少植物の調査", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{22}},
			{ID: "item-todo-plant-survey", Kind: "todo", Title: "植物の種類確認の予備調査", Body: "専門家による希少植物の予備調査", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: 1, EvidenceSequenceNos: []int64{23, 24}},
			{ID: "item-todo-wind-standard", Kind: "todo", Title: "強風日での風速基準の決定", Body: "騒音測定時の風速基準", Status: "open", ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-plant-video", AssignmentConfidence: 1, EvidenceSequenceNos: []int64{13, 28}},
			{ID: "item-decision-public-web", Kind: "decision", Title: "調査結果を図付きでウェブ公開", Body: "住民説明資料の公開方針", Status: "open", ClassificationStatus: classificationAssigned, AssignmentConfidence: .95, EvidenceSequenceNos: []int64{17}},
		},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "沿岸部風力発電計画", Origin: topicOriginSystem},
			{ID: stableAgendaTopicID("agenda-1", 0), Kind: "topic", ParentID: treeRootNodeID, Label: "渡り鳥の調査計画", Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary, AgendaRefs: []string{"agenda-1"}, Materialized: true},
			{ID: stableAgendaTopicID("agenda-2", 0), Kind: "topic", ParentID: treeRootNodeID, Label: "騒音測定の実施方法", Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary, AgendaRefs: []string{"agenda-2"}, Materialized: true},
			{ID: stableAgendaTopicID("agenda-3", 0), Kind: "topic", ParentID: treeRootNodeID, Label: "住民説明資料の作成", Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary, AgendaRefs: []string{"agenda-3"}, Materialized: true},
			{ID: "candidate-plant-study", Kind: "topic", ParentID: treeRootNodeID, Label: "植物調査", Description: "湿地・希少植物の生態系調査", Origin: topicOriginDynamic},
			{ID: "candidate-info-public", Kind: "topic", ParentID: treeRootNodeID, Label: "情報公開・説明資料", Description: "公開資料の方針", Origin: topicOriginDynamic},
			{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "追加論点", Origin: topicOriginSystem},
			{ID: "item-risk-rare-plants", Kind: "risk", ParentID: "candidate-info-public", Label: "建設予定地近傍の湿地・希少植物の可能性の調査", Status: "open"},
			{ID: "item-todo-plant-survey", Kind: "todo", ParentID: "candidate-plant-study", Label: "植物の種類確認の予備調査", Status: "open"},
			{ID: "item-todo-wind-standard", Kind: "todo", ParentID: treeUnclassifiedTopicID, Label: "強風日での風速基準の決定", Status: "open"},
			{ID: "item-decision-public-web", Kind: "decision", ParentID: "candidate-info-public", Label: "調査結果を図付きでウェブ公開", Status: "open"},
		}},
		EmergingTopics: []emergingTopicCandidate{{ID: "candidate-plant-video", Label: "植物関連資料・動画", Description: "希少植物調査の新規話題", EvidenceItemIDs: []string{"item-todo-wind-standard"}, FirstRound: 12, LastRound: 12, RoundCount: 1}},
	}
	rebuildTreeAuditEdges(state.Tree)
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	texts := map[int64]string{
		13: "ただし、強風日の測定事項については、どの風速を基準にするか決まっていません。",
		17: "住民が後から確認できるよう、調査結果の概要を団体のウェブサイトで公開します。",
		22: "アジェンダ外ですが、小規模な湿地が見つかり希少な植物が生息している可能性があります。",
		23: "既存の鳥類調査や騒音調査に含めず、新しい植物調査課題として扱います。",
		24: "植物の種類を確認するため、専門家による予備調査を検討します。",
		25: "以上をまとめます。",
		28: "未解決の課題は強風日の風速基準と住民説明会の開催日です。",
		29: "希少植物については、アジェンダ外から生まれた新しい動画として次回も検討します。",
	}
	segments := make([]domain.TranscriptSegment, 0, len(texts))
	for _, sequenceNo := range []int64{13, 17, 22, 23, 24, 25, 28, 29} {
		segments = append(segments, domain.TranscriptSegment{SessionID: "session_26959b9519c5f880", CallID: "call-1", SequenceNo: sequenceNo, SpeakerName: "山下", Text: texts[sequenceNo], IsFinal: true})
	}
	return encoded, segments, mc
}

func assertAuditFindingForNode(t *testing.T, findings []treeAuditPrecheckFinding, findingType TreeAuditFindingType, nodeID string) {
	t.Helper()
	for _, finding := range findings {
		if finding.Type == findingType && containsExactString(finding.NodeIDs, nodeID) {
			return
		}
	}
	t.Fatalf("finding type=%s node=%s not found in %+v", findingType, nodeID, findings)
}

type internalAuditAnalysisRepository struct {
	mu    sync.Mutex
	store map[string]domain.MeetingAIAnalysis
}

func (r *internalAuditAnalysisRepository) UpsertMeetingAIAnalysis(_ context.Context, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store[analysis.SessionID] = analysis
	copy := analysis
	return &copy, nil
}

func (r *internalAuditAnalysisRepository) CompareAndSwapMeetingAIAnalysis(_ context.Context, expectedVersion int64, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, exists := r.store[analysis.SessionID]
	if (exists && current.Version != expectedVersion) || (!exists && expectedVersion != 0) {
		return nil, false, nil
	}
	r.store[analysis.SessionID] = analysis
	copy := analysis
	return &copy, true, nil
}

func (r *internalAuditAnalysisRepository) GetMeetingAIAnalysis(_ context.Context, sessionID string, analysisType domain.MeetingAIAnalysisType) (*domain.MeetingAIAnalysis, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	analysis, ok := r.store[sessionID]
	if !ok || analysis.Type != analysisType {
		return nil, domain.ErrNotFound
	}
	copy := analysis
	return &copy, nil
}

func (r *internalAuditAnalysisRepository) ListMeetingAIAnalysesForSessions(_ context.Context, sessionIDs []string, analysisType domain.MeetingAIAnalysisType) ([]domain.MeetingAIAnalysis, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]domain.MeetingAIAnalysis, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		if analysis, ok := r.store[sessionID]; ok && analysis.Type == analysisType {
			items = append(items, analysis)
		}
	}
	return items, nil
}

func (r *internalAuditAnalysisRepository) version(sessionID string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.store[sessionID].Version
}

type internalAuditRepository struct {
	mu          sync.Mutex
	runs        []domain.MeetingTreeAuditRun
	analysis    *internalAuditAnalysisRepository
	staleCAS    bool
	tryStartErr error
	saveErr     error
	countErr    error
}

func (r *internalAuditRepository) CheckMeetingTreeAuditRepository(context.Context) error {
	return nil
}

func (r *internalAuditRepository) TryStartMeetingTreeAuditRun(_ context.Context, run domain.MeetingTreeAuditRun) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tryStartErr != nil {
		return false, r.tryStartErr
	}
	for _, existing := range r.runs {
		if existing.Status == domain.MeetingTreeAuditRunning && existing.SessionID == run.SessionID && existing.Task == run.Task && existing.BasedOnTreeVersion == run.BasedOnTreeVersion && existing.SnapshotHash == run.SnapshotHash && existing.PromptVersion == run.PromptVersion && existing.Deployment == run.Deployment {
			return false, nil
		}
	}
	r.runs = append(r.runs, run)
	return true, nil
}

func (r *internalAuditRepository) SaveMeetingTreeAuditRun(_ context.Context, run domain.MeetingTreeAuditRun) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveErr != nil {
		return r.saveErr
	}
	for index := range r.runs {
		if r.runs[index].ID == run.ID {
			r.runs[index] = run
			return nil
		}
	}
	r.runs = append(r.runs, run)
	return nil
}

func (r *internalAuditRepository) GetLatestMeetingTreeAuditRun(context.Context, string) (*domain.MeetingTreeAuditRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.runs) == 0 {
		return nil, domain.ErrNotFound
	}
	copy := r.runs[len(r.runs)-1]
	return &copy, nil
}

func (r *internalAuditRepository) CountMeetingTreeAuditProviderCalls(_ context.Context, _ string, triggerClass domain.MeetingTreeAuditTriggerClass, since time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.countErr != nil {
		return 0, r.countErr
	}
	count := 0
	for _, run := range r.runs {
		if run.Task == string(aiTaskFinalTreeReview) || !run.ProviderCalled {
			continue
		}
		if triggerClass != "" && run.TriggerClass != triggerClass {
			continue
		}
		if !since.IsZero() && run.CreatedAt.Before(since) {
			continue
		}
		count++
	}
	return count, nil
}

func (r *internalAuditRepository) ApplyMeetingTreeAudit(ctx context.Context, run domain.MeetingTreeAuditRun, expectedVersion int64, analysis domain.MeetingAIAnalysis) (*domain.MeetingAIAnalysis, bool, error) {
	if r.staleCAS || r.analysis.version(analysis.SessionID) != expectedVersion {
		return nil, false, nil
	}
	saved, err := r.analysis.UpsertMeetingAIAnalysis(ctx, analysis)
	if err != nil {
		return nil, false, err
	}
	if err := r.SaveMeetingTreeAuditRun(ctx, run); err != nil {
		return nil, false, err
	}
	return saved, true, nil
}

func (r *internalAuditRepository) latest() *domain.MeetingTreeAuditRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.runs) == 0 {
		return nil
	}
	copy := r.runs[len(r.runs)-1]
	return &copy
}

type internalAuditTranscriptRepository struct{ segments []domain.TranscriptSegment }

func (r internalAuditTranscriptRepository) SaveTranscriptSegment(context.Context, domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error) {
	return domain.TranscriptSegmentStoreResult{}, errors.New("not implemented")
}
func (r internalAuditTranscriptRepository) ListTranscriptSegments(context.Context, string, string, int) ([]domain.TranscriptSegment, error) {
	return append([]domain.TranscriptSegment(nil), r.segments...), nil
}

type internalAuditPublisher struct {
	mu       sync.Mutex
	analyses []domain.MeetingAIAnalysis
}

func (p *internalAuditPublisher) PublishMeetingAIAnalysis(analysis domain.MeetingAIAnalysis) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.analyses = append(p.analyses, analysis)
}
func (p *internalAuditPublisher) snapshot() []domain.MeetingAIAnalysis {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]domain.MeetingAIAnalysis(nil), p.analyses...)
}

type internalAuditCompleter struct {
	mu            sync.Mutex
	content       string
	calls         int
	requests      []AIChatRequest
	block         chan struct{}
	ignoreContext bool
	panicOnCall   bool
}

func (c *internalAuditCompleter) Complete(ctx context.Context, request AIChatRequest) (AIChatResult, error) {
	c.mu.Lock()
	c.calls++
	c.requests = append(c.requests, request)
	block := c.block
	ignoreContext := c.ignoreContext
	panicOnCall := c.panicOnCall
	c.mu.Unlock()
	if panicOnCall {
		panic("tree audit provider panic")
	}
	if block != nil {
		if ignoreContext {
			<-block
		} else {
			select {
			case <-block:
			case <-ctx.Done():
				return AIChatResult{}, ctx.Err()
			}
		}
	}
	return AIChatResult{Content: c.content, Model: "gpt-5-mini", PromptTokens: 100, CompletionTokens: 50}, nil
}
func (c *internalAuditCompleter) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}
func (c *internalAuditCompleter) requestsSnapshot() []AIChatRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]AIChatRequest(nil), c.requests...)
}

func newTreeAuditRunnerFixture(t *testing.T, staleCAS bool) (*MeetingAnalysisService, *internalAuditAnalysisRepository, *internalAuditRepository, *internalAuditPublisher, *internalAuditCompleter, json.RawMessage) {
	t.Helper()
	payload, segments, mc := targetTreeAuditFixture(t)
	analysisRepo := &internalAuditAnalysisRepository{store: map[string]domain.MeetingAIAnalysis{
		"session_26959b9519c5f880": {SessionID: "session_26959b9519c5f880", Type: domain.MeetingAIAnalysisLive, Status: domain.MeetingAIAnalysisCompleted, Version: 12, Payload: payload, SegmentCount: len(segments)},
	}}
	auditRepo := &internalAuditRepository{analysis: analysisRepo, staleCAS: staleCAS}
	publisher := &internalAuditPublisher{}
	completer := &internalAuditCompleter{content: validAuditMoveResponse()}
	service := NewMeetingAnalysisService(analysisRepo, internalAuditTranscriptRepository{segments: segments}, nil, completer, MeetingAnalysisConfig{
		Enabled: true, LiveEnabled: true, Model: "shared", TaskModels: AITaskModels{TreeAudit: "tree-audit-mini", FinalTreeReview: "tree-audit-mini"},
		TreeAudit: TreeAuditConfig{Enabled: true, MinInterval: time.Millisecond, Timeout: time.Second},
	}, publisher)
	service.SetMeetingTreeAuditRepository(auditRepo)
	service.mu.Lock()
	state := service.sessionStateLocked("session_26959b9519c5f880")
	state.context = mc
	state.contextFallback = mc
	state.contextStatus = meetingContextStatusReady
	state.contextVersion = 1
	state.lastPayload = payload
	state.lastVersion = 12
	state.lastActivityAt = service.now()
	service.mu.Unlock()
	return service, analysisRepo, auditRepo, publisher, completer, payload
}

func validAuditMoveResponse() string {
	return `{
  "basedOnTreeVersion":12,
  "summary":"湿地・希少植物itemの親が不整合",
  "findings":[{
    "findingId":"finding-1","type":"subject_mismatch","severity":"high",
    "nodeIds":["item-risk-rare-plants"],"currentParentIds":["candidate-info-public"],
    "relatedNodeIds":["candidate-plant-study"],"evidenceSequenceNos":[22],
    "reason":"植物調査topicが意味的に一致","confidence":0.97
  }],
  "operations":[{
    "operationId":"operation-1","type":"move_item","targetCanonicalItemId":"item-risk-rare-plants",
    "targetCanonicalNodeId":"","targetCanonicalItemIds":[],"targetCandidateId":"",
    "fromParentCanonicalNodeId":"candidate-info-public","toParentCanonicalNodeId":"candidate-plant-study",
    "label":"","reason":"植物調査topicへ戻す",
    "confidence":0.97,"evidenceSequenceNos":[22],"dependsOnOperationIds":[]
  }]
}`
}

func TestTreeAuditPrecheckFindsSemanticStructureRegressions(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{ID: "agenda-1", Title: "申請フォーム改善", Role: agendaRolePrimary}}}
	agendaTopicID := stableAgendaTopicID("agenda-1", 0)
	state := liveAnalysisPayload{TreeVersion: 14, Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
		{ID: treeRootNodeID, Kind: "topic"},
		{ID: agendaTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "申請フォーム改善", Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-1"}, Materialized: true},
		{ID: "group-cause", Kind: "group", ParentID: agendaTopicID, Label: "何が原因でしたか"},
		{ID: "item-cause", Kind: "issue", ParentID: "group-cause", Label: "何が原因でしたか", Subtype: issueSubtypeQuestion},
		{ID: "item-end", Kind: "decision", ParentID: treeUnclassifiedTopicID, Label: "本日これで終了"},
		{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: treeUnclassifiedTopicLabel, Origin: topicOriginSystem},
		{ID: "item-form-todo", Kind: "todo", ParentID: treeUnclassifiedTopicID, Label: "申請フォーム改善案を作成"},
	}}, Items: []liveAnalysisItem{
		{ID: "item-cause", Kind: "issue", Subtype: issueSubtypeQuestion, Title: "何が原因でしたか", Body: "何が原因でしたか", Status: "open", ClassificationStatus: classificationAssigned, EvidenceSequenceNos: []int64{4}},
		{ID: "item-end", Kind: "decision", Title: "本日これで終了", Body: "本日の議事をここで打ち切る決定。", Status: "open", ClassificationStatus: classificationUnclassified, EvidenceSequenceNos: []int64{35}},
		{ID: "item-form-todo", Kind: "todo", Title: "申請フォーム改善案を作成", Body: "山下さんが来週までに作成", Status: "open", ClassificationStatus: classificationAssigned, AssignmentSource: assignmentSourceNoAgendaSpan, EvidenceSequenceNos: []int64{29}},
	}}
	findings := deterministicTreeAuditPrecheck(state, mc, map[int64]treeAuditEvidenceRole{4: treeAuditEvidencePrimary, 29: treeAuditEvidencePrimary, 35: treeAuditEvidencePrimary}, TreeAuditConfig{})
	wants := []TreeAuditFindingType{
		TreeAuditParentChildSameTitle, TreeAuditLowInformationChild,
		TreeAuditGenericQuestionWithoutSubject, TreeAuditMeetingEndAsDecision,
		TreeAuditAgendaItemForcedNoAgenda, TreeAuditAgendaReentryMissed,
		TreeAuditActionSummaryMissingActiveTodos,
	}
	for _, want := range wants {
		found := false
		for _, finding := range findings {
			if finding.Type == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing %s findings=%+v", want, findings)
		}
	}
}

func waitForInternalAudit(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !condition() {
		t.Fatalf("condition not met within %s", timeout)
	}
}
