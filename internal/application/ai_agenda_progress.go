package application

import (
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// このファイルは「アジェンダ進捗」(agenda progress)を持つ。ライブタブに
// 会議の現在地(現在の議題・話し合い済み・未着手・会議中に追加された論点)を
// 静かに可視化するための、新しいAI呼び出しを伴わないサーバー側決定的計算。
// 既存live analysisの構造化データ(tree/items/agendaAnchors/emergingTopics/
// agendaContextSpan/discourseTimeline)のみを入力に用いる。
// 警告・推奨・severity・脱線表示は実装しない(契約書§0参照)。

const (
	agendaProgressNotStarted = "not_started"
	agendaProgressDiscussing = "discussing"
	agendaProgressDiscussed  = "discussed"

	agendaOutcomeConcluded  = "concluded"
	agendaOutcomeUnresolved = "unresolved"

	agendaProgressSourceFixed   = "fixed_agenda"
	agendaProgressSourceDynamic = "dynamic_topic"

	agendaProgressLinkMaterializedTopic = "materialized-topic"
	agendaProgressLinkVisibleItems      = "visible-items"
	agendaProgressLinkNotLinkable       = "not-linkable"

	outcomeExpectationDecision   = "decision"
	outcomeExpectationOwnerTodo  = "owner_todo"
	outcomeExpectationDueTodo    = "due_todo"
	outcomeExpectationCollection = "collection"
	outcomeExpectationNone       = "none"

	// agendaProgressLeaderScoreThreshold is the minimum round activity score
	// (roundSegments*2+roundItems) an entry needs to become the round's
	// "leader" candidate for the current-topic state machine.
	agendaProgressLeaderScoreThreshold = 2
	// agendaProgressCandidateSwitchRounds is how many consecutive rounds a
	// non-explicit leader must hold before current actually switches to it.
	agendaProgressCandidateSwitchRounds = 2
	// agendaProgressCurrentDematerializeRounds is how many consecutive
	// leaderless rounds clear the current topic entirely.
	agendaProgressCurrentDematerializeRounds = 3
	// agendaProgressDiscussedInactiveRounds is how many consecutive inactive
	// rounds a non-current discussing entry needs before it can become
	// discussed.
	agendaProgressDiscussedInactiveRounds = 2
	// agendaProgressDisplayMinRounds / agendaProgressDisplayMinEvidenceItems
	// gate an unpromoted candidate's display as an additional-topic entry.
	agendaProgressDisplayMinRounds        = 2
	agendaProgressDisplayMinEvidenceItems = 2
)

// agendaProgressEntry is one row of the "アジェンダ進捗" projection: either a
// fixed pre-meeting agenda item or a dynamic topic discovered during the
// meeting. See docs contract §1.2 for the wire shape; tracking fields below
// EffectiveStatus/LastProgressAtVersion are server-internal and are stripped
// by the frontend normalizer.
type agendaProgressEntry struct {
	ID                    string         `json:"id"`
	SourceType            string         `json:"sourceType"`
	Title                 string         `json:"title"`
	Order                 int            `json:"order,omitempty"`
	ComputedStatus        string         `json:"computedStatus"`
	ManualStatus          string         `json:"manualStatus,omitempty"`
	EffectiveStatus       string         `json:"effectiveStatus,omitempty"`
	OutcomeStatus         string         `json:"outcomeStatus,omitempty"`
	DiscussionWeight      float64        `json:"discussionWeight,omitempty"`
	RelatedItemCounts     map[string]int `json:"relatedItemCounts,omitempty"`
	MaterializedTopicIDs  []string       `json:"materializedTopicIds,omitempty"`
	PrimaryNodeID         string         `json:"primaryNodeId,omitempty"`
	CandidateID           string         `json:"candidateId,omitempty"`
	MaterializedTopicID   string         `json:"materializedTopicId,omitempty"`
	FocusNodeIDs          []string       `json:"focusNodeIds,omitempty"`
	LinkState             string         `json:"linkState,omitempty"`
	LastProgressAtVersion int64          `json:"lastProgressAtVersion,omitempty"`
	// 以下はサーバー内部tracking。フロントは正規化時に無視する。
	ActiveRounds        int     `json:"activeRounds,omitempty"`
	SubstantiveSegments int     `json:"substantiveSegments,omitempty"`
	WeightRaw           float64 `json:"weightRaw,omitempty"`
	FirstActiveVersion  int64   `json:"firstActiveVersion,omitempty"`
	InactiveRounds      int     `json:"inactiveRounds,omitempty"`
	OutcomeExpectation  string  `json:"outcomeExpectation,omitempty"`
}

// agendaProgressState is the server-computed projection stored on
// liveAnalysisPayload.AgendaProgress. ManualCurrentTopicID/EffectiveCurrentTopicID
// and each entry's ManualStatus/EffectiveStatus are populated only by
// applyAgendaProgressOverrides on a delivery-time copy; they are never
// present in the payload persisted to the database.
type agendaProgressState struct {
	ComputedCurrentTopicID  string                `json:"computedCurrentTopicId,omitempty"`
	ManualCurrentTopicID    string                `json:"manualCurrentTopicId,omitempty"`
	EffectiveCurrentTopicID string                `json:"effectiveCurrentTopicId,omitempty"`
	Entries                 []agendaProgressEntry `json:"entries"`
	UpdatedAtVersion        int64                 `json:"updatedAtVersion,omitempty"`
	// current topic hysteresis tracking(サーバー内部)。
	CandidateTopicID      string `json:"candidateTopicId,omitempty"`
	CandidateRounds       int    `json:"candidateRounds,omitempty"`
	CurrentSinceVersion   int64  `json:"currentSinceVersion,omitempty"`
	CurrentInactiveRounds int    `json:"currentInactiveRounds,omitempty"`
}

// AgendaProgressOverrides is the durable manual-override record persisted in
// meeting_session_agenda_progress_overrides. It is never part of the live
// analysis payload.
type AgendaProgressOverrides struct {
	StatusOverrides map[string]string `json:"statusOverrides,omitempty"`
	CurrentTopicID  string            `json:"currentTopicId,omitempty"`
}

func isValidAgendaProgressStatus(status string) bool {
	switch status {
	case agendaProgressNotStarted, agendaProgressDiscussing, agendaProgressDiscussed:
		return true
	default:
		return false
	}
}

// --- outcome expectation classification (§2.7) ------------------------------

var (
	agendaOutcomeDecisionPattern   = regexp.MustCompile(`決め|決定|選定|確定|合意|判断|方針を|採否|どちらに|比較`)
	agendaOutcomeOwnerTodoPattern  = regexp.MustCompile(`担当者?を(決|割り当て|アサイン)`)
	agendaOutcomeDueTodoPattern    = regexp.MustCompile(`期限|締め?切り|スケジュール.*(決|確定)|いつまで`)
	agendaOutcomeCollectionPattern = regexp.MustCompile(`洗い出|整理|棚卸|リストアップ|列挙`)
	agendaOwnerHintPattern         = regexp.MustCompile(`さん|氏`)
	agendaDueHintPattern           = regexp.MustCompile(`までに|日|曜|来週|今週`)
)

// classifyAgendaOutcomeExpectation deterministically classifies a fixed
// agenda entry's title into what kind of outcome it is expected to produce.
// This runs once, at entry creation, and never changes afterward.
// The specific expectations are checked before the generic decision pattern:
// 「担当者を決める」「期限を決める」 should expect an owner/due TODO (a decision
// item is not the required artifact for them) even though 「決め」 also matches
// the decision pattern.
func classifyAgendaOutcomeExpectation(title string) string {
	switch {
	case agendaOutcomeOwnerTodoPattern.MatchString(title):
		return outcomeExpectationOwnerTodo
	case agendaOutcomeDueTodoPattern.MatchString(title):
		return outcomeExpectationDueTodo
	case agendaOutcomeCollectionPattern.MatchString(title):
		return outcomeExpectationCollection
	case agendaOutcomeDecisionPattern.MatchString(title):
		return outcomeExpectationDecision
	default:
		return outcomeExpectationNone
	}
}

// --- evaluateAgendaProgress inputs -------------------------------------------

// agendaProgressInputs bundles everything evaluateAgendaProgress needs from
// the current merge round. All fields are read-only server-computed state
// already available at the reconcileAgendaAnchors call site; the model's own
// (ignored) agendaProgress diff is never part of this.
type agendaProgressInputs struct {
	Previous    *agendaProgressState
	MC          *meetingContext
	Tree        *liveAnalysisTree
	Items       []liveAnalysisItem
	Anchors     []agendaAnchor
	Emerging    []emergingTopicCandidate
	Spans       []agendaContextSpan
	Timeline    discourseTimeline
	Scope       liveEvidenceScope
	RoundSeqNos []int64
	DiffItems   []liveAnalysisItem
	TreeVersion int64
	// Stats is optional observability. When non-nil, evaluateAgendaProgress
	// populates its AgendaProgress* fields for the caller (which owns
	// sessionId) to log in the same "Agenda progress evaluated." line style
	// as the rest of this package's per-round diagnostics.
	Stats *liveAnalysisTreeMergeStats
}

// evaluateAgendaProgress computes the next agendaProgressState from the
// previous round's state plus this round's merged tree/items/anchors. It is
// purely deterministic: no AI call, no randomness, and the model's own
// (diff-side) agendaProgress field is never read.
func evaluateAgendaProgress(in agendaProgressInputs) *agendaProgressState {
	state := &agendaProgressState{}
	entriesByID := make(map[string]*agendaProgressEntry)
	order := make([]string, 0, 8)
	if in.Previous != nil {
		state.ComputedCurrentTopicID = in.Previous.ComputedCurrentTopicID
		state.CandidateTopicID = in.Previous.CandidateTopicID
		state.CandidateRounds = in.Previous.CandidateRounds
		state.CurrentSinceVersion = in.Previous.CurrentSinceVersion
		state.CurrentInactiveRounds = in.Previous.CurrentInactiveRounds
		for _, entry := range in.Previous.Entries {
			copied := entry
			entriesByID[copied.ID] = &copied
			order = append(order, copied.ID)
		}
	}

	records := agendaRecordMap(in.MC)
	fixedIDs := make(map[string]struct{})
	if in.MC != nil {
		for _, agenda := range in.MC.Agenda {
			if effectiveAgendaRole(agenda.Role, agenda.Title, "") == agendaRoleActionSummary {
				continue
			}
			fixedIDs[agenda.ID] = struct{}{}
			entry, exists := entriesByID[agenda.ID]
			if !exists {
				created := agendaProgressEntry{
					ID: agenda.ID, SourceType: agendaProgressSourceFixed,
					Title: agenda.Title, Order: agenda.Order,
					ComputedStatus:     agendaProgressNotStarted,
					OutcomeExpectation: classifyAgendaOutcomeExpectation(agenda.Title),
				}
				entriesByID[agenda.ID] = &created
				order = append(order, agenda.ID)
				continue
			}
			entry.Title = agenda.Title
			entry.Order = agenda.Order
			entry.SourceType = agendaProgressSourceFixed
			if entry.OutcomeExpectation == "" {
				entry.OutcomeExpectation = classifyAgendaOutcomeExpectation(agenda.Title)
			}
		}
	}

	anchorByID := make(map[string]agendaAnchor, len(in.Anchors))
	for _, anchor := range in.Anchors {
		anchorByID[anchor.AgendaID] = anchor
	}
	for id := range fixedIDs {
		entry := entriesByID[id]
		anchor := anchorByID[id]
		entry.MaterializedTopicIDs = append([]string(nil), anchor.MaterializedTopicIDs...)
		if len(anchor.MaterializedTopicIDs) > 0 {
			entry.PrimaryNodeID = anchor.MaterializedTopicIDs[0]
			entry.MaterializedTopicID = anchor.MaterializedTopicIDs[0]
			entry.FocusNodeIDs = []string{anchor.MaterializedTopicIDs[0]}
			entry.LinkState = agendaProgressLinkMaterializedTopic
		} else {
			entry.PrimaryNodeID = ""
			entry.MaterializedTopicID = ""
			entry.FocusNodeIDs = nil
			entry.LinkState = agendaProgressLinkNotLinkable
		}
	}

	nodesByID := make(map[string]liveAnalysisTreeNode)
	parents := make(map[string]string)
	dynamicNodeIDs := make(map[string]string)
	dynamicEntryIDByNodeID := make(map[string]string)
	if in.Tree != nil {
		for _, node := range in.Tree.Nodes {
			nodesByID[node.ID] = node
			parents[node.ID] = node.ParentID
			if node.Kind == "topic" && node.Origin == topicOriginDynamic {
				entryID := strings.TrimSpace(node.SourceCandidateID)
				if entryID == "" {
					// Compatibility for payloads written before candidate/topic
					// namespaces were separated.
					entryID = node.ID
				}
				dynamicNodeIDs[entryID] = node.ID
				dynamicEntryIDByNodeID[node.ID] = entryID
			}
		}
	}

	itemsByID := make(map[string]liveAnalysisItem, len(in.Items))
	for _, item := range in.Items {
		itemsByID[item.ID] = item
	}

	candidateByID := make(map[string]emergingTopicCandidate, len(in.Emerging))
	displayableCandidateIDs := make(map[string]struct{})
	for _, candidate := range in.Emerging {
		candidateByID[candidate.ID] = candidate
		if !candidate.Inactive && (candidate.RoundCount >= agendaProgressDisplayMinRounds || len(candidate.EvidenceItemIDs) >= agendaProgressDisplayMinEvidenceItems) {
			displayableCandidateIDs[candidate.ID] = struct{}{}
		}
	}

	// A dynamic entry stays displayed only while its backing still exists: a
	// promoted dynamic topic node in the tree, or a still-displayable
	// unpromoted candidate. A previous entry whose topic was merged into an
	// agenda topic (or whose candidate went inactive) drops out instead of
	// lingering as a ghost row next to the agenda it was folded into.
	dynamicWanted := make(map[string]struct{})
	for id := range dynamicNodeIDs {
		dynamicWanted[id] = struct{}{}
	}
	for id := range displayableCandidateIDs {
		dynamicWanted[id] = struct{}{}
	}

	newDynamicIDs := make([]string, 0)
	for id := range dynamicWanted {
		if _, exists := entriesByID[id]; !exists {
			newDynamicIDs = append(newDynamicIDs, id)
		}
	}
	sort.Strings(newDynamicIDs)
	for _, id := range newDynamicIDs {
		created := agendaProgressEntry{
			ID: id, SourceType: agendaProgressSourceDynamic,
			ComputedStatus:     agendaProgressDiscussing,
			OutcomeExpectation: outcomeExpectationNone,
			FirstActiveVersion: in.TreeVersion,
		}
		entriesByID[id] = &created
		order = append(order, id)
	}
	for id := range dynamicWanted {
		entry := entriesByID[id]
		if nodeID, materialized := dynamicNodeIDs[id]; materialized {
			node := nodesByID[nodeID]
			entry.Title = node.Label
			entry.CandidateID = strings.TrimSpace(node.SourceCandidateID)
			if entry.CandidateID == "" && strings.HasPrefix(id, "candidate-") {
				entry.CandidateID = id
			}
			entry.PrimaryNodeID = nodeID
			entry.MaterializedTopicID = nodeID
			entry.MaterializedTopicIDs = []string{nodeID}
			entry.FocusNodeIDs = []string{nodeID}
			entry.LinkState = agendaProgressLinkMaterializedTopic
		} else if candidate, ok := candidateByID[id]; ok {
			entry.Title = candidate.Label
			entry.CandidateID = candidate.ID
			entry.PrimaryNodeID = ""
			entry.MaterializedTopicID = ""
			entry.MaterializedTopicIDs = nil
			entry.FocusNodeIDs = nil
			for _, itemID := range candidate.EvidenceItemIDs {
				item, itemExists := itemsByID[itemID]
				node, nodeExists := nodesByID[itemID]
				if !itemExists || !nodeExists || node.Kind == "topic" || node.Kind == "group" ||
					item.Inactive || item.MergedIntoID != "" || item.Status == "dismissed" ||
					item.ClassificationStatus == classificationTentative {
					continue
				}
				entry.FocusNodeIDs = append(entry.FocusNodeIDs, itemID)
			}
			if len(entry.FocusNodeIDs) > 0 {
				entry.PrimaryNodeID = entry.FocusNodeIDs[0]
				entry.LinkState = agendaProgressLinkVisibleItems
			} else {
				entry.LinkState = agendaProgressLinkNotLinkable
			}
		}
	}

	resolveTarget := func(itemID string) string {
		topicID := treeItemTopic(in.Tree, itemID)
		if topicID != "" {
			if node, ok := nodesByID[topicID]; ok {
				if refs := topicAgendaRefs(node, records); len(refs) > 0 {
					return refs[0]
				}
				if node.Origin == topicOriginDynamic {
					if entryID := dynamicEntryIDByNodeID[topicID]; entryID != "" {
						return entryID
					}
					return topicID
				}
			}
		}
		if item, ok := itemsByID[itemID]; ok {
			candidateID := strings.TrimSpace(item.CandidateTopicID)
			if candidateID != "" && candidateID != treeUnclassifiedTopicID {
				if _, wanted := dynamicWanted[candidateID]; wanted {
					return candidateID
				}
			}
		}
		return ""
	}

	roundSegments := make(map[string]int)
	roundItems := make(map[string]int)
	multiAgendaEvidenceCount := 0
	dedupedDiffItems := make(map[string]struct{}, len(in.DiffItems))
	for _, item := range in.DiffItems {
		if _, dup := dedupedDiffItems[item.ID]; dup {
			continue
		}
		dedupedDiffItems[item.ID] = struct{}{}
		target := resolveTarget(item.ID)
		if target == "" {
			continue
		}
		if _, wanted := entriesByID[target]; !wanted {
			continue
		}
		roundItems[target]++
		for _, relatedID := range item.RelatedAgendaIDs {
			if relatedID != target {
				multiAgendaEvidenceCount++
			}
		}
	}
	seqToItem := make(map[int64]string)
	for _, item := range in.DiffItems {
		for _, seq := range item.EvidenceSequenceNos {
			if _, exists := seqToItem[seq]; !exists {
				seqToItem[seq] = item.ID
			}
		}
	}
	for _, seq := range in.RoundSeqNos {
		role := in.Timeline.Roles[seq]
		if role != liveEvidencePrimary && role != liveEvidenceSupporting && role != liveEvidenceCorrection {
			continue
		}
		target := ""
		if span, found := agendaContextSpanForEvidence([]int64{seq}, in.Spans); found && span.Mode == agendaContextModeFixed && span.AgendaID != "" {
			target = span.AgendaID
		} else if itemID, ok := seqToItem[seq]; ok {
			target = resolveTarget(itemID)
		}
		if target == "" {
			continue
		}
		if _, wanted := entriesByID[target]; !wanted {
			continue
		}
		roundSegments[target]++
	}

	for id, entry := range entriesByID {
		rs := roundSegments[id]
		ri := roundItems[id]
		entry.SubstantiveSegments += rs
		entry.WeightRaw += float64(rs) + 0.5*float64(minAgendaProgressInt(ri, 2))
		if rs >= 1 || ri >= 1 {
			entry.ActiveRounds++
			entry.LastProgressAtVersion = in.TreeVersion
			entry.InactiveRounds = 0
			if entry.FirstActiveVersion == 0 {
				entry.FirstActiveVersion = in.TreeVersion
			}
		} else {
			entry.InactiveRounds++
		}
	}

	// --- current topic state machine (§2.6) ----------------------------------
	roundSeqSet := make(map[int64]struct{}, len(in.RoundSeqNos))
	for _, seq := range in.RoundSeqNos {
		roundSeqSet[seq] = struct{}{}
	}
	explicitLeaderID := ""
	for _, span := range in.Spans {
		if span.Mode != agendaContextModeFixed || !span.Explicit || span.AgendaID == "" {
			continue
		}
		if _, startedThisRound := roundSeqSet[span.StartSequenceNo]; startedThisRound {
			explicitLeaderID = span.AgendaID
		}
	}
	type scoredEntry struct {
		id      string
		score   int
		order   int
		isFixed bool
	}
	scored := make([]scoredEntry, 0, len(entriesByID))
	for id, entry := range entriesByID {
		score := roundSegments[id]*2 + roundItems[id]
		if score < agendaProgressLeaderScoreThreshold {
			continue
		}
		_, isFixed := fixedIDs[id]
		scored = append(scored, scoredEntry{id: id, score: score, order: entry.Order, isFixed: isFixed})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		if scored[i].isFixed != scored[j].isFixed {
			return scored[i].isFixed
		}
		if scored[i].isFixed && scored[i].order != scored[j].order {
			return scored[i].order < scored[j].order
		}
		return scored[i].id < scored[j].id
	})
	leaderID := ""
	if len(scored) > 0 {
		leaderID = scored[0].id
	}

	previousCurrent := state.ComputedCurrentTopicID
	currentChanged := false
	switch {
	case explicitLeaderID != "":
		currentChanged = previousCurrent != explicitLeaderID
		state.ComputedCurrentTopicID = explicitLeaderID
		state.CandidateTopicID, state.CandidateRounds, state.CurrentInactiveRounds = "", 0, 0
	case previousCurrent == "":
		if leaderID != "" {
			state.ComputedCurrentTopicID = leaderID
			currentChanged = true
			state.CandidateTopicID, state.CandidateRounds, state.CurrentInactiveRounds = "", 0, 0
		}
	case leaderID == previousCurrent:
		state.CandidateTopicID, state.CandidateRounds, state.CurrentInactiveRounds = "", 0, 0
	case leaderID != "":
		if state.CandidateTopicID == leaderID {
			state.CandidateRounds++
		} else {
			state.CandidateTopicID = leaderID
			state.CandidateRounds = 1
		}
		if state.CandidateRounds >= agendaProgressCandidateSwitchRounds || state.CurrentInactiveRounds >= agendaProgressDiscussedInactiveRounds {
			state.ComputedCurrentTopicID = leaderID
			currentChanged = true
			state.CandidateTopicID, state.CandidateRounds, state.CurrentInactiveRounds = "", 0, 0
		}
	default:
		state.CurrentInactiveRounds++
		if state.CurrentInactiveRounds >= agendaProgressCurrentDematerializeRounds {
			state.ComputedCurrentTopicID = ""
			currentChanged = true
			state.CandidateTopicID, state.CandidateRounds, state.CurrentInactiveRounds = "", 0, 0
		}
	}
	if currentChanged && state.ComputedCurrentTopicID != "" {
		state.CurrentSinceVersion = in.TreeVersion
	}
	newCurrent := state.ComputedCurrentTopicID

	// RelatedItemCounts must be current before the status machine runs: the
	// discussing->discussed transition's outcome-evidence condition (§2.5d)
	// reads it. OutcomeStatus itself is computed afterward, once each entry's
	// final ComputedStatus for this round is known.
	relatedByID := computeAgendaProgressRelatedItems(entriesByID, order, in.Tree, in.Items, fixedIDs)

	// --- per-entry status machine (§2.5) -------------------------------------
	statusTransitions := make([]string, 0)
	for _, id := range order {
		entry, ok := entriesByID[id]
		if !ok {
			continue
		}
		_, isFixed := fixedIDs[id]
		anchor, hasAnchor := anchorByID[id]
		grounded := hasAnchor && (anchor.Status == agendaStatusDiscussed || anchor.Status == agendaStatusMerged)
		materialized := hasAnchor && (grounded || anchor.Status == agendaStatusMaterialized)
		before := entry.ComputedStatus
		switch entry.ComputedStatus {
		case agendaProgressNotStarted, "":
			becomeCurrent := id == newCurrent && newCurrent != ""
			if roundSegments[id] >= 2 || (entry.SubstantiveSegments >= 2 && entry.ActiveRounds >= 2) || (isFixed && materialized) || becomeCurrent {
				entry.ComputedStatus = agendaProgressDiscussing
			}
		case agendaProgressDiscussing:
			if id != newCurrent && entry.InactiveRounds >= agendaProgressDiscussedInactiveRounds {
				relatedTotal := 0
				for _, count := range entry.RelatedItemCounts {
					relatedTotal += count
				}
				activeEnough := entry.ActiveRounds >= 2 || entry.SubstantiveSegments >= 4 || (isFixed && grounded)
				outcomeEnough := relatedTotal >= 1 || (isFixed && grounded)
				if activeEnough && outcomeEnough {
					entry.ComputedStatus = agendaProgressDiscussed
				}
			}
		case agendaProgressDiscussed:
			if id == newCurrent && newCurrent != "" {
				entry.ComputedStatus = agendaProgressDiscussing
			}
		}
		if entry.ComputedStatus != before {
			statusTransitions = append(statusTransitions, id+":"+before+">"+entry.ComputedStatus)
		}
	}

	applyAgendaProgressOutcome(entriesByID, order, fixedIDs, relatedByID)

	maxWeight := 0.0
	for _, entry := range entriesByID {
		if entry.WeightRaw > maxWeight {
			maxWeight = entry.WeightRaw
		}
	}
	for _, entry := range entriesByID {
		if entry.ComputedStatus == agendaProgressNotStarted || maxWeight <= 0 {
			entry.DiscussionWeight = 0
			continue
		}
		entry.DiscussionWeight = entry.WeightRaw / maxWeight
	}

	fixedList := make([]agendaProgressEntry, 0, len(fixedIDs))
	dynamicList := make([]agendaProgressEntry, 0)
	for _, id := range order {
		entry, ok := entriesByID[id]
		if !ok {
			continue
		}
		if _, isFixed := fixedIDs[id]; isFixed {
			fixedList = append(fixedList, *entry)
			continue
		}
		if _, wanted := dynamicWanted[id]; wanted {
			dynamicList = append(dynamicList, *entry)
		}
	}
	sort.SliceStable(fixedList, func(i, j int) bool { return fixedList[i].Order < fixedList[j].Order })
	state.Entries = append(fixedList, dynamicList...)
	if state.Entries == nil {
		state.Entries = []agendaProgressEntry{}
	}
	state.UpdatedAtVersion = in.TreeVersion

	if in.Stats != nil {
		weights := make([]string, 0, len(state.Entries))
		for _, entry := range state.Entries {
			if entry.DiscussionWeight > 0 {
				weights = append(weights, entry.ID+":"+formatAgendaProgressWeight(entry.DiscussionWeight))
			}
		}
		in.Stats.AgendaProgressAgendaCount = len(fixedList)
		in.Stats.AgendaProgressCurrentTopicID = state.ComputedCurrentTopicID
		in.Stats.AgendaProgressCurrentTopicChanged = currentChanged
		in.Stats.AgendaProgressStatusTransitions = statusTransitions
		in.Stats.AgendaProgressAdditionalTopicCandidates = len(in.Emerging)
		in.Stats.AgendaProgressAdditionalTopicsDisplayed = len(dynamicList)
		in.Stats.AgendaProgressMultiAgendaEvidenceCount = multiAgendaEvidenceCount
		in.Stats.AgendaProgressWeights = weights
	}

	return state
}

func minAgendaProgressInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func formatAgendaProgressWeight(value float64) string {
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(value, 'f', 2, 64), "0"), ".")
}

// isAgendaProgressDescendantOrSelf walks nodeID's ancestor chain (via
// parents) looking for ancestorID.
func isAgendaProgressDescendantOrSelf(parents map[string]string, nodeID, ancestorID string) bool {
	if nodeID == "" || ancestorID == "" {
		return false
	}
	seen := make(map[string]struct{})
	current := nodeID
	for current != "" {
		if current == ancestorID {
			return true
		}
		if _, loop := seen[current]; loop {
			return false
		}
		seen[current] = struct{}{}
		current = parents[current]
	}
	return false
}

// computeAgendaProgressRelatedItems recomputes RelatedItemCounts for every
// entry from the current canonical item set and returns the same matched
// items keyed by entry id (for applyAgendaProgressOutcome). It is shared by
// evaluateAgendaProgress and finalizeAgendaProgress (§2.7's "related item" =
// this entry's tree subtree plus any item whose RelatedAgendaIDs names it).
// It must run before the §2.5 status machine, since the discussing->discussed
// transition's outcome-evidence condition reads RelatedItemCounts.
func computeAgendaProgressRelatedItems(entriesByID map[string]*agendaProgressEntry, order []string, tree *liveAnalysisTree, items []liveAnalysisItem, fixedIDs map[string]struct{}) map[string][]liveAnalysisItem {
	parents := make(map[string]string)
	if tree != nil {
		for _, node := range tree.Nodes {
			parents[node.ID] = node.ParentID
		}
	}
	relatedByID := make(map[string][]liveAnalysisItem, len(order))
	for _, id := range order {
		entry, ok := entriesByID[id]
		if !ok {
			continue
		}
		_, isFixed := fixedIDs[id]
		var nodeIDs []string
		if isFixed {
			nodeIDs = entry.MaterializedTopicIDs
		} else {
			nodeIDs = entry.MaterializedTopicIDs
		}
		counts := make(map[string]int)
		related := make([]liveAnalysisItem, 0, 4)
		for _, item := range items {
			if item.Inactive || item.MergedIntoID != "" {
				continue
			}
			matched := containsExactString(item.RelatedAgendaIDs, id)
			if !matched && !isFixed && entry.CandidateID != "" {
				matched = item.CandidateTopicID == entry.CandidateID
			}
			if !matched {
				topicID := treeItemTopic(tree, item.ID)
				if topicID != "" {
					for _, nodeID := range nodeIDs {
						if nodeID != "" && (topicID == nodeID || isAgendaProgressDescendantOrSelf(parents, topicID, nodeID)) {
							matched = true
							break
						}
					}
				}
			}
			if !matched {
				continue
			}
			counts[item.Kind]++
			related = append(related, item)
		}
		if len(counts) == 0 {
			entry.RelatedItemCounts = nil
		} else {
			entry.RelatedItemCounts = counts
		}
		relatedByID[id] = related
	}
	return relatedByID
}

