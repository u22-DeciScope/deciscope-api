package application

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	issueAnaphoraPattern              = regexp.MustCompile(`(?:この点|本件|それ|上記|前述|当該事項|引き続き)`)
	issueReferentFreePredicatePattern = regexp.MustCompile(`^(?:[[:space:]、。,.!！?？]|は|が|を|の|に|へ|と|も|では|について|として|引き続き|継続|確認|検討|対応|調査|事項|必要|未確認|未確定|未解決|不明|できていない|です|ます|する|します|したい|行う|行います|残す|残し|残して|残しています|残る|残ります)+$`)
	// Capture the grammatical subject immediately before an unresolved-state
	// predicate. Starting at the nearest clause boundary avoids treating a
	// sentence such as "この点は…ため、現時点では未確定です" as a list of
	// independent subjects merely because an earlier verb contains 「と」.
	collapsedOpenIssuePattern    = regexp.MustCompile(`(?:^|[、,])([^、,。]{2,80}?)(?:が|は|では)(?:未解決|未確定)(?:の)?(?:事項|課題|調査事項)?(?:として)?(?:残し|残す|残ります|です)`)
	issueSubjectSeparatorPattern = regexp.MustCompile(`(?:と|および|及び|ならびに|並びに|、)`)
	confirmationStatementPattern = regexp.MustCompile(`(?:確認(?:します|する|が必要|が必要です|したい)|問い合わせ(?:ます|る)|不明)`)
	contextQuestionPattern       = regexp.MustCompile(`^(.+?)(?:は|を)?(?:何[^。]*か|どの[^。]*か)$`)
)

// repairLowInformationIssueItems runs before canonical ID assignment. It
// prefers concrete rewrite/split and keeps a still-unresolvable substantive
// issue as tentative; it never deactivates an item.
func repairLowInformationIssueItems(previous, diff []liveAnalysisItem, assignments []treeAssignment, timeline discourseTimeline, scope liveEvidenceScope, stats *liveAnalysisTreeMergeStats) ([]liveAnalysisItem, []treeAssignment) {
	repaired := make([]liveAnalysisItem, 0, len(diff)+2)
	replacementRefs := make(map[string][]string)
	seenRefs := make(map[string]struct{}, len(diff))
	for _, item := range diff {
		seenRefs[canonicalReferenceKey(modelItemReference(item))] = struct{}{}
		if item.Kind != "issue" {
			repaired = append(repaired, item)
			continue
		}
		statements := concreteIssueStatements(item, scope)
		if len(statements) > 1 {
			originalRef := modelItemReference(item)
			refs := make([]string, 0, len(statements))
			for index, statement := range statements {
				split := item
				split.Title = truncateRunes(statement, 40)
				split.Body = statement
				split.Subtype = inferIssueSubtype(statement, item.Subtype)
				split.InformationStatus = informationStatusGrounded
				ref := fmt.Sprintf("%s-split-%d", originalRef, index+1)
				if strings.TrimSpace(item.ClientKey) != "" {
					split.ClientKey, split.ID = ref, ""
				} else {
					split.ClientKey, split.ID = "", ref
				}
				refs = append(refs, ref)
				repaired = append(repaired, split)
			}
			replacementRefs[canonicalReferenceKey(originalRef)] = refs
			if stats != nil {
				stats.LowInformationItemsSplit++
			}
			continue
		}

		if issueTextNeedsReferent(item.Title + " " + item.Body) {
			if concrete := nearestConcreteIssueEvidence(item, scope, timeline); concrete != "" {
				concrete = normalizeIssueStatementForSubtype(concrete, item.Subtype)
				item.Title = truncateRunes(concrete, 40)
				item.Body = concrete
				if !validIssueSubtype(item.Subtype) {
					item.Subtype = inferIssueSubtype(concrete, item.Subtype)
				}
				item.InformationStatus = informationStatusGrounded
				if stats != nil {
					stats.LowInformationItemsRewritten++
				}
			} else {
				item.InformationStatus = informationStatusTentative
			}
		} else {
			item.InformationStatus = informationStatusGrounded
		}
		repaired = append(repaired, item)
	}

	// A tentative issue may be absent from the model diff when the next
	// utterance finally names its subject. Promote that same canonical item
	// instead of creating a second card or losing the provisional one.
	for _, prior := range previous {
		if prior.Kind != "issue" || prior.InformationStatus != informationStatusTentative {
			continue
		}
		if _, alreadyUpdated := seenRefs[canonicalReferenceKey(modelItemReference(prior))]; alreadyUpdated {
			continue
		}
		concrete := nearestConcreteIssueEvidence(prior, scope, timeline)
		if concrete == "" {
			continue
		}
		update := prior
		update.Title = truncateRunes(concrete, 40)
		update.Body = concrete
		update.Subtype = inferIssueSubtype(concrete, prior.Subtype)
		update.Status = "updated"
		update.InformationStatus = informationStatusGrounded
		for sequenceNo := range scope.CurrentRound {
			if strings.TrimSpace(scope.TranscriptText[sequenceNo]) == strings.TrimSpace(strings.TrimSuffix(concrete, "が必要")) || semanticItemSimilarity(concrete, scope.TranscriptText[sequenceNo]) >= 0.12 {
				update.EvidenceSequenceNos = appendUniqueSequence(update.EvidenceSequenceNos, sequenceNo)
			}
		}
		repaired = append(repaired, update)
		if stats != nil {
			stats.LowInformationItemsRewritten++
		}
	}

	if len(replacementRefs) == 0 {
		return repaired, assignments
	}
	repairedAssignments := make([]treeAssignment, 0, len(assignments)+len(replacementRefs))
	for _, assignment := range assignments {
		refs := replacementRefs[canonicalReferenceKey(assignment.nodeID())]
		if len(refs) == 0 {
			repairedAssignments = append(repairedAssignments, assignment)
			continue
		}
		for _, ref := range refs {
			copy := assignment
			copy.NodeID, copy.ItemID = ref, ""
			repairedAssignments = append(repairedAssignments, copy)
		}
	}
	return repaired, repairedAssignments
}

