package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"deciscope-core-api/internal/domain"
)

const (
	defaultLiveAnalysisInterval         = 10 * time.Second
	meetingAnalysisMaxBackoff           = 5 * time.Minute
	meetingAnalysisSessionGCAfter       = 3 * time.Hour
	meetingAnalysisFinalTranscriptLimit = 2000
	// Token caps are ceilings, not targets. Reasoning models (gpt-5 family,
	// o-series) spend part of the completion budget on hidden reasoning
	// tokens before emitting the JSON answer, so these are sized well above
	// the expected visible output. The live cap covers the v2 payload
	// (items + tree) plus reasoning headroom.
	liveAnalysisMaxTokens  = 3000
	finalAnalysisMaxTokens = 4000
)

const (
	liveAnalysisTreeMaxNodes  = 36
	liveAnalysisItemsMaxCount = 50
	// liveAnalysisResolvedItemsMaxCount and liveAnalysisTreeMaxResolvedNodes
	// are separate caps for resolved items/nodes so that a burst of active
	// discussion can never evict resolved entries (and vice versa). Without
	// this, resolved items/nodes -- which tend to be the oldest entries in
	// their list -- would be the first evicted by the shared cap, even though
	// "resolved" is a terminal, intentionally-retained state.
	liveAnalysisResolvedItemsMaxCount = 50
	liveAnalysisTreeMaxResolvedNodes  = 36

	liveAnalysisTreeDescriptionMaxRunes = 100
)

const liveAnalysisSystemPrompt = "あなたは日本語の会議分析アシスタントです。与えられた「前回の分析状態」を新しい発言で更新し、指定されたJSONスキーマのオブジェクトだけを出力してください。JSON以外の説明文やコードフェンスは出力しないでください。"

const liveAnalysisSchemaDescription = `{
  "summary": "議論全体のこれまでの要約(毎回全文を出力、400字程度まで)",
  "currentTopic": "現在の主なトピック(毎回出力)",
  "resolvedIds": ["新しい発言によって解消・回答・完了した既存itemのid"],
  "items": [
    {
      "id": "英小文字・数字・ハイフンの安定ID(例: risk-db-migration)",
      "kind": "issue | question | risk | decision | todo",
      "severity": "low | medium | high",
      "title": "カード見出し(25字程度まで)",
      "body": "1〜2文の説明。todoで担当者や期限が分かる場合はここに含める",
      "status": "open | updated | resolved"
    }
  ],
  "tree": {
    "nodes": [
      {
        "id": "安定ID(itemsのidと共有可)",
        "kind": "topic | issue | question | risk | decision",
        "label": "短いラベル(20字程度)",
        "status": "open | updated | resolved",
        "description": "ノード内容の短い説明(1〜2行、100字程度まで)",
        "relatedItemIds": ["このノードに関連するitems[].id。対応itemと同じidのノードでは同じidを入れる"]
      }
    ],
    "edges": [
      {"source": "ノードid", "target": "ノードid"}
    ]
  }
}`

const liveAnalysisRulesDescription = `- summaryとcurrentTopicは毎回全文を出力してください。
- itemsには、このラウンドの新しい発言によって新しく生まれた論点・懸念・質問・決定事項・TODO、または内容が変化した既存item(idは既存のものを使う)だけを出力してください。変化のない既存itemは出力しないでください(サーバー側で保持されます)。
- 新しい発言に新規の論点・懸念・質問・決定事項・TODOが含まれる場合は、必ず対応するitemを出力してください。
- 新しく追加するitemはstatusを"open"に、既存itemを更新した場合はstatusを"updated"にしてください。
- 新しい発言によって解消された懸念(risk)・回答が出た質問(question)・対応が完了した論点があれば、そのitemのidを必ずresolvedIdsに列挙してください。該当が無ければresolvedIdsは空配列にしてください。「解消した」「対応済み」という内容の新規itemを別IDで出力してはいけません。決定事項(decision)は会議中は残してください。
  例: 前回の分析状態に {"id":"risk-x","kind":"risk",...} があり、新しい発言でその懸念が解消したと明言された場合、resolvedIdsに"risk-x"を入れます。
- 解決済みのitemやnodeは削除せず、statusを"resolved"として残してください。再度議論が始まった場合は既存idのままstatusを"updated"に戻してください。
- treeも同様に、追加するノード・内容が変化したノード・新しいedgeだけを出力してください。既存の構造はサーバー側で保持されます。ノードのidは安定させ、対応するitemがある場合は同じidを使ってください。currentTopicが変わった場合は対応するtopicノードを出力してください。
- treeに新しいノードを追加するときは、必ず親となる既存ノード(適切な親が無ければ現在のtopicノード)へのedgeを同じラウンドのedgesに必ず含めてください。edgeの無い孤立ノードを出力してはいけません。
- edgeの親(source)には、可能な限りtopicノードではなく、内容が直接関連する既存のノードを選んでください。topicノード直下につなぐのは、適切な親が見つからない場合だけにしてください。
- 新しいitemを出力したときは、必ず同じidのtreeノードと、その親となるノードへのedgeも同時に出力してください。
- tree.nodes[].descriptionには、そのノードで何が議論されているかを1〜2行で短く書いてください。冗長な背景説明や会議全体の要約は入れないでください。
- tree.nodes[].relatedItemIdsには、そのノードに関連するitems[].idだけを入れてください。存在しないidを作らないでください。対応するitemと同じidのノードでは、そのidを必ず含めてください。
- severityは影響度で判断してください(会議の結論を左右するものはhigh)。
- tree.nodes[].kindには topic / issue / question / risk / decision のみを使ってください。TODOや作業項目は、itemsでは kind="todo" として出してよいですが、tree.nodesに出すときは "todo" ではなく "issue" として表現してください。
- ツリーは階層構造にしてください。root(最上位のtopicノード)の直下には「大分類」となるtopicノードだけを置き、その数は原則5〜8個程度に抑えてください。
- 個々の論点・質問・リスク・決定・TODOは、root直下に直接つながず、内容が最も近い大分類topicノードの配下(またはその配下の既存ノードの下)に接続してください。
- 新しい話題が既存の大分類やその配下のノードに近い場合は、新しいroot直下ノードを作らず、その大分類配下のissue/question/risk/decisionとして追加、または既存ノードを更新してください。
- 大分類は会議の内容に合わせて決めてください(例:基盤・技術、分析ロジック、UI/UX、コスト、権限・セキュリティ、検証環境 など。会議に合わないものは使わない)。
- 各論点・質問・リスク・決定・TODOは、必ずいずれかの大分類topicノードの配下に置いてください。大分類がまだ無い場合は、まず大分類topicノードを作ってからその配下に置いてください。
- 新規ノードを出すときは、必ずそのノードへの親エッジ(parent edge)も同じラウンドで出力してください。親の無いノードを出してはいけません。
- 親が曖昧な場合でも、root直下に逃がさず、最も近い既存の大分類topicまたはissueを親として選んでください。`

const finalAnalysisSystemPrompt = "あなたは日本語の会議分析アシスタントです。会議全体の文字起こしと事前情報から最終要約を作成し、指定されたJSONスキーマのオブジェクトだけを出力してください。JSON以外の説明文やコードフェンスは出力しないでください。"

const finalAnalysisSchemaDescription = `{
  "suggestedTitle": "会議タイトル案",
  "overview": "会議全体の要約(600字程度まで)",
  "decisions": [{"text": "", "importance": "high|medium|low"}],
  "actionItems": [{"text": "", "owner": "", "due": "", "priority": "high|medium|low"}],
  "openIssues": ["未解決事項"],
  "keyPoints": ["重要な論点"],
  "nextMeetingTopics": ["次回に持ち越すべき内容"]
}`

// MeetingAnalysisConfig controls MeetingAnalysisService behavior. Enabled
// gates both live analysis and the final summary at the Azure OpenAI
// configuration level; LiveEnabled/FinalEnabled further gate each feature
// independently.
type MeetingAnalysisConfig struct {
	Enabled bool

	LiveEnabled        bool
	LiveInterval       time.Duration
	LiveMinChars       int
	LiveMaxInputChars  int
	LiveRequestTimeout time.Duration

	FinalEnabled        bool
	FinalMaxInputChars  int
	FinalRequestTimeout time.Duration

	// Model is the Azure OpenAI deployment name recorded on every analysis
	// row and included in AI analysis log lines.
	Model string

	// DebugDroppedNodes は破棄ノード詳細ログを出すか。
	DebugDroppedNodes bool
}

func (c MeetingAnalysisConfig) liveActive() bool {
	return c.Enabled && c.LiveEnabled
}

func (c MeetingAnalysisConfig) finalActive() bool {
	return c.Enabled && c.FinalEnabled
}

// MeetingAnalysisService buffers final transcript segments per session,
// periodically asks Azure OpenAI to update a running live analysis, and
// generates a final summary once a session ends. It implements
// TranscriptSegmentPublisher (to receive final segments) and
// MeetingSessionEndedObserver (to trigger the final summary), and is always
// constructed non-nil; MeetingAnalysisConfig.Enabled/LiveEnabled/FinalEnabled
// make every operation a no-op when AI is not configured, so callers never
// need nil checks.
type MeetingAnalysisService struct {
	analysisRepo   MeetingAIAnalysisRepository
	completer      AIChatCompleter
	publisher      MeetingAIAnalysisPublisher
	transcriptRepo TranscriptSegmentRepository
	sessionRepo    MeetingSessionRepository
	config         MeetingAnalysisConfig
	now            func() time.Time

	mu       sync.Mutex
	sessions map[string]*liveAnalysisSessionState

	// finalSummaryInFlight guards against concurrent final-summary generation
	// for the same session. Two MeetingSessionEnded notifications can race (e.g.
	// a bot "ended" status PATCH and the watchdog ending the session at nearly
	// the same time), and each launches generateFinalSummary in its own
	// goroutine. Without this, both goroutines can pass the existing-analysis DB
	// check before either writes the "running" row, producing two final
	// summaries. Keyed by sessionID; entries are added atomically under mu and
	// removed when generation finishes.
	finalSummaryInFlight map[string]struct{}

	startOnce sync.Once
	closeOnce sync.Once
	stopCh    chan struct{}
}

type liveAnalysisSessionState struct {
	pending      []domain.TranscriptSegment
	pendingChars int
	running      bool
	lastPayload  json.RawMessage
	lastVersion  int64
	// versionSeeded guards the one-time DB lookup that restores lastPayload
	// and lastVersion after a backend restart, so versions keep increasing
	// across restarts and clients never discard newer updates as stale.
	versionSeeded  bool
	failureCount   int
	nextAttemptAt  time.Time
	lastActivityAt time.Time
	contextLoaded  bool
	context        *meetingSessionPreContext
}

