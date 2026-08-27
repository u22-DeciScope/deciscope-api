package application

import (
	"fmt"
	"sort"
	"strings"
)

const semanticTopicConcentrationMin = 0.80

// semanticTreeHealth complements treeHealth. treeHealth answers whether the
// parent graph is structurally crowded; this value answers whether discussion
// from multiple agenda/subject clusters has been collapsed into one otherwise
// valid topic.
type semanticTreeHealth struct {
	MaterializedAgendaCount            int            `json:"materializedAgendaCount"`
	DiscussedAgendaCount               int            `json:"discussedAgendaCount"`
	ActiveDetailItemCount              int            `json:"activeDetailItemCount"`
	ItemsPerTopic                      map[string]int `json:"itemsPerTopic"`
	ItemsPerAgenda                     map[string]int `json:"itemsPerAgenda"`
	MaxTopicConcentration              float64        `json:"maxTopicConcentration"`
	CrossAgendaItemCount               int            `json:"crossAgendaItemCount"`
	TopicSemanticDiversity             int            `json:"topicSemanticDiversity"`
	UnmaterializedDiscussedAgendaCount int            `json:"unmaterializedDiscussedAgendaCount"`
	SemanticTopicConcentrationCount    int            `json:"semanticTopicConcentrationCount"`
	NeedsReorganization                bool           `json:"needsReorganization"`
}

func (h semanticTreeHealth) String() string {
	return fmt.Sprintf("materializedAgendaCount=%d discussedAgendaCount=%d activeDetailItemCount=%d itemsPerTopic=%s itemsPerAgenda=%s maxTopicConcentration=%.2f crossAgendaItemCount=%d topicSemanticDiversity=%d unmaterializedDiscussedAgendaCount=%d semanticTopicConcentrationCount=%d semanticNeedsReorganization=%t",
		h.MaterializedAgendaCount, h.DiscussedAgendaCount, h.ActiveDetailItemCount,
		formatSemanticHealthCounts(h.ItemsPerTopic), formatSemanticHealthCounts(h.ItemsPerAgenda),
		h.MaxTopicConcentration, h.CrossAgendaItemCount, h.TopicSemanticDiversity,
		h.UnmaterializedDiscussedAgendaCount, h.SemanticTopicConcentrationCount,
		h.NeedsReorganization)
}