func concreteIssueStatements(item liveAnalysisItem, scope liveEvidenceScope) []string {
	for _, sequenceNo := range item.EvidenceSequenceNos {
		text := strings.Trim(strings.TrimSpace(scope.TranscriptText[sequenceNo]), "。.!！ ")
		match := collapsedOpenIssuePattern.FindStringSubmatch(text)
		if len(match) != 2 {
			continue
		}
		parts := issueSubjectSeparatorPattern.Split(strings.TrimSpace(match[1]), -1)
		if len(parts) < 2 || len(parts) > 4 {
			continue
		}
		statements := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.Trim(strings.TrimSpace(part), "、。 ")
			if len([]rune(semanticItemKey(part))) < 2 {
				statements = nil
				break
			}
			switch {
			case strings.Contains(part, "原因"):
				statements = append(statements, part+"が特定できていない")
			case strings.Contains(part, "アラート") && strings.Contains(part, "条件"):
				part = strings.Replace(part, "アラートの条件", "アラートの発報条件", 1)
				statements = append(statements, part+"が確定していない")
			default:
				statements = append(statements, part+"が未確定")
			}
		}
		if len(statements) > 1 {
			return statements
		}
	}
	return nil
}

func issueTextNeedsReferent(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || metaOnlyLiveItemText(text) {
		return true
	}
	if !issueAnaphoraPattern.MatchString(text) {
		return false
	}
	withoutAnaphora := issueAnaphoraPattern.ReplaceAllString(text, "")
	semanticRemainder := semanticItemKey(withoutAnaphora)
	return len([]rune(semanticRemainder)) < 6 || issueReferentFreePredicatePattern.MatchString(withoutAnaphora)
}

func nearestConcreteIssueEvidence(item liveAnalysisItem, scope liveEvidenceScope, timelines ...discourseTimeline) string {
	var timeline discourseTimeline
	if len(timelines) > 0 {
		timeline = timelines[0]
	}
	bestSequence := int64(0)
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if sequenceNo > bestSequence {
			bestSequence = sequenceNo
		}
	}
	for distance := int64(0); distance <= 3; distance++ {
		for _, sequenceNo := range []int64{bestSequence + distance, bestSequence - distance} {
			if crossesIssueDiscourseBoundary(bestSequence, sequenceNo, timeline) {
				continue
			}
			role := timeline.DetectedRoles[sequenceNo]
			if role == liveUtteranceDiscourseTransition || role == liveUtteranceFiller || role == liveUtteranceRecap {
				continue
			}
			text := strings.Trim(strings.TrimSpace(scope.TranscriptText[sequenceNo]), "。.!！ ")
			if text == "" || issueTextNeedsReferent(text) || isDiscourseOnlyItem(text, "") {
				continue
			}
			if len([]rune(semanticItemKey(text))) < 4 {
				continue
			}
			return normalizeConcreteIssueStatement(text)
		}
	}
	return ""
}

func crossesIssueDiscourseBoundary(from, to int64, timeline discourseTimeline) bool {
	if from == 0 || to == 0 || from == to {
		return false
	}
	start, end := from, to
	if start > end {
		start, end = end, start
	}
	for sequenceNo := start + 1; sequenceNo <= end; sequenceNo++ {
		if timeline.DetectedRoles[sequenceNo] == liveUtteranceDiscourseTransition {
			return true
		}
	}
	return false
}

func normalizeIssueStatementForSubtype(text, subtype string) string {
	if subtype != issueSubtypeDiscussion && subtype != issueSubtypeInvestigation {
		return text
	}
	if match := contextQuestionPattern.FindStringSubmatch(strings.TrimSpace(text)); len(match) == 2 {
		return strings.TrimSpace(match[1]) + "が未確定"
	}
	return text
}

func normalizeConcreteIssueStatement(text string) string {
	text = strings.Trim(strings.TrimSpace(text), "、。 ")
	for _, ending := range []string{"確認します", "確認する", "問い合わせます", "問い合わせる"} {
		if strings.HasSuffix(text, ending) {
			return strings.TrimSuffix(text, ending) + "確認が必要"
		}
	}
	return text
}

func inferIssueSubtype(text, fallback string) string {
	switch {
	case strings.Contains(text, "原因") || strings.Contains(text, "調査"):
		return issueSubtypeInvestigation
	case confirmationStatementPattern.MatchString(text):
		return issueSubtypeConfirmation
	case lowInformationQuestionPattern.MatchString(text):
		return issueSubtypeQuestion
	case validIssueSubtype(fallback):
		return fallback
	default:
		return issueSubtypeDiscussion
	}
}
