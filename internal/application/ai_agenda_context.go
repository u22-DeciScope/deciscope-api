package application

import (
	"regexp"
	"sort"
	"strings"
)

type agendaContextSpan struct {
	Mode               string
	AgendaID           string
	StartSequenceNo    int64
	EndSequenceNo      int64
	Confidence         float64
	EvidenceSequenceNo int64
}

type agendaTransitionEvaluation struct {
	SequenceNo int64
	Mode       string
	AgendaID   string
	Confidence float64
}

const (
	agendaContextModeFixed    = "fixed_agenda"
	agendaContextModeNoAgenda = "no_agenda"
)

var (
	agendaTransitionPattern = regexp.MustCompile(`(?:^|[。！？!?\s])(?:まず|続いて|次に|ここからは|最後に)|(?:について(?:確認|検討|議論)(?:します|する)|の議題に移(?:ります|る)|へ移(?:ります|る))`)
	externalTopicPattern    = regexp.MustCompile(`(?:アジェンダ|議題)(?:に|で)?(?:は)?(?:ありません|なかった|外)|新しい(?:報告|論点|調査課題)`)
	explicitNoAgendaPattern = regexp.MustCompile(`(?:アジェンダ|議題)(?:に|で)?(?:は)?(?:ありません|なかった|外)`)
)

func agendaTransitionTarget(text string, mc *meetingContext) (string, float64) {
	if mc == nil {
		return "", 0
	}
	bestID, bestScore := "", 0.0
	textKey := semanticTopicCore(text)
	for _, agenda := range mc.Agenda {
		if effectiveAgendaRole(agenda.Role, agenda.Title, "") == agendaRoleActionSummary {
			continue
		}
		core := semanticTopicCore(agenda.Title)
		score := semanticItemSimilarity(agenda.Title, text)
		if len([]rune(core)) >= 2 && strings.Contains(textKey, core) && score < 0.95 {
			score = 0.95
		}
		if score > bestScore {
			bestID, bestScore = agenda.ID, score
		}
	}
	if bestScore < 0.12 {
		return "", bestScore
	}
	return bestID, bestScore
}

// detectAgendaContextSpans turns explicit topic-shift utterances into
// deterministic sequence intervals. The transition vocabulary is generic;
// agenda selection is based on the meeting-context titles rather than a
// session-specific phrase.
func detectAgendaContextSpans(scope liveEvidenceScope, mc *meetingContext, stats *liveAnalysisTreeMergeStats) []agendaContextSpan {
	sequenceNos := make([]int64, 0, len(scope.TranscriptText))
	for sequenceNo := range scope.TranscriptText {
		if sequenceNo > 0 && sequenceNo <= scope.CoveredThrough {
			sequenceNos = append(sequenceNos, sequenceNo)
		}
	}
	sort.Slice(sequenceNos, func(i, j int) bool { return sequenceNos[i] < sequenceNos[j] })
	spans := make([]agendaContextSpan, 0)
	activeAt := -1
	for _, sequenceNo := range sequenceNos {
		text := strings.TrimSpace(scope.TranscriptText[sequenceNo])
		if text == "" {
			continue
		}
		externalTopic := externalTopicPattern.MatchString(text)
		if !explicitNoAgendaPattern.MatchString(text) && !agendaTransitionPattern.MatchString(text) {
			continue
		}
		agendaID, confidence := agendaTransitionTarget(text, mc)
		// An explicit agenda-external transition closes the active fixed-agenda
		// span. Weak lexical overlap must not pull the new subject back into an
		// unrelated fixed topic.
		if externalTopic {
			agendaID, confidence = "", 0
		}
		if agendaID == "" && !externalTopic {
			continue
		}
		if externalTopic && activeAt >= 0 && spans[activeAt].Mode == agendaContextModeNoAgenda {
			// A recap that explicitly calls the subject agenda-external confirms
			// the existing span; it does not start a second candidate context.
			continue
		}
		if activeAt >= 0 {
			spans[activeAt].EndSequenceNo = sequenceNo - 1
			activeAt = -1
		}
		mode := agendaContextModeFixed
		if externalTopic {
			mode = agendaContextModeNoAgenda
		}
		spans = append(spans, agendaContextSpan{Mode: mode, AgendaID: agendaID, StartSequenceNo: sequenceNo, EndSequenceNo: scope.CoveredThrough, Confidence: confidence, EvidenceSequenceNo: sequenceNo})
		activeAt = len(spans) - 1
		if stats != nil {
			stats.AgendaTransitions = append(stats.AgendaTransitions, agendaTransitionEvaluation{SequenceNo: sequenceNo, Mode: mode, AgendaID: agendaID, Confidence: confidence})
			if mode == agendaContextModeNoAgenda {
				stats.NoAgendaSpanCount++
				stats.NoAgendaSpanStartSequences = append(stats.NoAgendaSpanStartSequences, sequenceNo)
			}
		}
	}
	if activeAt >= 0 {
		spans[activeAt].EndSequenceNo = scope.CoveredThrough
	}
	if stats != nil {
		stats.ActiveAgendaSpanCount = len(spans)
	}
	return spans
}

