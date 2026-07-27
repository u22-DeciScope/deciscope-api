package application

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

type itemKindValidationMode string

const (
	itemKindValidationLive   itemKindValidationMode = "live"
	itemKindValidationLegacy itemKindValidationMode = "legacy"
	itemKindValidationAudit  itemKindValidationMode = "audit"
	itemKindValidationFinal  itemKindValidationMode = "final"
)

type itemSemanticFeatures struct {
	TemporalScope              string
	EpistemicStatus            string
	SemanticRole               string
	NegativeImpactPresent      bool
	UncertaintyPresent         bool
	FutureEventPresent         bool
	CurrentProblemPresent      bool
	ConfirmedEvidencePresent   bool
	ActionVerbPresent          bool
	OwnerPresent               bool
	DeadlinePresent            bool
	DecisionOrCommitment       bool
	InvestigationIntentPresent bool
	MitigationIntentPresent    bool
	CausalHypothesisPresent    bool
	ProposalPresent            bool
	ConfirmationSupersedesOpen bool
}

type itemKindValidationDecision struct {
	Stage            string
	SequenceNos      []int64
	ItemID           string
	ModelItemID      string
	OriginalKind     string
	CanonicalKind    string
	OriginalSubtype  string
	CanonicalSubtype string
	Features         itemSemanticFeatures
	Decision         string
	Reason           string
	Confidence       float64
}

type itemKindSplitDecision struct {
	SourceItemID      string
	FragmentCount     int
	FragmentKinds     []string
	RejectedFragments int
	RelationsCreated  int
}

var (
	kindUncertaintyPattern      = regexp.MustCompile(`(?i)(?:可能性|おそれ|恐れ|懸念|かもしれ|なりかね|risk|may|might|could)`)
	kindCausalHypothesisPattern = regexp.MustCompile(
		`(?i)(?:(?:原因|要因|理由|因果).{0,24}(?:可能性|候補|仮説|推定|考え)|(?:可能性|候補|仮説|推定).{0,24}(?:原因|要因|理由|因果)|root cause.{0,24}(?:may|might|likely))`,
	)
	kindFutureEventPattern = regexp.MustCompile(
		`(?i)(?:今後|将来|次回|来週|来月|放置すると|すると|すと|しないと|ないと|なければ|の場合|のままだと|再発|期限切れにな|できなくな|停止し得|失われ得|will|future|if .+ then)`,
	)
	kindOngoingEventPattern = regexp.MustCompile(
		`(?i)(?:継続的|断続的|引き続き|依然として|常時|繰り返し|ongoing|continuing|recurring)`,
	)
	kindNegativeImpactPattern = regexp.MustCompile(
		`(?i)(?:障害|停止|切断|切れ|接続(?:が)?できな|利用(?:が)?できな|過多|多くなりすぎ|見落と|失敗|損失|漏えい|遅延|再発|不能|悪化|欠落|期限切れ|危険|adverse|outage|failure|loss|unavailable)`,
	)
	kindCurrentProblemPattern = regexp.MustCompile(
		`(?i)(?:現在|現時点|発生中|発生している|継続している|できていな(?:い|く|かった)|できていません|接続できない|未解決|未確認|未確定|未決定|決まっていな(?:い|かった)|決まっていません|特定できていな(?:い|かった)|特定できていません|unknown|unresolved|currently)`,
	)
	kindConfirmedPattern = regexp.MustCompile(
		`(?i)(?:確認した|確認しました|確認済み|判明した|判明しました|観測した|報告された|報告されました|報告がありました|報告があった|漏れてい(?:た|ました)|異常はなかった|正常になった|復旧した|切り戻した|修正した後|であることが分かった|confirmed|observed|verified|reported)`,
	)
	kindPastEventPattern = regexp.MustCompile(
		`(?i)(?:発生した|発生しました|していた|だった|でした|行った|実施した|完了した|occurred|was |were |completed)`,
	)
	kindOpenQuestionPattern = regexp.MustCompile(
		`(?i)(?:[?？]|(?:何|いつ|どこ|誰|どれ|どの|どちら|どう).{0,24}か|を行うか|か未確認|説明できるか|何を|どのように|決まっていな(?:い|かった)|決まっていません|特定できていな(?:い|かった)|特定できていません|確認が必要|調査が必要|検討が必要|未解決|未確定|未決定|open question|needs investigation)`,
	)
	kindActionVerbPattern = regexp.MustCompile(
		`(?i)(?:追加|作成|更新|修正|調査|確認|実施|対応|検討|決定|決め|設定|適用|依頼|連絡|共有|提出|準備|送付|レビュー|監視|継続|切り戻|assign|update|create|check|review|investigate|implement)`,
	)
	kindActionIntentPattern = regexp.MustCompile(
		`(?i)(?:(?:追加|作成|更新|修正|調査|確認|実施|対応|検討|決定|決め|設定|適用|依頼|連絡|共有|提出|準備|送付|レビュー|監視|継続)(?:する|します|してもら|を行う|予定|ことにする)|(?:will|shall|must|to )(?:update|create|check|review|investigate|implement))`,
	)
	kindCommitmentPattern = regexp.MustCompile(
		`(?i)(?:(?:追加|作成|更新|修正|調査|確認|実施|対応|設定|適用|依頼|連絡|共有|提出|準備|送付|レビュー|監視)します|行います|確認してもら|ことにします|完了条件|合意|決定した|担当する|依頼する|予定です|will|shall|committed)`,
	)
	kindProposalPattern = regexp.MustCompile(
		`(?i)(?:案|候補|提案|してはどう|検討したい|検討中|選択肢|proposal|option|considering)`,
	)
	kindOwnerPattern = regexp.MustCompile(
		`(?i)(?:[一-龠々ぁ-んァ-ヶーA-Za-z]{1,24}(?:さん|氏)(?:が|は|に)|(?:私|わたし|自分)(?:が|は)|担当者|責任者|owner|assignee)`,
	)
	kindDeadlinePattern = regexp.MustCompile(
		`(?i)(?:までに|今週|来週|本日|明日|月末|週末|次回(?:会議)?まで|[月火水木金土日]曜(?:日)?|期限|due|deadline|by next)`,
	)
	kindInvestigationPattern = regexp.MustCompile(
		`(?i)(?:原因|要因|因果|調査|検証|確認が必要|特定|説明できるか|切り分け|investigat|verify|root cause)`,
	)
	kindMitigationPattern = regexp.MustCompile(
		`(?i)(?:防止|予防|対策|回避|抑制|監視|更新|チェックリスト|見直し|再発防止|mitigat|prevent|remediat)`,
	)
	kindSentenceBoundaryPattern = regexp.MustCompile(`[。！？\r\n]+`)
)