func NewMeetingAnalysisService(
	analysisRepo MeetingAIAnalysisRepository,
	transcriptRepo TranscriptSegmentRepository,
	sessionRepo MeetingSessionRepository,
	completer AIChatCompleter,
	config MeetingAnalysisConfig,
	publisher ...MeetingAIAnalysisPublisher,
) *MeetingAnalysisService {
	var analysisPublisher MeetingAIAnalysisPublisher
	if len(publisher) > 0 {
		analysisPublisher = publisher[0]
	}
	if config.LiveInterval <= 0 {
		config.LiveInterval = defaultLiveAnalysisInterval
	}
	return &MeetingAnalysisService{
		analysisRepo:         analysisRepo,
		transcriptRepo:       transcriptRepo,
		sessionRepo:          sessionRepo,
		completer:            completer,
		publisher:            analysisPublisher,
		config:               config,
		now:                  time.Now,
		sessions:             make(map[string]*liveAnalysisSessionState),
		finalSummaryInFlight: make(map[string]struct{}),
		stopCh:               make(chan struct{}),
	}
}

// PublishTranscriptSegment implements TranscriptSegmentPublisher. Only final
// segments with a non-empty session id and text are buffered.
func (s *MeetingAnalysisService) PublishTranscriptSegment(segment domain.TranscriptSegment) {
	if s == nil || !s.config.liveActive() {
		return
	}
	if !segment.IsFinal {
		return
	}
	sessionID := strings.TrimSpace(segment.SessionID)
	if sessionID == "" {
		return
	}
	if strings.TrimSpace(segment.Text) == "" {
		return
	}

	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	state.pending = append(state.pending, segment)
	state.pendingChars = sumSegmentChars(state.pending)
	state.lastActivityAt = s.now()
	s.mu.Unlock()
}

// Start launches the periodic live-analysis scheduler. It is a no-op when
// live analysis is disabled. Stop the scheduler with Close.
func (s *MeetingAnalysisService) Start(ctx context.Context) {
	if s == nil || !s.config.liveActive() {
		return
	}
	s.startOnce.Do(func() {
		go s.run(ctx)
	})
}

// Close stops the scheduler started by Start. It does not cancel in-flight
// analysis calls.
func (s *MeetingAnalysisService) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		close(s.stopCh)
	})
	return nil
}

func (s *MeetingAnalysisService) run(ctx context.Context) {
	ticker := time.NewTicker(s.config.LiveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

type liveAnalysisJob struct {
	sessionID string
	segments  []domain.TranscriptSegment
}

func (s *MeetingAnalysisService) tick(ctx context.Context) {
	now := s.now()
	var jobs []liveAnalysisJob

	s.mu.Lock()
	for sessionID, state := range s.sessions {
		if now.Sub(state.lastActivityAt) > meetingAnalysisSessionGCAfter {
			delete(s.sessions, sessionID)
			continue
		}
		if state.running {
			continue
		}
		if state.pendingChars < s.config.LiveMinChars {
			continue
		}
		if !state.nextAttemptAt.IsZero() && now.Before(state.nextAttemptAt) {
			continue
		}
		segments := state.pending
		state.pending = nil
		state.pendingChars = 0
		state.running = true
		jobs = append(jobs, liveAnalysisJob{sessionID: sessionID, segments: segments})
	}
	s.mu.Unlock()

	for _, job := range jobs {
		go s.runLiveAnalysis(ctx, job.sessionID, job.segments)
	}
}

func (s *MeetingAnalysisService) sessionStateLocked(sessionID string) *liveAnalysisSessionState {
	state, ok := s.sessions[sessionID]
	if !ok {
		state = &liveAnalysisSessionState{}
		s.sessions[sessionID] = state
	}
	return state
}

func (s *MeetingAnalysisService) runLiveAnalysis(ctx context.Context, sessionID string, segments []domain.TranscriptSegment) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Live AI analysis panic recovered. sessionId=%s panic=%v", sessionID, r)
			s.mu.Lock()
			state := s.sessionStateLocked(sessionID)
			state.running = false
			state.pending = append(append([]domain.TranscriptSegment{}, segments...), state.pending...)
			state.pendingChars = sumSegmentChars(state.pending)
			s.mu.Unlock()
		}
	}()

	start := s.now()
	log.Printf("Live AI analysis started. sessionId=%s segmentCount=%d", sessionID, len(segments))

	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	previousPayload := state.lastPayload
	previousVersion := state.lastVersion
	versionSeeded := state.versionSeeded
	s.mu.Unlock()

	if !versionSeeded {
		previousPayload, previousVersion = s.seedLiveAnalysisState(ctx, sessionID, previousPayload, previousVersion)
	}

	preContext := s.sessionPreContext(ctx, sessionID)
	diffText, inputChars := buildAnalysisTranscript(segments, s.config.LiveMaxInputChars)
	userPrompt := buildLiveAnalysisUserPrompt(previousPayload, preContext, diffText)

	if s.completer == nil {
		s.handleLiveAnalysisFailure(ctx, sessionID, segments, previousPayload, previousVersion, errors.New("azure openai completer is not configured"), len(segments), inputChars, s.now().Sub(start))
		return
	}

	analysisCtx := ctx
	if s.config.LiveRequestTimeout > 0 {
		var cancel context.CancelFunc
		analysisCtx, cancel = context.WithTimeout(ctx, s.config.LiveRequestTimeout)
		defer cancel()
	}
	// Ephemeral running notification so clients can show a "generating"
	// state. It is broadcast only (never written to the database) and keeps
	// the current version/payload so clients can safely replace their whole
	// state with it.
	s.publishAnalysis(domain.MeetingAIAnalysis{
		SessionID: sessionID,
		Type:      domain.MeetingAIAnalysisLive,
		Status:    domain.MeetingAIAnalysisRunning,
		Version:   previousVersion,
		Payload:   previousPayload,
		Model:     s.config.Model,
		UpdatedAt: s.now().UTC(),
	})
	result, err := s.completer.Complete(analysisCtx, AIChatRequest{
		System:    liveAnalysisSystemPrompt,
		User:      userPrompt,
		MaxTokens: liveAnalysisMaxTokens,
	})
	elapsed := s.now().Sub(start)
	if err != nil {
		s.handleLiveAnalysisFailure(ctx, sessionID, segments, previousPayload, previousVersion, err, len(segments), inputChars, elapsed)
		return
	}
	treeStats := &liveAnalysisTreeMergeStats{}
	payload, parseErr := parseAndMergeLiveAnalysisPayload(result.Content, previousPayload, treeStats)
	if parseErr != nil {
		s.handleLiveAnalysisFailure(ctx, sessionID, segments, previousPayload, previousVersion, parseErr, len(segments), inputChars, elapsed)
		return
	}

	newVersion := previousVersion + 1
	saved, upsertErr := s.analysisRepo.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID:    sessionID,
		Type:         domain.MeetingAIAnalysisLive,
		Status:       domain.MeetingAIAnalysisCompleted,
		Version:      newVersion,
		Payload:      payload,
		Model:        s.config.Model,
		SegmentCount: len(segments),
		InputChars:   inputChars,
		UpdatedAt:    s.now().UTC(),
	})

	s.mu.Lock()
	state = s.sessionStateLocked(sessionID)
	state.running = false
	if upsertErr == nil {
		state.lastPayload = payload
		state.lastVersion = newVersion
		state.failureCount = 0
		state.nextAttemptAt = time.Time{}
	}
	s.mu.Unlock()

	if upsertErr != nil {
		log.Printf("Live AI analysis persist failed. sessionId=%s version=%d error=%v", sessionID, newVersion, upsertErr)
		return
	}
	modelResolvedIDCount := countModelResolvedIDs(result.Content)
	diffItemCount, diffTreeNodeCount, diffTreeEdgeCount := countLiveAnalysisDiffStats(result.Content)
	stats := countLiveAnalysisPayloadStats(payload)
	log.Printf("Live AI analysis completed. sessionId=%s segmentCount=%d inputChars=%d version=%d promptTokens=%d completionTokens=%d elapsed=%s modelResolvedIds=%d resolvedItems=%d totalItems=%d resolvedNodes=%d totalNodes=%d diffItems=%d diffTreeNodes=%d diffTreeEdges=%d droppedNodes=%d droppedNodeReasons=%s droppedEdges=%d synthesizedNodes=%d prunedTopicEdges=%d",
		sessionID, len(segments), inputChars, newVersion, result.PromptTokens, result.CompletionTokens, elapsed,
		modelResolvedIDCount, stats.ResolvedItems, stats.TotalItems, stats.ResolvedNodes, stats.TotalNodes,
		diffItemCount, diffTreeNodeCount, diffTreeEdgeCount,
		treeStats.droppedNodes(), treeStats.droppedNodeReasons(), treeStats.DroppedEdges, treeStats.SynthesizedNodes, treeStats.PrunedTopicEdges)
	log.Printf("Live AI analysis tree metrics. sessionId=%s diffTreeNodes=%d diffTreeEdges=%d newNodeIds=%d updatedNodeIds=%d normalizedTodoNodes=%d droppedNodes=%d droppedNodeReasons=%s synthesizedNodes=%d orphanRescuedEdges=%d prunedTopicEdges=%d reparentedNodes=%d totalNodes=%d totalEdges=%d topicChildren=%d maxDepth=%d flatTreeDetected=%t",
		sessionID, diffTreeNodeCount, diffTreeEdgeCount, treeStats.DiffNewNodes, treeStats.DiffUpdatedNodes, treeStats.NormalizedTodoNodes,
		treeStats.droppedNodes(), treeStats.droppedNodeReasons(), treeStats.SynthesizedNodes, treeStats.OrphanRescuedEdges, treeStats.PrunedTopicEdges, treeStats.ReparentedNodes,
		stats.TotalNodes, treeStats.TotalEdges, treeStats.TopicChildCount, treeStats.MaxDepth, treeStats.FlatTreeDetected)
	if s.config.DebugDroppedNodes {
		for _, detail := range treeStats.DroppedNodeDetails {
			log.Printf("Live AI analysis dropped tree node. sessionId=%s id=%s kind=%s title=%q reason=%s", sessionID, detail.ID, detail.Kind, detail.Title, detail.Reason)
		}
	}
	s.publishAnalysis(*saved)
}

// seedLiveAnalysisState restores the previous live analysis payload/version
// from the database the first time a session is analyzed in this process, so
// a backend restart mid-meeting neither resets the version sequence nor loses
// the accumulated analysis state.
func (s *MeetingAnalysisService) seedLiveAnalysisState(ctx context.Context, sessionID string, payload json.RawMessage, version int64) (json.RawMessage, int64) {
	if version == 0 && len(payload) == 0 {
		existing, err := s.analysisRepo.GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisLive)
		switch {
		case err == nil && existing != nil:
			payload = existing.Payload
			version = existing.Version
		case err != nil && !errors.Is(err, domain.ErrNotFound):
			log.Printf("Live AI analysis previous state lookup failed. sessionId=%s error=%v", sessionID, err)
		}
	}

	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	state.versionSeeded = true
	state.lastPayload = payload
	state.lastVersion = version
	s.mu.Unlock()
	return payload, version
}

