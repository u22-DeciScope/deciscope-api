package application

import "encoding/json"

// MeetingQualitySuite is a deterministic, network-free collection of meeting
// replays. FixedAIResponse is fed through the production live merge pipeline;
// expectations intentionally describe semantics instead of generated IDs or
// array positions.
type MeetingQualitySuite struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Name          string                   `json:"name"`
	Scenarios     []MeetingQualityScenario `json:"scenarios"`
}

type MeetingQualityScenario struct {
	ID                        string                              `json:"id"`
	Description               string                              `json:"description"`
	TranscriptSegments        []MeetingQualityTranscriptSegment   `json:"transcriptSegments"`
	MeetingContext            MeetingQualityMeetingContext        `json:"meetingContext"`
	SeedPayload               json.RawMessage                     `json:"seedPayload,omitempty"`
	Rounds                    []MeetingQualityRound               `json:"rounds"`
	RequiredPropositions      []MeetingQualityProposition         `json:"requiredPropositions"`
	RequiredRelations         []MeetingQualityRelation            `json:"requiredRelations,omitempty"`
	RequiredParentSeparations []MeetingQualityParentSeparation    `json:"requiredParentSeparations,omitempty"`
	ForbiddenResults          []MeetingQualityForbiddenResult     `json:"forbiddenResults,omitempty"`
	SafetyExpectations        []MeetingQualitySafetyExpectation   `json:"safetyExpectations,omitempty"`
	FinalCoverage             int64                               `json:"finalCoverage"`
	ApplyFinalRepair          bool                                `json:"applyFinalRepair"`
	Classification            MeetingQualityClassificationOptions `json:"classification,omitempty"`
}

type MeetingQualityTranscriptSegment struct {
	SequenceNo int64  `json:"sequenceNo"`
	CallID     string `json:"callId,omitempty"`
	Speaker    string `json:"speaker,omitempty"`
	Text       string `json:"text"`
	IsFinal    *bool  `json:"isFinal,omitempty"`
}

type MeetingQualityMeetingContext struct {
	Title      string                     `json:"title,omitempty"`
	Purpose    string                     `json:"purpose,omitempty"`
	Background string                     `json:"background,omitempty"`
	Agenda     []MeetingQualityAgendaItem `json:"agendaItems,omitempty"`
	Directives []string                   `json:"aiDirectives,omitempty"`
}

type MeetingQualityAgendaItem struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Description   string   `json:"description,omitempty"`
	Goal          string   `json:"goal,omitempty"`
	SemanticHints []string `json:"semanticHints,omitempty"`
	Order         int      `json:"order"`
	Role          string   `json:"role,omitempty"`
}

type MeetingQualityRound struct {
	SequenceNos     []int64         `json:"sequenceNos"`
	FixedAIResponse json.RawMessage `json:"fixedAIResponse"`
}

type MeetingQualityClassificationOptions struct {
	AgendaAssignmentThreshold float64 `json:"agendaAssignmentThreshold,omitempty"`
	PromotionMinItems         int     `json:"promotionMinItems,omitempty"`
	PromotionMinRounds        int     `json:"promotionMinRounds,omitempty"`
	MaxDynamicTopics          int     `json:"maxDynamicTopics,omitempty"`
}

type MeetingQualityProposition struct {
	ID                        string   `json:"id"`
	Text                      string   `json:"text"`
	RequiredKind              string   `json:"requiredKind,omitempty"`
	AllowedKinds              []string `json:"allowedKinds,omitempty"`
	EvidenceSequenceNos       []int64  `json:"evidenceSequenceNos,omitempty"`
	RequiredAgendaID          string   `json:"requiredAgendaId,omitempty"`
	RequiredTemporalScope     string   `json:"requiredTemporalScope,omitempty"`
	RequiredEpistemicStatus   string   `json:"requiredEpistemicStatus,omitempty"`
	RequiredStatus            string   `json:"requiredStatus,omitempty"`
	RequiredDescriptionStatus string   `json:"requiredDescriptionStatus,omitempty"`
	MinimumSimilarity         float64  `json:"minimumSimilarity,omitempty"`
}

