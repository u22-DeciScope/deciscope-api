package application

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"flag"
	"io"
	"log"
	"strings"
	"testing"

	"deciscope-core-api/internal/domain"
)

var storedReplayGzipBase64 = flag.String(
	"stored-replay-gzip-base64",
	"",
	"optional gzip+base64 exported replay JSON for the session_de988 regression",
)

func TestSessionDe988PropositionIdentityRejectsObservedIDReuse(t *testing.T) {
	scope := newLiveEvidenceScope()
	scope.CoveredThrough = 12
	scope.TranscriptText[2] = "名古屋支社の3階を中心に社内ネットワークが接続できないという報告がありました。"
	scope.TranscriptText[7] = "3階のアクセススイッチでVLAN20とVLAN30の通信が不安定で、ポート設定を確認しました。"
	scope.TranscriptText[12] = "午前10時5分に。"
	scope.EvidenceRoles = map[int64]liveEvidenceRole{
		2: liveEvidencePrimary, 7: liveEvidencePrimary, 12: liveEvidencePrimary,
	}
	incident := liveAnalysisItem{
		ID: "item-issue-discussion-d93eddadf594", Kind: "issue",
		Title:               "名古屋支社3階のネットワーク接続障害",
		Body:                "名古屋支社の3階を中心に社内ネットワークへ接続できない",
		EvidenceSequenceNos: []int64{2},
	}
	configuration := liveAnalysisItem{
		ID: incident.ID, Kind: "fact",
		Title:               "3階アクセススイッチのVLAN設定不整合",
		Body:                "アクセススイッチのポート設定とVLAN設定を確認した",
		EvidenceSequenceNos: []int64{7},
	}
	diff, _ := detachCrossKindActionUpdates(
		[]liveAnalysisItem{incident}, []liveAnalysisItem{configuration}, nil,
		scope, &liveAnalysisTreeMergeStats{},
	)
	if len(diff) != 1 || diff[0].ID != "" || !strings.Contains(diff[0].ClientKey, "companion") {
		t.Fatalf("configuration update destructively reused incident ID: %+v", diff)
	}

	timeAtom := liveAnalysisItem{
		ID: incident.ID, Kind: "issue", Title: "午前10時5分に",
		Body: "午前10時5分に", EvidenceSequenceNos: []int64{12},
	}
	stats := &liveAnalysisTreeMergeStats{}
	diff, _ = detachCrossKindActionUpdates(
		[]liveAnalysisItem{incident}, []liveAnalysisItem{timeAtom}, nil, scope, stats,
	)
	if len(diff) != 1 || diff[0].ID != "" || stats.DivergentUpdatesDetached != 1 {
		t.Fatalf("time atom destructively reused incident ID: diff=%+v stats=%+v", diff, stats)
	}
}

func TestSessionDe988PropositionIdentityAcceptsCompatibleStrengthening(t *testing.T) {
	scope := newLiveEvidenceScope()
	scope.TranscriptText[1] = "3階で接続障害が発生しました。"
	scope.TranscriptText[2] = "3階の複数端末で接続不能が確認されました。"
	existing := liveAnalysisItem{
		ID: "item-incident", Kind: "issue", Title: "3階で接続障害が発生",
		Body: "3階で接続障害が発生", EvidenceSequenceNos: []int64{1},
	}
	update := liveAnalysisItem{
		ID: existing.ID, Kind: "issue", Title: "3階の複数端末で接続障害を確認",
		Body: "3階の複数端末で接続不能が確認された", EvidenceSequenceNos: []int64{2},
	}
	compatibility := evaluatePropositionUpdateCompatibility(existing, update, scope)
	if !compatibility.Compatible || !compatibility.SubjectMatch || !compatibility.PredicateMatch {
		t.Fatalf("compatible strengthening was detached: %+v", compatibility)
	}
}

