package application

import (
	"context"
	"errors"
	"log"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"

	"deciscope-core-api/internal/domain"
)

const (
	liveAnalysisTriggerFinalTranscript      = "final_transcript"
	liveAnalysisTriggerPeriodicTick         = "periodic_tick"
	liveAnalysisTriggerContextReady         = "context_ready"
	liveAnalysisTriggerCompletedRerun       = "analysis_completed_rerun"
	liveAnalysisTriggerMaxWait              = "max_wait"
	liveAnalysisTriggerFinalizationFlush    = "finalization_flush"
	liveAnalysisTriggerScheduledTimer       = "debounced_timer"
	liveAnalysisDeferredSchedulerNotStarted = "scheduler_not_started"
	liveAnalysisDeferredNoPendingFinal      = "no_pending_final"
	liveAnalysisDeferredEmptyFinal          = "empty_final"
	liveAnalysisDeferredAnalysisRunning     = "analysis_running"
	liveAnalysisDeferredAlreadyScheduled    = "already_scheduled"
	liveAnalysisDeferredCooldown            = "cooldown"
	liveAnalysisDeferredBelowMinimumInput   = "below_minimum_input"
	liveAnalysisDeferredContextNotReady     = "context_not_ready"
	liveAnalysisDeferredMeetingFinalizing   = "meeting_finalizing"
	liveAnalysisDeferredMeetingStopped      = "meeting_stopped"
	liveAnalysisDeferredLowInformation      = "low_information"
	liveAnalysisDeferredRetryBlocked        = "retry_blocked"
	liveAnalysisDeferredBackoff             = "backoff"
	liveAnalysisDeferredSessionStatusLookup = "session_status_unavailable"
	liveAnalysisTriggerImmediateCatchUp     = "immediate_catch_up"
)

var liveAnalysisFillerOnly = map[string]struct{}{
	"はい": {}, "はいはい": {}, "ええ": {}, "うん": {}, "うんうん": {},
	"ああ": {}, "へえ": {}, "ほう": {}, "そう": {}, "そうですね": {},
	"なるほど": {}, "了解": {}, "わかりました": {}, "承知しました": {},
	"え": {}, "えー": {}, "ええと": {}, "えっと": {}, "あの": {},
	"その": {}, "まあ": {}, "なんか": {}, "うーん": {}, "ん": {}, "無音": {},
}

var (
	liveSemanticSentenceCompletePattern = regexp.MustCompile(`(?:でした|ました|ています|ていました|ありません|ないです|です|ます|した|する|なった|なる|できない|できません|未確認|未決定|可能性(?:が)?(?:高い|ある))[。．.!！?？]?$`)
	liveSemanticSubjectPattern          = regexp.MustCompile(`(?:は|が|を|には|では|において|について|から|まで)`)
	liveSemanticPredicatePattern        = regexp.MustCompile(`(?:発生|確認|異常|接続|復旧|切り戻|修正|影響|遅延|決定|合意|対応|実施|確認|担当|期限|原因|漏れ|不足|完了|終了|未確認|未決定|可能性)`)
	liveSemanticCorrectionPattern       = regexp.MustCompile(`(?:正確には|訂正すると|先ほどの説明は違|ではなく|じゃなく|厳密には)`)
	liveSemanticDecisionPattern         = regexp.MustCompile(`(?:決定しました|決めました|合意しました|とします|で進めます)`)
	liveSemanticRiskPattern             = regexp.MustCompile(`(?:リスク|恐れ|懸念|可能性があります|おそれ)`)
	liveSemanticTodoPattern             = regexp.MustCompile(`(?:担当|期限|までに|します|対応します|確認します|実施します)`)
	liveSemanticHypothesisPattern       = regexp.MustCompile(`(?:原因である可能性|原因の可能性|可能性が最も高|原因候補|と考えられ)`)
	liveSemanticUnresolvedPattern       = regexp.MustCompile(`(?:未確認|未解決|不明|未決定|確認できていない|説明できるか)`)
	liveSemanticCompletedActionPattern  = regexp.MustCompile(`(?:完了しました|実施しました|切り戻しました|修正しました|復旧しました|対応済み)`)
	liveSemanticConfirmedStatePattern   = regexp.MustCompile(`(?:発生していました|確認しました|異常はありませんでした|接続できませんでした|復旧しました|影響していました|遅延がありました)`)
	liveSemanticSpecificPattern         = regexp.MustCompile(`(?:\d|午前|午後|本日|今日|明日|来週|VLAN|ルーター|ファイアウォール|サーバー|スイッチ|ネットワーク)`)
)

