package application

import (
	"encoding/json"
	"strings"
	"time"
)

const treeAuditPromptVersion = "v8"

type TreeAuditFindingType string

const (
	TreeAuditSubjectMismatch                   TreeAuditFindingType = "subject_mismatch"
	TreeAuditCrossAgendaContamination          TreeAuditFindingType = "cross_agenda_contamination"
	TreeAuditCandidateFragmentation            TreeAuditFindingType = "candidate_fragmentation"
	TreeAuditCandidateMixedSubjects            TreeAuditFindingType = "candidate_mixed_subjects"
	TreeAuditDuplicateDynamicTopic             TreeAuditFindingType = "duplicate_dynamic_topic"
	TreeAuditIncorrectReparent                 TreeAuditFindingType = "incorrect_reparent"
	TreeAuditReferenceEvidenceReparent         TreeAuditFindingType = "reference_evidence_reparent"
	TreeAuditRecapCreatedNewItem               TreeAuditFindingType = "recap_created_new_item"
	TreeAuditRecapCreatedNewCandidate          TreeAuditFindingType = "recap_created_new_candidate"
	TreeAuditFloatingTentativeCandidate        TreeAuditFindingType = "floating_tentative_candidate"
	TreeAuditTopicOutlier                      TreeAuditFindingType = "topic_outlier"
	TreeAuditGroupOutlier                      TreeAuditFindingType = "group_outlier"
	TreeAuditGroupLabelMismatch                TreeAuditFindingType = "group_label_mismatch"
	TreeAuditGroupChurn                        TreeAuditFindingType = "group_churn"
	TreeAuditMissingGroup                      TreeAuditFindingType = "missing_group"
	TreeAuditCandidateShouldPromote            TreeAuditFindingType = "candidate_should_promote"
	TreeAuditCandidateShouldNotPromote         TreeAuditFindingType = "candidate_should_not_promote"
	TreeAuditCandidateShouldFoldIntoTopic      TreeAuditFindingType = "candidate_should_fold_into_existing_topic"
	TreeAuditParentLowConfidence               TreeAuditFindingType = "parent_low_confidence"
	TreeAuditStaleTentative                    TreeAuditFindingType = "stale_tentative"
	TreeAuditLowInformationDecision            TreeAuditFindingType = "low_information_decision"
	TreeAuditSemanticDuplicateSibling          TreeAuditFindingType = "semantic_duplicate_sibling"
	TreeAuditDuplicateCrossKindProposition     TreeAuditFindingType = "duplicate_cross_kind_proposition"
	TreeAuditMissingRequiredTopic              TreeAuditFindingType = "missing_required_topic"
	TreeAuditRecapReferenceContamination       TreeAuditFindingType = "recap_reference_contamination"
	TreeAuditDiscourseOnlyItem                 TreeAuditFindingType = "discourse_only_item"
	TreeAuditLowInformationItem                TreeAuditFindingType = "low_information_item"
	TreeAuditIncompleteDecision                TreeAuditFindingType = "incomplete_decision"
	TreeAuditSemanticDuplicateSiblings         TreeAuditFindingType = "semantic_duplicate_siblings"
	TreeAuditCrossKindDuplicateProposition     TreeAuditFindingType = "cross_kind_duplicate_proposition"
	TreeAuditMissingDynamicTopic               TreeAuditFindingType = "missing_dynamic_topic"
	TreeAuditCandidateSubjectEvidenceMismatch  TreeAuditFindingType = "candidate_subject_evidence_mismatch"
	TreeAuditRecapPromotedCandidate            TreeAuditFindingType = "recap_promoted_candidate"
	TreeAuditOrphanTentativeItem               TreeAuditFindingType = "orphan_tentative_item"
	TreeAuditGenericTitle                      TreeAuditFindingType = "generic_title"
	TreeAuditEvidenceFragmentation             TreeAuditFindingType = "evidence_fragmentation"
	TreeAuditRecapOnlyItem                     TreeAuditFindingType = "recap_only_item"
	TreeAuditDuplicateItem                     TreeAuditFindingType = "duplicate_item"
	TreeAuditSupersededItem                    TreeAuditFindingType = "superseded_item"
	TreeAuditEmptyGroup                        TreeAuditFindingType = "empty_group"
	TreeAuditEmptyUnclassifiedContainer        TreeAuditFindingType = "empty_unclassified_container"
	TreeAuditLowInformationTitle               TreeAuditFindingType = "low_information_title"
	TreeAuditStatusOnlyNode                    TreeAuditFindingType = "status_only_node"
	TreeAuditAnaphoraWithoutReferent           TreeAuditFindingType = "anaphora_without_referent"
	TreeAuditMetaUtteranceNode                 TreeAuditFindingType = "meta_utterance_node"
	TreeAuditMultiplePropositionsCollapsed     TreeAuditFindingType = "multiple_propositions_collapsed"
	TreeAuditDuplicateOrParaphrase             TreeAuditFindingType = "duplicate_or_paraphrase"
	TreeAuditSubtypeMismatch                   TreeAuditFindingType = "subtype_mismatch"
	TreeAuditSemanticKindMismatch              TreeAuditFindingType = "semantic_kind_mismatch"
	TreeAuditPlannedAgendaWithoutEvidence      TreeAuditFindingType = "planned_agenda_materialized_without_evidence"
	TreeAuditDiscussedAgendaMissingTopic       TreeAuditFindingType = "discussed_agenda_missing_topic"
	TreeAuditTopicWithoutActiveAgendaRef       TreeAuditFindingType = "materialized_topic_without_active_agenda_ref"
	TreeAuditDuplicateAgendaMaterialization    TreeAuditFindingType = "duplicate_agenda_materialization"
	TreeAuditEmptyAgendaTopic                  TreeAuditFindingType = "empty_agenda_topic"
	TreeAuditAgendaTopicShouldMergeDynamic     TreeAuditFindingType = "agenda_topic_should_merge_with_dynamic_topic"
	TreeAuditAgendaTopicShouldDematerialize    TreeAuditFindingType = "agenda_topic_should_dematerialize"
	TreeAuditStaleNoAgendaSpan                 TreeAuditFindingType = "stale_no_agenda_span"
	TreeAuditAgendaReentryMissed               TreeAuditFindingType = "agenda_reentry_missed"
	TreeAuditAgendaItemForcedNoAgenda          TreeAuditFindingType = "agenda_item_forced_to_no_agenda"
	TreeAuditUnclassifiedTodoAfterReentry      TreeAuditFindingType = "unclassified_todo_after_agenda_reentry"
	TreeAuditParentChildSameTitle              TreeAuditFindingType = "parent_child_same_title"
	TreeAuditLowInformationChild               TreeAuditFindingType = "low_information_child"
	TreeAuditGenericQuestionWithoutSubject     TreeAuditFindingType = "generic_question_without_subject"
	TreeAuditAgendaTitleCopiedAsItem           TreeAuditFindingType = "agenda_title_copied_as_item"
	TreeAuditMeetingEndAsDecision              TreeAuditFindingType = "meeting_end_as_decision"
	TreeAuditActionSummaryMissingActiveTodos   TreeAuditFindingType = "action_summary_missing_active_todos"
	TreeAuditGenericTopicLabel                 TreeAuditFindingType = "generic_topic_label"
	TreeAuditGenericCandidateLabel             TreeAuditFindingType = "generic_candidate_label"
	TreeAuditTopicLabelNotDerivedFromChildren  TreeAuditFindingType = "topic_label_not_derived_from_children"
	TreeAuditSingleChildGenericTopic           TreeAuditFindingType = "single_child_generic_topic"
	TreeAuditRiskTodoSubjectFragmentation      TreeAuditFindingType = "risk_todo_subject_fragmentation"
	TreeAuditRelatedActionOutsideRiskTopic     TreeAuditFindingType = "related_action_outside_risk_topic"
	TreeAuditLeadingParticleFragment           TreeAuditFindingType = "leading_particle_fragment"
	TreeAuditAnaphoraTargetMissing             TreeAuditFindingType = "anaphora_target_missing"
	TreeAuditIncompleteSTTSegmentItem          TreeAuditFindingType = "incomplete_stt_segment_item"
	TreeAuditDecisionMissingObject             TreeAuditFindingType = "decision_missing_object"
	TreeAuditNoAgendaFalsePositiveFromModifier TreeAuditFindingType = "no_agenda_false_positive_from_modifier"
	// The following six finding types cover final-review-shaped defects that
	// the previous vocabulary could not name precisely: a recap utterance
	// that got as far as promoting its own duplicate dynamic topic, a root
	// with too many direct topic children, a tree that still needs
	// reorganization after final repairs, a promoted dynamic topic that
	// duplicates a still-open candidate's subject, a topic whose one child
	// leaves related items stranded elsewhere, and a newly-promoted topic
	// whose label is generic rather than derived from its evidence.
	TreeAuditRecapOnlyPromotedTopic                    TreeAuditFindingType = "recap_only_promoted_topic"
	TreeAuditRootTopicOverpopulation                   TreeAuditFindingType = "root_topic_overpopulation"
	TreeAuditFinalTreeNeedsReorganization              TreeAuditFindingType = "final_tree_needs_reorganization"
	TreeAuditPromotedTopicCandidateOverlap             TreeAuditFindingType = "promoted_topic_candidate_overlap"
	TreeAuditSingleChildTopicWithRelatedItemsElsewhere TreeAuditFindingType = "single_child_topic_with_related_items_elsewhere"
	TreeAuditGenericAdditionalTopic                    TreeAuditFindingType = "generic_additional_topic"
)