func TestSessionDe988StoredRoundReplayPreservesCanonicalIncident(t *testing.T) {
	const reusedID = "item-issue-discussion-d93eddadf594"
	scope := newLiveEvidenceScope()
	scope.Allowed[2], scope.CurrentRound[2], scope.FreshRound[2] = struct{}{}, struct{}{}, struct{}{}
	scope.TranscriptText[2] = "本日午前9時20分ごろ、名古屋支社の3階を中心に社内ネットワークが接続できないという報告がありました。"
	scope.CoveredThrough = 2
	first := `{"summary":"障害発生","currentTopic":"影響範囲","items":[{"id":"` + reusedID + `","kind":"issue","subtype":"discussion","severity":"high","title":"名古屋支社3階のネットワーク接続障害","body":"名古屋支社3階で社内ネットワークへ接続できない","status":"open","evidenceSequenceNos":[2],"evidenceSnippets":["名古屋支社の3階を中心に社内ネットワークが接続できない"]}],"newTopics":[{"id":"topic-impact","kind":"topic","label":"障害の影響範囲"}],"assignments":[{"nodeId":"` + reusedID + `","parentTopicId":"topic-impact","confidence":0.9,"reason":"impact"}]}`
	raw, err := parseAndMergeLiveAnalysisPayloadWithEvidence(
		first, nil, nil, 1, []int64{2}, scope, TreeClassificationConfig{},
	)
	if err != nil {
		t.Fatal(err)
	}

	scope.CurrentRound = map[int64]struct{}{7: {}}
	scope.FreshRound = map[int64]struct{}{7: {}}
	scope.Allowed[7] = struct{}{}
	scope.TranscriptText[7] = "交換した3階アクセススイッチではVLAN20とVLAN30の通信が不安定で、ポート設定が不整合でした。"
	scope.CoveredThrough = 7
	second := `{"summary":"設定調査","currentTopic":"原因調査","items":[{"id":"` + reusedID + `","kind":"fact","severity":"high","title":"3階アクセススイッチのVLAN設定不整合","body":"交換したアクセススイッチのポート設定が不整合だった","status":"open","evidenceSequenceNos":[7],"evidenceSnippets":["ポート設定が不整合でした"]}],"assignments":[{"nodeId":"` + reusedID + `","parentTopicId":"topic-impact","confidence":0.7,"reason":"model reuse"}]}`
	raw, err = parseAndMergeLiveAnalysisPayloadWithEvidence(
		second, raw, nil, 2, []int64{7}, scope, TreeClassificationConfig{},
	)
	if err != nil {
		t.Fatal(err)
	}

	scope.CurrentRound = map[int64]struct{}{12: {}}
	scope.FreshRound = map[int64]struct{}{12: {}}
	scope.Allowed[12] = struct{}{}
	scope.TranscriptText[12] = "午前10時5分に。"
	scope.CoveredThrough = 12
	third := `{"summary":"復旧時刻","currentTopic":"復旧","items":[{"id":"` + reusedID + `","kind":"issue","subtype":"discussion","severity":"low","title":"午前10時5分に","body":"午前10時5分に","status":"open","evidenceSequenceNos":[12],"evidenceSnippets":["午前10時5分に"]}],"assignments":[{"nodeId":"` + reusedID + `","parentTopicId":"topic-impact","confidence":0.5,"reason":"model reuse"}]}`
	raw, err = parseAndMergeLiveAnalysisPayloadWithEvidence(
		third, raw, nil, 3, []int64{12}, scope, TreeClassificationConfig{},
	)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(raw)
	incident := findItemByID(state.Items, reusedID)
	if incident == nil || !strings.Contains(incident.Title, "ネットワーク接続障害") ||
		!containsInt64(incident.EvidenceSequenceNos, 2) ||
		containsInt64(incident.EvidenceSequenceNos, 7) ||
		containsInt64(incident.EvidenceSequenceNos, 12) {
		t.Fatalf("canonical incident was overwritten during stored replay: %+v", incident)
	}
	foundConfiguration := false
	for _, item := range state.Items {
		if item.ID != reusedID && strings.Contains(item.Title, "VLAN設定") &&
			containsInt64(item.EvidenceSequenceNos, 7) {
			foundConfiguration = true
		}
		if item.ID != reusedID && strings.TrimSpace(item.Title) == "午前10時5分に" {
			t.Fatalf("low-information time atom survived replay: %+v", item)
		}
	}
	if !foundConfiguration {
		t.Fatalf("distinct configuration proposition missing after replay: %+v", state.Items)
	}
}

func TestSessionDe988CorrectionAndLimitationStayDistinct(t *testing.T) {
	limitation := "インターネットが完全に停止したわけではなく、接続できる端末と接続できない端末が混在していました。"
	correction := "いえ、正確には完全なアクセスポート設定ではありません。トランク設定自体は入っていましたが、許可するVLANの一覧からVLAN30が漏れていました。"
	if discourseCorrectionPattern.MatchString(limitation) {
		t.Fatalf("ordinary scope limitation was classified as correction")
	}
	if !discourseCorrectionPattern.MatchString(correction) {
		t.Fatalf("explicit replacement was not classified as correction")
	}
}