func itemKindValidationThreshold(mode itemKindValidationMode) float64 {
	switch mode {
	case itemKindValidationFinal:
		return 0.88
	case itemKindValidationLegacy, itemKindValidationAudit:
		return 0.92
	default:
		return 0.90
	}
}

func itemSemanticEvidenceText(item liveAnalysisItem, scope liveEvidenceScope) string {
	var values []string
	seen := make(map[int64]struct{}, len(item.EvidenceSequenceNos))
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if _, duplicate := seen[sequenceNo]; duplicate {
			continue
		}
		seen[sequenceNo] = struct{}{}
		if text := strings.TrimSpace(scope.TranscriptText[sequenceNo]); text != "" {
			values = append(values, text)
		}
	}
	return strings.Join(values, "。")
}

func latestItemSemanticEvidence(item liveAnalysisItem, scope liveEvidenceScope) (int64, string) {
	var latestSequenceNo int64
	latestText := ""
	for _, sequenceNo := range item.EvidenceSequenceNos {
		role := semanticEvidenceRole(item.EvidenceRoles, sequenceNo)
		if role == "" {
			role = scope.EvidenceRoles[sequenceNo]
		}
		if role == liveEvidenceReferenceRecap ||
			role == liveEvidenceDiscourseOnly {
			continue
		}
		if sequenceNo < latestSequenceNo {
			continue
		}
		if text := strings.TrimSpace(scope.TranscriptText[sequenceNo]); text != "" {
			latestSequenceNo = sequenceNo
			latestText = text
		}
	}
	return latestSequenceNo, latestText
}

func semanticEvidenceRole(roles []liveEvidenceRoleRef, sequenceNo int64) liveEvidenceRole {
	for _, role := range roles {
		if role.SequenceNo == sequenceNo {
			return role.Role
		}
	}
	return ""
}

