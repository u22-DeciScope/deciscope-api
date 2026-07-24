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
	Explicit           bool
	EndReason          string
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
	agendaTransitionPattern           = regexp.MustCompile(`(?:^|[。！？!?\s])(?:まず|続いて|次に|ここからは|最後に)|(?:(?:今後|これから)の対応について(?:です|確認(?:します|する))|について(?:確認|検討|議論)(?:します|する)|の議題に移(?:ります|る)|へ移(?:ります|る))`)
	explicitNoAgendaPattern           = regexp.MustCompile(`(?:[アマ]ジェンダ|議題)(?:に|で)?(?:は)?(?:ありません|なかった|外)`)
	explicitExternalTransitionPattern = regexp.MustCompile(`(?i)^(?:(?:ここ(?:で|から)|では|次に|さて))?(?:(?:本題とは)?別(?:の)?(?:問題|論点|話|話題|議題|テーマ|件)|追加(?:の)?(?:問題|論点|話題|議題|テーマ|件)|新しい(?:問題|論点|話題|議題|テーマ|件)|別件)(?:が)?(?:あり(?:ます)?|です|ですが|を(?:扱い|取り上げ)(?:ます|る))?$|^(?:本題とは別|本題外|(?:少し)?(?:話(?:は|が)?変わ(?:ります|る)|話を変え(?:ます|る)))(?:です|ですが|が|けれど|けれども|ます)?$`)
	agendaReturnPattern               = regexp.MustCompile(`(?:本題|元の話|先ほどの話|話|議題|アジェンダ)(?:に|へ|を)?(?:戻(?:り|る|し|しま|って|った)|復帰)|(?:脱線|寄り道).{0,16}(?:本題|元の話).{0,8}(?:戻|復帰)`)
	agendaContinuationPattern         = regexp.MustCompile(`^(?:また|さらに|加えて|そして|ただし|一方|そのため|このため|これにより|そこで)(?:、|,)?`)
	agendaContextStopBigrams          = map[string]struct{}{
		"です": {}, "ます": {}, "まし": {}, "した": {}, "して": {}, "する": {}, "あり": {}, "ある": {},
		"ので": {}, "ため": {}, "こと": {}, "につ": {}, "つい": {}, "いて": {}, "てい": {}, "いま": {},
		"から": {}, "では": {}, "とし": {}, "しま": {}, "なり": {}, "もの": {}, "確認": {},
	}
)

func isExplicitNoAgendaTransition(text string) bool {
	if explicitNoAgendaPattern.MatchString(text) {
		return true
	}
	normalized := normalizeDiscourseText(text)
	return explicitExternalTransitionPattern.MatchString(strings.ToLower(normalized))
}

func isAgendaReturnTransition(text string) bool {
	return agendaReturnPattern.MatchString(normalizeDiscourseText(text))
}

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
		normalizedAgenda, normalizedText := normalizeForMatch(agenda.Title), normalizeForMatch(text)
		if (strings.Contains(normalizedAgenda, "再発防止") || strings.Contains(normalizedAgenda, "今後の対応")) &&
			(strings.Contains(normalizedText, "今後の対応") || strings.Contains(normalizedText, "再発防止") || strings.Contains(normalizedText, "改善策")) && score < 0.90 {
			score = 0.90
		}
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

// agendaReentryTarget evaluates concrete content, not just transition words.
// Agenda titles remain the preferred target; purpose/background are used only
// to decide whether a run has returned to the meeting subject when no single
// agenda title is lexically specific enough.
func agendaReentryTarget(text string, mc *meetingContext) (string, float64, bool) {
	agendaID, score := agendaTransitionTarget(text, mc)
	if mc == nil {
		return agendaID, score, agendaID != ""
	}
	meetingScore := score
	for _, contextText := range []string{mc.Title, mc.Purpose, mc.Background} {
		if candidate := semanticItemSimilarity(text, contextText); candidate > meetingScore {
			meetingScore = candidate
		}
	}
	aligned := agendaID != "" || meetingContextSharedSignals(text, mc) >= 2
	return agendaID, meetingScore, aligned
}