type liveSemanticTriggerFeatures struct {
	SubjectPresent, PredicatePresent, SentenceComplete                   bool
	CorrectionCuePresent, DecisionCuePresent, RiskCuePresent             bool
	TodoCuePresent, HypothesisCuePresent, UnresolvedCuePresent           bool
	CompletedActionPresent, ConfirmedStatePresent, SpecificEntityPresent bool
}

func semanticTriggerFeatures(text string) liveSemanticTriggerFeatures {
	text = strings.TrimSpace(text)
	return liveSemanticTriggerFeatures{
		SubjectPresent:         liveSemanticSubjectPattern.MatchString(text),
		PredicatePresent:       liveSemanticPredicatePattern.MatchString(text),
		SentenceComplete:       liveSemanticSentenceCompletePattern.MatchString(text),
		CorrectionCuePresent:   liveSemanticCorrectionPattern.MatchString(text),
		DecisionCuePresent:     liveSemanticDecisionPattern.MatchString(text),
		RiskCuePresent:         liveSemanticRiskPattern.MatchString(text),
		TodoCuePresent:         liveSemanticTodoPattern.MatchString(text),
		HypothesisCuePresent:   liveSemanticHypothesisPattern.MatchString(text),
		UnresolvedCuePresent:   liveSemanticUnresolvedPattern.MatchString(text),
		CompletedActionPresent: liveSemanticCompletedActionPattern.MatchString(text),
		ConfirmedStatePresent:  liveSemanticConfirmedStatePattern.MatchString(text),
		SpecificEntityPresent:  liveSemanticSpecificPattern.MatchString(text),
	}
}

func (f liveSemanticTriggerFeatures) complete() bool {
	semanticCue := f.CorrectionCuePresent || f.DecisionCuePresent || f.RiskCuePresent ||
		f.TodoCuePresent || f.HypothesisCuePresent || f.UnresolvedCuePresent ||
		f.CompletedActionPresent || f.ConfirmedStatePresent
	return f.SentenceComplete && ((f.SubjectPresent && f.PredicatePresent) || semanticCue || (f.PredicatePresent && f.SpecificEntityPresent))
}

func (f liveSemanticTriggerFeatures) highPriority() bool {
	return f.CorrectionCuePresent || f.DecisionCuePresent || f.RiskCuePresent ||
		f.TodoCuePresent || f.HypothesisCuePresent || f.UnresolvedCuePresent
}

func pendingSemanticTrigger(segments []domain.TranscriptSegment) (complete, highPriority bool) {
	for _, segment := range segments {
		features := semanticTriggerFeatures(segment.Text)
		complete = complete || features.complete()
		highPriority = highPriority || features.highPriority()
	}
	return complete, highPriority
}

func (s *MeetingAnalysisService) logLiveAnalysisSchedulerStopped(reason string) {
	if s == nil || s.schedulerRegistrationID == "" {
		return
	}
	s.schedulerStopLogOnce.Do(func() {
		log.Printf("Live AI analysis scheduler stopped. schedulerInstanceId=%s schedulerRegistrationId=%s reason=%s",
			s.schedulerInstanceID, s.schedulerRegistrationID, reason)
	})
}

