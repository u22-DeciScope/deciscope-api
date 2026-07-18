package application

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

var reservedItemIDPrefixes = []string{
	"agenda-",
	"topic-",
	"group-",
	"reference-",
	"candidate-",
	"action-summary-",
}

type itemIdentityEvaluation struct {
	ModelItemID           string
	CanonicalItemID       string
	NodeType              string
	CollisionWithNodeType string
	Remapped              bool
	Quarantined           bool
	Reason                string
}

func modelItemReference(item liveAnalysisItem) string {
	if key := strings.TrimSpace(item.ClientKey); key != "" {
		return key
	}
	return strings.TrimSpace(item.ID)
}

func reservedItemID(id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return false
	}
	if id == treeRootNodeID {
		return true
	}
	for _, prefix := range reservedItemIDPrefixes {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

func itemIDKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "open_issue" {
		return "open-issue"
	}
	if validLiveAnalysisItemKind(kind) {
		return kind
	}
	return "item"
}

// serverGeneratedItemID owns the persistent item namespace. Model client keys
// are round-local references only; the stable ID is derived from item meaning.
func serverGeneratedItemID(item liveAnalysisItem) string {
	subject := semanticItemKey(strings.TrimSpace(item.Title + " " + item.Body))
	if subject == "" {
		subject = canonicalReferenceKey(modelItemReference(item))
	}
	sum := sha256.Sum256([]byte(itemIDKind(item.Kind) + "\x00" + subject))
	return "item-" + itemIDKind(item.Kind) + "-" + hex.EncodeToString(sum[:6])
}

func recordItemIdentity(stats *liveAnalysisTreeMergeStats, evaluation itemIdentityEvaluation) {
	if stats == nil {
		return
	}
	stats.ItemIdentityDecisions = append(stats.ItemIdentityDecisions, evaluation)
	if evaluation.Remapped {
		stats.ReservedItemIDsRemapped++
	}
	if evaluation.Quarantined {
		stats.ReservedItemIDsRejected++
	}
	if evaluation.CollisionWithNodeType != "" {
		stats.CrossKindIDCollisions++
	}
}

// repairReservedPersistedItemIDs upgrades a legacy payload in memory. Only
// detail nodes are renamed; a fixed topic with the same old ID is retained.
// The database row is never mutated by this compatibility repair on read.
func repairReservedPersistedItemIDs(state *liveAnalysisPayload, stats *liveAnalysisTreeMergeStats) map[string]string {
	if state == nil {
		return nil
	}
	used := make(map[string]struct{}, len(state.Items))
	remap := make(map[string]string)
	kept := make([]liveAnalysisItem, 0, len(state.Items))
	for _, item := range state.Items {
		oldID := strings.TrimSpace(item.ID)
		if oldID == "" {
			continue
		}
		if !reservedItemID(oldID) {
			if _, duplicate := used[oldID]; duplicate {
				recordItemIdentity(stats, itemIdentityEvaluation{ModelItemID: oldID, NodeType: "item", Quarantined: true, Reason: "duplicate_item_id"})
				continue
			}
			used[oldID] = struct{}{}
			kept = append(kept, item)
			continue
		}
		newID := serverGeneratedItemID(item)
		if _, duplicate := used[newID]; duplicate {
			recordItemIdentity(stats, itemIdentityEvaluation{ModelItemID: oldID, CanonicalItemID: newID, NodeType: "item", CollisionWithNodeType: "topic", Quarantined: true, Reason: "reserved_item_remap_collision"})
			continue
		}
		item.ID = newID
		item.ClientKey = ""
		used[newID] = struct{}{}
		remap[oldID] = newID
		kept = append(kept, item)
		recordItemIdentity(stats, itemIdentityEvaluation{ModelItemID: oldID, CanonicalItemID: newID, NodeType: "item", CollisionWithNodeType: "topic", Remapped: true, Reason: "reserved_item_id"})
	}
	state.Items = kept
	if len(remap) == 0 {
		return nil
	}
	remapLegacyDetailNodes(state.Tree, remap)
	for i := range state.EmergingTopics {
		for at, id := range state.EmergingTopics[i].EvidenceItemIDs {
			if canonical := remap[id]; canonical != "" {
				state.EmergingTopics[i].EvidenceItemIDs[at] = canonical
			}
		}
		state.EmergingTopics[i].EvidenceItemIDs = uniqueNonEmptyIDs(state.EmergingTopics[i].EvidenceItemIDs)
	}
	return remap
}

func remapLegacyDetailNodes(tree *liveAnalysisTree, remap map[string]string) {
	if tree == nil || len(remap) == 0 {
		return
	}
	for i := range tree.Nodes {
		node := &tree.Nodes[i]
		canonical := remap[node.ID]
		if canonical == "" || node.Kind == "topic" || node.Kind == "group" || node.ID == treeRootNodeID {
			continue
		}
		node.ID = canonical
		// A legacy item commonly had id=agenda-N and parentId=agenda-N.
		// After the detail ID is remapped, the parent remains the fixed topic.
	}
	for i := range tree.Edges {
		edge := &tree.Edges[i]
		if canonical := remap[edge.Target]; canonical != "" {
			edge.Target = canonical
		}
	}
}

func mergeIDRemaps(remaps ...map[string]string) map[string]string {
	merged := make(map[string]string)
	for _, remap := range remaps {
		for alias, canonical := range remap {
			merged[alias] = canonical
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func legacySemanticIdentityMatch(previous []liveAnalysisItem, candidate liveAnalysisItem) string {
	bestID, bestScore := "", 0.0
	for _, existing := range previous {
		sameKind := strings.EqualFold(existing.Kind, candidate.Kind)
		kindTransition := (existing.Kind == "todo" && candidate.Kind == "decision") || (existing.Kind == "decision" && candidate.Kind == "todo")
		if !sameKind && !kindTransition {
			continue
		}
		score := semanticItemSimilarity(existing.Title+" "+existing.Body, candidate.Title+" "+candidate.Body)
		if score > bestScore {
			bestID, bestScore = existing.ID, score
		}
	}
	if bestScore < 0.22 {
		return ""
	}
	return bestID
}
