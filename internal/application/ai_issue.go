package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"deciscope-core-api/internal/domain"
)

type issueCandidate struct {
	SequenceNo int64
	Subtype    string
	Statement  string
	// Recap marks a candidate whose utterance the discourse timeline placed
	// in a recap span (set by the caller from classifyDiscourseTimeline,
	// distinct from the narrower issueRecapPattern text match below).
	Recap bool
}

const issueSynthesisAssignmentReason = "server unresolved candidate"

type issueExtractionAudit struct {
	QuestionCandidates              int
	OpenIssueCandidates             int
	QuestionsAccepted               int
	OpenIssuesAccepted              int
	ExistingMerged                  int
	RecapMerged                     int
	SameEvidenceSynthesisSuppressed int
	Decisions                       []issueSynthesisDecision
}

// issueSynthesisDecision deliberately excludes transcript and item text.
// Sequence numbers and canonical/model references are sufficient to trace why
// the deterministic extractor did or did not create a node.
type issueSynthesisDecision struct {
	SequenceNo   int64
	Subtype      string
	MatchedItem  string
	Decision     string
	Reason       string
	MatchScore   float64
	SameEvidence bool
	GeneratedBy  string
	EvidenceHash string
	ParentID     string
	AgendaRefs   []string
	Status       string
	MergedInto   string
}

var (
	openIssueMarkerPattern = regexp.MustCompile(`(?:決まってい(?:ない|ません)|決定してい(?:ない|ません)|まだ決定(?:してい(?:ない|ません)|せず)?|未決定|未確定|未解決|調整できてい(?:ない|ません)|結論が出てい(?:ない|ません)|判断材料が不足)`)
	questionMarkerPattern  = regexp.MustCompile(`(?:(?:何|いつ|どの|どれ|どこ|誰|どのように|どう|何を)[^。！？!?]{0,40}(?:する|します|したら|すべき|か)|(?:実施|調査|採用|依頼)するかどうか|何m/s|何ｍ/ｓ)`)
	issueRecapPattern      = regexp.MustCompile(`(?:未解決|未確定)(?:の)?(?:課題|事項)(?:は|として)`)
)

// detectIssueCandidates only produces candidates. Reconciliation below must
// still find a semantic model/previous match or create a separate stable item;
// it never rewrites a question/open issue into a TODO or decision.
func detectIssueCandidates(segments []domain.TranscriptSegment) []issueCandidate {
	seen := make(map[string]struct{})
	var candidates []issueCandidate
	for _, segment := range segments {
		if !segment.IsFinal || segment.SequenceNo <= 0 {
			continue
		}
		for _, raw := range decisionClauseSplitPattern.Split(segment.Text, -1) {
			clause := strings.TrimSpace(raw)
			if clause == "" {
				continue
			}
			var candidatesForClause []issueCandidate
			if openIssueMarkerPattern.MatchString(clause) {
				statements := splitCollapsedOpenIssueStatement(clause)
				if len(statements) == 0 {
					statements = []string{clause}
				}
				for _, statement := range statements {
					candidatesForClause = append(candidatesForClause, issueCandidate{SequenceNo: segment.SequenceNo, Subtype: inferIssueSubtype(statement, issueSubtypeDiscussion), Statement: statement})
				}
			}
			if questionMarkerPattern.MatchString(clause) {
				candidatesForClause = append(candidatesForClause, issueCandidate{SequenceNo: segment.SequenceNo, Subtype: issueSubtypeQuestion, Statement: clause})
			}
			for _, candidate := range candidatesForClause {
				key := candidate.Subtype + "\x00" + semanticIssueKey(candidate.Statement) + "\x00" + strconv.FormatInt(segment.SequenceNo, 10)
				if _, duplicate := seen[key]; duplicate {
					continue
				}
				seen[key] = struct{}{}
				candidates = append(candidates, candidate)
			}
		}
	}
	return candidates
}