type TreeAuditOperationType string

const (
	TreeAuditMoveItem                  TreeAuditOperationType = "move_item"
	TreeAuditRestorePreviousParent     TreeAuditOperationType = "restore_previous_parent"
	TreeAuditMergeCandidates           TreeAuditOperationType = "merge_candidates"
	TreeAuditFoldCandidateIntoTopic    TreeAuditOperationType = "fold_candidate_into_topic"
	TreeAuditPromoteCandidate          TreeAuditOperationType = "promote_candidate"
	TreeAuditMarkCandidateTentative    TreeAuditOperationType = "mark_candidate_tentative"
	TreeAuditDeactivateCandidate       TreeAuditOperationType = "deactivate_candidate"
	TreeAuditMergeDynamicTopics        TreeAuditOperationType = "merge_dynamic_topics"
	TreeAuditCreateGroup               TreeAuditOperationType = "create_group"
	TreeAuditMoveItemsToGroup          TreeAuditOperationType = "move_items_to_group"
	TreeAuditRenameGroup               TreeAuditOperationType = "rename_group"
	TreeAuditRenameTopic               TreeAuditOperationType = "rename_topic"
	TreeAuditRemoveEmptyGroup          TreeAuditOperationType = "remove_empty_group"
	TreeAuditMergeItems                TreeAuditOperationType = "merge_items"
	TreeAuditRewriteItem               TreeAuditOperationType = "rewrite_item"
	TreeAuditDeactivateItem            TreeAuditOperationType = "deactivate_item"
	TreeAuditSplitCandidate            TreeAuditOperationType = "split_candidate"
	TreeAuditCreateTopicFromCandidate  TreeAuditOperationType = "create_topic_from_candidate"
	TreeAuditAssignItemToCandidate     TreeAuditOperationType = "assign_item_to_candidate"
	TreeAuditChangeEvidenceRole        TreeAuditOperationType = "change_evidence_role"
	TreeAuditMergeFragmentedUtterances TreeAuditOperationType = "merge_fragmented_utterances"
	TreeAuditRewriteItemTitle          TreeAuditOperationType = "rewrite_item_title"
	TreeAuditRewriteItemDescription    TreeAuditOperationType = "rewrite_item_description"
	TreeAuditReclassifyKind            TreeAuditOperationType = "reclassify_kind"
	TreeAuditReclassifySubtype         TreeAuditOperationType = "reclassify_subtype"
	// TreeAuditMoveNode moves a topic/group container node to a new parent
	// (root/topic/group). See treeAuditOperationClassification and its
	// applier in applyOneTreeAuditOperation.
	TreeAuditMoveNode TreeAuditOperationType = "move_node"
)

