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

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO meeting_sessions (
			id, workspace_id, created_by_user_id, meeting_id,
			join_url, join_url_hash, title, title_source, title_updated_at, provider,
			organizer_name, organizer_email, scheduled_start_at, scheduled_end_at,
			purpose, context, agenda, decision_points, concerns, expected_output, custom_instruction,
			status, requested_at, joined_at, ended_at, end_reason,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, 'user_input', $8, 'teams',
			'田中 PM', 'tanaka@deciscope.local', $9, $10,
			'来期の価格改定方針を決める',
			'昨年から原価が上昇しており、価格据え置きでは利益率が悪化している。',
			E'1. 値上げ対象顧客の範囲\n2. 値上げ率\n3. 適用タイミング',
			'対象顧客・値上げ率・適用開始時期',
			'中小顧客の解約リスク',
			'値上げ方針の合意と対象顧客リストの作成',
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

	// 議論ツリー (tree.update)。node kind の語彙はフロントの DiscussionTree と一致させる。
	treePayload := `{"version":1,"mode":"snapshot","nodes":[{"id":"n_price_topic","kind":"topic","label":"価格改定方針"},{"id":"n_price_scope","kind":"issue","label":"対象顧客の範囲"},{"id":"n_price_churn","kind":"risk","label":"中小顧客の解約リスク"},{"id":"n_price_timing","kind":"question","label":"更新タイミングのばらつき"},{"id":"n_price_decision","kind":"decision","label":"ENTは更新月から8%・中小は据え置き"}],"edges":[{"id":"e_price_1","source":"n_price_topic","target":"n_price_scope","kind":"decomposes"},{"id":"e_price_2","source":"n_price_scope","target":"n_price_churn","kind":"depends_on"},{"id":"e_price_3","source":"n_price_scope","target":"n_price_timing","kind":"raises"},{"id":"e_price_4","source":"n_price_topic","target":"n_price_decision","kind":"concludes"}]}`
	// 分析カード (analysis.delta)。linked_segment_ids は上で生成した transcript の event_id を参照する。
	analysisPayload := fmt.Sprintf(`{"items":[{"op":"add","item":{"id":"an_price_1","kind":"issue","severity":"high","title":"対象顧客の範囲が未確定","body":"値上げ対象をエンタープライズに限定するか全体にするか結論が必要。","status":"resolved","linked_segment_ids":["%s"]}},{"op":"add","item":{"id":"an_price_2","kind":"risk","severity":"medium","title":"中小顧客の解約リスク","body":"中小顧客への値上げは解約につながる懸念がある。","status":"open","linked_segment_ids":["%s"]}},{"op":"add","item":{"id":"an_price_3","kind":"question","severity":"low","title":"更新タイミングの確認","body":"顧客ごとの契約更新月にばらつきがあり、一斉適用が難しい。","status":"updated","linked_segment_ids":["%s"]}}]}`,
		eventID(2), eventID(2), eventID(5))

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

	return tx.Commit()
}
