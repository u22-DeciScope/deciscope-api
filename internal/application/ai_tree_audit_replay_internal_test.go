package application

import (
	"context"
	"embed"
	"encoding/json"
	"testing"
	"time"

	"deciscope-core-api/internal/domain"
)

// session2dee3b1da5b72979Testdata embeds the fixture files at compile time so
// this test never imports "os"/"path/filepath" - internal/application is
// architecturally forbidden from importing external IO packages even in its
// own test files (see internal/architecture/dependency_test.go, "application
// avoids external IO packages").
//
//go:embed testdata/session_2dee3b1da5b72979/*.json
var session2dee3b1da5b72979Testdata embed.FS

// This file replays session_2dee3b1da5b72979 offline: the fixture files under
// testdata/session_2dee3b1da5b72979/ are a real (anonymized) production tree
// snapshot at version 13, its transcript segments, and its meeting context.
// Six real audit runs against this session proposed reasonable moves that
// were all rejected under the pre-D4 design (below_high_confidence_threshold,
// parser_non_canonical_node_id, shadow_only_operation). This test drives the
// full runTreeAudit path (snapshot -> precheck -> fake AI -> parse ->
// canonicalization -> per-operation validation -> apply -> integrity -> CAS
// save -> publish) against that same tree with a fake completer, to confirm
// the current design applies the safe subset of a realistic patch set.
//
// Not every operation a model could plausibly propose against this tree
// clears the general (non-session-specific) safety net: op-3's destination
// (candidate-73edc40ca0ec) is a dynamic topic, not a fixed agenda, so the
// fixed-agenda-return exemption does not apply to it, and its own real
// evidence text's similarity to that destination (0.085) falls just short of
// the halved 0.09 stickiness margin - it is correctly rejected on
// parent_stickiness_margin. This is intentional: the test asserts the actual
// (rejected) outcome rather than forcing it through, and documents why in
// the assertion itself. See the final report for the full breakdown.
//
// op-1 and op-2 (moving the two recurrence-prevention items back to
// agenda-3) clear three independent general gates in sequence: the
// fixed-agenda-return margin/stickiness exemption, and the symmetric
// self-subject-finding exclusion on the heuristic non-worsening gate (both
// items still carry a subject_mismatch/cross_agenda_contamination finding
// against agenda-3's own low-bigram-surface label, or already carried one
// against their old dynamic-topic home before the move - the exclusion
// treats that as the exact ambiguity the exemption already adjudicated,
// while still counting any defect the move introduces on *other* nodes).
//
// The fixture files themselves are read-only inputs and must never be
// modified by this test.

func loadSession2dee3b1da5b72979Fixture(t *testing.T) (json.RawMessage, []domain.TranscriptSegment, *meetingContext) {
	t.Helper()
	const dir = "testdata/session_2dee3b1da5b72979/"
	payload, err := session2dee3b1da5b72979Testdata.ReadFile(dir + "live_payload_v13.json")
	if err != nil {
		t.Fatalf("read live payload fixture: %v", err)
	}
	segmentsRaw, err := session2dee3b1da5b72979Testdata.ReadFile(dir + "segments.json")
	if err != nil {
		t.Fatalf("read segments fixture: %v", err)
	}
	var segments []domain.TranscriptSegment
	if err := json.Unmarshal(segmentsRaw, &segments); err != nil {
		t.Fatalf("parse segments fixture: %v", err)
	}
	for index := range segments {
		segments[index].SessionID = "session_2dee3b1da5b72979"
		segments[index].IsFinal = true
	}
	contextRaw, err := session2dee3b1da5b72979Testdata.ReadFile(dir + "context_payload.json")
	if err != nil {
		t.Fatalf("read context fixture: %v", err)
	}
	var mc meetingContext
	if err := json.Unmarshal(contextRaw, &mc); err != nil {
		t.Fatalf("parse context fixture: %v", err)
	}
	return json.RawMessage(payload), segments, &mc
}

const session2dee3b1da5b72979ID = "session_2dee3b1da5b72979"

