package application

import (
	"crypto/sha256"
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
	ScheduledEventPresent      bool
	CurrentProblemPresent      bool
	ConfirmedEvidencePresent   bool
	ActionVerbPresent          bool
	CompletedActionPresent     bool
	OwnerPresent               bool
	DeadlinePresent            bool
	EventDatePresent           bool
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
	kindUncertaintyPattern      = regexp.MustCompile(`(?i)(?:可能性|おそれ|恐れ|懸念|リスク|かもしれ|なりかね|risk|may|might|could)`)
	kindCausalHypothesisPattern = regexp.MustCompile(
		`(?i)(?:(?:原因|要因|理由|因果).{0,24}(?:可能性|候補|仮説|推定|考え)|(?:可能性|候補|仮説|推定).{0,24}(?:原因|要因|理由|因果)|root cause.{0,24}(?:may|might|likely))`,
	)
	kindFutureEventPattern = regexp.MustCompile(
		`(?i)(?:今後|将来|次回|来週|来月|放置すると|すると|すと|しないと|ないと|なければ|の場合|のままだと|再発|期限切れにな|できなくな|停止し得|失われ得|will|future|if .+ then)`,
	)
	kindOngoingEventPattern = regexp.MustCompile(
		`(?i)(?:継続的|断続的|引き続き|依然として|常時|繰り返し|ongoing|continuing|recurring)`,
	)
	kindPastObservationPattern = regexp.MustCompile(
		`(?i)(?:(?:混在していました|混在していた|発生していました|発生していた|遅延していました|遅延していた|停止していました|停止していた|影響が出ていました|影響が出ていた|接続できませんでした|接続できなかった|利用できませんでした|利用できなかった)|(?:障害発生時|当時|昨日|先週|午前|午後).{0,80}(?:でした|ました|していた|していました|できなかった|できませんでした|解消した|解消しました|正常になった|正常になりました))`,
	)
	kindExplicitCurrentIssuePattern = regexp.MustCompile(
		`(?i)(?:現在(?:も|は)|現時点(?:でも|では)|今も|引き続き|依然として|発生中|継続中|まだ.{0,24}(?:解決していな|分かっていな|わかっていな|特定できていな|接続できな)|原因.{0,16}(?:不明|分かっていな|わかっていな|特定できていな)|(?:調査|対応|判断|確認)(?:する)?(?:こと)?が必要|未解決|unresolved|currently)`,
	)
	kindNegativeImpactPattern = regexp.MustCompile(
		`(?i)(?:障害|停止|切断|切れ|接続(?:が)?できな|利用(?:が)?できな|過多|多くなりすぎ|見落と|失敗|損失|漏えい|遅延|再発|不能|悪化|欠落|期限切れ|危険|adverse|outage|failure|loss|unavailable)`,
	)
	kindCurrentProblemPattern = regexp.MustCompile(
		`(?i)(?:現在|現時点|発生中|発生している|継続している|できていな(?:い|く|かった)|できていません|接続できない|未解決|未確認|未確定|未決定|決まっていな(?:い|かった)|決まっていません|特定できていな(?:い|かった)|特定できていません|unknown|unresolved|currently)`,
	)
	kindConfirmedPattern = regexp.MustCompile(
		`(?i)(?:確認した|確認しました|確認済み|判明した|判明しました|分かりました|わかりました|明らかになった|観測した|報告された|報告されました|報告がありました|報告があった|漏れてい(?:た|ました)|異常はなかった|正常になった|復旧した|解消した|解消しました|切り戻した|修正した後|であることが分かった|confirmed|observed|verified|reported)`,
	)
	kindPastEventPattern = regexp.MustCompile(
		`(?i)(?:発生した|発生しました|していた|していました|しておりました|できなかった|できませんでした|だった|でした|行った|実施した|完了した|解消した|解消しました|正常になった|正常になりました|occurred|was |were |completed)`,
	)
	kindCompletedActionPattern = regexp.MustCompile(
		`(?i)(?:(?:追加|作成|更新|修正|調査|確認|実施|対応|検討|決定|設定|適用|依頼|連絡|共有|提出|準備|送付|レビュー|監視|継続|切り戻|復旧)(?:しました|した|済み|を完了(?:しました|した))|(?:行|おこな)(?:いました|った)|完了(?:しました|した|済み)|completed|was (?:updated|created|checked|reviewed|implemented))`,
	)
	kindOpenQuestionPattern = regexp.MustCompile(
		`(?i)(?:[?？]|(?:何|いつ|どこ|誰|どれ|どの|どちら|どう).{0,24}か|を行うか|か未確認|説明できるか|何を|どのように|決まっていな(?:い|かった)|決まっていません|特定できていな(?:い|かった)|特定できていません|(?:確認|調査|検討|対応|修正|更新|作業)(?:する)?(?:が)?必要|未解決|未確定|未決定|open question|needs investigation)`,
	)
	kindActionVerbPattern = regexp.MustCompile(
		`(?i)(?:追加|作成|作(?:る|り|っ)|更新|修正|調査|確認|実施|対応|検討|決定|決め|設定|適用|依頼|連絡|共有|提出|準備|送付|レビュー|監視|継続|切り戻|assign|update|create|check|review|investigate|implement)`,
	)
	kindActionIntentPattern = regexp.MustCompile(
		`(?i)(?:(?:追加|作成|更新|修正|調査|確認|実施|対応|検討|決定|決め|設定|適用|依頼|連絡|共有|提出|準備|送付|レビュー|監視|継続)(?:する|します|してください|して下さい|してもら(?:う|います)|を行(?:う|います)|をお願い(?:します|する)|予定(?:です)?|ことに(?:する|します|なりました))|作(?:る|ります)|(?:will|shall|must|to )(?:update|create|check|review|investigate|implement))`,
	)
	kindCommitmentPattern = regexp.MustCompile(
		`(?i)(?:(?:追加|作成|更新|修正|調査|確認|実施|対応|設定|適用|依頼|連絡|共有|提出|準備|送付|レビュー|監視)します|作ります|行います|してください|して下さい|(?:追加|作成|更新|修正|調査|確認|実施|対応|検討|設定|適用|連絡|共有|提出|準備|送付|レビュー|監視)してもら|(?:追加|作成|更新|修正|調査|確認|実施|対応|検討|設定|適用|連絡|共有|提出|準備|送付|レビュー|監視)をお願いします|ことに(?:します|なりました)|完了条件|合意|決定した|担当する|依頼する|予定です|will|shall|committed)`,
	)
	kindProposalPattern = regexp.MustCompile(
		`(?i)(?:案|候補|提案|してはどう|した方が(?:よ|良)|する方が(?:よ|良)|よさそう|良さそう|すべき|検討したい|検討中|選択肢|proposal|option|considering)`,
	)
	kindRecommendationPattern = regexp.MustCompile(
		`(?i)(?:した方が(?:よ|良)|する方が(?:よ|良)|してはどう|よさそう|良さそう|すべき|would be better|should consider)`,
	)
	kindIncompletePurposePattern = regexp.MustCompile(
		`(?i)(?:できる|する|なる)ように[。.!！]?$`,
	)
	kindUnassignedNecessityPattern = regexp.MustCompile(
		`(?i)(?:(?:確認|調査|検討|対応|修正|更新|作業|調整|判断|決定)(?:する)?(?:こと)?(?:が|は)?必要|要検討|要確認|needs? (?:review|investigation|consideration))`,
	)
	kindUnassignedManagementPattern = regexp.MustCompile(
		`(?i)(?:別(?:の|件|枠).{0,20}(?:対応事項|管理)|対応事項として(?:管理|扱)|管理(?:する|します|方針)|(?:計画|方針)を確定|追加する案)`,
	)
	kindOwnerPattern = regexp.MustCompile(
		`(?i)(?:[一-龠々ぁ-んァ-ヶーA-Za-z]{1,24}(?:さん|氏)(?:が|は|に|へ)|(?:私|わたし|自分)(?:が|は)|担当者|責任者|owner|assignee)`,
	)
	kindDeadlineMarkerPattern = regexp.MustCompile(
		`(?i)(?:までに|まで|今週中|来週中|本日中|明日中|月末まで|週末まで|次回(?:会議)?まで|[月火水木金土日]曜(?:日)?まで|due|deadline|by next)`,
	)
	// kindDateMentionPattern is used only by the grounding atom checker. Kind
	// classification must use actionDeadlinePresent so an object's event date
	// cannot become a TODO deadline.
	kindDateMentionPattern = regexp.MustCompile(
		`(?i)(?:までに|今週|来週|本日|明日|月末|週末|次回(?:会議)?まで|[月火水木金土日]曜(?:日)?|期限|due|deadline|by next)`,
	)
	kindRelativeWorkDatePattern = regexp.MustCompile(
		`(?i)(?:今週|来週|本日|明日|今月|来月|月末|週末|[月火水木金土日]曜(?:日)?)`,
	)
	kindEventDatePattern = regexp.MustCompile(
		`(?i)(?:今週|来週|今月|来月|本日|明日|月末|週末|午前|午後|\d{1,2}月\d{1,2}日|\d{1,2}時\d{0,2}分?|[月火水木金土日]曜(?:日)?|\d{4}[-/年]\d{1,2}[-/月]\d{1,2}日?)`,
	)
	kindScheduledEventPattern = regexp.MustCompile(
		`(?i)(?:(?:期限切れ|失効|満了|終了|開始|開催|到来)(?:に)?(?:なる|なります|する|します|予定)|(?:有効期限|契約期限|終了日|開催日)(?:は|が)|will (?:expire|end|start))`,
	)
	kindInvestigationPattern = regexp.MustCompile(
		`(?i)(?:原因|要因|因果|調査|検証|確認が必要|特定|説明できるか|切り分け|investigat|verify|root cause)`,
	)
	kindMitigationPattern = regexp.MustCompile(
		`(?i)(?:防止|予防|対策|回避|抑制|監視|更新|チェックリスト|見直し|再発防止|mitigat|prevent|remediat)`,
	)
	kindSentenceBoundaryPattern = regexp.MustCompile(`[。！？\r\n]+`)
	// ASR often removes punctuation between two commitments. Split only after
	// a completed commitment form and only when the following text begins with
	// an explicit new owner, so ordinary compound predicates stay intact.
	kindClauseCommitmentEndPattern = regexp.MustCompile(
		`(?:してもらいます|をお願いします|いたします|行います|作ります|します)`,
	)
	kindClauseOwnerLeadPattern = regexp.MustCompile(
		`^(?:(?:私|わたし|自分)(?:が|は)|[一-龠々ぁ-んァ-ヶーA-Za-z]{1,24}(?:さん|氏)(?:に(?:は)?|が|は|へ))`,
	)
)