// meetingContextSharedSignals derives subject evidence from the meeting record
// itself. This avoids hard-coding one meeting's nouns while still recognizing
// detailed return turns whose wording does not repeat a short agenda title.
func meetingContextSharedSignals(text string, mc *meetingContext) int {
	if mc == nil {
		return 0
	}
	parts := []string{mc.Title, mc.Purpose, mc.Background}
	for _, agenda := range mc.Agenda {
		if effectiveAgendaRole(agenda.Role, agenda.Title, "") != agendaRoleActionSummary {
			parts = append(parts, agenda.Title)
		}
	}
	return sharedAgendaContextSignals(text, strings.Join(parts, " "))
}

func sharedAgendaContextSignals(a, b string) int {
	aGrams, bGrams := runeBigrams(semanticItemKey(a)), runeBigrams(semanticItemKey(b))
	count := 0
	for gram := range aGrams {
		if _, stop := agendaContextStopBigrams[gram]; stop {
			continue
		}
		if _, shared := bGrams[gram]; shared {
			count++
		}
	}
	return count
}

func agendaContextSubstantive(text string, sequenceNo int64, timelines ...discourseTimeline) bool {
	if len(timelines) > 0 {
		role := timelines[0].Roles[sequenceNo]
		if role == liveEvidenceDiscourseOnly || role == liveEvidenceReferenceRecap {
			return false
		}
	}
	if classifyDiscourseAct(text) != discourseContent || isAgendaReturnTransition(text) || isExplicitNoAgendaTransition(text) {
		return false
	}
	// Short reactions such as 「残念です」 are not sufficient evidence for
	// opening or closing a context span. Counting them as a second substantive
	// turn made a trailing comment create a false no-agenda interval.
	return len([]rune(semanticItemKey(text))) >= 6
}

// agendaContextWindowText joins only adjacent same-speaker STT fragments.
// It is used for transition/context classification, while evidence IDs remain
// attached to their original segments. This prevents a trailing particle in
// one segment from being interpreted as an independent topic transition.
func agendaContextWindowText(scope liveEvidenceScope, sequenceNo int64) string {
	text := strings.TrimSpace(scope.TranscriptText[sequenceNo])
	current := segmentFromEvidenceScope(scope, sequenceNo)
	if current.SequenceNo <= 0 {
		return text
	}
	if previous := segmentFromEvidenceScope(scope, sequenceNo-1); adjacentSameSpeakerSegments(previous, current) &&
		(logicalUtteranceContinuation(previous, current) || decisionStatementNeedsReferent(current.Text)) {
		text = strings.TrimSpace(previous.Text + " " + text)
	}
	if next := segmentFromEvidenceScope(scope, sequenceNo+1); adjacentSameSpeakerSegments(current, next) && logicalUtteranceContinuation(current, next) {
		text = strings.TrimSpace(text + " " + next.Text)
	}
	return text
}

