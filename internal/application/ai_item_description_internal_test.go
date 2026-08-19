package application

import (
	"testing"

	"deciscope-core-api/internal/domain"
)

func TestRepairPersistedItemDescriptionsMovesGroundedDeadlineOutOfRiskHeadline(t *testing.T) {
	item := liveAnalysisItem{
		ID:                  "vpn-risk",
		Kind:                "risk",
		Title:               "VPN証明書は来月末に期限切れとなり、放置するとリモート接続ができなくなる可能性があります",
		Body:                "VPN証明書は来月末に期限切れとなり、放置するとリモート接続ができなくなる可能性があります",
		EvidenceSequenceNos: []int64{1, 2},
	}
	state := liveAnalysisPayload{
		Items: []liveAnalysisItem{item},
		Tree: &liveAnalysisTree{Nodes: []liveAnalysisTreeNode{
			{ID: treeRootNodeID, Kind: "topic", Label: "会議全体"},
			{ID: item.ID, Kind: item.Kind, ParentID: treeRootNodeID, Label: item.Title, Description: item.Body},
		}},
	}
	scope := newLiveEvidenceScope()
	scope.Allowed[1] = struct{}{}
	scope.Allowed[2] = struct{}{}
	scope.TranscriptText[1] = "VPN証明書は来月末に期限切れとなります。"
	scope.TranscriptText[2] = "放置するとリモート接続ができなくなる可能性があります。"
	scope.Segments[1] = domain.TranscriptSegment{SequenceNo: 1, IsFinal: true, Text: scope.TranscriptText[1]}
	scope.Segments[2] = domain.TranscriptSegment{SequenceNo: 2, IsFinal: true, Text: scope.TranscriptText[2]}
	const headline = "VPN証明書失効によるリモート接続不能リスク"
	if !itemLabelCandidatePreservesSemanticsWithQualifierPolicy(item, headline, scope, false) {
		source := itemLabelSemanticSourceText(item, scope)
		t.Fatalf("headline semantic validation failed: desired=%+v actual=%+v source=%q",
			inferItemSemanticFeatures(liveAnalysisItem{Kind: item.Kind, Title: source, Body: source}, liveEvidenceScope{}),
			inferItemSemanticFeatures(liveAnalysisItem{Kind: item.Kind, Title: headline, Body: headline}, liveEvidenceScope{}),
			source,
		)
	}

	repairPersistedItemDescriptions(&state, scope)

	got := state.Items[0]
	if got.Title != headline {
		t.Fatalf("title = %q", got.Title)
	}
	if got.Body == "" || got.DescriptionResolution == nil ||
		got.DescriptionResolution.Status != descriptionStatusNormal {
		t.Fatalf("description contract = %+v", got)
	}
	if labelCopiesTranscript(got, scope.Segments) {
		t.Fatalf("compressed headline still copies a transcript sentence: %+v", got)
	}
}