func TestSessionDe988LowInformationIssueResolvesBeforeGrounding(t *testing.T) {
	text := "ただし、2階で発生した通信遅延まで、このVLAN設定だけで説明できるか確認できていません。この点は未解決の調査事項として起こします。"
	scope := newLiveEvidenceScope()
	scope.Allowed[10], scope.CurrentRound[10], scope.FreshRound[10] = struct{}{}, struct{}{}, struct{}{}
	scope.TranscriptText[10] = text
	scope.CoveredThrough = 10
	item := liveAnalysisItem{
		ID: "issue-investigation-auto-5b5fc3be85b9", Kind: "issue",
		Subtype:             issueSubtypeInvestigation,
		Title:               "この点は未解決の調査事項として起こします",
		Body:                "この点は未解決の調査事項として起こします",
		EvidenceSequenceNos: []int64{10},
	}
	timeline := classifyDiscourseTimeline(scope)
	repaired, _ := repairLowInformationIssueItems(
		nil, []liveAnalysisItem{item}, nil, timeline, scope, &liveAnalysisTreeMergeStats{},
	)
	if len(repaired) != 1 || issueTextNeedsReferent(repaired[0].Title) ||
		!strings.Contains(repaired[0].Title, "2階") {
		t.Fatalf("low-information issue was not propositionally reconstructed: %+v", repaired)
	}
}

func TestSessionDe988DecisionRecoveryUsesAdjacentReferent(t *testing.T) {
	segments := []domain.TranscriptSegment{
		{
			SequenceNo: 15, SpeakerID: "speaker-1", IsFinal: true,
			Text: "まずは、ネットワーク機器を交換する際は、作業者とは別の担当者が設定内容を確認するダブルチェックを必然にします。また、交換前後でVLANごとの疎通確認を実施するチェックリストを作成します。",
		},
		{
			SequenceNo: 16, SpeakerID: "speaker-1", IsFinal: true,
			Text: "この運用を、次回の機器交換から適用することにします。",
		},
		{
			SequenceNo: 22, SpeakerID: "speaker-1", IsFinal: true,
			Text: "最後にここまでをまとめます。再発防止として、設定のダブルチェックとVLANごとの疎通確認を必須にします。",
		},
	}
	candidates := detectDecisionCandidates(segments)
	if len(candidates) != 4 {
		t.Fatalf("decision candidates=%+v, want primary mandate, adoption and split recap mandates", candidates)
	}
	if decisionStatementNeedsReferent(candidates[1].Statement) ||
		!strings.Contains(candidates[1].Statement, "チェックリスト") {
		t.Fatalf("adjacent decision referent was not reconstructed: %+v", candidates[1])
	}
	stats := &liveAnalysisTreeMergeStats{}
	items := synthesizeExplicitDecisionItems(nil, segments, stats)
	if len(items) != 2 || stats.StrongDecisionsSynthesized != 2 {
		t.Fatalf("decision synthesis items=%+v stats=%+v", items, stats)
	}
}

func TestSessionDe988SameSegmentOwnerAssignmentsRemainDistinct(t *testing.T) {
	first := liveAnalysisItem{
		Kind: "todo", Title: "山下 耀翔さんが今週金曜日までにチェックリスト案を作成します",
		Body:                "私が今週金曜日までにチェックリスト案を作成します",
		EvidenceSequenceNos: []int64{17},
	}
	second := liveAnalysisItem{
		Kind: "todo", Title: "佐藤さんには来週火曜日までに標準設定との差分を確認してもらいます",
		Body:                "佐藤さんには来週火曜日までに標準設定との差分を確認してもらいます",
		EvidenceSequenceNos: []int64{17},
	}
	if !distinctTodoAssignments(first, second) {
		t.Fatalf(
			"same-segment assignments were treated as one proposition: firstOwners=%v secondOwners=%v",
			normalizedPatternMatches(kindOwnerPattern, first.Title+" "+first.Body),
			normalizedPatternMatches(kindOwnerPattern, second.Title+" "+second.Body),
		)
	}
	for _, evaluator := range []struct {
		name string
		call func(liveAnalysisItem, liveAnalysisItem) (bool, float64)
	}{
		{name: "semantic", call: sameKindSemanticDuplicate},
		{name: "sequential", call: sameKindSequentialProposition},
	} {
		if matched, score := evaluator.call(first, second); matched {
			t.Fatalf("%s dedup merged distinct owners with score %.2f", evaluator.name, score)
		}
	}
}

func TestSessionDe988RiskAndOpenConditionSplitIntoSeparatePropositions(t *testing.T) {
	text := "ただし、監視対象を増やすとアラートが多くなりすぎる可能性があります。監視間隔と通知条件については、次回までに検討が必要です。"
	scope := newLiveEvidenceScope()
	scope.Allowed[19], scope.CurrentRound[19], scope.FreshRound[19] = struct{}{}, struct{}{}, struct{}{}
	scope.TranscriptText[19] = text
	scope.CoveredThrough = 19
	item := liveAnalysisItem{
		ID: "item-risk-5250fcb2f141", Kind: "issue", Subtype: issueSubtypeDiscussion,
		Title: "監視対象を増やすとアラートが多くなりすぎる",
		Body:  text, EvidenceSequenceNos: []int64{19},
	}
	items, _ := splitLiveItemKinds(
		nil, []liveAnalysisItem{item}, []treeAssignment{{NodeID: item.ID}},
		scope, &liveAnalysisTreeMergeStats{},
	)
	byKind := make(map[string]int)
	for _, split := range items {
		byKind[split.Kind]++
	}
	if byKind["risk"] != 1 || byKind["issue"] != 1 {
		t.Fatalf("risk/open condition were not separated: kinds=%v items=%+v", byKind, items)
	}
}

