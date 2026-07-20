package application

import "strings"

// treeAuditCanonicalIDSpaces indexes the live tree's canonical ID spaces
// (tree nodes, detail items, and not-yet-promoted candidates) plus the
// item-side ClientKey alias table, so operation and finding ID fields coming
// back from the model can be resolved to canonical IDs before validation.
//
// Detail items are also tree nodes (an item and its node share the same ID),
// so the node space is checked first: a promoted candidate keeps its
// candidate ID as its node ID, and promotion removes the candidate from
// EmergingTopics tracking, so this ordering alone gives promoted candidates
// node-space priority without any special-casing.
type treeAuditCanonicalIDSpaces struct {
	nodeIDs        map[string]struct{}
	itemIDs        map[string]struct{}
	candidateIDs   map[string]struct{}
	clientKeyItems map[string][]string
}

func buildTreeAuditCanonicalIDSpaces(state liveAnalysisPayload) treeAuditCanonicalIDSpaces {
	spaces := treeAuditCanonicalIDSpaces{
		nodeIDs:        make(map[string]struct{}),
		itemIDs:        make(map[string]struct{}, len(state.Items)),
		candidateIDs:   make(map[string]struct{}, len(state.EmergingTopics)),
		clientKeyItems: make(map[string][]string),
	}
	if state.Tree != nil {
		for _, node := range state.Tree.Nodes {
			spaces.nodeIDs[node.ID] = struct{}{}
		}
	}
	for _, item := range state.Items {
		spaces.itemIDs[item.ID] = struct{}{}
		if key := strings.TrimSpace(item.ClientKey); key != "" {
			spaces.clientKeyItems[key] = append(spaces.clientKeyItems[key], item.ID)
		}
	}
	for _, candidate := range state.EmergingTopics {
		spaces.candidateIDs[candidate.ID] = struct{}{}
	}
	return spaces
}

// treeAuditCanonicalResolution is the outcome of resolving a raw ID-shaped
// alias against the canonical ID spaces, before any field-context (item vs.
// node vs. candidate) check is applied.
type treeAuditCanonicalResolution struct {
	id         string
	ambiguous  bool
	unresolved bool
}

// resolve implements steps 1-3 and 5-6 of the canonicalization order: exact
// match against node, item, then candidate IDs (in that order, so a
// promoted candidate resolves as a node); failing that, a unique item
// ClientKey alias. A blank (post-trim) value is not a reference at all and
// resolves to itself.
func (spaces treeAuditCanonicalIDSpaces) resolve(raw string) treeAuditCanonicalResolution {
	id := strings.TrimSpace(raw)
	if id == "" {
		return treeAuditCanonicalResolution{}
	}
	if _, ok := spaces.nodeIDs[id]; ok {
		return treeAuditCanonicalResolution{id: id}
	}
	if _, ok := spaces.itemIDs[id]; ok {
		return treeAuditCanonicalResolution{id: id}
	}
	if _, ok := spaces.candidateIDs[id]; ok {
		return treeAuditCanonicalResolution{id: id}
	}
	if matches, ok := spaces.clientKeyItems[id]; ok {
		if len(matches) == 1 {
			return treeAuditCanonicalResolution{id: matches[0]}
		}
		return treeAuditCanonicalResolution{ambiguous: true}
	}
	return treeAuditCanonicalResolution{unresolved: true}
}

// resolveItemField resolves raw as a reference to a detail item. It returns
// the canonical ID (unchanged/empty when raw was blank), whether the value
// actually changed, and a rejection reason ("" when the field resolved
// cleanly).
func (spaces treeAuditCanonicalIDSpaces) resolveItemField(raw string) (id string, changed bool, reason string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false, ""
	}
	resolution := spaces.resolve(trimmed)
	switch {
	case resolution.ambiguous:
		return raw, false, "ambiguous_alias"
	case resolution.unresolved:
		return raw, false, "unresolved_canonical_id"
	}
	if _, ok := spaces.itemIDs[resolution.id]; !ok {
		// Resolved to a real node or candidate, but not a detail item.
		return raw, false, "target_not_item"
	}
	return resolution.id, resolution.id != trimmed, ""
}