// applyAgendaProgressOutcome sets OutcomeStatus for every entry from its
// final (post status-machine) ComputedStatus, using the related items
// computeAgendaProgressRelatedItems already matched.
func applyAgendaProgressOutcome(entriesByID map[string]*agendaProgressEntry, order []string, fixedIDs map[string]struct{}, relatedByID map[string][]liveAnalysisItem) {
	for _, id := range order {
		entry, ok := entriesByID[id]
		if !ok {
			continue
		}
		entry.OutcomeStatus = ""
		if entry.ComputedStatus != agendaProgressDiscussed {
			continue
		}
		_, isFixed := fixedIDs[id]
		entry.OutcomeStatus = computeAgendaOutcomeStatus(entry, isFixed, relatedByID[id])
	}
}

func computeAgendaOutcomeStatus(entry *agendaProgressEntry, isFixed bool, related []liveAnalysisItem) string {
	kindCount := func(kind string) int {
		count := 0
		for _, item := range related {
			if item.Kind == kind {
				count++
			}
		}
		return count
	}
	decisionCount := kindCount("decision")
	if !isFixed {
		if decisionCount >= 1 {
			return agendaOutcomeConcluded
		}
		return ""
	}
	switch entry.OutcomeExpectation {
	case outcomeExpectationDecision:
		if decisionCount >= 1 {
			return agendaOutcomeConcluded
		}
		if entry.ActiveRounds >= 2 {
			return agendaOutcomeUnresolved
		}
		return ""
	case outcomeExpectationOwnerTodo:
		todoCount := kindCount("todo")
		if todoCount > 0 && agendaTodosHaveHint(related, agendaOwnerHintPattern) {
			return agendaOutcomeConcluded
		}
		if entry.ActiveRounds >= 2 && todoCount > 0 {
			return agendaOutcomeUnresolved
		}
		return ""
	case outcomeExpectationDueTodo:
		todoCount := kindCount("todo")
		if todoCount > 0 && agendaTodosHaveHint(related, agendaDueHintPattern) {
			return agendaOutcomeConcluded
		}
		if entry.ActiveRounds >= 2 && todoCount > 0 {
			return agendaOutcomeUnresolved
		}
		return ""
	case outcomeExpectationCollection:
		if kindCount("issue")+kindCount("risk") >= 2 {
			return agendaOutcomeConcluded
		}
		return ""
	default:
		if decisionCount >= 1 {
			return agendaOutcomeConcluded
		}
		return ""
	}
}

