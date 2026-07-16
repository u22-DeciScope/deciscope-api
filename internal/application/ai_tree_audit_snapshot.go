package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"deciscope-core-api/internal/domain"
)

type treeAuditSnapshot struct {
	SessionID                 string                       `json:"sessionId"`
	TreeVersion               int64                        `json:"treeVersion"`
	CoverageThroughSequenceNo int64                        `json:"coverageThroughSequenceNo"`
	Nodes                     []treeAuditSnapshotNode      `json:"nodes"`
	Candidates                []treeAuditSnapshotCandidate `json:"candidates"`
	RecentTreeChanges         *liveAnalysisTreeChanges     `json:"recentTreeChanges,omitempty"`
	PrecheckFindings          []treeAuditPrecheckFinding   `json:"precheckFindings"`
	EvidenceSegments          []treeAuditEvidenceSegment   `json:"evidenceSegments"`
	RecentTranscript          []treeAuditEvidenceSegment   `json:"recentTranscript"`
	Compressed                bool                         `json:"compressed"`
}

type treeAuditSnapshotNode struct {
	ID                       string                 `json:"id"`
	Kind                     string                 `json:"kind"`
	Title                    string                 `json:"title"`
	Description              string                 `json:"description,omitempty"`
	ParentID                 string                 `json:"parentId"`
	Fixed                    bool                   `json:"fixed"`
	Dynamic                  bool                   `json:"dynamic"`
	Tentative                bool                   `json:"tentative"`
	Status                   string                 `json:"status,omitempty"`
	ClassificationConfidence float64                `json:"classificationConfidence,omitempty"`
	AssignmentReason         string                 `json:"assignmentReason,omitempty"`
	CandidateTopicID         string                 `json:"candidateTopicId,omitempty"`
	EvidenceSequenceNos      []int64                `json:"evidenceSequenceNos,omitempty"`
	EvidenceRoles            []treeAuditEvidenceRef `json:"evidenceRoles,omitempty"`
}

type treeAuditEvidenceRef struct {
	SequenceNo int64                 `json:"sequenceNo"`
	Role       treeAuditEvidenceRole `json:"role"`
}

type treeAuditSnapshotCandidate struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Description     string   `json:"description,omitempty"`
	EvidenceItemIDs []string `json:"evidenceItemIds"`
	FirstVersion    int64    `json:"firstVersion"`
	LastVersion     int64    `json:"lastVersion"`
	RoundCount      int      `json:"roundCount"`
	Inactive        bool     `json:"inactive"`
}

type treeAuditSnapshotBuild struct {
	Snapshot      treeAuditSnapshot
	Hash          string
	EvidenceRoles map[int64]treeAuditEvidenceRole
	InputJSON     json.RawMessage
}