func (s *MeetingAnalysisService) handleLiveAnalysisFailure(ctx context.Context, sessionID string, segments []domain.TranscriptSegment, previousPayload json.RawMessage, previousVersion int64, cause error, segmentCount, inputChars int, elapsed time.Duration) {
	log.Printf("Live AI analysis failed. sessionId=%s segmentCount=%d inputChars=%d elapsed=%s error=%v", sessionID, segmentCount, inputChars, elapsed, cause)

	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	state.running = false
	state.pending = append(append([]domain.TranscriptSegment{}, segments...), state.pending...)
	state.pendingChars = sumSegmentChars(state.pending)
	state.failureCount++
	state.nextAttemptAt = s.now().Add(liveAnalysisBackoff(s.config.LiveInterval, state.failureCount))
	s.mu.Unlock()

	saved, err := s.analysisRepo.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID:    sessionID,
		Type:         domain.MeetingAIAnalysisLive,
		Status:       domain.MeetingAIAnalysisFailed,
		Version:      previousVersion,
		Payload:      previousPayload,
		Model:        s.config.Model,
		SegmentCount: segmentCount,
		InputChars:   inputChars,
		LastError:    truncateErrorMessage(cause, 300),
		UpdatedAt:    s.now().UTC(),
	})
	if err != nil {
		log.Printf("Live AI analysis failure persist failed. sessionId=%s error=%v", sessionID, err)
		return
	}
	s.publishAnalysis(*saved)
}

func liveAnalysisBackoff(interval time.Duration, failureCount int) time.Duration {
	if interval <= 0 {
		interval = defaultLiveAnalysisInterval
	}
	backoff := interval
	for i := 0; i < failureCount; i++ {
		backoff *= 2
		if backoff >= meetingAnalysisMaxBackoff {
			return meetingAnalysisMaxBackoff
		}
	}
	return backoff
}

// NotifyMeetingSessionEnded implements MeetingSessionEndedObserver. It
// launches the final summary generation in the background so the caller
// (MeetingSessionService) never blocks on it.
func (s *MeetingAnalysisService) NotifyMeetingSessionEnded(session domain.MeetingSession) {
	if s == nil || !s.config.finalActive() {
		return
	}
	sessionID := strings.TrimSpace(session.ID)
	if sessionID == "" {
		return
	}
	go s.generateFinalSummary(context.Background(), session)
}

func (s *MeetingAnalysisService) generateFinalSummary(ctx context.Context, session domain.MeetingSession) {
	sessionID := strings.TrimSpace(session.ID)
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Final AI summary panic recovered. sessionId=%s panic=%v", sessionID, r)
		}
	}()

	s.mu.Lock()
	if _, inFlight := s.finalSummaryInFlight[sessionID]; inFlight {
		s.mu.Unlock()
		log.Printf("Final AI summary skipped because generation is already in flight. sessionId=%s", sessionID)
		return
	}
	s.finalSummaryInFlight[sessionID] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.finalSummaryInFlight, sessionID)
		s.mu.Unlock()
	}()

	existing, err := s.analysisRepo.GetMeetingAIAnalysis(ctx, sessionID, domain.MeetingAIAnalysisFinal)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		log.Printf("Final AI summary existing lookup failed. sessionId=%s error=%v", sessionID, err)
		return
	}
	if err == nil && existing != nil && (existing.Status == domain.MeetingAIAnalysisRunning || existing.Status == domain.MeetingAIAnalysisCompleted) {
		log.Printf("Final AI summary skipped because it already exists. sessionId=%s status=%s", sessionID, existing.Status)
		return
	}

	segments, err := s.transcriptRepo.ListTranscriptSegments(ctx, "", sessionID, meetingAnalysisFinalTranscriptLimit)
	if err != nil {
		log.Printf("Final AI summary transcript lookup failed. sessionId=%s error=%v", sessionID, err)
		return
	}
	finalSegments := filterNonEmptySegments(segments)
	if len(finalSegments) == 0 {
		log.Printf("Final AI summary skipped because transcript is empty. sessionId=%s", sessionID)
		return
	}

	if s.completer == nil {
		log.Printf("Final AI summary skipped because azure openai completer is not configured. sessionId=%s", sessionID)
		return
	}

	version := int64(1)
	var previousPayload json.RawMessage
	if existing != nil {
		version = existing.Version + 1
		previousPayload = existing.Payload
	}
	runningSaved, err := s.analysisRepo.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID: sessionID,
		Type:      domain.MeetingAIAnalysisFinal,
		Status:    domain.MeetingAIAnalysisRunning,
		Version:   version,
		Payload:   previousPayload,
		Model:     s.config.Model,
		UpdatedAt: s.now().UTC(),
	})
	if err != nil {
		log.Printf("Final AI summary running state persist failed. sessionId=%s error=%v", sessionID, err)
		return
	}
	s.publishAnalysis(*runningSaved)

	s.mu.Lock()
	state, hasState := s.sessions[sessionID]
	var livePayload json.RawMessage
	if hasState {
		livePayload = state.lastPayload
	}
	delete(s.sessions, sessionID)
	s.mu.Unlock()

	preContext := s.sessionPreContext(ctx, sessionID)
	transcriptText, inputChars, truncated := buildAnalysisTranscriptTruncated(finalSegments, s.config.FinalMaxInputChars)
	userPrompt := buildFinalAnalysisUserPrompt(livePayload, preContext, transcriptText, truncated)

	start := s.now()
	log.Printf("Final AI summary started. sessionId=%s segmentCount=%d inputChars=%d", sessionID, len(finalSegments), inputChars)

	analysisCtx := ctx
	if s.config.FinalRequestTimeout > 0 {
		var cancel context.CancelFunc
		analysisCtx, cancel = context.WithTimeout(ctx, s.config.FinalRequestTimeout)
		defer cancel()
	}
	result, err := s.completer.Complete(analysisCtx, AIChatRequest{
		System:    finalAnalysisSystemPrompt,
		User:      userPrompt,
		MaxTokens: finalAnalysisMaxTokens,
	})
	elapsed := s.now().Sub(start)
	if err != nil {
		s.handleFinalAnalysisFailure(ctx, sessionID, version, previousPayload, err, len(finalSegments), inputChars, elapsed)
		return
	}
	payload, parseErr := parseAndValidateFinalAnalysisPayload(result.Content)
	if parseErr != nil {
		s.handleFinalAnalysisFailure(ctx, sessionID, version, previousPayload, parseErr, len(finalSegments), inputChars, elapsed)
		return
	}

	saved, err := s.analysisRepo.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID:    sessionID,
		Type:         domain.MeetingAIAnalysisFinal,
		Status:       domain.MeetingAIAnalysisCompleted,
		Version:      version,
		Payload:      payload,
		Model:        s.config.Model,
		SegmentCount: len(finalSegments),
		InputChars:   inputChars,
		UpdatedAt:    s.now().UTC(),
	})
	if err != nil {
		log.Printf("Final AI summary persist failed. sessionId=%s error=%v", sessionID, err)
		return
	}
	log.Printf("Final AI summary completed. sessionId=%s segmentCount=%d inputChars=%d version=%d promptTokens=%d completionTokens=%d elapsed=%s",
		sessionID, len(finalSegments), inputChars, saved.Version, result.PromptTokens, result.CompletionTokens, elapsed)
	s.publishAnalysis(*saved)
}

func (s *MeetingAnalysisService) handleFinalAnalysisFailure(ctx context.Context, sessionID string, version int64, previousPayload json.RawMessage, cause error, segmentCount, inputChars int, elapsed time.Duration) {
	log.Printf("Final AI summary failed. sessionId=%s segmentCount=%d inputChars=%d elapsed=%s error=%v", sessionID, segmentCount, inputChars, elapsed, cause)
	saved, err := s.analysisRepo.UpsertMeetingAIAnalysis(ctx, domain.MeetingAIAnalysis{
		SessionID:    sessionID,
		Type:         domain.MeetingAIAnalysisFinal,
		Status:       domain.MeetingAIAnalysisFailed,
		Version:      version,
		Payload:      previousPayload,
		Model:        s.config.Model,
		SegmentCount: segmentCount,
		InputChars:   inputChars,
		LastError:    truncateErrorMessage(cause, 300),
		UpdatedAt:    s.now().UTC(),
	})
	if err != nil {
		log.Printf("Final AI summary failure persist failed. sessionId=%s error=%v", sessionID, err)
		return
	}
	s.publishAnalysis(*saved)
}

func (s *MeetingAnalysisService) publishAnalysis(analysis domain.MeetingAIAnalysis) {
	if s.publisher != nil {
		if analysis.Type == domain.MeetingAIAnalysisLive {
			analysis.IntervalSeconds = s.LiveAnalysisIntervalSeconds()
		}
		s.publisher.PublishMeetingAIAnalysis(analysis)
	}
}

// LiveAnalysisIntervalSeconds returns the live analysis check interval in
// seconds, or 0 when live analysis is disabled. Clients use it for a "next
// update in about N seconds" display.
func (s *MeetingAnalysisService) LiveAnalysisIntervalSeconds() int {
	if s == nil || !s.config.liveActive() {
		return 0
	}
	return int(s.config.LiveInterval.Seconds())
}

// MeetingAIAnalysesSnapshot is the latest live/final analysis pair for a
// session. Live and/or Final are nil when no analysis exists yet.
// LiveIntervalSeconds is the live analysis check interval (0 when AI or live
// analysis is disabled).
type MeetingAIAnalysesSnapshot struct {
	SessionID           string
	Live                *domain.MeetingAIAnalysis
	Final               *domain.MeetingAIAnalysis
	LiveIntervalSeconds int
}

func (s *MeetingAnalysisService) GetMeetingAIAnalyses(ctx context.Context, sessionID string) (*MeetingAIAnalysesSnapshot, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%w: sessionId is required", domain.ErrInvalidArgument)
	}
	if s == nil {
		return &MeetingAIAnalysesSnapshot{SessionID: sessionID}, nil
	}
	live, err := s.getOptionalAnalysis(ctx, sessionID, domain.MeetingAIAnalysisLive)
	if err != nil {
		return nil, err
	}
	final, err := s.getOptionalAnalysis(ctx, sessionID, domain.MeetingAIAnalysisFinal)
	if err != nil {
		return nil, err
	}
	return &MeetingAIAnalysesSnapshot{
		SessionID:           sessionID,
		Live:                live,
		Final:               final,
		LiveIntervalSeconds: s.LiveAnalysisIntervalSeconds(),
	}, nil
}

