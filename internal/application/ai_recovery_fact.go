package application

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	recoveryAtomicFactPattern     = regexp.MustCompile(`(?:切り戻|設定.{0,16}修正|修正(?:した|しました)|接続.{0,24}正常|正常.{0,16}確認|疎通.{0,8}確認)`)
	recoveryAggregateLabelPattern = regexp.MustCompile(`(?:復旧対応|復旧作業|確認作業|復旧措置)`)
	recoveryRollbackFactPattern   = regexp.MustCompile(`切り戻`)
	recoveryRepairFactPattern     = regexp.MustCompile(`(?:トランク|設定).{0,20}修正|修正.{0,20}(?:トランク|設定)`)
	recoveryNormalFactPattern     = regexp.MustCompile(`(?:接続|疎通).{0,28}正常|正常.{0,20}(?:接続|疎通)|正常になった`)
)

// splitLiveRecoveryFacts decomposes one model aggregate into independently
// grounded historical facts. Each fragment keeps only the source sequence
// that contains it, so one unsupported atom cannot reject its siblings.
func splitLiveRecoveryFacts(items []liveAnalysisItem, assignments []treeAssignment, scope liveEvidenceScope, stats *liveAnalysisTreeMergeStats) ([]liveAnalysisItem, []treeAssignment) {
	expanded := make([]liveAnalysisItem, 0, len(items)+2)
	expandedAssignments := append([]treeAssignment(nil), assignments...)
	for _, item := range items {
		fragments := recoveryFactFragments(item, scope)
		if len(fragments) < 2 {
			expanded = append(expanded, item)
			continue
		}
		sourceRef := modelItemReference(item)
		for index, fragment := range fragments {
			candidate := item
			candidate.Title = semanticallyCompleteItemLabelOrOriginal(fragment.Text, "fact")
			candidate.Body = truncateRunes(fragment.Text, liveAnalysisTreeDescriptionMaxRunes)
			candidate.EvidenceSequenceNos = []int64{fragment.SequenceNo}
			candidate.EvidenceSnippets = []string{fragment.Text}
			candidate.evidenceSpecified = true
			candidate.semanticSplitFragment = true
			candidate.modelReference = sourceRef
			if index > 0 {
				ref := fmt.Sprintf("%s-recovery-split-%d", sourceRef, index+1)
				if strings.TrimSpace(item.ClientKey) != "" {
					candidate.ClientKey, candidate.ID = ref, ""
				} else {
					candidate.ClientKey = ""
					candidate.ID = serverGeneratedItemID(candidate)
					ref = candidate.ID
				}
				expandedAssignments = cloneKindSplitAssignment(expandedAssignments, sourceRef, item.ID, ref)
			}
			expanded = append(expanded, candidate)
		}
		recordRecoveryFactSplit(stats, sourceRef, len(fragments))
	}
	return expanded, expandedAssignments
}

type recoveryFactFragment struct {
	Text       string
	SequenceNo int64
}

func recoveryFactFragments(item liveAnalysisItem, scope liveEvidenceScope) []recoveryFactFragment {
	if item.Kind != "fact" || item.Inactive || item.MergedIntoID != "" {
		return nil
	}
	var fragments []recoveryFactFragment
	for _, sequenceNo := range item.EvidenceSequenceNos {
		for _, clause := range recoveryAtomicClauses(scope.TranscriptText[sequenceNo]) {
			if !recoveryAtomicFactPattern.MatchString(clause) {
				continue
			}
			fragments = append(fragments, recoveryFactFragment{Text: clause, SequenceNo: sequenceNo})
		}
	}
	if len(fragments) < 2 {
		return nil
	}
	itemText := item.Title + " " + item.Body
	if recoveryFactCategoryCount(itemText) < 2 &&
		!recoveryAggregateLabelPattern.MatchString(itemText) {
		return nil
	}
	return fragments
}

func recoveryFactCategoryCount(text string) int {
	count := 0
	for _, pattern := range []*regexp.Regexp{
		recoveryRollbackFactPattern,
		recoveryRepairFactPattern,
		recoveryNormalFactPattern,
	} {
		if pattern.MatchString(text) {
			count++
		}
	}
	return count
}

func recoveryAtomicClauses(text string) []string {
	text = strings.ReplaceAll(text, "、その後、", "。")
	text = strings.ReplaceAll(text, "、その後", "。")
	text = strings.ReplaceAll(text, "その後、", "")
	var result []string
	for _, raw := range kindSentenceBoundaryPattern.Split(text, -1) {
		clause := strings.Trim(strings.TrimSpace(raw), "、, ")
		for _, prefix := range []string{"復旧対応としては、", "復旧対応として、", "復旧対応としては", "復旧対応として"} {
			clause = strings.TrimSpace(strings.TrimPrefix(clause, prefix))
		}
		if clause != "" && recoveryAtomicFactPattern.MatchString(clause) && !containsExactString(result, clause) {
			result = append(result, clause)
		}
	}
	return result
}

func recordRecoveryFactSplit(stats *liveAnalysisTreeMergeStats, sourceID string, fragments int) {
	if stats == nil || fragments < 2 {
		return
	}
	stats.KindSemanticSplits++
	stats.KindSplitFragments += fragments
	stats.KindSplitDecisions = append(stats.KindSplitDecisions, itemKindSplitDecision{
		SourceItemID: sourceID, FragmentCount: fragments,
		FragmentKinds: []string{"fact", "fact", "fact"},
	})
}

func splitPersistedRecoveryFacts(state *liveAnalysisPayload, scope liveEvidenceScope, version int64, stats *liveAnalysisTreeMergeStats) {
	if state == nil || state.Tree == nil {
		return
	}
	original := append([]liveAnalysisItem(nil), state.Items...)
	for _, item := range original {
		fragments := recoveryFactFragments(item, scope)
		if len(fragments) < 2 {
			continue
		}
		parentID := ""
		if node := liveTreeNodeByID(state.Tree, item.ID); node != nil {
			parentID = node.ParentID
		}
		for index, fragment := range fragments {
			candidate := item
			candidate.Title = semanticallyCompleteItemLabelOrOriginal(fragment.Text, "fact")
			candidate.Body = truncateRunes(fragment.Text, liveAnalysisTreeDescriptionMaxRunes)
			candidate.EvidenceSequenceNos = []int64{fragment.SequenceNo}
			candidate.EvidenceSnippets = []string{fragment.Text}
			candidate.semanticSplitFragment = true
			candidate.evidenceSpecified = true
			if index == 0 {
				updateFinalItemAndNode(state, candidate)
				continue
			}
			candidate.ID = serverGeneratedItemID(candidate)
			if _, exists := finalItemByID(state.Items, candidate.ID); exists {
				continue
			}
			state.Items = append(state.Items, candidate)
			state.Tree.Nodes = append(state.Tree.Nodes, liveAnalysisTreeNode{
				ID: candidate.ID, Kind: candidate.Kind, ParentID: parentID,
				Label: candidate.Title, Description: candidate.Body, Status: candidate.Status,
				CreatedAtVersion: version, UpdatedAtVersion: version,
				LastParentChangeSource: "recovery_atomic_split", LastParentChangeVersion: version,
			})
		}
		recordRecoveryFactSplit(stats, item.ID, len(fragments))
	}
	rebuildTreeAuditEdges(state.Tree)
}