type treeAuditFinding struct {
	FindingID           string               `json:"findingId"`
	Type                TreeAuditFindingType `json:"type"`
	Severity            string               `json:"severity"`
	NodeIDs             []string             `json:"nodeIds"`
	CurrentParentIDs    []string             `json:"currentParentIds"`
	RelatedNodeIDs      []string             `json:"relatedNodeIds"`
	EvidenceSequenceNos []int64              `json:"evidenceSequenceNos"`
	Reason              string               `json:"reason"`
	Confidence          float64              `json:"confidence"`
}

// treeAuditOperation is the v3 patch schema. Every ID field is a canonical
// machine ID reference (or a resolvable alias handled by
// canonicalizeTreeAuditResponse): TargetCanonicalItemID/TargetCanonicalItemIDs
// point at detail items, TargetCanonicalNodeID/FromParentCanonicalNodeID/
// ToParentCanonicalNodeID point at tree nodes (topic/group containers for
// move_node/rename_group/remove_empty_group), and TargetCandidateID points at
// an emerging (not yet promoted) topic candidate.
type treeAuditOperation struct {
	OperationID               string                 `json:"operationId"`
	Type                      TreeAuditOperationType `json:"type"`
	TargetCanonicalItemID     string                 `json:"targetCanonicalItemId"`
	TargetCanonicalNodeID     string                 `json:"targetCanonicalNodeId"`
	TargetCanonicalItemIDs    []string               `json:"targetCanonicalItemIds"`
	TargetCandidateID         string                 `json:"targetCandidateId"`
	FromParentCanonicalNodeID string                 `json:"fromParentCanonicalNodeId"`
	ToParentCanonicalNodeID   string                 `json:"toParentCanonicalNodeId"`
	Label                     string                 `json:"label"`
	Kind                      string                 `json:"kind"`
	Subtype                   string                 `json:"subtype"`
	Reason                    string                 `json:"reason"`
	Confidence                float64                `json:"confidence"`
	EvidenceSequenceNos       []int64                `json:"evidenceSequenceNos"`
	DependsOnOperationIDs     []string               `json:"dependsOnOperationIds"`
}