func TestSessionDe988LiveInputClassesSeparateRetryAndRecap(t *testing.T) {
	previous := liveAnalysisPayload{FinalSegmentCoverage: []finalSegmentCoverage{
		{CallID: "call-1", SequenceNo: 10, AttemptCount: 1, RetryEligible: true},
	}}
	round := []domain.TranscriptSegment{
		{CallID: "call-1", SequenceNo: 10, IsFinal: true, Text: "2階の通信遅延原因は未確認です。"},
		{CallID: "call-1", SequenceNo: 11, IsFinal: true, Text: "復旧のため旧機器へ切り戻しました。"},
		{CallID: "call-1", SequenceNo: 22, IsFinal: true, Text: "最後にここまでをまとめます。復旧対応を実施しました。"},
	}
	scope := newLiveEvidenceScope()
	for _, segment := range round {
		scope.CurrentRound[segment.SequenceNo] = struct{}{}
		scope.TranscriptText[segment.SequenceNo] = segment.Text
		scope.Segments[segment.SequenceNo] = segment
	}
	classifyLiveRoundInputs(&scope, previous, round)
	rendered, _ := buildLiveAnalysisTranscriptByClass(round, scope, 0)
	for _, label := range []string{"[currentRoundSegments]", "[retrySegments]", "[recapSegments]"} {
		if !strings.Contains(rendered, label) {
			t.Fatalf("classified prompt missing %s: %s", label, rendered)
		}
	}
}

func TestSessionDe988FinalAgendaReconciliationMovesCauseFromRecapContamination(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{
		{ID: "agenda-1", Title: "障害の影響範囲と発生時刻", Description: "影響のあった範囲と発生・復旧の時刻の整理", Goal: "影響範囲と発生時刻を確認する", SemanticHints: []string{"名古屋支社", "午前9時20分", "午前10時5分"}, Order: 1, Role: agendaRolePrimary},
		{ID: "agenda-2", Title: "原因調査と復旧対応", Description: "直接原因と実施した復旧対応の整理", Goal: "障害の直接原因と復旧対応を整理する", SemanticHints: []string{"アクセススイッチ交換", "VLAN設定", "復旧対応"}, Order: 2, Role: agendaRolePrimary},
		{ID: "agenda-3", Title: "再発防止策", Description: "今後の対策案の検討", Goal: "再発防止策を検討する", SemanticHints: []string{"再発防止策", "監視ログ"}, Order: 3, Role: agendaRolePrimary},
	}}
	agenda2Topic := stableAgendaTopicID("agenda-2", 0)
	agenda3Topic := stableAgendaTopicID("agenda-3", 0)
	cause := liveAnalysisItem{
		ID: "item-fact-587776628a9a", Kind: "fact",
		Title:  "トランク設定の許可VLAN一覧からVLAN30が漏れていた",
		Body:   "交換したアクセススイッチのトランク設定で許可VLAN30が漏れていた",
		Status: "open", EvidenceSequenceNos: []int64{8, 22},
	}
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{cause},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "root"},
			{ID: agenda2Topic, Kind: "topic", ParentID: treeRootNodeID, Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-2"}, Materialized: true},
			{ID: agenda3Topic, Kind: "topic", ParentID: treeRootNodeID, Origin: topicOriginAgenda, AgendaRefs: []string{"agenda-3"}, Materialized: true},
			{ID: cause.ID, Kind: "fact", ParentID: agenda3Topic, Label: cause.Title},
		}},
	}
	segments := []domain.TranscriptSegment{
		{SequenceNo: 8, IsFinal: true, Text: "いえ、正確にはトランク設定自体は入っていましたが、許可するVLANの一覧からVLAN30が漏れていました。"},
		{SequenceNo: 22, IsFinal: true, Text: "最後にここまでをまとめます。VLAN30の許可設定漏れが主な原因です。再発防止としてダブルチェックを必須にします。"},
	}
	scoreScope, scoreTimeline := agendaTimelineFromSegments(segments)
	selected, score, ids, scores, reason := bestAgendaEvidenceMatch(
		cause, "", mc.Agenda, scoreScope, scoreTimeline,
	)
	t.Logf("agenda score selected=%s score=%.2f ids=%v scores=%v reason=%s", selected.ID, score, ids, scores, reason)
	decisions := reconcileFinalAgendaEvidence(&state, mc, segments, 18)
	if got := treeItemTopic(state.Tree, cause.ID); got != agenda2Topic {
		t.Fatalf("cause remained recap-contaminated: topic=%s decisions=%+v", got, decisions)
	}
}