func semanticKindClauses(text string) []string {
	raw := kindSentenceBoundaryPattern.Split(text, -1)
	clauses := make([]string, 0, len(raw))
	for _, clause := range raw {
		for _, temporalSplit := range splitPastCurrentTransition(clause) {
			for _, split := range splitImplicitTodoOwnerTransitions(temporalSplit) {
				if trimmed := strings.TrimSpace(split); trimmed != "" {
					clauses = append(clauses, trimmed)
				}
			}
		}
	}
	return clauses
}

func splitPastCurrentTransition(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	for _, marker := range []string{"が、原因", "が原因", "が、理由", "が理由", "が、要因", "が要因"} {
		at := strings.Index(text, marker)
		if at <= 0 {
			continue
		}
		left := strings.TrimSpace(text[:at])
		right := strings.TrimSpace(text[at+len("が"):])
		right = strings.TrimLeft(right, "、, ")
		if kindPastObservationPattern.MatchString(left) && kindExplicitCurrentIssuePattern.MatchString(right) {
			return []string{left, right}
		}
	}
	return []string{text}
}

func splitImplicitTodoOwnerTransitions(text string) []string {
	remaining := strings.TrimSpace(text)
	if remaining == "" {
		return nil
	}
	var result []string
	for {
		splitAt := -1
		for _, location := range kindClauseCommitmentEndPattern.FindAllStringIndex(remaining, -1) {
			suffix := remaining[location[1]:]
			trimmed := strings.TrimLeft(suffix, " \t　、,")
			if trimmed == "" || !kindClauseOwnerLeadPattern.MatchString(trimmed) {
				continue
			}
			splitAt = location[1]
			break
		}
		if splitAt <= 0 || splitAt >= len(remaining) {
			result = append(result, remaining)
			break
		}
		result = append(result, strings.TrimSpace(remaining[:splitAt]))
		remaining = strings.TrimLeft(remaining[splitAt:], " \t　、,")
	}
	return result
}