func reconcileIssueCandidates(content string, previousPayload json.RawMessage, candidates []issueCandidate) (string, issueExtractionAudit, error) {
	var audit issueExtractionAudit
	cleaned := stripJSONCodeFence(content)
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(cleaned), &diff); err != nil {
		return content, audit, err
	}
	for i := range diff.Items {
		diff.Items[i].Kind, diff.Items[i].Subtype, diff.Items[i].Status, _ = normalizeSemanticClassification(diff.Items[i].Kind, diff.Items[i].Subtype, diff.Items[i].Status)
	}
	if len(candidates) == 0 {
		return cleaned, audit, nil
	}
	previous := previousLiveAnalysisState(previousPayload)
	for _, candidate := range candidates {
		if candidate.Subtype == issueSubtypeQuestion {
			audit.QuestionCandidates++
		} else {
			audit.OpenIssueCandidates++
		}
		if (candidate.Recap || issueRecapPattern.MatchString(candidate.Statement)) && mergeIssueRecap(&diff, previous.Items, candidate) {
			audit.ExistingMerged++
			audit.RecapMerged++
			continue
		}
		if candidate.Recap {
			// 談話タイムライン由来のrecap候補はmerge-onlyとする。
			// mergeIssueRecapで拾えなかった場合に限り、subtype不問の
			// same-kind(issue)マッチを追加で試す。それでもマッチしなければ
			// 新規item・assignmentを作らずスキップする(非recap時の新規作成
			// パスへは落とさない)。
			if at, score := bestSameKindMatch(diff.Items, candidate, true); at >= 0 && score >= 0.16 {
				diff.Items[at].EvidenceSequenceNos = appendUniqueSequence(diff.Items[at].EvidenceSequenceNos, candidate.SequenceNo)
				appendIssueOpenUpdate(&diff, modelItemReference(diff.Items[at]), candidate)
				audit.ExistingMerged++
				audit.RecapMerged++
				continue
			}
			if at, score := bestSameKindMatch(previous.Items, candidate, true); at >= 0 && score >= 0.16 {
				updated := previous.Items[at]
				updated.Status = "updated"
				updated.EvidenceSequenceNos = []int64{candidate.SequenceNo}
				diff.Items = append(diff.Items, updated)
				appendIssueOpenUpdate(&diff, updated.ID, candidate)
				audit.ExistingMerged++
				audit.RecapMerged++
				continue
			}
			continue
		}
		// The model may already have represented the unresolved proposition as
		// a concrete TODO/risk/fact rather than kind=issue. Before creating the
		// server fallback, compare the candidate against every model item using
		// evidence identity, unresolved state, subject overlap and semantic
		// specificity. Requiring subject overlap preserves independent issues
		// extracted from one utterance.
		if at, score, sameEvidence := bestRepresentedIssueCandidate(diff, candidate); at >= 0 {
			decision, reason := "suppressed", "model_item_represents_same_unresolved_evidence"
			if issueModelRepresentationAbstract(diff.Items[at]) && !issueTextNeedsReferent(candidate.Statement) {
				diff.Items[at].Title = issueCandidateTitle(candidate)
				diff.Items[at].Body = truncateRunes(candidate.Statement, liveAnalysisTreeDescriptionMaxRunes)
				diff.Items[at].InformationStatus = informationStatusGrounded
				diff.Items[at].EvidenceSequenceNos = appendUniqueSequence(diff.Items[at].EvidenceSequenceNos, candidate.SequenceNo)
				decision, reason = "rewritten", "abstract_model_item_enriched_from_same_evidence"
			}
			audit.ExistingMerged++
			audit.SameEvidenceSynthesisSuppressed++
			audit.Decisions = append(audit.Decisions, issueSynthesisDecision{
				SequenceNo: candidate.SequenceNo, Subtype: candidate.Subtype,
				MatchedItem: modelItemReference(diff.Items[at]), Decision: decision,
				Reason:     reason,
				MatchScore: score, SameEvidence: sameEvidence,
				GeneratedBy: "open_issue_synthesis",
				EvidenceHash: itemEvidenceFingerprint(liveAnalysisItem{
					EvidenceSequenceNos: []int64{candidate.SequenceNo},
				}),
				ParentID:   issueItemParent(diff, diff.Items[at]),
				AgendaRefs: append([]string(nil), diff.Items[at].RelatedAgendaIDs...),
				Status:     diff.Items[at].ClassificationStatus,
				MergedInto: modelItemReference(diff.Items[at]),
			})
			continue
		}
		if at, score := bestSameKindMatch(diff.Items, candidate, false); at >= 0 && score >= 0.16 {
			diff.Items[at].EvidenceSequenceNos = appendUniqueSequence(diff.Items[at].EvidenceSequenceNos, candidate.SequenceNo)
			appendIssueOpenUpdate(&diff, modelItemReference(diff.Items[at]), candidate)
			audit.ExistingMerged++
			audit.Decisions = append(audit.Decisions, issueSynthesisDecision{
				SequenceNo: candidate.SequenceNo, Subtype: candidate.Subtype,
				MatchedItem: modelItemReference(diff.Items[at]), Decision: "merged",
				Reason: "same_kind_semantic_match", MatchScore: score,
				SameEvidence: containsInt64(diff.Items[at].EvidenceSequenceNos, candidate.SequenceNo),
				GeneratedBy:  "open_issue_synthesis",
				EvidenceHash: itemEvidenceFingerprint(diff.Items[at]),
				ParentID:     issueItemParent(diff, diff.Items[at]),
				AgendaRefs:   append([]string(nil), diff.Items[at].RelatedAgendaIDs...),
				Status:       diff.Items[at].ClassificationStatus,
				MergedInto:   modelItemReference(diff.Items[at]),
			})
			continue
		}
		if at, score := bestSameKindMatch(previous.Items, candidate, false); at >= 0 && score >= 0.22 {
			updated := previous.Items[at]
			updated.Status = "updated"
			updated.EvidenceSequenceNos = []int64{candidate.SequenceNo}
			diff.Items = append(diff.Items, updated)
			appendIssueOpenUpdate(&diff, updated.ID, candidate)
			audit.ExistingMerged++
			audit.Decisions = append(audit.Decisions, issueSynthesisDecision{
				SequenceNo: candidate.SequenceNo, Subtype: candidate.Subtype,
				MatchedItem: updated.ID, Decision: "merged",
				Reason: "previous_same_kind_semantic_match", MatchScore: score,
				SameEvidence: containsInt64(previous.Items[at].EvidenceSequenceNos, candidate.SequenceNo),
				GeneratedBy:  "open_issue_synthesis",
				EvidenceHash: itemEvidenceFingerprint(updated),
				AgendaRefs:   append([]string(nil), updated.RelatedAgendaIDs...),
				Status:       updated.ClassificationStatus,
				MergedInto:   updated.ID,
			})
			continue
		}

		id := stableIssueID(candidate.Subtype, candidate.Statement)
		if existing := findItemByID(previous.Items, id); existing != nil {
			updated := *existing
			updated.Status = "updated"
			updated.EvidenceSequenceNos = []int64{candidate.SequenceNo}
			diff.Items = append(diff.Items, updated)
			appendIssueOpenUpdate(&diff, updated.ID, candidate)
			audit.ExistingMerged++
			audit.Decisions = append(audit.Decisions, issueSynthesisDecision{
				SequenceNo: candidate.SequenceNo, Subtype: candidate.Subtype,
				MatchedItem: updated.ID, Decision: "merged",
				Reason:       "stable_issue_id_match",
				SameEvidence: containsInt64(existing.EvidenceSequenceNos, candidate.SequenceNo),
				GeneratedBy:  "open_issue_synthesis",
				EvidenceHash: itemEvidenceFingerprint(updated),
				AgendaRefs:   append([]string(nil), updated.RelatedAgendaIDs...),
				Status:       updated.ClassificationStatus,
				MergedInto:   updated.ID,
			})
			continue
		}
		parentID := relatedCandidateParent(diff, candidate.Statement)
		diff.Items = append(diff.Items, liveAnalysisItem{
			ID: id, Kind: "issue", Subtype: candidate.Subtype, Severity: "medium",
			Title: issueCandidateTitle(candidate), Body: candidate.Statement,
			Status: "open", InformationStatus: informationStatusGrounded, EvidenceSequenceNos: []int64{candidate.SequenceNo},
		})
		diff.Assignments = append(diff.Assignments, treeAssignment{NodeID: id, ParentTopicID: parentID, Confidence: 0.6, Reason: issueSynthesisAssignmentReason})
		if candidate.Subtype == issueSubtypeQuestion {
			audit.QuestionsAccepted++
		} else {
			audit.OpenIssuesAccepted++
		}
		audit.Decisions = append(audit.Decisions, issueSynthesisDecision{
			SequenceNo: candidate.SequenceNo, Subtype: candidate.Subtype,
			MatchedItem: id, Decision: "created", Reason: "no_existing_representation",
			SameEvidence: true, GeneratedBy: "open_issue_synthesis",
			EvidenceHash: itemEvidenceFingerprint(liveAnalysisItem{
				EvidenceSequenceNos: []int64{candidate.SequenceNo},
			}),
			ParentID: parentID,
		})
	}
	encoded, err := json.Marshal(diff)
	if err != nil {
		return content, audit, err
	}
	return string(encoded), audit, nil
}

