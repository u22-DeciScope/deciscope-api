package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"deciscope-core-api/internal/domain"
)

// SampleMeetingSeeder は、初回作成されたワークスペースへ初期サンプル会議
// (終了済みのTeams会議セッション + 文字起こし + 議論ツリー / 分析カード) を投入する。
// application/workspace の SampleMeetingCreator port の実装。
//
// データ内容は「価格改定方針の検討会議」のサンプルシナリオを使用する。
// 固定の workspace_id (ws_demo_deciscope) は使わず、渡された workspace_id / user_id に紐づける。
type SampleMeetingSeeder struct {
	db *sql.DB
}

func NewSampleMeetingSeeder(db *sql.DB) *SampleMeetingSeeder {
	return &SampleMeetingSeeder{db: db}
}

type sampleTranscriptSegment struct {
	sequenceNo    int
	speakerID     string
	speakerName   string
	recognizedAt  string
	offsetTicks   int64
	durationTicks int64
	text          string
	receivedAt    string
}

const sampleMeetingTitle = "【サンプル】価格改定方針の検討会議"

// CreateSampleMeeting は指定ワークスペースにサンプル会議一式を1件作成する。
// IDはすべて呼び出しごとに生成し、他ワークスペースと衝突しない。
func (s *SampleMeetingSeeder) CreateSampleMeeting(ctx context.Context, workspaceID, createdByUserID string) error {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(createdByUserID) == "" {
		return fmt.Errorf("workspace id and user id are required")
	}
	suffix := domain.NewUUID()
	meetingID := "m_sample_" + suffix
	// フロントエンドは "session_" prefix で会議セッションIDを判定するため、prefix を維持する。
	sessionID := "session_sample_" + suffix
	eventID := func(n int) string { return fmt.Sprintf("evt_sample_%s_%d", suffix, n) }

	// 過去の終了済み会議として、直近の時刻を基準に相対配置する。
	base := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Minute)
	at := func(offset time.Duration) string { return base.Add(offset).Format(time.RFC3339) }

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO meetings (id, workspace_id, title, status, source, next_seq, created_at, updated_at, ended_at)
		VALUES ($1, $2, $3, 'ended', 'teams', 3, $4, $5, $5)
	`, meetingID, workspaceID, sampleMeetingTitle, at(-2*time.Minute), at(32*time.Minute)); err != nil {
		return fmt.Errorf("insert sample meeting: %w", err)
	}

	// 事前情報は入室フォームの現行構成(目的・ゴール / 前提・背景 / アジェンダ / AIへの補足指示)
	// に合わせる。旧フィールド(decision_points / concerns / expected_output)は現行フォームでは
	// 入力されないため空にする。graph_title は「ユーザー入力タイトルを優先しつつTeams側の
	// 会議名を別途保持する」仕様のサンプルとして、title と異なる名前を入れる。
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO meeting_sessions (
			id, workspace_id, created_by_user_id, meeting_id,
			join_url, join_url_hash, title, title_source, title_updated_at,
			user_provided_title, graph_title, provider,
			organizer_name, organizer_email, scheduled_start_at, scheduled_end_at,
			purpose, context, agenda, decision_points, concerns, expected_output, custom_instruction,
			status, requested_at, joined_at, ended_at, end_reason,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, 'user_input', $8,
			$7, '価格改定検討MTG', 'teams',
			'田中 PM', 'tanaka@deciscope.local', $9, $10,
			'来期の価格改定方針を決める。値上げの対象顧客・値上げ率・適用開始時期を決定し、対象顧客リストの作成につなげる。',
			'昨年から原価が上昇しており、価格据え置きでは利益率が悪化している。中小顧客は解約リスクが高い点が懸念。',
			E'1. 値上げ対象顧客の範囲\n2. 値上げ率\n3. 適用タイミング',
			'', '', '',
			'財務影響は数値で示すこと',
			'ended', $11, $9, $12, 'organizer_ended',
			$11, $12
		)
	`, sessionID, workspaceID, createdByUserID, meetingID,
		"https://teams.microsoft.com/l/meetup-join/sample-"+suffix, "sample_hash_"+suffix,
		sampleMeetingTitle, at(0), at(0), at(30*time.Minute), at(-2*time.Minute), at(32*time.Minute)); err != nil {
		return fmt.Errorf("insert sample meeting session: %w", err)
	}

	segments := []sampleTranscriptSegment{
		{1, "spk_tanaka", "田中 PM", at(20 * time.Second), 200000000, 80000000, "本日は来期の価格改定方針を決めたいと思います。最初の論点は値上げの対象顧客の範囲です。", at(21 * time.Second)},
		{2, "spk_sato", "佐藤 営業", at(65 * time.Second), 650000000, 90000000, "エンタープライズ顧客は値上げの余地がありますが、中小顧客は解約リスクが高いと感じています。", at(66 * time.Second)},
		{3, "spk_suzuki", "鈴木 財務", at(130 * time.Second), 1300000000, 85000000, "財務的には全体で八パーセントの値上げを想定していますが、段階的な適用でも問題ありません。", at(131 * time.Second)},
		{4, "spk_tanaka", "田中 PM", at(210 * time.Second), 2100000000, 60000000, "まず対象をエンタープライズ顧客に限定する案はどうでしょうか。", at(211 * time.Second)},
		{5, "spk_sato", "佐藤 営業", at(265 * time.Second), 2650000000, 95000000, "既存契約の更新タイミングが顧客ごとにばらつくのが懸念です。一斉適用は難しいかもしれません。", at(266 * time.Second)},
		{6, "spk_suzuki", "鈴木 財務", at(340 * time.Second), 3400000000, 88000000, "更新月にあわせて段階的に適用すれば、解約は最小化できると思います。", at(341 * time.Second)},
		{7, "spk_tanaka", "田中 PM", at(420 * time.Second), 4200000000, 100000000, "ではエンタープライズ向けに更新月から八パーセント、中小顧客は据え置きで決定とします。", at(421 * time.Second)},
		{8, "spk_sato", "佐藤 営業", at(485 * time.Second), 4850000000, 55000000, "承知しました。対象顧客のリストを今週中に展開します。", at(486 * time.Second)},
	}
	for _, segment := range segments {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO transcript_segments (
				event_id, session_id, call_id, sequence_no, speaker_id, speaker_name,
				recognized_at_utc, offset_ticks, duration_ticks, text, received_at_utc
			) VALUES ($1, $2, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, eventID(segment.sequenceNo), sessionID, segment.sequenceNo, segment.speakerID, segment.speakerName,
			segment.recognizedAt, segment.offsetTicks, segment.durationTicks, segment.text, segment.receivedAt); err != nil {
			return fmt.Errorf("insert sample transcript segment %d: %w", segment.sequenceNo, err)
		}
	}

	// 議論ツリー (tree.update)。ライブ分析v2と同じ最新形式に合わせる:
	// - 階層構造(root topic → 大分類topic → 個別ノード)
	// - 各ノードに status / description / relatedItemIds を持たせる
	// - 対応する分析カード(items)とはノードid・kind・subtypeを共有する
	treePayload := `{"version":1,"mode":"snapshot",` + sampleTreeNodesEdgesJSON + `}`
	// 分析カード (analysis.delta)。ライブ分析v2のitemsと同じ語彙(kind/severity/status)。
	// linked_segment_ids は上で生成した transcript の event_id を参照する。
	analysisPayload := fmt.Sprintf(
		`{"items":[`+
			`{"op":"add","item":{"id":"issue-target-scope","kind":"issue","severity":"high","title":"値上げ対象顧客の範囲","body":"値上げ対象をエンタープライズ顧客に限定するか全体にするかの論点。エンタープライズ限定で合意した。","status":"resolved","linked_segment_ids":["%s"]}},`+
			`{"op":"add","item":{"id":"risk-smb-churn","kind":"risk","severity":"medium","title":"中小顧客の解約リスク","body":"中小顧客への値上げは解約につながる懸念。今回は据え置きとして回避した。","status":"resolved","linked_segment_ids":["%s"]}},`+
			`{"op":"add","item":{"id":"question-renewal-timing","kind":"issue","subtype":"question","severity":"medium","title":"契約更新タイミングのばらつき","body":"顧客ごとに契約更新月が異なり一斉適用が難しい。更新月にあわせた段階適用で対応する。","status":"resolved","linked_segment_ids":["%s"]}},`+
			`{"op":"add","item":{"id":"decision-ent-repricing","kind":"decision","severity":"high","title":"ENTは更新月から8%%値上げ・中小は据え置き","body":"エンタープライズ顧客は契約更新月から8%%値上げし、中小顧客は当面据え置きとする。","status":"open","linked_segment_ids":["%s"]}},`+
			`{"op":"add","item":{"id":"todo-customer-list","kind":"todo","severity":"medium","title":"対象顧客リストの展開","body":"値上げ対象顧客リストを作成して共有する(担当: 佐藤、今週中)。","status":"open","linked_segment_ids":["%s"]}},`+
			`{"op":"add","item":{"id":"risk-revenue-timing","kind":"risk","severity":"low","title":"値上げ効果の発現遅延","body":"更新月ごとの段階適用のため、値上げ効果が全顧客に行き渡るまで最長1年かかる。","status":"open","linked_segment_ids":["%s"]}}`+
			`]}`,
		eventID(1), eventID(2), eventID(5), eventID(7), eventID(8), eventID(6))

	treeAt := base.Add(30 * time.Minute)
	for i, event := range []struct {
		eventType string
		payload   string
		at        time.Time
	}{
		{"tree.update", treePayload, treeAt},
		{"analysis.delta", analysisPayload, treeAt.Add(5 * time.Second)},
	} {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO meeting_events (meeting_id, seq, type, ts_ms, payload, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, meetingID, i+1, event.eventType, event.at.UnixMilli(), event.payload, event.at.Format(time.RFC3339)); err != nil {
			return fmt.Errorf("insert sample meeting event %s: %w", event.eventType, err)
		}
	}

	// AI分析レコード (meeting_session_ai_analyses)。現行の会議終了フローと同じく、
	// 構造化会議コンテキスト(context)、ライブ分析(live)、確定ツリー(tree)、
	// 最終要約(final)、終了処理状態(finalization)を一式投入する。
	// historyにもライブ版を残し、サマリー画面の「カードの更新」を表示できるようにする。
	inputChars := 0
	for _, segment := range segments {
		inputChars += len([]rune(segment.text))
	}
	finalizedAt := at(33 * time.Minute)
	for _, analysis := range []struct {
		analysisType string
		payload      string
		version      int
		at           string
		segmentCount int
		inputChars   int
	}{
		{"context", sampleMeetingContextPayload, 1, at(0), 0, 0},
		{"live", sampleLiveAnalysisPayload, 8, at(30 * time.Minute), len(segments), inputChars},
		{"tree", sampleTreeSnapshotPayload(finalizedAt), 8, finalizedAt, len(segments), inputChars},
		{"final", sampleFinalAnalysisPayload, 1, finalizedAt, len(segments), inputChars},
		{"finalization", sampleFinalizationPayload(finalizedAt), 6, finalizedAt, 0, 0},
	} {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO meeting_session_ai_analyses (
				session_id, analysis_type, status, version, payload, model, segment_count, input_chars, created_at, updated_at
			) VALUES ($1, $2, 'completed', $3, $4, 'sample', $5, $6, $7, $7)
		`, sessionID, analysis.analysisType, analysis.version, analysis.payload, analysis.segmentCount, analysis.inputChars, analysis.at); err != nil {
			return fmt.Errorf("insert sample ai analysis %s: %w", analysis.analysisType, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO meeting_session_ai_analysis_live_history (
			session_id, version, payload, model, updated_at
		) VALUES ($1, 8, $2, 'sample', $3)
	`, sessionID, sampleLiveAnalysisPayload, at(30*time.Minute)); err != nil {
		return fmt.Errorf("insert sample live analysis history: %w", err)
	}

	return tx.Commit()
}