func agendaContextForEvidence(sequenceNos []int64, spans []agendaContextSpan) (string, string, float64) {
	selectedMode, selectedID, selectedConfidence, selectedSequence := "", "", 0.0, int64(0)
	for _, sequenceNo := range sequenceNos {
		for _, span := range spans {
			if sequenceNo < span.StartSequenceNo || sequenceNo > span.EndSequenceNo {
				continue
			}
			if sequenceNo > selectedSequence || (sequenceNo == selectedSequence && span.Confidence > selectedConfidence) {
				selectedMode, selectedID, selectedConfidence, selectedSequence = span.Mode, span.AgendaID, span.Confidence, sequenceNo
			}
		}
	}
	return selectedMode, selectedID, selectedConfidence
}

func agendaForEvidence(sequenceNos []int64, spans []agendaContextSpan) (string, float64) {
	_, agendaID, confidence := agendaContextForEvidence(sequenceNos, spans)
	return agendaID, confidence
}

func earliestAgendaContextForEvidence(sequenceNos []int64, spans []agendaContextSpan) (string, string) {
	selectedMode, selectedAgenda := "", ""
	selectedSequence := int64(0)
	for _, sequenceNo := range sequenceNos {
		for _, span := range spans {
			if sequenceNo < span.StartSequenceNo || sequenceNo > span.EndSequenceNo {
				continue
			}
			if selectedSequence == 0 || sequenceNo < selectedSequence {
				selectedMode, selectedAgenda, selectedSequence = span.Mode, span.AgendaID, sequenceNo
			}
		}
	}
	return selectedMode, selectedAgenda
}

func treeItemTopic(tree *liveAnalysisTree, itemID string) string {
	if tree == nil {
		return ""
	}
	parents := make(map[string]string, len(tree.Nodes))
	kinds := make(map[string]string, len(tree.Nodes))
	for _, node := range tree.Nodes {
		parents[node.ID], kinds[node.ID] = node.ParentID, node.Kind
	}
	seen := make(map[string]struct{})
	current := parents[itemID]
	for current != "" {
		if _, loop := seen[current]; loop {
			return ""
		}
		seen[current] = struct{}{}
		if kinds[current] == "topic" {
			return current
		}
		current = parents[current]
	}
	return ""
}