func issueItemParent(diff liveAnalysisPayload, item liveAnalysisItem) string {
	reference := canonicalReferenceKey(modelItemReference(item))
	for _, assignment := range diff.Assignments {
		if canonicalReferenceKey(assignment.nodeID()) == reference {
			return strings.TrimSpace(assignment.ParentTopicID)
		}
	}
	return ""
}

func bestRepresentedIssueCandidate(diff liveAnalysisPayload, candidate issueCandidate) (int, float64, bool) {
	bestAt, bestScore, bestSameEvidence := -1, 0.0, false
	candidateText := strings.TrimSpace(candidate.Statement)
	candidateSubject := semanticIssueKey(candidateText)
	candidateNeedsReferent := issueTextNeedsReferent(candidateText)
	candidateParent := relatedCandidateParent(diff, candidate.Statement)
	for i, item := range diff.Items {
		if item.Inactive || item.MergedIntoID != "" {
			continue
		}
		itemText := strings.TrimSpace(item.Title + " " + item.Body)
		itemSubject := semanticIssueKey(itemText)
		sameEvidence := containsInt64(item.EvidenceSequenceNos, candidate.SequenceNo)
		similarity := semanticItemSimilarity(itemText, candidateText)
		subjectSimilarity := semanticItemSimilarity(itemSubject, candidateSubject)
		sharedSubject := itemSubject != "" && candidateSubject != "" &&
			sharedTreeAuditSubjectTerm(itemSubject, candidateSubject) &&
			subjectSimilarity >= 0.18
		unresolved := openIssueMarkerPattern.MatchString(itemText) ||
			(item.Kind == "issue" && item.Status != "resolved" && item.Status != "dismissed")
		abstractRepresentation := sameEvidence && issueModelRepresentationAbstract(item) && !candidateNeedsReferent

		// Evidence identity alone is insufficient: a single utterance can state
		// several independent unresolved propositions. A referent-free fallback
		// carries no independent subject, so an unresolved model item on the same
		// evidence is the only safe canonical representation for it.
		if !unresolved || (!abstractRepresentation && !sharedSubject && subjectSimilarity < 0.48 && !candidateNeedsReferent) {
			continue
		}
		score := similarity
		if sameEvidence {
			score += 0.55
		}
		if sharedSubject {
			score += 0.20
		}
		if unresolved {
			score += 0.15
		}
		if item.Kind == "issue" {
			score += 0.10
			if item.Subtype == candidate.Subtype {
				score += 0.08
			}
		}
		itemParent := issueItemParent(diff, item)
		if itemParent != "" && itemParent == candidateParent {
			score += 0.08
		}
		if candidateParent != "" && containsExactString(item.RelatedAgendaIDs, candidateParent) {
			score += 0.05
		}
		if candidateNeedsReferent && sameEvidence {
			score += 0.10
		}
		if abstractRepresentation {
			score += 0.25
		}
		threshold := 0.72
		if !sameEvidence {
			threshold = 0.90
		}
		if score >= threshold && score > bestScore {
			bestAt, bestScore, bestSameEvidence = i, score, sameEvidence
		}
	}
	return bestAt, bestScore, bestSameEvidence
}