// Relation is evaluated against the semantic proposition matches. Explicit
// tree.relations are preferred. A relation may also be represented by the
// existing hierarchy when RequireSameBranch is true; this lets the evaluator
// express logical expectations before the persisted schema gains first-class
// logical edges.
type MeetingQualityRelation struct {
	From              string `json:"from"`
	To                string `json:"to"`
	Kind              string `json:"kind"`
	RequireSameBranch bool   `json:"requireSameBranch,omitempty"`
	RequiredAncestor  string `json:"requiredAncestor,omitempty"`
}

// MeetingQualityParentSeparation requires two matched propositions to retain
// different top-level discussion subjects. It expresses cases where lexical
// proximity must not collapse distinct business objects into one candidate.
type MeetingQualityParentSeparation struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type MeetingQualityForbiddenResult struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Kind     string `json:"kind,omitempty"`
	AgendaID string `json:"agendaId,omitempty"`
}

type MeetingQualitySafetyExpectation struct {
	Principle     string `json:"principle"`
	PropositionID string `json:"propositionId,omitempty"`
}

type MeetingQualityMetrics struct {
	RequiredPropositionRecall                 float64 `json:"requiredPropositionRecall"`
	UnsupportedPropositionCount               int     `json:"unsupportedPropositionCount"`
	ClassificationAccuracy                    float64 `json:"classificationAccuracy"`
	TemporalScopeAccuracy                     float64 `json:"temporalScopeAccuracy"`
	PastFactCount                             int     `json:"pastFactCount"`
	IssueCount                                int     `json:"issueCount"`
	ResolvedIssueCount                        int     `json:"resolvedIssueCount"`
	IncorrectResolvedIssueCount               int     `json:"incorrectResolvedIssueCount"`
	RiskRecall                                float64 `json:"riskRecall"`
	TodoRecall                                float64 `json:"todoRecall"`
	DecisionRecall                            float64 `json:"decisionRecall"`
	SemanticDuplicateCount                    int     `json:"semanticDuplicateCount"`
	SemanticSplitSourceActiveCount            int     `json:"semantic_split_source_active_count"`
	SemanticSplitSourceFragmentDuplicateCount int     `json:"semantic_split_source_fragment_duplicate_count"`
	SemanticSplitReplacementMissingCount      int     `json:"semantic_split_replacement_missing_count"`
	RecapDuplicateItemCount                   int     `json:"recap_duplicate_item_count"`
	RecapExistingItemUpdateCount              int     `json:"recap_existing_item_update_count"`
	RecapNewInformationItemCount              int     `json:"recap_new_information_item_count"`
	LowInformationLabelCount                  int     `json:"lowInformationLabelCount"`
	ContextDependentLabelCount                int     `json:"contextDependentLabelCount"`
	TruncatedLabelCount                       int     `json:"truncatedLabelCount"`
	HierarchyRelationAccuracy                 float64 `json:"hierarchyRelationAccuracy"`
	CandidateFragmentationCount               int     `json:"candidateFragmentationCount"`
	CrossAgendaContaminationCount             int     `json:"crossAgendaContaminationCount"`
	LabelDescriptionExactDuplicateCount       int     `json:"label_description_exact_duplicate_count"`
	LabelDescriptionHighSimilarityCount       int     `json:"label_description_high_similarity_count"`
	DescriptionUnsupportedAtomCount           int     `json:"description_unsupported_atom_count"`
	DescriptionAddedGroundedDetailCount       int     `json:"description_added_grounded_detail_count"`
	LabelTranscriptCopyRatio                  float64 `json:"label_transcript_copy_ratio"`
	LabelCompressionRatio                     float64 `json:"label_compression_ratio"`
	DescriptionRedundantCount                 int     `json:"description_redundant_count"`
}

type MeetingQualityParentAssignment struct {
	PropositionID string   `json:"propositionId"`
	Kind          string   `json:"kind"`
	ParentPath    []string `json:"parentPath,omitempty"`
}

type MeetingQualityKindCount struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