func buildTreeAuditSnapshot(sessionID string, payload json.RawMessage, segments []domain.TranscriptSegment, mc *meetingContext, cfg TreeAuditConfig) (treeAuditSnapshotBuild, error) {
	cfg = cfg.normalized()
	state := previousLiveAnalysisState(payload)
	if state.Tree == nil || len(state.Tree.Nodes) == 0 || state.TreeVersion <= 0 {
		return treeAuditSnapshotBuild{}, fmt.Errorf("tree audit snapshot requires a non-empty versioned tree")
	}
	roles := classifyTreeAuditEvidence(state, segments)
	precheck := deterministicTreeAuditPrecheck(state, mc, roles, cfg)
	if len(precheck) > 100 {
		precheck = precheck[:100]
	}
	selected, compressed := selectTreeAuditNodes(state, precheck, cfg.MaxNodes)
	items := make(map[string]liveAnalysisItem, len(state.Items))
	for _, item := range state.Items {
		items[item.ID] = item
	}
	nodes := make([]treeAuditSnapshotNode, 0, len(selected))
	for _, node := range selected {
		item := items[node.ID]
		refs := make([]treeAuditEvidenceRef, 0, len(item.EvidenceSequenceNos))
		for _, sequenceNo := range item.EvidenceSequenceNos {
			refs = append(refs, treeAuditEvidenceRef{SequenceNo: sequenceNo, Role: roles[sequenceNo]})
		}
		nodes = append(nodes, treeAuditSnapshotNode{
			ID: node.ID, Kind: node.Kind, Title: node.Label,
			Description:              firstNonEmptyTrimmed(node.Description, item.Body),
			ParentID:                 node.ParentID,
			Fixed:                    node.ID == treeRootNodeID || node.Origin == topicOriginAgenda,
			Dynamic:                  node.Origin == topicOriginDynamic,
			Tentative:                item.ClassificationStatus == classificationTentative,
			Status:                   firstNonEmptyTrimmed(node.Status, item.Status),
			ClassificationConfidence: item.AssignmentConfidence,
			AssignmentReason:         item.AssignmentReason,
			CandidateTopicID:         item.CandidateTopicID,
			EvidenceSequenceNos:      append([]int64(nil), item.EvidenceSequenceNos...),
			EvidenceRoles:            refs,
		})
	}
	candidates := make([]treeAuditSnapshotCandidate, 0, len(state.EmergingTopics))
	for _, candidate := range state.EmergingTopics {
		candidates = append(candidates, treeAuditSnapshotCandidate{
			ID: candidate.ID, Title: candidate.Label, Description: candidate.Description,
			EvidenceItemIDs: append([]string(nil), candidate.EvidenceItemIDs...),
			FirstVersion:    candidate.FirstRound, LastVersion: candidate.LastRound,
			RoundCount: candidate.RoundCount, Inactive: candidate.Inactive,
		})
	}
	evidence, recent := selectTreeAuditTranscript(state, segments, roles, precheck, cfg)
	snapshot := treeAuditSnapshot{
		SessionID: sessionID, TreeVersion: state.TreeVersion,
		CoverageThroughSequenceNo: state.CoveredThroughSequenceNo,
		Nodes:                     nodes, Candidates: candidates, RecentTreeChanges: state.TreeChanges,
		PrecheckFindings: precheck, EvidenceSegments: evidence,
		RecentTranscript: recent, Compressed: compressed,
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return treeAuditSnapshotBuild{}, fmt.Errorf("marshal tree audit snapshot: %w", err)
	}
	maxChars := cfg.MaxInputTokens * 2
	if maxChars > 0 && len([]rune(string(encoded))) > maxChars {
		snapshot.Compressed = true
		for len(snapshot.RecentTranscript) > 4 && utf8.RuneCount(encoded) > maxChars {
			snapshot.RecentTranscript = snapshot.RecentTranscript[1:]
			encoded, _ = json.Marshal(snapshot)
		}
		for len(snapshot.EvidenceSegments) > 8 && utf8.RuneCount(encoded) > maxChars {
			snapshot.EvidenceSegments = snapshot.EvidenceSegments[:len(snapshot.EvidenceSegments)-1]
			encoded, _ = json.Marshal(snapshot)
		}
		for index := range snapshot.Nodes {
			snapshot.Nodes[index].Description = truncateRunes(snapshot.Nodes[index].Description, 120)
			snapshot.Nodes[index].AssignmentReason = truncateRunes(snapshot.Nodes[index].AssignmentReason, 80)
		}
		prioritizeTreeAuditSnapshotNodes(snapshot.Nodes, snapshot.PrecheckFindings)
		encoded, _ = json.Marshal(snapshot)
		for len(snapshot.Nodes) > 8 && utf8.RuneCount(encoded) > maxChars {
			snapshot.Nodes = snapshot.Nodes[:len(snapshot.Nodes)-1]
			encoded, _ = json.Marshal(snapshot)
		}
		for len(snapshot.PrecheckFindings) > 10 && utf8.RuneCount(encoded) > maxChars {
			snapshot.PrecheckFindings = snapshot.PrecheckFindings[:len(snapshot.PrecheckFindings)-1]
			encoded, _ = json.Marshal(snapshot)
		}
		if utf8.RuneCount(encoded) > maxChars {
			return treeAuditSnapshotBuild{}, fmt.Errorf("compressed tree audit snapshot exceeds max input tokens")
		}
	}
	sum := sha256.Sum256(encoded)
	return treeAuditSnapshotBuild{
		Snapshot: snapshot, Hash: hex.EncodeToString(sum[:]),
		EvidenceRoles: roles, InputJSON: encoded,
	}, nil
}

