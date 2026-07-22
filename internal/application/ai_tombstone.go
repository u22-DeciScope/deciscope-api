package application

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

const liveAnalysisItemTombstonesMaxCount = 200

// liveAnalysisItemTombstone is durable within one meeting's live snapshot.
// Text is represented by deterministic hashes; the retained inactive item
// remains available for audit history when semantic comparison is needed.
type liveAnalysisItemTombstone struct {
	CanonicalItemID     string   `json:"canonicalItemId"`
	PropositionKey      string   `json:"propositionKey,omitempty"`
	SemanticKeyHash     string   `json:"semanticKeyHash,omitempty"`
	EvidenceFingerprint string   `json:"evidenceFingerprint,omitempty"`
	CandidateAliases    []string `json:"candidateAliases,omitempty"`
	Reason              string   `json:"reason"`
	MergedIntoItemID    string   `json:"mergedIntoItemId,omitempty"`
	CreatedBy           string   `json:"createdBy"`
	CreatedAtVersion    int64    `json:"createdAtVersion"`
	SourceTreeVersion   int64    `json:"sourceTreeVersion,omitempty"`
	AuditRunID          string   `json:"auditRunId,omitempty"`
	ReopenedAtVersion   int64    `json:"reopenedAtVersion,omitempty"`
	ReopenReason        string   `json:"reopenReason,omitempty"`
}

type itemResurrectionPrevention struct {
	CanonicalItemID     string
	PropositionKeyHash  string
	TombstoneReason     string
	EvidenceSequenceNos []int64
}

func itemPropositionKey(item liveAnalysisItem) string {
	if strings.TrimSpace(item.PropositionKey) != "" {
		return strings.TrimSpace(item.PropositionKey)
	}
	core := semanticItemKey(item.Title + " " + item.Body)
	if core == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(core))
	return "prop-" + hex.EncodeToString(sum[:6])
}

func itemSemanticKeyHash(item liveAnalysisItem) string {
	key := semanticItemKey(item.Title + " " + item.Body)
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return "sem-" + hex.EncodeToString(sum[:8])
}