type treeAuditResponse struct {
	BasedOnTreeVersion          int64                     `json:"basedOnTreeVersion"`
	Summary                     string                    `json:"summary"`
	Findings                    []treeAuditFinding        `json:"findings"`
	Operations                  []treeAuditOperation      `json:"operations"`
	ParseRejections             []treeAuditParseRejection `json:"-"`
	CanonicalizationCount       int                       `json:"-"`
	CanonicalizedOperationCount int                       `json:"-"`
}

type treeAuditParseRejection struct {
	ElementType string `json:"elementType"`
	ElementID   string `json:"elementId,omitempty"`
	Reason      string `json:"reason"`
}

type treeAuditValidatorEvaluation struct {
	OperationID string                 `json:"operationId"`
	Type        TreeAuditOperationType `json:"type"`
	Result      string                 `json:"result"`
	Reason      string                 `json:"reason,omitempty"`
	// Category is "unsupported" for an operation type with no applier (never
	// applied regardless of confidence); otherwise it is the operation's
	// risk class - "safe", "moderate", or "destructive" (see
	// treeAuditOperationRiskClass/treeAuditEffectiveRiskClass) - which is
	// also what treeAuditRiskConfidenceThreshold derived this operation's
	// EffectiveConfidence gate from. Unlike the previous fixed "unsafe"
	// label, it is populated for every evaluated operation, accepted or
	// rejected, not only on rejection.
	Category           string  `json:"category,omitempty"`
	Valid              bool    `json:"valid"`
	Applied            bool    `json:"applied"`
	CurrentParentScore float64 `json:"currentParentScore,omitempty"`
	NewParentScore     float64 `json:"newParentScore,omitempty"`
	Improvement        float64 `json:"improvement,omitempty"`
	// ModelConfidence is the operation's own self-reported confidence, exactly
	// as the model returned it. EffectiveConfidence is the server-adjusted
	// value the HighConfidenceThreshold gate actually compares against (see
	// treeAuditEffectiveConfidence): for move-type operations it applies
	// bounded structural bonuses/penalties on top of ModelConfidence; for
	// every other operation type it equals ModelConfidence unchanged.
	ModelConfidence     float64 `json:"modelConfidence"`
	EffectiveConfidence float64 `json:"effectiveConfidence"`
}