// Active spans outrank model/semantic fallback for new or server-corrected
// items. A stable model-assigned canonical parent is retained; this preserves
// hysteresis while still repairing the observed semantic miscorrection.
func applyAgendaContextAssignments(assignments []treeAssignment, newTopics []liveAnalysisTreeNode, previousTree *liveAnalysisTree, items, changed []liveAnalysisItem, priorCandidates []emergingTopicCandidate, spans []agendaContextSpan, stats *liveAnalysisTreeMergeStats) ([]treeAssignment, []liveAnalysisTreeNode) {
	if len(spans) == 0 || len(changed) == 0 {
		return assignments, newTopics
	}
	itemByID := make(map[string]liveAnalysisItem, len(items))
	for _, item := range items {
		itemByID[item.ID] = item
	}
	// One explicit no-agenda transition represents a subject context until the
	// next explicit transition. Pick one durable candidate anchor per span and
	// then attach each item's own evidence to that anchor. This prevents model
	// wording variants from splitting cross-kind companions.
	noAgendaCandidateByStart := make(map[int64]string)
	for _, span := range spans {
		if span.Mode != agendaContextModeNoAgenda {
			continue
		}
		contextText := ""
		fallbackLabel := ""
		for _, item := range changed {
			mode, _, _ := agendaContextForEvidence(item.EvidenceSequenceNos, []agendaContextSpan{span})
			if mode == agendaContextModeNoAgenda {
				contextText += " " + item.Title + " " + item.Body
				if fallbackLabel == "" {
					fallbackLabel = item.Title
				}
			}
		}
		bestID, bestScore := "", 0.0
		bestWasPrior := false
		for _, candidate := range priorCandidates {
			score := semanticItemSimilarity(candidate.Label+" "+candidate.Description, contextText)
			if sharesSemanticTopicBigram(candidate.Label+" "+candidate.Description, contextText) && score < 0.75 {
				score = 0.75
			}
			if score > bestScore {
				bestID, bestScore = candidate.ID, score
				bestWasPrior = true
			}
		}
		for _, topic := range newTopics {
			score := semanticItemSimilarity(topic.Label+" "+topic.Description, contextText)
			if sharesSemanticTopicBigram(topic.Label+" "+topic.Description, contextText) && score < 0.35 {
				score = 0.35
			}
			if score > bestScore {
				bestID, bestScore = topic.ID, score
				bestWasPrior = false
			}
		}
		if bestScore < 0.08 {
			bestID = ""
		}
		if bestID == "" {
			label := noAgendaSubjectLabel(fallbackLabel)
			newTopics = append(newTopics, liveAnalysisTreeNode{Kind: "topic", Label: label, Description: "明示的なアジェンダ外区間から検出"})
			bestID = normalizeProposedTopicID("", label)
		}
		if bestWasPrior {
			for i := range newTopics {
				score := semanticItemSimilarity(newTopics[i].Label+" "+newTopics[i].Description, contextText)
				if score >= 0.08 || sharesSemanticTopicBigram(newTopics[i].Label+" "+newTopics[i].Description, contextText) {
					if newTopics[i].ID != bestID && stats != nil {
						stats.CandidateIDsMerged++
					}
					newTopics[i].ID = bestID
				}
			}
		}
		noAgendaCandidateByStart[span.StartSequenceNo] = bestID
	}

	seen := make(map[string]struct{})
	for _, item := range changed {
		if _, duplicate := seen[item.ID]; duplicate {
			continue
		}
		seen[item.ID] = struct{}{}
		merged, ok := itemByID[item.ID]
		if !ok {
			continue
		}
		mode, agendaID, confidence := agendaContextForEvidence(merged.EvidenceSequenceNos, spans)
		if mode == "" {
			continue
		}
		if mode == agendaContextModeNoAgenda {
			originMode, originAgendaID := earliestAgendaContextForEvidence(merged.EvidenceSequenceNos, spans)
			preserveOriginAgenda := false
			if originMode == agendaContextModeFixed {
				for _, proposed := range assignments {
					if proposed.nodeID() == item.ID && strings.TrimSpace(proposed.ParentTopicID) == originAgendaID {
						// A recap can mention a stable fixed-agenda item after an
						// agenda-external transition. Its originating evidence and
						// explicit matching assignment keep it under that agenda.
						preserveOriginAgenda = true
						break
					}
				}
			}
			if preserveOriginAgenda {
				continue
			}
			candidateID := ""
			for _, span := range spans {
				if span.Mode == mode {
					for _, sequenceNo := range merged.EvidenceSequenceNos {
						if sequenceNo >= span.StartSequenceNo && sequenceNo <= span.EndSequenceNo {
							candidateID = noAgendaCandidateByStart[span.StartSequenceNo]
						}
					}
				}
			}
			current := treeItemTopic(previousTree, item.ID)
			if current != "" && current != treeUnclassifiedTopicID && !strings.HasPrefix(current, agendaTopicIDPrefix) {
				// A promoted dynamic topic is durable. A later recap inside the
				// no-agenda context may update evidence, but must not stage it again.
				continue
			}
			if strings.HasPrefix(current, agendaTopicIDPrefix) && merged.AssignmentSource != assignmentSourceActiveSpan && merged.AssignmentSource != assignmentSourceRule && merged.AssignmentSource != assignmentSourceFallback {
				// Existing fixed-agenda items mentioned in a cross-topic recap retain
				// their stable parent. The no-agenda override repairs only stale
				// active-span/fallback placement.
				continue
			}
			if current != "" && current != treeUnclassifiedTopicID {
				if stats != nil {
					stats.StaleAgendaFallbackRejected++
					stats.FixedAgendaAssignmentRejectedByNoAgendaSpan++
				}
			}
			for _, proposed := range assignments {
				if proposed.nodeID() == item.ID && strings.HasPrefix(strings.TrimSpace(proposed.ParentTopicID), agendaTopicIDPrefix) && stats != nil {
					stats.StaleAgendaFallbackRejected++
					stats.FixedAgendaAssignmentRejectedByNoAgendaSpan++
				}
			}
			assignments = append(assignments, treeAssignment{NodeID: item.ID, ParentTopicID: candidateID, Confidence: 1, Reason: "item evidence belongs to explicit no-agenda span", ServerSource: assignmentSourceNoAgendaSpan, EvidenceSequenceNos: append([]int64(nil), merged.EvidenceSequenceNos...), ResolvedAgendaSpanMode: mode})
			continue
		}
		if agendaID == "" {
			continue
		}
		current := treeItemTopic(previousTree, item.ID)
		if current != "" && current != treeUnclassifiedTopicID && current != agendaID && merged.AssignmentSource != assignmentSourceRule && merged.AssignmentSource != assignmentSourceFallback {
			continue
		}
		assignments = append(assignments, treeAssignment{NodeID: item.ID, ParentTopicID: agendaID, Confidence: confidence, Reason: "active agenda span", ServerSource: assignmentSourceActiveSpan, EvidenceSequenceNos: append([]int64(nil), merged.EvidenceSequenceNos...), ResolvedAgendaSpanMode: mode})
	}
	return assignments, newTopics
}

func noAgendaSubjectLabel(value string) string {
	label := strings.Trim(strings.TrimSpace(value), "、。！？!? ")
	if label == "" {
		label = "アジェンダ外の追加論点"
	}
	return truncateRunes(label, liveAnalysisTopicLabelMaxRunes)
}
