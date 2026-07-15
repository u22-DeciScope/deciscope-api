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
	Kind       string
	Statement  string
}

type issueExtractionAudit struct {
	QuestionCandidates  int
	OpenIssueCandidates int
	QuestionsAccepted   int
	OpenIssuesAccepted  int
	ExistingMerged      int
	RecapMerged         int
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
			kinds := make([]string, 0, 2)
			if openIssueMarkerPattern.MatchString(clause) {
				kinds = append(kinds, "open_issue")
			}
			if questionMarkerPattern.MatchString(clause) {
				kinds = append(kinds, "question")
			}
			for _, kind := range kinds {
				key := kind + "\x00" + semanticIssueKey(clause) + "\x00" + strconv.FormatInt(segment.SequenceNo, 10)
				if _, duplicate := seen[key]; duplicate {
					continue
				}
				seen[key] = struct{}{}
				candidates = append(candidates, issueCandidate{SequenceNo: segment.SequenceNo, Kind: kind, Statement: clause})
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
	if len(candidates) == 0 {
		return cleaned, audit, nil
	}
	previous := previousLiveAnalysisState(previousPayload)
	for _, candidate := range candidates {
		if candidate.Kind == "question" {
			audit.QuestionCandidates++
		} else {
			audit.OpenIssueCandidates++
		}
		if issueRecapPattern.MatchString(candidate.Statement) && mergeIssueRecap(&diff, previous.Items, candidate) {
			audit.ExistingMerged++
			audit.RecapMerged++
			continue
		}
		if at, score := bestSameKindMatch(diff.Items, candidate); at >= 0 && score >= 0.16 {
			diff.Items[at].EvidenceSequenceNos = appendUniqueSequence(diff.Items[at].EvidenceSequenceNos, candidate.SequenceNo)
			appendIssueOpenUpdate(&diff, modelItemReference(diff.Items[at]), candidate)
			audit.ExistingMerged++
			continue
		}
		if at, score := bestSameKindMatch(previous.Items, candidate); at >= 0 && score >= 0.22 {
			updated := previous.Items[at]
			updated.Status = "updated"
			updated.EvidenceSequenceNos = []int64{candidate.SequenceNo}
			diff.Items = append(diff.Items, updated)
			appendIssueOpenUpdate(&diff, updated.ID, candidate)
			audit.ExistingMerged++
			continue
		}

		id := stableIssueID(candidate.Kind, candidate.Statement)
		if existing := findItemByID(previous.Items, id); existing != nil {
			updated := *existing
			updated.Status = "updated"
			updated.EvidenceSequenceNos = []int64{candidate.SequenceNo}
			diff.Items = append(diff.Items, updated)
			appendIssueOpenUpdate(&diff, updated.ID, candidate)
			audit.ExistingMerged++
			continue
		}
		parentID := relatedCandidateParent(diff, candidate.Statement)
		diff.Items = append(diff.Items, liveAnalysisItem{
			ID: id, Kind: candidate.Kind, Severity: "medium",
			Title: issueCandidateTitle(candidate), Body: candidate.Statement,
			Status: "open", EvidenceSequenceNos: []int64{candidate.SequenceNo},
		})
		diff.Assignments = append(diff.Assignments, treeAssignment{NodeID: id, ParentTopicID: parentID, Confidence: 0.6, Reason: "server unresolved candidate"})
		if candidate.Kind == "question" {
			audit.QuestionsAccepted++
		} else {
			audit.OpenIssuesAccepted++
		}
	}
	encoded, err := json.Marshal(diff)
	if err != nil {
		return content, audit, err
	}
	return string(encoded), audit, nil
}

func mergeIssueRecap(diff *liveAnalysisPayload, previous []liveAnalysisItem, candidate issueCandidate) bool {
	matched := false
	seen := make(map[string]struct{}, len(diff.Items))
	recapKind := func(kind string) bool {
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "question", "open_issue", "issue", "risk":
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

func bestSameKindMatch(items []liveAnalysisItem, candidate issueCandidate) (int, float64) {
	bestAt, bestScore := -1, 0.0
	for i, item := range items {
		if strings.ToLower(strings.TrimSpace(item.Kind)) != candidate.Kind {
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
	return prefix + "-auto-" + hex.EncodeToString(sum[:6])
}

func issueCandidateTitle(candidate issueCandidate) string {
	title := strings.Trim(strings.TrimSpace(candidate.Statement), "、。？? ")
	if candidate.Kind == "open_issue" && !strings.Contains(title, "未確定") && openIssueMarkerPattern.MatchString(title) {
		title = openIssueMarkerPattern.ReplaceAllString(title, "未確定")
	}
	return truncateRunes(title, 40)
}