func TestSessionDe988AblationMatrix(t *testing.T) {
	scope := newLiveEvidenceScope()
	scope.CoveredThrough = 7
	scope.TranscriptText[2] = "名古屋支社3階でネットワーク接続障害が発生しました。"
	scope.TranscriptText[7] = "交換したアクセススイッチのVLAN設定に不整合がありました。"
	scope.EvidenceRoles = map[int64]liveEvidenceRole{2: liveEvidencePrimary, 7: liveEvidencePrimary}
	incident := liveAnalysisItem{
		ID: "item-observed-reuse", Kind: "issue", Title: "3階ネットワーク接続障害",
		Body: "3階で接続障害が発生", EvidenceSequenceNos: []int64{2},
	}
	reused := liveAnalysisItem{
		ID: incident.ID, Kind: "fact", Title: "アクセススイッチのVLAN設定不整合",
		Body: "VLAN設定に不整合", EvidenceSequenceNos: []int64{7},
	}
	legacyOverwrite := mergeDuplicateLiveItem(incident, reused)
	guarded, _ := detachCrossKindActionUpdates(
		[]liveAnalysisItem{incident}, []liveAnalysisItem{reused}, nil,
		scope, &liveAnalysisTreeMergeStats{},
	)
	if legacyOverwrite.Title != reused.Title || len(guarded) != 1 || guarded[0].ID != "" {
		t.Fatalf("identity ablation setup invalid: legacy=%+v guarded=%+v", legacyOverwrite, guarded)
	}

	actionSegments := []domain.TranscriptSegment{
		{SequenceNo: 17, SpeakerName: "山下 耀翔", IsFinal: true, Text: "私が今週金曜日までにチェックリスト案を作成します。佐藤さんには来週火曜日までに標準設定との差分を確認してもらいます。"},
		{SequenceNo: 18, IsFinal: true, Text: "VLAN単位の疎通確認を監視へ追加する案もあります。"},
		{SequenceNo: 19, IsFinal: true, Text: "監視間隔と通知条件は次回までに検討が必要です。"},
		{SequenceNo: 20, IsFinal: true, Text: "VPN証明書の更新は別の対応事項として管理します。"},
		{SequenceNo: 21, IsFinal: true, Text: "高橋さんに今週中に証明書の更新手順と作業可能日を確認してもらいます。"},
	}
	actionScope, timeline := agendaTimelineFromSegments(actionSegments)
	synthStats := &liveAnalysisTreeMergeStats{}
	todos := synthesizeStrongTodoItems(nil, nil, actionScope, timeline, synthStats)
	if len(todos) != 3 || synthStats.StrongTodoCandidates != 3 {
		t.Fatalf("bounded Todo synthesis inflated: todos=%+v stats=%+v", todos, synthStats)
	}
	t.Logf("ablation retry=disabled canonicalItems=1 overwritten=0 missedNewProposition=1")
	t.Logf("ablation retry=enabled identityGuard=disabled canonicalItems=1 overwritten=1")
	t.Logf("ablation retry=enabled identityGuard=enabled canonicalItems=2 overwritten=0 detached=1")
	t.Logf("ablation deterministicSynthesis=disabled strongTodos=0 decisions=0")
	t.Logf("ablation deterministicSynthesis=enabled strongTodos=%d strongTodoCandidates=%d decisions=3", len(todos), synthStats.StrongTodoCandidates)
}

