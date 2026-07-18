package application

import (
	"strings"
	"unicode"
)

// canonicalReferenceKey compares model-produced IDs without changing an ID
// that has already been persisted. Models occasionally vary case or insert
// whitespace around hyphens between the item and a later reference to it.
// Those presentation differences must not split one canonical item.
func canonicalReferenceKey(value string) string {
	var b strings.Builder
	pendingSeparator := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if pendingSeparator && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingSeparator = false
			b.WriteRune(r)
			continue
		}
		pendingSeparator = true
	}
	return strings.Trim(b.String(), "-")
}

// canonicalNewItemID gives a not-yet-persisted model item a deterministic ID
// shape. Existing IDs are deliberately preserved and resolved through aliases
// so upgrading the backend never renames a node visible to clients.
func canonicalNewItemID(value string) string {
	return canonicalReferenceKey(value)
}

type canonicalReferenceResolver struct {
	byKey     map[string]string
	ambiguous map[string]struct{}
}

func newCanonicalReferenceResolver(ids ...string) *canonicalReferenceResolver {
	r := &canonicalReferenceResolver{
		byKey:     make(map[string]string),
		ambiguous: make(map[string]struct{}),
	}
	for _, id := range ids {
		r.add(id, id)
	}
	return r
}

func (r *canonicalReferenceResolver) add(alias, canonical string) {
	if r == nil {
		return
	}
	canonical = strings.TrimSpace(canonical)
	key := canonicalReferenceKey(alias)
	if key == "" || canonical == "" {
		return
	}
	if existing, ok := r.byKey[key]; ok && existing != canonical {
		delete(r.byKey, key)
		r.ambiguous[key] = struct{}{}
		return
	}
	if _, ambiguous := r.ambiguous[key]; ambiguous {
		return
	}
	r.byKey[key] = canonical
}

func (r *canonicalReferenceResolver) redirect(alias, canonical string) {
	if r == nil {
		return
	}
	alias = strings.TrimSpace(alias)
	canonical = strings.TrimSpace(canonical)
	if alias == "" || canonical == "" {
		return
	}
	for key, value := range r.byKey {
		if value == alias {
			r.byKey[key] = canonical
		}
	}
	key := canonicalReferenceKey(alias)
	delete(r.ambiguous, key)
	r.byKey[key] = canonical
	r.add(canonical, canonical)
}

func (r *canonicalReferenceResolver) resolve(value string) (canonical string, aliased bool, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" || r == nil {
		return "", false, false
	}
	key := canonicalReferenceKey(value)
	if _, ambiguous := r.ambiguous[key]; ambiguous {
		return "", false, false
	}
	canonical, ok = r.byKey[key]
	if !ok {
		return "", false, false
	}
	return canonical, canonical != value, true
}

func itemReferenceResolver(previous, diff []liveAnalysisItem, legacyAliases map[string]string, stats *liveAnalysisTreeMergeStats) *canonicalReferenceResolver {
	ids := make([]string, 0, len(previous)+len(diff))
	for _, item := range previous {
		ids = append(ids, item.ID)
	}
	r := newCanonicalReferenceResolver(ids...)
	for alias, canonical := range legacyAliases {
		r.add(alias, canonical)
	}
	for i := range diff {
		raw := modelItemReference(diff[i])
		if raw == "" {
			continue
		}
		if existing, _, ok := r.resolve(raw); ok {
			diff[i].ID = existing
			diff[i].ClientKey = ""
			r.add(raw, existing)
			continue
		}
		if reservedItemID(raw) {
			if existingID := legacySemanticIdentityMatch(previous, diff[i]); existingID != "" {
				diff[i].ID = existingID
				diff[i].ClientKey = ""
				r.add(raw, existingID)
				recordItemIdentity(stats, itemIdentityEvaluation{ModelItemID: raw, CanonicalItemID: existingID, NodeType: "item", CollisionWithNodeType: "topic", Remapped: true, Reason: "reserved_item_semantic_alias"})
				continue
			}
		}
		canonical := canonicalNewItemID(raw)
		reason := "legacy_model_item_id"
		remapped := false
		collisionType := ""
		if strings.TrimSpace(diff[i].ClientKey) != "" || reservedItemID(raw) {
			canonical = serverGeneratedItemID(diff[i])
			remapped = true
			reason = "model_client_key"
			if reservedItemID(raw) {
				reason = "reserved_item_id"
				collisionType = "topic"
			}
		}
		if canonical == "" {
			diff[i].ID = ""
			diff[i].ClientKey = ""
			recordItemIdentity(stats, itemIdentityEvaluation{ModelItemID: raw, NodeType: "item", Quarantined: true, Reason: "empty_canonical_item_id"})
			continue
		}
		diff[i].ID = canonical
		diff[i].ClientKey = ""
		r.add(raw, canonical)
		r.add(canonical, canonical)
		if remapped {
			recordItemIdentity(stats, itemIdentityEvaluation{ModelItemID: raw, CanonicalItemID: canonical, NodeType: "item", CollisionWithNodeType: collisionType, Remapped: true, Reason: reason})
		}
	}
	return r
}

func treeReferenceResolver(tree *liveAnalysisTree) *canonicalReferenceResolver {
	if tree == nil {
		return newCanonicalReferenceResolver()
	}
	ids := make([]string, 0, len(tree.Nodes))
	for _, node := range tree.Nodes {
		ids = append(ids, node.ID)
	}
	return newCanonicalReferenceResolver(ids...)
}