func agendaTodosHaveHint(items []liveAnalysisItem, pattern *regexp.Regexp) bool {
	for _, item := range items {
		if item.Kind != "todo" {
			continue
		}
		if pattern.MatchString(item.Title + item.Body) {
			return true
		}
	}
	return false
}

// --- finalization (§2.9) -----------------------------------------------------

// finalizeAgendaProgress runs the deterministic meeting-end lifecycle pass:
// the computed current topic is cleared, discussing entries that already
// satisfy the discussed criteria are promoted, and outcome is re-evaluated.
// It mutates state.AgendaProgress in place (creating it from mc.Agenda when
// the payload never had one and there is an agenda to project).
func finalizeAgendaProgress(state *liveAnalysisPayload, mc *meetingContext, treeVersion int64) {
	if state == nil {
		return
	}
	if state.AgendaProgress == nil {
		if mc == nil || len(mc.Agenda) == 0 {
			return
		}
		state.AgendaProgress = synthesizeAgendaProgressFromAnchors(mc, state.AgendaAnchors, treeVersion)
		return
	}
	progress := state.AgendaProgress
	progress.ComputedCurrentTopicID = ""
	progress.CandidateTopicID = ""
	progress.CandidateRounds = 0
	progress.CurrentInactiveRounds = 0
	// Final tree review may materialize or remove a dynamic topic after the
	// last live-analysis round. Reconcile candidate→topic links before the
	// final projection is persisted and broadcast.
	refreshAgendaProgressNodeRefs(progress, state.Tree)

	fixedIDs := make(map[string]struct{})
	if mc != nil {
		for _, agenda := range mc.Agenda {
			if effectiveAgendaRole(agenda.Role, agenda.Title, "") == agendaRoleActionSummary {
				continue
			}
			fixedIDs[agenda.ID] = struct{}{}
		}
	}
	anchorByID := make(map[string]agendaAnchor, len(state.AgendaAnchors))
	for _, anchor := range state.AgendaAnchors {
		anchorByID[anchor.AgendaID] = anchor
	}

	order := make([]string, 0, len(progress.Entries))
	entriesByID := make(map[string]*agendaProgressEntry, len(progress.Entries))
	for i := range progress.Entries {
		entriesByID[progress.Entries[i].ID] = &progress.Entries[i]
		order = append(order, progress.Entries[i].ID)
	}
	// RelatedItemCounts must be current before the discussing->discussed
	// promotion check below reads it (same ordering requirement as §2.5 in
	// evaluateAgendaProgress).
	relatedByID := computeAgendaProgressRelatedItems(entriesByID, order, state.Tree, state.Items, fixedIDs)
	for id := range fixedIDs {
		entry, ok := entriesByID[id]
		if !ok || entry.ComputedStatus != agendaProgressDiscussing {
			continue
		}
		anchor := anchorByID[id]
		grounded := anchor.Status == agendaStatusDiscussed || anchor.Status == agendaStatusMerged
		relatedTotal := 0
		for _, count := range entry.RelatedItemCounts {
			relatedTotal += count
		}
		activeEnough := entry.ActiveRounds >= 2 || entry.SubstantiveSegments >= 4 || grounded
		outcomeEnough := relatedTotal >= 1 || grounded
		if activeEnough && outcomeEnough {
			entry.ComputedStatus = agendaProgressDiscussed
		}
	}
	// Dynamic entries have no anchor lifecycle to finalize against; only
	// their already-computed status (from the last live round) applies.
	applyAgendaProgressOutcome(entriesByID, order, fixedIDs, relatedByID)
	progress.UpdatedAtVersion = treeVersion
}