type MeetingQualityActualItem struct {
	ID                  string  `json:"id"`
	Kind                string  `json:"kind"`
	Title               string  `json:"title"`
	Body                string  `json:"body,omitempty"`
	Status              string  `json:"status,omitempty"`
	EvidenceSequenceNos []int64 `json:"evidenceSequenceNos,omitempty"`
}

type MeetingQualityPropositionMatch struct {
	PropositionID             string                    `json:"propositionId"`
	ExpectedText              string                    `json:"expectedText"`
	RequiredKind              string                    `json:"requiredKind,omitempty"`
	AllowedKinds              []string                  `json:"allowedKinds,omitempty"`
	ExpectedEvidence          []int64                   `json:"expectedEvidence,omitempty"`
	RequiredTemporalScope     string                    `json:"requiredTemporalScope,omitempty"`
	RequiredEpistemicStatus   string                    `json:"requiredEpistemicStatus,omitempty"`
	RequiredStatus            string                    `json:"requiredStatus,omitempty"`
	RequiredDescriptionStatus string                    `json:"requiredDescriptionStatus,omitempty"`
	Matched                   bool                      `json:"matched"`
	Similarity                float64                   `json:"similarity"`
	BestActualCandidate       *MeetingQualityActualItem `json:"bestActualCandidate,omitempty"`
}

type MeetingQualityKindMismatch struct {
	PropositionID string   `json:"propositionId"`
	ExpectedKinds []string `json:"expectedKinds"`
	ActualItemID  string   `json:"actualItemId"`
	ActualKind    string   `json:"actualKind"`
}

type MeetingQualityEvidenceMismatch struct {
	PropositionID    string  `json:"propositionId"`
	ActualItemID     string  `json:"actualItemId"`
	ExpectedSequence int64   `json:"expectedSequence"`
	ActualSequences  []int64 `json:"actualSequences,omitempty"`
}

type MeetingQualitySemanticStateMismatch struct {
	PropositionID string `json:"propositionId"`
	ActualItemID  string `json:"actualItemId"`
	Field         string `json:"field"`
	Expected      string `json:"expected"`
	Actual        string `json:"actual"`
}

type MeetingQualityMetricEvidence struct {
	Metric         string   `json:"metric"`
	ExpectationIDs []string `json:"expectationIds,omitempty"`
	ActualItemIDs  []string `json:"actualItemIds,omitempty"`
	ActualLabels   []string `json:"actualLabels,omitempty"`
	Reason         string   `json:"reason"`
}

type MeetingQualityScenarioResult struct {
	ID                          string                                `json:"id"`
	Passed                      bool                                  `json:"passed"`
	Metrics                     MeetingQualityMetrics                 `json:"metrics"`
	InputMode                   string                                `json:"inputMode,omitempty"`
	ProductionStages            []string                              `json:"productionStages,omitempty"`
	HardInvariantViolations     []string                              `json:"hardInvariantViolations,omitempty"`
	MissingRequiredPropositions []string                              `json:"missingRequiredPropositions,omitempty"`
	UnsupportedPropositions     []string                              `json:"unsupportedPropositions,omitempty"`
	PropositionMatches          []MeetingQualityPropositionMatch      `json:"propositionMatches,omitempty"`
	UnsupportedItems            []MeetingQualityActualItem            `json:"unsupportedItems,omitempty"`
	KindMismatches              []MeetingQualityKindMismatch          `json:"kindMismatches,omitempty"`
	EvidenceMismatches          []MeetingQualityEvidenceMismatch      `json:"evidenceMismatches,omitempty"`
	SemanticStateMismatches     []MeetingQualitySemanticStateMismatch `json:"semanticStateMismatches,omitempty"`
	MetricEvidence              []MeetingQualityMetricEvidence        `json:"metricEvidence,omitempty"`
	RelationFailures            []string                              `json:"relationFailures,omitempty"`
	ForbiddenResultsFound       []string                              `json:"forbiddenResultsFound,omitempty"`
	SafetyFailures              []string                              `json:"safetyFailures,omitempty"`
	ParentAssignments           []MeetingQualityParentAssignment      `json:"parentAssignments,omitempty"`
	RequiredParentSeparations   []MeetingQualityParentSeparation      `json:"requiredParentSeparations,omitempty"`
	KindDistribution            []MeetingQualityKindCount             `json:"kindDistribution,omitempty"`
	FinalCoverage               int64                                 `json:"finalCoverage"`
	TreeVersion                 int64                                 `json:"treeVersion"`
	Error                       string                                `json:"error,omitempty"`
}