func prioritizeTreeAuditSnapshotNodes(nodes []treeAuditSnapshotNode, findings []treeAuditPrecheckFinding) {
	priority := make(map[string]int)
	for _, finding := range findings {
		for _, id := range append(append([]string(nil), finding.NodeIDs...), finding.RelatedNodeIDs...) {
			priority[id] += 100
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if priority[nodes[i].ID] != priority[nodes[j].ID] {
			return priority[nodes[i].ID] > priority[nodes[j].ID]
		}
		containerI := nodes[i].Fixed || nodes[i].Kind == "topic" || nodes[i].Kind == "group"
		containerJ := nodes[j].Fixed || nodes[j].Kind == "topic" || nodes[j].Kind == "group"
		if containerI != containerJ {
			return containerI
		}
		return nodes[i].ID < nodes[j].ID
	})
}

func selectTreeAuditNodes(state liveAnalysisPayload, findings []treeAuditPrecheckFinding, max int) ([]liveAnalysisTreeNode, bool) {
	if state.Tree == nil || len(state.Tree.Nodes) <= max {
		if state.Tree == nil {
			return nil, false
		}
		return append([]liveAnalysisTreeNode(nil), state.Tree.Nodes...), false
	}
	priority := make(map[string]int)
	for _, finding := range findings {
		for _, id := range append(append([]string(nil), finding.NodeIDs...), finding.RelatedNodeIDs...) {
			priority[id] += 100
		}
	}
	if state.TreeChanges != nil {
		for _, id := range append(append(append([]string(nil), state.TreeChanges.NewNodeIDs...), state.TreeChanges.UpdatedNodeIDs...), state.TreeChanges.ReparentedNodeIDs...) {
			priority[id] += 50
		}
	}
	nodes := append([]liveAnalysisTreeNode(nil), state.Tree.Nodes...)
	sort.SliceStable(nodes, func(i, j int) bool {
		fixedI := nodes[i].ID == treeRootNodeID || nodes[i].Origin == topicOriginAgenda
		fixedJ := nodes[j].ID == treeRootNodeID || nodes[j].Origin == topicOriginAgenda
		if fixedI != fixedJ {
			return fixedI
		}
		if priority[nodes[i].ID] != priority[nodes[j].ID] {
			return priority[nodes[i].ID] > priority[nodes[j].ID]
		}
		return nodes[i].ID < nodes[j].ID
	})
	return nodes[:max], true
}

