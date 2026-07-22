package application

import "strings"

const (
	issueSubtypeDiscussion    = "discussion"
	issueSubtypeConfirmation  = "confirmation"
	issueSubtypeQuestion      = "question"
	issueSubtypeInvestigation = "investigation"

	informationStatusGrounded  = "grounded"
	informationStatusTentative = "tentative"
)

func validIssueSubtype(value string) bool {
	switch value {
	case issueSubtypeDiscussion, issueSubtypeConfirmation, issueSubtypeQuestion, issueSubtypeInvestigation:
		return true
	default:
		return false
	}
}

// normalizeSemanticClassification upgrades the historical wire vocabulary to
// the canonical three-axis model: structural role lives on the tree, semantic
// kind is issue/risk/todo/decision/fact, and open/resolved remains a status.
// The boolean reports whether a legacy classification was migrated.
func normalizeSemanticClassification(kind, subtype, status string) (string, string, string, bool) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	subtype = strings.ToLower(strings.TrimSpace(subtype))
	status = strings.ToLower(strings.TrimSpace(status))
	originalKind, originalSubtype, originalStatus := kind, subtype, status

	switch kind {
	case "open_issue":
		kind = "issue"
		if subtype == "" {
			subtype = issueSubtypeDiscussion
		}
		if status == "" {
			status = "open"
		}
	case "confirmation":
		kind, subtype = "issue", issueSubtypeConfirmation
	case "question":
		kind, subtype = "issue", issueSubtypeQuestion
	case "investigation":
		kind, subtype = "issue", issueSubtypeInvestigation
	case "resolved":
		// A small number of historical/model payloads used the state label as
		// a kind. Preserve the proposition as an issue and move the state to
		// the existing status field instead of dropping the record.
		kind, subtype, status = "issue", issueSubtypeDiscussion, "resolved"
	case "issue":
		if !validIssueSubtype(subtype) {
			subtype = issueSubtypeDiscussion
		}
	default:
		subtype = ""
	}
	if kind == "issue" && !validIssueSubtype(subtype) {
		subtype = issueSubtypeDiscussion
	}
	if kind == "issue" && status == "" {
		status = "open"
	}
	return kind, subtype, status, kind != originalKind || subtype != originalSubtype || status != originalStatus
}

func normalizePersistedSemanticClassifications(state *liveAnalysisPayload) int {
	if state == nil {
		return 0
	}
	migrated := 0
	itemSubtype := make(map[string]string, len(state.Items))
	for i := range state.Items {
		kind, subtype, status, changed := normalizeSemanticClassification(state.Items[i].Kind, state.Items[i].Subtype, state.Items[i].Status)
		state.Items[i].Kind, state.Items[i].Subtype, state.Items[i].Status = kind, subtype, status
		if state.Items[i].InformationStatus == "" {
			state.Items[i].InformationStatus = informationStatusGrounded
		}
		itemSubtype[state.Items[i].ID] = subtype
		if changed {
			migrated++
		}
	}
	if state.Tree == nil {
		return migrated
	}
	for i := range state.Tree.Nodes {
		node := &state.Tree.Nodes[i]
		if node.Kind == "topic" || node.Kind == "group" {
			continue
		}
		kind, subtype, status, changed := normalizeSemanticClassification(node.Kind, node.Subtype, node.Status)
		if subtype == issueSubtypeDiscussion && itemSubtype[node.ID] != "" {
			subtype = itemSubtype[node.ID]
		}
		node.Kind, node.Subtype, node.Status = kind, subtype, status
		if changed {
			migrated++
		}
	}
	return migrated
}

func sameSemanticClassification(a, b liveAnalysisItem) bool {
	if !strings.EqualFold(strings.TrimSpace(a.Kind), strings.TrimSpace(b.Kind)) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(a.Kind), "issue") {
		return strings.EqualFold(strings.TrimSpace(a.Subtype), strings.TrimSpace(b.Subtype))
	}
	return true
}