func inferItemSemanticFeatures(item liveAnalysisItem, scope liveEvidenceScope) itemSemanticFeatures {
	proposition := strings.TrimSpace(item.Title + "。" + item.Body)
	evidence := itemSemanticEvidenceText(item, scope)
	text := proposition
	if utf8.RuneCountInString(strings.TrimSpace(proposition)) < 12 {
		text = strings.TrimSpace(proposition + "。" + evidence)
	}

	features := itemSemanticFeatures{
		NegativeImpactPresent:      kindNegativeImpactPattern.MatchString(text),
		UncertaintyPresent:         kindUncertaintyPattern.MatchString(text),
		FutureEventPresent:         kindFutureEventPattern.MatchString(text),
		CurrentProblemPresent:      kindCurrentProblemPattern.MatchString(text),
		ConfirmedEvidencePresent:   kindConfirmedPattern.MatchString(text),
		ActionVerbPresent:          kindActionVerbPattern.MatchString(text),
		OwnerPresent:               kindOwnerPattern.MatchString(text),
		DeadlinePresent:            kindDeadlinePattern.MatchString(text),
		DecisionOrCommitment:       kindCommitmentPattern.MatchString(text),
		InvestigationIntentPresent: kindInvestigationPattern.MatchString(text),
		MitigationIntentPresent:    kindMitigationPattern.MatchString(text),
		CausalHypothesisPresent:    kindCausalHypothesisPattern.MatchString(text),
		ProposalPresent:            kindProposalPattern.MatchString(text),
	}
	if features.CausalHypothesisPresent {
		// A hypothesis about a past/current cause is not a future adverse event,
		// even though both propositions may contain the word "可能性".
		features.FutureEventPresent = false
	}
	laterSequenceNo, latestEvidence := latestItemSemanticEvidence(item, scope)
	if latestEvidence != "" &&
		semanticItemSimilarity(proposition, latestEvidence) >= 0.12 &&
		kindConfirmedPattern.MatchString(latestEvidence) &&
		!kindUncertaintyPattern.MatchString(latestEvidence) &&
		!kindOpenQuestionPattern.MatchString(latestEvidence) {
		features.ConfirmedEvidencePresent = true
		if item.CreatedThroughSequenceNo > 0 &&
			item.InitialEvidenceMaxSequenceNo > 0 &&
			laterSequenceNo > item.CreatedThroughSequenceNo &&
			laterSequenceNo > item.InitialEvidenceMaxSequenceNo {
			features.ConfirmationSupersedesOpen = true
		}
	}

	switch {
	case features.ConfirmationSupersedesOpen:
		features.TemporalScope = "past"
	case kindOngoingEventPattern.MatchString(text):
		features.TemporalScope = "ongoing"
	case features.FutureEventPresent:
		features.TemporalScope = "future"
	case features.CurrentProblemPresent:
		features.TemporalScope = "current"
	case kindPastEventPattern.MatchString(text) || features.ConfirmedEvidencePresent:
		features.TemporalScope = "past"
	default:
		features.TemporalScope = "unknown"
	}
	switch {
	case features.ConfirmationSupersedesOpen:
		features.EpistemicStatus = "confirmed"
	case features.DecisionOrCommitment:
		features.EpistemicStatus = "committed"
	case features.CausalHypothesisPresent:
		features.EpistemicStatus = "hypothesis"
	case features.CurrentProblemPresent || kindOpenQuestionPattern.MatchString(text):
		features.EpistemicStatus = "unresolved"
	case features.ProposalPresent:
		features.EpistemicStatus = "proposed"
	case features.UncertaintyPresent:
		features.EpistemicStatus = "uncertain"
	case features.ConfirmedEvidencePresent:
		features.EpistemicStatus = "confirmed"
	default:
		features.EpistemicStatus = "reported"
	}
	openQuestion := kindOpenQuestionPattern.MatchString(text)
	actionIntent := kindActionIntentPattern.MatchString(text) ||
		(features.ActionVerbPresent && !openQuestion &&
			(features.OwnerPresent || features.DeadlinePresent || features.DecisionOrCommitment))
	switch {
	case features.ConfirmationSupersedesOpen:
		features.SemanticRole = "state"
	case actionIntent && features.ProposalPresent:
		features.SemanticRole = "proposal"
	case actionIntent:
		features.SemanticRole = "action"
	case features.CausalHypothesisPresent:
		features.SemanticRole = "causal_hypothesis"
	case features.CurrentProblemPresent || kindOpenQuestionPattern.MatchString(text):
		features.SemanticRole = "open_question"
	case (features.FutureEventPresent || features.TemporalScope == "ongoing") &&
		features.UncertaintyPresent && features.NegativeImpactPresent:
		features.SemanticRole = "adverse_outcome"
	default:
		features.SemanticRole = "state"
	}
	return features
}