type treeAuditValidatorResult struct {
	TreeIntegrityValid             bool                           `json:"treeIntegrityValid"`
	Evaluations                    []treeAuditValidatorEvaluation `json:"evaluations"`
	OperationsProposed             int                            `json:"operationsProposed"`
	OperationsValid                int                            `json:"operationsValid"`
	OperationsApplied              int                            `json:"operationsApplied"`
	OperationsRejected             int                            `json:"operationsRejected"`
	StaleOperationsRejected        int                            `json:"staleOperationsRejected"`
	TopicOutliersBefore            int                            `json:"topicOutliersBefore"`
	TopicOutliersAfter             int                            `json:"topicOutliersAfter"`
	CandidateFragmentationBefore   int                            `json:"candidateFragmentationBefore"`
	CandidateFragmentationAfter    int                            `json:"candidateFragmentationAfter"`
	CrossAgendaContaminationBefore int                            `json:"crossAgendaContaminationBefore"`
	CrossAgendaContaminationAfter  int                            `json:"crossAgendaContaminationAfter"`
	HeuristicDefectCountBefore     int                            `json:"heuristicDefectCountBefore"`
	HeuristicDefectCountAfter      int                            `json:"heuristicDefectCountAfter"`
	LowInformationItemsBefore      int                            `json:"lowInformationItemsBefore"`
	LowInformationItemsAfter       int                            `json:"lowInformationItemsAfter"`
	NodeCountBefore                int                            `json:"nodeCountBefore"`
	NodeCountAfter                 int                            `json:"nodeCountAfter"`
	RewritesApplied                int                            `json:"rewritesApplied"`
	MergesApplied                  int                            `json:"mergesApplied"`
	ReclassificationsApplied       int                            `json:"reclassificationsApplied"`
	DeactivationsApplied           int                            `json:"deactivationsApplied"`
	ParserElementsRejected         int                            `json:"parserElementsRejected"`
	OperationsCanonicalized        int                            `json:"operationsCanonicalized"`
}

type treeAuditPrecheckFinding struct {
	Type                TreeAuditFindingType `json:"type"`
	NodeIDs             []string             `json:"nodeIds"`
	RelatedNodeIDs      []string             `json:"relatedNodeIds,omitempty"`
	EvidenceSequenceNos []int64              `json:"evidenceSequenceNos,omitempty"`
	Reason              string               `json:"reason"`
	Score               float64              `json:"score"`
}

type treeAuditEvidenceRole string

const (
	treeAuditEvidencePrimary    treeAuditEvidenceRole = "primary"
	treeAuditEvidenceSupporting treeAuditEvidenceRole = "supporting"
	treeAuditEvidenceReference  treeAuditEvidenceRole = "reference"
)

// treeAuditRiskClass is the three-tier risk classification used to derive
// an operation's confidence gate (see treeAuditOperationRiskClass,
// treeAuditEffectiveRiskClass, treeAuditRiskConfidenceThreshold in
// ai_tree_audit_validator.go). It replaces the previous single flat
// HighConfidenceThreshold gate shared by every applicable operation type,
// which rejected most of the model's real-world proposals (typically
// reported in the 0.70-0.85 range) even though their underlying change was
// low-risk (a title rewrite) rather than high-risk (deactivating an item).
type treeAuditRiskClass string

const (
	treeAuditRiskSafe        treeAuditRiskClass = "safe"
	treeAuditRiskModerate    treeAuditRiskClass = "moderate"
	treeAuditRiskDestructive treeAuditRiskClass = "destructive"
)

type treeAuditEvidenceSegment struct {
	SequenceNo int64                 `json:"sequenceNo"`
	Speaker    string                `json:"speaker,omitempty"`
	Text       string                `json:"text"`
	Role       treeAuditEvidenceRole `json:"role"`
}

// TreeAuditConfig bounds scheduling, model input/output, persistence and
// semantic movement. Zero values are normalized to conservative defaults.
type TreeAuditConfig struct {
	Enabled                    bool
	IntervalVersions           int64
	Interval                   time.Duration
	MinInterval                time.Duration
	MaxRunsPerSession          int
	MaxRunsPerHour             int
	HighSeverityMinInterval    time.Duration
	HighSeverityMaxRunsPerHour int
	Timeout                    time.Duration
	MaxOutputTokens            int
	MaxNodes                   int
	MaxRecentSegments          int
	MaxEvidenceSegments        int
	MaxInputTokens             int
	MaxPersistedJSONBytes      int
	HighConfidenceThreshold    float64
	RequiredImprovementMargin  float64
	CohesionThreshold          float64
	TentativeMaxVersions       int64
	UnappliedWarningThreshold  int
}