// evaluateLiveAnalysisTrigger is the single entry point for final events,
// periodic fallback ticks, context completion, and completion reruns. All
// state decisions are serialized by the service mutex, so concurrent trigger
// sources can create at most one timer or one provider call per session.
func (s *MeetingAnalysisService) evaluateLiveAnalysisTrigger(sessionID, trigger string) {
	if s == nil || !s.config.liveActive() || strings.TrimSpace(sessionID) == "" {
		return
	}
	now := s.now()
	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)

	switch {
	case state.stopped:
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, trigger, now, state, "ignored", liveAnalysisDeferredMeetingStopped, 0, time.Time{})
		s.mu.Unlock()
		return
	case state.finalizing:
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, trigger, now, state, "ignored", liveAnalysisDeferredMeetingFinalizing, 0, time.Time{})
		s.mu.Unlock()
		return
	case len(state.pending) == 0:
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, trigger, now, state, "ignored", liveAnalysisDeferredNoPendingFinal, 0, time.Time{})
		s.mu.Unlock()
		return
	case state.running:
		state.rerunRequested = true
		state.catchUpRequested = true
		state.coalescedTriggerCount++
		state.lastDeferredReason = liveAnalysisDeferredAnalysisRunning
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, trigger, now, state, "coalesced", liveAnalysisDeferredAnalysisRunning, 0, time.Time{})
		s.mu.Unlock()
		return
	case state.retryBlocked:
		state.lastDeferredReason = liveAnalysisDeferredRetryBlocked
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, trigger, now, state, "deferred", liveAnalysisDeferredRetryBlocked, 0, time.Time{})
		s.mu.Unlock()
		return
	case state.contextStatus == meetingContextStatusPending:
		state.lastDeferredReason = liveAnalysisDeferredContextNotReady
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, trigger, now, state, "deferred", liveAnalysisDeferredContextNotReady, 0, time.Time{})
		s.mu.Unlock()
		return
	case !hasSubstantiveLiveAnalysisInput(state.pending):
		cancelLiveAnalysisTimerLocked(state)
		state.lastDeferredReason = liveAnalysisDeferredLowInformation
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, trigger, now, state, "deferred", liveAnalysisDeferredLowInformation, 0, time.Time{})
		s.mu.Unlock()
		return
	}

	scheduledFor, reason := s.nextLiveAnalysisTimeLocked(state, now)
	if state.analysisScheduled && !scheduledFor.Before(state.scheduledAt) {
		state.coalescedTriggerCount++
		state.lastDeferredReason = liveAnalysisDeferredAlreadyScheduled
		delay := state.scheduledAt.Sub(now)
		if delay < 0 {
			delay = 0
		}
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, trigger, now, state, "coalesced", liveAnalysisDeferredAlreadyScheduled, delay, state.scheduledAt)
		s.mu.Unlock()
		return
	}

	if state.analysisScheduled {
		previousScheduledFor := state.scheduledAt
		cancelLiveAnalysisTimerLocked(state)
		log.Printf("Live AI analysis timer cancelled. sessionId=%s cancelReason=rescheduled_earlier scheduledFor=%s analysisRunning=%t analysisScheduled=false finalizing=%t stopped=%t replacementTimer=true",
			sessionID, previousScheduledFor.UTC().Format(time.RFC3339Nano), state.running, state.finalizing, state.stopped)
	}
	if s.runCtx == nil {
		state.lastDeferredReason = liveAnalysisDeferredSchedulerNotStarted
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, trigger, now, state, "deferred", liveAnalysisDeferredSchedulerNotStarted, 0, time.Time{})
		s.mu.Unlock()
		return
	}

	delay := scheduledFor.Sub(now)
	if delay < 0 {
		delay = 0
	}
	state.analysisScheduled = true
	state.scheduledAt = scheduledFor
	state.scheduledTrigger = trigger
	state.scheduleGeneration++
	generation := state.scheduleGeneration
	state.lastTrigger = trigger
	state.lastDeferredReason = reason
	state.analysisTimer = time.AfterFunc(delay, func() {
		s.dispatchScheduledLiveAnalysis(sessionID, generation)
	})
	s.logLiveAnalysisTriggerEvaluationLocked(sessionID, trigger, now, state, "scheduled", reason, delay, scheduledFor)
	s.mu.Unlock()
}

