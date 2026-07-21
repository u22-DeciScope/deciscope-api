package application

import (
	"encoding/json"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

// session7e10430eFixture reproduces session_7e10430ec0ac3b82's final state
// (名古屋支社ネットワーク障害の振り返りと再発防止会議, same background as
// session_125e3cc5): two tree-audit runs (tree-audit_d6f48f3a1d184685 and the
// final review tree-audit_3eb8b8f7b31343f2) both proposed the correct repair
// move_item: item-decision-edf1c3a94148 (evidence[14], the "ダブルチェックを
// 必須にします" decision) -> topic-agenda-7dd3ab9e5ea9 (materialized agenda-3,
// "再発防止策"), confidence 0.9, but both were wrongly rejected as
// reference_evidence_only. The item and its "アラート過多" risk sibling
// (item-risk-7ea1c7ce45cc, evidence[19,20]) are therefore stuck
// classificationStatus=tentative/candidateInactive: the tree hides them
// (stageTentativeTree, deciscope-web) while the AI assistant card list still
// shows them (kind-only filter, no classificationStatus check) -- both are
// correct per their own frontend contract, but the tentative item never
// getting reparented is the underlying real bug (H1).
//
// The other 13-ish assigned items from the real session are simplified to
// just the two that matter for this replay's assertions: the sibling
// checklist-adoption decision (decision-auto-e49ac1564676) and the checklist
// creation TODO (item-todo-c85e17da0410), both already correctly assigned
// under topic-agenda-7dd3ab9e5ea9, plus one other assigned risk
// (item-risk-vpn-cert) so the final decision/risk totals are exactly 2 each.
func session7e10430eFixture(t *testing.T) (json.RawMessage, []domain.TranscriptSegment, *meetingContext) {
	t.Helper()
	mc := &meetingContext{
		Title: "名古屋支社ネットワーク障害の振り返りと再発防止会議",
		Agenda: []agendaItem{
			{ID: "agenda-1", Title: "障害の影響範囲と発生時刻", Order: 1, Role: agendaRolePrimary},
			{ID: "agenda-2", Title: "原因調査と復旧対応", Order: 2, Role: agendaRolePrimary},
			{ID: "agenda-3", Title: "再発防止策", Order: 3, Role: agendaRolePrimary},
			{ID: "agenda-4", Title: "未解決事項と次回対応確認", Order: 4, Role: agendaRoleActionSummary},
		},
	}
	agenda3TopicID := stableAgendaTopicID("agenda-3", 0) // == "topic-agenda-7dd3ab9e5ea9"

	seq14Text := "まず、ネットワーク機器を交換する際は、作業者とは別の担当者が設定内容を確認するダブルチェックを必須にします。また、交換前後でブイランごとの疎通確認を実施するチェックリストを作成します。"
	seq19Text := "ただし、監視対象を増やすとアラートが多くなりすぎる問題があります。"
	seq20Text := "監視感覚と通知条件については、次回までに検討が必要です。"

	state := liveAnalysisPayload{
		Summary: "名古屋支社ネットワーク障害の振り返り", TreeVersion: 20, CoveredThroughSequenceNo: 29,
		Items: []liveAnalysisItem{
			// assigned: 既に正しく再発防止策topicへ配置済みの兄弟decision。
			{ID: "decision-auto-e49ac1564676", Kind: "decision", Title: "チェックリストの運用を次回から適用", Body: "この運用を、次回の危機交換から適用することにします。", Status: "open",
				ClassificationStatus: classificationAssigned, AssignmentConfidence: .95, EvidenceSequenceNos: []int64{15}, EvidenceRoles: []liveEvidenceRoleRef{{SequenceNo: 15, Role: liveEvidencePrimary}}},
			// assigned: 正しく配置済みのチェックリスト作成TODO。
			{ID: "item-todo-c85e17da0410", Kind: "todo", Title: "スイッチ交換用チェックリスト案の作成", Body: "交換前後でブイランごとの疎通確認を実施するチェックリストを作成する", Status: "open",
				ClassificationStatus: classificationAssigned, AssignmentConfidence: .9, EvidenceSequenceNos: []int64{14}, EvidenceRoles: []liveEvidenceRoleRef{{SequenceNo: 14, Role: liveEvidencePrimary}}},
			// assigned: risk合計を2件にするための無関係な既存risk。
			{ID: "item-risk-vpn-cert", Kind: "risk", Title: "VPN証明書期限切れリスク", Body: "VPN装置の証明書が来月末に期限切れになる", Status: "open",
				ClassificationStatus: classificationAssigned, AssignmentConfidence: .9, EvidenceSequenceNos: []int64{16}, EvidenceRoles: []liveEvidenceRoleRef{{SequenceNo: 16, Role: liveEvidencePrimary}}},
			// tentative/candidateInactive: 監査move_itemが繰り返し誤拒否されている対象decision。
			{ID: "item-decision-edf1c3a94148", Kind: "decision", Title: "ダブルチェックを必須にします", Body: seq14Text, Status: "open",
				ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-doublecheck-stale", CandidateInactive: true, AssignmentConfidence: .6,
				EvidenceSequenceNos: []int64{14}, EvidenceRoles: []liveEvidenceRoleRef{{SequenceNo: 14, Role: liveEvidencePrimary}}},
			// tentative/candidateInactive: 同様に取り残されているrisk。
			{ID: "item-risk-7ea1c7ce45cc", Kind: "risk", Title: "監視アラート過多のリスク", Body: seq19Text + seq20Text, Status: "open",
				ClassificationStatus: classificationTentative, CandidateTopicID: "candidate-alert-stale", CandidateInactive: true, AssignmentConfidence: .6,
				EvidenceSequenceNos: []int64{19, 20}, EvidenceRoles: []liveEvidenceRoleRef{{SequenceNo: 19, Role: liveEvidencePrimary}, {SequenceNo: 20, Role: liveEvidencePrimary}}},
		},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "名古屋支社ネットワーク障害の振り返りと再発防止会議", Origin: topicOriginSystem},
			{ID: stableAgendaTopicID("agenda-1", 0), Kind: "topic", ParentID: treeRootNodeID, Label: "障害の影響範囲と発生時刻", Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary, AgendaRefs: []string{"agenda-1"}, Materialized: true},
			{ID: stableAgendaTopicID("agenda-2", 0), Kind: "topic", ParentID: treeRootNodeID, Label: "原因調査と復旧対応", Origin: topicOriginAgenda, AgendaRole: agendaRolePrimary, AgendaRefs: []string{"agenda-2"}, Materialized: true},
			{ID: agenda3TopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "再発防止策",
				Description: "ネットワーク機器交換時の設定確認・ダブルチェックとチェックリスト運用による再発防止",
				Origin:      topicOriginAgenda, AgendaRole: agendaRolePrimary, AgendaRefs: []string{"agenda-3"}, Materialized: true},
			{ID: "candidate-vpn-cert", Kind: "topic", ParentID: treeRootNodeID, Label: "VPN証明書期限切れ対策", Origin: topicOriginDynamic},
			{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "追加論点", Origin: topicOriginSystem},
			{ID: "decision-auto-e49ac1564676", Kind: "decision", ParentID: agenda3TopicID, Label: "チェックリストの運用を次回から適用", Status: "open"},
			{ID: "item-todo-c85e17da0410", Kind: "todo", ParentID: agenda3TopicID, Label: "スイッチ交換用チェックリスト案の作成", Status: "open"},
			{ID: "item-risk-vpn-cert", Kind: "risk", ParentID: "candidate-vpn-cert", Label: "VPN証明書期限切れリスク", Status: "open"},
			// tentativeな2itemはstageTentativeTreeの前提どおりtreeUnclassifiedTopicID配下。
			{ID: "item-decision-edf1c3a94148", Kind: "decision", ParentID: treeUnclassifiedTopicID, Label: "ダブルチェックを必須にします", Status: "open"},
			{ID: "item-risk-7ea1c7ce45cc", Kind: "risk", ParentID: treeUnclassifiedTopicID, Label: "監視アラート過多のリスク", Status: "open"},
		}},
		EmergingTopics: []emergingTopicCandidate{
			{ID: "candidate-doublecheck-stale", Label: "ダブルチェック運用の候補話題", Description: "四半期以上更新のない失効候補", EvidenceItemIDs: []string{"item-decision-edf1c3a94148"}, FirstRound: 15, LastRound: 16, RoundCount: 1, Inactive: true},
			{ID: "candidate-alert-stale", Label: "監視アラート運用の候補話題", Description: "四半期以上更新のない失効候補", EvidenceItemIDs: []string{"item-risk-7ea1c7ce45cc"}, FirstRound: 19, LastRound: 20, RoundCount: 1, Inactive: true},
		},
	}
	rebuildTreeAuditEdges(state.Tree)
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	texts := map[int64]string{
		13: "今後の対応についてです。",
		14: seq14Text,
		15: "この運用を、次回の危機交換から適用することにします。",
		19: seq19Text,
		20: seq20Text,
		27: "再発防止として、設定のダブルチェックとvランごとの疎通確認オフィスにします。",
		28: "私は今週金曜日までにチェックリストを作成し、佐藤さんは来週火曜日までに標準設定との差分を確認します。",
		29: "2階の通信遅延の原因と監視アラートの条件は、未解決事項として残します。",
	}
	sequenceNos := []int64{13, 14, 15, 19, 20, 27, 28, 29}
	segments := make([]domain.TranscriptSegment, 0, len(sequenceNos))
	for _, sequenceNo := range sequenceNos {
		segments = append(segments, domain.TranscriptSegment{SessionID: "session_7e10430ec0ac3b82", CallID: "call-1", SequenceNo: sequenceNo, SpeakerName: "山田", Text: texts[sequenceNo], IsFinal: true})
	}
	return encoded, segments, mc
}