// futureActionIntent requires a future/imperative action form in the same
// sentence. A completed form such as 「修正しました」 contains the byte
// sequence 「修正します」, so a whole-text regexp alone would incorrectly
// classify it as a TODO.
func futureActionIntent(text string) bool {
	for _, clause := range semanticKindClauses(text) {
		remaining := kindCompletedActionPattern.ReplaceAllString(clause, "")
		if kindActionIntentPattern.MatchString(remaining) {
			return true
		}
	}
	return false
}

func futureActionCommitment(text string) bool {
	for _, clause := range semanticKindClauses(text) {
		remaining := kindCompletedActionPattern.ReplaceAllString(clause, "")
		if kindCommitmentPattern.MatchString(remaining) {
			return true
		}
	}
	return false
}

// actionDeadlinePresent accepts a date as a deadline only when that date and
// a future action belong to the same sentence. Object/event dates such as
// 「証明書が来月末に失効する」 and past timestamps therefore remain event
// metadata, not TODO deadlines.
func actionDeadlinePresent(text string) bool {
	for _, clause := range semanticKindClauses(text) {
		action := futureActionIntent(clause)
		if !action && kindOpenQuestionPattern.MatchString(clause) {
			action = kindActionVerbPattern.MatchString(clause)
		}
		if !action {
			continue
		}
		if kindDeadlineMarkerPattern.MatchString(clause) ||
			(kindRelativeWorkDatePattern.MatchString(clause) &&
				!kindScheduledEventPattern.MatchString(clause)) {
			return true
		}
	}
	return false
}

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
	return strings.Join(itemSemanticEvidenceClauses(item, scope), "。")
}