func (s *MeetingAnalysisService) nextLiveAnalysisTimeLocked(state *liveAnalysisSessionState, now time.Time) (time.Time, string) {
	if state.catchUpRequested || (state.rerunRequested && state.lastDeferredReason == liveAnalysisDeferredAnalysisRunning) {
		scheduledFor := now
		if !state.nextAttemptAt.IsZero() && state.nextAttemptAt.After(scheduledFor) {
			return state.nextAttemptAt, liveAnalysisDeferredBackoff
		}
		return scheduledFor, liveAnalysisTriggerImmediateCatchUp
	}
	semanticComplete, highPriority := pendingSemanticTrigger(state.pending)
	debounceBase := state.latestPendingFinalAt
	if debounceBase.IsZero() {
		debounceBase = now
	}
	scheduledFor := debounceBase.Add(s.config.LiveDebounce)
	reason := "debounce"
	if state.pendingChars < s.config.LiveMinChars && !semanticComplete {
		scheduledFor = state.oldestPendingFinalAt.Add(s.config.LiveMaxWait)
		reason = liveAnalysisDeferredBelowMinimumInput
	}
	if !state.lastAnalysisCompletedAt.IsZero() && !highPriority {
		cooldownUntil := state.lastAnalysisCompletedAt.Add(s.config.LiveCooldown)
		if cooldownUntil.After(scheduledFor) {
			scheduledFor = cooldownUntil
			reason = liveAnalysisDeferredCooldown
		}
	}
	maxWaitAt := state.oldestPendingFinalAt.Add(s.config.LiveMaxWait)
	if maxWaitAt.Before(scheduledFor) {
		scheduledFor = maxWaitAt
		reason = liveAnalysisTriggerMaxWait
	}
	if !state.nextAttemptAt.IsZero() && state.nextAttemptAt.After(scheduledFor) {
		scheduledFor = state.nextAttemptAt
		reason = liveAnalysisDeferredBackoff
	}
	if scheduledFor.Before(now) {
		scheduledFor = now
	}
	return scheduledFor, reason
}