// countActiveCanonicalDuplicates counts, among active (not inactive, not
// merged) items, how many exceed one occurrence per (kind, canonical
// semantic key) pair -- i.e. genuine duplicate propositions rather than the
// single canonical item each subject should have.
func countActiveCanonicalDuplicates(items []liveAnalysisItem) int {
	seen := make(map[string]int, len(items))
	for _, item := range items {
		if item.Inactive || item.MergedIntoID != "" {
			continue
		}
		key := item.Kind + "|" + semanticItemKey(item.Title+" "+item.Body)
		seen[key]++
	}
	duplicates := 0
	for _, count := range seen {
		if count > 1 {
			duplicates += count - 1
		}
	}
	return duplicates
}

func countActiveItemsMatching(items []liveAnalysisItem, kind, substring string) int {
	count := 0
	for _, item := range items {
		if item.Inactive || item.MergedIntoID != "" || item.Kind != kind {
			continue
		}
		if strings.Contains(item.Title+item.Body, substring) {
			count++
		}
	}
	return count
}

// TestSession7e10430eReplayAppliesPreviouslyRejectedMoveItem is the offline
// replay for session_7e10430ec0ac3b82. It reproduces H1: the exact move_item
// operation both real audit runs proposed (target
// item-decision-edf1c3a94148, evidence[14], confidence 0.9,
// topic-agenda-7dd3ab9e5ea9) must now be VALID and APPLIED. Before H1, this
// same operation was rejected with reason "reference_evidence_only" because
// classifyTreeAuditEvidence's looksLikeTreeAuditReference heuristic
// downgraded sequence 14 to "reference": it contains 確認 twice
// (statusReview) and closely resembles both the very decision item it
// produced and a label-derived topic (matchedItems/matchedTopics), even
// though the deterministic discourse timeline and the item's own persisted
// EvidenceRoles both already recorded it primary.
func TestSession7e10430eReplayAppliesPreviouslyRejectedMoveItem(t *testing.T) {
	payload, segments, mc := session7e10430eFixture(t)
	state := previousLiveAnalysisState(payload)
	agenda3TopicID := stableAgendaTopicID("agenda-3", 0)

	// 移動前のH2メトリクス整合性: tentative2件はいずれもtree非表示(tentative
	// item数)、かつAI assistantカードはkindのみでフィルタするため両方表示対象
	// (decision/risk、status!=resolved)。
	beforeStats := countLiveAnalysisPayloadStats(payload)
	if beforeStats.TentativeItems != 2 {
		t.Fatalf("beforeStats.TentativeItems = %d, want 2", beforeStats.TentativeItems)
	}
	if beforeStats.AssistantVisibleTentativeItems != 2 {
		t.Fatalf("beforeStats.AssistantVisibleTentativeItems = %d, want 2", beforeStats.AssistantVisibleTentativeItems)
	}

	roles := classifyTreeAuditEvidence(state, segments)
	if roles[14] != treeAuditEvidencePrimary {
		t.Fatalf("roles[14] = %q, want primary (H1 prerequisite for the move_item repair to be accepted)", roles[14])
	}

	// 実監査run(tree-audit_d6f48f3a1d184685, tree-audit_3eb8b8f7b31343f2)が
	// 繰り返し提案し、いずれもreference_evidence_onlyで誤拒否された修復op。
	operation := treeAuditOperation{
		OperationID: "op-move-doublecheck", Type: TreeAuditMoveItem,
		TargetCanonicalItemID:     "item-decision-edf1c3a94148",
		FromParentCanonicalNodeID: treeUnclassifiedTopicID,
		ToParentCanonicalNodeID:   agenda3TopicID,
		Confidence:                0.9,
		Reason:                    "ダブルチェックdecisionは再発防止策アジェンダの内容",
		EvidenceSequenceNos:       []int64{14},
	}
	dry, result := validateAndDryRunTreeAuditOperations(state, []treeAuditOperation{operation}, segments, mc, roles, TreeAuditConfig{}, "audit-7e10430e", 21, true)
	if result.OperationsValid != 1 || result.OperationsApplied != 1 {
		var reason string
		for _, evaluation := range result.Evaluations {
			if evaluation.OperationID == operation.OperationID {
				reason = evaluation.Reason
				t.Logf("evaluation = %+v", evaluation)
			}
		}
		t.Fatalf("validator result = %+v, want the move_item applied (reason=%q); before H1 this was rejected as reference_evidence_only", result, reason)
	}
	if !result.TreeIntegrityValid {
		t.Fatalf("treeIntegrityValid = %t, want true", result.TreeIntegrityValid)
	}

	// 適用後: 対象decisionはtopic-agenda-7dd3ab9e5ea9配下、classificationStatus
	// はassigned(move_item applierが自動でtentative離脱させる、既存の意味論)。
	movedNode := treeNodeByID(dry.Tree, "item-decision-edf1c3a94148")
	if movedNode == nil || movedNode.ParentID != agenda3TopicID {
		t.Fatalf("moved node = %+v, want parent %q", movedNode, agenda3TopicID)
	}
	movedItem := findItemByID(dry.Items, "item-decision-edf1c3a94148")
	if movedItem == nil || movedItem.ClassificationStatus != classificationAssigned || movedItem.CandidateTopicID != "" || movedItem.CandidateInactive {
		t.Fatalf("moved item = %+v, want classificationStatus=assigned, candidateTopicId cleared, candidateInactive=false", movedItem)
	}

	// 全体検証: ダブルチェックdecisionとアラート過多riskは各1件のみ、重複無し、
	// unclassified無し、整合性valid、decision/risk合計は各2件のまま。
	duplicateVisibleDecisions := countActiveItemsMatching(dry.Items, "decision", "ダブルチェック") - 1
	duplicateVisibleRisks := countActiveItemsMatching(dry.Items, "risk", "アラート") - 1
	duplicateCanonicalItems := countActiveCanonicalDuplicates(dry.Items)
	if duplicateVisibleDecisions != 0 {
		t.Fatalf("duplicateVisibleDecisions = %d, want 0", duplicateVisibleDecisions)
	}
	if duplicateVisibleRisks != 0 {
		t.Fatalf("duplicateVisibleRisks = %d, want 0", duplicateVisibleRisks)
	}
	if duplicateCanonicalItems != 0 {
		t.Fatalf("duplicateCanonicalItems = %d, want 0", duplicateCanonicalItems)
	}

	afterEncoded, err := json.Marshal(dry)
	if err != nil {
		t.Fatal(err)
	}
	afterStats := countLiveAnalysisPayloadStats(afterEncoded)
	if afterStats.UnclassifiedItems != 0 {
		t.Fatalf("trueUnclassifiedItems = %d, want 0", afterStats.UnclassifiedItems)
	}
	integrity := validateTreeIntegrity(dry.Tree, dry.Items, mc, dry.AgendaAnchors)
	if !integrity.Valid {
		t.Fatalf("integrity = %+v, want valid", integrity)
	}
	decisionCount, riskCount := 0, 0
	for _, item := range dry.Items {
		if item.Inactive || item.MergedIntoID != "" {
			continue
		}
		switch item.Kind {
		case "decision":
			decisionCount++
		case "risk":
			riskCount++
		}
	}
	if decisionCount != 2 {
		t.Fatalf("decisionCount = %d, want 2", decisionCount)
	}
	if riskCount != 2 {
		t.Fatalf("riskCount = %d, want 2", riskCount)
	}

	// 移動後のH2メトリクス: 移動したdecision分だけ1ずつ減る
	// (残るtentativeはitem-risk-7ea1c7ce45ccのみ)。
	if afterStats.TentativeItems != 1 {
		t.Fatalf("afterStats.TentativeItems = %d, want 1 (decision resolved out of tentative)", afterStats.TentativeItems)
	}
	if afterStats.AssistantVisibleTentativeItems != 1 {
		t.Fatalf("afterStats.AssistantVisibleTentativeItems = %d, want 1", afterStats.AssistantVisibleTentativeItems)
	}

	t.Logf("session_7e10430e replay summary: decisionCount=%d riskCount=%d duplicateVisibleDecisions=%d duplicateVisibleRisks=%d duplicateCanonicalItems=%d trueUnclassifiedItems=%d treeIntegrityValid=%t beforeTreeHiddenTentativeItems=%d beforeAssistantVisibleTentativeItems=%d afterTreeHiddenTentativeItems=%d afterAssistantVisibleTentativeItems=%d",
		decisionCount, riskCount, duplicateVisibleDecisions, duplicateVisibleRisks, duplicateCanonicalItems, afterStats.UnclassifiedItems, integrity.Valid, beforeStats.TentativeItems, beforeStats.AssistantVisibleTentativeItems, afterStats.TentativeItems, afterStats.AssistantVisibleTentativeItems)
}