func evaluateLiveItemKind(item liveAnalysisItem, scope liveEvidenceScope, stage string) itemKindValidationDecision {
	originalKind := strings.ToLower(strings.TrimSpace(item.Kind))
	originalSubtype := strings.ToLower(strings.TrimSpace(item.Subtype))
	features := inferItemSemanticFeatures(item, scope)
	decision := itemKindValidationDecision{
		Stage: stage, SequenceNos: append([]int64(nil), item.EvidenceSequenceNos...),
		ItemID: item.ID, ModelItemID: firstNonEmptyTrimmed(item.modelReference, modelItemReference(item)),
		OriginalKind: originalKind, CanonicalKind: originalKind,
		OriginalSubtype: originalSubtype, CanonicalSubtype: originalSubtype,
		Features: features, Decision: "accepted", Reason: "semantic_kind_matches", Confidence: 0.75,
	}
	if originalKind == "decision" {
		decision.Confidence = 1
		decision.Reason = "decision_kind_outside_common_validator"
		return decision
	}

	text := strings.TrimSpace(item.Title + " " + item.Body)
	openQuestion := kindOpenQuestionPattern.MatchString(text)
	actionIntent := kindActionIntentPattern.MatchString(text) ||
		(features.ActionVerbPresent && !openQuestion &&
			(features.OwnerPresent || features.DeadlinePresent || features.DecisionOrCommitment))
	strongAction := actionIntent &&
		((!features.ProposalPresent &&
			(features.OwnerPresent || features.DeadlinePresent || features.DecisionOrCommitment)) ||
			features.DecisionOrCommitment || (features.OwnerPresent && features.DeadlinePresent))
	preserveExplicitQuestion := originalKind == "issue" && originalSubtype == issueSubtypeQuestion && openQuestion

	switch {
	case strongAction && !preserveExplicitQuestion:
		decision.CanonicalKind = "todo"
		decision.CanonicalSubtype = ""
		decision.Reason = "committed_action"
		decision.Confidence = 0.94
		if features.OwnerPresent && features.DeadlinePresent {
			decision.Reason = "assigned_action_with_owner_and_deadline"
			decision.Confidence = 0.98
		}
	case features.ConfirmedEvidencePresent &&
		(!features.UncertaintyPresent || features.ConfirmationSupersedesOpen) &&
		(!openQuestion || features.ConfirmationSupersedesOpen) && !strongAction:
		decision.CanonicalKind = "fact"
		decision.CanonicalSubtype = ""
		decision.Reason = "confirmed_observed_or_reported_state"
		decision.Confidence = 0.95
		if features.ConfirmationSupersedesOpen {
			decision.Reason = "later_confirmed_evidence_supersedes_open_state"
			decision.Confidence = 0.97
		}
	case features.CausalHypothesisPresent:
		decision.CanonicalKind = "issue"
		decision.CanonicalSubtype = issueSubtypeInvestigation
		decision.Reason = "causal_hypothesis_requires_verification"
		decision.Confidence = 0.97
	case originalKind == "todo" && actionIntent && (features.CurrentProblemPresent || openQuestion):
		decision.Decision = "tentative"
		decision.Reason = "composite_issue_action_requires_split_or_more_evidence"
		decision.Confidence = 0.70
	case features.CurrentProblemPresent || openQuestion:
		decision.CanonicalKind = "issue"
		if originalKind != "issue" || !validIssueSubtype(originalSubtype) {
			decision.CanonicalSubtype = inferIssueSubtype(text, originalSubtype)
			if features.InvestigationIntentPresent || features.CausalHypothesisPresent {
				decision.CanonicalSubtype = issueSubtypeInvestigation
			}
		}
		decision.Reason = "current_unresolved_or_open_question"
		decision.Confidence = 0.94
	case (features.FutureEventPresent || features.TemporalScope == "ongoing") &&
		features.UncertaintyPresent && features.NegativeImpactPresent:
		decision.CanonicalKind = "risk"
		decision.CanonicalSubtype = ""
		decision.Reason = "future_uncertain_adverse_outcome"
		decision.Confidence = 0.97
	case actionIntent && features.ProposalPresent:
		if originalKind == "todo" {
			decision.Decision = "tentative"
			decision.Reason = "uncommitted_action_proposal"
			decision.Confidence = 0.86
			break
		}
		decision.CanonicalKind = "issue"
		if originalKind != "issue" || !validIssueSubtype(originalSubtype) {
			decision.CanonicalSubtype = issueSubtypeDiscussion
		}
		decision.Reason = "uncommitted_action_proposal"
		decision.Confidence = 0.94
	case actionIntent:
		decision.CanonicalKind = "todo"
		decision.CanonicalSubtype = ""
		decision.Reason = "explicit_next_action"
		decision.Confidence = 0.91
	default:
		decision.Decision = "tentative"
		decision.Reason = "semantic_kind_ambiguous"
		decision.Confidence = 0.45
	}

	if decision.CanonicalKind != "issue" {
		decision.CanonicalSubtype = ""
	} else if !validIssueSubtype(decision.CanonicalSubtype) {
		decision.CanonicalSubtype = issueSubtypeDiscussion
	}
	if decision.CanonicalKind != originalKind ||
		(decision.CanonicalKind == "issue" && decision.CanonicalSubtype != originalSubtype) {
		decision.Decision = "rewrite_candidate"
	}
	return decision
}