func (s *MeetingAnalysisService) dispatchScheduledLiveAnalysis(sessionID string, generation uint64) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("Live AI analysis timer panic recovered. sessionId=%s panic=%v", sessionID, recovered)
			s.evaluateLiveAnalysisTrigger(sessionID, liveAnalysisTriggerPeriodicTick)
		}
	}()

	s.mu.Lock()
	runCtx := s.runCtx
	s.mu.Unlock()
	if runCtx == nil {
		runCtx = context.Background()
	}
	statusAllowed, statusReason, statusErr := s.liveSessionStatusAllowsAnalysis(runCtx, sessionID)
	now := s.now()
	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	if !state.analysisScheduled || state.scheduleGeneration != generation {
		s.mu.Unlock()
		return
	}
	state.analysisScheduled = false
	state.analysisTimer = nil
	state.scheduledAt = time.Time{}
	if statusErr != nil {
		state.lastDeferredReason = liveAnalysisDeferredSessionStatusLookup
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, liveAnalysisTriggerScheduledTimer, now, state, "deferred", liveAnalysisDeferredSessionStatusLookup, 0, time.Time{})
		s.mu.Unlock()
		log.Printf("Live AI analysis session status lookup failed. sessionId=%s error=%v", sessionID, statusErr)
		return
	}
	if !statusAllowed {
		state.stopped = true
		state.lastDeferredReason = statusReason
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, liveAnalysisTriggerScheduledTimer, now, state, "ignored", statusReason, 0, time.Time{})
		s.mu.Unlock()
		return
	}

	switch {
	case state.stopped:
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, liveAnalysisTriggerScheduledTimer, now, state, "ignored", liveAnalysisDeferredMeetingStopped, 0, time.Time{})
		s.mu.Unlock()
		return
	case state.finalizing:
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, liveAnalysisTriggerScheduledTimer, now, state, "ignored", liveAnalysisDeferredMeetingFinalizing, 0, time.Time{})
		s.mu.Unlock()
		return
	case len(state.pending) == 0:
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, liveAnalysisTriggerScheduledTimer, now, state, "ignored", liveAnalysisDeferredNoPendingFinal, 0, time.Time{})
		s.mu.Unlock()
		return
	case state.running:
		state.rerunRequested = true
		state.catchUpRequested = true
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, liveAnalysisTriggerScheduledTimer, now, state, "coalesced", liveAnalysisDeferredAnalysisRunning, 0, time.Time{})
		s.mu.Unlock()
		return
	case state.contextStatus == meetingContextStatusPending:
		state.lastDeferredReason = liveAnalysisDeferredContextNotReady
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, liveAnalysisTriggerScheduledTimer, now, state, "deferred", liveAnalysisDeferredContextNotReady, 0, time.Time{})
		s.mu.Unlock()
		return
	case state.retryBlocked:
		state.lastDeferredReason = liveAnalysisDeferredRetryBlocked
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, liveAnalysisTriggerScheduledTimer, now, state, "deferred", liveAnalysisDeferredRetryBlocked, 0, time.Time{})
		s.mu.Unlock()
		return
	case !hasSubstantiveLiveAnalysisInput(state.pending):
		state.lastDeferredReason = liveAnalysisDeferredLowInformation
		s.logLiveAnalysisTriggerEvaluationLocked(sessionID, liveAnalysisTriggerScheduledTimer, now, state, "deferred", liveAnalysisDeferredLowInformation, 0, time.Time{})
		s.mu.Unlock()
		return
	}

	eligibleAt, reason := s.nextLiveAnalysisTimeLocked(state, now)
	if eligibleAt.After(now) {
		state.lastDeferredReason = reason
		s.mu.Unlock()
		s.evaluateLiveAnalysisTrigger(sessionID, liveAnalysisTriggerScheduledTimer)
		return
	}

	segments := append([]domain.TranscriptSegment(nil), state.pending...)
	oldestPendingAt := state.oldestPendingFinalAt
	latestFinalAt := state.latestPendingFinalAt
	runTrigger := state.scheduledTrigger
	if runTrigger == "" {
		runTrigger = liveAnalysisTriggerScheduledTimer
	}
	semanticComplete, _ := pendingSemanticTrigger(state.pending)
	if (state.pendingChars < s.config.LiveMinChars && !semanticComplete) || (!oldestPendingAt.IsZero() && now.Sub(oldestPendingAt) >= s.config.LiveMaxWait) {
		runTrigger = liveAnalysisTriggerMaxWait
	}
	fromSequence, throughSequence := liveAnalysisSequenceRange(segments)
	coalesced := state.coalescedTriggerCount
	state.pending = nil
	state.pendingChars = 0
	state.oldestPendingFinalAt = time.Time{}
	state.latestPendingFinalAt = time.Time{}
	state.running = true
	state.runningDone = make(chan struct{})
	state.rerunRequested = false
	state.catchUpRequested = false
	state.lastAnalysisStartedAt = now
	state.lastTrigger = runTrigger
	state.lastDeferredReason = ""
	state.runningOldestPendingAt = oldestPendingAt
	state.runningLatestFinalAt = latestFinalAt
	state.runningTargetFromSequenceNo = fromSequence
	state.runningTargetThroughSequenceNo = throughSequence
	state.runningTrigger = runTrigger
	state.runningCoalescedTriggerCount = coalesced
	state.coalescedTriggerCount = 0
	state.scheduledTrigger = ""
	delays := livePendingFinalDelays(now, oldestPendingAt, latestFinalAt)
	log.Printf("Live AI analysis trigger evaluated. sessionId=%s trigger=%s currentTime=%s lastCoveredSequenceNo=%d highestAvailableFinalSequenceNo=%d pendingFinalSegmentCount=%d pendingChars=%d oldestPendingAgeMs=%d analysisRunning=%t analysisScheduled=%t cooldownRemainingMs=0 contextStatus=%s decision=scheduled reason=eligible scheduledDelayMs=0 scheduledFor=%s",
		sessionID, runTrigger, now.UTC().Format(time.RFC3339Nano), state.lastCoveredSequenceNo, state.highestAvailableFinalSequenceNo,
		len(segments), sumSegmentChars(segments), delays.FromOldest.Milliseconds(), state.running, state.analysisScheduled, liveContextStatus(state), now.UTC().Format(time.RFC3339Nano))
	log.Printf("Live AI analysis started. sessionId=%s trigger=%s targetFromSequenceNo=%d targetThroughSequenceNo=%d segmentCount=%d chars=%d oldestPendingAgeMs=%d delayFromLatestFinalMs=%d delayFromOldestFinalMs=%d coalescedTriggerCount=%d",
		sessionID, runTrigger, fromSequence, throughSequence, len(segments), sumSegmentChars(segments),
		delays.FromOldest.Milliseconds(), delays.FromLatest.Milliseconds(), delays.FromOldest.Milliseconds(), coalesced)
	s.mu.Unlock()

	go s.runLiveAnalysis(runCtx, sessionID, segments)
}