func itemEvidenceFingerprint(item liveAnalysisItem) string {
	if len(item.EvidenceSequenceNos) == 0 {
		return ""
	}
	values := append([]int64(nil), item.EvidenceSequenceNos...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	values = uniqueSortedSequenceNos(values)
	var b strings.Builder
	for _, value := range values {
		b.WriteString(fmt.Sprintf("%d,", value))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "evidence-" + hex.EncodeToString(sum[:8])
}

func uniqueSortedSequenceNos(values []int64) []int64 {
	kept := values[:0]
	var previous int64
	for _, value := range values {
		if value <= 0 || (len(kept) > 0 && value == previous) {
			continue
		}
		kept = append(kept, value)
		previous = value
	}
	return kept
}

func addItemTombstone(state *liveAnalysisPayload, item liveAnalysisItem, reason, mergedInto, createdBy, auditRunID string, sourceVersion, createdVersion int64, aliases ...string) {
	if state == nil || strings.TrimSpace(item.ID) == "" {
		return
	}
	reason = normalizeTombstoneReason(reason)
	aliases = uniqueNonEmptyIDs(append(aliases, item.CandidateTopicID))
	entry := liveAnalysisItemTombstone{
		CanonicalItemID: item.ID, PropositionKey: itemPropositionKey(item),
		SemanticKeyHash: itemSemanticKeyHash(item), EvidenceFingerprint: itemEvidenceFingerprint(item),
		CandidateAliases: aliases, Reason: reason, MergedIntoItemID: strings.TrimSpace(mergedInto),
		CreatedBy: firstNonEmptyTrimmed(createdBy, "server"), CreatedAtVersion: createdVersion,
		SourceTreeVersion: sourceVersion, AuditRunID: strings.TrimSpace(auditRunID),
	}
	for index := range state.ItemTombstones {
		current := &state.ItemTombstones[index]
		if current.CanonicalItemID != entry.CanonicalItemID &&
			(current.PropositionKey == "" || current.PropositionKey != entry.PropositionKey) &&
			(current.EvidenceFingerprint == "" || current.EvidenceFingerprint != entry.EvidenceFingerprint) {
			continue
		}
		entry.CandidateAliases = uniqueNonEmptyIDs(append(entry.CandidateAliases, current.CandidateAliases...))
		// Normal live repair revisits retained inactive items on every merge.
		// That is not a new deactivation event and must not replace the original
		// auditor provenance with live_repair/empty audit metadata. Only a real
		// deactivation after a persisted reopen starts a fresh tombstone cycle.
		newCycleAfterReopen := current.ReopenedAtVersion > current.CreatedAtVersion &&
			entry.CreatedAtVersion > current.ReopenedAtVersion
		if !newCycleAfterReopen {
			entry.CanonicalItemID = firstNonEmptyTrimmed(current.CanonicalItemID, entry.CanonicalItemID)
			entry.PropositionKey = firstNonEmptyTrimmed(current.PropositionKey, entry.PropositionKey)
			entry.SemanticKeyHash = firstNonEmptyTrimmed(current.SemanticKeyHash, entry.SemanticKeyHash)
			entry.EvidenceFingerprint = firstNonEmptyTrimmed(current.EvidenceFingerprint, entry.EvidenceFingerprint)
			entry.Reason = firstNonEmptyTrimmed(current.Reason, entry.Reason)
			entry.MergedIntoItemID = firstNonEmptyTrimmed(entry.MergedIntoItemID, current.MergedIntoItemID)
			entry.CreatedBy = firstNonEmptyTrimmed(current.CreatedBy, entry.CreatedBy)
			if current.CreatedAtVersion > 0 {
				entry.CreatedAtVersion = current.CreatedAtVersion
			}
			if current.SourceTreeVersion > 0 {
				entry.SourceTreeVersion = current.SourceTreeVersion
			}
			entry.AuditRunID = firstNonEmptyTrimmed(current.AuditRunID, entry.AuditRunID)
			entry.ReopenedAtVersion = current.ReopenedAtVersion
			entry.ReopenReason = current.ReopenReason
		}
		state.ItemTombstones[index] = entry
		return
	}
	state.ItemTombstones = append(state.ItemTombstones, entry)
	if len(state.ItemTombstones) > liveAnalysisItemTombstonesMaxCount {
		state.ItemTombstones = append([]liveAnalysisItemTombstone(nil), state.ItemTombstones[len(state.ItemTombstones)-liveAnalysisItemTombstonesMaxCount:]...)
	}
}

func normalizeTombstoneReason(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(reason, "merge") || strings.Contains(reason, "duplicate") || strings.Contains(reason, "重複"):
		return "merged"
	case strings.Contains(reason, "supersed") || strings.Contains(reason, "置換"):
		return "superseded"
	case strings.Contains(reason, "discourse") || strings.Contains(reason, "会話制御") || strings.Contains(reason, "談話"):
		return "discourse_only"
	case strings.Contains(reason, "recap") || strings.Contains(reason, "reference") || strings.Contains(reason, "まとめ"):
		return "recap_only"
	case strings.Contains(reason, "low") || strings.Contains(reason, "低情報"):
		return "low_information"
	case reason == "deactivated":
		return "deactivated"
	default:
		return firstNonEmptyTrimmed(reason, "deactivated")
	}
}

func ensureLegacyItemTombstones(state *liveAnalysisPayload) {
	if state == nil {
		return
	}
	for _, item := range state.Items {
		switch {
		case item.MergedIntoID != "":
			addItemTombstone(state, item, "merged", item.MergedIntoID, "legacy_payload", "", state.TreeVersion, state.TreeVersion)
		case item.Inactive:
			addItemTombstone(state, item, "deactivated", "", "legacy_payload", "", state.TreeVersion, state.TreeVersion)
		}
	}
}

func filterTombstoneResurrections(previous *liveAnalysisPayload, diff []liveAnalysisItem, assignments []treeAssignment, updates []resolutionUpdate, scope liveEvidenceScope, treeVersion int64, stats *liveAnalysisTreeMergeStats) ([]liveAnalysisItem, []treeAssignment) {
	if previous == nil {
		return diff, assignments
	}
	ensureLegacyItemTombstones(previous)
	if len(previous.ItemTombstones) == 0 || len(diff) == 0 {
		return diff, assignments
	}
	explicitReopen := explicitTombstoneReopenReferences(updates, scope)
	assignmentParent := make(map[string]string, len(assignments))
	for _, assignment := range assignments {
		assignmentParent[canonicalReferenceKey(assignment.nodeID())] = strings.TrimSpace(assignment.ParentTopicID)
	}
	blockedRefs := make(map[string]struct{})
	kept := make([]liveAnalysisItem, 0, len(diff))
	for index := range diff {
		item := diff[index]
		parentAlias := assignmentParent[canonicalReferenceKey(firstNonEmptyTrimmed(item.modelReference, item.ID))]
		tombstone := matchingItemTombstone(*previous, item, parentAlias)
		if tombstone == nil {
			kept = append(kept, item)
			continue
		}
		_, requestedReopen := explicitReopen[canonicalReferenceKey(item.ID)]
		if !requestedReopen {
			_, requestedReopen = explicitReopen[canonicalReferenceKey(item.modelReference)]
		}
		if legitimateTombstoneReopen(previous, tombstone, item, requestedReopen, scope) {
			item.reopenFromTombstone = true
			tombstone.ReopenedAtVersion = treeVersion
			tombstone.ReopenReason = tombstoneReopenReason(tombstone, item, requestedReopen, scope)
			kept = append(kept, item)
			continue
		}
		blockedRefs[canonicalReferenceKey(item.ID)] = struct{}{}
		blockedRefs[canonicalReferenceKey(item.modelReference)] = struct{}{}
		if stats != nil {
			stats.ItemResurrectionPrevented++
			stats.ResurrectionPreventions = append(stats.ResurrectionPreventions, itemResurrectionPrevention{
				CanonicalItemID: item.ID, PropositionKeyHash: tombstoneLogHash(firstNonEmptyTrimmed(tombstone.PropositionKey, tombstone.SemanticKeyHash)),
				TombstoneReason: tombstone.Reason, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...),
			})
		}
	}
	if len(blockedRefs) == 0 {
		return kept, assignments
	}
	keptAssignments := assignments[:0]
	for _, assignment := range assignments {
		if _, blocked := blockedRefs[canonicalReferenceKey(assignment.nodeID())]; blocked {
			continue
		}
		keptAssignments = append(keptAssignments, assignment)
	}
	return kept, keptAssignments
}