func (s *MeetingAnalysisService) getOptionalAnalysis(ctx context.Context, sessionID string, analysisType domain.MeetingAIAnalysisType) (*domain.MeetingAIAnalysis, error) {
	analysis, err := s.analysisRepo.GetMeetingAIAnalysis(ctx, sessionID, analysisType)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return analysis, nil
}

// meetingSessionPreContext is the subset of session metadata that is
// injected into every AI prompt as background information.
type meetingSessionPreContext struct {
	Title             string
	Purpose           string
	Agenda            string
	DecisionPoints    string
	Concerns          string
	ExpectedOutput    string
	CustomInstruction string
}

func (c *meetingSessionPreContext) isEmpty() bool {
	return c.Title == "" && c.Purpose == "" && c.Agenda == "" && c.DecisionPoints == "" &&
		c.Concerns == "" && c.ExpectedOutput == "" && c.CustomInstruction == ""
}

func (c *meetingSessionPreContext) render() string {
	var lines []string
	if c.Title != "" {
		lines = append(lines, "タイトル: "+c.Title)
	}
	if c.Purpose != "" {
		lines = append(lines, "目的: "+c.Purpose)
	}
	if c.Agenda != "" {
		lines = append(lines, "アジェンダ: "+c.Agenda)
	}
	if c.DecisionPoints != "" {
		lines = append(lines, "決定すべき事項: "+c.DecisionPoints)
	}
	if c.Concerns != "" {
		lines = append(lines, "懸念点: "+c.Concerns)
	}
	if c.ExpectedOutput != "" {
		lines = append(lines, "期待される成果: "+c.ExpectedOutput)
	}
	if c.CustomInstruction != "" {
		lines = append(lines, "特別な指示: "+c.CustomInstruction)
	}
	return strings.Join(lines, "\n")
}

// sessionPreContext loads and caches the pre-meeting context for a session.
// The cache also stores negative lookups (nil) so a repeated tick does not
// call MeetingSessionRepository again.
func (s *MeetingAnalysisService) sessionPreContext(ctx context.Context, sessionID string) *meetingSessionPreContext {
	s.mu.Lock()
	state := s.sessionStateLocked(sessionID)
	if state.contextLoaded {
		cached := state.context
		s.mu.Unlock()
		return cached
	}
	s.mu.Unlock()

	preContext := s.fetchSessionPreContext(ctx, sessionID)

	s.mu.Lock()
	state = s.sessionStateLocked(sessionID)
	state.context = preContext
	state.contextLoaded = true
	s.mu.Unlock()

	return preContext
}

func (s *MeetingAnalysisService) fetchSessionPreContext(ctx context.Context, sessionID string) *meetingSessionPreContext {
	if s.sessionRepo == nil {
		return nil
	}
	session, err := s.sessionRepo.GetMeetingSession(ctx, sessionID)
	if err != nil || session == nil {
		return nil
	}
	preContext := &meetingSessionPreContext{
		Title:             strings.TrimSpace(session.Title),
		Purpose:           strings.TrimSpace(session.Purpose),
		Agenda:            strings.TrimSpace(session.Agenda),
		DecisionPoints:    strings.TrimSpace(session.DecisionPoints),
		Concerns:          strings.TrimSpace(session.Concerns),
		ExpectedOutput:    strings.TrimSpace(session.ExpectedOutput),
		CustomInstruction: strings.TrimSpace(session.CustomInstruction),
	}
	if preContext.isEmpty() {
		return nil
	}
	return preContext
}

func buildLiveAnalysisUserPrompt(previousPayload json.RawMessage, preContext *meetingSessionPreContext, diffText string) string {
	var b strings.Builder
	b.WriteString("[前回の分析状態(JSON)]\n")
	if len(previousPayload) == 0 {
		b.WriteString("null")
	} else {
		b.Write(previousPayload)
	}
	b.WriteString("\n\n")
	if preContext != nil {
		b.WriteString("[会議の事前情報]\n")
		b.WriteString(preContext.render())
		b.WriteString("\n\n")
	}
	b.WriteString("[新しい発言(差分)]\n")
	if diffText == "" {
		b.WriteString("(新しい発言はありません)")
	} else {
		b.WriteString(diffText)
	}
	b.WriteString("\n\n")
	b.WriteString("[更新ルール]\n")
	b.WriteString(liveAnalysisRulesDescription)
	b.WriteString("\n\n")
	b.WriteString("上記の情報とルールを踏まえて、前回の分析状態を更新し、次のJSONスキーマのオブジェクトだけを出力してください:\n")
	b.WriteString(liveAnalysisSchemaDescription)
	return b.String()
}

func buildFinalAnalysisUserPrompt(livePayload json.RawMessage, preContext *meetingSessionPreContext, transcriptText string, truncated bool) string {
	var b strings.Builder
	if len(livePayload) > 0 {
		b.WriteString("[会議中に生成されたライブ分析の最新状態(JSON)]\n")
		b.Write(livePayload)
		b.WriteString("\n\n")
	}
	if preContext != nil {
		b.WriteString("[会議の事前情報]\n")
		b.WriteString(preContext.render())
		b.WriteString("\n\n")
	}
	b.WriteString("[会議全体の文字起こし]\n")
	if truncated {
		b.WriteString("(注意: 文字数上限のため、冒頭の発言は省略されています。以降の発言のみが含まれます。)\n")
	}
	b.WriteString(transcriptText)
	b.WriteString("\n\n")
	b.WriteString("上記の情報を踏まえて、会議全体の最終要約として次のJSONスキーマのオブジェクトだけを出力してください:\n")
	b.WriteString(finalAnalysisSchemaDescription)
	return b.String()
}

// buildAnalysisTranscript formats final segments as "話者名: テキスト" lines
// and drops the oldest lines first when the joined text exceeds maxChars.
func buildAnalysisTranscript(segments []domain.TranscriptSegment, maxChars int) (string, int) {
	text, chars, _ := buildAnalysisTranscriptTruncated(segments, maxChars)
	return text, chars
}

func buildAnalysisTranscriptTruncated(segments []domain.TranscriptSegment, maxChars int) (string, int, bool) {
	lines := make([]string, 0, len(segments))
	for _, segment := range segments {
		text := strings.TrimSpace(segment.Text)
		if text == "" {
			continue
		}
		speaker := strings.TrimSpace(segment.SpeakerName)
		if speaker == "" {
			speaker = "話者不明"
		}
		lines = append(lines, speaker+": "+text)
	}
	originalChars := totalLineChars(lines)
	joined, chars := truncateLinesFromOldest(lines, maxChars)
	return joined, chars, chars < originalChars
}

func truncateLinesFromOldest(lines []string, maxChars int) (string, int) {
	if maxChars <= 0 {
		joined := strings.Join(lines, "\n")
		return joined, len([]rune(joined))
	}
	start := 0
	total := totalLineChars(lines)
	for total > maxChars && start < len(lines) {
		total -= lineChars(lines[start], start > 0)
		start++
	}
	joined := strings.Join(lines[start:], "\n")
	return joined, len([]rune(joined))
}

func totalLineChars(lines []string) int {
	total := 0
	for i, line := range lines {
		total += lineChars(line, i > 0)
	}
	return total
}

func lineChars(line string, withNewline bool) int {
	length := len([]rune(line))
	if withNewline {
		length++
	}
	return length
}

func sumSegmentChars(segments []domain.TranscriptSegment) int {
	total := 0
	for _, segment := range segments {
		total += len([]rune(strings.TrimSpace(segment.Text)))
	}
	return total
}

func filterNonEmptySegments(segments []domain.TranscriptSegment) []domain.TranscriptSegment {
	filtered := make([]domain.TranscriptSegment, 0, len(segments))
	for _, segment := range segments {
		if strings.TrimSpace(segment.Text) == "" {
			continue
		}
		filtered = append(filtered, segment)
	}
	return filtered
}

func truncateErrorMessage(err error, limit int) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	runes := []rune(message)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return message
}

// liveAnalysisPayload is the v2 live analysis schema. items follows the
// AnalysisItem vocabulary and tree follows the tree.update vocabulary from
// docs/events.md. Unknown fields in the model output (including the removed
// v1 fields decisions/actionItems/openQuestions/concerns/nextChecks) are
// silently ignored by json.Unmarshal.
//
// ResolvedIds is a model-to-server instruction channel only: the model lists
// the ids of items resolved by the new utterances, the server marks those
// items and matching tree nodes as resolved deterministically, and the field
// is cleared before persisting so it never appears in stored/broadcast payloads
// or in the next prompt's previous state.
type liveAnalysisPayload struct {
	Summary      string             `json:"summary"`
	CurrentTopic string             `json:"currentTopic"`
	ResolvedIds  []string           `json:"resolvedIds,omitempty"`
	Items        []liveAnalysisItem `json:"items"`
	Tree         *liveAnalysisTree  `json:"tree"`
}

type liveAnalysisItem struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Status   string `json:"status"`
}

type liveAnalysisTree struct {
	Nodes []liveAnalysisTreeNode `json:"nodes"`
	Edges []liveAnalysisTreeEdge `json:"edges"`
}

type liveAnalysisTreeNode struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	Label          string   `json:"label"`
	Status         string   `json:"status,omitempty"`
	Description    string   `json:"description,omitempty"`
	RelatedItemIDs []string `json:"relatedItemIds,omitempty"`
}

type liveAnalysisTreeEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

func (p liveAnalysisPayload) isEmpty() bool {
	return strings.TrimSpace(p.Summary) == "" && strings.TrimSpace(p.CurrentTopic) == "" &&
		len(p.Items) == 0 && (p.Tree == nil || len(p.Tree.Nodes) == 0)
}

func validLiveAnalysisItemKind(kind string) bool {
	switch kind {
	case "issue", "question", "risk", "decision", "todo":
		return true
	default:
		return false
	}
}

func validLiveAnalysisTreeNodeKind(kind string) bool {
	switch kind {
	case "topic", "issue", "question", "risk", "decision":
		return true
	default:
		return false
	}
}

func validLiveAnalysisSeverity(severity string) bool {
	switch severity {
	case "low", "medium", "high":
		return true
	default:
		return false
	}
}

func validLiveAnalysisItemStatus(status string) bool {
	switch status {
	case "open", "updated", "resolved":
		return true
	default:
		return false
	}
}

func validLiveAnalysisTreeNodeStatus(status string) bool {
	switch status {
	case "open", "updated", "resolved":
		return true
	default:
		return false
	}
}

