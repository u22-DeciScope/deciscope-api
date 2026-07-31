package application

import (
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"deciscope-core-api/internal/domain"
)

const (
	agendaReconciliationDynamicCandidate  = "dynamic_candidate_reconciliation"
	agendaReconciliationSkipBackfill      = "agenda_skip_backfill"
	agendaReconciliationFinalization      = "finalization_reconciliation"
	agendaReconciliationAmbiguousFallback = "ambiguous_agenda_fallback"

	agendaReconciliationMinScore  = 0.62
	agendaReconciliationMinMargin = 0.10
	// A unique but not-yet-strong planned-agenda match is retained as a
	// tentative candidate. applyAssignments keeps it out of the visible
	// unclassified bucket and requires a repeated/later stronger observation
	// before materializing the planned topic.
	agendaReconciliationPendingMinScore = 0.40
)

// agendaReconciliationDecision is bounded, text-free observability for every
// deterministic planned-agenda reconsideration. The transcript and item text
// remain in their canonical stores; logs carry only IDs, sequence numbers and
// scores so an incident can be reconstructed without leaking meeting content.
type agendaReconciliationDecision struct {
	Trigger                 string
	ItemID                  string
	EvidenceSequenceNos     []int64
	CurrentActiveAgendaID   string
	TransitionNextAgendaID  string
	SkippedAgendaIDs        []string
	TransitionDirect        bool
	BackfillPerformed       bool
	CandidateAgendaIDs      []string
	CandidateScores         []string
	SelectedAgendaID        string
	Score                   float64
	PreviousStatus          string
	NewStatus               string
	RejectedReason          string
	ManualOverride          bool
	AgendaRefsRepaired      bool
	ItemMoved               bool
	PreviousParentID        string
	SelectedMaterializedID  string
	DynamicCandidateChecked bool
}

type agendaMatchCandidate struct {
	agenda agendaItem
	score  float64
}

var (
	agendaPreviewOnlyPattern       = regexp.MustCompile(`(?i)(?:あとで|後で|のちほど|次に|今度|次回).{0,20}(?:話|確認|検討|議論|取り上げ|扱)|(?:話|確認|検討|議論|取り上げ|扱).{0,16}(?:予定|つもり|あとで|後で|次回)`)
	agendaNegativeOnlyPattern      = regexp.MustCompile(`(?i)(?:未実施|未確認|未対応|まだ.{0,12}(?:していない|できていない|決まっていない)|(?:話|確認|検討|議論|実施|対応).{0,8}(?:していない|しなかった|できていない))`)
	agendaInvestigationRolePattern = regexp.MustCompile(`(?:原因調査|直接原因|障害診断)`)
	agendaRecoveryRolePattern      = regexp.MustCompile(`(?:復旧対応|復旧作業)`)
	agendaPreventionRolePattern    = regexp.MustCompile(`(?:再発防止|予防|今後の対策|改善策)`)
	evidenceDiagnosticRolePattern  = regexp.MustCompile(`(?:原因|異常|設定|構成|漏れ|不整合)`)
	evidenceRecoveryRolePattern    = regexp.MustCompile(`(?:切り戻|復旧|修正|正常化)`)
	evidencePreventionRolePattern  = regexp.MustCompile(`(?:再発防止|必須|運用|適用|導入|予防|対策|改善|提案|案がある)`)
)

func normalizedAgendaSemanticHints(values []string) []string {
	result := make([]string, 0, 6)
	seen := make(map[string]struct{}, 6)
	for _, value := range values {
		value = truncateRunes(strings.TrimSpace(value), 40)
		key := normalizeForMatch(value)
		if key == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
		if len(result) == 6 {
			break
		}
	}
	return result
}

func agendaSemanticText(agenda agendaItem) string {
	parts := []string{agenda.Title, agenda.Description, agenda.Goal}
	parts = append(parts, agenda.SemanticHints...)
	return strings.TrimSpace(strings.Join(parts, " "))
}

func agendaSemanticIdentityIsBroad(agenda agendaItem) bool {
	if strings.TrimSpace(agenda.Description) != "" || strings.TrimSpace(agenda.Goal) != "" || len(agenda.SemanticHints) > 0 {
		return false
	}
	core := semanticTopicCore(agenda.Title)
	for _, generic := range []string{"今後", "項目", "事項", "全体", "その他", "検証"} {
		core = strings.ReplaceAll(core, generic, "")
	}
	return len([]rune(core)) < 3
}

func agendaProgressStatusForID(progress *agendaProgressState, agendaID string) string {
	if progress == nil {
		return ""
	}
	for _, entry := range progress.Entries {
		if entry.ID == agendaID {
			return entry.ComputedStatus
		}
	}
	return ""
}

