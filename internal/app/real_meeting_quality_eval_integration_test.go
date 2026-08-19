package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	postgresrepository "deciscope-core-api/internal/adapter/repository/postgres"
	"deciscope-core-api/internal/application"
	"deciscope-core-api/internal/domain"
	"deciscope-core-api/internal/infrastructure/database"
)

// TestRealMeetingQualityEvalGPT5Mini is the opt-in real-deployment companion
// to the deterministic PR suite. It never updates the approved baseline: one
// model sample is evidence about a deployment, not a new golden truth.
func TestRealMeetingQualityEvalGPT5Mini(t *testing.T) {
	enabled, err := evaluateRealAITestEnvironment(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled {
		t.Skip("RUN_REAL_AI_INTEGRATION_TESTS=true is required")
	}
	baseURL := strings.TrimSpace(os.Getenv("DATABASE_REAL_AI_TEST_URL"))
	requireTestDatabaseURL(t, baseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_, db := createIsolatedRealAIDatabase(t, ctx, baseURL)
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate isolated real quality-eval database: %v", err)
	}

	const sessionID = "session_real_meeting_quality_eval"
	seedRealTreeAuditFixture(t, ctx, db, sessionID)
	deployment := strings.TrimSpace(os.Getenv("AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT"))
	config := aiConfigFromEnv()
	config.AzureOpenAI.Deployment = deployment
	config.AzureOpenAI.Timeout = 60 * time.Second
	config.LiveAnalysisEnabled = true
	config.LiveAnalysisMinChars = 1
	config.LiveAnalysisMaxInputChars = 12000
	config.FinalSummaryEnabled = true
	config.FinalSummaryMaxInputChars = 12000
	config.FinalSummaryTimeout = 60 * time.Second
	config.FinalizationWaitTimeout = 90 * time.Second
	config.FinalizationQuietPeriod = time.Millisecond
	config.FinalFlushMaxAttempts = 2
	config.TreeAudit.Enabled = true
	config.TreeAudit.Timeout = 60 * time.Second
	config.TaskModels.ContextPlanner = deployment
	config.TaskModels.LiveExtraction = deployment
	config.TaskModels.TreeAudit = deployment
	config.TaskModels.TreeReorganizer = deployment
	config.TaskModels.FinalTreeReview = deployment
	config.TaskModels.FinalSummary = deployment

	service := buildMeetingAnalysisService(
		config,
		db,
		postgresrepository.NewMeetingSessionRepository(db),
		nil,
	)
	t.Cleanup(func() { _ = service.Close() })
	if err := service.FinalizeMeetingSession(ctx, domain.MeetingSession{ID: sessionID}, application.MeetingSessionFinalizationRequest{
		BotLastForwardedFinalSequence: 3,
		TranscriptQueueDrained:        true,
	}); err != nil {
		t.Fatalf("real meeting quality finalization: %v", err)
	}

	live, err := postgresrepository.NewMeetingAIAnalysisRepository(db).
		GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisLive)
	if err != nil {
		t.Fatalf("load real meeting quality snapshot: %v", err)
	}
	scenario := application.MeetingQualityScenario{
		ID: "real-vpn-certificate-quality",
		TranscriptSegments: []application.MeetingQualityTranscriptSegment{
			{SequenceNo: 1, Text: "ここで、アジェンダにはなかった別の問題があります。"},
			{SequenceNo: 2, Text: "VPN装置の証明書が来月末に期限切れになり、放置するとリモート接続ができなくなる可能性があります。"},
			{SequenceNo: 3, Text: "高橋さんに今週中に証明書の更新手順と作業可能日を確認してもらいます。"},
		},
		RequiredPropositions: []application.MeetingQualityProposition{
			{ID: "vpn-risk", Text: "VPN証明書の期限切れでリモート接続できなくなるリスク", RequiredKind: "risk"},
			{ID: "vpn-todo", Text: "高橋さんが今週中に証明書の更新手順と作業可能日を確認する", RequiredKind: "todo"},
		},
		RequiredRelations: []application.MeetingQualityRelation{{
			From: "vpn-todo", To: "vpn-risk", Kind: "action_for", RequireSameBranch: true,
		}},
		ForbiddenResults: []application.MeetingQualityForbiddenResult{{Type: "low_information"}},
		FinalCoverage:    3,
	}
	result := application.EvaluateMeetingQualitySnapshot(scenario, live.Payload)
	t.Logf("real meeting quality result: passed=%t metrics=%+v hard=%v missing=%v relations=%v forbidden=%v",
		result.Passed, result.Metrics, result.HardInvariantViolations,
		result.MissingRequiredPropositions, result.RelationFailures, result.ForbiddenResultsFound)
	if !result.Passed {
		t.Fatalf("real deployment quality evaluation failed: %+v", result)
	}
}