// liveAnalysisCurrentTopicNodeID is the id of the synthetic topic node that
// normalizeLiveAnalysisTree inserts when the model omitted a topic node.
const liveAnalysisCurrentTopicNodeID = "topic-current"

const liveAnalysisTopicLabelMaxRunes = 20

// normalizeLiveAnalysisItems lowercases kind/severity/status and drops items
// with no usable text or an out-of-vocabulary kind. Partially invalid output
// never fails the whole payload; only the offending element is discarded.
//
// resolvedIDs carries the ids the model listed in resolvedIds. Items with a
// matching id are kept but forced to status="resolved". Items whose status is
// already "resolved" are added to the same set (mutating the passed map), so
// previous-state items and matching tree nodes receive the same status.
func normalizeLiveAnalysisItems(items []liveAnalysisItem, resolvedIDs map[string]struct{}) []liveAnalysisItem {
	normalized := make([]liveAnalysisItem, 0, len(items))
	for _, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		item.Kind = strings.ToLower(strings.TrimSpace(item.Kind))
		item.Severity = strings.ToLower(strings.TrimSpace(item.Severity))
		item.Title = strings.TrimSpace(item.Title)
		item.Body = strings.TrimSpace(item.Body)
		item.Status = strings.ToLower(strings.TrimSpace(item.Status))
		if item.Status == "resolved" && item.ID != "" {
			resolvedIDs[item.ID] = struct{}{}
		}
		if item.Title == "" && item.Body == "" {
			continue
		}
		if !validLiveAnalysisItemKind(item.Kind) {
			continue
		}
		if !validLiveAnalysisSeverity(item.Severity) {
			item.Severity = "medium"
		}
		if !validLiveAnalysisItemStatus(item.Status) {
			item.Status = "open"
		}
		if _, resolved := resolvedIDs[item.ID]; item.ID != "" && resolved {
			item.Status = "resolved"
		}
		normalized = append(normalized, item)
	}
	return normalized
}

// mergeLiveAnalysisItems merges the model's diff items into the previous
// state: previous items keep their order, a diff item with an existing id
// replaces that item in place (status forced to "updated" unless it is
// resolved), and new ids are appended. Items whose id is in resolvedIDs are
// retained and marked resolved. Active (status != "resolved") and resolved
// items are then capped independently -- active at liveAnalysisItemsMaxCount
// and resolved at liveAnalysisResolvedItemsMaxCount, each evicting its own
// oldest entries first -- so a flood of one kind can never evict the other.
// The returned list preserves the merged list's original relative order.
func mergeLiveAnalysisItems(previous, diff []liveAnalysisItem, resolvedIDs map[string]struct{}) []liveAnalysisItem {
	merged := make([]liveAnalysisItem, 0, len(previous)+len(diff))
	index := make(map[string]int, len(previous)+len(diff))
	for _, item := range previous {
		if item.ID != "" {
			if _, resolved := resolvedIDs[item.ID]; resolved {
				item.Status = "resolved"
			}
			index[item.ID] = len(merged)
		}
		merged = append(merged, item)
	}
	for _, item := range diff {
		if item.ID != "" {
			if _, resolved := resolvedIDs[item.ID]; resolved {
				item.Status = "resolved"
			}
			if at, ok := index[item.ID]; ok {
				if item.Status != "resolved" {
					item.Status = "updated"
				}
				merged[at] = item
				continue
			}
			index[item.ID] = len(merged)
		}
		merged = append(merged, item)
	}
	return capLiveAnalysisItems(merged, liveAnalysisItemsMaxCount, liveAnalysisResolvedItemsMaxCount)
}

// capLiveAnalysisItems caps active and resolved items independently: active
// (status != "resolved") items are capped at activeMax and resolved items at
// resolvedMax, each evicting its own oldest entries first. The result
// preserves the input's original relative order.
func capLiveAnalysisItems(items []liveAnalysisItem, activeMax, resolvedMax int) []liveAnalysisItem {
	activeCount, resolvedCount := 0, 0
	for _, item := range items {
		if item.Status == "resolved" {
			resolvedCount++
		} else {
			activeCount++
		}
	}
	if activeCount <= activeMax && resolvedCount <= resolvedMax {
		return items
	}

	activeExcess := activeCount - activeMax
	resolvedExcess := resolvedCount - resolvedMax
	kept := make([]liveAnalysisItem, 0, len(items))
	for _, item := range items {
		if item.Status == "resolved" {
			if resolvedExcess > 0 {
				resolvedExcess--
				continue
			}
		} else {
			if activeExcess > 0 {
				activeExcess--
				continue
			}
		}
		kept = append(kept, item)
	}
	return kept
}

// flatTreeMinTopicChildren/flatTreeChildRatioThreshold are the thresholds
// finalizeLiveAnalysisTree uses to flag a tree as "flat" (see
// liveAnalysisTreeMergeStats.FlatTreeDetected): either the primary topic
// node has an unusually large number of direct children with almost no
// depth beyond it, or an unusually large share of all nodes hang directly
// off the primary topic. This is observability only -- it never changes the
// merge result -- so operators can tell "the model never produced 大分類
// (major-category) topic nodes" apart from a healthy, intentionally broad
// tree.
const (
	flatTreeMinTopicChildren    = 8
	flatTreeChildRatioThreshold = 0.7
)

// liveAnalysisTreeMergeStats collects diagnostics from a single
// mergeLiveAnalysisTree call for observability logging only; it never
// affects the merge result. Passing a nil *liveAnalysisTreeMergeStats
// disables collection entirely, so mergeLiveAnalysisTree stays usable as a
// plain pure function wherever the diagnostics are not needed (e.g. tests
// that don't care about them, or via parseAndMergeLiveAnalysisPayload's
// omitted variadic stats argument).
type liveAnalysisTreeMergeStats struct {
	// DroppedEmptyID/DroppedEmptyLabel/DroppedInvalidKind count nodes
	// discarded by addNode's validation, broken down by reason, so an
	// operator can tell "the model produced a tree node but it failed
	// validation" apart from "the model produced no tree node at all"
	// (countLiveAnalysisDiffStats covers the latter).
	DroppedEmptyID     int
	DroppedEmptyLabel  int
	DroppedInvalidKind int
	// DroppedEdges counts edges removed by finalizeLiveAnalysisTree because
	// their source or target id is not in the final node set (whether
	// because the model referenced an unknown id or because the node was
	// evicted by the node cap).
	DroppedEdges int
	// SynthesizedNodes counts nodes mergeLiveAnalysisTree created itself
	// (from an item that had no corresponding tree node) rather than
	// receiving from the model.
	SynthesizedNodes int
	// PrunedTopicEdges counts redundant "primary topic -> X" fallback edges
	// removed by pruneRedundantTopicFallbackEdges because X also has a more
	// specific parent elsewhere in the tree. See pruneRedundantTopicFallbackEdges
	// for what makes an edge a pruning candidate in the first place.
	PrunedTopicEdges int
	// NormalizedTodoNodes は、モデルが tree 側に出した kind "todo" のノードを
	// "issue" に正規化して救済した件数。
	NormalizedTodoNodes int
	// DiffNewNodes / DiffUpdatedNodes は、モデルの差分ノードのうち、それぞれ
	// 「既存に無いidで新規追加されたもの」「既存idを上書き更新したもの」の件数。
	// (サーバが合成したノードや前回状態のノードは数えない。)
	DiffNewNodes     int
	DiffUpdatedNodes int
	// OrphanRescuedEdges は connectOrphanLiveAnalysisTreeNodes が追加した
	// 「topic -> 孤立ノード」救済エッジの件数。
	OrphanRescuedEdges int
	// TotalEdges / TopicChildCount / MaxDepth はマージ確定後のツリー形状。
	// TopicChildCount は主topicノードを source に持つエッジの異なるtarget数、
	// MaxDepth は主topicノードからの最長深さ(topic自身を深さ0とする)。
	TotalEdges      int
	TopicChildCount int
	MaxDepth        int
	// FlatTreeDetected は、大分類(root以外のtopicノード)がほぼ無いまま個々の
	// ノードがroot直下に並んでしまっている(平坦化)兆候を検知したか。
	// flatTreeMinTopicChildren/flatTreeChildRatioThreshold のいずれかを満たすと
	// true になる。マージ結果には影響しないログ用フラグ。
	FlatTreeDetected bool
	// ReparentedNodes は reparentRootFallbackNodes が「root直下への救済のみ」
	// だったノードを、後から現れた大分類topicノード配下へ付け替えた件数。
	ReparentedNodes int
	// DroppedNodeDetails は破棄された各ノードの詳細(開発用フラグ有効時のみログ出力)。
	DroppedNodeDetails []liveAnalysisDroppedNodeDetail
}

// liveAnalysisDroppedNodeDetail は addNode が破棄した個々のノードの内訳。
// Title はノードのlabel(会議内容を含みうるため、ログ出力は開発用フラグ有効時のみ)。
type liveAnalysisDroppedNodeDetail struct {
	ID     string
	Kind   string
	Title  string
	Reason string
}

// droppedNodes returns the total node count dropped for any reason. A nil
// receiver returns 0, matching the "collection disabled" contract.
func (s *liveAnalysisTreeMergeStats) droppedNodes() int {
	if s == nil {
		return 0
	}
	return s.DroppedEmptyID + s.DroppedEmptyLabel + s.DroppedInvalidKind
}

