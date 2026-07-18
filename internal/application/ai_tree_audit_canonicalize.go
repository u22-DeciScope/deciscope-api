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

		updated.TargetCanonicalItemID = resolveField(operation.TargetCanonicalItemID, spaces.resolveItemField)
		updated.TargetCanonicalNodeID = resolveField(operation.TargetCanonicalNodeID, func(raw string) (string, bool, string) {
			return spaces.resolveNodeField(raw, true)
		})
		updated.FromParentCanonicalNodeID = resolveField(operation.FromParentCanonicalNodeID, func(raw string) (string, bool, string) {
			return spaces.resolveNodeField(raw, false)
		})
		updated.ToParentCanonicalNodeID = resolveField(operation.ToParentCanonicalNodeID, func(raw string) (string, bool, string) {
			return spaces.resolveNodeField(raw, false)
		})
		updated.TargetCandidateID = resolveField(operation.TargetCandidateID, spaces.resolveCandidateField)

		if rejected == "" && len(operation.TargetCanonicalItemIDs) > 0 {
			resolvedIDs := make([]string, len(operation.TargetCanonicalItemIDs))
			for index, raw := range operation.TargetCanonicalItemIDs {
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
}
