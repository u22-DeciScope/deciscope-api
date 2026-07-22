package application

import (
	"encoding/json"
	"testing"

	"deciscope-core-api/internal/domain"
)

// TestAgendaProgressReplayR1ToR8 replays the contract's §7 scenario: a
// meeting with 3 fixed agenda items (現状確認[none], 改修案を決定する[decision],
// 担当者をアサインする[owner_todo]) plus one off-agenda side topic, driven
// through parseAndMergeLiveAnalysisPayloadWithEvidence exactly like the
// existing ai_session_* replay tests (evidenceScopeFromTexts, hand-written
// model diff JSON per round).
//
// R3/R5's agenda-1->agenda-2 and agenda-2->agenda-3 transitions intentionally
// use the *gradual* (2-consecutive-round) current-topic switch instead of an
// explicit natural-language transition marker: detectAgendaContextSpans
// treats an explicitly opened fixed-agenda span as authoritative until
// another explicit transition/no-agenda marker appears (see
// ai_agenda_context.go's "already carries stronger evidence than generic
// lexical mismatch" comment), so a natural-language explicit switch away
// from agenda-2 mid-scenario would keep re-forcing later utterances back
// onto agenda-2's span. The "explicit transition -> immediate switch" rule
// itself is covered directly and reliably by
// TestAgendaProgressCurrentTopicExplicitTransitionSwitchesImmediately in
// ai_agenda_progress_internal_test.go, which controls agendaContextSpan
// input directly.
func TestAgendaProgressReplayR1ToR8(t *testing.T) {
	mc := &meetingContext{
		Title: "改修計画会議",
		Agenda: []agendaItem{
			{ID: "agenda-1", Title: "現状確認", Order: 1, Role: agendaRolePrimary},
			{ID: "agenda-2", Title: "改修案を決定する", Order: 2, Role: agendaRolePrimary},
			{ID: "agenda-3", Title: "担当者をアサインする", Order: 3, Role: agendaRolePrimary},
		},
	}
	cfg := TreeClassificationConfig{}

	scope := liveEvidenceScope{
		Allowed:        map[int64]struct{}{},
		CurrentRound:   map[int64]struct{}{},
		TranscriptText: map[int64]string{},
		Segments:       map[int64]domain.TranscriptSegment{},
	}
	addRound := func(texts map[int64]string) []int64 {
		seqNos := make([]int64, 0, len(texts))
		scope.CurrentRound = map[int64]struct{}{}
		for seq, text := range texts {
			scope.Allowed[seq] = struct{}{}
			scope.CurrentRound[seq] = struct{}{}
			scope.TranscriptText[seq] = text
			scope.Segments[seq] = domain.TranscriptSegment{SequenceNo: seq, SpeakerID: "speaker-1", Text: text, IsFinal: true}
			if seq > scope.CoveredThrough {
				scope.CoveredThrough = seq
			}
			seqNos = append(seqNos, seq)
		}
		return seqNos
	}
	agendaProgressOf := func(raw json.RawMessage) *agendaProgressState {
		return previousLiveAnalysisState(raw).AgendaProgress
	}
	statusOf := func(progress *agendaProgressState, id string) string {
		entry := agendaProgressEntryByID(progress, id)
		if entry == nil {
			return "<missing>"
		}
		return entry.ComputedStatus
	}

	// --- R1: agenda names are merely read out. No items. ----------------------
	round1Texts := map[int64]string{
		1: "本日は3つの項目を扱う予定です。1つ目はサーバーの現状確認、2つ目は改修案を決定すること、3つ目は担当者をアサインすることです。",
	}
	round1Seq := addRound(round1Texts)
	model1 := `{"summary":"議題確認","currentTopic":"","items":[]}`
	raw1, err := parseAndMergeLiveAnalysisPayloadWithEvidence(model1, nil, mc, 1, round1Seq, scope, cfg)
	if err != nil {
		t.Fatalf("R1 error = %v", err)
	}
	progress1 := agendaProgressOf(raw1)
	if progress1 == nil {
		t.Fatalf("R1 agendaProgress is nil")
	}
	for _, id := range []string{"agenda-1", "agenda-2", "agenda-3"} {
		if got := statusOf(progress1, id); got != agendaProgressNotStarted {
			t.Fatalf("R1 %s status = %s, want not_started (reading agenda names aloud must not count as discussion)", id, got)
		}
	}
	if progress1.ComputedCurrentTopicID != "" {
		t.Fatalf("R1 currentTopicId = %q, want empty", progress1.ComputedCurrentTopicID)
	}
	t.Logf("R1: agenda-1=%s agenda-2=%s agenda-3=%s current=%q", statusOf(progress1, "agenda-1"), statusOf(progress1, "agenda-2"), statusOf(progress1, "agenda-3"), progress1.ComputedCurrentTopicID)

	// --- R2: agenda-1 (現状確認), 3 substantive utterances, 2 items. -----------
	// Each utterance echoes "現状確認" so detectAgendaContextSpans recognizes
	// it as aligned with agenda-1 and never opens an implicit no-agenda span
	// (which would otherwise hijack these items' assignment -- see the R1
	// comment above about explicit-vs-implicit span authority).
	round2Texts := map[int64]string{
		2: "現状確認として、サーバーAのCPU使用率は日中平均60%で推移していることを共有します。",
		3: "現状確認の一環で、メモリ使用率については問題ない範囲で安定していると報告します。",
		4: "現状確認としてもう一点、ディスク容量は残り20%まで減少してきており監視が必要な状況です。",
	}
	round2Seq := addRound(round2Texts)
	model2 := `{"summary":"現状確認","currentTopic":"現状確認","items":[
		{"id":"item-fact-cpu","kind":"fact","severity":"low","title":"サーバーAのCPU使用率","body":"日中平均60%で推移している","status":"open","evidenceSequenceNos":[2]},
		{"id":"item-issue-disk","kind":"issue","subtype":"discussion","severity":"medium","title":"ディスク容量の減少","body":"残り20%まで減少しており監視が必要","status":"open","evidenceSequenceNos":[4]}
	],"assignments":[
		{"nodeId":"item-fact-cpu","parentTopicId":"agenda-1","confidence":0.9},
		{"nodeId":"item-issue-disk","parentTopicId":"agenda-1","confidence":0.9}
	]}`
	raw2, err := parseAndMergeLiveAnalysisPayloadWithEvidence(model2, raw1, mc, 2, round2Seq, scope, cfg)
	if err != nil {
		t.Fatalf("R2 error = %v", err)
	}
	progress2 := agendaProgressOf(raw2)
	agenda1R2 := agendaProgressEntryByID(progress2, "agenda-1")
	if agenda1R2 == nil || agenda1R2.ComputedStatus != agendaProgressDiscussing {
		t.Fatalf("R2 agenda-1 = %+v, want discussing", agenda1R2)
	}
	if progress2.ComputedCurrentTopicID != "agenda-1" {
		t.Fatalf("R2 currentTopicId = %q, want agenda-1", progress2.ComputedCurrentTopicID)
	}
	t.Logf("R2: agenda-1=%s weight=%.2f current=%q", agenda1R2.ComputedStatus, agenda1R2.DiscussionWeight, progress2.ComputedCurrentTopicID)

	// --- R3: agenda-2 (改修案の比較) starts, 2 utterances, 1 issue. current stays agenda-1 (round 1 of the gradual switch). ---
	round3Texts := map[int64]string{
		5: "改修案を決定する話として、A案とB案の2つが候補に挙がっています。",
		6: "改修案の比較では、A案はコストが低く、B案は工期が短いという特徴があります。",
	}
	round3Seq := addRound(round3Texts)
	model3 := `{"summary":"改修案の比較","currentTopic":"改修案の比較","items":[
		{"id":"item-issue-compare","kind":"issue","subtype":"discussion","severity":"medium","title":"改修方法の候補比較","body":"A案はコストが低く、B案は工期が短い","status":"open","evidenceSequenceNos":[5,6]}
	],"assignments":[
		{"nodeId":"item-issue-compare","parentTopicId":"agenda-2","confidence":0.9}
	]}`
	raw3, err := parseAndMergeLiveAnalysisPayloadWithEvidence(model3, raw2, mc, 3, round3Seq, scope, cfg)
	if err != nil {
		t.Fatalf("R3 error = %v", err)
	}
	progress3 := agendaProgressOf(raw3)
	agenda2R3 := agendaProgressEntryByID(progress3, "agenda-2")
	if agenda2R3 == nil || agenda2R3.ComputedStatus != agendaProgressDiscussing {
		t.Fatalf("R3 agenda-2 = %+v, want discussing", agenda2R3)
	}
	if progress3.ComputedCurrentTopicID != "agenda-1" {
		t.Fatalf("R3 currentTopicId = %q, want agenda-1 (still)", progress3.ComputedCurrentTopicID)
	}
	if agenda1R3 := agendaProgressEntryByID(progress3, "agenda-1"); agenda1R3 == nil || agenda1R3.InactiveRounds != 1 {
		t.Fatalf("R3 agenda-1 = %+v, want inactiveRounds=1", agenda1R3)
	}
	t.Logf("R3: agenda-2=%s current=%q candidateTopicId=%q candidateRounds=%d", agenda2R3.ComputedStatus, progress3.ComputedCurrentTopicID, progress3.CandidateTopicID, progress3.CandidateRounds)

	// --- R4: agenda-2 continues (2 more), agenda-1 gets 1 light mention. current switches to agenda-2 (round 2 of the gradual switch). ---
	round4Texts := map[int64]string{
		7: "改修案の検討を続けると、B案を採用する場合は来月中に着手できる見込みです。",
		8: "改修案としては、コスト面でもB案の方が予算内に収まりやすいという意見が出ました。",
		9: "現状確認で挙げたサーバーAのディスク増設について、見積もりも確認しておきます。",
	}
	round4Seq := addRound(round4Texts)
	model4 := `{"summary":"改修案の継続検討","currentTopic":"改修案の検討","items":[
		{"id":"item-issue-b-plan","kind":"issue","subtype":"discussion","severity":"medium","title":"B案採用時の着手時期とコスト","body":"来月中に着手可能で予算内に収まりやすい","status":"open","evidenceSequenceNos":[7,8]},
		{"id":"item-fact-disk-estimate","kind":"fact","severity":"low","title":"ディスク増設の見積もり確認","body":"サーバーAのディスク増設の見積もりを確認する","status":"open","evidenceSequenceNos":[9]}
	],"assignments":[
		{"nodeId":"item-issue-b-plan","parentTopicId":"agenda-2","confidence":0.9},
		{"nodeId":"item-fact-disk-estimate","parentTopicId":"agenda-1","confidence":0.9}
	]}`
	raw4, err := parseAndMergeLiveAnalysisPayloadWithEvidence(model4, raw3, mc, 4, round4Seq, scope, cfg)
	if err != nil {
		t.Fatalf("R4 error = %v", err)
	}
	progress4 := agendaProgressOf(raw4)
	if progress4.ComputedCurrentTopicID != "agenda-2" {
		t.Fatalf("R4 currentTopicId = %q, want agenda-2 (two consecutive leader rounds)", progress4.ComputedCurrentTopicID)
	}
	if agenda1R4 := agendaProgressEntryByID(progress4, "agenda-1"); agenda1R4 == nil || agenda1R4.InactiveRounds != 0 || agenda1R4.ActiveRounds != 2 {
		t.Fatalf("R4 agenda-1 = %+v, want inactiveRounds=0 activeRounds=2 (the light mention keeps it active)", agenda1R4)
	}
	t.Logf("R4: current=%q agenda-1.activeRounds=%d", progress4.ComputedCurrentTopicID, agendaProgressEntryByID(progress4, "agenda-1").ActiveRounds)

	// --- R5: agenda-2 reaches a decision. ---------------------------------------
	round5Texts := map[int64]string{
		10: "改修案を決定する話の結論として、コストと工期のバランスからB案を採用することにします。",
		11: "改修案の決定事項として、来月からB案での改修に着手します。",
	}
	round5Seq := addRound(round5Texts)
	model5 := `{"summary":"改修案の決定","currentTopic":"改修案の決定","items":[
		{"id":"item-decision-b-plan","kind":"decision","severity":"medium","title":"改修方法はB案を採用","body":"コストと工期のバランスからB案を採用することに決定した","status":"open","evidenceSequenceNos":[10,11]}
	],"assignments":[
		{"nodeId":"item-decision-b-plan","parentTopicId":"agenda-2","confidence":0.9}
	]}`
	raw5, err := parseAndMergeLiveAnalysisPayloadWithEvidence(model5, raw4, mc, 5, round5Seq, scope, cfg)
	if err != nil {
		t.Fatalf("R5 error = %v", err)
	}
	progress5 := agendaProgressOf(raw5)
	if progress5.ComputedCurrentTopicID != "agenda-2" {
		t.Fatalf("R5 currentTopicId = %q, want agenda-2 (still)", progress5.ComputedCurrentTopicID)
	}
	agenda2R5 := agendaProgressEntryByID(progress5, "agenda-2")
	if agenda2R5 == nil || agenda2R5.RelatedItemCounts["decision"] < 1 {
		t.Fatalf("R5 agenda-2 = %+v, want a related decision item", agenda2R5)
	}
	t.Logf("R5: agenda-2 relatedItemCounts=%v", agenda2R5.RelatedItemCounts)

	// --- R6: agenda-3 drifts in (round 1 of its gradual switch), plus an
	// off-agenda side topic's first round of evidence (must not display yet). ---
	round6Texts := map[int64]string{
		12: "担当者をアサインする話として、山田さんが実装を担当する案が出ています。",
		13: "担当者の件の続きで、レビュー担当は佐藤さんにお願いする方向で調整しています。",
		14: "備品の発注状況について少し触れておきます。今期の発注はまだ完了していません。",
		15: "特にモニターの発注が遅れており、来月には届く予定です。",
	}
	round6Seq := addRound(round6Texts)
	model6 := `{"summary":"担当者の検討と備品の状況","currentTopic":"担当者の検討","items":[
		{"id":"item-issue-owners","kind":"issue","subtype":"discussion","severity":"medium","title":"改修担当者の候補","body":"山田さんが実装、佐藤さんがレビューを担当する案","status":"open","evidenceSequenceNos":[12,13]},
		{"id":"item-side-order-1","kind":"issue","subtype":"discussion","severity":"low","title":"備品発注の遅延状況","body":"今期の発注が未完了で、モニターの発注も遅延している","status":"open","evidenceSequenceNos":[14,15]}
	],"newTopics":[{"id":"topic-side-order","label":"備品発注状況","description":"備品発注の遅延状況の確認"}],
	"assignments":[
		{"nodeId":"item-issue-owners","parentTopicId":"agenda-3","confidence":0.9},
		{"nodeId":"item-side-order-1","parentTopicId":"topic-side-order","confidence":0.6}
	]}`
	raw6, err := parseAndMergeLiveAnalysisPayloadWithEvidence(model6, raw5, mc, 6, round6Seq, scope, cfg)
	if err != nil {
		t.Fatalf("R6 error = %v", err)
	}
	progress6 := agendaProgressOf(raw6)
	agenda3R6 := agendaProgressEntryByID(progress6, "agenda-3")
	if agenda3R6 == nil || agenda3R6.ComputedStatus != agendaProgressDiscussing {
		t.Fatalf("R6 agenda-3 = %+v, want discussing", agenda3R6)
	}
	if progress6.ComputedCurrentTopicID != "agenda-2" {
		t.Fatalf("R6 currentTopicId = %q, want agenda-2 (still; agenda-3 is only round 1 of its switch)", progress6.ComputedCurrentTopicID)
	}
	sideTopicIDs6 := make([]string, 0)
	for _, entry := range progress6.Entries {
		if entry.SourceType == agendaProgressSourceDynamic {
			sideTopicIDs6 = append(sideTopicIDs6, entry.ID)
		}
	}
	if len(sideTopicIDs6) != 0 {
		t.Fatalf("R6 additional topics = %v, want none displayed yet (side topic's first round)", sideTopicIDs6)
	}
	t.Logf("R6: agenda-3=%s current=%q additionalTopics=%v", agenda3R6.ComputedStatus, progress6.ComputedCurrentTopicID, sideTopicIDs6)

	// --- R7: agenda-3 continues (round 2, switches current), agenda-2 goes
	// discussed+concluded, and the side topic's 2nd round makes it displayed. ---
	round7Texts := map[int64]string{
		16: "担当者をアサインする話の続きとして、山田さんの実装スケジュールは来週から着手予定です。",
		17: "担当者の件では、佐藤さんのレビュー体制も来週から並行して進める予定です。",
		18: "モニターの発注については引き続き確認を進めます。",
		19: "発注先の在庫状況も来週中に確認する予定です。",
	}
	round7Seq := addRound(round7Texts)
	model7 := `{"summary":"担当体制の確定と備品の追加確認","currentTopic":"担当体制の確定","items":[
		{"id":"item-todo-roles","kind":"todo","severity":"medium","title":"実装とレビューの体制確定","body":"山田さんが来週から実装、佐藤さんが並行してレビューする","status":"open","evidenceSequenceNos":[16,17]},
		{"id":"item-side-order-2","kind":"issue","subtype":"discussion","severity":"low","title":"モニター発注の追加確認","body":"発注先の在庫状況を来週中に確認する","status":"open","evidenceSequenceNos":[18,19]}
	],"newTopics":[{"id":"topic-side-order","label":"備品発注状況","description":"備品発注の遅延状況の確認"}],
	"assignments":[
		{"nodeId":"item-todo-roles","parentTopicId":"agenda-3","confidence":0.9},
		{"nodeId":"item-side-order-2","parentTopicId":"topic-side-order","confidence":0.6}
	]}`
	raw7, err := parseAndMergeLiveAnalysisPayloadWithEvidence(model7, raw6, mc, 7, round7Seq, scope, cfg)
	if err != nil {
		t.Fatalf("R7 error = %v", err)
	}
	progress7 := agendaProgressOf(raw7)
	if progress7.ComputedCurrentTopicID != "agenda-3" {
		t.Fatalf("R7 currentTopicId = %q, want agenda-3 (two consecutive leader rounds)", progress7.ComputedCurrentTopicID)
	}
	agenda2R7 := agendaProgressEntryByID(progress7, "agenda-2")
	if agenda2R7 == nil || agenda2R7.ComputedStatus != agendaProgressDiscussed {
		t.Fatalf("R7 agenda-2 = %+v, want discussed", agenda2R7)
	}
	if agenda2R7.OutcomeStatus != agendaOutcomeConcluded {
		t.Fatalf("R7 agenda-2 outcomeStatus = %q, want concluded (no false unresolved)", agenda2R7.OutcomeStatus)
	}
	sideTopicR7 := (*agendaProgressEntry)(nil)
	for i := range progress7.Entries {
		if progress7.Entries[i].SourceType == agendaProgressSourceDynamic {
			sideTopicR7 = &progress7.Entries[i]
		}
	}
	if sideTopicR7 == nil || sideTopicR7.ComputedStatus != agendaProgressDiscussing {
		t.Fatalf("R7 side topic = %+v, want a displayed discussing additional topic (round 2 of evidence)", sideTopicR7)
	}
	t.Logf("R7: current=%q agenda-2=%s/%s agenda-3=%s sideTopic=%s(%s)",
		progress7.ComputedCurrentTopicID, agenda2R7.ComputedStatus, agenda2R7.OutcomeStatus,
		statusOf(progress7, "agenda-3"), sideTopicR7.ID, sideTopicR7.ComputedStatus)

	// --- R8: manual override -- discussed on agenda-3, current forced to agenda-1. ---
	overrides := &AgendaProgressOverrides{StatusOverrides: map[string]string{"agenda-3": agendaProgressDiscussed}, CurrentTopicID: "agenda-1"}
	stamped7 := applyAgendaProgressOverrides(progress7, overrides)
	agenda3Stamped := agendaProgressEntryByID(stamped7, "agenda-3")
	if agenda3Stamped == nil || agenda3Stamped.ManualStatus != agendaProgressDiscussed || agenda3Stamped.EffectiveStatus != agendaProgressDiscussed {
		t.Fatalf("R8 stamped agenda-3 = %+v, want manual/effective=discussed", agenda3Stamped)
	}
	if stamped7.ManualCurrentTopicID != "agenda-1" || stamped7.EffectiveCurrentTopicID != "agenda-1" {
		t.Fatalf("R8 stamped current = manual=%q effective=%q, want agenda-1/agenda-1", stamped7.ManualCurrentTopicID, stamped7.EffectiveCurrentTopicID)
	}
	// Unaffected entries keep effective == computed.
	if agenda2Stamped := agendaProgressEntryByID(stamped7, "agenda-2"); agenda2Stamped == nil || agenda2Stamped.ManualStatus != "" || agenda2Stamped.EffectiveStatus != agenda2Stamped.ComputedStatus {
		t.Fatalf("R8 stamped agenda-2 = %+v, want unaffected", agenda2Stamped)
	}

	// One more round runs (overrides are never part of the merged payload, so
	// computed values keep evolving independently of the manual override).
	round8Texts := map[int64]string{20: "実装とレビューの進捗については、来週改めて確認します。"}
	round8Seq := addRound(round8Texts)
	model8 := `{"summary":"進捗確認の予定","currentTopic":"担当体制の確定","items":[
		{"id":"item-followup","kind":"todo","severity":"low","title":"実装レビュー進捗の確認予定","body":"来週改めて進捗を確認する","status":"open","evidenceSequenceNos":[20]}
	],"assignments":[{"nodeId":"item-followup","parentTopicId":"agenda-3","confidence":0.9}]}`
	raw8, err := parseAndMergeLiveAnalysisPayloadWithEvidence(model8, raw7, mc, 8, round8Seq, scope, cfg)
	if err != nil {
		t.Fatalf("R8 merge error = %v", err)
	}
	progress8 := agendaProgressOf(raw8)
	stamped8 := applyAgendaProgressOverrides(progress8, overrides)
	agenda3Stamped8 := agendaProgressEntryByID(stamped8, "agenda-3")
	if agenda3Stamped8 == nil || agenda3Stamped8.EffectiveStatus != agendaProgressDiscussed {
		t.Fatalf("R8 (next round) stamped agenda-3 = %+v, want the override to still apply", agenda3Stamped8)
	}
	if stamped8.EffectiveCurrentTopicID != "agenda-1" {
		t.Fatalf("R8 (next round) stamped current = %q, want the override to still apply (agenda-1)", stamped8.EffectiveCurrentTopicID)
	}

	// Clearing the override reverts effective values to computed.
	stampedCleared := applyAgendaProgressOverrides(progress8, nil)
	agenda3Cleared := agendaProgressEntryByID(stampedCleared, "agenda-3")
	if agenda3Cleared == nil || agenda3Cleared.EffectiveStatus != agenda3Cleared.ComputedStatus || agenda3Cleared.ManualStatus != "" {
		t.Fatalf("R8 cleared agenda-3 = %+v, want effective reverted to computed", agenda3Cleared)
	}
	if stampedCleared.EffectiveCurrentTopicID != stampedCleared.ComputedCurrentTopicID || stampedCleared.ManualCurrentTopicID != "" {
		t.Fatalf("R8 cleared current = effective=%q computed=%q manual=%q, want effective==computed and manual empty",
			stampedCleared.EffectiveCurrentTopicID, stampedCleared.ComputedCurrentTopicID, stampedCleared.ManualCurrentTopicID)
	}
	t.Logf("R8: override stamp verified (set/persisted-across-round/cleared)")
}