func validateLiveItemKinds(items []liveAnalysisItem, scope liveEvidenceScope, mode itemKindValidationMode, stage string, stats *liveAnalysisTreeMergeStats) []liveAnalysisItem {
	threshold := itemKindValidationThreshold(mode)
	validated := append([]liveAnalysisItem(nil), items...)
	for index := range validated {
		decision := evaluateLiveItemKind(validated[index], scope, stage)
		if decision.Decision == "rewrite_candidate" && decision.Confidence >= threshold {
			validated[index].Kind = decision.CanonicalKind
			validated[index].Subtype = decision.CanonicalSubtype
			repairNonResolvableStatus(&validated[index])
			decision.Decision = "rewritten"
			if stats != nil {
				stats.KindValidationChanges++
				stats.SemanticKindMigrations++
			}
		} else if decision.Decision == "rewrite_candidate" {
			decision.Decision = "tentative"
			if stats != nil {
				stats.KindValidationAmbiguous++
			}
		} else if decision.Decision == "tentative" && stats != nil {
			stats.KindValidationAmbiguous++
		}
		if stats != nil {
			stats.KindValidationDecisions = append(stats.KindValidationDecisions, decision)
		}
	}
	return validated
}

func splitAndValidateLiveItemKinds(previous, items []liveAnalysisItem, assignments []treeAssignment, scope liveEvidenceScope, mode itemKindValidationMode, stage string, stats *liveAnalysisTreeMergeStats) ([]liveAnalysisItem, []treeAssignment) {
	expanded, expandedAssignments := splitLiveItemKinds(previous, items, assignments, scope, stats)
	return validateLiveItemKinds(expanded, scope, mode, stage, stats), expandedAssignments
}

func splitLiveItemKinds(previous, items []liveAnalysisItem, assignments []treeAssignment, scope liveEvidenceScope, stats *liveAnalysisTreeMergeStats) ([]liveAnalysisItem, []treeAssignment) {
	expanded := make([]liveAnalysisItem, 0, len(items)+2)
	expandedAssignments := append([]treeAssignment(nil), assignments...)
	allKnown := append(append([]liveAnalysisItem(nil), previous...), items...)

	for _, item := range items {
		fragments := strongKindFragments(item, scope)
		if len(fragments) < 2 {
			expanded = append(expanded, item)
			continue
		}
		distinctKinds := make(map[string]struct{})
		for _, fragment := range fragments {
			distinctKinds[fragment.Kind] = struct{}{}
		}
		if len(distinctKinds) < 2 {
			expanded = append(expanded, item)
			continue
		}

		primary := 0
		for index := range fragments {
			if fragments[index].Kind == item.Kind {
				primary = index
				break
			}
		}
		sourceRef := modelItemReference(item)
		created := make([]liveAnalysisItem, 0, len(fragments))
		fragmentKinds := make([]string, 0, len(fragments))
		rejected := 0
		for index, fragment := range fragments {
			fragmentKinds = append(fragmentKinds, fragment.Kind)
			if index != primary && representedKindFragment(allKnown, fragment, sourceRef) {
				rejected++
				continue
			}
			candidate := item
			candidate.Kind = fragment.Kind
			candidate.Subtype = fragment.Subtype
			candidate.Title = truncateRunes(fragment.Text, 40)
			candidate.Body = truncateRunes(fragment.Text, liveAnalysisTreeDescriptionMaxRunes)
			candidate.EvidenceSequenceNos = []int64{fragment.SequenceNo}
			candidate.EvidenceSnippets = groundingSnippetsForFragment(item.EvidenceSnippets, fragment.Text, fragment.SequenceNo, scope)
			candidate.EvidenceRoles = semanticFragmentEvidenceRoles(item.EvidenceRoles, fragment.SequenceNo)
			candidate.evidenceSpecified = true
			candidate.modelReference = sourceRef
			candidate.semanticSplitFragment = true
			if index != primary {
				ref := fmt.Sprintf("%s-kind-split-%d", sourceRef, index+1)
				if strings.TrimSpace(item.ClientKey) != "" {
					candidate.ClientKey, candidate.ID = ref, ""
				} else {
					candidate.ClientKey = ""
					candidate.ID = serverGeneratedItemID(candidate)
					ref = candidate.ID
				}
				expandedAssignments = cloneKindSplitAssignment(expandedAssignments, sourceRef, item.ID, ref)
			}
			created = append(created, candidate)
			allKnown = append(allKnown, candidate)
		}
		if len(created) == 0 {
			expanded = append(expanded, item)
			continue
		}
		expanded = append(expanded, created...)
		if stats != nil {
			relationsCreated := expectedSemanticKindRelations(created)
			stats.KindSemanticSplits++
			stats.KindSplitFragments += len(created)
			stats.KindSplitRejected += rejected
			stats.KindSplitDecisions = append(stats.KindSplitDecisions, itemKindSplitDecision{
				SourceItemID: sourceRef, FragmentCount: len(created), FragmentKinds: fragmentKinds,
				RejectedFragments: rejected, RelationsCreated: relationsCreated,
			})
		}
	}
	return expanded, expandedAssignments
}