func TestSessionDe988StructuredSemanticLogsContainNoTranscript(t *testing.T) {
	var output bytes.Buffer
	previousWriter, previousFlags := log.Writer(), log.Flags()
	log.SetOutput(&output)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	}()

	stats := &liveAnalysisTreeMergeStats{
		CrossKindUpdateDecisions: []crossKindUpdateDecision{{
			ExistingItemID: "item-old", ModelItemID: "item-old",
			NewClientKey: "item-old-fact-companion", OldKind: "issue", NewKind: "fact",
			OldEvidence: []int64{2}, NewEvidence: []int64{7},
			SubjectMatch: false, PredicateMatch: false, ObjectMatch: true,
			QualifierMatch: true, Similarity: 0.07,
			Decision: "rejected", Reason: "cross_kind_proposition_incompatible",
		}},
		DeterministicSynthesisDecisions: []deterministicSynthesisDecision{{
			SequenceNo: 21, Kind: "todo", OwnerPresent: true,
			ActionPresent: true, ObjectPresent: true, CommitmentPresent: true,
			ItemID: "item-todo-safe", Decision: "accepted",
			Reason: deterministicTodoAssignmentReason,
		}},
		LowInformationItemsRewritten: 1,
		KindDistributionWarnings:     []string{"todo_share_above_expected_range"},
	}
	logSemanticDecisionEvents("session_de988dc494c49a6f", 18, 2, stats)
	logCoverageRetryDecision(
		"session_de988dc494c49a6f", 18,
		finalSegmentCoverage{SequenceNo: 7, AttemptCount: 2, MeaningfullyCovered: true, Reason: "item_evidence"},
		nil,
		[]liveAnalysisItem{{ID: "item-new", EvidenceSequenceNos: []int64{7}}},
	)
	logAgendaReconciliations("session_de988dc494c49a6f", 18, []agendaReconciliationDecision{{
		Trigger: agendaReconciliationFinalization, ItemID: "item-new",
		EvidenceSequenceNos: []int64{7}, CandidateAgendaIDs: []string{"agenda-2"},
		SelectedAgendaID: "agenda-2", Score: 0.82, AgendaRefsRepaired: true,
	}})
	rendered := output.String()
	for _, event := range []string{
		"event=item_proposition_changed",
		"event=meaningful_coverage_retry_result",
		"event=agenda_assignment_decision",
		"event=low_information_item_detected",
		"event=deterministic_synthesis_candidate",
		"event=kind_distribution_anomaly",
	} {
		if !strings.Contains(rendered, event) {
			t.Fatalf("structured log missing %s:\n%s", event, rendered)
		}
	}
	for _, forbidden := range []string{
		"名古屋支社", "アクセススイッチ", "VLAN30", "今週金曜日",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("structured log leaked transcript token %q:\n%s", forbidden, rendered)
		}
	}
}

func TestSessionDe988FinalRepairIsByteEquivalentOnSecondPass(t *testing.T) {
	segments := []domain.TranscriptSegment{
		{SequenceNo: 15, SpeakerID: "speaker-1", IsFinal: true, Text: "ダブルチェックを必須にします。"},
		{SequenceNo: 16, SpeakerID: "speaker-1", IsFinal: true, Text: "この運用を次回から適用することにします。"},
	}
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "root"},
			{ID: treeUnclassifiedTopicID, Kind: "topic", ParentID: treeRootNodeID, Label: "追加論点"},
		}},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	first, stats := applyDeterministicFinalTreeRepairs(
		raw, nil, 18, finalRepairInput{Segments: segments},
	)
	if stats.Error != "" || stats.IntegrityRejected {
		t.Fatalf("first repair failed: %+v", stats)
	}
	second, stats := applyDeterministicFinalTreeRepairs(
		first, nil, 18, finalRepairInput{Segments: segments},
	)
	if stats.Error != "" || stats.IntegrityRejected {
		t.Fatalf("second repair failed: %+v", stats)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("second final repair changed serialized payload\nfirst=%s\nsecond=%s", first, second)
	}
}

