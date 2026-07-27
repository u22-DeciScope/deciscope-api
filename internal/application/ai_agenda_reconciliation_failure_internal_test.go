package application

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestFinalAgendaReconciliationFailureInjectionRollsBackEveryStage(t *testing.T) {
	raw, mc, segments := finalReconciliationFixture(
		t,
		"切り戻しと設定修正でサービスを正常化",
		"旧スイッチへ切り戻し、トランク設定と許可VLANを修正した",
		"復旧対応として旧スイッチへ切り戻し、トランク設定と許可VLANを修正して各サービスの正常化を確認しました。",
	)
	original := previousLiveAnalysisState(raw)
	original.AgendaProgress.Entries[0].ManualStatus = agendaProgressDiscussing
	original.AgendaProgress.Entries[0].EffectiveStatus = agendaProgressDiscussing
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	stages := []agendaFinalizationStage{
		agendaFinalizationStageMeetingContext,
		agendaFinalizationStageTranscript,
		agendaFinalizationStageTopicRepair,
		agendaFinalizationStageAgendaRefs,
		agendaFinalizationStageProgress,
		agendaFinalizationStageIntegrity,
	}
	for _, stage := range stages {
		t.Run(string(stage), func(t *testing.T) {
			finalized, decisions, gotErr := finalizeAgendaLifecyclePayloadWithEvidenceAndHook(
				raw, mc, 12, segments,
				func(current agendaFinalizationStage) error {
					if current == stage {
						return errors.New("injected " + string(stage))
					}
					return nil
				},
			)
			if gotErr == nil || !strings.Contains(gotErr.Error(), string(stage)) {
				t.Fatalf("error=%v, want injected %s failure", gotErr, stage)
			}
			rolledBack := previousLiveAnalysisState(finalized)
			if !reflect.DeepEqual(rolledBack.Tree, original.Tree) ||
				!reflect.DeepEqual(rolledBack.Items, original.Items) ||
				!reflect.DeepEqual(rolledBack.AgendaAnchors, original.AgendaAnchors) ||
				!reflect.DeepEqual(rolledBack.AgendaProgress, original.AgendaProgress) {
				t.Fatalf("partial reconciliation survived %s failure: rollback=%+v original=%+v", stage, rolledBack, original)
			}
			if len(decisions) == 0 ||
				!strings.Contains(decisions[len(decisions)-1].RejectedReason, string(stage)) {
				t.Fatalf("failure reason missing for %s: %+v", stage, decisions)
			}
		})
	}
}

func TestFinalAgendaReconciliationMissingInputsFailOpen(t *testing.T) {
	raw, mc, _ := finalReconciliationFixture(
		t,
		"切り戻しと設定修正でサービスを正常化",
		"旧スイッチへ切り戻し、トランク設定と許可VLANを修正した",
		"",
	)
	withoutTranscript, decisions, err := finalizeAgendaLifecyclePayloadWithEvidence(raw, mc, 12, nil)
	if err != nil {
		t.Fatalf("missing transcript stopped finalization: %v", err)
	}
	transcriptState := previousLiveAnalysisState(withoutTranscript)
	if agendaTopicNodeByRef(transcriptState.Tree, "agenda-2") != nil {
		t.Fatalf("missing transcript created a partial repair: %+v", transcriptState.Tree)
	}
	if len(decisions) == 0 || decisions[0].RejectedReason != "no_final_transcript_evidence" {
		t.Fatalf("missing transcript reason=%+v", decisions)
	}

	withoutContext, _, err := finalizeAgendaLifecyclePayloadWithEvidence(raw, nil, 12, nil)
	if err != nil {
		t.Fatalf("missing meeting context stopped finalization: %v", err)
	}
	contextState := previousLiveAnalysisState(withoutContext)
	if !reflect.DeepEqual(contextState.Tree, previousLiveAnalysisState(raw).Tree) {
		t.Fatalf("missing context changed the original tree: %+v", contextState.Tree)
	}
	if entry := reconciliationProgressEntryByID(contextState.AgendaProgress, "agenda-2"); entry == nil ||
		entry.ComputedStatus != agendaProgressNotStarted {
		t.Fatalf("missing context lost agenda progress: %+v", contextState.AgendaProgress)
	}
}

func TestFinalAgendaReconciliationIntegrityRejectionKeepsOriginalTree(t *testing.T) {
	raw, mc, segments := finalReconciliationFixture(
		t,
		"切り戻しと設定修正でサービスを正常化",
		"旧スイッチへ切り戻し、トランク設定と許可VLANを修正した",
		"復旧対応として旧スイッチへ切り戻し、トランク設定と許可VLANを修正して各サービスの正常化を確認しました。",
	)
	original := previousLiveAnalysisState(raw)
	original.Tree.Nodes = append(original.Tree.Nodes, liveAnalysisTreeNode{
		ID: "topic-unknown-agenda", Kind: "topic", ParentID: treeRootNodeID,
		Label: "invalid agenda reference", AgendaRefs: []string{"agenda-missing"}, Materialized: true,
	})
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	finalized, decisions, err := finalizeAgendaLifecyclePayloadWithEvidence(raw, mc, 12, segments)
	if err != nil {
		t.Fatal(err)
	}
	state := previousLiveAnalysisState(finalized)
	if agendaTopicNodeByRef(state.Tree, "agenda-2") != nil ||
		itemTopicID(state.Tree, "fact-recovery") != "topic-dynamic-recovery" {
		t.Fatalf("integrity rejection persisted a partial repair: %+v", state.Tree)
	}
	found := false
	for _, decision := range decisions {
		if decision.RejectedReason == "tree_integrity_rejected" {
			found = true
		}
	}
	if !found || !state.Degraded || state.DegradedReason != "final_agenda_integrity_rejected" {
		t.Fatalf("integrity rejection observability missing: decisions=%+v state=%+v", decisions, state)
	}
}