// resolveNodeField resolves raw as a reference to a tree node
// (FromParentCanonicalNodeID/ToParentCanonicalNodeID/TargetCanonicalNodeID).
// An unpromoted candidate ID used in a node context cannot resolve (design
// rule 4): it is reported as unresolved_canonical_id, not as a candidate.
// When requireContainer is set (TargetCanonicalNodeID, the move_node/
// rename_group/remove_empty_group target field), a resolved detail-item
// node is rejected as target_not_node; parent fields leave the topic/group
// Kind check to the existing validator logic.
func (spaces treeAuditCanonicalIDSpaces) resolveNodeField(raw string, requireContainer bool) (id string, changed bool, reason string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false, ""
	}
	resolution := spaces.resolve(trimmed)
	switch {
	case resolution.ambiguous:
		return raw, false, "ambiguous_alias"
	case resolution.unresolved:
		return raw, false, "unresolved_canonical_id"
	}
	if _, ok := spaces.nodeIDs[resolution.id]; !ok {
		// Resolved to a still-unpromoted candidate: not usable as a node yet.
		return raw, false, "unresolved_canonical_id"
	}
	if requireContainer {
		if _, isItem := spaces.itemIDs[resolution.id]; isItem {
			return raw, false, "target_not_node"
		}
	}
	return resolution.id, resolution.id != trimmed, ""
}

// resolveCandidateField resolves raw as a reference to a not-yet-promoted
// emerging topic candidate (TargetCandidateID).
func (spaces treeAuditCanonicalIDSpaces) resolveCandidateField(raw string) (id string, changed bool, reason string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false, ""
	}
	resolution := spaces.resolve(trimmed)
	switch {
	case resolution.ambiguous:
		return raw, false, "ambiguous_alias"
	case resolution.unresolved:
		return raw, false, "unresolved_canonical_id"
	}
	if _, ok := spaces.candidateIDs[resolution.id]; !ok {
		// Resolved to a node (already promoted) or an item, not a live
		// candidate; from a candidate-field's perspective that alias does
		// not resolve.
		return raw, false, "unresolved_canonical_id"
	}
	return resolution.id, resolution.id != trimmed, ""
}

// resolveAnyField resolves raw as a finding reference, which may point at a
// node, an item, or a candidate interchangeably.
func (spaces treeAuditCanonicalIDSpaces) resolveAnyField(raw string) (id string, changed bool, reason string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false, ""
	}
	resolution := spaces.resolve(trimmed)
	switch {
	case resolution.ambiguous:
		return raw, false, "ambiguous_alias"
	case resolution.unresolved:
		return raw, false, "unresolved_canonical_id"
	}
	return resolution.id, resolution.id != trimmed, ""
}