type MeetingQualitySuiteReport struct {
	SchemaVersion int                            `json:"schemaVersion"`
	Suite         string                         `json:"suite"`
	Passed        bool                           `json:"passed"`
	Scenarios     []MeetingQualityScenarioResult `json:"scenarios"`
}

type MeetingQualityBaseline struct {
	SchemaVersion int                            `json:"schemaVersion"`
	Suite         string                         `json:"suite"`
	MetricSchema  []string                       `json:"metricSchema"`
	Scenarios     []MeetingQualityScenarioResult `json:"scenarios"`
}

type MeetingQualityMetricChange struct {
	Scenario string  `json:"scenario"`
	Metric   string  `json:"metric"`
	Before   float64 `json:"before"`
	After    float64 `json:"after"`
}

type MeetingQualityTextDiff struct {
	Scenario string   `json:"scenario"`
	Values   []string `json:"values"`
}

type MeetingQualityParentDiff struct {
	Scenario string                           `json:"scenario"`
	Before   []MeetingQualityParentAssignment `json:"before"`
	After    []MeetingQualityParentAssignment `json:"after"`
}

type MeetingQualityKindDistributionDiff struct {
	Scenario string                    `json:"scenario"`
	Before   []MeetingQualityKindCount `json:"before"`
	After    []MeetingQualityKindCount `json:"after"`
}

type MeetingQualityComparisonReport struct {
	Passed                     bool                                 `json:"passed"`
	BaselineUpdateRequired     bool                                 `json:"baselineUpdateRequired"`
	NewScenarios               []string                             `json:"newScenarios,omitempty"`
	ImprovedMetrics            []MeetingQualityMetricChange         `json:"improvedMetrics,omitempty"`
	WorsenedMetrics            []MeetingQualityMetricChange         `json:"worsenedMetrics,omitempty"`
	NewFailures                []string                             `json:"newFailures,omitempty"`
	RepairedScenarios          []string                             `json:"repairedScenarios,omitempty"`
	LostRequiredPropositions   []MeetingQualityTextDiff             `json:"lostRequiredPropositions,omitempty"`
	NewUnsupportedPropositions []MeetingQualityTextDiff             `json:"newUnsupportedPropositions,omitempty"`
	NewHardInvariantViolations []MeetingQualityTextDiff             `json:"newHardInvariantViolations,omitempty"`
	NewRelationFailures        []MeetingQualityTextDiff             `json:"newRelationFailures,omitempty"`
	NewKindMismatches          []MeetingQualityTextDiff             `json:"newKindMismatches,omitempty"`
	NewEvidenceMismatches      []MeetingQualityTextDiff             `json:"newEvidenceMismatches,omitempty"`
	NewSemanticStateMismatches []MeetingQualityTextDiff             `json:"newSemanticStateMismatches,omitempty"`
	ParentRelationDiffs        []MeetingQualityParentDiff           `json:"parentRelationDiffs,omitempty"`
	KindDistributionDiffs      []MeetingQualityKindDistributionDiff `json:"kindDistributionDiffs,omitempty"`
}

type MeetingQualityBaselineUpdateReport struct {
	AppliedMetrics    []MeetingQualityMetricChange `json:"appliedMetrics,omitempty"`
	AppliedRepairs    []string                     `json:"appliedRepairs,omitempty"`
	AddedScenarios    []string                     `json:"addedScenarios,omitempty"`
	AddedMetricSchema []string                     `json:"addedMetricSchema,omitempty"`
	UnchangedBaseline bool                         `json:"unchangedBaseline"`
}