// droppedNodeReasons renders the per-reason breakdown as a log-friendly
// "[reason:count ...]" string, e.g. "[emptyLabel:1 invalidKind:1]". Reasons
// with a zero count are omitted.
func (s *liveAnalysisTreeMergeStats) droppedNodeReasons() string {
	var parts []string
	if s != nil {
		if s.DroppedEmptyID > 0 {
			parts = append(parts, fmt.Sprintf("emptyId:%d", s.DroppedEmptyID))
		}
		if s.DroppedEmptyLabel > 0 {
			parts = append(parts, fmt.Sprintf("emptyLabel:%d", s.DroppedEmptyLabel))
		}
		if s.DroppedInvalidKind > 0 {
			parts = append(parts, fmt.Sprintf("invalidKind:%d", s.DroppedInvalidKind))
		}
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// liveAnalysisItemKindToTreeNodeKindFallback maps liveAnalysisItem kinds that
// are not valid tree node kinds (validLiveAnalysisTreeNodeKind) to the
// closest valid tree node kind. It is used only when synthesizing a tree
// node from an item that has no corresponding tree node (see
// mergeLiveAnalysisTree). Every valid item kind except "todo" is already a
// valid tree node kind; "todo" maps to "issue" because a to-do is
// functionally an actionable issue in the discussion tree's vocabulary.
var liveAnalysisItemKindToTreeNodeKindFallback = map[string]string{
	"todo": "issue",
}

// liveAnalysisTreeNodeKindForItem returns the tree node kind to use when
// synthesizing a node for an item: the item's own kind when it is already a
// valid tree node kind, its mapped fallback from
// liveAnalysisItemKindToTreeNodeKindFallback otherwise, and "issue" as a
// last-resort default (defensive only: normalizeLiveAnalysisItems already
// restricts item.Kind to the known item vocabulary by the time this runs).
func liveAnalysisTreeNodeKindForItem(itemKind string) string {
	if validLiveAnalysisTreeNodeKind(itemKind) {
		return itemKind
	}
	if mapped, ok := liveAnalysisItemKindToTreeNodeKindFallback[itemKind]; ok {
		return mapped
	}
	return "issue"
}

// mergeLiveAnalysisTree merges the model's diff tree into the previous tree:
// diff nodes upsert previous nodes by id (new nodes are appended), nodes with
// a resolved id are retained and marked resolved, relatedItemIds are
// normalized against the merged item set, and edges are the deduplicated union
// of previous and diff edges.
//
// After the model's own nodes are merged, any item in items (except a
// dismissed one) whose id has no corresponding tree node gets a node
// synthesized for it (id/label/description/status derived from the item,
// see the synthesis loop below), so a card the model reports always has a
// matching tree node even when the model omits the tree entirely. A
// synthesized node sitting near the eviction edge of the node cap may be
// evicted and then re-synthesized on a later tick if the model still hasn't
// emitted its own node for it; this is an accepted tradeoff, not a bug --
// liveAnalysisTreeMaxNodes/liveAnalysisTreeMaxResolvedNodes were raised
// specifically to keep this churn rare.
//
// The merged set then goes through finalizeLiveAnalysisTree for topic
// completion, the node cap, and edge validation. stats may be nil to skip
// diagnostics collection.
func mergeLiveAnalysisTree(previous, diff *liveAnalysisTree, resolvedIDs map[string]struct{}, currentTopic string, items []liveAnalysisItem, stats *liveAnalysisTreeMergeStats) *liveAnalysisTree {
	itemIDs := liveAnalysisItemIDSet(items)
	var nodes []liveAnalysisTreeNode
	index := make(map[string]int)
	addNode := func(node liveAnalysisTreeNode, fromDiff bool) bool {
		node.ID = strings.TrimSpace(node.ID)
		node.Kind = strings.ToLower(strings.TrimSpace(node.Kind))
		node.Label = strings.TrimSpace(node.Label)
		node.Status = strings.ToLower(strings.TrimSpace(node.Status))
		node.Description = truncateRunes(strings.TrimSpace(node.Description), liveAnalysisTreeDescriptionMaxRunes)
		node.RelatedItemIDs = normalizeLiveAnalysisRelatedItemIDs(node.RelatedItemIDs, node.ID, itemIDs)
		// モデルが tree 側に item専用の kind "todo" を出した場合の救済。tree の
		// 語彙には "todo" が無いため、これが無いと本来ノードを追加しようとした
		// モデルの出力が invalidKind として捨てられてしまう。意図的に "todo" だけを
		// 対象にし、それ以外の未知kindは下の検証で従来どおり drop する。
		if node.Kind == "todo" {
			node.Kind = "issue"
			if stats != nil {
				stats.NormalizedTodoNodes++
			}
		}
		if node.ID == "" {
			if stats != nil {
				stats.DroppedEmptyID++
				stats.DroppedNodeDetails = append(stats.DroppedNodeDetails, liveAnalysisDroppedNodeDetail{ID: node.ID, Kind: node.Kind, Title: node.Label, Reason: "emptyId"})
			}
			return false
		}
		if node.Label == "" {
			if stats != nil {
				stats.DroppedEmptyLabel++
				stats.DroppedNodeDetails = append(stats.DroppedNodeDetails, liveAnalysisDroppedNodeDetail{ID: node.ID, Kind: node.Kind, Title: node.Label, Reason: "emptyLabel"})
			}
			return false
		}
		if !validLiveAnalysisTreeNodeKind(node.Kind) {
			if stats != nil {
				stats.DroppedInvalidKind++
				stats.DroppedNodeDetails = append(stats.DroppedNodeDetails, liveAnalysisDroppedNodeDetail{ID: node.ID, Kind: node.Kind, Title: node.Label, Reason: "invalidKind"})
			}
			return false
		}
		if node.Status != "" && !validLiveAnalysisTreeNodeStatus(node.Status) {
			node.Status = ""
		}
		if _, resolved := resolvedIDs[node.ID]; resolved {
			node.Status = "resolved"
		}
		if at, ok := index[node.ID]; ok {
			if node.Status == "" {
				if fromDiff && nodes[at].Status == "resolved" {
					node.Status = "updated"
				} else {
					node.Status = nodes[at].Status
				}
			}
			if node.Description == "" {
				node.Description = nodes[at].Description
			}
			if len(node.RelatedItemIDs) == 0 {
				node.RelatedItemIDs = nodes[at].RelatedItemIDs
			}
			nodes[at] = node
			if fromDiff && stats != nil {
				stats.DiffUpdatedNodes++
			}
			return true
		}
		index[node.ID] = len(nodes)
		nodes = append(nodes, node)
		if fromDiff && stats != nil {
			stats.DiffNewNodes++
		}
		return true
	}
	var rawEdges []liveAnalysisTreeEdge
	if previous != nil {
		for _, node := range previous.Nodes {
			addNode(node, false)
		}
		rawEdges = append(rawEdges, previous.Edges...)
	}
	if diff != nil {
		for _, node := range diff.Nodes {
			addNode(node, true)
		}
		rawEdges = append(rawEdges, diff.Edges...)
	}

	for _, item := range items {
		if item.ID == "" || item.Status == "dismissed" {
			continue
		}
		if _, ok := index[item.ID]; ok {
			continue
		}
		status := ""
		if item.Status == "resolved" {
			status = "resolved"
		}
		synthesized := liveAnalysisTreeNode{
			ID:             item.ID,
			Kind:           liveAnalysisTreeNodeKindForItem(item.Kind),
			Label:          item.Title,
			Status:         status,
			Description:    item.Body,
			RelatedItemIDs: []string{item.ID},
		}
		if addNode(synthesized, false) && stats != nil {
			stats.SynthesizedNodes++
		}
	}

	edges := make([]liveAnalysisTreeEdge, 0, len(rawEdges))
	seenEdges := make(map[string]struct{}, len(rawEdges))
	for _, edge := range rawEdges {
		edge.Source = strings.TrimSpace(edge.Source)
		edge.Target = strings.TrimSpace(edge.Target)
		key := edge.Source + "\x00" + edge.Target
		if _, ok := seenEdges[key]; ok {
			continue
		}
		seenEdges[key] = struct{}{}
		edges = append(edges, edge)
	}
	return finalizeLiveAnalysisTree(nodes, edges, currentTopic, stats)
}

func liveAnalysisItemIDSet(items []liveAnalysisItem) map[string]struct{} {
	ids := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.ID != "" {
			ids[item.ID] = struct{}{}
		}
	}
	return ids
}

func normalizeLiveAnalysisRelatedItemIDs(ids []string, nodeID string, itemIDs map[string]struct{}) []string {
	normalized := make([]string, 0, len(ids)+1)
	seen := make(map[string]struct{}, len(ids)+1)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := itemIDs[id]; !ok {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}

	add(nodeID)
	for _, id := range ids {
		add(id)
	}
	return normalized
}

// finalizeLiveAnalysisTree applies the invariants of the stored tree to a
// merged node/edge set: an empty node list collapses the tree to nil
// (serialized as JSON null); when there is no topic node and currentTopic is
// non-empty a synthetic "topic-current" node is inserted at the head (unless
// a node already uses that id); resolved (status "resolved") non-topic nodes
// and active nodes (topic nodes plus non-resolved non-topic nodes) are then
// capped independently -- active at liveAnalysisTreeMaxNodes (keeping topic
// nodes and evicting the oldest non-topic nodes first) and resolved at
// liveAnalysisTreeMaxResolvedNodes (evicting the oldest resolved nodes
// first) -- so a burst of active discussion can never evict resolved nodes
// or vice versa; edges referencing missing nodes are dropped; and any node
// without an incoming edge (including secondary topic nodes created by a
// topic change) is connected to the primary topic node when one exists.
func finalizeLiveAnalysisTree(nodes []liveAnalysisTreeNode, edges []liveAnalysisTreeEdge, currentTopic string, stats *liveAnalysisTreeMergeStats) *liveAnalysisTree {
	if len(nodes) == 0 {
		return nil
	}
	hasTopicNode := false
	hasCurrentTopicID := false
	for _, node := range nodes {
		if node.Kind == "topic" {
			hasTopicNode = true
		}
		if node.ID == liveAnalysisCurrentTopicNodeID {
			hasCurrentTopicID = true
		}
	}

	insertedTopic := false
	currentTopic = strings.TrimSpace(currentTopic)
	if !hasTopicNode && currentTopic != "" && !hasCurrentTopicID {
		nodes = append([]liveAnalysisTreeNode{{
			ID:    liveAnalysisCurrentTopicNodeID,
			Kind:  "topic",
			Label: truncateRunes(currentTopic, liveAnalysisTopicLabelMaxRunes),
		}}, nodes...)
		insertedTopic = true
	}

	nodes = capLiveAnalysisTreeNodes(nodes, liveAnalysisTreeMaxNodes, liveAnalysisTreeMaxResolvedNodes)
	nodeIDs := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		nodeIDs[node.ID] = struct{}{}
	}
	validEdges := make([]liveAnalysisTreeEdge, 0, len(edges))
	for _, edge := range edges {
		if _, ok := nodeIDs[edge.Source]; !ok {
			continue
		}
		if _, ok := nodeIDs[edge.Target]; !ok {
			continue
		}
		validEdges = append(validEdges, edge)
	}
	if stats != nil {
		stats.DroppedEdges += len(edges) - len(validEdges)
	}

	beforeOrphan := len(validEdges)
	validEdges = connectOrphanLiveAnalysisTreeNodes(nodes, validEdges, insertedTopic)
	if stats != nil {
		stats.OrphanRescuedEdges += len(validEdges) - beforeOrphan
	}

	topicID := primaryLiveAnalysisTopicID(nodes, insertedTopic)
	validEdges = reparentRootFallbackNodes(nodes, validEdges, topicID, stats)
	prunedEdges := pruneRedundantTopicFallbackEdges(nodes, validEdges, topicID)
	if stats != nil {
		stats.PrunedTopicEdges += len(validEdges) - len(prunedEdges)
	}
	if stats != nil {
		stats.TotalEdges = len(prunedEdges)
		stats.TopicChildCount = countLiveAnalysisTopicChildren(topicID, prunedEdges)
		stats.MaxDepth = liveAnalysisTreeMaxDepth(topicID, prunedEdges)
		totalNodes := len(nodes)
		stats.FlatTreeDetected = (stats.TopicChildCount >= flatTreeMinTopicChildren && stats.MaxDepth <= 1) ||
			(totalNodes > 0 && float64(stats.TopicChildCount)/float64(totalNodes) > flatTreeChildRatioThreshold)
	}
	return &liveAnalysisTree{Nodes: nodes, Edges: prunedEdges}
}