func newSession2dee3b1da5b72979RunnerFixture(t *testing.T, responseContent string) (*MeetingAnalysisService, *internalAuditAnalysisRepository, *internalAuditRepository, *internalAuditPublisher, *internalAuditCompleter, json.RawMessage) {
	t.Helper()
	payload, segments, mc := loadSession2dee3b1da5b72979Fixture(t)
	analysisRepo := &internalAuditAnalysisRepository{store: map[string]domain.MeetingAIAnalysis{
		session2dee3b1da5b72979ID: {SessionID: session2dee3b1da5b72979ID, Type: domain.MeetingAIAnalysisLive, Status: domain.MeetingAIAnalysisCompleted, Version: 13, Payload: payload, SegmentCount: len(segments)},
	}}
	auditRepo := &internalAuditRepository{analysis: analysisRepo}
	publisher := &internalAuditPublisher{}
	completer := &internalAuditCompleter{content: responseContent}
	service := NewMeetingAnalysisService(analysisRepo, internalAuditTranscriptRepository{segments: segments}, nil, completer, MeetingAnalysisConfig{
		Enabled: true, LiveEnabled: true, Model: "shared", TaskModels: AITaskModels{TreeAudit: "tree-audit-mini", FinalTreeReview: "tree-audit-mini"},
		TreeAudit: TreeAuditConfig{Enabled: true, MinInterval: time.Millisecond, Timeout: time.Second},
	}, publisher)
	service.SetMeetingTreeAuditRepository(auditRepo)
	service.mu.Lock()
	state := service.sessionStateLocked(session2dee3b1da5b72979ID)
	state.context = mc
	state.contextFallback = mc
	state.contextStatus = meetingContextStatusReady
	state.contextVersion = 1
	state.lastPayload = payload
	state.lastVersion = 13
	state.lastActivityAt = service.now()
	service.mu.Unlock()
	return service, analysisRepo, auditRepo, publisher, completer, payload
}

