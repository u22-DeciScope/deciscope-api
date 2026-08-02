package application

import (
	"fmt"
	"sort"
	"strings"
)

type finalRelationSummary struct {
	RelationCount          int
	RelationCountByType    map[string]int
	ActiveRelationCount    int
	InactiveRelationCount  int
	DanglingRelationCount  int
	SelfRelationCount      int
	DuplicateRelationCount int
}

func summarizeFinalRelations(tree *liveAnalysisTree) finalRelationSummary {
	summary := finalRelationSummary{RelationCountByType: make(map[string]int)}
	if tree == nil {
		return summary
	}
	nodes := make(map[string]struct{}, len(tree.Nodes))
	for _, node := range tree.Nodes {
		nodes[node.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(tree.Relations))
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
		key := strings.Join([]string{kind, relation.Source, relation.Target, status}, "|")
		if _, duplicate := seen[key]; duplicate {
			summary.DuplicateRelationCount++
		} else {
			seen[key] = struct{}{}
		}
	}
	return summary
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
