package application

import (
	"encoding/json"
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestLogicalTopicAgendaAliasResolvesToMaterializedAgendaNode(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{
		ID: "agenda-1", Title: "試験導入の対象部署", Order: 1, Role: agendaRolePrimary,
	}}}
	diff := `{
		"summary":"対象部署を確定","currentTopic":"",
		"items":[{"id":"decision-target","kind":"decision","severity":"high","title":"営業部の5人を試験対象にする","body":"営業部の5人を対象に試験する","status":"open"}],
		"newTopics":[],
		"assignments":[{"nodeId":"decision-target","parentTopicId":"topic-agenda-1","confidence":0.94}]
	}`
	merged := mergeForTestWithContext(t, diff, nil, mc)
	topic := agendaTopicNodeByRef(merged.Tree, "agenda-1")
	item := treeNodeByID(merged.Tree, "decision-target")
	if topic == nil || item == nil || item.ParentID != topic.ID || item.ParentID == treeUnclassifiedTopicID {
		t.Fatalf("topic=%+v item=%+v", topic, item)
	}
}

func TestDeliveryOmitsVirtualActionSummaryIdentifierButKeepsCanonicalAgendaReference(t *testing.T) {
	mc := &meetingContext{Agenda: []agendaItem{{
		ID: "agenda-1", Title: "セキュリティ上の懸念", Order: 1, Role: agendaRolePrimary,
	}}}
	state := liveAnalysisPayload{Items: []liveAnalysisItem{{
		ID: "todo-security", Kind: "todo", Title: "セキュリティルールを確認する",
		Status: "open", ProjectionStatus: "stable",
		RelatedAgendaIDs: []string{"agenda-1", virtualActionSummaryProjectionID},
	}}}
	projected := stableProjectionItemsForDelivery(state.Items)
	if len(projected) != 1 || len(projected[0].RelatedAgendaIDs) != 1 || projected[0].RelatedAgendaIDs[0] != "agenda-1" {
		t.Fatalf("projected relatedAgendaIds=%v", projected[0].RelatedAgendaIDs)
	}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	delivered := sanitizeLiveAnalysisForDelivery(&domain.MeetingAIAnalysis{Payload: payload}, mc, TreeClassificationConfig{})
	parsed := previousLiveAnalysisState(delivered.Payload)
	if len(parsed.Items) != 1 || containsExactString(parsed.Items[0].RelatedAgendaIDs, virtualActionSummaryProjectionID) {
		t.Fatalf("relatedAgendaIds=%v", parsed.Items[0].RelatedAgendaIDs)
	}
}