func deterministicTreeAuditPrecheck(state liveAnalysisPayload, mc *meetingContext, roles map[int64]treeAuditEvidenceRole, cfg TreeAuditConfig) []treeAuditPrecheckFinding {
	cfg = cfg.normalized()
	if state.Tree == nil {
		return nil
	}
	byID := make(map[string]liveAnalysisTreeNode, len(state.Tree.Nodes))
	items := make(map[string]liveAnalysisItem, len(state.Items))
	for _, node := range state.Tree.Nodes {
		byID[node.ID] = node
	}
	for _, item := range state.Items {
		items[item.ID] = item
	}
	containerText := func(id string) string {
		node := byID[id]
		text := node.Label + " " + node.Description
		seen := map[string]struct{}{}
		for node.ParentID != "" && node.ParentID != treeRootNodeID {
			if _, loop := seen[node.ParentID]; loop {
				break
			}
			seen[node.ParentID] = struct{}{}
			node = byID[node.ParentID]
			text += " " + node.Label + " " + node.Description
		}
		return text
	}
	topContainer := func(id string) string {
		current := byID[id]
		seen := map[string]struct{}{}
		for current.ParentID != "" && current.ParentID != treeRootNodeID {
			if _, loop := seen[current.ParentID]; loop {
				return ""
			}
			seen[current.ParentID] = struct{}{}
			current = byID[current.ParentID]
		}
		if current.ParentID == treeRootNodeID {
			return current.ID
		}
		return ""
	}
	var findings []treeAuditPrecheckFinding
	seenFinding := make(map[string]struct{})
	add := func(f treeAuditPrecheckFinding) {
		sort.Strings(f.NodeIDs)
		sort.Strings(f.RelatedNodeIDs)
		key := string(f.Type) + "\x00" + strings.Join(f.NodeIDs, ",") + "\x00" + strings.Join(f.RelatedNodeIDs, ",")
		if _, exists := seenFinding[key]; exists {
			return
		}
		seenFinding[key] = struct{}{}
		findings = append(findings, f)
	}

	containers := make([]liveAnalysisTreeNode, 0)
	for _, node := range state.Tree.Nodes {
		if node.Kind == "topic" && node.ID != treeRootNodeID && node.ID != treeUnclassifiedTopicID {
			containers = append(containers, node)
		}
	}
	for _, node := range state.Tree.Nodes {
		item, detail := items[node.ID]
		if !detail {
			continue
		}
		itemText := item.Title + " " + item.Body
		currentScore := semanticItemSimilarity(itemText, containerText(node.ParentID))
		bestID, bestScore := "", currentScore
		for _, candidate := range containers {
			if candidate.ID == topContainer(node.ID) {
				continue
			}
			score := semanticItemSimilarity(itemText, candidate.Label+" "+candidate.Description)
			if score > bestScore {
				bestID, bestScore = candidate.ID, score
			}
		}
		bestText := ""
		if bestID != "" {
			best := byID[bestID]
			bestText = best.Label + " " + best.Description
		}
		clearAlternative := bestScore-currentScore >= cfg.RequiredImprovementMargin ||
			(currentScore < cfg.CohesionThreshold && bestScore > currentScore && sharedTreeAuditSubjectTerm(itemText, bestText) && !sharedTreeAuditSubjectTerm(itemText, containerText(node.ParentID)))
		if bestID != "" && clearAlternative && (currentScore < cfg.CohesionThreshold || bestScore-currentScore >= 0.08) {
			typeName := TreeAuditSubjectMismatch
			currentTop := byID[topContainer(node.ID)]
			if currentTop.Origin == topicOriginAgenda || inferredAgendaForTopic(currentTop, mc) != "" {
				typeName = TreeAuditCrossAgendaContamination
			}
			add(treeAuditPrecheckFinding{Type: typeName, NodeIDs: []string{node.ID}, RelatedNodeIDs: []string{bestID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "current parent has low subject cohesion and another canonical topic is materially closer", Score: bestScore - currentScore})
			if typeName == TreeAuditCrossAgendaContamination {
				add(treeAuditPrecheckFinding{Type: TreeAuditSubjectMismatch, NodeIDs: []string{node.ID}, RelatedNodeIDs: []string{bestID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "detail subject does not match its current agenda family", Score: bestScore})
			}
		}
		if item.AssignmentConfidence > 0 && item.AssignmentConfidence < cfg.normalized().HighConfidenceThreshold {
			add(treeAuditPrecheckFinding{Type: TreeAuditParentLowConfidence, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "server classification confidence is below audit high-confidence threshold", Score: 1 - item.AssignmentConfidence})
		}
		if state.TreeChanges != nil && containsExactString(state.TreeChanges.ReparentedNodeIDs, node.ID) && allTreeAuditEvidenceReference(item.EvidenceSequenceNos, roles) {
			add(treeAuditPrecheckFinding{Type: TreeAuditReferenceEvidenceReparent, NodeIDs: []string{node.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "the latest parent change is supported only by reference or recap evidence", Score: 1})
		}
	}

	for _, candidate := range state.EmergingTopics {
		var evidenceTexts []string
		for _, id := range candidate.EvidenceItemIDs {
			if item, ok := items[id]; ok {
				evidenceTexts = append(evidenceTexts, item.Title+" "+item.Body)
				candidateScore := semanticItemSimilarity(candidate.Label+" "+candidate.Description, item.Title+" "+item.Body)
				if candidateScore < cfg.CohesionThreshold || (candidateScore < 0.35 && !sharedTreeAuditSubjectTerm(candidate.Label+" "+candidate.Description, item.Title+" "+item.Body)) {
					add(treeAuditPrecheckFinding{Type: TreeAuditCandidateMixedSubjects, NodeIDs: []string{id}, RelatedNodeIDs: []string{candidate.ID}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "candidate subject and evidence item subject have low cohesion", Score: 1 - candidateScore})
					bestTopic, bestScore := "", candidateScore
					for _, topic := range containers {
						score := semanticItemSimilarity(item.Title+" "+item.Body, topic.Label+" "+topic.Description)
						if score > bestScore {
							bestTopic, bestScore = topic.ID, score
						}
					}
					if bestTopic != "" && bestScore-candidateScore >= cfg.RequiredImprovementMargin {
						add(treeAuditPrecheckFinding{Type: TreeAuditCandidateShouldFoldIntoTopic, NodeIDs: []string{id}, RelatedNodeIDs: []string{candidate.ID, bestTopic}, EvidenceSequenceNos: append([]int64(nil), item.EvidenceSequenceNos...), Reason: "candidate evidence is materially more cohesive with an existing canonical topic", Score: bestScore - candidateScore})
					}
				}
			}
		}
		if candidate.RoundCount <= 1 && len(candidate.EvidenceItemIDs) <= 3 {
			add(treeAuditPrecheckFinding{Type: TreeAuditFloatingTentativeCandidate, NodeIDs: append([]string(nil), candidate.EvidenceItemIDs...), RelatedNodeIDs: []string{candidate.ID}, Reason: "single-round tentative candidate requires semantic review", Score: 0.7})
		}
		if candidate.Inactive || (candidate.FirstRound > 0 && state.TreeVersion-candidate.FirstRound >= cfg.TentativeMaxVersions) {
			add(treeAuditPrecheckFinding{Type: TreeAuditStaleTentative, NodeIDs: append([]string(nil), candidate.EvidenceItemIDs...), RelatedNodeIDs: []string{candidate.ID}, Reason: "tentative candidate has remained without promotion beyond the configured version window", Score: 0.8})
		}
		_ = evidenceTexts
	}

	// Similar detail subjects split across different top-level containers are
	// candidate-fragmentation hints, not automatic merge instructions.
	for i := 0; i < len(state.Items); i++ {
		leftTop := topContainer(state.Items[i].ID)
		if leftTop == "" {
			continue
		}
		for j := i + 1; j < len(state.Items); j++ {
			rightTop := topContainer(state.Items[j].ID)
			if rightTop == "" || leftTop == rightTop {
				continue
			}
			score := semanticItemSimilarity(state.Items[i].Title+" "+state.Items[i].Body, state.Items[j].Title+" "+state.Items[j].Body)
			if score >= 0.42 || sharedTreeAuditSubjectTerm(state.Items[i].Title+" "+state.Items[i].Body, state.Items[j].Title+" "+state.Items[j].Body) && score >= 0.12 {
				add(treeAuditPrecheckFinding{Type: TreeAuditCandidateFragmentation, NodeIDs: []string{state.Items[i].ID, state.Items[j].ID}, RelatedNodeIDs: []string{leftTop, rightTop}, EvidenceSequenceNos: append(append([]int64(nil), state.Items[i].EvidenceSequenceNos...), state.Items[j].EvidenceSequenceNos...), Reason: "semantically similar evidence is split across top-level subjects", Score: score})
			}
		}
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Score != findings[j].Score {
			return findings[i].Score > findings[j].Score
		}
		return string(findings[i].Type) < string(findings[j].Type)
	})
	return findings
}

func inferredAgendaForTopic(topic liveAnalysisTreeNode, mc *meetingContext) string {
	if mc == nil || topic.ID == "" {
		return ""
	}
	bestID, best := "", 0.0
	for _, agenda := range mc.Agenda {
		if agenda.Role == agendaRoleActionSummary {
			continue
		}
		score := semanticItemSimilarity(topic.Label+" "+topic.Description, agenda.Title)
		if score > best {
			bestID, best = agenda.ID, score
		}
	}
	if best >= 0.15 {
		return bestID
	}
	return ""
}

func classifyTreeAuditEvidence(state liveAnalysisPayload, segments []domain.TranscriptSegment) map[int64]treeAuditEvidenceRole {
	roles := make(map[int64]treeAuditEvidenceRole)
	segmentBySequence := make(map[int64]domain.TranscriptSegment, len(segments))
	for _, segment := range segments {
		segmentBySequence[segment.SequenceNo] = segment
		roles[segment.SequenceNo] = treeAuditEvidenceSupporting
	}
	for _, item := range state.Items {
		if len(item.EvidenceSequenceNos) == 0 {
			continue
		}
		primary := item.EvidenceSequenceNos[0]
		if looksLikeTreeAuditReference(segmentBySequence[primary].Text, state) {
			roles[primary] = treeAuditEvidenceReference
		} else {
			roles[primary] = treeAuditEvidencePrimary
		}
		for _, sequenceNo := range item.EvidenceSequenceNos[1:] {
			if looksLikeTreeAuditReference(segmentBySequence[sequenceNo].Text, state) {
				roles[sequenceNo] = treeAuditEvidenceReference
			}
		}
	}
	return roles
}

func looksLikeTreeAuditReference(text string, state liveAnalysisPayload) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	matchedItems := 0
	for _, item := range state.Items {
		if semanticItemSimilarity(text, item.Title+" "+item.Body) >= 0.48 {
			matchedItems++
		}
	}
	matchedTopics := 0
	if state.Tree != nil {
		for _, node := range state.Tree.Nodes {
			if node.Kind == "topic" && node.ID != treeRootNodeID && semanticItemSimilarity(text, node.Label+" "+node.Description) >= 0.22 {
				matchedTopics++
			}
		}
	}
	statusReview := strings.Contains(text, "未解決") || strings.Contains(text, "解決済") || strings.Contains(text, "決定事項") || strings.Contains(text, "確認")
	explicitRecap := strings.Contains(text, "まとめ") || strings.Contains(text, "振り返") || strings.Contains(text, "以上")
	return explicitRecap || (matchedTopics >= 2 && matchedItems >= 1) || (statusReview && (matchedItems >= 1 || matchedTopics >= 1 || strings.Contains(text, "課題は")))
}

func sharedTreeAuditSubjectTerm(a, b string) bool {
	aKey, bKey := []rune(semanticItemKey(a)), []rune(semanticItemKey(b))
	if len(aKey) < 2 || len(bKey) < 2 {
		return false
	}
	ignored := map[string]struct{}{
		"調査": {}, "検討": {}, "決定": {}, "課題": {}, "実施": {}, "予定": {},
		"確認": {}, "対応": {}, "結果": {}, "説明": {}, "資料": {}, "方法": {},
	}
	for size := 4; size >= 2; size-- {
		if len(aKey) < size || len(bKey) < size {
			continue
		}
		for i := 0; i+size <= len(aKey); i++ {
			term := string(aKey[i : i+size])
			if _, skip := ignored[term]; skip {
				continue
			}
			if strings.Contains(string(bKey), term) {
				return true
			}
		}
	}
	return false
}

func allTreeAuditEvidenceReference(sequenceNos []int64, roles map[int64]treeAuditEvidenceRole) bool {
	if len(sequenceNos) == 0 {
		return false
	}
	for _, sequenceNo := range sequenceNos {
		if roles[sequenceNo] != treeAuditEvidenceReference {
			return false
		}
	}
	return true
}

func selectTreeAuditTranscript(state liveAnalysisPayload, segments []domain.TranscriptSegment, roles map[int64]treeAuditEvidenceRole, findings []treeAuditPrecheckFinding, cfg TreeAuditConfig) ([]treeAuditEvidenceSegment, []treeAuditEvidenceSegment) {
	bySequence := make(map[int64]domain.TranscriptSegment, len(segments))
	var ordered []int64
	for _, segment := range segments {
		if segment.SequenceNo <= 0 || strings.TrimSpace(segment.Text) == "" {
			continue
		}
		bySequence[segment.SequenceNo] = segment
		ordered = append(ordered, segment.SequenceNo)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	wanted := make(map[int64]struct{})
	for _, finding := range findings {
		for _, sequenceNo := range finding.EvidenceSequenceNos {
			for offset := int64(-2); offset <= 2; offset++ {
				if _, ok := bySequence[sequenceNo+offset]; ok {
					wanted[sequenceNo+offset] = struct{}{}
				}
			}
		}
	}
	evidence := make([]treeAuditEvidenceSegment, 0, len(wanted))
	for _, sequenceNo := range ordered {
		if _, ok := wanted[sequenceNo]; !ok {
			continue
		}
		segment := bySequence[sequenceNo]
		evidence = append(evidence, treeAuditEvidenceSegment{SequenceNo: sequenceNo, Speaker: segment.SpeakerName, Text: truncateRunes(segment.Text, 500), Role: roles[sequenceNo]})
		if len(evidence) >= cfg.MaxEvidenceSegments {
			break
		}
	}
	start := 0
	if len(ordered) > cfg.MaxRecentSegments {
		start = len(ordered) - cfg.MaxRecentSegments
	}
	recent := make([]treeAuditEvidenceSegment, 0, len(ordered)-start)
	for _, sequenceNo := range ordered[start:] {
		segment := bySequence[sequenceNo]
		recent = append(recent, treeAuditEvidenceSegment{SequenceNo: sequenceNo, Speaker: segment.SpeakerName, Text: truncateRunes(segment.Text, 500), Role: roles[sequenceNo]})
	}
	return evidence, recent
}