// normalizeTreeAuditOperationFields zeroes (or, for two narrow rescues,
// migrates) any ID-shaped field of operation that its own applier
// (applyOneTreeAuditOperation) never reads for operation.Type, before the
// per-field canonical-ID resolution in canonicalizeTreeAuditResponse runs.
//
// Every operation type exposes the full v3 schema's ID fields
// (TargetCanonicalItemID, TargetCanonicalNodeID, TargetCanonicalItemIDs,
// TargetCandidateID, FromParentCanonicalNodeID, ToParentCanonicalNodeID)
// even though a given operation type only ever consumes a subset of them.
// Before this normalization pass existed, every field was resolved
// unconditionally regardless of operation type, so a stray value the model
// left in a field it does not use for this operation - most often by
// redundantly repeating the same ID it already supplied correctly in the
// field it does use - could still fail that field's own context check
// (typically target_not_node, since TargetCanonicalNodeID is resolved with
// requireContainer=true) and discard the whole operation, even though every
// field the applier actually reads was correct. This mirrors an anomaly
// observed in a real production run: a move_item operation correctly set
// targetCanonicalItemId to a detail item, but the model also copied that
// same item ID into the unused targetCanonicalNodeId field, and the old
// unconditional resolution rejected the entire operation on
// target_not_node for a field move_item never reads in the first place.
//
// Two operation types get a narrow rescue instead of a plain clear, applied
// before the field is zeroed:
//   - merge_items: if the model put its one non-first merge target in the
//     singular TargetCanonicalItemID field alongside a single-element
//     TargetCanonicalItemIDs, the two are combined into a two-element
//     TargetCanonicalItemIDs before TargetCanonicalItemID is cleared.
//   - rewrite_item/rewrite_item_title/rewrite_item_description/
//     reclassify_kind/reclassify_subtype/deactivate_item/
//     change_evidence_role: if TargetCanonicalItemID is blank but
//     TargetCanonicalNodeID resolves to a detail item, that value is moved
//     over before TargetCanonicalNodeID is cleared.
//   - fold_candidate_into_topic: applyOneTreeAuditOperation and
//     treeAuditEffectiveConfidence both read ToParentCanonicalNodeID (not
//     TargetCanonicalNodeID) as the destination topic; if the model put the
//     destination in TargetCanonicalNodeID instead and left
//     ToParentCanonicalNodeID blank, the value is copied over before
//     TargetCanonicalNodeID is cleared.
//
// Every clear or rescue that actually changes a field increments *changed,
// the same counter canonicalizeTreeAuditResponse's per-operation field
// resolution loop uses toward response.CanonicalizationCount - this pass
// never rejects an operation by itself, it only records that a
// normalization happened. It never touches Label, Kind, Subtype, Reason,
// Confidence, EvidenceSequenceNos, or DependsOnOperationIDs.
func normalizeTreeAuditOperationFields(operation *treeAuditOperation, spaces treeAuditCanonicalIDSpaces, changed *int) {
	clearItemID := func() {
		if strings.TrimSpace(operation.TargetCanonicalItemID) != "" {
			operation.TargetCanonicalItemID = ""
			*changed++
		}
	}
	clearNodeID := func() {
		if strings.TrimSpace(operation.TargetCanonicalNodeID) != "" {
			operation.TargetCanonicalNodeID = ""
			*changed++
		}
	}
	clearItemIDs := func() {
		if len(operation.TargetCanonicalItemIDs) != 0 {
			operation.TargetCanonicalItemIDs = nil
			*changed++
		}
	}
	clearCandidateID := func() {
		if strings.TrimSpace(operation.TargetCandidateID) != "" {
			operation.TargetCandidateID = ""
			*changed++
		}
	}
	clearFrom := func() {
		if strings.TrimSpace(operation.FromParentCanonicalNodeID) != "" {
			operation.FromParentCanonicalNodeID = ""
			*changed++
		}
	}
	clearTo := func() {
		if strings.TrimSpace(operation.ToParentCanonicalNodeID) != "" {
			operation.ToParentCanonicalNodeID = ""
			*changed++
		}
	}

	switch operation.Type {
	case TreeAuditMoveItem, TreeAuditRestorePreviousParent:
		// Used: TargetCanonicalItemID, FromParentCanonicalNodeID, ToParentCanonicalNodeID.
		clearNodeID()
		clearItemIDs()
		clearCandidateID()

	case TreeAuditMoveNode:
		// Used: TargetCanonicalNodeID, FromParentCanonicalNodeID, ToParentCanonicalNodeID.
		clearItemID()
		clearItemIDs()
		clearCandidateID()

	case TreeAuditMergeItems:
		// Used: TargetCanonicalItemIDs. Rescue: a lone second target left in
		// the singular field is folded into the plural one first.
		if len(operation.TargetCanonicalItemIDs) == 1 {
			solo := strings.TrimSpace(operation.TargetCanonicalItemID)
			first := strings.TrimSpace(operation.TargetCanonicalItemIDs[0])
			if solo != "" && solo != first {
				operation.TargetCanonicalItemIDs = []string{operation.TargetCanonicalItemID, operation.TargetCanonicalItemIDs[0]}
				*changed++
			}
		}
		clearItemID()
		clearNodeID()
		clearCandidateID()
		clearFrom()
		clearTo()

	case TreeAuditRewriteItem, TreeAuditRewriteItemTitle, TreeAuditRewriteItemDescription,
		TreeAuditReclassifyKind, TreeAuditReclassifySubtype, TreeAuditDeactivateItem, TreeAuditChangeEvidenceRole:
		// Used: TargetCanonicalItemID (+Label/Kind/Subtype). Rescue: an item
		// ID left in TargetCanonicalNodeID is moved over when the item field
		// itself is blank.
		if strings.TrimSpace(operation.TargetCanonicalItemID) == "" {
			if raw := strings.TrimSpace(operation.TargetCanonicalNodeID); raw != "" {
				if resolvedID, _, reason := spaces.resolveItemField(raw); reason == "" {
					operation.TargetCanonicalItemID = resolvedID
					*changed++
				}
			}
		}
		clearNodeID()
		clearItemIDs()
		clearCandidateID()
		clearFrom()
		clearTo()

	case TreeAuditAssignItemToCandidate:
		// Used: TargetCanonicalItemID, TargetCandidateID.
		clearNodeID()
		clearItemIDs()
		clearFrom()
		clearTo()

	case TreeAuditFoldCandidateIntoTopic:
		// Used: TargetCandidateID, ToParentCanonicalNodeID (the fold
		// destination topic - both applyOneTreeAuditOperation and
		// treeAuditEffectiveConfidence read ToParentCanonicalNodeID, never
		// TargetCanonicalNodeID). TargetCanonicalItemIDs is kept: both the
		// applier and treeAuditEffectiveConfidence read it (falling back to
		// the candidate's own EvidenceItemIDs when empty). Rescue: a
		// destination left in TargetCanonicalNodeID is copied over when
		// ToParentCanonicalNodeID itself is blank.
		if strings.TrimSpace(operation.ToParentCanonicalNodeID) == "" {
			if raw := strings.TrimSpace(operation.TargetCanonicalNodeID); raw != "" {
				operation.ToParentCanonicalNodeID = operation.TargetCanonicalNodeID
				*changed++
			}
		}
		clearNodeID()
		clearItemID()
		clearFrom()

	case TreeAuditCreateTopicFromCandidate, TreeAuditDeactivateCandidate:
		// Used: TargetCandidateID.
		clearItemID()
		clearNodeID()
		clearItemIDs()
		clearFrom()
		clearTo()

	case TreeAuditRenameGroup, TreeAuditRemoveEmptyGroup:
		// Used: TargetCanonicalNodeID (+Label for rename_group).
		clearItemID()
		clearItemIDs()
		clearCandidateID()
		clearFrom()
		clearTo()
	}
}

