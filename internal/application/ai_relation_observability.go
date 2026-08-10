package application

import (
	"fmt"
	"sort"
	"strings"
)

type finalRelationSummary struct {
	RelationCount                   int
	RelationCountByType             map[string]int
	ActiveRelationCount             int
	InactiveRelationCount           int
	DanglingRelationCount           int
	SelfRelationCount               int
	DuplicateRelationCount          int
	InactiveEndpointRelationCount   int
	ActionForFanOutMax              int
	ActionForSourceCount            int
	ExpectedSupportedByMissingCount int
	ExpectedLimitsMissingCount      int
	RelationMonoculture             bool
}

func summarizeFinalRelationsWithItems(
	tree *liveAnalysisTree,
	items []liveAnalysisItem,
) finalRelationSummary {
	summary := finalRelationSummary{RelationCountByType: make(map[string]int)}
	if tree == nil {
		return summary
	}
	nodes := make(map[string]struct{}, len(tree.Nodes))
	for _, node := range tree.Nodes {
		nodes[node.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(tree.Relations))
	activeKeys := make(map[string]struct{}, len(tree.Relations))
	actionForTargets := make(map[string]map[string]struct{})
	activeItems := make(map[string]liveAnalysisItem, len(items))
	for _, item := range items {
		if !item.Inactive && item.MergedIntoID == "" {
			activeItems[item.ID] = item
		}
	}
	for _, relation := range tree.Relations {
		summary.RelationCount++
		kind := strings.TrimSpace(relation.Kind)
		if kind == "" {
			kind = "unknown"
		}
		summary.RelationCountByType[kind]++
		status := strings.ToLower(strings.TrimSpace(relation.Status))
		if status == "" || status == "active" {
			summary.ActiveRelationCount++
		} else {
			summary.InactiveRelationCount++
		}
		_, sourceExists := nodes[relation.Source]
		_, targetExists := nodes[relation.Target]
		if !sourceExists || !targetExists {
			summary.DanglingRelationCount++
		}
		if relation.Source == relation.Target {
			summary.SelfRelationCount++
		}
		if len(items) > 0 {
			_, sourceActive := activeItems[relation.Source]
			_, targetActive := activeItems[relation.Target]
			if !sourceActive || !targetActive {
				summary.InactiveEndpointRelationCount++
			}
		}
		if kind == itemRelationActionFor {
			if actionForTargets[relation.Source] == nil {
				actionForTargets[relation.Source] = make(map[string]struct{})
			}
			actionForTargets[relation.Source][relation.Target] = struct{}{}
		}
		key := strings.Join([]string{kind, relation.Source, relation.Target, status}, "|")
		if _, duplicate := seen[key]; duplicate {
			summary.DuplicateRelationCount++
		} else {
			seen[key] = struct{}{}
		}
		if status == "" || status == "active" {
			activeKeys[relationKey(relation)] = struct{}{}
		}
	}
	for _, targets := range actionForTargets {
		summary.ActionForSourceCount++
		if len(targets) > summary.ActionForFanOutMax {
			summary.ActionForFanOutMax = len(targets)
		}
	}
	if summary.RelationCount > 0 && len(summary.RelationCountByType) == 1 {
		summary.RelationMonoculture = true
	}
	if len(items) > 0 {
		expected := expectedLogicalRelationKeys(tree, activeItems)
		for key, kind := range expected {
			if _, exists := activeKeys[key]; exists {
				continue
			}
			switch kind {
			case itemRelationSupportedBy:
				summary.ExpectedSupportedByMissingCount++
			case itemRelationLimits:
				summary.ExpectedLimitsMissingCount++
			}
		}
	}
	return summary
}

func expectedLogicalRelationKeys(
	tree *liveAnalysisTree,
	items map[string]liveAnalysisItem,
) map[string]string {
	result := make(map[string]string)
	values := make([]liveAnalysisItem, 0, len(items))
	for _, item := range items {
		values = append(values, item)
	}
	for left := 0; left < len(values); left++ {
		for right := left + 1; right < len(values); right++ {
			for _, relation := range semanticKindRelations(values[left], values[right], liveEvidenceScope{}) {
				if relation.Kind != itemRelationSupportedBy && relation.Kind != itemRelationLimits {
					continue
				}
				if semanticRelationItemsRelated(tree, items[relation.Source], items[relation.Target], relation.Kind) {
					result[relationKey(relation)] = relation.Kind
				}
			}
		}
	}
	return result
}

func formatRelationCountByType(values map[string]int) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", key, values[key]))
	}
	return strings.Join(parts, ",")
}

func sortedRelationsForLog(tree *liveAnalysisTree) []liveAnalysisTreeRelation {
	if tree == nil {
		return nil
	}
	result := append([]liveAnalysisTreeRelation(nil), tree.Relations...)
	sort.SliceStable(result, func(i, j int) bool {
		left := result[i].ID + "|" + result[i].Kind + "|" + result[i].Source + "|" + result[i].Target
		right := result[j].ID + "|" + result[j].Kind + "|" + result[j].Source + "|" + result[j].Target
		return left < right
	})
	return result
}