func (c TreeAuditConfig) normalized() TreeAuditConfig {
	if c.IntervalVersions <= 0 {
		c.IntervalVersions = 3
	}
	if c.Interval <= 0 {
		c.Interval = 5 * time.Minute
	}
	if c.MinInterval <= 0 {
		c.MinInterval = 5 * time.Minute
	}
	if c.MaxRunsPerSession <= 0 {
		c.MaxRunsPerSession = 20
	}
	if c.MaxRunsPerHour <= 0 {
		c.MaxRunsPerHour = 12
	}
	if c.HighSeverityMinInterval <= 0 {
		c.HighSeverityMinInterval = time.Minute
	}
	if c.HighSeverityMaxRunsPerHour <= 0 {
		c.HighSeverityMaxRunsPerHour = 4
	}
	if c.Timeout <= 0 {
		c.Timeout = 25 * time.Second
	}
	if c.MaxOutputTokens <= 0 {
		c.MaxOutputTokens = 2500
	}
	if c.MaxNodes <= 0 {
		c.MaxNodes = 80
	}
	if c.MaxRecentSegments <= 0 {
		c.MaxRecentSegments = 16
	}
	if c.MaxEvidenceSegments <= 0 {
		c.MaxEvidenceSegments = 24
	}
	if c.MaxInputTokens <= 0 {
		c.MaxInputTokens = 12000
	}
	if c.MaxPersistedJSONBytes <= 0 {
		c.MaxPersistedJSONBytes = 256 * 1024
	}
	if c.HighConfidenceThreshold <= 0 || c.HighConfidenceThreshold > 1 {
		c.HighConfidenceThreshold = 0.90
	}
	if c.RequiredImprovementMargin <= 0 || c.RequiredImprovementMargin > 1 {
		c.RequiredImprovementMargin = 0.18
	}
	if c.CohesionThreshold <= 0 || c.CohesionThreshold >= 1 {
		c.CohesionThreshold = 0.20
	}
	if c.TentativeMaxVersions <= 0 {
		c.TentativeMaxVersions = 3
	}
	if c.UnappliedWarningThreshold <= 0 {
		c.UnappliedWarningThreshold = 3
	}
	return c
}

func (c TreeAuditConfig) active() bool {
	c = c.normalized()
	return c.Enabled
}