// synthesizeAgendaProgressFromAnchors builds a minimal projection for legacy
// payloads that predate AgendaProgress, mapping agenda anchor lifecycle
// status onto the three agenda progress states (§2.11).
func synthesizeAgendaProgressFromAnchors(mc *meetingContext, anchors []agendaAnchor, treeVersion int64) *agendaProgressState {
	if mc == nil || len(mc.Agenda) == 0 {
		return &agendaProgressState{Entries: []agendaProgressEntry{}}
	}
	anchorByID := make(map[string]agendaAnchor, len(anchors))
	for _, anchor := range anchors {
		anchorByID[anchor.AgendaID] = anchor
	}
	entries := make([]agendaProgressEntry, 0, len(mc.Agenda))
	for _, agenda := range mc.Agenda {
		if effectiveAgendaRole(agenda.Role, agenda.Title, "") == agendaRoleActionSummary {
			continue
		}
		anchor := anchorByID[agenda.ID]
		status := agendaProgressNotStarted
		switch anchor.Status {
		case agendaStatusMaterialized:
			status = agendaProgressDiscussing
		case agendaStatusDiscussed, agendaStatusMerged:
			status = agendaProgressDiscussed
		}
		entry := agendaProgressEntry{
			ID: agenda.ID, SourceType: agendaProgressSourceFixed, Title: agenda.Title, Order: agenda.Order,
			ComputedStatus:       status,
			OutcomeExpectation:   classifyAgendaOutcomeExpectation(agenda.Title),
			MaterializedTopicIDs: append([]string(nil), anchor.MaterializedTopicIDs...),
		}
		if len(anchor.MaterializedTopicIDs) > 0 {
			entry.PrimaryNodeID = anchor.MaterializedTopicIDs[0]
			entry.MaterializedTopicID = anchor.MaterializedTopicIDs[0]
			entry.FocusNodeIDs = []string{anchor.MaterializedTopicIDs[0]}
			entry.LinkState = agendaProgressLinkMaterializedTopic
		} else {
			entry.LinkState = agendaProgressLinkNotLinkable
		}
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Order < entries[j].Order })
	return &agendaProgressState{Entries: entries, UpdatedAtVersion: treeVersion}
}