func expectedSemanticKindRelations(items []liveAnalysisItem) int {
	count := 0
	for left := 0; left < len(items); left++ {
		for right := left + 1; right < len(items); right++ {
			if _, _, kind := semanticKindRelation(items[left], items[right]); kind != "" {
				count++
			}
		}
	}
	return count
}

type strongKindFragment struct {
	Text       string
	Kind       string
	Subtype    string
	SequenceNo int64
	Confidence float64
}

func strongKindFragments(item liveAnalysisItem, scope liveEvidenceScope) []strongKindFragment {
	var fragments []strongKindFragment
	body := strings.TrimSpace(item.Body)
	sentences := kindSentenceBoundaryPattern.Split(body, -1)
	if len(sentences) < 2 {
		return nil
	}
	for index, sentence := range sentences {
		sentence = strings.Trim(strings.TrimSpace(sentence), "、, ")
		if utf8.RuneCountInString(sentence) < 6 {
			continue
		}
		sequenceNo := semanticFragmentSequenceNo(sentence, index, item.EvidenceSequenceNos, scope)
		if sequenceNo <= 0 {
			continue
		}
		probe := item
		probe.Title, probe.Body = sentence, sentence
		probe.EvidenceSequenceNos = []int64{sequenceNo}
		if finalItemIsLowInformation(probe) || liveItemTextNeedsReferent(probe) {
			continue
		}
		decision := evaluateLiveItemKind(probe, liveEvidenceScope{}, "semantic_split")
		if decision.Confidence < 0.90 || decision.Decision == "tentative" ||
			decision.CanonicalKind == "decision" {
			continue
		}
		if decision.CanonicalKind == "todo" &&
			!decision.Features.OwnerPresent && !decision.Features.DeadlinePresent &&
			utf8.RuneCountInString(semanticItemKey(sentence)) < 8 {
			continue
		}
		fragments = append(fragments, strongKindFragment{
			Text: sentence, Kind: decision.CanonicalKind, Subtype: decision.CanonicalSubtype,
			SequenceNo: sequenceNo, Confidence: decision.Confidence,
		})
	}
	return fragments
}

func semanticFragmentSequenceNo(fragment string, index int, evidenceSequenceNos []int64, scope liveEvidenceScope) int64 {
	if sequenceNo := semanticFragmentGroundingSequenceNo(fragment, evidenceSequenceNos, scope); sequenceNo > 0 {
		return sequenceNo
	}
	// Compatibility for structural-only in-package fixtures. Production
	// scopes always contain the cited final transcript text and therefore
	// never use positional evidence inheritance.
	if len(scope.TranscriptText) > 0 {
		return 0
	}
	if len(evidenceSequenceNos) == 0 {
		return 0
	}
	if index >= len(evidenceSequenceNos) {
		index = len(evidenceSequenceNos) - 1
	}
	return evidenceSequenceNos[index]
}

func semanticFragmentEvidenceRoles(roles []liveEvidenceRoleRef, sequenceNo int64) []liveEvidenceRoleRef {
	for _, role := range roles {
		if role.SequenceNo == sequenceNo {
			return []liveEvidenceRoleRef{role}
		}
	}
	return nil
}

func representedKindFragment(items []liveAnalysisItem, fragment strongKindFragment, sourceRef string) bool {
	for _, existing := range items {
		if sourceRef != "" && modelItemReference(existing) == sourceRef {
			continue
		}
		if existing.Inactive || existing.MergedIntoID != "" || existing.Kind != fragment.Kind ||
			!containsInt64(existing.EvidenceSequenceNos, fragment.SequenceNo) {
			continue
		}
		if semanticItemSimilarity(existing.Title+" "+existing.Body, fragment.Text) >= 0.28 {
			return true
		}
	}
	return false
}

func cloneKindSplitAssignment(assignments []treeAssignment, sourceRef, sourceID, newRef string) []treeAssignment {
	for _, assignment := range assignments {
		requested := assignment.nodeID()
		if requested != sourceRef && requested != sourceID {
			continue
		}
		clone := assignment
		clone.NodeID, clone.ItemID, clone.ModelNodeID = newRef, "", newRef
		clone.ServerSource = "semantic_kind_split"
		return append(assignments, clone)
	}
	return assignments
}

