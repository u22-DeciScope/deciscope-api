package application

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	issueAnaphoraPattern              = regexp.MustCompile(`(?:(?:この|その)(?:点|問題|件|条件|事項)|本件|それ|上記|前述|当該(?:事項|条件|問題|件)|引き続き)`)
	issueReferentFreePredicatePattern = regexp.MustCompile(`^(?:[[:space:]、。,.!！?？]|は|が|を|の|に|へ|と|も|では|について|として|引き続き|継続|確認|検討|対応|調査|点|問題|件|条件|事項|必要|未確認|未確定|未解決|不明|できていない|です|ます|する|します|したい|行う|行います|残す|残し|残して|残しています|残る|残ります)+$`)
	// A meeting-management act is not itself the subject of an Issue. When it
	// is headed by an anaphor, the concrete proposition must be reconstructed
	// from the same utterance or a bounded adjacent utterance before grounding.
	issueMetaActPattern              = regexp.MustCompile(`(?:(?:この|その|本)(?:点|件|事項|問題)?|それ|上記|前述|当該事項)?(?:は|を)?(?:未解決の)?(?:論点|調査事項|確認事項|対応事項|課題)(?:として|に)?(?:起こ|挙げ|残|扱|登録|記録|保持|持ち越|管理)(?:します|する|しておきます|しておく|とします|ます)?`)
	issueBareTemporalFragmentPattern = regexp.MustCompile(`^(?:午前|午後)?[0-9０-９]{1,2}時(?:[0-9０-９]{1,2}分)?(?:ごろ|頃)?(?:に|です|でした)?[。.!！]?$`)
	// Capture the grammatical subject immediately before an unresolved-state
	// predicate. Starting at the nearest clause boundary avoids treating a
	// sentence such as "この点は…ため、現時点では未確定です" as a list of
	// independent subjects merely because an earlier verb contains 「と」.
	collapsedOpenIssuePattern            = regexp.MustCompile(`(?:^|[、,])([^、,。]{2,80}?)(?:が|は|では)[、,]?(?:未解決|未確定)(?:の)?(?:事項|課題|調査事項)?(?:として)?(?:残し|残す|残ります|です)`)
	issueSubjectSeparatorPattern         = regexp.MustCompile(`(?:と|および|及び|ならびに|並びに|、)`)
	confirmationStatementPattern         = regexp.MustCompile(`(?:確認(?:します|する|が必要|が必要です|したい)|問い合わせ(?:ます|る)|不明)`)
	contextQuestionPattern               = regexp.MustCompile(`^(.+?)(?:は|を)?(?:何[^。]*か|どの[^。]*か)$`)
	genericQuestionWithoutSubjectPattern = regexp.MustCompile(`^(?:(?:何|なに)(?:が|は)?(?:原因|理由|問題|課題|対象|方法|条件|結果)|(?:どう|なぜ|いつ|誰|どこ|どれ)(?:です|でした|します|する|なります|なる)?)(?:です|でした|でしょう|なの)?(?:か)?$`)
	// Detect a broken noun compound structurally rather than banning a known
	// mistranscription. A content noun immediately glued to a personal or
	// demonstrative pronoun (without a particle/clause boundary) is a common
	// Japanese STT splice shape and is unsafe as a standalone split subject.
	malformedPronounCompoundPattern = regexp.MustCompile(`[一-龠々ァ-ヶー]{2,}(?:あなた|わたし|われわれ|我々|彼|彼女|これ|それ|あれ|ここ|そこ|あそこ)(?:の|が|は|を|に|へ)`)
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
				split.Title = semanticallyCompleteItemLabelOrOriginal(statement, split.Kind)
				split.Body = statement
				split.Subtype = inferIssueSubtype(statement, item.Subtype)
				split.InformationStatus = informationStatusGrounded
				ref := fmt.Sprintf("%s-split-%d", originalRef, index+1)
				if strings.TrimSpace(item.ClientKey) != "" {
					split.ClientKey, split.ID = ref, ""
				} else {
					split.ClientKey, split.ID = "", ref
				}
				reason, role := validateLiveItemInformation(split, false, timeline, scope)
				semanticCoherence := splitIssueFragmentSemanticallyCoherent(split)
				if reason == "" && !semanticCoherence {
					reason = "split_fragment_semantically_incoherent"
				}
				if reason == "" && !splitIssueFragmentGrounded(split, scope) {
					reason = "split_fragment_not_grounded"
				}
				if reason != "" {
					if stats != nil {
						stats.LowInformationSplitFragmentsRejected++
						stats.LowInformationItemsRejected++
						stats.LowInformationRejections = append(stats.LowInformationRejections, liveItemRejection{
							ModelItemID: originalRef, CanonicalItemID: ref, Kind: split.Kind,
							GeneratedBy: "split", SourceItemID: originalRef, FragmentIndex: index + 1,
							EvidenceSequenceNos: append([]int64(nil), split.EvidenceSequenceNos...),
							Reason:              reason, DetectedRole: role,
							SubjectComplete:     !liveItemTextNeedsReferent(split),
							AnaphoraDetected:    issueAnaphoraPattern.MatchString(split.Title + " " + split.Body),
							SemanticCoherent:    semanticCoherence,
							RewriteCandidate:    concreteIssueRepairText(split, scope, timeline) != "",
							ExistingItemMatchID: lowInformationExistingItemMatch(previous, split),
							FinalDecision:       "rejected",
						})
					}
					continue
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

		if issueTextNeedsReferent(item.Title) {
			if concrete := concreteIssueRepairText(item, scope, timeline); concrete != "" {
				if sourceSequenceNo := concreteIssueRepairEvidenceSequence(
					item, concrete, scope, timeline,
				); sourceSequenceNo > 0 {
					item.EvidenceSequenceNos = appendUniqueSequence(
						item.EvidenceSequenceNos, sourceSequenceNo,
					)
				}
				concrete = normalizeIssueStatementForSubtype(concrete, item.Subtype)
				item.Title = semanticallyCompleteItemLabelOrOriginal(concrete, item.Kind)
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
	// utterance finally names its subject. A previously-grounded legacy item can
	// also still carry a generic question title while its body already contains
	// the subject. Update that same canonical item instead of creating a second
	// card or leaving the low-information title in the persisted tree.
	for _, prior := range previous {
		if prior.Kind != "issue" || (prior.InformationStatus != informationStatusTentative && !issueTextNeedsReferent(prior.Title)) {
			continue
		}
		if _, alreadyUpdated := seenRefs[canonicalReferenceKey(modelItemReference(prior))]; alreadyUpdated {
			continue
		}
		concrete := concreteIssueRepairText(prior, scope, timeline)
		if concrete == "" {
			continue
		}
		update := prior
		update.Title = semanticallyCompleteItemLabelOrOriginal(concrete, update.Kind)
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
		refs, replaced := replacementRefs[canonicalReferenceKey(assignment.nodeID())]
		if !replaced {
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

func splitIssueFragmentGrounded(item liveAnalysisItem, scope liveEvidenceScope) bool {
	itemText := strings.TrimSpace(item.Title + " " + item.Body)
	for _, sequenceNo := range item.EvidenceSequenceNos {
		evidence := strings.TrimSpace(scope.TranscriptText[sequenceNo])
		if evidence == "" {
			continue
		}
		if sharedTreeAuditSubjectTerm(itemText, evidence) ||
			semanticItemSimilarity(itemText, evidence) >= 0.12 {
			return true
		}
	}
	return false
}

func splitIssueFragmentSemanticallyCoherent(item liveAnalysisItem) bool {
	text := strings.TrimSpace(item.Title + " " + item.Body)
	return text != "" && !malformedPronounCompoundPattern.MatchString(text)
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
	if issueBareTemporalFragmentPattern.MatchString(text) {
		return true
	}
	if issueMetaActPattern.MatchString(text) &&
		(issueAnaphoraPattern.MatchString(text) ||
			len([]rune(semanticItemKey(issueMetaActPattern.ReplaceAllString(text, "")))) < 6) {
		return true
	}
	if genericQuestionWithoutSubjectPattern.MatchString(normalizeDiscourseText(text)) {
		return true
	}
	if !issueAnaphoraPattern.MatchString(text) {
		return false
	}
	withoutAnaphora := issueAnaphoraPattern.ReplaceAllString(text, "")
	semanticRemainder := semanticItemKey(withoutAnaphora)
	return len([]rune(semanticRemainder)) < 6 || issueReferentFreePredicatePattern.MatchString(withoutAnaphora)
}

func concreteIssueRepairText(item liveAnalysisItem, scope liveEvidenceScope, timelines ...discourseTimeline) string {
	body := strings.Trim(strings.TrimSpace(item.Body), "。.!！ ")
	if body != "" && !issueTextNeedsReferent(body) && len([]rune(semanticItemKey(body))) >= 4 {
		return normalizeConcreteIssueStatement(body)
	}
	return nearestConcreteIssueEvidence(item, scope, timelines...)
}

func nearestConcreteIssueEvidence(item liveAnalysisItem, scope liveEvidenceScope, timelines ...discourseTimeline) string {
	text, _ := nearestConcreteIssueEvidenceWithSequence(item, scope, timelines...)
	return text
}

func nearestConcreteIssueEvidenceWithSequence(item liveAnalysisItem, scope liveEvidenceScope, timelines ...discourseTimeline) (string, int64) {
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
				if concrete := concreteIssueClause(text); concrete != "" {
					return normalizeConcreteIssueStatement(concrete), sequenceNo
				}
				continue
			}
			if len([]rune(semanticItemKey(text))) < 4 {
				continue
			}
			return normalizeConcreteIssueStatement(text), sequenceNo
		}
	}
	return "", 0
}

func concreteIssueRepairEvidenceSequence(
	item liveAnalysisItem,
	concrete string,
	scope liveEvidenceScope,
	timeline discourseTimeline,
) int64 {
	_, sequenceNo := nearestConcreteIssueEvidenceWithSequence(item, scope, timeline)
	if sequenceNo <= 0 {
		return 0
	}
	evidence := strings.TrimSpace(scope.TranscriptText[sequenceNo])
	if strings.Contains(evidence, concrete) ||
		semanticItemSimilarity(evidence, concrete) >= 0.10 {
		return sequenceNo
	}
	return 0
}

func concreteIssueClause(text string) string {
	clauses := decisionClauseSplitPattern.Split(strings.TrimSpace(text), -1)
	for _, clause := range clauses {
		clause = strings.Trim(strings.TrimSpace(clause), "、。.!！ ")
		if clause == "" || issueMetaActPattern.MatchString(clause) ||
			issueTextNeedsReferent(clause) || isDiscourseOnlyItem(clause, "") {
			continue
		}
		probe := liveAnalysisItem{Kind: "issue", Title: clause, Body: clause}
		features := inferItemSemanticFeatures(probe, liveEvidenceScope{})
		if features.CurrentProblemPresent || features.CausalHypothesisPresent ||
			kindOpenQuestionPattern.MatchString(clause) ||
			confirmationStatementPattern.MatchString(clause) {
			return clause
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