// refreshAgendaProgressNodeRefs drops MaterializedTopicIds/primaryNodeId
// references to tree nodes that no longer exist, used by
// sanitizeLiveAnalysisForDelivery after a legacy-repair tree rebuild (§2.11).
func refreshAgendaProgressNodeRefs(state *agendaProgressState, tree *liveAnalysisTree) {
	if state == nil {
		return
	}
	existing := make(map[string]struct{})
	materializedTopicByCandidate := make(map[string]string)
	if tree != nil {
		for _, node := range tree.Nodes {
			existing[node.ID] = struct{}{}
			if node.Kind == "topic" && node.Origin == topicOriginDynamic &&
				strings.TrimSpace(node.SourceCandidateID) != "" {
				materializedTopicByCandidate[node.SourceCandidateID] = node.ID
			}
		}
	}
	for i := range state.Entries {
		entry := &state.Entries[i]
		candidateID := strings.TrimSpace(entry.CandidateID)
		if candidateID == "" && entry.SourceType == agendaProgressSourceDynamic &&
			strings.HasPrefix(entry.ID, "candidate-") {
			candidateID = entry.ID
			entry.CandidateID = candidateID
		}
		if topicID := materializedTopicByCandidate[candidateID]; topicID != "" {
			entry.MaterializedTopicIDs = []string{topicID}
		}
		kept := make([]string, 0, len(entry.MaterializedTopicIDs))
		for _, id := range entry.MaterializedTopicIDs {
			if _, ok := existing[id]; ok {
				kept = append(kept, id)
			}
		}
		if len(kept) == 0 {
			entry.MaterializedTopicIDs = nil
		} else {
			entry.MaterializedTopicIDs = kept
		}
		if entry.PrimaryNodeID != "" {
			if _, ok := existing[entry.PrimaryNodeID]; !ok {
				if len(entry.MaterializedTopicIDs) > 0 {
					entry.PrimaryNodeID = entry.MaterializedTopicIDs[0]
				} else {
					entry.PrimaryNodeID = ""
				}
			}
		}
		entry.MaterializedTopicID = ""
		if len(entry.MaterializedTopicIDs) > 0 {
			entry.MaterializedTopicID = entry.MaterializedTopicIDs[0]
		}
		keptFocusNodeIDs := make([]string, 0, len(entry.FocusNodeIDs))
		for _, id := range entry.FocusNodeIDs {
			if _, ok := existing[id]; ok {
				keptFocusNodeIDs = append(keptFocusNodeIDs, id)
			}
		}
		entry.FocusNodeIDs = keptFocusNodeIDs
		switch {
		case entry.MaterializedTopicID != "":
			entry.PrimaryNodeID = entry.MaterializedTopicID
			entry.FocusNodeIDs = []string{entry.MaterializedTopicID}
			entry.LinkState = agendaProgressLinkMaterializedTopic
		case len(entry.FocusNodeIDs) > 0:
			entry.PrimaryNodeID = entry.FocusNodeIDs[0]
			entry.LinkState = agendaProgressLinkVisibleItems
		default:
			entry.PrimaryNodeID = ""
			entry.FocusNodeIDs = nil
			entry.LinkState = agendaProgressLinkNotLinkable
		}
	}
}