func issueModelRepresentationAbstract(item liveAnalysisItem) bool {
	text := strings.TrimSpace(item.Title + " " + item.Body)
	return liveItemTextNeedsReferent(item) ||
		metaOnlyLiveItemText(text) ||
		len([]rune(semanticIssueKey(text))) < 8
}

func mergeIssueRecap(diff *liveAnalysisPayload, previous []liveAnalysisItem, candidate issueCandidate) bool {
	matched := false
	seen := make(map[string]struct{}, len(diff.Items))
	recapKind := func(kind string) bool {
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "issue", "risk":
			return true
		default:
			return false
		}
	}
	for i := range diff.Items {
		seen[modelItemReference(diff.Items[i])] = struct{}{}
		if !recapKind(diff.Items[i].Kind) {
			continue
		}
		if semanticItemSimilarity(diff.Items[i].Title+" "+diff.Items[i].Body, candidate.Statement) < 0.12 {
			continue
		}
		diff.Items[i].EvidenceSequenceNos = appendUniqueSequence(diff.Items[i].EvidenceSequenceNos, candidate.SequenceNo)
		appendIssueOpenUpdate(diff, modelItemReference(diff.Items[i]), candidate)
		matched = true
	}
	// Recaps often mention an existing unresolved item without the model also
	// returning that item in the current diff. Update those canonical records
	// instead of creating a new broad "未確定の課題は…" item.
	for _, item := range previous {
		if _, alreadyInDiff := seen[item.ID]; alreadyInDiff || !recapKind(item.Kind) {
			continue
		}
		if semanticItemSimilarity(item.Title+" "+item.Body, candidate.Statement) < 0.12 {
			continue
		}
		item.Status = "updated"
		item.EvidenceSequenceNos = []int64{candidate.SequenceNo}
		diff.Items = append(diff.Items, item)
		appendIssueOpenUpdate(diff, item.ID, candidate)
		seen[item.ID] = struct{}{}
		matched = true
	}
	return matched
}

