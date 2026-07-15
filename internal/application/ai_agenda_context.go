package application

import (
	"regexp"
	"sort"
	"strings"
)

type agendaContextSpan struct {
	AgendaID           string
	StartSequenceNo    int64
	EndSequenceNo      int64
	Confidence         float64
	EvidenceSequenceNo int64
}

type agendaTransitionEvaluation struct {
	SequenceNo int64
	AgendaID   string
	Confidence float64
}

var (
	agendaTransitionPattern = regexp.MustCompile(`(?:^|[。！？!?\s])(?:まず|続いて|次に|ここからは|最後に)|(?:について(?:確認|検討|議論)(?:します|する)|の議題に移(?:ります|る)|へ移(?:ります|る))`)
	externalTopicPattern    = regexp.MustCompile(`(?:アジェンダ|議題)(?:に|で)?(?:は)?(?:ありません|なかった|外)|新しい(?:報告|論点|調査課題)`)
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
		if text == "" || !agendaTransitionPattern.MatchString(text) {
			continue
		}
		externalTopic := externalTopicPattern.MatchString(text)
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
		if activeAt >= 0 {
			spans[activeAt].EndSequenceNo = sequenceNo - 1
			activeAt = -1
		}
		if agendaID != "" {
			spans = append(spans, agendaContextSpan{AgendaID: agendaID, StartSequenceNo: sequenceNo, EndSequenceNo: scope.CoveredThrough, Confidence: confidence, EvidenceSequenceNo: sequenceNo})
			activeAt = len(spans) - 1
		}
		if stats != nil {
			stats.AgendaTransitions = append(stats.AgendaTransitions, agendaTransitionEvaluation{SequenceNo: sequenceNo, AgendaID: agendaID, Confidence: confidence})
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

func agendaForEvidence(sequenceNos []int64, spans []agendaContextSpan) (string, float64) {
	selectedID, selectedConfidence, selectedSequence := "", 0.0, int64(0)
	for _, sequenceNo := range sequenceNos {
		for _, span := range spans {
			if sequenceNo < span.StartSequenceNo || sequenceNo > span.EndSequenceNo {
				continue
			}
			if sequenceNo > selectedSequence || (sequenceNo == selectedSequence && span.Confidence > selectedConfidence) {
				selectedID, selectedConfidence, selectedSequence = span.AgendaID, span.Confidence, sequenceNo
			}
		}
	}
	return selectedID, selectedConfidence
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
func applyAgendaContextAssignments(assignments []treeAssignment, previousTree *liveAnalysisTree, items, changed []liveAnalysisItem, spans []agendaContextSpan) []treeAssignment {
	if len(spans) == 0 || len(changed) == 0 {
		return assignments
	}
	itemByID := make(map[string]liveAnalysisItem, len(items))
	for _, item := range items {
		itemByID[item.ID] = item
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
		agendaID, confidence := agendaForEvidence(merged.EvidenceSequenceNos, spans)
		if agendaID == "" {
			continue
		}
		current := treeItemTopic(previousTree, item.ID)
		if current != "" && current != treeUnclassifiedTopicID && current != agendaID && merged.AssignmentSource != assignmentSourceRule && merged.AssignmentSource != assignmentSourceFallback {
			continue
		}
		assignments = append(assignments, treeAssignment{NodeID: item.ID, ParentTopicID: agendaID, Confidence: confidence, Reason: "active agenda span", ServerSource: assignmentSourceActiveSpan})
	}
	return assignments
}