func (s *MeetingAnalysisService) liveSessionStatusAllowsAnalysis(ctx context.Context, sessionID string) (bool, string, error) {
	if s.sessionRepo == nil {
		return true, "", nil
	}
	session, err := s.sessionRepo.GetMeetingSession(ctx, sessionID)
	if errors.Is(err, domain.ErrNotFound) {
		// Some internal/test callers use the analysis service without a
		// durable meeting-session row. Preserve that established behavior.
		return true, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if session == nil {
		return true, "", nil
	}
	switch session.Status {
	case domain.MeetingSessionEnding:
		return false, liveAnalysisDeferredMeetingFinalizing, nil
	case domain.MeetingSessionEnded, domain.MeetingSessionFailed, domain.MeetingSessionStale:
		return false, liveAnalysisDeferredMeetingStopped, nil
	default:
		return true, "", nil
	}
}

func appendPendingLiveSegmentLocked(state *liveAnalysisSessionState, segment domain.TranscriptSegment, arrivedAt time.Time) bool {
	identity := livePendingSegmentIdentity(segment)
	if identity != "" {
		for _, existing := range state.pending {
			if livePendingSegmentIdentity(existing) == identity {
				return false
			}
		}
	}
	state.pending = append(state.pending, segment)
	if state.oldestPendingFinalAt.IsZero() {
		state.oldestPendingFinalAt = arrivedAt
	}
	state.latestPendingFinalAt = arrivedAt
	if segment.SequenceNo > state.highestAvailableFinalSequenceNo {
		state.highestAvailableFinalSequenceNo = segment.SequenceNo
	}
	return true
}

func restorePendingLiveSegmentsLocked(state *liveAnalysisSessionState, segments []domain.TranscriptSegment, oldestAt time.Time, restoredAt time.Time) {
	for _, segment := range segments {
		appendPendingLiveSegmentLocked(state, segment, restoredAt)
	}
	if !oldestAt.IsZero() && (state.oldestPendingFinalAt.IsZero() || oldestAt.Before(state.oldestPendingFinalAt)) {
		state.oldestPendingFinalAt = oldestAt
	}
	state.pendingChars = sumSegmentChars(state.pending)
}

func livePendingSegmentIdentity(segment domain.TranscriptSegment) string {
	if segment.SequenceNo > 0 {
		return finalSegmentKey(segment.CallID, segment.SequenceNo)
	}
	if eventID := strings.TrimSpace(segment.EventID); eventID != "" {
		return "event:" + eventID
	}
	return ""
}

func liveAnalysisSequenceRange(segments []domain.TranscriptSegment) (int64, int64) {
	var from, through int64
	for _, segment := range segments {
		if segment.SequenceNo <= 0 {
			continue
		}
		if from == 0 || segment.SequenceNo < from {
			from = segment.SequenceNo
		}
		if segment.SequenceNo > through {
			through = segment.SequenceNo
		}
	}
	return from, through
}

func hasSubstantiveLiveAnalysisInput(segments []domain.TranscriptSegment) bool {
	for _, segment := range segments {
		if !isLowInformationFinalText(segment.Text) {
			return true
		}
	}
	return false
}

func isLowInformationFinalText(value string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, strings.TrimSpace(value))
	if normalized == "" {
		return true
	}
	_, filler := liveAnalysisFillerOnly[normalized]
	return filler
}

func cancelLiveAnalysisTimerLocked(state *liveAnalysisSessionState) bool {
	if state == nil || !state.analysisScheduled {
		return false
	}
	if state.analysisTimer != nil {
		state.analysisTimer.Stop()
	}
	state.analysisTimer = nil
	state.analysisScheduled = false
	state.scheduledAt = time.Time{}
	state.scheduledTrigger = ""
	state.scheduleGeneration++
	return true
}