func repairPersistedItemKinds(state *liveAnalysisPayload, scope liveEvidenceScope, mode itemKindValidationMode, stage string, stats *liveAnalysisTreeMergeStats) {
	if state == nil {
		return
	}
	state.Items = validateLiveItemKinds(state.Items, scope, mode, stage, stats)
	if state.Tree == nil {
		return
	}
	byID := make(map[string]liveAnalysisItem, len(state.Items))
	for _, item := range state.Items {
		byID[item.ID] = item
	}
	for index := range state.Tree.Nodes {
		item, ok := byID[state.Tree.Nodes[index].ID]
		if !ok {
			continue
		}
		state.Tree.Nodes[index].Kind = item.Kind
		state.Tree.Nodes[index].Subtype = item.Subtype
		state.Tree.Nodes[index].Status = item.Status
	}
}

func splitPersistedItemKinds(state *liveAnalysisPayload, scope liveEvidenceScope, mode itemKindValidationMode, stage string, stats *liveAnalysisTreeMergeStats) {
	if state == nil || state.Tree == nil || len(state.Items) == 0 {
		return
	}
	nodeByID := make(map[string]liveAnalysisTreeNode, len(state.Tree.Nodes))
	usedIDs := make(map[string]struct{}, len(state.Items))
	for _, node := range state.Tree.Nodes {
		nodeByID[node.ID] = node
	}
	for _, item := range state.Items {
		usedIDs[item.ID] = struct{}{}
	}
	allKnown := append([]liveAnalysisItem(nil), state.Items...)
	expanded := make([]liveAnalysisItem, 0, len(state.Items)+2)
	var addedNodes []liveAnalysisTreeNode

	for _, item := range state.Items {
		if item.Inactive || item.MergedIntoID != "" {
			expanded = append(expanded, item)
			continue
		}
		fragments := strongKindFragments(item, scope)
		distinctKinds := make(map[string]struct{}, len(fragments))
		for _, fragment := range fragments {
			distinctKinds[fragment.Kind] = struct{}{}
		}
		if len(fragments) < 2 || len(distinctKinds) < 2 {
			expanded = append(expanded, item)
			continue
		}

		primary := 0
		for index := range fragments {
			if fragments[index].Kind == item.Kind {
				primary = index
				break
			}
		}
		sourceRef := modelItemReference(item)
		sourceNode, hasNode := nodeByID[item.ID]
		created := make([]liveAnalysisItem, 0, len(fragments))
		fragmentKinds := make([]string, 0, len(fragments))
		rejected := 0
		for index, fragment := range fragments {
			fragmentKinds = append(fragmentKinds, fragment.Kind)
			if index != primary && representedKindFragment(allKnown, fragment, sourceRef) {
				rejected++
				continue
			}
			candidate := item
			candidate.Kind = fragment.Kind
			candidate.Subtype = fragment.Subtype
			candidate.Title = truncateRunes(fragment.Text, 40)
			candidate.Body = truncateRunes(fragment.Text, liveAnalysisTreeDescriptionMaxRunes)
			candidate.EvidenceSequenceNos = []int64{fragment.SequenceNo}
			candidate.EvidenceSnippets = groundingSnippetsForFragment(item.EvidenceSnippets, fragment.Text, fragment.SequenceNo, scope)
			candidate.EvidenceRoles = semanticFragmentEvidenceRoles(item.EvidenceRoles, fragment.SequenceNo)
			candidate.evidenceSpecified = true
			candidate.modelReference = sourceRef
			candidate.semanticSplitFragment = true
			if index != primary {
				candidate.ID = serverGeneratedItemID(candidate)
				candidate.ClientKey = ""
				if _, collision := usedIDs[candidate.ID]; collision {
					rejected++
					continue
				}
				usedIDs[candidate.ID] = struct{}{}
			}
			created = append(created, candidate)
			allKnown = append(allKnown, candidate)

			if hasNode {
				node := sourceNode
				node.ID = candidate.ID
				node.Kind = candidate.Kind
				node.Subtype = candidate.Subtype
				node.Label = candidate.Title
				node.Description = candidate.Body
				node.Status = candidate.Status
				if index == primary {
					for nodeIndex := range state.Tree.Nodes {
						if state.Tree.Nodes[nodeIndex].ID == item.ID {
							state.Tree.Nodes[nodeIndex] = node
							break
						}
					}
				} else {
					addedNodes = append(addedNodes, node)
				}
			}
		}
		if len(created) == 0 {
			expanded = append(expanded, item)
			continue
		}
		expanded = append(expanded, created...)
		if stats != nil {
			stats.KindSemanticSplits++
			stats.KindSplitFragments += len(created)
			stats.KindSplitRejected += rejected
			stats.KindSplitDecisions = append(stats.KindSplitDecisions, itemKindSplitDecision{
				SourceItemID: sourceRef, FragmentCount: len(created), FragmentKinds: fragmentKinds,
				RejectedFragments: rejected, RelationsCreated: expectedSemanticKindRelations(created),
			})
		}
	}
	state.Items = validateLiveItemKinds(expanded, scope, mode, stage, stats)
	state.Tree.Nodes = append(state.Tree.Nodes, addedNodes...)
	rebuildTreeAuditEdges(state.Tree)
}