func validTreeAuditFindingType(value TreeAuditFindingType) bool {
	switch value {
	case TreeAuditSubjectMismatch, TreeAuditCrossAgendaContamination,
		TreeAuditCandidateFragmentation, TreeAuditCandidateMixedSubjects,
		TreeAuditDuplicateDynamicTopic, TreeAuditIncorrectReparent,
		TreeAuditReferenceEvidenceReparent, TreeAuditRecapCreatedNewItem,
		TreeAuditRecapCreatedNewCandidate, TreeAuditFloatingTentativeCandidate,
		TreeAuditTopicOutlier, TreeAuditGroupOutlier, TreeAuditGroupLabelMismatch,
		TreeAuditGroupChurn, TreeAuditMissingGroup, TreeAuditCandidateShouldPromote,
		TreeAuditCandidateShouldNotPromote, TreeAuditCandidateShouldFoldIntoTopic,
		TreeAuditParentLowConfidence, TreeAuditStaleTentative:
		fallthrough
	case TreeAuditLowInformationDecision, TreeAuditSemanticDuplicateSibling,
		TreeAuditDuplicateCrossKindProposition, TreeAuditMissingRequiredTopic,
		TreeAuditRecapReferenceContamination, TreeAuditDiscourseOnlyItem,
		TreeAuditLowInformationItem, TreeAuditIncompleteDecision,
		TreeAuditSemanticDuplicateSiblings, TreeAuditCrossKindDuplicateProposition,
		TreeAuditMissingDynamicTopic, TreeAuditCandidateSubjectEvidenceMismatch,
		TreeAuditRecapPromotedCandidate, TreeAuditOrphanTentativeItem,
		TreeAuditGenericTitle, TreeAuditEvidenceFragmentation,
		TreeAuditRecapOnlyItem, TreeAuditDuplicateItem, TreeAuditSupersededItem,
		TreeAuditEmptyGroup, TreeAuditEmptyUnclassifiedContainer,
		TreeAuditLowInformationTitle, TreeAuditStatusOnlyNode,
		TreeAuditAnaphoraWithoutReferent, TreeAuditMetaUtteranceNode,
		TreeAuditMultiplePropositionsCollapsed, TreeAuditDuplicateOrParaphrase,
		TreeAuditSubtypeMismatch, TreeAuditSemanticKindMismatch:
		fallthrough
	case TreeAuditPlannedAgendaWithoutEvidence, TreeAuditDiscussedAgendaMissingTopic,
		TreeAuditTopicWithoutActiveAgendaRef, TreeAuditDuplicateAgendaMaterialization,
		TreeAuditEmptyAgendaTopic, TreeAuditAgendaTopicShouldMergeDynamic,
		TreeAuditAgendaTopicShouldDematerialize, TreeAuditStaleNoAgendaSpan,
		TreeAuditAgendaReentryMissed, TreeAuditAgendaItemForcedNoAgenda,
		TreeAuditUnclassifiedTodoAfterReentry, TreeAuditParentChildSameTitle,
		TreeAuditLowInformationChild, TreeAuditGenericQuestionWithoutSubject,
		TreeAuditAgendaTitleCopiedAsItem, TreeAuditMeetingEndAsDecision,
		TreeAuditActionSummaryMissingActiveTodos, TreeAuditGenericTopicLabel,
		TreeAuditGenericCandidateLabel, TreeAuditTopicLabelNotDerivedFromChildren,
		TreeAuditSingleChildGenericTopic, TreeAuditRiskTodoSubjectFragmentation,
		TreeAuditRelatedActionOutsideRiskTopic, TreeAuditLeadingParticleFragment,
		TreeAuditAnaphoraTargetMissing, TreeAuditIncompleteSTTSegmentItem,
		TreeAuditDecisionMissingObject, TreeAuditNoAgendaFalsePositiveFromModifier:
		fallthrough
	case TreeAuditRecapOnlyPromotedTopic, TreeAuditRootTopicOverpopulation,
		TreeAuditFinalTreeNeedsReorganization, TreeAuditPromotedTopicCandidateOverlap,
		TreeAuditSingleChildTopicWithRelatedItemsElsewhere, TreeAuditGenericAdditionalTopic:
		return true
	default:
		return false
	}
}

func validTreeAuditOperationType(value TreeAuditOperationType) bool {
	switch value {
	case TreeAuditMoveItem, TreeAuditRestorePreviousParent, TreeAuditMergeCandidates,
		TreeAuditFoldCandidateIntoTopic, TreeAuditPromoteCandidate,
		TreeAuditMarkCandidateTentative, TreeAuditDeactivateCandidate,
		TreeAuditMergeDynamicTopics, TreeAuditCreateGroup,
		TreeAuditMoveItemsToGroup, TreeAuditRenameGroup, TreeAuditRenameTopic, TreeAuditRemoveEmptyGroup:
		fallthrough
	case TreeAuditMergeItems, TreeAuditRewriteItem, TreeAuditDeactivateItem,
		TreeAuditSplitCandidate, TreeAuditCreateTopicFromCandidate,
		TreeAuditAssignItemToCandidate, TreeAuditChangeEvidenceRole,
		TreeAuditMergeFragmentedUtterances, TreeAuditRewriteItemTitle,
		TreeAuditRewriteItemDescription, TreeAuditReclassifyKind,
		TreeAuditReclassifySubtype, TreeAuditMoveNode:
		return true
	default:
		return false
	}
}

func boundedAuditJSON(value any, maxBytes int) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	if maxBytes <= 0 || len(encoded) <= maxBytes {
		return encoded
	}
	return json.RawMessage(`{"truncated":true,"reason":"size_limit"}`)
}

func boundedAuditText(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	remaining := maxBytes
	var builder strings.Builder
	for _, r := range value {
		size := len(string(r))
		if size > remaining {
			break
		}
		builder.WriteRune(r)
		remaining -= size
	}
	return builder.String()
}

func normalizedAuditReason(value string) string {
	return truncateRunes(strings.TrimSpace(value), 300)
}