// sampleMeetingContextPayload は会議開始時に正規化・保存される現行の
// structured meeting context。agenda ID はツリーノードIDとは分離する。
const sampleMeetingContextPayload = `{` +
	`"title":"【サンプル】価格改定方針の検討会議",` +
	`"purpose":"来期の価格改定方針を決める。値上げの対象顧客・値上げ率・適用開始時期を決定し、対象顧客リストの作成につなげる。",` +
	`"background":"昨年から原価が上昇しており、価格据え置きでは利益率が悪化している。中小顧客は解約リスクが高い点が懸念。",` +
	`"agendaItems":[` +
	`{"id":"agenda-1","title":"値上げ対象顧客の範囲","order":1,"role":"primary"},` +
	`{"id":"agenda-2","title":"値上げ率","order":2,"role":"primary"},` +
	`{"id":"agenda-3","title":"適用タイミング","order":3,"role":"primary"}` +
	`],` +
	`"aiDirectives":["財務影響は数値で示すこと"]` +
	`}`

// sampleTreeNodesEdgesJSON は現行canonical treeの nodes/edges/relations 部分。
// 親子関係は parentId を正とし、detail同士を親子にせず root → topic → detail
// に揃える。agendaRefs と materializedTopicIds は双方向に対応する。
const sampleTreeNodesEdgesJSON = `"nodes":[` +
	`{"id":"root","kind":"topic","label":"【サンプル】価格改定方針の検討会議","status":"open","description":"来期の価格改定方針を決め、対象顧客リストの作成につなげる。","origin":"system"},` +
	`{"id":"topic-price-scope","kind":"topic","parentId":"root","label":"値上げ対象顧客の範囲","status":"resolved","description":"値上げ対象とする顧客セグメントの検討。","origin":"agenda","agendaRefs":["agenda-1"],"materialized":true},` +
	`{"id":"topic-price-rate","kind":"topic","parentId":"root","label":"値上げ率","status":"resolved","description":"原価上昇を踏まえた値上げ率の検討。","origin":"agenda","agendaRefs":["agenda-2"],"materialized":true},` +
	`{"id":"topic-price-rollout","kind":"topic","parentId":"root","label":"適用タイミング","status":"resolved","description":"契約更新月にあわせた段階適用の検討。","origin":"agenda","agendaRefs":["agenda-3"],"materialized":true},` +
	`{"id":"issue-target-scope","kind":"issue","subtype":"discussion","parentId":"topic-price-scope","label":"値上げ対象顧客の範囲","status":"resolved","description":"エンタープライズ限定か全顧客かを検討し、エンタープライズ限定で合意した。","relatedItemIds":["issue-target-scope"]},` +
	`{"id":"risk-smb-churn","kind":"risk","parentId":"topic-price-scope","label":"中小顧客の解約リスク","status":"resolved","description":"中小顧客への値上げは解約リスクが高く、据え置きで回避する。","relatedItemIds":["risk-smb-churn"]},` +
	`{"id":"decision-ent-repricing","kind":"decision","parentId":"topic-price-rate","label":"ENTは8%値上げ・中小は据え置き","status":"open","description":"エンタープライズは更新月から8%値上げし、中小は据え置く。","relatedItemIds":["decision-ent-repricing"]},` +
	`{"id":"question-renewal-timing","kind":"issue","subtype":"question","parentId":"topic-price-rollout","label":"更新タイミングのばらつき","status":"resolved","description":"顧客ごとに異なる契約更新月にあわせ、段階適用する。","relatedItemIds":["question-renewal-timing"]},` +
	`{"id":"risk-revenue-timing","kind":"risk","parentId":"topic-price-rollout","label":"値上げ効果の発現遅延","status":"open","description":"段階適用のため効果が全顧客に及ぶまで最長1年かかる。","relatedItemIds":["risk-revenue-timing"]},` +
	`{"id":"todo-customer-list","kind":"todo","parentId":"topic-price-rollout","label":"対象顧客リストの展開","status":"open","description":"佐藤が値上げ対象顧客リストを今週中に作成・共有する。","relatedItemIds":["todo-customer-list"]}` +
	`],"edges":[` +
	`{"source":"root","target":"topic-price-scope"},` +
	`{"source":"root","target":"topic-price-rate"},` +
	`{"source":"root","target":"topic-price-rollout"},` +
	`{"source":"topic-price-scope","target":"issue-target-scope"},` +
	`{"source":"topic-price-scope","target":"risk-smb-churn"},` +
	`{"source":"topic-price-rate","target":"decision-ent-repricing"},` +
	`{"source":"topic-price-rollout","target":"question-renewal-timing"},` +
	`{"source":"topic-price-rollout","target":"risk-revenue-timing"},` +
	`{"source":"topic-price-rollout","target":"todo-customer-list"}` +
	`],"relations":[` +
	`{"id":"relation-decision-resolves-scope","source":"decision-ent-repricing","target":"issue-target-scope","kind":"resolves","confidence":1,"evidenceSequenceNos":[7],"origin":"sample","status":"active"},` +
	`{"id":"relation-todo-for-rollout","source":"todo-customer-list","target":"question-renewal-timing","kind":"action_for","confidence":1,"evidenceSequenceNos":[8],"origin":"sample","status":"active"}` +
	`]`