// canonicalizeTreeAuditResponse resolves every ID-shaped field in the
// parsed response's findings and operations against the live tree's
// canonical ID spaces. It runs after parseTreeAuditResponse (which only
// checks schema/type/confidence/duplicate-ID/dependency invariants) and
// before validateAndDryRunTreeAuditOperations.
//
// A finding with any unresolved/ambiguous reference is dropped entirely
// (matching the previous parser's all-or-nothing per-finding behavior). An
// operation with any unresolved/ambiguous/mismatched-type ID field is
// dropped, but the rest of the response's operations are still evaluated -
// one bad alias never discards the whole audit. Every drop is recorded in
// response.ParseRejections so the existing "parser_"-prefixed aggregation in
// runTreeAudit still surfaces it in logs/validator_result.
func canonicalizeTreeAuditResponse(response *treeAuditResponse, state liveAnalysisPayload) {
	if response == nil {
		return
	}
	spaces := buildTreeAuditCanonicalIDSpaces(state)
	reject := func(elementType, elementID, reason string) {
		response.ParseRejections = append(response.ParseRejections, treeAuditParseRejection{ElementType: elementType, ElementID: elementID, Reason: reason})
	}

	validFindings := make([]treeAuditFinding, 0, len(response.Findings))
	for _, finding := range response.Findings {
		updated := finding
		localCount := 0
		failReason := ""
		rewriteList := func(ids []string) []string {
			if failReason != "" || len(ids) == 0 {
				return ids
			}
			result := make([]string, len(ids))
			for index, raw := range ids {
				resolvedID, changed, reason := spaces.resolveAnyField(raw)
				if reason != "" {
					failReason = reason
					return ids
				}
				if changed {
					localCount++
				}
				result[index] = resolvedID
			}
			return result
		}
		updated.NodeIDs = rewriteList(finding.NodeIDs)
		updated.CurrentParentIDs = rewriteList(finding.CurrentParentIDs)
		updated.RelatedNodeIDs = rewriteList(finding.RelatedNodeIDs)
		if failReason != "" {
			reject("finding", finding.FindingID, failReason)
			continue
		}
		response.CanonicalizationCount += localCount
		validFindings = append(validFindings, updated)
	}
	response.Findings = validFindings

	validOperations := make([]treeAuditOperation, 0, len(response.Operations))
	for _, operation := range response.Operations {
		updated := operation
		localCount := 0
		rejected := ""

		// Clear/migrate whatever ID fields updated.Type's own applier never
		// reads before resolving the fields it does read below, so a stray
		// value in an unused field can no longer fail its own field-context
		// check and discard an otherwise-valid operation. See
		// normalizeTreeAuditOperationFields.
		normalizeTreeAuditOperationFields(&updated, spaces, &localCount)

		resolveField := func(raw string, resolver func(string) (string, bool, string)) string {
			if rejected != "" {
				return raw
			}
			resolvedID, changed, reason := resolver(raw)
			if reason != "" {
				rejected = reason
				return raw
			}
			if changed {
				localCount++
			}
			return resolvedID
		}

		updated.TargetCanonicalItemID = resolveField(updated.TargetCanonicalItemID, spaces.resolveItemField)
		updated.TargetCanonicalNodeID = resolveField(updated.TargetCanonicalNodeID, func(raw string) (string, bool, string) {
			return spaces.resolveNodeField(raw, true)
		})
		updated.FromParentCanonicalNodeID = resolveField(updated.FromParentCanonicalNodeID, func(raw string) (string, bool, string) {
			return spaces.resolveNodeField(raw, false)
		})
		updated.ToParentCanonicalNodeID = resolveField(updated.ToParentCanonicalNodeID, func(raw string) (string, bool, string) {
			return spaces.resolveNodeField(raw, false)
		})
		updated.TargetCandidateID = resolveField(updated.TargetCandidateID, spaces.resolveCandidateField)

		if rejected == "" && len(updated.TargetCanonicalItemIDs) > 0 {
			resolvedIDs := make([]string, len(updated.TargetCanonicalItemIDs))
			for index, raw := range updated.TargetCanonicalItemIDs {
				resolvedID, changed, reason := spaces.resolveItemField(raw)
				if reason != "" {
					rejected = reason
					break
				}
				if changed {
					localCount++
				}
				resolvedIDs[index] = resolvedID
			}
			if rejected == "" {
				updated.TargetCanonicalItemIDs = resolvedIDs
			}
		}

		if rejected != "" {
			reject("operation", operation.OperationID, rejected)
			continue
		}
		response.CanonicalizationCount += localCount
		validOperations = append(validOperations, updated)
	}
	response.Operations = validOperations
	response.CanonicalizedOperationCount = len(validOperations)
}