// itemSemanticEvidenceClauses returns only the evidence clauses that belong to
// this proposition. One transcript segment can contain several independent
// actions/issues; carrying the full segment into every sibling item leaks an
// assignee or deadline from one clause into another.
func itemSemanticEvidenceClauses(item liveAnalysisItem, scope liveEvidenceScope) []string {
	var localized []string
	add := func(value string) {
		for _, clause := range semanticKindClauses(value) {
			clause = strings.TrimSpace(clause)
			if clause != "" && !containsExactString(localized, clause) {
				localized = append(localized, clause)
			}
		}
	}

	// A grounding-verified snippet is the strongest proposition-local signal.
	// Select the best matching snippet clause per cited sequence; split
	// fragments intentionally retain the original snippet list for audit.
	proposition := strings.TrimSpace(item.Title + " " + item.Body)
	for _, sequenceNo := range item.EvidenceSequenceNos {
		transcript := normalizeGroundingText(scope.TranscriptText[sequenceNo])
		bestClause, bestScore := "", -1.0
		for _, snippet := range item.EvidenceSnippets {
			if normalized := normalizeGroundingText(snippet); normalized == "" ||
				!strings.Contains(transcript, normalized) {
				continue
			}
			for _, clause := range semanticKindClauses(snippet) {
				score := semanticItemSimilarity(proposition, clause)
				if sharedTreeAuditSubjectTerm(proposition, clause) {
					score += 0.15
				}
				if score > bestScore {
					bestClause, bestScore = clause, score
				}
			}
		}
		if bestClause != "" {
			add(bestClause)
		}
	}
	if len(localized) > 0 {
		return localized
	}

	seen := make(map[int64]struct{}, len(item.EvidenceSequenceNos))
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if _, duplicate := seen[sequenceNo]; duplicate {
			continue
		}
		seen[sequenceNo] = struct{}{}
		clauses := semanticKindClauses(scope.TranscriptText[sequenceNo])
		if len(clauses) == 1 {
			add(clauses[0])
			continue
		}
		bestClause := ""
		bestScore := -1.0
		for _, clause := range clauses {
			score := semanticItemSimilarity(proposition, clause)
			if sharedTreeAuditSubjectTerm(proposition, clause) {
				score += 0.15
			}
			if score > bestScore {
				bestClause, bestScore = clause, score
			}
		}
		if bestClause != "" && bestScore >= 0.08 {
			add(bestClause)
		}
	}
	return localized
}