const sampleAgendaAnchorsJSON = `[` +
	`{"agendaId":"agenda-1","originalTitle":"値上げ対象顧客の範囲","normalizedSubject":"値上げ対象顧客の範囲","order":1,"role":"primary","status":"discussed","materializedTopicIds":["topic-price-scope"]},` +
	`{"agendaId":"agenda-2","originalTitle":"値上げ率","normalizedSubject":"値上げ率","order":2,"role":"primary","status":"discussed","materializedTopicIds":["topic-price-rate"]},` +
	`{"agendaId":"agenda-3","originalTitle":"適用タイミング","normalizedSubject":"適用タイミング","order":3,"role":"primary","status":"discussed","materializedTopicIds":["topic-price-rollout"]}` +
	`]`

const sampleAgendaProgressJSON = `{` +
	`"entries":[` +
	`{"id":"agenda-1","sourceType":"fixed_agenda","title":"値上げ対象顧客の範囲","order":1,"computedStatus":"discussed","outcomeStatus":"concluded","focusNodeIds":["topic-price-scope","issue-target-scope","risk-smb-churn"],"materializedTopicIds":["topic-price-scope"],"primaryNodeId":"topic-price-scope","linkState":"materialized-topic","lastProgressAtVersion":8},` +
	`{"id":"agenda-2","sourceType":"fixed_agenda","title":"値上げ率","order":2,"computedStatus":"discussed","outcomeStatus":"concluded","focusNodeIds":["topic-price-rate","decision-ent-repricing"],"materializedTopicIds":["topic-price-rate"],"primaryNodeId":"topic-price-rate","linkState":"materialized-topic","lastProgressAtVersion":8},` +
	`{"id":"agenda-3","sourceType":"fixed_agenda","title":"適用タイミング","order":3,"computedStatus":"discussed","outcomeStatus":"concluded","focusNodeIds":["topic-price-rollout","question-renewal-timing","risk-revenue-timing","todo-customer-list"],"materializedTopicIds":["topic-price-rollout"],"primaryNodeId":"topic-price-rollout","linkState":"materialized-topic","lastProgressAtVersion":8}` +
	`],"updatedAtVersion":8` +
	`}`