func agendaCandidateTextForItem(item liveAnalysisItem, newTopics []liveAnalysisTreeNode, candidates []emergingTopicCandidate, tree *liveAnalysisTree) string {
	var parts []string
	for _, topic := range newTopics {
		if topic.ID == item.CandidateTopicID {
			parts = append(parts, topic.Label, topic.Description)
		}
	}
	for _, candidate := range candidates {
		if candidate.ID == item.CandidateTopicID || containsExactString(candidate.ModelTopicIDs, item.CandidateTopicID) {
			parts = append(parts, candidate.Label, candidate.Description)
		}
	}
	if topicID := treeItemTopic(tree, item.ID); topicID != "" && tree != nil {
		for _, node := range tree.Nodes {
			if node.ID == topicID && node.Kind == "topic" {
				parts = append(parts, node.Label, node.Description)
				break
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func evidenceTextForAgendaMatch(item liveAnalysisItem, candidateText string, scope liveEvidenceScope) string {
	parts := []string{item.Title, item.Body, candidateText}
	primary := make([]string, 0, len(item.EvidenceSequenceNos))
	fallback := make([]string, 0, len(item.EvidenceSequenceNos))
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if text := strings.TrimSpace(scope.TranscriptText[sequenceNo]); text != "" {
			switch scope.EvidenceRoles[sequenceNo] {
			case liveEvidenceReferenceRecap, liveEvidenceDiscourseOnly:
				fallback = append(fallback, text)
			default:
				primary = append(primary, text)
			}
		}
	}
	// Primary/source evidence determines identity and agenda. A recap is only a
	// fallback when the snapshot genuinely has no substantive source text.
	if len(primary) > 0 {
		parts = append(parts, primary...)
	} else {
		parts = append(parts, fallback...)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func agendaEvidenceIsSubstantive(item liveAnalysisItem, agenda agendaItem, evidenceText string, timeline discourseTimeline) (bool, string) {
	if item.Inactive || item.MergedIntoID != "" || item.Status == "dismissed" {
		return false, "inactive_or_merged_item"
	}
	if agendaSemanticIdentityIsBroad(agenda) {
		return false, "broad_agenda_without_metadata"
	}
	primaryEvidence := len(item.EvidenceSequenceNos) == 0
	if len(item.EvidenceSequenceNos) > 0 {
		primaryEvidence = false
		for _, sequenceNo := range item.EvidenceSequenceNos {
			switch timeline.Roles[sequenceNo] {
			case liveEvidencePrimary, liveEvidenceSupporting, liveEvidenceCorrection:
				primaryEvidence = true
			}
		}
	}
	if !primaryEvidence {
		return false, "reference_or_discourse_only"
	}
	normalizedEvidence := semanticItemKey(evidenceText)
	if normalizedEvidence == "" || len([]rune(normalizedEvidence)) < 8 {
		return false, "low_information_evidence"
	}
	if agendaPreviewOnlyPattern.MatchString(evidenceText) {
		return false, "preview_only"
	}
	if agendaNegativeOnlyPattern.MatchString(evidenceText) {
		return false, "negative_or_unperformed_only"
	}
	titleKey := semanticItemKey(agenda.Title)
	residual := normalizedEvidence
	if titleKey != "" {
		residual = strings.ReplaceAll(residual, titleKey, "")
	}
	core := semanticTopicCore(agenda.Title)
	if core != "" {
		residual = strings.ReplaceAll(residual, core, "")
	}
	if len([]rune(residual)) < 6 {
		return false, "agenda_name_only"
	}
	return true, ""
}

func agendaEvidenceScore(agenda agendaItem, item liveAnalysisItem, candidateText, evidenceText string) float64 {
	agendaText := agendaSemanticText(agenda)
	score := semanticItemSimilarity(agendaText, evidenceText)
	for _, value := range []float64{
		semanticItemSimilarity(agenda.Title, item.Title+" "+item.Body),
		semanticItemSimilarity(agenda.Title, candidateText),
		semanticItemSimilarity(agenda.Description+" "+agenda.Goal, evidenceText),
	} {
		if value > score {
			score = value
		}
	}
	agendaCore := semanticTopicCore(agenda.Title)
	evidenceCore := semanticTopicCore(evidenceText)
	if len([]rune(agendaCore)) >= 3 &&
		(strings.Contains(evidenceCore, agendaCore) || strings.Contains(agendaCore, semanticTopicCore(item.Title))) &&
		score < 0.72 {
		score = 0.72
	}
	hintHits := 0
	evidenceKey := semanticItemKey(evidenceText)
	for _, hint := range agenda.SemanticHints {
		hintKey := semanticItemKey(hint)
		if len([]rune(hintKey)) >= 2 &&
			(strings.Contains(evidenceKey, hintKey) ||
				semanticHintComponentMatch(hint, evidenceKey)) {
			hintHits++
		}
	}
	switch {
	case hintHits >= 2 && score < 0.82:
		score = 0.82
	case hintHits == 1 && score < 0.68:
		score = 0.68
	}
	// Shared object hints (for example the same configuration name) do not
	// distinguish a diagnostic finding from a later prevention policy.
	// Compare the agenda's semantic role with the evidence predicate/causal
	// role so an earlier cause finding can beat an active prevention span.
	propositionText := item.Title + " " + item.Body
	switch {
	case agendaInvestigationRolePattern.MatchString(agendaText) &&
		evidenceDiagnosticRolePattern.MatchString(propositionText) &&
		!evidencePreventionRolePattern.MatchString(propositionText):
		if score < 0.82 {
			score = 0.82
		}
	case agendaRecoveryRolePattern.MatchString(agendaText) &&
		evidenceRecoveryRolePattern.MatchString(propositionText) &&
		!evidencePreventionRolePattern.MatchString(propositionText):
		if score < 0.82 {
			score = 0.82
		}
	case agendaPreventionRolePattern.MatchString(agendaText) &&
		evidencePreventionRolePattern.MatchString(propositionText):
		if score < 0.82 {
			score = 0.82
		}
	}
	return score
}

var agendaSemanticHintComponentPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_-]*|[\p{Katakana}ー]{2,}|[\p{Han}々]{2,}`)

func semanticHintComponentMatch(hint, evidenceKey string) bool {
	components := agendaSemanticHintComponentPattern.FindAllString(hint, -1)
	matched := 0
	for _, component := range components {
		key := semanticItemKey(component)
		if len([]rune(key)) < 2 {
			continue
		}
		if !strings.Contains(evidenceKey, key) {
			return false
		}
		matched++
	}
	return matched >= 2
}

func bestAgendaEvidenceMatch(item liveAnalysisItem, candidateText string, agendas []agendaItem, scope liveEvidenceScope, timeline discourseTimeline) (agendaItem, float64, []string, []string, string) {
	evidenceText := evidenceTextForAgendaMatch(item, candidateText, scope)
	scored := make([]agendaMatchCandidate, 0, len(agendas))
	rejection := ""
	for _, agenda := range agendas {
		if effectiveAgendaRole(agenda.Role, agenda.Title, agenda.Description) == agendaRoleActionSummary {
			continue
		}
		if substantive, reason := agendaEvidenceIsSubstantive(item, agenda, evidenceText, timeline); !substantive {
			rejection = reason
			continue
		}
		scored = append(scored, agendaMatchCandidate{agenda: agenda, score: agendaEvidenceScore(agenda, item, candidateText, evidenceText)})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].agenda.Order < scored[j].agenda.Order
	})
	ids := make([]string, 0, len(scored))
	scores := make([]string, 0, len(scored))
	for _, candidate := range scored {
		ids = append(ids, candidate.agenda.ID)
		scores = append(scores, candidate.agenda.ID+":"+strconv.FormatFloat(candidate.score, 'f', 2, 64))
	}
	if len(scored) == 0 {
		if rejection == "" {
			rejection = "no_primary_agenda_candidates"
		}
		return agendaItem{}, 0, ids, scores, rejection
	}
	if scored[0].score < agendaReconciliationMinScore {
		return agendaItem{}, scored[0].score, ids, scores, "score_below_threshold"
	}
	if len(scored) > 1 && scored[0].score-scored[1].score < agendaReconciliationMinMargin {
		return agendaItem{}, scored[0].score, ids, scores, "ambiguous_agenda_match"
	}
	return scored[0].agenda, scored[0].score, ids, scores, ""
}

func agendaIDsFromMeetingContext(mc *meetingContext) map[string]struct{} {
	result := make(map[string]struct{})
	if mc == nil {
		return result
	}
	for _, agenda := range mc.Agenda {
		if effectiveAgendaRole(agenda.Role, agenda.Title, agenda.Description) == agendaRolePrimary {
			result[agenda.ID] = struct{}{}
		}
	}
	return result
}

func concreteTopicIDs(tree *liveAnalysisTree) map[string]struct{} {
	result := make(map[string]struct{})
	if tree == nil {
		return result
	}
	for _, node := range tree.Nodes {
		if node.Kind == "topic" {
			result[node.ID] = struct{}{}
		}
	}
	return result
}

func problematicAgendaParent(parentID string, newTopics map[string]liveAnalysisTreeNode, agendas, topics map[string]struct{}) bool {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" || parentID == treeUnclassifiedTopicID || strings.HasPrefix(parentID, "topic-unknown-") {
		return true
	}
	if _, agenda := agendas[parentID]; agenda {
		return false
	}
	if _, topic := topics[parentID]; topic {
		return false
	}
	if _, proposed := newTopics[parentID]; proposed {
		return true
	}
	return true
}

func replaceItemAssignments(assignments []treeAssignment, itemID string, replacement treeAssignment) []treeAssignment {
	result := make([]treeAssignment, 0, len(assignments)+1)
	for _, assignment := range assignments {
		if assignment.nodeID() == itemID {
			continue
		}
		result = append(result, assignment)
	}
	return append(result, replacement)
}

func currentAgendaForEvidence(item liveAnalysisItem, spans []agendaContextSpan) string {
	mode, agendaID, _ := agendaContextForEvidence(item.EvidenceSequenceNos, spans)
	if mode != agendaContextModeFixed {
		return ""
	}
	return agendaID
}

func activeAgendaFallbackForEvidence(item liveAnalysisItem, spans []agendaContextSpan, previous *agendaProgressState, mc *meetingContext) string {
	candidate := currentAgendaForEvidence(item, spans)
	if candidate == "" && previous != nil {
		candidate = strings.TrimSpace(previous.ComputedCurrentTopicID)
	}
	if candidate == "" || mc == nil {
		return ""
	}
	for _, agenda := range mc.Agenda {
		if agenda.ID == candidate &&
			effectiveAgendaRole(agenda.Role, agenda.Title, agenda.Description) == agendaRolePrimary {
			return candidate
		}
	}
	return ""
}

// reconcileDynamicCandidateAssignments runs after active-span correction but
// before rebuildDiscussionTree creates candidates. Strong, unique matches are
// rewritten to the logical agenda anchor, allowing the ordinary materializer
// to create a separate topic ID and preventing a one-turn agenda discussion
// from being stranded behind the multi-round dynamic-promotion gate.
func reconcileDynamicCandidateAssignments(
	assignments []treeAssignment,
	newTopics []liveAnalysisTreeNode,
	previous liveAnalysisPayload,
	items []liveAnalysisItem,
	changed []liveAnalysisItem,
	mc *meetingContext,
	spans []agendaContextSpan,
	timeline discourseTimeline,
	scope liveEvidenceScope,
	stats *liveAnalysisTreeMergeStats,
) []treeAssignment {
	if mc == nil || len(mc.Agenda) == 0 || len(changed) == 0 {
		return assignments
	}
	agendaIDs := agendaIDsFromMeetingContext(mc)
	topicIDs := concreteTopicIDs(previous.Tree)
	type agendaModelTopicAlias struct {
		agendaID string
		text     string
	}
	agendaAliasByModelTopicID := make(map[string][]agendaModelTopicAlias)
	if previous.Tree != nil {
		records := agendaRecordMap(mc)
		for _, topic := range previous.Tree.Nodes {
			if topic.Kind != "topic" {
				continue
			}
			refs := topicAgendaRefs(topic, records)
			if len(refs) == 0 {
				continue
			}
			for _, modelTopicID := range topic.ModelTopicIDs {
				if modelTopicID != "" {
					for _, agendaID := range refs {
						agendaAliasByModelTopicID[modelTopicID] = append(agendaAliasByModelTopicID[modelTopicID], agendaModelTopicAlias{
							agendaID: agendaID, text: strings.TrimSpace(topic.Label + " " + topic.Description),
						})
					}
				}
			}
		}
	}
	proposedTopics := make(map[string]liveAnalysisTreeNode, len(newTopics))
	for _, topic := range newTopics {
		proposedTopics[topic.ID] = topic
	}
	itemByID := make(map[string]liveAnalysisItem, len(items))
	for _, item := range items {
		itemByID[item.ID] = item
	}
	for _, changedItem := range changed {
		item := itemByID[changedItem.ID]
		if span, found := agendaContextSpanForEvidence(item.EvidenceSequenceNos, spans); found && span.Mode == agendaContextModeNoAgenda && span.Explicit {
			continue
		}
		currentRoundEvidence := len(item.EvidenceSequenceNos) == 0
		for _, sequenceNo := range item.EvidenceSequenceNos {
			if _, current := scope.CurrentRound[sequenceNo]; current {
				currentRoundEvidence = true
				break
			}
		}
		if !currentRoundEvidence {
			continue
		}
		candidateText := agendaCandidateTextForItem(item, newTopics, previous.EmergingTopics, previous.Tree)
		dynamicSignal := item.ClassificationStatus == classificationTentative || item.CandidateTopicID != ""
		unclassifiedSignal := item.ClassificationStatus == classificationUnclassified
		fixedAgendaAssignment := false
		assignmentSeen := false
		candidateParentID := ""
		for _, assignment := range assignments {
			if assignment.nodeID() != item.ID {
				continue
			}
			assignmentSeen = true
			if _, planned := agendaIDs[assignment.ParentTopicID]; planned {
				fixedAgendaAssignment = true
				continue
			}
			if aliases := agendaAliasByModelTopicID[assignment.ParentTopicID]; len(aliases) > 0 {
				proposed := proposedTopics[assignment.ParentTopicID]
				aliasEvidence := candidateText + " " + proposed.Label + " " + proposed.Description + " " + item.Title + " " + item.Body
				for _, alias := range aliases {
					if semanticItemSimilarity(alias.text, aliasEvidence) >= candidateSubjectCoherenceThreshold {
						dynamicSignal = true
						candidateParentID = assignment.ParentTopicID
						candidateText = strings.TrimSpace(candidateText + " " + alias.text)
					}
				}
				if candidateParentID != "" {
					continue
				}
			}
			if previous.Tree != nil {
				for _, topic := range previous.Tree.Nodes {
					if topic.ID == assignment.ParentTopicID && topic.Kind == "topic" &&
						topic.Origin == topicOriginDynamic && len(topic.AgendaRefs) == 0 {
						dynamicSignal = true
						candidateText = strings.TrimSpace(candidateText + " " + topic.Label + " " + topic.Description)
						break
					}
				}
			}
			if problematicAgendaParent(assignment.ParentTopicID, proposedTopics, agendaIDs, topicIDs) {
				if topic := proposedTopics[assignment.ParentTopicID]; topic.ID != "" {
					dynamicSignal = true
					candidateParentID = assignment.ParentTopicID
					candidateText = strings.TrimSpace(candidateText + " " + topic.Label + " " + topic.Description)
				} else if assignment.ParentTopicID == "" || assignment.ParentTopicID == treeUnclassifiedTopicID {
					unclassifiedSignal = true
				} else {
					dynamicSignal = true
				}
			}
		}
		// A model omission is still an unclassified proposal for every
		// transcript-grounded canonical item, not only high-severity/action
		// kinds. Otherwise an ordinary Fact such as an initial investigation
		// result bypasses planned-agenda matching and becomes a permanent
		// "追加論点".
		if !assignmentSeen && currentRoundEvidence &&
			!item.Inactive && item.MergedIntoID == "" && len(item.EvidenceSequenceNos) > 0 {
			unclassifiedSignal = true
		}
		if !dynamicSignal && !unclassifiedSignal {
			continue
		}
		if fixedAgendaAssignment && !dynamicSignal {
			continue
		}
		// An explicit unclassified assignment is not, by itself, evidence that
		// the model lost a planned agenda. Require transcript-backed evidence in
		// that weaker case. Dynamic/new-topic proposals remain eligible because
		// they are the precise failure mode this reconciliation repairs.
		if !dynamicSignal {
			transcriptGrounded := false
			for _, sequenceNo := range item.EvidenceSequenceNos {
				if strings.TrimSpace(scope.TranscriptText[sequenceNo]) != "" {
					transcriptGrounded = true
					break
				}
			}
			if !transcriptGrounded {
				continue
			}
		}
		eligible := make([]agendaItem, 0, len(mc.Agenda))
		for _, agenda := range mc.Agenda {
			if agendaProgressStatusForID(previous.AgendaProgress, agenda.ID) == agendaProgressDiscussed {
				continue
			}
			eligible = append(eligible, agenda)
		}
		selected, score, candidateIDs, candidateScores, rejected := bestAgendaEvidenceMatch(item, candidateText, eligible, scope, timeline)
		decision := agendaReconciliationDecision{
			Trigger: agendaReconciliationDynamicCandidate, ItemID: item.ID,
			EvidenceSequenceNos:   append([]int64(nil), item.EvidenceSequenceNos...),
			CurrentActiveAgendaID: currentAgendaForEvidence(item, spans),
			CandidateAgendaIDs:    candidateIDs, CandidateScores: candidateScores,
			SelectedAgendaID: selected.ID, Score: score, RejectedReason: rejected,
			PreviousStatus:          agendaProgressStatusForID(previous.AgendaProgress, selected.ID),
			DynamicCandidateChecked: true,
		}
		if selected.ID == "" {
			if fallback := initialAgendaForSelfContainedCorrection(item, items, spans, mc); fallback.ID != "" {
				const fallbackConfidence = 0.55
				assignments = replaceItemAssignments(assignments, item.ID, treeAssignment{
					NodeID: item.ID, ParentTopicID: fallback.ID, Confidence: fallbackConfidence,
					Reason: "self-contained correction before first agenda transition", ServerSource: assignmentSourceRule,
					ModelParentTopicID:  candidateParentID,
					EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...),
				})
				decision.SelectedAgendaID = fallback.ID
				decision.Score = fallbackConfidence
				decision.RejectedReason = "initial_agenda_self_contained_correction_fallback"
				decision.NewStatus = agendaProgressDiscussing
				decision.AgendaRefsRepaired = true
				decision.ItemMoved = true
				if stats != nil {
					stats.AgendaReconciliations = append(stats.AgendaReconciliations, decision)
				}
				continue
			}
			if !dynamicSignal && rejected == "score_below_threshold" &&
				score >= agendaReconciliationPendingMinScore &&
				len(candidateIDs) > 0 &&
				agendaCandidateScoreMargin(candidateScores) >= agendaReconciliationMinMargin {
				pendingAgendaID := candidateIDs[0]
				for _, agenda := range mc.Agenda {
					if agenda.ID != pendingAgendaID ||
						effectiveAgendaRole(agenda.Role, agenda.Title, agenda.Description) != agendaRolePrimary {
						continue
					}
					assignments = replaceItemAssignments(assignments, item.ID, treeAssignment{
						NodeID: item.ID, ParentTopicID: pendingAgendaID, Confidence: score,
						Reason: "planned_agenda_match_pending", ServerSource: assignmentSourceRule,
						ModelParentTopicID:  candidateParentID,
						EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...),
					})
					decision.SelectedAgendaID = pendingAgendaID
					decision.RejectedReason = "score_below_threshold_pending"
					decision.ItemMoved = true
					break
				}
			}
			if rejected == "ambiguous_agenda_match" {
				fallbackAgendaID := activeAgendaFallbackForEvidence(item, spans, previous.AgendaProgress, mc)
				fallbackParentID := fallbackAgendaID
				fallbackReason := "ambiguous_agenda_match_active_fallback"
				fallbackConfidence := 0.55
				if fallbackParentID == "" {
					fallbackParentID = treeUnclassifiedTopicID
					fallbackReason = "ambiguous_agenda_match_unclassified_fallback"
					fallbackConfidence = 0
				}
				assignments = replaceItemAssignments(assignments, item.ID, treeAssignment{
					NodeID: item.ID, ParentTopicID: fallbackParentID, Confidence: fallbackConfidence,
					Reason: agendaReconciliationAmbiguousFallback, ServerSource: assignmentSourceRule,
					ModelParentTopicID:  candidateParentID,
					EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...),
				})
				decision.SelectedAgendaID = fallbackAgendaID
				decision.Score = fallbackConfidence
				decision.RejectedReason = fallbackReason
				decision.AgendaRefsRepaired = fallbackAgendaID != ""
				decision.ItemMoved = true
				if fallbackAgendaID != "" {
					decision.NewStatus = agendaProgressDiscussing
				}
			}
			if stats != nil {
				stats.AgendaReconciliations = append(stats.AgendaReconciliations, decision)
			}
			continue
		}
		assignments = replaceItemAssignments(assignments, item.ID, treeAssignment{
			NodeID: item.ID, ParentTopicID: selected.ID, Confidence: score,
			Reason: agendaReconciliationDynamicCandidate, ServerSource: assignmentSourceRule,
			ModelParentTopicID:  candidateParentID,
			EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...),
		})
		decision.NewStatus = agendaProgressDiscussing
		decision.AgendaRefsRepaired = true
		decision.ItemMoved = true
		if stats != nil {
			stats.AgendaReconciliations = append(stats.AgendaReconciliations, decision)
		}
	}
	return assignments
}

// initialAgendaForSelfContainedCorrection is a narrow fallback for a
// server-reconstructed correction before the meeting records its first topic
// transition. It does not lower semantic matching thresholds: once any agenda
// or no-agenda span has started, normal span and candidate reconciliation stay
// authoritative.
func initialAgendaForSelfContainedCorrection(item liveAnalysisItem, items []liveAnalysisItem, spans []agendaContextSpan, mc *meetingContext) agendaItem {
	if item.AssignmentReason != deterministicCorrectionAssignmentReason || mc == nil || len(item.EvidenceSequenceNos) == 0 {
		return agendaItem{}
	}
	itemText := item.Title + " " + item.Body
	for _, companion := range items {
		if companion.ID == item.ID || companion.Inactive || companion.MergedIntoID != "" ||
			!companion.observedInCurrentBatch || !itemEvidenceWithin(item, companion, 3) {
			continue
		}
		companionText := companion.Title + " " + companion.Body
		if sharedTreeAuditSubjectTerm(itemText, companionText) || semanticItemSimilarity(itemText, companionText) >= 0.18 {
			// Let normal semantic grouping place a same-round logical family
			// together instead of pinning one member to a planned agenda first.
			return agendaItem{}
		}
	}
	firstEvidence := item.EvidenceSequenceNos[0]
	for _, sequenceNo := range item.EvidenceSequenceNos[1:] {
		if sequenceNo < firstEvidence {
			firstEvidence = sequenceNo
		}
	}
	if mode, _, _ := agendaContextForEvidence(item.EvidenceSequenceNos, spans); mode != "" {
		return agendaItem{}
	}
	for _, span := range spans {
		if span.StartSequenceNo <= firstEvidence {
			return agendaItem{}
		}
	}
	var selected agendaItem
	for _, agenda := range mc.Agenda {
		if effectiveAgendaRole(agenda.Role, agenda.Title, agenda.Description) != agendaRolePrimary {
			continue
		}
		if selected.ID == "" || (agenda.Order > 0 && (selected.Order <= 0 || agenda.Order < selected.Order)) {
			selected = agenda
		}
	}
	return selected
}

func agendaCandidateScoreMargin(scores []string) float64 {
	if len(scores) < 2 {
		return 1
	}
	parse := func(value string) float64 {
		at := strings.LastIndex(value, ":")
		if at < 0 || at+1 >= len(value) {
			return 0
		}
		score, _ := strconv.ParseFloat(value[at+1:], 64)
		return score
	}
	return parse(scores[0]) - parse(scores[1])
}

func maxEvidenceSequence(item liveAnalysisItem) int64 {
	var result int64
	for _, value := range item.EvidenceSequenceNos {
		if value > result {
			result = value
		}
	}
	return result
}

func priorFixedAgendaForTransition(previous *agendaProgressState, spans []agendaContextSpan, transition agendaContextSpan) string {
	if previous != nil && previous.ComputedCurrentTopicID != "" {
		return previous.ComputedCurrentTopicID
	}
	selectedSequence := int64(0)
	selectedAgenda := ""
	for _, span := range spans {
		if span.Mode != agendaContextModeFixed || span.AgendaID == "" || span.StartSequenceNo >= transition.StartSequenceNo {
			continue
		}
		if span.StartSequenceNo > selectedSequence {
			selectedSequence = span.StartSequenceNo
			selectedAgenda = span.AgendaID
		}
	}
	return selectedAgenda
}

func agendasBetween(mc *meetingContext, previousID, nextID string) []agendaItem {
	if mc == nil || previousID == "" || nextID == "" {
		return nil
	}
	previousOrder, nextOrder := 0, 0
	for _, agenda := range mc.Agenda {
		if agenda.ID == previousID {
			previousOrder = agenda.Order
		}
		if agenda.ID == nextID {
			nextOrder = agenda.Order
		}
	}
	if previousOrder == 0 || nextOrder <= previousOrder+1 {
		return nil
	}
	result := make([]agendaItem, 0, nextOrder-previousOrder-1)
	for _, agenda := range mc.Agenda {
		if agenda.Order > previousOrder && agenda.Order < nextOrder &&
			effectiveAgendaRole(agenda.Role, agenda.Title, agenda.Description) == agendaRolePrimary {
			result = append(result, agenda)
		}
	}
	return result
}

// backfillSkippedAgendaAssignments reconsiders only a bounded window before a
// direct ordered jump (agenda-1 -> agenda-3). A skipped ordinal is never proof
// by itself; the item must independently win the same strong semantic matcher.
func backfillSkippedAgendaAssignments(
	assignments []treeAssignment,
	previous liveAnalysisPayload,
	items []liveAnalysisItem,
	mc *meetingContext,
	spans []agendaContextSpan,
	roundSeqNos []int64,
	timeline discourseTimeline,
	scope liveEvidenceScope,
	stats *liveAnalysisTreeMergeStats,
) []treeAssignment {
	if mc == nil || len(spans) == 0 {
		return assignments
	}
	roundSet := make(map[int64]struct{}, len(roundSeqNos))
	for _, sequenceNo := range roundSeqNos {
		roundSet[sequenceNo] = struct{}{}
	}
	for _, transition := range spans {
		if transition.Mode != agendaContextModeFixed || !transition.Explicit || transition.AgendaID == "" {
			continue
		}
		if _, currentRound := roundSet[transition.StartSequenceNo]; !currentRound {
			continue
		}
		previousAgendaID := priorFixedAgendaForTransition(previous.AgendaProgress, spans, transition)
		skipped := agendasBetween(mc, previousAgendaID, transition.AgendaID)
		if len(skipped) == 0 {
			continue
		}
		for _, agenda := range skipped {
			if status := agendaProgressStatusForID(previous.AgendaProgress, agenda.ID); status != "" && status != agendaProgressNotStarted {
				continue
			}
			bestItem := liveAnalysisItem{}
			bestScore := 0.0
			var bestCandidateIDs, bestCandidateScores []string
			bestRejected := "no_recent_candidate_item"
			for _, item := range items {
				sequenceNo := maxEvidenceSequence(item)
				if sequenceNo <= 0 || sequenceNo >= transition.StartSequenceNo || transition.StartSequenceNo-sequenceNo > 4 {
					continue
				}
				topicID := treeItemTopic(previous.Tree, item.ID)
				if topicID != "" && topicID != treeUnclassifiedTopicID {
					var topic liveAnalysisTreeNode
					for _, node := range previous.Tree.Nodes {
						if node.ID == topicID {
							topic = node
							break
						}
					}
					if len(topic.AgendaRefs) > 0 || topic.Origin == topicOriginAgenda || topic.Origin == topicOriginMixed {
						continue
					}
				}
				candidateText := agendaCandidateTextForItem(item, nil, previous.EmergingTopics, previous.Tree)
				selected, score, candidateIDs, candidateScores, rejected := bestAgendaEvidenceMatch(item, candidateText, mc.Agenda, scope, timeline)
				if selected.ID != agenda.ID || score <= bestScore {
					if bestItem.ID == "" && rejected != "" {
						bestRejected = rejected
					}
					continue
				}
				bestItem, bestScore = item, score
				bestCandidateIDs, bestCandidateScores = candidateIDs, candidateScores
				bestRejected = ""
			}
			decision := agendaReconciliationDecision{
				Trigger: agendaReconciliationSkipBackfill, ItemID: bestItem.ID,
				EvidenceSequenceNos:   append([]int64(nil), bestItem.EvidenceSequenceNos...),
				CurrentActiveAgendaID: previousAgendaID, CandidateAgendaIDs: bestCandidateIDs,
				TransitionNextAgendaID: transition.AgendaID,
				SkippedAgendaIDs: func() []string {
					ids := make([]string, 0, len(skipped))
					for _, skippedAgenda := range skipped {
						ids = append(ids, skippedAgenda.ID)
					}
					return ids
				}(),
				TransitionDirect: true,
				CandidateScores:  bestCandidateScores, SelectedAgendaID: agenda.ID, Score: bestScore,
				PreviousStatus: agendaProgressStatusForID(previous.AgendaProgress, agenda.ID),
				RejectedReason: bestRejected, DynamicCandidateChecked: true,
			}
			if bestItem.ID != "" {
				assignments = replaceItemAssignments(assignments, bestItem.ID, treeAssignment{
					NodeID: bestItem.ID, ParentTopicID: agenda.ID, Confidence: bestScore,
					Reason: agendaReconciliationSkipBackfill, ServerSource: assignmentSourceRule,
					EvidenceSequenceNos: append([]int64(nil), bestItem.EvidenceSequenceNos...),
				})
				decision.NewStatus = agendaProgressDiscussed
				decision.AgendaRefsRepaired = true
				decision.ItemMoved = true
				decision.BackfillPerformed = true
			}
			if stats != nil {
				stats.AgendaReconciliations = append(stats.AgendaReconciliations, decision)
			}
		}
	}
	return assignments
}

func safelyReconcileLiveAgendaAssignments(
	assignments []treeAssignment,
	newTopics []liveAnalysisTreeNode,
	previous liveAnalysisPayload,
	items []liveAnalysisItem,
	changed []liveAnalysisItem,
	mc *meetingContext,
	spans []agendaContextSpan,
	roundSeqNos []int64,
	timeline discourseTimeline,
	scope liveEvidenceScope,
	stats *liveAnalysisTreeMergeStats,
) (result []treeAssignment) {
	result = append([]treeAssignment(nil), assignments...)
	defer func() {
		if recovered := recover(); recovered != nil {
			result = append([]treeAssignment(nil), assignments...)
			if stats != nil {
				stats.AgendaReconciliations = append(stats.AgendaReconciliations, agendaReconciliationDecision{
					Trigger: agendaReconciliationDynamicCandidate, RejectedReason: "reconciliation_panic",
				})
			}
		}
	}()
	result = reconcileDynamicCandidateAssignments(result, newTopics, previous, items, changed, mc, spans, timeline, scope, stats)
	result = backfillSkippedAgendaAssignments(result, previous, items, mc, spans, roundSeqNos, timeline, scope, stats)
	return result
}

func agendaTimelineFromSegments(segments []domain.TranscriptSegment) (liveEvidenceScope, discourseTimeline) {
	scope := liveEvidenceScope{
		Allowed: make(map[int64]struct{}), CurrentRound: make(map[int64]struct{}),
		TranscriptText: make(map[int64]string), Segments: make(map[int64]domain.TranscriptSegment),
	}
	for _, segment := range segments {
		if !segment.IsFinal || segment.SequenceNo <= 0 || strings.TrimSpace(segment.Text) == "" {
			continue
		}
		scope.Allowed[segment.SequenceNo] = struct{}{}
		scope.CurrentRound[segment.SequenceNo] = struct{}{}
		scope.TranscriptText[segment.SequenceNo] = strings.TrimSpace(segment.Text)
		scope.Segments[segment.SequenceNo] = segment
		if segment.SequenceNo > scope.CoveredThrough {
			scope.CoveredThrough = segment.SequenceNo
		}
	}
	timeline := classifyDiscourseTimeline(scope)
	scope.EvidenceRoles = timeline.Roles
	return scope, timeline
}

func topicNodeForItem(tree *liveAnalysisTree, itemID string) (liveAnalysisTreeNode, bool) {
	topicID := treeItemTopic(tree, itemID)
	if topicID == "" || tree == nil {
		return liveAnalysisTreeNode{}, false
	}
	for _, node := range tree.Nodes {
		if node.ID == topicID {
			return node, true
		}
	}
	return liveAnalysisTreeNode{}, false
}

func rebuildTreeEdges(tree *liveAnalysisTree) {
	if tree == nil {
		return
	}
	tree.Edges = tree.Edges[:0]
	for _, node := range tree.Nodes {
		if node.ID != treeRootNodeID && node.ParentID != "" {
			tree.Edges = append(tree.Edges, liveAnalysisTreeEdge{Source: node.ParentID, Target: node.ID})
		}
	}
}

func reconcileFinalAgendaEvidence(state *liveAnalysisPayload, mc *meetingContext, segments []domain.TranscriptSegment, treeVersion int64) []agendaReconciliationDecision {
	if state == nil || state.Tree == nil || mc == nil || len(mc.Agenda) == 0 {
		return nil
	}
	scope, timeline := agendaTimelineFromSegments(segments)
	anchors := reconcileAgendaAnchors(state.AgendaAnchors, mc, state.Tree, state.Items, treeVersion, false)
	anchorByID := make(map[string]agendaAnchor, len(anchors))
	for _, anchor := range anchors {
		anchorByID[anchor.AgendaID] = anchor
	}
	eligible := make([]agendaItem, 0)
	for _, agenda := range mc.Agenda {
		if effectiveAgendaRole(agenda.Role, agenda.Title, agenda.Description) == agendaRolePrimary {
			eligible = append(eligible, agenda)
		}
	}
	if len(eligible) == 0 {
		return nil
	}
	records := agendaRecordMap(mc)
	nodes := make(map[string]liveAnalysisTreeNode, len(state.Tree.Nodes))
	for _, node := range state.Tree.Nodes {
		nodes[node.ID] = node
	}
	type repair struct {
		itemAt int
		agenda agendaItem
		score  float64
		ids    []string
		scores []string
	}
	repairs := make([]repair, 0)
	for itemAt, item := range state.Items {
		if item.Inactive || item.MergedIntoID != "" {
			continue
		}
		topic, hasTopic := topicNodeForItem(state.Tree, item.ID)
		if node := nodes[item.ID]; treeAuditIsManualChangeSource(node.LastParentChangeSource) {
			continue
		}
		currentAgendaIDs := topicAgendaRefs(topic, records)
		currentAgendaID := ""
		if hasTopic && len(currentAgendaIDs) == 1 {
			currentAgendaID = currentAgendaIDs[0]
		}
		candidateText := agendaCandidateTextForItem(item, nil, state.EmergingTopics, state.Tree)
		selected, score, candidateIDs, candidateScores, _ := bestAgendaEvidenceMatch(item, candidateText, eligible, scope, timeline)
		if selected.ID == "" || selected.ID == currentAgendaID {
			continue
		}
		// Moving an already classified item is deliberately stricter than
		// filling an unclassified one. This repairs recap contamination without
		// turning weak topical similarity into churn.
		if currentAgendaID != "" && score < 0.68 {
			continue
		}
		repairs = append(repairs, repair{itemAt: itemAt, agenda: selected, score: score, ids: candidateIDs, scores: candidateScores})
	}
	sort.SliceStable(repairs, func(i, j int) bool { return repairs[i].score > repairs[j].score })
	repairedAgenda := make(map[string]struct{})
	decisions := make([]agendaReconciliationDecision, 0, len(repairs))
	for _, candidate := range repairs {
		item := &state.Items[candidate.itemAt]
		previousParent := ""
		for _, node := range state.Tree.Nodes {
			if node.ID == item.ID {
				previousParent = node.ParentID
				break
			}
		}
		topicID, reused := availableAgendaTopicID(candidate.agenda.ID, nodes, records)
		if !reused {
			topic := liveAnalysisTreeNode{
				ID: topicID, Kind: "topic", ParentID: treeRootNodeID,
				Label:       truncateRunes(item.Title, liveAnalysisTopicLabelMaxRunes),
				Description: truncateRunes(item.Body, liveAnalysisTreeDescriptionMaxRunes),
				Origin:      topicOriginAgenda, AgendaRole: agendaRolePrimary,
				AgendaRefs: []string{candidate.agenda.ID}, Materialized: true,
				CreatedAtVersion: treeVersion, UpdatedAtVersion: treeVersion,
			}
			state.Tree.Nodes = append(state.Tree.Nodes, topic)
			nodes[topicID] = topic
		}
		for nodeAt := range state.Tree.Nodes {
			if state.Tree.Nodes[nodeAt].ID != item.ID {
				continue
			}
			state.Tree.Nodes[nodeAt].ParentID = topicID
			state.Tree.Nodes[nodeAt].LastParentChangeVersion = treeVersion
			state.Tree.Nodes[nodeAt].LastParentChangeSource = agendaReconciliationFinalization
			break
		}
		item.ClassificationStatus = classificationAssigned
		item.AssignmentSource = assignmentSourceRule
		item.AssignmentReason = agendaReconciliationFinalization
		item.AssignmentConfidence = candidate.score
		oldCandidateID := item.CandidateTopicID
		item.CandidateTopicID = ""
		item.CandidateInactive = false
		for candidateAt := range state.EmergingTopics {
			if state.EmergingTopics[candidateAt].ID != oldCandidateID {
				continue
			}
			kept := state.EmergingTopics[candidateAt].EvidenceItemIDs[:0]
			for _, itemID := range state.EmergingTopics[candidateAt].EvidenceItemIDs {
				if itemID != item.ID {
					kept = append(kept, itemID)
				}
			}
			state.EmergingTopics[candidateAt].EvidenceItemIDs = kept
		}
		keptCandidates := state.EmergingTopics[:0]
		for _, emerging := range state.EmergingTopics {
			if len(emerging.EvidenceItemIDs) > 0 {
				keptCandidates = append(keptCandidates, emerging)
			}
		}
		state.EmergingTopics = keptCandidates
		repairedAgenda[candidate.agenda.ID] = struct{}{}
		decisions = append(decisions, agendaReconciliationDecision{
			Trigger: agendaReconciliationFinalization, ItemID: item.ID,
			EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...),
			CandidateAgendaIDs:  candidate.ids, CandidateScores: candidate.scores,
			SelectedAgendaID: candidate.agenda.ID, Score: candidate.score,
			PreviousStatus: anchorByID[candidate.agenda.ID].Status, NewStatus: agendaProgressDiscussed,
			AgendaRefsRepaired: true, ItemMoved: previousParent != topicID,
			PreviousParentID: previousParent, SelectedMaterializedID: topicID,
			DynamicCandidateChecked: true,
		})
	}
	if len(decisions) > 0 {
		rebuildTreeEdges(state.Tree)
		pruneEmptyDynamicTopics(state.Tree)
		rebuildTreeEdges(state.Tree)
	}
	for _, agenda := range eligible {
		if _, repaired := repairedAgenda[agenda.ID]; repaired {
			continue
		}
		status := anchorByID[agenda.ID].Status
		if status != "" && status != agendaStatusPlanned && status != agendaStatusNotDiscussed {
			continue
		}
		reason := "no_strong_unique_match"
		if agendaSemanticIdentityIsBroad(agenda) {
			reason = "broad_agenda_without_metadata"
		} else if len(segments) == 0 {
			reason = "no_final_transcript_evidence"
		}
		decisions = append(decisions, agendaReconciliationDecision{
			Trigger: agendaReconciliationFinalization, CandidateAgendaIDs: []string{agenda.ID},
			PreviousStatus: anchorByID[agenda.ID].Status, RejectedReason: reason,
			DynamicCandidateChecked: true,
		})
	}
	return decisions
}

func safelyReconcileFinalAgendaEvidence(state *liveAnalysisPayload, mc *meetingContext, segments []domain.TranscriptSegment, treeVersion int64) (decisions []agendaReconciliationDecision) {
	if state == nil {
		return nil
	}
	original := cloneLiveAnalysisPayload(*state)
	defer func() {
		if recovered := recover(); recovered != nil {
			*state = original
			decisions = []agendaReconciliationDecision{{
				Trigger: agendaReconciliationFinalization, RejectedReason: "reconciliation_panic",
			}}
		}
	}()
	return reconcileFinalAgendaEvidence(state, mc, segments, treeVersion)
}

func logAgendaReconciliations(sessionID string, treeVersion int64, decisions []agendaReconciliationDecision) {
	finalCandidateIDs := make(map[string]struct{})
	finalRepairedIDs := make(map[string]struct{})
	finalNoChangeIDs := make(map[string]struct{})
	skipCandidates, skipBackfilled, manualProtected := 0, 0, 0
	for _, decision := range decisions {
		switch decision.Trigger {
		case agendaReconciliationFinalization:
			agendaID := decision.SelectedAgendaID
			if agendaID == "" && len(decision.CandidateAgendaIDs) == 1 {
				agendaID = decision.CandidateAgendaIDs[0]
			}
			if agendaID != "" {
				finalCandidateIDs[agendaID] = struct{}{}
			}
			if decision.AgendaRefsRepaired {
				finalRepairedIDs[agendaID] = struct{}{}
			} else {
				finalNoChangeIDs[agendaID] = struct{}{}
			}
		case agendaReconciliationSkipBackfill:
			skipCandidates++
			if decision.BackfillPerformed {
				skipBackfilled++
			}
		}
		if decision.ManualOverride {
			manualProtected++
		}
		log.Printf("Agenda reconciliation evaluated. event=agenda_assignment_decision sessionId=%s treeVersion=%d trigger=%s transcriptSequenceNos=%v itemId=%s currentActiveAgendaId=%s transitionNextAgendaId=%s skippedAgendaIds=%v transitionDirect=%t backfillPerformed=%t candidateAgendaIds=%v candidateScores=%v selectedAgendaId=%s score=%.2f previousStatus=%s newStatus=%s manualOverride=%t agendaRefsRepaired=%t itemMoved=%t previousParentId=%s materializedTopicId=%s dynamicCandidateChecked=%t rejectedReason=%s",
			sessionID, treeVersion, decision.Trigger, decision.EvidenceSequenceNos, decision.ItemID,
			decision.CurrentActiveAgendaID, decision.TransitionNextAgendaID, decision.SkippedAgendaIDs,
			decision.TransitionDirect, decision.BackfillPerformed, decision.CandidateAgendaIDs, decision.CandidateScores,
			decision.SelectedAgendaID, decision.Score, decision.PreviousStatus, decision.NewStatus,
			decision.ManualOverride, decision.AgendaRefsRepaired, decision.ItemMoved,
			decision.PreviousParentID, decision.SelectedMaterializedID,
			decision.DynamicCandidateChecked, decision.RejectedReason)
	}
	if len(finalCandidateIDs) > 0 || skipCandidates > 0 {
		log.Printf("Agenda reconciliation summary. sessionId=%s treeVersion=%d finalizationCandidates=%d finalizationRepaired=%d finalizationNoChange=%d skippedAgendaCandidates=%d skippedAgendaBackfilled=%d manualOverridesProtected=%d",
			sessionID, treeVersion, len(finalCandidateIDs), len(finalRepairedIDs), len(finalNoChangeIDs),
			skipCandidates, skipBackfilled, manualProtected)
	}
}

func annotateAgendaReconciliationManualOverrides(decisions []agendaReconciliationDecision, overrides *AgendaProgressOverrides) []agendaReconciliationDecision {
	result := append([]agendaReconciliationDecision(nil), decisions...)
	if overrides == nil || len(overrides.StatusOverrides) == 0 {
		return result
	}
	for index := range result {
		agendaID := result[index].SelectedAgendaID
		if agendaID == "" && len(result[index].CandidateAgendaIDs) == 1 {
			agendaID = result[index].CandidateAgendaIDs[0]
		}
		if _, manual := overrides.StatusOverrides[agendaID]; manual {
			result[index].ManualOverride = true
		}
	}
	return result
}