func appendSemanticKindRelations(tree *liveAnalysisTree, items []liveAnalysisItem) int {
	if tree == nil {
		return 0
	}
	active := make([]liveAnalysisItem, 0, len(items))
	for _, item := range items {
		if !item.Inactive && item.MergedIntoID == "" {
			active = append(active, item)
		}
	}
	existing := make(map[string]struct{}, len(tree.Relations))
	for _, relation := range tree.Relations {
		existing[relation.Source+"\x00"+relation.Target] = struct{}{}
	}
	created := 0
	for left := 0; left < len(active); left++ {
		for right := left + 1; right < len(active); right++ {
			source, target, kind := semanticKindRelation(active[left], active[right])
			if kind == "" {
				continue
			}
			leftText := active[left].Title + " " + active[left].Body
			rightText := active[right].Title + " " + active[right].Body
			related := itemEvidenceOverlaps(active[left], active[right]) ||
				(itemEvidenceWithin(active[left], active[right], 2) &&
					sharedTreeAuditSubjectTerm(leftText, rightText) &&
					semanticItemSimilarity(leftText, rightText) >= 0.20)
			if !related {
				continue
			}
			key := source + "\x00" + target
			if _, duplicate := existing[key]; duplicate {
				continue
			}
			tree.Relations = append(tree.Relations, liveAnalysisTreeRelation{Source: source, Target: target, Kind: kind})
			existing[key] = struct{}{}
			created++
		}
	}
	sort.SliceStable(tree.Relations, func(i, j int) bool {
		if tree.Relations[i].Source != tree.Relations[j].Source {
			return tree.Relations[i].Source < tree.Relations[j].Source
		}
		return tree.Relations[i].Target < tree.Relations[j].Target
	})
	return created
}

func semanticKindRelation(left, right liveAnalysisItem) (source, target, kind string) {
	switch {
	case left.Kind == "todo" && right.Kind == "risk":
		return left.ID, right.ID, "mitigates"
	case right.Kind == "todo" && left.Kind == "risk":
		return right.ID, left.ID, "mitigates"
	case left.Kind == "todo" && right.Kind == "issue":
		return left.ID, right.ID, "addresses"
	case right.Kind == "todo" && left.Kind == "issue":
		return right.ID, left.ID, "addresses"
	case left.Kind == "fact" && right.Kind == "issue":
		return left.ID, right.ID, "supports"
	case right.Kind == "fact" && left.Kind == "issue":
		return right.ID, left.ID, "supports"
	default:
		return "", "", ""
	}
}

func recordItemKindDistribution(state *liveAnalysisPayload, scope liveEvidenceScope, stats *liveAnalysisTreeMergeStats) {
	if state == nil || stats == nil {
		return
	}
	stats.KindValidationAmbiguous = 0
	stats.ConfirmedEvidenceCandidates = 0
	stats.AssignedActionRiskCandidates = 0
	stats.CausalHypothesisRiskCandidates = 0
	stats.KindDistributionWarnings = nil
	factCount, riskCount := 0, 0
	for _, item := range state.Items {
		if item.Inactive || item.MergedIntoID != "" {
			continue
		}
		if item.Kind == "fact" {
			factCount++
		}
		if item.Kind == "risk" {
			riskCount++
		}
		decision := evaluateLiveItemKind(item, scope, "kind_distribution")
		if decision.Decision == "tentative" ||
			(decision.Decision == "rewrite_candidate" &&
				decision.Confidence < itemKindValidationThreshold(itemKindValidationLive)) {
			stats.KindValidationAmbiguous++
		}
		features := inferItemSemanticFeatures(item, scope)
		if features.ConfirmedEvidencePresent {
			stats.ConfirmedEvidenceCandidates++
		}
		if item.Kind == "risk" && features.ActionVerbPresent &&
			(features.OwnerPresent || features.DeadlinePresent || features.DecisionOrCommitment) {
			stats.AssignedActionRiskCandidates++
		}
		if item.Kind == "risk" && features.CausalHypothesisPresent {
			stats.CausalHypothesisRiskCandidates++
		}
	}
	if factCount == 0 && stats.ConfirmedEvidenceCandidates > 0 {
		stats.KindDistributionWarnings = append(stats.KindDistributionWarnings, "confirmed_evidence_without_fact")
	}
	if riskCount > 0 && stats.AssignedActionRiskCandidates+stats.CausalHypothesisRiskCandidates > 0 {
		stats.KindDistributionWarnings = append(stats.KindDistributionWarnings, "risk_distribution_contains_non_risk_roles")
	}
}