// sampleLiveAnalysisPayload は終了時に同期された現行のfull snapshot。
// itemsには分類・根拠・agenda関連・投影状態を含める。
const sampleLiveAnalysisPayload = `{` +
	`"summary":"来期の価格改定方針について、値上げ対象顧客の範囲・値上げ率・適用タイミングを議論した。エンタープライズ顧客は契約更新月から8%値上げし、中小顧客は解約リスクを考慮して据え置くことで合意した。対象顧客リストは佐藤が今週中に展開する。",` +
	`"currentTopic":"会議終了",` +
	`"items":[` +
	`{"id":"issue-target-scope","kind":"issue","subtype":"discussion","severity":"high","title":"値上げ対象顧客の範囲","body":"値上げ対象をエンタープライズ顧客に限定するか全体にするかを検討し、エンタープライズ限定で合意した。","status":"resolved","projectionStatus":"stable","classificationStatus":"assigned","assignmentSource":"rule","evidenceSequenceNos":[1,2,4,7],"relatedAgendaIds":["agenda-1"]},` +
	`{"id":"risk-smb-churn","kind":"risk","severity":"medium","title":"中小顧客の解約リスク","body":"中小顧客への値上げは解約につながる懸念があり、今回は据え置きとして回避した。","status":"resolved","projectionStatus":"stable","classificationStatus":"assigned","assignmentSource":"rule","evidenceSequenceNos":[2,7],"relatedAgendaIds":["agenda-1"]},` +
	`{"id":"decision-ent-repricing","kind":"decision","severity":"high","title":"ENTは更新月から8%値上げ・中小は据え置き","body":"エンタープライズ顧客は契約更新月から8%値上げし、中小顧客は当面据え置きとする。","status":"open","projectionStatus":"stable","classificationStatus":"assigned","assignmentSource":"rule","evidenceSequenceNos":[3,7],"relatedAgendaIds":["agenda-1","agenda-2","agenda-3"]},` +
	`{"id":"question-renewal-timing","kind":"issue","subtype":"question","severity":"medium","title":"契約更新タイミングのばらつき","body":"顧客ごとに契約更新月が異なり一斉適用が難しいため、更新月にあわせて段階適用する。","status":"resolved","projectionStatus":"stable","classificationStatus":"assigned","assignmentSource":"rule","evidenceSequenceNos":[5,6],"relatedAgendaIds":["agenda-3"]},` +
	`{"id":"risk-revenue-timing","kind":"risk","severity":"low","title":"値上げ効果の発現遅延","body":"更新月ごとの段階適用のため、値上げ効果が全顧客に行き渡るまで最長1年かかる。","status":"open","projectionStatus":"stable","classificationStatus":"assigned","assignmentSource":"rule","evidenceSequenceNos":[6],"relatedAgendaIds":["agenda-3"]},` +
	`{"id":"todo-customer-list","kind":"todo","severity":"medium","title":"対象顧客リストの展開","body":"佐藤が値上げ対象顧客リストを今週中に作成して共有する。","status":"open","projectionStatus":"stable","classificationStatus":"assigned","assignmentSource":"rule","evidenceSequenceNos":[8],"relatedAgendaIds":["agenda-3"]}` +
	`],` +
	`"tree":{` + sampleTreeNodesEdgesJSON + `},` +
	`"agendaAnchors":` + sampleAgendaAnchorsJSON + `,` +
	`"agendaProgress":` + sampleAgendaProgressJSON + `,` +
	`"treeVersion":8,"analysisVersion":8,"aiAssistantAnalysisVersion":8,"treeAnalysisVersion":8,` +
	`"highestAvailableFinalSequenceNo":8,"itemProjectionVersion":8,"treeProjectionVersion":8,` +
	`"itemProjectionCompleted":true,"treeProjectionCompleted":true,"treeProjectionDisposition":"updated",` +
	`"payloadKind":"full_snapshot","nodeCount":10,"edgeCount":9,"coveredThroughSequenceNo":8,` +
	`"meaningfullyCoveredThroughSequenceNo":8,"pendingTreeProjectionItemCount":0` +
	`}`