func (s *MeetingAnalysisService) logIgnoredLiveTranscriptTrigger(sessionID, reason string) {
	if s == nil || !s.config.liveActive() || strings.TrimSpace(sessionID) == "" {
		return
	}
	now := s.now()
	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	s.logLiveAnalysisTriggerEvaluationLocked(sessionID, liveAnalysisTriggerFinalTranscript, now, state, "ignored", reason, 0, time.Time{})
	s.mu.Unlock()
}

func (s *MeetingAnalysisService) logLiveAnalysisTriggerEvaluationLocked(sessionID, trigger string, now time.Time, state *liveAnalysisSessionState, decision, reason string, delay time.Duration, scheduledFor time.Time) {
	cooldownRemaining := time.Duration(0)
	if !state.lastAnalysisCompletedAt.IsZero() {
		cooldownUntil := state.lastAnalysisCompletedAt.Add(s.config.LiveCooldown)
		if cooldownUntil.After(now) {
			cooldownRemaining = cooldownUntil.Sub(now)
		}
	}
	scheduledForText := ""
	if !scheduledFor.IsZero() {
		scheduledForText = scheduledFor.UTC().Format(time.RFC3339Nano)
	}
	log.Printf("Live AI analysis trigger evaluated. schedulerInstanceId=%s schedulerRegistrationId=%s sessionId=%s trigger=%s currentTime=%s lastCoveredSequenceNo=%d highestAvailableFinalSequenceNo=%d pendingFinalSegmentCount=%d pendingChars=%d oldestPendingAgeMs=%d analysisRunning=%t analysisScheduled=%t cooldownRemainingMs=%d contextStatus=%s decision=%s reason=%s scheduledDelayMs=%d scheduledFor=%s",
		s.schedulerInstanceID, s.schedulerRegistrationID, sessionID, trigger, now.UTC().Format(time.RFC3339Nano), state.lastCoveredSequenceNo, state.highestAvailableFinalSequenceNo,
		len(state.pending), state.pendingChars, durationSince(now, state.oldestPendingFinalAt).Milliseconds(),
		state.running, state.analysisScheduled, cooldownRemaining.Milliseconds(), liveContextStatus(state),
		decision, reason, delay.Milliseconds(), scheduledForText)
}

func liveContextStatus(state *liveAnalysisSessionState) string {
	if state.contextStatus == "" {
		return "unknown"
	}
	return state.contextStatus
}

func durationSince(now, then time.Time) time.Duration {
	if then.IsZero() || now.Before(then) {
		return 0
	}
	return now.Sub(then)
}

type livePendingFinalDelayMetrics struct {
	FromOldest time.Duration
	FromLatest time.Duration
}

// livePendingFinalDelays names the two scheduler ages at their source. The
// oldest pending final arrived first and therefore must never have a smaller
// delay than the latest pending final when both timestamps are valid.
func livePendingFinalDelays(now, oldest, latest time.Time) livePendingFinalDelayMetrics {
	return livePendingFinalDelayMetrics{
		FromOldest: durationSince(now, oldest),
		FromLatest: durationSince(now, latest),
	}
}

func liveAnalysisTreesEqual(left, right *liveAnalysisTree) bool {
	return reflect.DeepEqual(left, right)
}

func liveAgendaProgressEqual(left, right *agendaProgressState) bool {
	return reflect.DeepEqual(left, right)
}

func liveEvidenceEqual(left, right []liveAnalysisItem) bool {
	type evidence struct {
		SequenceNos           []int64
		Roles                 []liveEvidenceRoleRef
		ResolutionSequenceNos []int64
		ReopenSequenceNos     []int64
	}
	project := func(items []liveAnalysisItem) map[string]evidence {
		result := make(map[string]evidence, len(items))
		for _, item := range items {
			result[item.ID] = evidence{
				SequenceNos:           item.EvidenceSequenceNos,
				Roles:                 item.EvidenceRoles,
				ResolutionSequenceNos: item.ResolutionEvidenceSequenceNos,
				ReopenSequenceNos:     item.ReopenEvidenceSequenceNos,
			}
		}
		return result
	}
	return reflect.DeepEqual(project(left), project(right))
}