// primaryLiveAnalysisTopicID returns the id of the tree's primary topic
// node: the same node connectOrphanLiveAnalysisTreeNodes and
// pruneRedundantTopicFallbackEdges treat as "the" topic node when a tree
// carries more than one. It is the first topic-kind node in nodes, unless
// preferCurrentTopic is set and a node with id liveAnalysisCurrentTopicNodeID
// exists (the topic node for the newest topic change), in which case that
// node wins instead. Returns "" when nodes contains no topic node.
func primaryLiveAnalysisTopicID(nodes []liveAnalysisTreeNode, preferCurrentTopic bool) string {
	topicID := ""
	for _, node := range nodes {
		if node.Kind != "topic" {
			continue
		}
		if topicID == "" {
			topicID = node.ID
		}
		if preferCurrentTopic && node.ID == liveAnalysisCurrentTopicNodeID {
			return node.ID
		}
	}
	return topicID
}

// chooseOrphanParentID は、親エッジが必要なノードに与える親idを選ぶ。
// 詳細ノード(topic以外)は、root以外の「大分類」(root以外のtopicノード)のうち、
// そのノードの子孫でない最新のものを優先する。適切な大分類が無ければ primaryTopicID
// (root)を返す。topicノード(大分類)は常に primaryTopicID にぶら下げる
// (既存の second-topic 挙動を保つ)。
func chooseOrphanParentID(node liveAnalysisTreeNode, nodes []liveAnalysisTreeNode, edges []liveAnalysisTreeEdge, primaryTopicID string) string {
	if node.Kind == "topic" {
		return primaryTopicID
	}
	nodeIDs := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		nodeIDs[n.ID] = struct{}{}
	}
	descendants := reachableLiveAnalysisNodeIDs(node.ID, edges, nodeIDs) // 自分+子孫(サイクル防止用)
	best := ""
	for _, n := range nodes { // nodes順で最後に見つかった候補=最新を採用
		if n.Kind != "topic" || n.ID == primaryTopicID || n.ID == node.ID {
			continue
		}
		if descendants[n.ID] { // 自分の子孫を親にしない
			continue
		}
		best = n.ID
	}
	if best == "" {
		return primaryTopicID
	}
	return best
}

func connectOrphanLiveAnalysisTreeNodes(nodes []liveAnalysisTreeNode, edges []liveAnalysisTreeEdge, preferCurrentTopic bool) []liveAnalysisTreeEdge {
	topicID := primaryLiveAnalysisTopicID(nodes, preferCurrentTopic)
	if topicID == "" {
		return edges
	}

	hasIncoming := make(map[string]bool, len(nodes))
	seenEdges := make(map[string]struct{}, len(edges)+len(nodes))
	for _, edge := range edges {
		hasIncoming[edge.Target] = true
		seenEdges[edge.Source+"\x00"+edge.Target] = struct{}{}
	}
	for _, node := range nodes {
		if node.ID == topicID || hasIncoming[node.ID] {
			continue
		}
		parent := chooseOrphanParentID(node, nodes, edges, topicID)
		if parent == node.ID {
			continue
		}
		key := parent + "\x00" + node.ID
		if _, ok := seenEdges[key]; ok {
			continue
		}
		seenEdges[key] = struct{}{}
		edges = append(edges, liveAnalysisTreeEdge{Source: parent, Target: node.ID})
	}
	return edges
}

// reparentRootFallbackNodes は、入ってくるエッジが primaryTopicID からの1本だけ
// (=以前のラウンドで大分類が無くrootへ救済された)非topicノードを、いま利用可能な
// 大分類(root以外のtopicノード)配下へ移動する。root直下の過剰なぶら下がりを、
// 後続ラウンドで大分類が現れた時点で解消するための保守的な付け替え。サイクルは作らない。
func reparentRootFallbackNodes(nodes []liveAnalysisTreeNode, edges []liveAnalysisTreeEdge, primaryTopicID string, stats *liveAnalysisTreeMergeStats) []liveAnalysisTreeEdge {
	if primaryTopicID == "" {
		return edges
	}
	// 各ノードの incoming source を集計
	incoming := make(map[string][]string)
	for _, e := range edges {
		incoming[e.Target] = append(incoming[e.Target], e.Source)
	}
	result := edges
	for _, n := range nodes {
		if n.Kind == "topic" {
			continue
		}
		srcs := incoming[n.ID]
		// 入ってくるエッジが root からの1本だけの場合のみ対象
		if len(srcs) != 1 || srcs[0] != primaryTopicID {
			continue
		}
		parent := chooseOrphanParentID(n, nodes, result, primaryTopicID)
		if parent == primaryTopicID || parent == n.ID {
			continue
		}
		// root->n を削除し parent->n を追加
		replaced := make([]liveAnalysisTreeEdge, 0, len(result))
		for _, e := range result {
			if e.Source == primaryTopicID && e.Target == n.ID {
				continue
			}
			replaced = append(replaced, e)
		}
		replaced = append(replaced, liveAnalysisTreeEdge{Source: parent, Target: n.ID})
		result = replaced
		if stats != nil {
			stats.ReparentedNodes++
		}
	}
	return result
}

// pruneRedundantTopicFallbackEdges removes "primary topic -> X" edges that
// have become redundant because X also has a direct edge from some other,
// more specific node. This targets the exact edge shape
// connectOrphanLiveAnalysisTreeNodes produces: when a node is first added
// without a clear parent, that function connects it directly from the
// primary topic node as a fallback. Edges are otherwise only ever added, not
// removed, across merge rounds -- so if the model later reports the node's
// real parent (some other node Y -> X) in a later round, the earlier
// "topic -> X" fallback edge would linger forever, leaving the topic node
// wrongly connected to nodes several levels deep in the discussion tree.
//
// A "topic -> X" edge is a pruning candidate only when X has at least one
// other incoming edge from a source other than the topic node. This is
// exactly what keeps the orphan-rescue behavior in
// connectOrphanLiveAnalysisTreeNodes intact: a freshly rescued node has
// exactly one incoming edge (the fallback itself), so it is never a
// candidate here and the fallback survives until a real parent shows up.
//
// Because edges otherwise only accumulate, blindly dropping every candidate
// could disconnect the tree if a candidate's target is only reachable
// through the very edges being removed. To stay conservative, this computes
// the set of nodes reachable from the topic using only the non-candidate
// edges (i.e. the edges that would remain if every candidate were dropped),
// and removes a candidate edge only when its target is still in that
// reachable set. A candidate whose target would become unreachable is left
// in place instead of being dropped.
func pruneRedundantTopicFallbackEdges(nodes []liveAnalysisTreeNode, edges []liveAnalysisTreeEdge, topicID string) []liveAnalysisTreeEdge {
	if topicID == "" || len(edges) == 0 {
		return edges
	}
	nodeIDs := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		nodeIDs[node.ID] = struct{}{}
	}

	incomingFromOther := make(map[string]bool, len(edges))
	for _, edge := range edges {
		if edge.Source != topicID {
			incomingFromOther[edge.Target] = true
		}
	}

	candidate := make([]bool, len(edges))
	hasCandidate := false
	survivingEdges := make([]liveAnalysisTreeEdge, 0, len(edges))
	for i, edge := range edges {
		if edge.Source == topicID && incomingFromOther[edge.Target] {
			candidate[i] = true
			hasCandidate = true
			continue
		}
		survivingEdges = append(survivingEdges, edge)
	}
	if !hasCandidate {
		return edges
	}

	reachable := reachableLiveAnalysisNodeIDs(topicID, survivingEdges, nodeIDs)

	pruned := make([]liveAnalysisTreeEdge, 0, len(edges))
	removedAny := false
	for i, edge := range edges {
		if candidate[i] && reachable[edge.Target] {
			removedAny = true
			continue
		}
		pruned = append(pruned, edge)
	}
	if !removedAny {
		return edges
	}
	return pruned
}

