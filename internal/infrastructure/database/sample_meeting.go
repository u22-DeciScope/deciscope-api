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
// データ内容は旧デモシード (seed/demo_seed.sql の「価格改定方針の検討会議」) を移植したもの。
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
	// - 対応する分析カード(items)とはノードidを共有する(todoはツリーではissueで表現)
	treePayload := `{"version":1,"mode":"snapshot",` + sampleTreeNodesEdgesJSON + `}`
	// 分析カード (analysis.delta)。ライブ分析v2のitemsと同じ語彙(kind/severity/status)。
	// linked_segment_ids は上で生成した transcript の event_id を参照する。
	analysisPayload := fmt.Sprintf(
		`{"items":[`+
			`{"op":"add","item":{"id":"issue-target-scope","kind":"issue","severity":"high","title":"値上げ対象顧客の範囲","body":"値上げ対象をエンタープライズ顧客に限定するか全体にするかの論点。エンタープライズ限定で合意した。","status":"resolved","linked_segment_ids":["%s"]}},`+
			`{"op":"add","item":{"id":"risk-smb-churn","kind":"risk","severity":"medium","title":"中小顧客の解約リスク","body":"中小顧客への値上げは解約につながる懸念。今回は据え置きとして回避した。","status":"resolved","linked_segment_ids":["%s"]}},`+
			`{"op":"add","item":{"id":"question-renewal-timing","kind":"question","severity":"medium","title":"契約更新タイミングのばらつき","body":"顧客ごとに契約更新月が異なり一斉適用が難しい。更新月にあわせた段階適用で対応する。","status":"resolved","linked_segment_ids":["%s"]}},`+
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

	// AI分析レコード (meeting_session_ai_analyses)。実際の会議終了後と同じく、
	// ライブ分析(live)と最終要約(final)の completed 行を投入する。
	// これが無いと、サマリー画面の最終要約パネルが数分間ポーリングした末に
	// 「AI分析なし」の表示になってしまう。
	inputChars := 0
	for _, segment := range segments {
		inputChars += len([]rune(segment.text))
	}
	for _, analysis := range []struct {
		analysisType string
		payload      string
		version      int
		at           string
	}{
		{"live", sampleLiveAnalysisPayload, 4, at(30 * time.Minute)},
		{"final", sampleFinalAnalysisPayload, 1, at(33 * time.Minute)},
	} {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO meeting_session_ai_analyses (
				session_id, analysis_type, status, version, payload, model, segment_count, input_chars, created_at, updated_at
			) VALUES ($1, $2, 'completed', $3, $4, 'sample', $5, $6, $7, $7)
		`, sessionID, analysis.analysisType, analysis.version, analysis.payload, len(segments), inputChars, analysis.at); err != nil {
			return fmt.Errorf("insert sample ai analysis %s: %w", analysis.analysisType, err)
		}
	}

	return tx.Commit()
}

// sampleTreeNodesEdgesJSON は議論ツリーの nodes/edges 部分。durable イベント
// (tree.update) とライブ分析payloadの tree の両方から同じ内容を参照する。
const sampleTreeNodesEdgesJSON = `"nodes":[` +
	`{"id":"topic-root","kind":"topic","label":"価格改定方針","status":"open","description":"来期の価格改定方針(対象顧客・値上げ率・適用時期)を決める。"},` +
	`{"id":"topic-scope","kind":"topic","label":"対象顧客","status":"open","description":"値上げ対象とする顧客セグメントの検討。"},` +
	`{"id":"topic-rollout","kind":"topic","label":"適用方法・時期","status":"open","description":"値上げの適用タイミングと段階適用の検討。"},` +
	`{"id":"issue-target-scope","kind":"issue","label":"値上げ対象顧客の範囲","status":"resolved","description":"エンタープライズ限定か全顧客か。エンタープライズ限定で合意。","relatedItemIds":["issue-target-scope"]},` +
	`{"id":"risk-smb-churn","kind":"risk","label":"中小顧客の解約リスク","status":"resolved","description":"中小への値上げは解約リスクが高い。据え置きで回避。","relatedItemIds":["risk-smb-churn"]},` +
	`{"id":"decision-ent-repricing","kind":"decision","label":"ENT8%値上げ・中小据え置き","status":"open","description":"エンタープライズは更新月から8%値上げ、中小は据え置きで決定。","relatedItemIds":["decision-ent-repricing"]},` +
	`{"id":"todo-customer-list","kind":"issue","label":"対象顧客リストの展開","status":"open","description":"値上げ対象顧客リストを今週中に作成・共有(担当: 佐藤)。","relatedItemIds":["todo-customer-list"]},` +
	`{"id":"question-renewal-timing","kind":"question","label":"更新タイミングのばらつき","status":"resolved","description":"契約更新月が顧客ごとに異なる。更新月にあわせ段階適用で解決。","relatedItemIds":["question-renewal-timing"]},` +
	`{"id":"risk-revenue-timing","kind":"risk","label":"値上げ効果の発現遅延","status":"open","description":"段階適用のため効果が全顧客に及ぶまで最長1年。","relatedItemIds":["risk-revenue-timing"]}` +
	`],"edges":[` +
	`{"id":"e-root-scope","source":"topic-root","target":"topic-scope"},` +
	`{"id":"e-root-rollout","source":"topic-root","target":"topic-rollout"},` +
	`{"id":"e-scope-issue","source":"topic-scope","target":"issue-target-scope"},` +
	`{"id":"e-issue-churn","source":"issue-target-scope","target":"risk-smb-churn"},` +
	`{"id":"e-issue-decision","source":"issue-target-scope","target":"decision-ent-repricing"},` +
	`{"id":"e-decision-todo","source":"decision-ent-repricing","target":"todo-customer-list"},` +
	`{"id":"e-rollout-question","source":"topic-rollout","target":"question-renewal-timing"},` +
	`{"id":"e-question-delay","source":"question-renewal-timing","target":"risk-revenue-timing"}` +
	`]`

// sampleLiveAnalysisPayload はライブ分析v2形式(summary/currentTopic/items/tree)の
// 最終スナップショット。itemsのidはツリーのノードidと共有する。
const sampleLiveAnalysisPayload = `{` +
	`"summary":"来期の価格改定方針について、値上げ対象顧客の範囲と適用方法を議論した。原価上昇により価格据え置きでは利益率が悪化するため、エンタープライズ顧客は契約更新月から8%値上げし、中小顧客は解約リスクを考慮して据え置きとすることで合意した。対象顧客リストは佐藤が今週中に展開する。",` +
	`"currentTopic":"価格改定の適用方法",` +
	`"items":[` +
	`{"id":"issue-target-scope","kind":"issue","severity":"high","title":"値上げ対象顧客の範囲","body":"値上げ対象をエンタープライズ顧客に限定するか全体にするかの論点。エンタープライズ限定で合意した。","status":"resolved"},` +
	`{"id":"risk-smb-churn","kind":"risk","severity":"medium","title":"中小顧客の解約リスク","body":"中小顧客への値上げは解約につながる懸念。今回は据え置きとして回避した。","status":"resolved"},` +
	`{"id":"question-renewal-timing","kind":"question","severity":"medium","title":"契約更新タイミングのばらつき","body":"顧客ごとに契約更新月が異なり一斉適用が難しい。更新月にあわせた段階適用で対応する。","status":"resolved"},` +
	`{"id":"decision-ent-repricing","kind":"decision","severity":"high","title":"ENTは更新月から8%値上げ・中小は据え置き","body":"エンタープライズ顧客は契約更新月から8%値上げし、中小顧客は当面据え置きとする。","status":"open"},` +
	`{"id":"todo-customer-list","kind":"todo","severity":"medium","title":"対象顧客リストの展開","body":"値上げ対象顧客リストを作成して共有する(担当: 佐藤、今週中)。","status":"open"},` +
	`{"id":"risk-revenue-timing","kind":"risk","severity":"low","title":"値上げ効果の発現遅延","body":"更新月ごとの段階適用のため、値上げ効果が全顧客に行き渡るまで最長1年かかる。","status":"open"}` +
	`],` +
	`"tree":{` + sampleTreeNodesEdgesJSON + `}` +
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
	`]` +
	`}`