func formatSemanticHealthCounts(values map[string]int) string {
	if len(values) == 0 {
		return "[]"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", key, values[key]))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func computeSemanticTreeHealth(state liveAnalysisPayload) semanticTreeHealth {
	health := semanticTreeHealth{
		ItemsPerTopic:  make(map[string]int),
		ItemsPerAgenda: make(map[string]int),
	}
	if state.Tree == nil {
		return health
	}

	byNodeID := make(map[string]liveAnalysisTreeNode, len(state.Tree.Nodes))
	materializedAgendas := make(map[string]struct{})
	for _, node := range state.Tree.Nodes {
		byNodeID[node.ID] = node
		if node.Kind == "topic" {
			for _, agendaID := range node.AgendaRefs {
				if agendaID = strings.TrimSpace(agendaID); agendaID != "" {
					materializedAgendas[agendaID] = struct{}{}
				}
			}
		}
	}
	for _, anchor := range state.AgendaAnchors {
		if len(anchor.MaterializedTopicIDs) > 0 {
			materializedAgendas[anchor.AgendaID] = struct{}{}
		}
	}

	discussedAgendas := make(map[string]struct{})
	for _, anchor := range state.AgendaAnchors {
		if anchor.Status == agendaStatusMaterialized || anchor.Status == agendaStatusDiscussed {
			discussedAgendas[anchor.AgendaID] = struct{}{}
		}
	}
	itemAgendas := make(map[string]map[string]struct{})
	if state.AgendaProgress != nil {
		for _, entry := range state.AgendaProgress.Entries {
			status := entry.EffectiveStatus
			if status == "" {
				status = entry.ComputedStatus
			}
			if status == agendaProgressDiscussing || status == agendaProgressDiscussed {
				discussedAgendas[entry.ID] = struct{}{}
			}
			for _, itemID := range entry.ActiveItemIDs {
				addSemanticHealthItemAgenda(itemAgendas, itemID, entry.ID)
			}
		}
	}

	topicForNode := func(id string) string {
		seen := make(map[string]struct{})
		for id != "" {
			node, ok := byNodeID[id]
			if !ok {
				return ""
			}
			if node.Kind == "topic" {
				return node.ID
			}
			if _, looped := seen[id]; looped {
				return ""
			}
			seen[id] = struct{}{}
			id = node.ParentID
		}
		return ""
	}
	itemTopics := make(map[string]string)
	for _, node := range state.Tree.Nodes {
		if node.Kind == "topic" || node.Kind == "group" {
			for _, itemID := range node.RelatedItemIDs {
				itemTopics[itemID] = topicForNode(node.ID)
			}
			continue
		}
		itemTopics[node.ID] = topicForNode(node.ID)
	}

	clusterRepresentatives := make([]string, 0)
	for _, item := range state.Items {
		if !semanticHealthActiveItem(item) {
			continue
		}
		health.ActiveDetailItemCount++
		topicID := itemTopics[item.ID]
		if topicID != "" {
			health.ItemsPerTopic[topicID]++
			for _, agendaID := range byNodeID[topicID].AgendaRefs {
				addSemanticHealthItemAgenda(itemAgendas, item.ID, agendaID)
			}
		}
		for _, agendaID := range item.RelatedAgendaIDs {
			addSemanticHealthItemAgenda(itemAgendas, item.ID, agendaID)
		}
		text := strings.TrimSpace(item.Title + " " + item.Body)
		if text != "" && !matchesSemanticHealthCluster(text, clusterRepresentatives) {
			clusterRepresentatives = append(clusterRepresentatives, text)
		}
	}
	for _, item := range state.Items {
		if !semanticHealthActiveItem(item) {
			continue
		}
		agendas := itemAgendas[item.ID]
		if len(agendas) > 1 {
			health.CrossAgendaItemCount++
		}
		for agendaID := range agendas {
			health.ItemsPerAgenda[agendaID]++
		}
	}

	maxTopicItems := 0
	for _, count := range health.ItemsPerTopic {
		if count > maxTopicItems {
			maxTopicItems = count
		}
	}
	if health.ActiveDetailItemCount > 0 {
		health.MaxTopicConcentration = float64(maxTopicItems) / float64(health.ActiveDetailItemCount)
	}
	health.MaterializedAgendaCount = len(materializedAgendas)
	health.DiscussedAgendaCount = len(discussedAgendas)
	for agendaID := range discussedAgendas {
		if _, materialized := materializedAgendas[agendaID]; !materialized {
			health.UnmaterializedDiscussedAgendaCount++
		}
	}
	health.TopicSemanticDiversity = len(clusterRepresentatives)
	health.NeedsReorganization = health.DiscussedAgendaCount >= 2 &&
		health.MaterializedAgendaCount == 1 &&
		health.ActiveDetailItemCount >= 2 &&
		health.MaxTopicConcentration >= semanticTopicConcentrationMin &&
		health.TopicSemanticDiversity >= 2
	if health.NeedsReorganization {
		health.SemanticTopicConcentrationCount = 1
	}
	return health
}

func semanticHealthActiveItem(item liveAnalysisItem) bool {
	return strings.TrimSpace(item.ID) != "" && !item.Inactive && item.MergedIntoID == "" &&
		item.Status != "resolved" && item.Status != "dismissed" &&
		item.ClassificationStatus != classificationTentative &&
		item.ClassificationStatus != classificationUnclassified
}

func addSemanticHealthItemAgenda(index map[string]map[string]struct{}, itemID, agendaID string) {
	itemID, agendaID = strings.TrimSpace(itemID), strings.TrimSpace(agendaID)
	if itemID == "" || agendaID == "" {
		return
	}
	if index[itemID] == nil {
		index[itemID] = make(map[string]struct{})
	}
	index[itemID][agendaID] = struct{}{}
}

func matchesSemanticHealthCluster(text string, representatives []string) bool {
	for _, representative := range representatives {
		if semanticItemSimilarity(text, representative) >= 0.60 {
			return true
		}
	}
	return false
}
