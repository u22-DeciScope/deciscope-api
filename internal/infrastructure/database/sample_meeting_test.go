package database

import (
	"encoding/json"
	"testing"
)

func TestSampleMeetingPayloadsMatchCurrentCompletedMeetingContract(t *testing.T) {
	for name, payload := range map[string]string{
		"context":      sampleMeetingContextPayload,
		"live":         sampleLiveAnalysisPayload,
		"tree":         sampleTreeSnapshotPayload("2026-01-02T03:04:05Z"),
		"final":        sampleFinalAnalysisPayload,
		"finalization": sampleFinalizationPayload("2026-01-02T03:04:05Z"),
	} {
		var decoded any
		if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
			t.Fatalf("%s payload is invalid JSON: %v", name, err)
		}
	}

	var contextPayload struct {
		AgendaItems []struct {
			ID string `json:"id"`
		} `json:"agendaItems"`
	}
	if err := json.Unmarshal([]byte(sampleMeetingContextPayload), &contextPayload); err != nil {
		t.Fatal(err)
	}
	if len(contextPayload.AgendaItems) != 3 {
		t.Fatalf("context agenda count = %d, want 3", len(contextPayload.AgendaItems))
	}

	type node struct {
		ID             string   `json:"id"`
		Kind           string   `json:"kind"`
		ParentID       string   `json:"parentId"`
		RelatedItemIDs []string `json:"relatedItemIds"`
		AgendaRefs     []string `json:"agendaRefs"`
		Materialized   bool     `json:"materialized"`
	}
	var live struct {
		Items []struct {
			ID                   string  `json:"id"`
			Kind                 string  `json:"kind"`
			ProjectionStatus     string  `json:"projectionStatus"`
			ClassificationStatus string  `json:"classificationStatus"`
			EvidenceSequenceNos  []int64 `json:"evidenceSequenceNos"`
		} `json:"items"`
		Tree struct {
			Nodes []node `json:"nodes"`
		} `json:"tree"`
		AgendaAnchors []struct {
			AgendaID             string   `json:"agendaId"`
			Status               string   `json:"status"`
			MaterializedTopicIDs []string `json:"materializedTopicIds"`
		} `json:"agendaAnchors"`
		AgendaProgress struct {
			Entries []struct {
				ID             string `json:"id"`
				ComputedStatus string `json:"computedStatus"`
				LinkState      string `json:"linkState"`
			} `json:"entries"`
		} `json:"agendaProgress"`
		TreeVersion             int64  `json:"treeVersion"`
		PayloadKind             string `json:"payloadKind"`
		ItemProjectionCompleted bool   `json:"itemProjectionCompleted"`
		TreeProjectionCompleted bool   `json:"treeProjectionCompleted"`
	}
	if err := json.Unmarshal([]byte(sampleLiveAnalysisPayload), &live); err != nil {
		t.Fatal(err)
	}
	if live.TreeVersion != 8 || live.PayloadKind != "full_snapshot" ||
		!live.ItemProjectionCompleted || !live.TreeProjectionCompleted {
		t.Fatalf("live projection metadata is incomplete: %+v", live)
	}

	nodes := make(map[string]node, len(live.Tree.Nodes))
	for _, current := range live.Tree.Nodes {
		if _, exists := nodes[current.ID]; exists {
			t.Fatalf("duplicate tree node id %q", current.ID)
		}
		nodes[current.ID] = current
	}
	root, ok := nodes["root"]
	if !ok || root.Kind != "topic" || root.ParentID != "" {
		t.Fatalf("canonical root node is missing or invalid: %+v", root)
	}
	for _, current := range live.Tree.Nodes {
		if current.ID == "root" {
			continue
		}
		parent, exists := nodes[current.ParentID]
		if !exists {
			t.Fatalf("node %q has unknown parent %q", current.ID, current.ParentID)
		}
		if current.Kind != "topic" && parent.Kind != "topic" && parent.Kind != "group" {
			t.Fatalf("detail node %q has detail parent %q", current.ID, parent.ID)
		}
	}

	for _, item := range live.Items {
		current, exists := nodes[item.ID]
		if !exists || current.Kind != item.Kind {
			t.Fatalf("item %q kind %q is not mirrored by its tree node", item.ID, item.Kind)
		}
		if item.ProjectionStatus != "stable" || item.ClassificationStatus != "assigned" || len(item.EvidenceSequenceNos) == 0 {
			t.Fatalf("item %q lacks current projection/classification/evidence metadata", item.ID)
		}
	}
	if len(live.AgendaAnchors) != 3 || len(live.AgendaProgress.Entries) != 3 {
		t.Fatalf("agenda projection is incomplete: anchors=%d progress=%d", len(live.AgendaAnchors), len(live.AgendaProgress.Entries))
	}
	for _, anchor := range live.AgendaAnchors {
		if anchor.Status != "discussed" || len(anchor.MaterializedTopicIDs) != 1 {
			t.Fatalf("agenda anchor %q is not finalized: %+v", anchor.AgendaID, anchor)
		}
		topic := nodes[anchor.MaterializedTopicIDs[0]]
		if topic.Kind != "topic" || !topic.Materialized || len(topic.AgendaRefs) != 1 || topic.AgendaRefs[0] != anchor.AgendaID {
			t.Fatalf("agenda anchor %q is not bidirectionally linked: %+v", anchor.AgendaID, topic)
		}
	}
	for _, entry := range live.AgendaProgress.Entries {
		if entry.ComputedStatus != "discussed" || entry.LinkState != "materialized-topic" {
			t.Fatalf("agenda progress %q is not finalized: %+v", entry.ID, entry)
		}
	}
}