// sampleFinalAnalysisPayload は最終要約(final)のpayload。
// FinalSummaryPayload(suggestedTitle/overview/decisions/actionItems/openIssues/keyPoints/nextMeetingTopics)に合わせる。
const sampleFinalAnalysisPayload = `{` +
	`"suggestedTitle":"来期価格改定方針の決定",` +
	`"overview":"来期の価格改定方針を決める会議。原価上昇により価格据え置きでは利益率が悪化している前提を共有し、値上げ対象顧客の範囲と適用方法を議論した。中小顧客は解約リスクが高いため対象から外し、エンタープライズ顧客に限定して契約更新月から8%の値上げを段階適用することで合意した。値上げ対象顧客リストは佐藤が今週中に作成・展開する。",` +
	`"decisions":[` +
	`{"text":"エンタープライズ顧客は契約更新月から8%値上げする","importance":"high"},` +
	`{"text":"中小顧客の価格は当面据え置く","importance":"medium"},` +
	`{"text":"値上げは一斉適用ではなく契約更新月にあわせて段階適用する","importance":"medium"}` +
	`],` +
	`"actionItems":[` +
	`{"text":"値上げ対象顧客リストの作成と展開","owner":"佐藤 営業","due":"今週中","priority":"high"}` +
	`],` +
	`"openIssues":[` +
	`"段階適用のため値上げ効果が全顧客に行き渡るまで最長1年かかる"` +
	`],` +
	`"keyPoints":[` +
	`"原価上昇により価格据え置きでは利益率が悪化している",` +
	`"中小顧客は解約リスクが高く値上げ対象から除外した",` +
	`"契約更新月にあわせた段階適用で解約リスクを最小化する"` +
	`],` +
	`"nextMeetingTopics":[` +
	`"中小顧客向け価格の再検討時期",` +
	`"値上げの顧客向けアナウンス方法"` +
	`],` +
	`"coveredThroughSequenceNo":8,"segmentCount":8,"treeVersion":8,"final":true` +
	`}`

