package application

import (
	"encoding/json"
	"strings"
	"time"

	"deciscope-core-api/internal/domain"
)

const treeAuditPromptVersion = "v1"

type TreeAuditFindingType string

const (
	TreeAuditSubjectMismatch              TreeAuditFindingType = "subject_mismatch"
	TreeAuditCrossAgendaContamination     TreeAuditFindingType = "cross_agenda_contamination"
	TreeAuditCandidateFragmentation       TreeAuditFindingType = "candidate_fragmentation"
	TreeAuditCandidateMixedSubjects       TreeAuditFindingType = "candidate_mixed_subjects"
	TreeAuditDuplicateDynamicTopic        TreeAuditFindingType = "duplicate_dynamic_topic"
	TreeAuditIncorrectReparent            TreeAuditFindingType = "incorrect_reparent"
	TreeAuditReferenceEvidenceReparent    TreeAuditFindingType = "reference_evidence_reparent"
	TreeAuditRecapCreatedNewItem          TreeAuditFindingType = "recap_created_new_item"
	TreeAuditRecapCreatedNewCandidate     TreeAuditFindingType = "recap_created_new_candidate"
	TreeAuditFloatingTentativeCandidate   TreeAuditFindingType = "floating_tentative_candidate"
	TreeAuditTopicOutlier                 TreeAuditFindingType = "topic_outlier"
	TreeAuditGroupOutlier                 TreeAuditFindingType = "group_outlier"
	TreeAuditGroupLabelMismatch           TreeAuditFindingType = "group_label_mismatch"
	TreeAuditGroupChurn                   TreeAuditFindingType = "group_churn"
	TreeAuditMissingGroup                 TreeAuditFindingType = "missing_group"
	TreeAuditCandidateShouldPromote       TreeAuditFindingType = "candidate_should_promote"
	TreeAuditCandidateShouldNotPromote    TreeAuditFindingType = "candidate_should_not_promote"
	TreeAuditCandidateShouldFoldIntoTopic TreeAuditFindingType = "candidate_should_fold_into_existing_topic"
	TreeAuditParentLowConfidence          TreeAuditFindingType = "parent_low_confidence"
	TreeAuditStaleTentative               TreeAuditFindingType = "stale_tentative"
)

type TreeAuditOperationType string

const (
	TreeAuditMoveItem               TreeAuditOperationType = "move_item"
	TreeAuditRestorePreviousParent  TreeAuditOperationType = "restore_previous_parent"
	TreeAuditMergeCandidates        TreeAuditOperationType = "merge_candidates"
	TreeAuditFoldCandidateIntoTopic TreeAuditOperationType = "fold_candidate_into_topic"
	TreeAuditPromoteCandidate       TreeAuditOperationType = "promote_candidate"
	TreeAuditMarkCandidateTentative TreeAuditOperationType = "mark_candidate_tentative"
	TreeAuditDeactivateCandidate    TreeAuditOperationType = "deactivate_candidate"
	TreeAuditMergeDynamicTopics     TreeAuditOperationType = "merge_dynamic_topics"
	TreeAuditCreateGroup            TreeAuditOperationType = "create_group"
	TreeAuditMoveItemsToGroup       TreeAuditOperationType = "move_items_to_group"
	TreeAuditRenameGroup            TreeAuditOperationType = "rename_group"
	TreeAuditRemoveEmptyGroup       TreeAuditOperationType = "remove_empty_group"
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

type treeAuditOperation struct {
	OperationID           string                 `json:"operationId"`
	Type                  TreeAuditOperationType `json:"type"`
	NodeID                string                 `json:"nodeId"`
	NodeIDs               []string               `json:"nodeIds"`
	CandidateID           string                 `json:"candidateId"`
	FromCandidateID       string                 `json:"fromCandidateId"`
	ToCandidateID         string                 `json:"toCandidateId"`
	FromParentID          string                 `json:"fromParentId"`
	ToParentID            string                 `json:"toParentId"`
	GroupID               string                 `json:"groupId"`
	Label                 string                 `json:"label"`
	Reason                string                 `json:"reason"`
	Confidence            float64                `json:"confidence"`
	EvidenceSequenceNos   []int64                `json:"evidenceSequenceNos"`
	DependsOnOperationIDs []string               `json:"dependsOnOperationIds"`
}

type treeAuditResponse struct {
	BasedOnTreeVersion int64                `json:"basedOnTreeVersion"`
	Summary            string               `json:"summary"`
	Findings           []treeAuditFinding   `json:"findings"`
	Operations         []treeAuditOperation `json:"operations"`
}

type treeAuditValidatorEvaluation struct {
	OperationID        string                 `json:"operationId"`
	Type               TreeAuditOperationType `json:"type"`
	Result             string                 `json:"result"`
	Reason             string                 `json:"reason,omitempty"`
	WouldApply         bool                   `json:"wouldApply"`
	Applied            bool                   `json:"applied"`
	CurrentParentScore float64                `json:"currentParentScore,omitempty"`
	NewParentScore     float64                `json:"newParentScore,omitempty"`
	Improvement        float64                `json:"improvement,omitempty"`
}

type treeAuditValidatorResult struct {
	TreeIntegrityValid             bool                           `json:"treeIntegrityValid"`
	Evaluations                    []treeAuditValidatorEvaluation `json:"evaluations"`
	OperationsProposed             int                            `json:"operationsProposed"`
	OperationsWouldApply           int                            `json:"operationsWouldApply"`
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
	Mode                       domain.MeetingTreeAuditMode
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
}

func (c TreeAuditConfig) normalized() TreeAuditConfig {
	if !domain.ValidMeetingTreeAuditMode(c.Mode) {
		c.Mode = domain.MeetingTreeAuditShadow
	}
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
	return c
}

func (c TreeAuditConfig) active() bool {
	c = c.normalized()
	return c.Enabled && c.Mode != domain.MeetingTreeAuditOff
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
		TreeAuditMoveItemsToGroup, TreeAuditRenameGroup, TreeAuditRemoveEmptyGroup:
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