// detectAgendaContextSpans turns explicit topic-shift utterances into
// deterministic sequence intervals. The transition vocabulary is generic;
// agenda selection is based on the meeting-context titles rather than a
// session-specific phrase.
func detectAgendaContextSpans(scope liveEvidenceScope, mc *meetingContext, stats *liveAnalysisTreeMergeStats, timelines ...discourseTimeline) []agendaContextSpan {
	sequenceNos := make([]int64, 0, len(scope.TranscriptText))
	for sequenceNo := range scope.TranscriptText {
		if sequenceNo > 0 && sequenceNo <= scope.CoveredThrough {
			sequenceNos = append(sequenceNos, sequenceNo)
		}
	}
	sort.Slice(sequenceNos, func(i, j int) bool { return sequenceNos[i] < sequenceNos[j] })
	spans := make([]agendaContextSpan, 0)
	activeAt := -1
	baselineAgendaSeen := false
	pendingExternal := make([]int64, 0, 2)
	type reentryEvidence struct {
		sequenceNo int64
		agendaID   string
		confidence float64
		text       string
	}
	pendingReentry := make([]reentryEvidence, 0, 2)
	closeActive := func(endSequenceNo int64, reason string) {
		if activeAt < 0 {
			return
		}
		if endSequenceNo < spans[activeAt].StartSequenceNo {
			spans = spans[:activeAt]
		} else {
			spans[activeAt].EndSequenceNo = endSequenceNo
			spans[activeAt].EndReason = reason
		}
		activeAt = -1
	}
	startSpan := func(span agendaContextSpan) {
		if activeAt >= 0 {
			closeActive(span.StartSequenceNo-1, "next_transition")
		}
		span.EndSequenceNo = scope.CoveredThrough
		spans = append(spans, span)
		activeAt = len(spans) - 1
	}
	for _, sequenceNo := range sequenceNos {
		text := strings.TrimSpace(scope.TranscriptText[sequenceNo])
		if text == "" {
			continue
		}
		contextText := agendaContextWindowText(scope, sequenceNo)
		if isAgendaReturnTransition(text) {
			if activeAt >= 0 && spans[activeAt].Mode == agendaContextModeNoAgenda {
				closeActive(sequenceNo-1, "explicit_agenda_reentry")
				if stats != nil {
					stats.NoAgendaSpansClosed++
					stats.ExplicitAgendaReentries++
				}
			}
			baselineAgendaSeen = true
			pendingExternal = pendingExternal[:0]
			pendingReentry = pendingReentry[:0]
			continue
		}
		// Only an explicit discourse-level declaration starts a strong
		// agenda-external interval. Concrete phrases such as 「別の担当者」,
		// 「別の機器」 and 「別の方法」 are ordinary modifiers.
		externalTopic := isExplicitNoAgendaTransition(contextText)
		explicitAgendaTransition := !externalTopic && agendaTransitionPattern.MatchString(text)
		agendaID, confidence := agendaTransitionTarget(contextText, mc)
		// An explicit agenda-external transition closes the active fixed-agenda
		// span. Weak lexical overlap must not pull the new subject back into an
		// unrelated fixed topic.
		if externalTopic {
			agendaID, confidence = "", 1
		}
		if externalTopic && activeAt >= 0 && spans[activeAt].Mode == agendaContextModeNoAgenda {
			// A recap that explicitly calls the subject agenda-external confirms
			// the existing span; it does not start a second candidate context.
			continue
		}
		if externalTopic {
			startSpan(agendaContextSpan{Mode: agendaContextModeNoAgenda, StartSequenceNo: sequenceNo, Confidence: confidence, EvidenceSequenceNo: sequenceNo, Explicit: true})
			baselineAgendaSeen = false
			pendingExternal = pendingExternal[:0]
			pendingReentry = pendingReentry[:0]
			continue
		}
		if explicitAgendaTransition && agendaID != "" {
			startSpan(agendaContextSpan{Mode: agendaContextModeFixed, AgendaID: agendaID, StartSequenceNo: sequenceNo, Confidence: confidence, EvidenceSequenceNo: sequenceNo, Explicit: true})
			baselineAgendaSeen = true
			pendingExternal = pendingExternal[:0]
			pendingReentry = pendingReentry[:0]
			continue
		}
		if !agendaContextSubstantive(text, sequenceNo, timelines...) {
			continue
		}
		reentryAgendaID, reentryConfidence, aligned := agendaReentryTarget(contextText, mc)
		if activeAt >= 0 && spans[activeAt].Mode == agendaContextModeNoAgenda {
			continuation := len(pendingReentry) > 0 && sharedAgendaContextSignals(pendingReentry[len(pendingReentry)-1].text, text) > 0
			if aligned || continuation {
				pendingReentry = append(pendingReentry, reentryEvidence{sequenceNo: sequenceNo, agendaID: reentryAgendaID, confidence: reentryConfidence, text: text})
				if len(pendingReentry) >= 2 {
					first := pendingReentry[0]
					closeActive(first.sequenceNo-1, "semantic_agenda_reentry")
					// Semantic reentry closes the stale exclusion interval but does
					// not create an unbounded fixed-agenda span. The model's direct
					// assignment and deterministic agenda materialization remain
					// authoritative for the concrete items that follow.
					baselineAgendaSeen = true
					pendingReentry = pendingReentry[:0]
					if stats != nil {
						stats.NoAgendaSpansClosed++
						stats.ImplicitAgendaReentries++
					}
				}
			} else {
				pendingReentry = pendingReentry[:0]
			}
			continue
		}
		// An explicitly selected fixed-agenda span already carries stronger
		// evidence than generic lexical mismatch. Only an explicit external
		// transition may open a detour inside it; this avoids treating detailed
		// measurements or implementation steps as off-topic merely because they
		// do not repeat the short agenda title.
		if activeAt >= 0 && spans[activeAt].Mode == agendaContextModeFixed {
			baselineAgendaSeen = true
			pendingExternal = pendingExternal[:0]
			continue
		}
		if aligned {
			baselineAgendaSeen = true
			pendingExternal = pendingExternal[:0]
			continue
		}
		// Additive/contrastive turns continue the active subject unless they
		// contain an explicit agenda-external declaration. STT commonly splits
		// enumerated countermeasures at exactly these conjunctions.
		if baselineAgendaSeen && agendaContinuationPattern.MatchString(strings.TrimSpace(text)) {
			pendingExternal = pendingExternal[:0]
			continue
		}
		if !baselineAgendaSeen {
			continue
		}
		pendingExternal = append(pendingExternal, sequenceNo)
		if len(pendingExternal) >= 2 {
			start := pendingExternal[0]
			// Two consecutive, non-continuation content turns form a bounded
			// inferred detour. A single mismatch never opens a span; direct
			// agenda continuity and joined STT fragments were handled above.
			startSpan(agendaContextSpan{Mode: agendaContextModeNoAgenda, StartSequenceNo: start, Confidence: 0.80, EvidenceSequenceNo: start, Explicit: false})
			pendingExternal = pendingExternal[:0]
			pendingReentry = pendingReentry[:0]
		}
	}
	if activeAt >= 0 {
		spans[activeAt].EndSequenceNo = scope.CoveredThrough
	}
	if stats != nil {
		for _, span := range spans {
			stats.AgendaTransitions = append(stats.AgendaTransitions, agendaTransitionEvaluation{SequenceNo: span.StartSequenceNo, Mode: span.Mode, AgendaID: span.AgendaID, Confidence: span.Confidence})
			if span.Mode == agendaContextModeNoAgenda {
				stats.NoAgendaSpanCount++
				stats.NoAgendaSpanStartSequences = append(stats.NoAgendaSpanStartSequences, span.StartSequenceNo)
			}
		}
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

func agendaContextSpanForEvidence(sequenceNos []int64, spans []agendaContextSpan) (agendaContextSpan, bool) {
	selected := agendaContextSpan{}
	selectedSequence := int64(0)
	found := false
	for _, sequenceNo := range sequenceNos {
		for _, span := range spans {
			if sequenceNo < span.StartSequenceNo || sequenceNo > span.EndSequenceNo {
				continue
			}
			if !found || sequenceNo > selectedSequence || (sequenceNo == selectedSequence && span.Confidence > selected.Confidence) {
				selected, selectedSequence, found = span, sequenceNo, true
			}
		}
	}
	return selected, found
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
func applyAgendaContextAssignments(assignments []treeAssignment, newTopics []liveAnalysisTreeNode, previousTree *liveAnalysisTree, items, changed []liveAnalysisItem, priorCandidates []emergingTopicCandidate, spans []agendaContextSpan, mc *meetingContext, stats *liveAnalysisTreeMergeStats) ([]treeAssignment, []liveAnalysisTreeNode) {
	if len(spans) == 0 || len(changed) == 0 {
		return assignments, newTopics
	}
	itemByID := make(map[string]liveAnalysisItem, len(items))
	for _, item := range items {
		itemByID[item.ID] = item
	}
	agendaIDs := make(map[string]struct{})
	for agendaID := range agendaRecordMap(mc) {
		agendaIDs[agendaID] = struct{}{}
	}
	for _, span := range spans {
		if span.AgendaID != "" {
			agendaIDs[span.AgendaID] = struct{}{}
		}
	}
	isAgendaID := func(id string) bool {
		id = strings.TrimSpace(id)
		_, exists := agendaIDs[id]
		return exists
	}
	strongAgendaProposalFor := func(itemID string) bool {
		for _, proposed := range assignments {
			if proposed.nodeID() == itemID && isAgendaID(proposed.ParentTopicID) && proposed.Confidence >= 0.80 {
				return true
			}
		}
		return false
	}
	isMaterializedAgendaTopic := func(id string) bool {
		if previousTree == nil {
			return false
		}
		for _, node := range previousTree.Nodes {
			if node.ID == id && node.Kind == "topic" {
				return node.Origin == topicOriginAgenda || node.Origin == topicOriginMixed || len(node.AgendaRefs) > 0
			}
		}
		return false
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
				if span.Confidence < 0.75 && strongAgendaProposalFor(item.ID) {
					continue
				}
				contextText += " " + item.Title + " " + item.Body
				if fallbackLabel == "" {
					fallbackLabel = item.Title
				}
			}
		}
		if strings.TrimSpace(contextText) == "" {
			continue
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
		selectedSpan, hasSpan := agendaContextSpanForEvidence(merged.EvidenceSequenceNos, spans)
		if !hasSpan {
			continue
		}
		mode, agendaID, confidence := selectedSpan.Mode, selectedSpan.AgendaID, selectedSpan.Confidence
		if mode == agendaContextModeNoAgenda {
			// A weak inferred span is supporting context, not an unconditional
			// veto. It may not erase a materially stronger direct agenda proposal.
			// Explicit external spans and hysteresis-confirmed implicit spans keep
			// their normal authority.
			if confidence < 0.75 {
				if strongAgendaProposalFor(item.ID) {
					if stats != nil {
						stats.LowConfidenceNoAgendaOverridesRejected++
					}
					continue
				}
			}
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
			currentIsAgenda := isMaterializedAgendaTopic(current)
			if current != "" && current != treeUnclassifiedTopicID && !currentIsAgenda {
				// A promoted dynamic topic is durable. A later recap inside the
				// no-agenda context may update evidence, but must not stage it again.
				continue
			}
			if currentIsAgenda && merged.AssignmentSource != assignmentSourceActiveSpan && merged.AssignmentSource != assignmentSourceRule && merged.AssignmentSource != assignmentSourceFallback {
				// Existing agenda-linked items mentioned in a cross-topic recap retain
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
				if proposed.nodeID() == item.ID && isAgendaID(proposed.ParentTopicID) && stats != nil {
					stats.StaleAgendaFallbackRejected++
					stats.FixedAgendaAssignmentRejectedByNoAgendaSpan++
				}
			}
			reason := "item evidence belongs to bounded no-agenda span"
			if selectedSpan.Explicit {
				reason = "item evidence belongs to explicit no-agenda span"
			}
			assignments = append(assignments, treeAssignment{NodeID: item.ID, ParentTopicID: candidateID, Confidence: confidence, Reason: reason, ServerSource: assignmentSourceNoAgendaSpan, EvidenceSequenceNos: append([]int64(nil), merged.EvidenceSequenceNos...), ResolvedAgendaSpanMode: mode})
			continue
		}
		if agendaID == "" {
			continue
		}
		current := treeItemTopic(previousTree, item.ID)
		if current != "" && current != treeUnclassifiedTopicID && current != agendaID && merged.AssignmentSource != assignmentSourceRule && merged.AssignmentSource != assignmentSourceFallback && !selectedSpan.Explicit {
			continue
		}
		assignments = append(assignments, treeAssignment{NodeID: item.ID, ParentTopicID: agendaID, Confidence: confidence, Reason: "active agenda span", ServerSource: assignmentSourceActiveSpan, EvidenceSequenceNos: append([]int64(nil), merged.EvidenceSequenceNos...), ResolvedAgendaSpanMode: mode})
	}
	return assignments, newTopics
}

// agendaSpanRepairItems makes a newly corrected explicit fixed-agenda span
// capable of repairing historical items that were previously promoted under
// a false inferred no-agenda span. Items in a real explicit no-agenda span are
// excluded and retain their dynamic-topic protection.
func agendaSpanRepairItems(items, changed []liveAnalysisItem, spans []agendaContextSpan) []liveAnalysisItem {
	seen := make(map[string]struct{}, len(changed))
	result := append([]liveAnalysisItem(nil), changed...)
	for _, item := range changed {
		seen[item.ID] = struct{}{}
	}
	for _, item := range items {
		if _, exists := seen[item.ID]; exists || item.Inactive || item.MergedIntoID != "" {
			continue
		}
		span, found := agendaContextSpanForEvidence(item.EvidenceSequenceNos, spans)
		if !found || span.Mode != agendaContextModeFixed || !span.Explicit || span.AgendaID == "" {
			continue
		}
		result = append(result, item)
	}
	return result
}

func noAgendaSubjectLabel(value string) string {
	label := strings.Trim(strings.TrimSpace(value), "、。！？!? ")
	if label == "" {
		label = "アジェンダ外の追加論点"
	}
	return truncateRunes(label, liveAnalysisTopicLabelMaxRunes)
}