func appendIssueOpenUpdate(diff *liveAnalysisPayload, itemID string, candidate issueCandidate) {
	if diff == nil || strings.TrimSpace(itemID) == "" || !resolutionOpenPattern.MatchString(candidate.Statement) {
		return
	}
	for _, update := range diff.ResolutionUpdates {
		if canonicalReferenceKey(update.ItemID) == canonicalReferenceKey(itemID) && normalizeResolutionStatus(update.Status) == "open" {
			return
		}
	}
	diff.ResolutionUpdates = append(diff.ResolutionUpdates, resolutionUpdate{
		ItemID: itemID, Status: "open", EvidenceSequenceNos: []int64{candidate.SequenceNo}, Reason: "explicit unresolved statement",
	})
}

// bestSameKindMatch finds the best issue match for candidate. allowAnySubtype
// disables the subtype equality check; it is only set true for recap
// candidates, where a discussion issue may legitimately be the canonical
// target for a recap statement whose own subtype classifier guessed
// differently (e.g. a recap clause split into an "investigation" statement
// that really refers to an existing "discussion" issue).
func bestSameKindMatch(items []liveAnalysisItem, candidate issueCandidate, allowAnySubtype bool) (int, float64) {
	bestAt, bestScore := -1, 0.0
	for i, item := range items {
		if strings.ToLower(strings.TrimSpace(item.Kind)) != "issue" {
			continue
		}
		if !allowAnySubtype && item.Subtype != candidate.Subtype {
			continue
		}
		score := semanticItemSimilarity(item.Title+" "+item.Body, candidate.Statement)
		if score > bestScore {
			bestAt, bestScore = i, score
		}
	}
	return bestAt, bestScore
}

func relatedCandidateParent(diff liveAnalysisPayload, statement string) string {
	parents := make(map[string]string)
	for _, assignment := range diff.Assignments {
		parents[canonicalReferenceKey(assignment.nodeID())] = strings.TrimSpace(assignment.ParentTopicID)
	}
	bestParent, bestScore := "", 0.0
	for _, item := range diff.Items {
		parent := parents[canonicalReferenceKey(modelItemReference(item))]
		if parent == "" {
			continue
		}
		score := semanticItemSimilarity(item.Title+" "+item.Body, statement)
		if score > bestScore {
			bestParent, bestScore = parent, score
		}
	}
	if bestScore >= 0.12 {
		return bestParent
	}
	return treeUnclassifiedTopicID
}

func semanticIssueKey(value string) string {
	key := semanticItemKey(value)
	for _, boilerplate := range []string{"まだ", "現時点では", "未決定", "未確定", "未解決", "決まっていない", "決まっていません", "ですか", "ますか"} {
		key = strings.ReplaceAll(key, boilerplate, "")
	}
	return key
}

func stableIssueID(kind, statement string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + semanticIssueKey(statement)))
	prefix := strings.ReplaceAll(kind, "_", "-")
	return "issue-" + prefix + "-auto-" + hex.EncodeToString(sum[:6])
}

func issueCandidateTitle(candidate issueCandidate) string {
	title := strings.Trim(strings.TrimSpace(candidate.Statement), "、。？? ")
	if candidate.Subtype != issueSubtypeQuestion && !strings.Contains(title, "未確定") && openIssueMarkerPattern.MatchString(title) {
		title = openIssueMarkerPattern.ReplaceAllString(title, "未確定")
	}
	return truncateRunes(title, 40)
}

func splitCollapsedOpenIssueStatement(statement string) []string {
	item := liveAnalysisItem{Kind: "issue", Subtype: issueSubtypeDiscussion, EvidenceSequenceNos: []int64{1}}
	scope := liveEvidenceScope{TranscriptText: map[int64]string{1: statement}}
	return concreteIssueStatements(item, scope)
}