func tombstoneLogHash(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}

func matchingItemTombstone(state liveAnalysisPayload, item liveAnalysisItem, candidateAlias string) *liveAnalysisItemTombstone {
	propositionKey := itemPropositionKey(item)
	semanticHash := itemSemanticKeyHash(item)
	evidenceFingerprint := itemEvidenceFingerprint(item)
	for index := range state.ItemTombstones {
		tombstone := &state.ItemTombstones[index]
		if tombstone.ReopenedAtVersion > tombstone.CreatedAtVersion {
			continue
		}
		if item.ID != "" && item.ID == tombstone.CanonicalItemID {
			return tombstone
		}
		if propositionKey != "" && propositionKey == tombstone.PropositionKey {
			return tombstone
		}
		if semanticHash != "" && semanticHash == tombstone.SemanticKeyHash {
			return tombstone
		}
		if evidenceFingerprint != "" && evidenceFingerprint == tombstone.EvidenceFingerprint {
			return tombstone
		}
		if candidateAlias != "" && containsExactString(tombstone.CandidateAliases, candidateAlias) && tombstoneSemanticallyMatches(state, *tombstone, item) {
			return tombstone
		}
		if tombstoneSemanticallyMatches(state, *tombstone, item) {
			return tombstone
		}
	}
	return nil
}

func tombstoneSemanticallyMatches(state liveAnalysisPayload, tombstone liveAnalysisItemTombstone, item liveAnalysisItem) bool {
	for _, previousItem := range state.Items {
		if previousItem.ID != tombstone.CanonicalItemID {
			continue
		}
		score := semanticItemSimilarity(previousItem.Title+" "+previousItem.Body, item.Title+" "+item.Body)
		return score >= 0.72 || (score >= 0.30 && sharedTreeAuditSubjectTerm(previousItem.Title+" "+previousItem.Body, item.Title+" "+item.Body))
	}
	return false
}

func explicitTombstoneReopenReferences(updates []resolutionUpdate, scope liveEvidenceScope) map[string]struct{} {
	refs := make(map[string]struct{})
	for _, update := range updates {
		if strings.ToLower(strings.TrimSpace(update.Status)) != "open" {
			continue
		}
		explicit := resolutionOpenPattern.MatchString(update.Reason)
		for _, sequenceNo := range update.EvidenceSequenceNos {
			text := scope.TranscriptText[sequenceNo]
			if resolutionOpenPattern.MatchString(text) || discourseCorrectionPattern.MatchString(text) || strings.Contains(text, "再開") || strings.Contains(text, "再オープン") {
				explicit = true
			}
		}
		if explicit {
			refs[canonicalReferenceKey(update.ItemID)] = struct{}{}
		}
	}
	return refs
}

func legitimateTombstoneReopen(state *liveAnalysisPayload, tombstone *liveAnalysisItemTombstone, item liveAnalysisItem, explicit bool, scope liveEvidenceScope) bool {
	if explicit {
		return true
	}
	if tombstone.MergedIntoItemID != "" {
		for _, target := range state.Items {
			if target.ID == tombstone.MergedIntoItemID && (target.Inactive || target.MergedIntoID != "") {
				return true
			}
		}
	}
	if itemEvidenceFingerprint(item) == tombstone.EvidenceFingerprint {
		return false
	}
	for _, previousItem := range state.Items {
		if previousItem.ID != tombstone.CanonicalItemID {
			continue
		}
		oldText, newText := previousItem.Title+" "+previousItem.Body, item.Title+" "+item.Body
		if numbers := numericSignature(newText); numbers != "" && numbers != numericSignature(oldText) {
			return true
		}
		if (!lowInformationAssigneePattern.MatchString(oldText) && lowInformationAssigneePattern.MatchString(newText)) ||
			(!lowInformationDeadlinePattern.MatchString(oldText) && lowInformationDeadlinePattern.MatchString(newText)) {
			return true
		}
		if semanticItemSimilarity(oldText, newText) < 0.30 && liveItemHasSpecificSubject(newText) {
			return true
		}
	}
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if discourseCorrectionPattern.MatchString(scope.TranscriptText[sequenceNo]) {
			return true
		}
	}
	return false
}

func tombstoneReopenReason(tombstone *liveAnalysisItemTombstone, item liveAnalysisItem, explicit bool, scope liveEvidenceScope) string {
	if explicit {
		return "explicit_reopen"
	}
	if tombstone.MergedIntoItemID != "" {
		return "merged_target_inactive"
	}
	for _, sequenceNo := range item.EvidenceSequenceNos {
		if discourseCorrectionPattern.MatchString(scope.TranscriptText[sequenceNo]) {
			return "corrected_proposition"
		}
	}
	return "material_new_information"
}