func (s *MeetingAnalysisService) recoverActiveLiveAnalysisSessions(ctx context.Context) {
	if s == nil || !s.config.liveActive() || s.sessionRepo == nil {
		return
	}
	sessions, err := s.sessionRepo.ListMeetingSessionsForBotWatchdog(ctx)
	if err != nil {
		log.Printf("Live AI analysis active-session recovery failed. error=%v", err)
		return
	}
	for _, session := range sessions {
		if domain.IsReusableMeetingSessionStatus(session.Status) {
			s.PrepareMeetingSession(session)
		}
	}
}

func (s *MeetingAnalysisService) recoverDurablePendingFinals(sessionID string) {
	if s == nil || !s.config.liveActive() || strings.TrimSpace(sessionID) == "" {
		return
	}
	if s.transcriptRepo == nil {
		s.evaluateLiveAnalysisTrigger(sessionID, liveAnalysisTriggerPeriodicTick)
		return
	}
	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	if state.recoveryInFlight || state.finalizing || state.stopped {
		s.mu.Unlock()
		return
	}
	state.recoveryInFlight = true
	runCtx := s.runCtx
	previousPayload := append([]byte(nil), state.lastPayload...)
	previousVersion := state.lastVersion
	s.mu.Unlock()
	// The recovery owner performs exactly one periodic evaluation on every
	// terminal path (success, repository error, or no recovered rows). A
	// concurrent caller that observes recoveryInFlight returns above and does
	// not emit a second evaluation.
	defer s.evaluateLiveAnalysisTrigger(sessionID, liveAnalysisTriggerPeriodicTick)
	if runCtx == nil {
		runCtx = context.Background()
	}

	previousPayload, previousVersion = s.seedLiveAnalysisState(runCtx, sessionID, previousPayload, previousVersion)
	segments, err := s.transcriptRepo.ListTranscriptSegments(runCtx, "", sessionID, meetingAnalysisFinalTranscriptLimit)
	if err != nil {
		s.mu.Lock()
		state = s.sessionStateLocked(sessionID)
		state.recoveryInFlight = false
		s.mu.Unlock()
		log.Printf("Live AI analysis durable pending recovery failed. sessionId=%s error=%v", sessionID, err)
		return
	}

	now := s.now()
	recovered := 0
	s.mu.Lock()
	state = s.sessionStateLocked(sessionID)
	state.recoveryInFlight = false
	if state.finalizing || state.stopped {
		s.mu.Unlock()
		return
	}
	if previousVersion > state.lastVersion {
		state.lastPayload = append([]byte(nil), previousPayload...)
		state.lastVersion = previousVersion
	}
	effectivePayload := state.lastPayload
	if len(effectivePayload) == 0 {
		effectivePayload = previousPayload
	}
	state.lastCoveredSequenceNo = previousLiveAnalysisState(effectivePayload).CoveredThroughSequenceNo
	uncovered := filterAlreadyAnalyzedSegments(segments, effectivePayload)
	for _, segment := range uncovered {
		if !segment.IsFinal || segment.SequenceNo <= 0 || strings.TrimSpace(segment.Text) == "" {
			continue
		}
		// A running round has already frozen this range. A later tick will
		// recover any out-of-order gap that the exact analyzed-segment keys did
		// not cover, without adding it to the in-flight prompt.
		if state.running && segment.SequenceNo >= state.runningTargetFromSequenceNo && segment.SequenceNo <= state.runningTargetThroughSequenceNo {
			continue
		}
		arrivedAt := segment.ReceivedAtUTC
		if arrivedAt.IsZero() {
			arrivedAt = segment.RecognizedAtUTC
		}
		if arrivedAt.IsZero() || arrivedAt.After(now) {
			arrivedAt = now
		}
		if appendPendingLiveSegmentLocked(state, segment, arrivedAt) {
			recovered++
		}
	}
	state.pendingChars = sumSegmentChars(state.pending)
	if recovered > 0 {
		state.retryBlocked = false
	}
	state.lastActivityAt = now
	pendingCount := len(state.pending)
	s.mu.Unlock()
	if recovered > 0 {
		log.Printf("Live AI analysis durable pending recovered. sessionId=%s recoveredFinalSegmentCount=%d pendingFinalSegmentCount=%d", sessionID, recovered, pendingCount)
	}
	s.ensureMeetingContextPlanning(sessionID, nil)
}
