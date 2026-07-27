package application

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestContextPlannerV4SemanticMetadataPreservesV3AgendaIdentity(t *testing.T) {
	fallback := &meetingContext{
		Title: "障害レビュー",
		Agenda: []agendaItem{
			{ID: "agenda-1", Title: "障害状況と原因", Order: 1, Role: agendaRolePrimary},
			{ID: "agenda-2", Title: "復旧対応の確認", Order: 2, Role: agendaRolePrimary},
			{ID: "agenda-3", Title: "復旧対応の標準化", Order: 3, Role: agendaRolePrimary},
			{ID: "agenda-4", Title: "今後の対応事項", Order: 4, Role: agendaRoleActionSummary},
		},
	}
	v3Payload := `{
		"title":"障害レビュー",
		"agendaItems":[
			{"title":"障害状況と原因","order":1,"role":"primary"},
			{"title":"復旧対応の確認","order":2,"role":"primary"},
			{"title":"復旧対応の標準化","order":3,"role":"primary"},
			{"title":"今後の対応事項","order":4,"role":"action_summary"}
		]
	}`
	v4Payload := `{
		"title":"障害レビュー",
		"agendaItems":[
			{"title":"障害状況と原因","description":"通信断の範囲と原因","goal":"原因を共有する","semanticHints":["通信断","設定不備"],"order":1,"role":"primary"},
			{"title":"復旧対応の確認","description":"切り戻しとVLAN修正","goal":"正常化を確認する","semanticHints":["旧スイッチ","許可VLAN"],"order":2,"role":"primary"},
			{"title":"復旧対応の標準化","description":"復旧手順を標準化","goal":"再利用可能な手順にする","semanticHints":["復旧手順","標準化"],"order":3,"role":"primary"},
			{"title":"今後の対応事項","description":"TODOと未解決事項の横断確認","goal":"担当と期限を確認する","semanticHints":["担当","期限"],"order":4,"role":"action_summary"}
		]
	}`
	v3, err := parseContextPlannerResult(v3Payload, fallback)
	if err != nil {
		t.Fatal(err)
	}
	v4, err := parseContextPlannerResult(v4Payload, fallback)
	if err != nil {
		t.Fatal(err)
	}
	type agendaIdentity struct {
		ID    string
		Title string
		Order int
		Role  string
	}
	identity := func(context *meetingContext) []agendaIdentity {
		result := make([]agendaIdentity, 0, len(context.Agenda))
		for _, agenda := range context.Agenda {
			result = append(result, agendaIdentity{
				ID: agenda.ID, Title: agenda.Title, Order: agenda.Order,
				Role: effectiveAgendaRole(agenda.Role, agenda.Title, agenda.Description),
			})
		}
		return result
	}
	if !reflect.DeepEqual(identity(v3), identity(v4)) {
		t.Fatalf("v3 identity=%+v v4 identity=%+v", identity(v3), identity(v4))
	}
	if len(v4.Agenda[1].SemanticHints) != 2 || v4.Agenda[1].Description == "" || v4.Agenda[1].Goal == "" {
		t.Fatalf("v4 semantic metadata=%+v", v4.Agenda[1])
	}
	if v3.Agenda[1].Description != "" || v3.Agenda[1].Goal != "" || len(v3.Agenda[1].SemanticHints) != 0 {
		t.Fatalf("metadata-missing v3 payload was not backward compatible: %+v", v3.Agenda[1])
	}

	// A v4 response that omits one item or repeats the previous title cannot
	// merge/split/reorder the authoritative agenda records.
	malformedRefinement := `{
		"agendaItems":[
			{"title":"障害状況と原因","order":1,"role":"primary"},
			{"title":"障害状況と原因","order":2,"role":"primary"}
		]
	}`
	reconciled, err := parseContextPlannerResult(malformedRefinement, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if got := identity(reconciled); !reflect.DeepEqual(got, identity(fallback)) {
		t.Fatalf("planner merge/omission changed authoritative agenda identity: got=%+v want=%+v", got, identity(fallback))
	}
}

func TestMeetingContextV3StoredPayloadLoadsWithoutSemanticMetadata(t *testing.T) {
	raw := json.RawMessage(`{
		"title":"旧保存会議",
		"agendaItems":[
			{"id":"agenda-1","title":"議題A","order":1,"role":"primary"},
			{"id":"agenda-2","title":"今後の対応事項","order":2,"role":"action_summary"}
		]
	}`)
	context := unmarshalMeetingContext(raw)
	if context == nil || len(context.Agenda) != 2 {
		t.Fatalf("old stored context=%+v", context)
	}
	for _, agenda := range context.Agenda {
		if agenda.Description != "" || agenda.Goal != "" || len(agenda.SemanticHints) != 0 {
			t.Fatalf("old stored agenda unexpectedly gained metadata: %+v", agenda)
		}
	}
	if context.Agenda[0].ID != "agenda-1" ||
		effectiveAgendaRole(context.Agenda[1].Role, context.Agenda[1].Title, "") != agendaRoleActionSummary {
		t.Fatalf("old stored identity/role changed: %+v", context.Agenda)
	}
}