// reachableLiveAnalysisNodeIDs returns the set of node ids reachable from
// rootID by following edges as directed source -> target links (a plain
// BFS). Only ids present in validIDs are followed, defensively guarding
// against edges referencing an id outside the current node set.
func reachableLiveAnalysisNodeIDs(rootID string, edges []liveAnalysisTreeEdge, validIDs map[string]struct{}) map[string]bool {
	adjacency := make(map[string][]string, len(edges))
	for _, edge := range edges {
		if _, ok := validIDs[edge.Source]; !ok {
			continue
		}
		if _, ok := validIDs[edge.Target]; !ok {
			continue
		}
		adjacency[edge.Source] = append(adjacency[edge.Source], edge.Target)
	}
	visited := map[string]bool{rootID: true}
	queue := []string{rootID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adjacency[current] {
			if !visited[next] {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	return visited
}

// countLiveAnalysisTopicChildren は topicID を source に持つエッジの異なる
// target数(=topic直下のノード数)を返す。topicID が空なら0。
func countLiveAnalysisTopicChildren(topicID string, edges []liveAnalysisTreeEdge) int {
	if topicID == "" {
		return 0
	}
	children := make(map[string]struct{})
	for _, edge := range edges {
		if edge.Source == topicID {
			children[edge.Target] = struct{}{}
		}
	}
	return len(children)
}

// liveAnalysisTreeMaxDepth は topicID から source->target 方向にたどれる最長深さ
// を返す(topic自身を深さ0)。訪問済み管理でサイクルを防ぐ。topicID が空なら0。
func liveAnalysisTreeMaxDepth(topicID string, edges []liveAnalysisTreeEdge) int {
	if topicID == "" {
		return 0
	}
	adjacency := make(map[string][]string, len(edges))
	for _, edge := range edges {
		adjacency[edge.Source] = append(adjacency[edge.Source], edge.Target)
	}
	type frame struct {
		id    string
		depth int
	}
	visited := map[string]bool{topicID: true}
	queue := []frame{{id: topicID, depth: 0}}
	maxDepth := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.depth > maxDepth {
			maxDepth = current.depth
		}
		for _, next := range adjacency[current.id] {
			if !visited[next] {
				visited[next] = true
				queue = append(queue, frame{id: next, depth: current.depth + 1})
			}
		}
	}
	return maxDepth
}

// capLiveAnalysisTreeNodes caps active and resolved nodes independently:
// resolved (status "resolved") non-topic nodes go in one bucket capped at
// maxResolved, and every other node (topic nodes plus non-resolved non-topic
// nodes) goes in the active bucket capped at maxActive. Each bucket is capped
// on its own so neither can evict the other, and the result preserves the
// input's original relative order.
func capLiveAnalysisTreeNodes(nodes []liveAnalysisTreeNode, maxActive, maxResolved int) []liveAnalysisTreeNode {
	var activeNodes, resolvedNodes []liveAnalysisTreeNode
	for _, node := range nodes {
		if node.Kind != "topic" && node.Status == "resolved" {
			resolvedNodes = append(resolvedNodes, node)
		} else {
			activeNodes = append(activeNodes, node)
		}
	}
	if len(activeNodes) <= maxActive && len(resolvedNodes) <= maxResolved {
		return nodes
	}

	keptActive := capActiveLiveAnalysisTreeNodes(activeNodes, maxActive)
	keptResolved := capResolvedLiveAnalysisTreeNodes(resolvedNodes, maxResolved)

	keepIDs := make(map[string]struct{}, len(keptActive)+len(keptResolved))
	for _, node := range keptActive {
		keepIDs[node.ID] = struct{}{}
	}
	for _, node := range keptResolved {
		keepIDs[node.ID] = struct{}{}
	}
	kept := make([]liveAnalysisTreeNode, 0, len(keepIDs))
	for _, node := range nodes {
		if _, ok := keepIDs[node.ID]; ok {
			kept = append(kept, node)
		}
	}
	return kept
}

// capActiveLiveAnalysisTreeNodes trims the active node list to max entries,
// evicting the oldest non-topic nodes first so topic nodes survive. If topic
// nodes alone exceed the cap, the oldest topics are evicted too.
func capActiveLiveAnalysisTreeNodes(nodes []liveAnalysisTreeNode, max int) []liveAnalysisTreeNode {
	if len(nodes) <= max {
		return nodes
	}
	excess := len(nodes) - max
	kept := make([]liveAnalysisTreeNode, 0, max)
	for _, node := range nodes {
		if excess > 0 && node.Kind != "topic" {
			excess--
			continue
		}
		kept = append(kept, node)
	}
	if len(kept) > max {
		kept = kept[len(kept)-max:]
	}
	return kept
}

// capResolvedLiveAnalysisTreeNodes trims the resolved node list to max
// entries, evicting the oldest resolved nodes first.
func capResolvedLiveAnalysisTreeNodes(nodes []liveAnalysisTreeNode, max int) []liveAnalysisTreeNode {
	if len(nodes) <= max {
		return nodes
	}
	return nodes[len(nodes)-max:]
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

// previousLiveAnalysisState parses the previously stored payload. A missing
// or invalid previous payload degrades to an empty state instead of failing,
// so a corrupt row can never wedge live analysis.
func previousLiveAnalysisState(previousPayload json.RawMessage) liveAnalysisPayload {
	if len(previousPayload) == 0 {
		return liveAnalysisPayload{}
	}
	var previous liveAnalysisPayload
	if err := json.Unmarshal(previousPayload, &previous); err != nil {
		return liveAnalysisPayload{}
	}
	previous.Summary = strings.TrimSpace(previous.Summary)
	previous.CurrentTopic = strings.TrimSpace(previous.CurrentTopic)
	return previous
}

// parseAndMergeLiveAnalysisPayload parses the model output as a diff (only
// new/changed items, new/changed tree nodes, new edges, and resolvedIds) and
// merges it into the previous payload, producing the complete state that is
// stored and broadcast. The model only reports changes; the server owns
// state retention, so weak-reasoning models cannot lose accumulated items by
// echoing a stale snapshot.
//
// The optional trailing stats argument receives tree-merge diagnostics
// (dropped nodes/edges, synthesized nodes) for observability logging; it is
// a variadic *liveAnalysisTreeMergeStats rather than a plain trailing
// parameter so every existing caller (including all tests) keeps compiling
// unchanged. Pass no argument, or nil, to skip collection.
func parseAndMergeLiveAnalysisPayload(content string, previousPayload json.RawMessage, stats ...*liveAnalysisTreeMergeStats) (json.RawMessage, error) {
	var treeStats *liveAnalysisTreeMergeStats
	if len(stats) > 0 {
		treeStats = stats[0]
	}
	cleaned := stripJSONCodeFence(content)
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(cleaned), &diff); err != nil {
		return nil, fmt.Errorf("parse live analysis payload: %w", err)
	}
	previous := previousLiveAnalysisState(previousPayload)

	resolvedIDs := make(map[string]struct{}, len(diff.ResolvedIds))
	for _, id := range diff.ResolvedIds {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			resolvedIDs[trimmed] = struct{}{}
		}
	}
	diffItems := normalizeLiveAnalysisItems(diff.Items, resolvedIDs)

	merged := liveAnalysisPayload{
		Summary:      firstNonEmptyTrimmed(diff.Summary, previous.Summary),
		CurrentTopic: firstNonEmptyTrimmed(diff.CurrentTopic, previous.CurrentTopic),
	}
	merged.Items = mergeLiveAnalysisItems(previous.Items, diffItems, resolvedIDs)
	merged.Tree = mergeLiveAnalysisTree(previous.Tree, diff.Tree, resolvedIDs, merged.CurrentTopic, merged.Items, treeStats)
	if merged.isEmpty() {
		return nil, fmt.Errorf("live analysis payload is empty")
	}
	if merged.Items == nil {
		merged.Items = []liveAnalysisItem{}
	}
	normalized, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal normalized live analysis payload: %w", err)
	}
	return normalized, nil
}

// liveAnalysisPayloadStats summarizes item/node counts of a merged live
// analysis payload for observability logging only.
type liveAnalysisPayloadStats struct {
	TotalItems    int
	ResolvedItems int
	TotalNodes    int
	ResolvedNodes int
}

// countLiveAnalysisPayloadStats re-parses an already-merged payload to count
// items/nodes and how many of each are resolved. It re-parses rather than
// threading extra return values through parseAndMergeLiveAnalysisPayload
// because the counts are only needed for the completion log line.
func countLiveAnalysisPayloadStats(payload json.RawMessage) liveAnalysisPayloadStats {
	var stats liveAnalysisPayloadStats
	if len(payload) == 0 {
		return stats
	}
	var parsed liveAnalysisPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return stats
	}
	stats.TotalItems = len(parsed.Items)
	for _, item := range parsed.Items {
		if item.Status == "resolved" {
			stats.ResolvedItems++
		}
	}
	if parsed.Tree != nil {
		stats.TotalNodes = len(parsed.Tree.Nodes)
		for _, node := range parsed.Tree.Nodes {
			if node.Status == "resolved" {
				stats.ResolvedNodes++
			}
		}
	}
	return stats
}

// countModelResolvedIDs counts how many resolvedIds the model reported in its
// raw completion output for this round. It is used only for observability
// logging, so an operator can tell "the model never reports resolvedIds"
// apart from "the model reports them but they get dropped/evicted downstream".
func countModelResolvedIDs(content string) int {
	cleaned := stripJSONCodeFence(content)
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(cleaned), &diff); err != nil {
		return 0
	}
	count := 0
	for _, id := range diff.ResolvedIds {
		if strings.TrimSpace(id) != "" {
			count++
		}
	}
	return count
}

// countLiveAnalysisDiffStats counts how many items, tree nodes, and tree
// edges the model reported in its raw completion output for this round,
// before any validation or merge. Like countModelResolvedIDs, it is used
// only for observability logging, so an operator can tell "the model
// reported 0 diff tree nodes" (the model itself never emits a tree) apart
// from "the model reported N diff tree nodes but validation/merge dropped
// them" (see liveAnalysisTreeMergeStats).
func countLiveAnalysisDiffStats(content string) (items, treeNodes, treeEdges int) {
	cleaned := stripJSONCodeFence(content)
	var diff liveAnalysisPayload
	if err := json.Unmarshal([]byte(cleaned), &diff); err != nil {
		return 0, 0, 0
	}
	items = len(diff.Items)
	if diff.Tree != nil {
		treeNodes = len(diff.Tree.Nodes)
		treeEdges = len(diff.Tree.Edges)
	}
	return items, treeNodes, treeEdges
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type finalAnalysisDecisionItem struct {
	Text       string `json:"text"`
	Importance string `json:"importance"`
}

type finalAnalysisActionItem struct {
	Text     string `json:"text"`
	Owner    string `json:"owner"`
	Due      string `json:"due"`
	Priority string `json:"priority"`
}

type finalAnalysisPayload struct {
	SuggestedTitle    string                      `json:"suggestedTitle"`
	Overview          string                      `json:"overview"`
	Decisions         []finalAnalysisDecisionItem `json:"decisions"`
	ActionItems       []finalAnalysisActionItem   `json:"actionItems"`
	OpenIssues        []string                    `json:"openIssues"`
	KeyPoints         []string                    `json:"keyPoints"`
	NextMeetingTopics []string                    `json:"nextMeetingTopics"`
}

func (p finalAnalysisPayload) isEmpty() bool {
	return strings.TrimSpace(p.SuggestedTitle) == "" && strings.TrimSpace(p.Overview) == "" &&
		len(p.Decisions) == 0 && len(p.ActionItems) == 0 && len(p.OpenIssues) == 0 &&
		len(p.KeyPoints) == 0 && len(p.NextMeetingTopics) == 0
}

func parseAndValidateFinalAnalysisPayload(content string) (json.RawMessage, error) {
	cleaned := stripJSONCodeFence(content)
	var payload finalAnalysisPayload
	if err := json.Unmarshal([]byte(cleaned), &payload); err != nil {
		return nil, fmt.Errorf("parse final analysis payload: %w", err)
	}
	if payload.isEmpty() {
		return nil, fmt.Errorf("final analysis payload is empty")
	}
	normalized, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal normalized final analysis payload: %w", err)
	}
	return normalized, nil
}

// stripJSONCodeFence removes ```json ... ``` style code fences that models
// sometimes add despite being asked for a bare JSON object, and trims any
// leading/trailing text outside the outermost braces.
func stripJSONCodeFence(content string) string {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
		trimmed = strings.TrimPrefix(trimmed, "```JSON")
		trimmed = strings.TrimPrefix(trimmed, "```")
		if idx := strings.LastIndex(trimmed, "```"); idx >= 0 {
			trimmed = trimmed[:idx]
		}
		trimmed = strings.TrimSpace(trimmed)
	}
	if start := strings.Index(trimmed, "{"); start > 0 {
		trimmed = trimmed[start:]
	}
	if end := strings.LastIndex(trimmed, "}"); end >= 0 && end < len(trimmed)-1 {
		trimmed = trimmed[:end+1]
	}
	return strings.TrimSpace(trimmed)
}