func itemKindSemanticText(item liveAnalysisItem, scope liveEvidenceScope) string {
	proposition := strings.TrimSpace(item.Title + "。" + item.Body)
	if len(item.EvidenceSnippets) == 0 &&
		utf8.RuneCountInString(proposition) >= 12 {
		return proposition
	}
	evidence := itemSemanticEvidenceText(item, scope)
	if evidence == "" {
		return proposition
	}
	// The title provides the model's intended subject while the localized,
	// transcript-grounded clause supplies authoritative owner/deadline/action
	// attributes. A composite body is deliberately omitted here.
	return strings.TrimSpace(item.Title + "。" + evidence)
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
	text := itemKindSemanticText(item, scope)
	if utf8.RuneCountInString(strings.TrimSpace(text)) < 12 {
		text = proposition
	}

	pastObservation := kindPastObservationPattern.MatchString(text)
	explicitCurrentIssue := kindExplicitCurrentIssuePattern.MatchString(text)
	confirmedEvidence := (kindConfirmedPattern.MatchString(text) ||
		(pastObservation && !explicitCurrentIssue)) &&
		!kindRecommendationPattern.MatchString(text)
	currentProblem := kindCurrentProblemPattern.MatchString(text)
	if pastObservation && !explicitCurrentIssue {
		currentProblem = false
	}
	completedAction := kindCompletedActionPattern.MatchString(text) &&
		!kindProposalPattern.MatchString(text)
	actionIntent := futureActionIntent(text)
	commitment := futureActionCommitment(text)
	scheduledEvent := kindScheduledEventPattern.MatchString(text)
	features := itemSemanticFeatures{
		NegativeImpactPresent:      kindNegativeImpactPattern.MatchString(text),
		UncertaintyPresent:         kindUncertaintyPattern.MatchString(text),
		FutureEventPresent:         kindFutureEventPattern.MatchString(text) || scheduledEvent,
		ScheduledEventPresent:      scheduledEvent,
		CurrentProblemPresent:      currentProblem,
		ConfirmedEvidencePresent:   confirmedEvidence,
		ActionVerbPresent:          kindActionVerbPattern.MatchString(text),
		CompletedActionPresent:     completedAction,
		OwnerPresent:               kindOwnerPattern.MatchString(text),
		DeadlinePresent:            actionDeadlinePresent(text),
		EventDatePresent:           kindEventDatePattern.MatchString(text),
		DecisionOrCommitment:       commitment,
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
		!kindRecommendationPattern.MatchString(latestEvidence) &&
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
	case features.CompletedActionPresent:
		features.EpistemicStatus = "confirmed"
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
	actionIntent = actionIntent ||
		(features.ActionVerbPresent && !openQuestion &&
			(features.DeadlinePresent || features.DecisionOrCommitment))
	switch {
	case features.ConfirmationSupersedesOpen:
		features.SemanticRole = "state"
	case features.CompletedActionPresent && !actionIntent:
		features.SemanticRole = "state"
	case actionIntent && features.ProposalPresent &&
		!features.OwnerPresent && !features.DeadlinePresent && !features.DecisionOrCommitment:
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

	text := itemKindSemanticText(item, scope)
	openQuestion := kindOpenQuestionPattern.MatchString(text)
	actionIntent := futureActionIntent(text) ||
		(features.ActionVerbPresent && !openQuestion &&
			(features.DeadlinePresent || features.DecisionOrCommitment))
	unassignedNecessity := kindUnassignedNecessityPattern.MatchString(text) &&
		!features.OwnerPresent && !features.DecisionOrCommitment
	uncommittedProposal := features.ProposalPresent &&
		!features.OwnerPresent && !features.DeadlinePresent && !features.DecisionOrCommitment
	strongAction := actionIntent && !uncommittedProposal &&
		(features.OwnerPresent || features.DeadlinePresent || features.DecisionOrCommitment)
	preserveExplicitQuestion := originalKind == "issue" && originalSubtype == issueSubtypeQuestion && openQuestion
	preserveOpenIssue := originalKind == "issue" && validIssueSubtype(originalSubtype) &&
		!features.ConfirmationSupersedesOpen &&
		(kindOpenQuestionPattern.MatchString(item.Title+" "+item.Body) ||
			features.CurrentProblemPresent)

	switch {
	case features.CompletedActionPresent && !actionIntent &&
		!openQuestion && !features.ProposalPresent && !features.UncertaintyPresent:
		decision.CanonicalKind = "fact"
		decision.CanonicalSubtype = ""
		decision.Reason = "completed_action_is_historical_fact"
		decision.Confidence = 0.98
	case preserveExplicitQuestion:
		decision.CanonicalKind = "issue"
		decision.CanonicalSubtype = issueSubtypeQuestion
		decision.Reason = "explicit_question_preserved"
		decision.Confidence = 0.98
	case preserveOpenIssue:
		decision.CanonicalKind = "issue"
		decision.CanonicalSubtype = originalSubtype
		decision.Reason = "explicit_open_issue_preserved_from_companion_action"
		decision.Confidence = 0.96
	case unassignedNecessity:
		decision.CanonicalKind = "issue"
		decision.CanonicalSubtype = issueSubtypeDiscussion
		decision.Reason = "unassigned_action_necessity_remains_open"
		decision.Confidence = 0.97
	case strongAction && !features.ConfirmationSupersedesOpen:
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
		(!openQuestion || features.ConfirmationSupersedesOpen) &&
		(!strongAction || features.ConfirmationSupersedesOpen):
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
	case kindIncompletePurposePattern.MatchString(text) && !actionIntent:
		decision.CanonicalKind = "issue"
		decision.CanonicalSubtype = issueSubtypeDiscussion
		decision.Reason = "uncommitted_purpose_or_incomplete_action"
		decision.Confidence = 0.92
	case originalKind == "todo" && actionIntent &&
		(features.CurrentProblemPresent || openQuestion) &&
		(features.OwnerPresent || features.DeadlinePresent || features.DecisionOrCommitment):
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
	case features.ScheduledEventPresent && !actionIntent && !openQuestion:
		decision.CanonicalKind = "fact"
		decision.CanonicalSubtype = ""
		decision.Reason = "scheduled_object_or_event_state"
		decision.Confidence = 0.95
	case actionIntent && uncommittedProposal:
		decision.CanonicalKind = "issue"
		if originalKind != "issue" || !validIssueSubtype(originalSubtype) {
			decision.CanonicalSubtype = issueSubtypeDiscussion
		}
		decision.Reason = "uncommitted_action_proposal"
		decision.Confidence = 0.94
	case kindRecommendationPattern.MatchString(text) && features.ActionVerbPresent:
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
	case originalKind == "todo" && !features.OwnerPresent &&
		!features.DeadlinePresent && !features.DecisionOrCommitment &&
		kindUnassignedManagementPattern.MatchString(text):
		decision.CanonicalKind = "issue"
		decision.CanonicalSubtype = issueSubtypeDiscussion
		decision.Reason = "unassigned_management_or_followup_is_not_todo"
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
			if decision.CanonicalKind == "todo" &&
				decision.OriginalKind != "todo" &&
				validated[index].Status == "resolved" {
				// Resolution belonged to the old Issue/Risk classification.
				// A newly recognized future commitment must be tracked as an
				// open Todo unless separate completion evidence resolves it.
				validated[index].Status = "open"
				validated[index].ResolvedAtVersion = 0
				validated[index].ResolutionEvidenceSequenceNos = nil
				validated[index].ResolutionReason = ""
			}
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
			candidate.Title = semanticallyCompleteItemLabelOrOriginal(fragment.Text, fragment.Kind)
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
			count += len(semanticKindRelations(items[left], items[right], liveEvidenceScope{}))
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
		conditionalReferent := index > 0 &&
			itemLabelConditionalWithoutSubjectPattern.MatchString(sentence) &&
			(kindScheduledEventPattern.MatchString(sentences[index-1]) ||
				kindFutureEventPattern.MatchString(sentences[index-1]))
		if (finalItemIsLowInformation(probe) || liveItemTextNeedsReferent(probe)) &&
			!conditionalReferent {
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
			candidate.Title = semanticallyCompleteItemLabelOrOriginal(fragment.Text, fragment.Kind)
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
	return reconcileSemanticKindRelations(
		tree, items, liveEvidenceScope{}, 0, "deterministic_inference",
	)
}

const (
	itemRelationSupportedBy = "supported_by"
	itemRelationCausedBy    = "caused_by"
	itemRelationLimits      = "limits"
	itemRelationResolves    = "resolves"
	itemRelationActionFor   = "action_for"
	itemRelationContradicts = "contradicts"
	itemRelationRefines     = "refines"
)

var itemRelationLimitPattern = regexp.MustCompile(
	`(?:ただし|一方で|まで.{0,24}説明できるか|適用範囲|限定|限界|未確認)`,
)

func validItemRelationKind(kind string) bool {
	switch kind {
	case itemRelationSupportedBy, itemRelationCausedBy, itemRelationLimits,
		itemRelationResolves, itemRelationActionFor, itemRelationContradicts,
		itemRelationRefines:
		return true
	default:
		return false
	}
}

func relationKey(relation liveAnalysisTreeRelation) string {
	return relation.Source + "\x00" + relation.Kind + "\x00" + relation.Target
}

func deterministicRelationID(source, kind, target string) string {
	sum := sha256.Sum256([]byte(source + "\x00" + kind + "\x00" + target))
	return fmt.Sprintf("relation-%x", sum[:8])
}

func reconcileSemanticKindRelations(
	tree *liveAnalysisTree,
	items []liveAnalysisItem,
	scope liveEvidenceScope,
	version int64,
	origin string,
) int {
	if tree == nil {
		return 0
	}
	active := make([]liveAnalysisItem, 0, len(items))
	activeByID := make(map[string]liveAnalysisItem, len(items))
	for _, item := range items {
		if !item.Inactive && item.MergedIntoID == "" && strings.TrimSpace(item.ID) != "" {
			active = append(active, item)
			activeByID[item.ID] = item
		}
	}
	desired := make(map[string]liveAnalysisTreeRelation)
	for left := 0; left < len(active); left++ {
		for right := left + 1; right < len(active); right++ {
			for _, relation := range semanticKindRelations(active[left], active[right], scope) {
				if !semanticRelationItemsRelated(tree, activeByID[relation.Source], activeByID[relation.Target], relation.Kind) {
					continue
				}
				relation.ID = deterministicRelationID(relation.Source, relation.Kind, relation.Target)
				relation.EvidenceSequenceNos = relationEvidenceSequenceNos(
					activeByID[relation.Source], activeByID[relation.Target],
				)
				relation.Origin = origin
				relation.Status = "active"
				relation.CreatedAtVersion = version
				relation.UpdatedAtVersion = version
				desired[relationKey(relation)] = relation
			}
		}
	}

	kept := make([]liveAnalysisTreeRelation, 0, len(tree.Relations)+len(desired))
	existing := make(map[string]struct{}, len(tree.Relations))
	for _, relation := range tree.Relations {
		relation = canonicalizeLegacyItemRelation(relation, activeByID)
		if !validSemanticTreeRelation(relation, activeByID) {
			continue
		}
		key := relationKey(relation)
		if _, duplicate := existing[key]; duplicate {
			continue
		}
		if replacement, ok := desired[key]; ok {
			if relation.CreatedAtVersion > 0 {
				replacement.CreatedAtVersion = relation.CreatedAtVersion
			}
			if strings.TrimSpace(relation.Origin) != "" {
				replacement.Origin = relation.Origin
			}
			relation = replacement
			delete(desired, key)
		} else if relation.Origin == "deterministic_inference" || relation.Origin == "final_repair" {
			// Canonical items are the source of truth. A deterministic relation
			// whose evidence no longer satisfies the rule is retired here.
			continue
		}
		if relation.ID == "" {
			relation.ID = deterministicRelationID(relation.Source, relation.Kind, relation.Target)
		}
		if relation.Status == "" {
			relation.Status = "active"
		}
		kept = append(kept, relation)
		existing[key] = struct{}{}
	}

	created := 0
	for key, relation := range desired {
		if _, duplicate := existing[key]; duplicate {
			continue
		}
		kept = append(kept, relation)
		existing[key] = struct{}{}
		created++
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Source != kept[j].Source {
			return kept[i].Source < kept[j].Source
		}
		if kept[i].Kind != kept[j].Kind {
			return kept[i].Kind < kept[j].Kind
		}
		return kept[i].Target < kept[j].Target
	})
	tree.Relations = kept
	return created
}

func semanticKindRelations(left, right liveAnalysisItem, scope liveEvidenceScope) []liveAnalysisTreeRelation {
	result := make([]liveAnalysisTreeRelation, 0, 2)
	for _, pair := range [][2]liveAnalysisItem{{left, right}, {right, left}} {
		source, target := pair[0], pair[1]
		sourceFeatures := inferItemSemanticFeatures(source, scope)
		targetFeatures := inferItemSemanticFeatures(target, scope)
		switch {
		case source.Kind == "issue" && sourceFeatures.CausalHypothesisPresent &&
			target.Kind == "fact" && targetFeatures.ConfirmedEvidencePresent:
			result = append(result, liveAnalysisTreeRelation{
				Source: source.ID, Target: target.ID, Kind: itemRelationSupportedBy, Confidence: 0.94,
			})
		case source.Kind == "issue" && target.Kind == "issue" &&
			itemRelationLimitPattern.MatchString(source.Title+" "+source.Body) &&
			kindOpenQuestionPattern.MatchString(source.Title+" "+source.Body) &&
			targetFeatures.CausalHypothesisPresent &&
			evidenceFollowsWithin(source, target, 2):
			result = append(result, liveAnalysisTreeRelation{
				Source: source.ID, Target: target.ID, Kind: itemRelationLimits, Confidence: 0.91,
			})
		case source.Kind == "todo" && (target.Kind == "issue" || target.Kind == "risk" || target.Kind == "decision") &&
			sourceFeatures.ActionVerbPresent:
			result = append(result, liveAnalysisTreeRelation{
				Source: source.ID, Target: target.ID, Kind: itemRelationActionFor, Confidence: 0.88,
			})
		}
	}
	return result
}

func semanticRelationItemsRelated(tree *liveAnalysisTree, source, target liveAnalysisItem, kind string) bool {
	sourceText := source.Title + " " + source.Body
	targetText := target.Title + " " + target.Body
	sourceTopic, targetTopic := treeItemTopic(tree, source.ID), treeItemTopic(tree, target.ID)
	crossTopic := sourceTopic != "" && targetTopic != "" && sourceTopic != targetTopic
	sharedSubject := sharedTreeAuditSubjectTerm(sourceText, targetText)
	similarity := semanticItemSimilarity(sourceText, targetText)
	switch kind {
	case itemRelationLimits:
		if crossTopic {
			return evidenceFollowsWithin(source, target, 2) && sharedSubject && similarity >= 0.08
		}
		return evidenceFollowsWithin(source, target, 2) &&
			(sharedSubject || itemLabelContextDependentPattern.MatchString(source.Title) ||
				itemLabelDeicticSettingPattern.MatchString(sourceText))
	default:
		if crossTopic {
			return itemEvidenceWithin(source, target, 2) && sharedSubject && similarity >= 0.10
		}
		return itemEvidenceOverlaps(source, target) ||
			(itemEvidenceWithin(source, target, 2) && sharedSubject && similarity >= 0.10)
	}
}

func evidenceFollowsWithin(source, target liveAnalysisItem, maxDistance int64) bool {
	for _, sourceSequence := range source.EvidenceSequenceNos {
		for _, targetSequence := range target.EvidenceSequenceNos {
			if sourceSequence > targetSequence && sourceSequence-targetSequence <= maxDistance {
				return true
			}
		}
	}
	return false
}

func relationEvidenceSequenceNos(source, target liveAnalysisItem) []int64 {
	sequenceNos := append([]int64(nil), source.EvidenceSequenceNos...)
	sequenceNos = append(sequenceNos, target.EvidenceSequenceNos...)
	sort.Slice(sequenceNos, func(i, j int) bool { return sequenceNos[i] < sequenceNos[j] })
	kept := sequenceNos[:0]
	for _, sequenceNo := range sequenceNos {
		if sequenceNo <= 0 || (len(kept) > 0 && kept[len(kept)-1] == sequenceNo) {
			continue
		}
		kept = append(kept, sequenceNo)
	}
	return kept
}

func canonicalizeLegacyItemRelation(
	relation liveAnalysisTreeRelation,
	active map[string]liveAnalysisItem,
) liveAnalysisTreeRelation {
	relation.Source = strings.TrimSpace(relation.Source)
	relation.Target = strings.TrimSpace(relation.Target)
	relation.Kind = strings.TrimSpace(relation.Kind)
	switch relation.Kind {
	case "supports":
		// Legacy payloads used evidence -> proposition. The canonical relation
		// vocabulary uses proposition --supported_by--> evidence.
		relation.Source, relation.Target = relation.Target, relation.Source
		relation.Kind = itemRelationSupportedBy
	case "mitigates", "addresses":
		relation.Kind = itemRelationActionFor
	}
	return relation
}

func validSemanticTreeRelation(relation liveAnalysisTreeRelation, active map[string]liveAnalysisItem) bool {
	if relation.Source == "" || relation.Target == "" || relation.Source == relation.Target ||
		!validItemRelationKind(relation.Kind) || relation.Status == "inactive" {
		return false
	}
	_, sourceOK := active[relation.Source]
	_, targetOK := active[relation.Target]
	return sourceOK && targetOK
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