// logAgendaProgressLinks emits one bounded row per displayed additional topic
// and tree version. It intentionally logs IDs/states only, never titles or
// transcript text, so a production incident can distinguish missing mapping,
// hidden tentative evidence, and a successful focus contract.
func logAgendaProgressLinks(sessionID string, treeVersion int64, state *agendaProgressState) {
	if state == nil {
		return
	}
	for _, entry := range state.Entries {
		if entry.SourceType != agendaProgressSourceDynamic {
			continue
		}
		log.Printf("Agenda progress link evaluated. sessionId=%s treeVersion=%d candidateId=%s materializedTopicId=%s focusNodeIds=%v linkState=%s",
			sessionID, treeVersion, entry.CandidateID, entry.MaterializedTopicID, entry.FocusNodeIDs, entry.LinkState)
	}
}

// --- manual override stamping (§2.10) ----------------------------------------

func cloneAgendaProgressState(state *agendaProgressState) *agendaProgressState {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.Entries = append([]agendaProgressEntry(nil), state.Entries...)
	return &cloned
}

// applyAgendaProgressOverrides stamps manual overrides onto a COPY of state,
// never mutating the argument (so the caller's live payload bytes, which are
// persisted/broadcast without manual fields, stay clean). An override that
// points at an entry id which does not exist in state is silently ignored
// (effective falls back to computed).
func applyAgendaProgressOverrides(state *agendaProgressState, overrides *AgendaProgressOverrides) *agendaProgressState {
	if state == nil {
		return nil
	}
	cloned := cloneAgendaProgressState(state)
	entryIDs := make(map[string]struct{}, len(cloned.Entries))
	for _, entry := range cloned.Entries {
		entryIDs[entry.ID] = struct{}{}
	}
	for i := range cloned.Entries {
		entry := &cloned.Entries[i]
		entry.ManualStatus = ""
		if overrides != nil {
			if manual, ok := overrides.StatusOverrides[entry.ID]; ok && isValidAgendaProgressStatus(manual) {
				entry.ManualStatus = manual
			}
		}
		if entry.ManualStatus != "" {
			entry.EffectiveStatus = entry.ManualStatus
		} else {
			entry.EffectiveStatus = entry.ComputedStatus
		}
	}
	cloned.ManualCurrentTopicID = ""
	if overrides != nil {
		currentID := strings.TrimSpace(overrides.CurrentTopicID)
		if currentID != "" {
			if _, exists := entryIDs[currentID]; exists {
				cloned.ManualCurrentTopicID = currentID
			}
		}
	}
	if cloned.ManualCurrentTopicID != "" {
		cloned.EffectiveCurrentTopicID = cloned.ManualCurrentTopicID
	} else {
		cloned.EffectiveCurrentTopicID = cloned.ComputedCurrentTopicID
	}
	return cloned
}