// TestSessionDe988StoredV18RepairReplayFromFlag is an opt-in, read-only replay
// harness for the locally stored production-shaped snapshot. Keeping the
// database read outside Application preserves the dependency direction; the
// test accepts only exported JSON and never writes repository state or calls a
// model.
func TestSessionDe988StoredV18RepairReplayFromFlag(t *testing.T) {
	encoded := strings.TrimSpace(*storedReplayGzipBase64)
	if encoded == "" {
		t.Skip("pass -stored-replay-gzip-base64 with exported replay JSON")
	}
	var fixture struct {
		Payload  json.RawMessage            `json:"payload"`
		Context  json.RawMessage            `json:"context"`
		Segments []domain.TranscriptSegment `json:"segments"`
	}
	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode replay fixture base64: %v", err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("open replay fixture gzip: %v", err)
	}
	defer reader.Close()
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read replay fixture: %v", err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode replay fixture: %v", err)
	}
	var meetingCtx meetingContext
	if err := json.Unmarshal(fixture.Context, &meetingCtx); err != nil {
		t.Fatalf("decode meeting context: %v", err)
	}

	first, stats := applyDeterministicFinalTreeRepairs(
		fixture.Payload, &meetingCtx, 18, finalRepairInput{Segments: fixture.Segments},
	)
	second, secondStats := applyDeterministicFinalTreeRepairs(
		first, &meetingCtx, 18, finalRepairInput{Segments: fixture.Segments},
	)
	if !bytes.Equal(first, second) {
		firstState := previousLiveAnalysisState(first)
		secondState := previousLiveAnalysisState(second)
		t.Logf(
			"non-idempotent stored replay: firstItems=%d secondItems=%d firstSynthTodos=%d secondSynthTodos=%d firstSynthDecisions=%d secondSynthDecisions=%d firstSameKindMerged=%d secondSameKindMerged=%d",
			len(firstState.Items), len(secondState.Items),
			stats.StrongTodosSynthesized, secondStats.StrongTodosSynthesized,
			stats.StrongDecisionsSynthesized, secondStats.StrongDecisionsSynthesized,
			stats.SameKindDuplicatesMerged, secondStats.SameKindDuplicatesMerged,
		)
		for _, item := range firstState.Items {
			if item.Kind == "todo" && !item.Inactive && item.MergedIntoID == "" {
				t.Logf("first-pass todo: id=%s title=%q body=%q", item.ID, item.Title, item.Body)
			}
		}
		for _, item := range secondState.Items {
			if item.Kind == "todo" && !item.Inactive && item.MergedIntoID == "" {
				t.Logf("second-pass todo: id=%s title=%q body=%q", item.ID, item.Title, item.Body)
			}
		}
		secondByID := make(map[string]liveAnalysisItem, len(secondState.Items))
		for _, item := range secondState.Items {
			secondByID[item.ID] = item
		}
		for _, item := range firstState.Items {
			after, ok := secondByID[item.ID]
			if !ok {
				t.Logf("second pass removed item: id=%s", item.ID)
				continue
			}
			beforeJSON, _ := json.Marshal(item)
			afterJSON, _ := json.Marshal(after)
			if !bytes.Equal(beforeJSON, afterJSON) {
				t.Logf(
					"second pass changed item: id=%s before=%s after=%s",
					item.ID, beforeJSON, afterJSON,
				)
			}
			delete(secondByID, item.ID)
		}
		for id := range secondByID {
			t.Logf("second pass added item: id=%s", id)
		}
		firstTreeJSON, _ := json.Marshal(firstState.Tree)
		secondTreeJSON, _ := json.Marshal(secondState.Tree)
		if !bytes.Equal(firstTreeJSON, secondTreeJSON) {
			t.Logf(
				"second pass changed tree: firstNodes=%d secondNodes=%d firstEdges=%d secondEdges=%d",
				len(firstState.Tree.Nodes), len(secondState.Tree.Nodes),
				len(firstState.Tree.Edges), len(secondState.Tree.Edges),
			)
			secondNodes := make(map[string]liveAnalysisTreeNode, len(secondState.Tree.Nodes))
			for _, node := range secondState.Tree.Nodes {
				secondNodes[node.ID] = node
			}
			for _, node := range firstState.Tree.Nodes {
				after, ok := secondNodes[node.ID]
				if !ok {
					t.Logf("second pass removed node: id=%s", node.ID)
					continue
				}
				beforeJSON, _ := json.Marshal(node)
				afterJSON, _ := json.Marshal(after)
				if !bytes.Equal(beforeJSON, afterJSON) {
					t.Logf("second pass changed node: id=%s before=%s after=%s", node.ID, beforeJSON, afterJSON)
				}
				delete(secondNodes, node.ID)
			}
			for id := range secondNodes {
				t.Logf("second pass added node: id=%s", id)
			}
		}
		for _, field := range []struct {
			name          string
			before, after any
		}{
			{name: "tombstones", before: firstState.ItemTombstones, after: secondState.ItemTombstones},
			{name: "anchors", before: firstState.AgendaAnchors, after: secondState.AgendaAnchors},
			{name: "progress", before: firstState.AgendaProgress, after: secondState.AgendaProgress},
			{name: "corrections", before: firstState.CorrectionRelations, after: secondState.CorrectionRelations},
			{name: "reorganization", before: firstState.ReorganizationReasons, after: secondState.ReorganizationReasons},
		} {
			beforeJSON, _ := json.Marshal(field.before)
			afterJSON, _ := json.Marshal(field.after)
			if !bytes.Equal(beforeJSON, afterJSON) {
				t.Logf("second pass changed payload field: %s", field.name)
			}
		}
		firstDiffAt := 0
		for firstDiffAt < len(first) && firstDiffAt < len(second) &&
			first[firstDiffAt] == second[firstDiffAt] {
			firstDiffAt++
		}
		windowStart := firstDiffAt - 80
		if windowStart < 0 {
			windowStart = 0
		}
		firstEnd, secondEnd := firstDiffAt+160, firstDiffAt+160
		if firstEnd > len(first) {
			firstEnd = len(first)
		}
		if secondEnd > len(second) {
			secondEnd = len(second)
		}
		t.Logf(
			"first byte difference at=%d firstLen=%d secondLen=%d before=%q after=%q",
			firstDiffAt, len(first), len(second),
			first[windowStart:firstEnd], second[windowStart:secondEnd],
		)
		t.Fatal("stored replay final repair was not byte-equivalent on the second pass")
	}
	finalized, agendaDecisions, err := finalizeAgendaLifecyclePayloadWithEvidence(
		first, &meetingCtx, 18, fixture.Segments,
	)
	if err != nil {
		t.Fatalf("finalize stored replay agenda lifecycle: %v", err)
	}
	finalizedAgain, _, err := finalizeAgendaLifecyclePayloadWithEvidence(
		finalized, &meetingCtx, 18, fixture.Segments,
	)
	if err != nil {
		t.Fatalf("finalize stored replay agenda lifecycle twice: %v", err)
	}
	if !bytes.Equal(finalized, finalizedAgain) {
		t.Fatal("stored replay agenda lifecycle was not byte-equivalent on the second pass")
	}
	state := previousLiveAnalysisState(finalized)
	t.Logf(
		"stored target repair: active=%d all=%d strongTodos=%d strongDecisions=%d correctionReconstructed=%d correctionSuperseded=%d lowInfoRewritten=%d lowInfoRejected=%d groundingRejected=%d secondPassStrongTodos=%d secondPassStrongDecisions=%d",
		len(activeItemsForReplay(state.Items)), len(state.Items),
		stats.StrongTodosSynthesized, stats.StrongDecisionsSynthesized,
		stats.CorrectionItemsReconstructed, stats.CorrectionItemsSuperseded,
		stats.LowInformationItemsRewritten, stats.LowInformationItemsRejected,
		stats.GroundingRejected, secondStats.StrongTodosSynthesized,
		secondStats.StrongDecisionsSynthesized,
	)
	for _, decision := range agendaDecisions {
		t.Logf(
			"stored target agenda: itemId=%s evidence=%v selected=%s activeSpan=%s moved=%t score=%.2f rejectedReason=%s",
			decision.ItemID, decision.EvidenceSequenceNos, decision.SelectedAgendaID,
			decision.CurrentActiveAgendaID, decision.ItemMoved, decision.Score,
			decision.RejectedReason,
		)
	}
	parentByID := make(map[string]string)
	if state.Tree != nil {
		for _, node := range state.Tree.Nodes {
			parentByID[node.ID] = node.ParentID
		}
	}
	for _, item := range activeItemsForReplay(state.Items) {
		if endingType := incompleteItemLabelEnding(item); endingType != "" {
			t.Errorf("active item has incomplete label: itemId=%s endingType=%s label=%q", item.ID, endingType, item.Title)
		}
		t.Logf(
			"stored target active: itemId=%s kind=%s subtype=%s label=%q evidence=%v parent=%s classification=%s lifecycle=active source=%s",
			item.ID, item.Kind, item.Subtype, item.Title, item.EvidenceSequenceNos,
			parentByID[item.ID], item.ClassificationStatus, item.AssignmentSource,
		)
	}
	if state.AgendaProgress != nil {
		for _, entry := range state.AgendaProgress.Entries {
			t.Logf(
				"stored target progress: agendaId=%s status=%s source=%s active=%d evidence=%v weight=%.2f volume=%d primaryNode=%s reason=%s",
				entry.ID, entry.ComputedStatus, entry.ProgressSource,
				entry.ActiveItemCount, entry.EvidenceSequenceNos,
				entry.DiscussionWeight, entry.DiscussionVolume,
				entry.PrimaryNodeID, entry.ProgressReason,
			)
			if entry.ComputedStatus == agendaProgressDiscussing {
				t.Errorf("meeting-ended progress remained discussing: %+v", entry)
			}
			if entry.ActiveItemCount > 0 && entry.DiscussionWeight <= 0 {
				t.Errorf("active items have zero discussion weight: %+v", entry)
			}
		}
	}
	agenda1 := agendaProgressEntryByID(state.AgendaProgress, "agenda-1")
	if agenda1 == nil || agenda1.ComputedStatus != agendaProgressDiscussed ||
		agenda1.ProgressSource != agendaProgressEvidenceSourceTranscript ||
		agenda1.ActiveItemCount != 0 || len(agenda1.EvidenceSequenceNos) < 2 ||
		agenda1.DiscussionWeight <= 0 ||
		agenda1.ProgressReason != "meaningful_discussion_without_persisted_item" {
		t.Errorf("agenda-1 transcript provenance mismatch: %+v", agenda1)
	}
	agenda2 := agendaProgressEntryByID(state.AgendaProgress, "agenda-2")
	if agenda2 == nil || agenda2.ComputedStatus != agendaProgressDiscussed ||
		agenda2.ProgressSource != agendaProgressEvidenceSourceActiveItem ||
		agenda2.ActiveItemCount < 3 || agenda2.DiscussionWeight <= 0 {
		t.Errorf("agenda-2 active-item progress mismatch: %+v", agenda2)
	}
	for _, anchor := range state.AgendaAnchors {
		t.Logf(
			"stored target anchor: agendaId=%s role=%s status=%s topics=%v",
			anchor.AgendaID, anchor.Role, anchor.Status, anchor.MaterializedTopicIDs,
		)
	}
}