// session2dee3b1da5b72979AuditResponse builds a realistic v3 audit response
// for the fixture tree at version 13. The operations mirror the actual
// GPT-5-mini deployment's real proposals across this session's audit runs:
// moving the two agenda-3/recurrence-prevention items out of the misplaced
// dynamic topic (op-1, op-2, exercising the fixed-agenda-return margin
// exemption and the matching symmetric exclusion on the heuristic
// non-worsening gate; once both leave, candidate-e781af10c938 is left empty
// and the empty-container cascade removes it too), attempting to move the
// VLAN item into an existing dynamic topic (op-3, correctly rejected - see
// the file header comment), retiring the single-round recap-derived
// candidate (op-4), flattening the redundant VPN group by moving both of its
// items out (op-5, op-6, exercising both the redundant-group-flatten
// exemption and the empty-container cascade that removes
// group-a7fab20c0cd1 once it is empty - no explicit remove_empty_group
// operation is needed for either container), deactivating a
// discourse-flavored open issue (op-8), and one operation intentionally left
// at low, unsupported confidence (op-9) to prove the effective-confidence
// gate still rejects it.
func session2dee3b1da5b72979AuditResponse() string {
	response := treeAuditResponse{
		BasedOnTreeVersion: 13,
		Summary:            "再発防止策の未整理項目、VPN対応の冗長group階層、単発recap由来candidateの整理",
		Findings: []treeAuditFinding{
			{
				FindingID: "finding-1", Type: TreeAuditMissingRequiredTopic, Severity: "high",
				NodeIDs: []string{"item-decision-b9335ac1c4af", "item-todo-605e737781ec"}, CurrentParentIDs: []string{"candidate-e781af10c938"},
				RelatedNodeIDs: []string{"agenda-3"}, EvidenceSequenceNos: []int64{13, 14, 15},
				Reason: "再発防止策(agenda-3)が空のまま、内容が別のdynamic topicに残っている", Confidence: 0.9,
			},
			{
				FindingID: "finding-2", Type: TreeAuditGroupOutlier, Severity: "medium",
				NodeIDs: []string{"group-a7fab20c0cd1"}, CurrentParentIDs: []string{"candidate-58e094611bf0"},
				RelatedNodeIDs: []string{"candidate-58e094611bf0"}, EvidenceSequenceNos: []int64{21, 23, 24},
				Reason: "groupが親topicと同主題で冗長な階層になっている", Confidence: 0.9,
			},
		},
		Operations: []treeAuditOperation{
			{
				OperationID: "op-1", Type: TreeAuditMoveItem,
				TargetCanonicalItemID: "item-decision-b9335ac1c4af", FromParentCanonicalNodeID: "candidate-e781af10c938",
				ToParentCanonicalNodeID: "agenda-3", Confidence: 0.85,
				Reason: "次回機器交換のダブルチェック決定は再発防止策そのものであるためagenda-3へ復帰", EvidenceSequenceNos: []int64{15},
			},
			{
				OperationID: "op-2", Type: TreeAuditMoveItem,
				TargetCanonicalItemID: "item-todo-605e737781ec", FromParentCanonicalNodeID: "candidate-e781af10c938",
				ToParentCanonicalNodeID: "agenda-3", Confidence: 0.85,
				Reason: "VLANごとの疎通確認チェックリスト作成は再発防止策そのものであるためagenda-3へ復帰", EvidenceSequenceNos: []int64{13},
			},
			{
				OperationID: "op-3", Type: TreeAuditMoveItem,
				TargetCanonicalItemID: "item-todo-7f68ebf40ed9", FromParentCanonicalNodeID: treeUnclassifiedTopicID,
				ToParentCanonicalNodeID: "candidate-73edc40ca0ec", Confidence: 0.85,
				Reason: "vlan30設定漏れの検証は既存の技術的原因の特定topicへ統合すべき", EvidenceSequenceNos: []int64{26},
			},
			{
				OperationID: "op-4", Type: TreeAuditDeactivateCandidate,
				TargetCandidateID: "candidate-ed1e9e672609", Confidence: 0.9,
				Reason: "単発recap由来で既存topicと重複するcandidateのため非活性化",
			},
			{
				OperationID: "op-5", Type: TreeAuditMoveItem,
				TargetCanonicalItemID: "item-todo-411dc20edd85", FromParentCanonicalNodeID: "group-a7fab20c0cd1",
				ToParentCanonicalNodeID: "candidate-58e094611bf0", Confidence: 0.9,
				Reason: "VPN証明書対応groupは親topicと同主題の冗長階層のため平坦化", EvidenceSequenceNos: []int64{21},
			},
			{
				OperationID: "op-6", Type: TreeAuditMoveItem,
				TargetCanonicalItemID: "item-todo-e06241373431", FromParentCanonicalNodeID: "group-a7fab20c0cd1",
				ToParentCanonicalNodeID: "candidate-58e094611bf0", Confidence: 0.9,
				Reason: "VPN証明書対応groupは親topicと同主題の冗長階層のため平坦化", EvidenceSequenceNos: []int64{23},
			},
			{
				OperationID: "op-8", Type: TreeAuditDeactivateItem,
				TargetCanonicalItemID: "open-issue-auto-dde3edac015b", Confidence: 0.9,
				Reason: "未確定事項として残す旨のみを述べたrecap/discourse-only由来のitem",
			},
			{
				OperationID: "op-9", Type: TreeAuditMoveItem,
				TargetCanonicalItemID: "item-fact-6a4e61602240", FromParentCanonicalNodeID: treeUnclassifiedTopicID,
				ToParentCanonicalNodeID: "agenda-2", Confidence: 0.65,
				Reason: "ルーター/FW異常なしの確認結果を原因調査agendaへ", EvidenceSequenceNos: []int64{5},
			},
		},
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func TestSession2dee3b1da5b72979OfflineReplayAppliesSafeOperationsAndRejectsWeakOnes(t *testing.T) {
	service, analysisRepo, auditRepo, publisher, completer, payload := newSession2dee3b1da5b72979RunnerFixture(t, session2dee3b1da5b72979AuditResponse())
	execution, err := service.runTreeAudit(context.Background(), session2dee3b1da5b72979ID, "manual_replay", aiTaskTreeAudit, payload, 13, false)
	if err != nil {
		t.Fatalf("runTreeAudit() error = %v", err)
	}
	if completer.callCount() != 1 {
		t.Fatalf("provider calls = %d, want 1", completer.callCount())
	}

	run := auditRepo.latest()
	if run == nil {
		t.Fatal("no audit run persisted")
	}
	var validator treeAuditValidatorResult
	if err := json.Unmarshal(run.ValidatorResult, &validator); err != nil {
		t.Fatalf("unmarshal validator result: %v", err)
	}
	byOp := make(map[string]treeAuditValidatorEvaluation, len(validator.Evaluations))
	for _, evaluation := range validator.Evaluations {
		byOp[evaluation.OperationID] = evaluation
		t.Logf("operation=%s type=%s result=%s reason=%s category=%s modelConfidence=%.2f effectiveConfidence=%.2f currentScore=%.3f newScore=%.3f",
			evaluation.OperationID, evaluation.Type, evaluation.Result, evaluation.Reason, evaluation.Category,
			evaluation.ModelConfidence, evaluation.EffectiveConfidence, evaluation.CurrentParentScore, evaluation.NewParentScore)
	}

	// --- overall run shape (design brief D5/14.5) ---
	if execution.Result != "partial_success" {
		t.Fatalf("execution.Result = %q, want partial_success (5 applied, 3 rejected)", execution.Result)
	}
	if execution.Version != 14 || !execution.Applied {
		t.Fatalf("execution = %+v, want version=14 applied=true", execution)
	}
	if !validator.TreeIntegrityValid {
		t.Fatalf("treeIntegrityValid = false: validator=%+v", validator)
	}
	if run.ResultingTreeVersion != 14 {
		t.Fatalf("run.ResultingTreeVersion = %d, want 14", run.ResultingTreeVersion)
	}
	if validator.OperationsProposed != 8 || validator.OperationsCanonicalized != 8 {
		t.Fatalf("validator proposed/canonicalized = %d/%d, want 8/8", validator.OperationsProposed, validator.OperationsCanonicalized)
	}
	if validator.OperationsApplied != 5 || validator.OperationsRejected != 3 {
		t.Fatalf("validator applied/rejected = %d/%d, want 5/3: %+v", validator.OperationsApplied, validator.OperationsRejected, validator)
	}

	// --- applied operations ---
	//
	// op-1/op-2: both clear the fixed-agenda-return margin/stickiness
	// exemption (candidate-e781af10c938 has no fixed-agenda ancestor, its
	// cohesion with either item is below CohesionThreshold, and agenda-3
	// does have a fixed-agenda ancestor), and then also clear the heuristic
	// non-worsening gate via its symmetric self-subject-finding exclusion:
	// each item still carries (or already carried, before the move) a
	// subject_mismatch/cross_agenda_contamination finding naming only
	// itself, which is excluded from both the before- and after-state defect
	// counts precisely because the exemption already made this placement's
	// structural/confidence judgment call - the plain, unfiltered defect
	// count actually goes up (13 -> 15, see HeuristicDefectCountBefore/After
	// above) purely from this self-referential doubling, not from any new
	// problem on another node.
	//
	// op-4/op-5/op-6: unchanged from the previous round - op-4 is the
	// unconditional single-round candidate deactivation, and op-5/op-6 are
	// both VPN items flattened out of their redundant group via the
	// redundant-group-flatten margin exemption (op-5's own currentScore
	// 0.174 is actually higher than its newScore 0.122, and op-6's 0.214
	// higher than its 0.111, so only the exemption lets either through),
	// with the empty-container cascade then pruning group-a7fab20c0cd1 once
	// op-6 leaves it with zero children.
	for _, id := range []string{"op-1", "op-2", "op-4", "op-5", "op-6"} {
		if !byOp[id].Valid || !byOp[id].Applied {
			t.Fatalf("%s must be applied: %+v", id, byOp[id])
		}
	}
	if got := byOp["op-1"].EffectiveConfidence; got < 0.90 {
		t.Fatalf("op-1 effectiveConfidence = %v, want >= 0.90 (the fixed-agenda-return exemption waives the margin/sticky/non-worsening checks, not the confidence gate itself)", got)
	}
	if got := byOp["op-2"].EffectiveConfidence; got < 0.90 {
		t.Fatalf("op-2 effectiveConfidence = %v, want >= 0.90", got)
	}
	if got := byOp["op-3"].Reason; got != "parent_stickiness_margin" {
		t.Fatalf("op-3 reason = %q, want parent_stickiness_margin (destination candidate-73edc40ca0ec is a dynamic topic, not a fixed agenda, so the fixed-agenda-return exemption does not apply, and similarity 0.085 falls just short of the halved 0.09 margin)", got)
	}
	if got := byOp["op-8"].Reason; got != "deactivate_grounds_not_verified" {
		t.Fatalf("op-8 reason = %q, want deactivate_grounds_not_verified (this open_issue's text does not meet the narrow isDiscourseOnlyItem definition)", got)
	}
	if got := byOp["op-9"].Reason; got != "below_effective_confidence_threshold" && got != "parent_stickiness_margin" {
		t.Fatalf("op-9 reason = %q, want below_effective_confidence_threshold or parent_stickiness_margin (must never pass at modelConfidence 0.65 with no structural corroboration)", got)
	}

	if got := analysisRepo.version(session2dee3b1da5b72979ID); got != 14 {
		t.Fatalf("live version = %d, want 14", got)
	}
	if len(publisher.snapshot()) != 1 || publisher.snapshot()[0].Version != 14 {
		t.Fatalf("publisher snapshot = %+v, want exactly one publish at version 14", publisher.snapshot())
	}

	var state liveAnalysisPayload
	if err := json.Unmarshal(analysisRepo.store[session2dee3b1da5b72979ID].Payload, &state); err != nil {
		t.Fatalf("unmarshal applied payload: %v", err)
	}

	assertNodeParent := func(nodeID, wantParent string) {
		t.Helper()
		node := treeNodeByID(state.Tree, nodeID)
		if node == nil {
			t.Fatalf("node %s missing after replay", nodeID)
		}
		if node.ParentID != wantParent {
			t.Fatalf("node %s parent = %q, want %q", nodeID, node.ParentID, wantParent)
		}
	}
	// VPN group flatten: op-5 and op-6 both applied, so both items now sit
	// directly under the topic, and the empty-container cascade pruned
	// group-a7fab20c0cd1 once op-6 left it with zero children - no explicit
	// remove_empty_group operation was needed.
	assertNodeParent("item-todo-411dc20edd85", "candidate-58e094611bf0")
	assertNodeParent("item-todo-e06241373431", "candidate-58e094611bf0")
	if node := treeNodeByID(state.Tree, "group-a7fab20c0cd1"); node != nil {
		t.Fatalf("group-a7fab20c0cd1 must be cascade-pruned once both its items relocate: %+v", node)
	}

	// candidate-ed1e9e672609 is deactivated (op-4 applied) and, since no
	// create_topic_from_candidate operation ever targeted it, was never
	// promoted to a tree node either way.
	foundCandidate := false
	for _, candidate := range state.EmergingTopics {
		if candidate.ID == "candidate-ed1e9e672609" {
			foundCandidate = true
			if !candidate.Inactive {
				t.Fatalf("candidate-ed1e9e672609 must be inactive: %+v", candidate)
			}
		}
	}
	if !foundCandidate {
		t.Fatal("candidate-ed1e9e672609 unexpectedly removed from EmergingTopics tracking")
	}
	if node := treeNodeByID(state.Tree, "candidate-ed1e9e672609"); node != nil {
		t.Fatalf("candidate-ed1e9e672609 must never be promoted to a tree node: %+v", node)
	}

	// op-1/op-2 both applied, so agenda-3 (re-prevention策) now holds both
	// recurrence-prevention items, and candidate-e781af10c938 - left with
	// zero children - was cascade-pruned in the same pass rather than
	// lingering as an orphaned, now-meaningless dynamic topic.
	assertNodeParent("item-decision-b9335ac1c4af", "agenda-3")
	assertNodeParent("item-todo-605e737781ec", "agenda-3")
	if node := treeNodeByID(state.Tree, "candidate-e781af10c938"); node != nil {
		t.Fatalf("candidate-e781af10c938 must be cascade-pruned once both its items relocate: %+v", node)
	}
	agenda3Children := 0
	for _, node := range state.Tree.Nodes {
		if node.ParentID == "agenda-3" {
			agenda3Children++
		}
	}
	if agenda3Children != 2 {
		t.Fatalf("agenda-3 children = %d, want 2 (item-decision-b9335ac1c4af, item-todo-605e737781ec)", agenda3Children)
	}

	// open-issue-auto-dde3edac015b was rejected (op-8), so it remains in the
	// tree under topic-unclassified.
	assertNodeParent("open-issue-auto-dde3edac015b", treeUnclassifiedTopicID)

	// item-fact-6a4e61602240 (op-9) was rejected, so it stays put too.
	assertNodeParent("item-fact-6a4e61602240", treeUnclassifiedTopicID)

	t.Logf("session_2dee3b1da5b72979 replay: result=%s operationsApplied=%d operationsRejected=%d resultingVersion=%d",
		execution.Result, validator.OperationsApplied, validator.OperationsRejected, run.ResultingTreeVersion)
}