func sampleTreeSnapshotPayload(generatedAt string) string {
	return `{` +
		`"treeVersion":8,"reason":"meeting_ended","final":true,` +
		`"coveredThroughSequenceNo":8,"segmentCount":8,` +
		`"generatedAtUtc":"` + generatedAt + `","reorganizationStatus":"not_needed",` +
		`"tree":{` + sampleTreeNodesEdgesJSON + `},` +
		`"agendaAnchors":` + sampleAgendaAnchorsJSON + `,` +
		`"agendaProgress":` + sampleAgendaProgressJSON +
		`}`
}

func sampleFinalizationPayload(finalizedAt string) string {
	return `{` +
		`"finalizationId":"finalization_sample","stage":"completed",` +
		`"latestPersistedFinalSequence":8,"lastSuccessfullyAnalyzedSequence":8,` +
		`"finalizationTargetSequence":8,"pendingSegmentCount":0,` +
		`"treeCoveredThroughSequenceNo":8,"summaryCoveredThroughSequenceNo":8,` +
		`"waitTimedOut":false,"finalizationIncomplete":false,"retryCount":0,` +
		`"finalizationStatus":"completed","finalizationStartedAt":"` + finalizedAt + `",` +
		`"finalizationUpdatedAt":"` + finalizedAt + `","finalizationCompletedAt":"` + finalizedAt + `",` +
		`"retryable":false,"attemptCount":1,"sourceTreeVersion":8,` +
		`"sourceAnalysisVersion":8,"summaryVersion":1` +
		`}`
}
