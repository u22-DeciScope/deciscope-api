-- 開発用デモシードデータ。
-- DECISCOPE_SEED_DEMO_DATA が有効なときだけ投入される。すべて冪等（ON CONFLICT DO NOTHING / 明示 UPDATE）。
-- 固定のデモ用ワークスペース（ws_demo_deciscope）に、終了済みの Teams 会議セッションを 3 件、
-- それぞれの文字起こし、および議論ツリー / 分析カードを格納する。
-- セッションは meeting_id で会議(meetings)に紐づき、tree.update / analysis.delta は meeting_events に保存される。
-- ログインしたユーザーはこのワークスペースへ自動参加して閲覧できる。

-- デモ用のオーナーユーザー（実在のログインユーザーではない。FK 用のプレースホルダ）。
INSERT INTO users (id, display_name, status, created_at, updated_at)
VALUES ('user_demo_owner', 'DeciScope デモ', 'active', '2026-06-01T00:00:00Z', '2026-06-01T00:00:00Z')
ON CONFLICT (id) DO NOTHING;

INSERT INTO user_emails (id, user_id, email, normalized_email, verified, is_primary, created_at)
VALUES ('email_demo_owner', 'user_demo_owner', 'demo@deciscope.local', 'demo@deciscope.local', TRUE, TRUE, '2026-06-01T00:00:00Z')
ON CONFLICT (normalized_email) DO NOTHING;

-- デモ用ワークスペース。created_at を早めに設定して一覧の先頭に来るようにする。
INSERT INTO workspaces (id, name, created_by, created_at, updated_at)
VALUES ('ws_demo_deciscope', 'DeciScope デモ会議', 'user_demo_owner', '2026-06-01T00:00:00Z', '2026-06-01T00:00:00Z')
ON CONFLICT (id) DO NOTHING;

INSERT INTO workspace_members (workspace_id, user_id, role, joined_at)
VALUES ('ws_demo_deciscope', 'user_demo_owner', 'owner', '2026-06-01T00:00:00Z')
ON CONFLICT (workspace_id, user_id) DO NOTHING;

-- 会議(meetings)。セッションの分析イベント（tree.update / analysis.delta）の入れ物。
INSERT INTO meetings (id, workspace_id, title, status, source, next_seq, created_at, updated_at, ended_at)
VALUES
('m_demo_pricing', 'ws_demo_deciscope', '価格改定方針の検討会議', 'ended', 'teams', 3, '2026-06-25T04:58:00Z', '2026-06-25T05:32:00Z', '2026-06-25T05:32:00Z'),
('m_demo_roadmap', 'ws_demo_deciscope', '新機能ロードマップ レビュー', 'ended', 'teams', 3, '2026-06-27T01:58:00Z', '2026-06-27T02:48:00Z', '2026-06-27T02:48:00Z'),
('m_demo_hiring', 'ws_demo_deciscope', '採用計画すり合わせ', 'ended', 'teams', 3, '2026-06-29T06:58:00Z', '2026-06-29T07:40:00Z', '2026-06-29T07:40:00Z')
ON CONFLICT (id) DO NOTHING;

-- 会議セッション 3 件（すべて終了済み）。meeting_id で会議に紐づける。
INSERT INTO meeting_sessions (
    id, workspace_id, created_by_user_id, meeting_id,
    join_url, join_url_hash, title, title_source, title_updated_at, provider,
    organizer_name, organizer_email, scheduled_start_at, scheduled_end_at,
    purpose, context, agenda, decision_points, concerns, expected_output, custom_instruction,
    status, requested_at, joined_at, ended_at, end_reason,
    created_at, updated_at
) VALUES
(
    'session_demo_pricing', 'ws_demo_deciscope', 'user_demo_owner', 'm_demo_pricing',
    'https://teams.microsoft.com/l/meetup-join/demo-pricing', 'demo_pricing_hash',
    '価格改定方針の検討会議', 'user_input', '2026-06-25T05:00:00Z', 'teams',
    '田中 PM', 'tanaka@deciscope.local', '2026-06-25T05:00:00Z', '2026-06-25T05:30:00Z',
    '来期の価格改定方針を決める',
    '昨年から原価が上昇しており、価格据え置きでは利益率が悪化している。',
    E'1. 値上げ対象顧客の範囲\n2. 値上げ率\n3. 適用タイミング',
    '対象顧客・値上げ率・適用開始時期',
    '中小顧客の解約リスク',
    '値上げ方針の合意と対象顧客リストの作成',
    '財務影響は数値で示すこと',
    'ended', '2026-06-25T04:58:00Z', '2026-06-25T05:00:00Z', '2026-06-25T05:32:00Z', 'organizer_ended',
    '2026-06-25T04:58:00Z', '2026-06-25T05:32:00Z'
),
(
    'session_demo_roadmap', 'ws_demo_deciscope', 'user_demo_owner', 'm_demo_roadmap',
    'https://teams.microsoft.com/l/meetup-join/demo-roadmap', 'demo_roadmap_hash',
    '新機能ロードマップ レビュー', 'user_input', '2026-06-27T02:00:00Z', 'teams',
    '山本 PdM', 'yamamoto@deciscope.local', '2026-06-27T02:00:00Z', '2026-06-27T03:00:00Z',
    'Q3 のプロダクトロードマップを確定する',
    'リアルタイム分析の精度がまだ目標値に届いていない。',
    E'1. 優先機能の確認\n2. 担当割り当て\n3. マイルストーン設定',
    '第一マイルストーンに据える機能と担当',
    'Azure OpenAI 連携の API コスト',
    'Q3 ロードマップとオーナー一覧',
    '',
    'ended', '2026-06-27T01:58:00Z', '2026-06-27T02:00:00Z', '2026-06-27T02:48:00Z', 'organizer_ended',
    '2026-06-27T01:58:00Z', '2026-06-27T02:48:00Z'
),
(
    'session_demo_hiring', 'ws_demo_deciscope', 'user_demo_owner', 'm_demo_hiring',
    'https://teams.microsoft.com/l/meetup-join/demo-hiring', 'demo_hiring_hash',
    '採用計画すり合わせ', 'user_input', '2026-06-29T07:00:00Z', 'teams',
    '高橋 HR', 'takahashi@deciscope.local', '2026-06-29T07:00:00Z', '2026-06-29T07:45:00Z',
    '下期の採用計画をすり合わせる',
    'エンジニアの増員が事業計画上必須になっている。',
    E'1. 採用人数と職種\n2. 採用チャネル\n3. 選考プロセス',
    '優先して採用する職種と人数',
    '採用基準を下げずに母集団を確保できるか',
    '下期の採用計画と公開する求人票',
    '',
    'ended', '2026-06-29T06:58:00Z', '2026-06-29T07:00:00Z', '2026-06-29T07:40:00Z', 'organizer_ended',
    '2026-06-29T06:58:00Z', '2026-06-29T07:40:00Z'
)
ON CONFLICT (id) DO NOTHING;

-- 既にシード済み（meeting_id 未設定）のセッションも会議へ紐づける。
UPDATE meeting_sessions SET meeting_id = 'm_demo_pricing' WHERE id = 'session_demo_pricing' AND meeting_id IS NULL;
UPDATE meeting_sessions SET meeting_id = 'm_demo_roadmap' WHERE id = 'session_demo_roadmap' AND meeting_id IS NULL;
UPDATE meeting_sessions SET meeting_id = 'm_demo_hiring' WHERE id = 'session_demo_hiring' AND meeting_id IS NULL;

-- 文字起こし（保存済み = final 相当）。offset_ticks / duration_ticks は 1 秒 = 10,000,000 ticks。
INSERT INTO transcript_segments (
    event_id, session_id, call_id, sequence_no, speaker_id, speaker_name,
    recognized_at_utc, offset_ticks, duration_ticks, text, received_at_utc
) VALUES
-- 価格改定方針の検討会議
('evt_demo_pricing_1', 'session_demo_pricing', 'session_demo_pricing', 1, 'spk_tanaka', '田中 PM',
 '2026-06-25T05:00:20Z', 200000000, 80000000, '本日は来期の価格改定方針を決めたいと思います。最初の論点は値上げの対象顧客の範囲です。', '2026-06-25T05:00:21Z'),
('evt_demo_pricing_2', 'session_demo_pricing', 'session_demo_pricing', 2, 'spk_sato', '佐藤 営業',
 '2026-06-25T05:01:05Z', 650000000, 90000000, 'エンタープライズ顧客は値上げの余地がありますが、中小顧客は解約リスクが高いと感じています。', '2026-06-25T05:01:06Z'),
('evt_demo_pricing_3', 'session_demo_pricing', 'session_demo_pricing', 3, 'spk_suzuki', '鈴木 財務',
 '2026-06-25T05:02:10Z', 1300000000, 85000000, '財務的には全体で八パーセントの値上げを想定していますが、段階的な適用でも問題ありません。', '2026-06-25T05:02:11Z'),
('evt_demo_pricing_4', 'session_demo_pricing', 'session_demo_pricing', 4, 'spk_tanaka', '田中 PM',
 '2026-06-25T05:03:30Z', 2100000000, 60000000, 'まず対象をエンタープライズ顧客に限定する案はどうでしょうか。', '2026-06-25T05:03:31Z'),
('evt_demo_pricing_5', 'session_demo_pricing', 'session_demo_pricing', 5, 'spk_sato', '佐藤 営業',
 '2026-06-25T05:04:25Z', 2650000000, 95000000, '既存契約の更新タイミングが顧客ごとにばらつくのが懸念です。一斉適用は難しいかもしれません。', '2026-06-25T05:04:26Z'),
('evt_demo_pricing_6', 'session_demo_pricing', 'session_demo_pricing', 6, 'spk_suzuki', '鈴木 財務',
 '2026-06-25T05:05:40Z', 3400000000, 88000000, '更新月にあわせて段階的に適用すれば、解約は最小化できると思います。', '2026-06-25T05:05:41Z'),
('evt_demo_pricing_7', 'session_demo_pricing', 'session_demo_pricing', 7, 'spk_tanaka', '田中 PM',
 '2026-06-25T05:07:00Z', 4200000000, 100000000, 'ではエンタープライズ向けに更新月から八パーセント、中小顧客は据え置きで決定とします。', '2026-06-25T05:07:01Z'),
('evt_demo_pricing_8', 'session_demo_pricing', 'session_demo_pricing', 8, 'spk_sato', '佐藤 営業',
 '2026-06-25T05:08:05Z', 4850000000, 55000000, '承知しました。対象顧客のリストを今週中に展開します。', '2026-06-25T05:08:06Z'),
-- 新機能ロードマップ レビュー
('evt_demo_roadmap_1', 'session_demo_roadmap', 'session_demo_roadmap', 1, 'spk_yamamoto', '山本 PdM',
 '2026-06-27T02:00:30Z', 300000000, 70000000, 'Q3 のロードマップをレビューします。最優先はリアルタイム分析の精度向上だと考えています。', '2026-06-27T02:00:31Z'),
('evt_demo_roadmap_2', 'session_demo_roadmap', 'session_demo_roadmap', 2, 'spk_nakamura', '中村 エンジニア',
 '2026-06-27T02:01:20Z', 800000000, 90000000, '分析基盤は Azure OpenAI 連携が前提なので、API コストの試算が必要になります。', '2026-06-27T02:01:21Z'),
('evt_demo_roadmap_3', 'session_demo_roadmap', 'session_demo_roadmap', 3, 'spk_kobayashi', '小林 デザイナー',
 '2026-06-27T02:02:15Z', 1350000000, 85000000, 'ノードツリーの UI は概ね固まりましたが、モバイル表示の検証がまだ残っています。', '2026-06-27T02:02:16Z'),
('evt_demo_roadmap_4', 'session_demo_roadmap', 'session_demo_roadmap', 4, 'spk_yamamoto', '山本 PdM',
 '2026-06-27T02:03:30Z', 2100000000, 75000000, 'コスト試算は中村さん、モバイル検証は小林さんでお願いできますか。', '2026-06-27T02:03:31Z'),
('evt_demo_roadmap_5', 'session_demo_roadmap', 'session_demo_roadmap', 5, 'spk_nakamura', '中村 エンジニア',
 '2026-06-27T02:04:20Z', 2600000000, 50000000, '了解です。来週前半に概算を共有します。', '2026-06-27T02:04:21Z'),
('evt_demo_roadmap_6', 'session_demo_roadmap', 'session_demo_roadmap', 6, 'spk_kobayashi', '小林 デザイナー',
 '2026-06-27T02:05:05Z', 3050000000, 55000000, 'モバイルは今週中にプロトタイプを用意します。', '2026-06-27T02:05:06Z'),
('evt_demo_roadmap_7', 'session_demo_roadmap', 'session_demo_roadmap', 7, 'spk_yamamoto', '山本 PdM',
 '2026-06-27T02:06:10Z', 3700000000, 80000000, 'では精度向上を第一マイルストーンとして進めましょう。', '2026-06-27T02:06:11Z'),
-- 採用計画すり合わせ
('evt_demo_hiring_1', 'session_demo_hiring', 'session_demo_hiring', 1, 'spk_takahashi', '高橋 HR',
 '2026-06-29T07:00:25Z', 250000000, 80000000, '下期の採用計画をすり合わせます。バックエンド二名、デザイナー一名を想定しています。', '2026-06-29T07:00:26Z'),
('evt_demo_hiring_2', 'session_demo_hiring', 'session_demo_hiring', 2, 'spk_ito', '伊藤 EM',
 '2026-06-29T07:01:15Z', 750000000, 90000000, 'バックエンドは即戦力が欲しいので、シニア中心で探したいです。', '2026-06-29T07:01:16Z'),
('evt_demo_hiring_3', 'session_demo_hiring', 'session_demo_hiring', 3, 'spk_watanabe', '渡辺 CTO',
 '2026-06-29T07:02:20Z', 1400000000, 88000000, '予算的には三名まで確保できますが、採用基準は下げたくありません。', '2026-06-29T07:02:21Z'),
('evt_demo_hiring_4', 'session_demo_hiring', 'session_demo_hiring', 4, 'spk_takahashi', '高橋 HR',
 '2026-06-29T07:03:30Z', 2100000000, 75000000, 'エージェント経由とリファラルを併用して母集団を広げます。', '2026-06-29T07:03:31Z'),
('evt_demo_hiring_5', 'session_demo_hiring', 'session_demo_hiring', 5, 'spk_ito', '伊藤 EM',
 '2026-06-29T07:04:25Z', 2650000000, 70000000, '技術面接のプロセスは現状の二回で問題ないと思います。', '2026-06-29T07:04:26Z'),
('evt_demo_hiring_6', 'session_demo_hiring', 'session_demo_hiring', 6, 'spk_watanabe', '渡辺 CTO',
 '2026-06-29T07:05:35Z', 3350000000, 78000000, 'では今期はシニアバックエンド二名を最優先で動きましょう。', '2026-06-29T07:05:36Z'),
('evt_demo_hiring_7', 'session_demo_hiring', 'session_demo_hiring', 7, 'spk_takahashi', '高橋 HR',
 '2026-06-29T07:06:30Z', 3900000000, 60000000, '承知しました。求人票を更新して今週公開します。', '2026-06-29T07:06:31Z')
ON CONFLICT (event_id) DO NOTHING;

-- 議論ツリー（tree.update）と分析カード（analysis.delta）。payload は JSON 文字列。
-- node kind の語彙は topic / issue / question / risk / decision（フロントの DiscussionTree と一致）。
INSERT INTO meeting_events (meeting_id, seq, type, ts_ms, payload, created_at)
VALUES
-- 価格改定方針の検討会議
('m_demo_pricing', 1, 'tree.update', (EXTRACT(EPOCH FROM TIMESTAMPTZ '2026-06-25T05:30:00Z') * 1000)::BIGINT,
 '{"version":1,"mode":"snapshot","nodes":[{"id":"n_price_topic","kind":"topic","label":"価格改定方針"},{"id":"n_price_scope","kind":"issue","label":"対象顧客の範囲"},{"id":"n_price_churn","kind":"risk","label":"中小顧客の解約リスク"},{"id":"n_price_timing","kind":"question","label":"更新タイミングのばらつき"},{"id":"n_price_decision","kind":"decision","label":"ENTは更新月から8%・中小は据え置き"}],"edges":[{"id":"e_price_1","source":"n_price_topic","target":"n_price_scope","kind":"decomposes"},{"id":"e_price_2","source":"n_price_scope","target":"n_price_churn","kind":"depends_on"},{"id":"e_price_3","source":"n_price_scope","target":"n_price_timing","kind":"raises"},{"id":"e_price_4","source":"n_price_topic","target":"n_price_decision","kind":"concludes"}]}',
 '2026-06-25T05:30:00Z'),
('m_demo_pricing', 2, 'analysis.delta', (EXTRACT(EPOCH FROM TIMESTAMPTZ '2026-06-25T05:30:05Z') * 1000)::BIGINT,
 '{"items":[{"op":"add","item":{"id":"an_price_1","kind":"issue","severity":"high","title":"対象顧客の範囲が未確定","body":"値上げ対象をエンタープライズに限定するか全体にするか結論が必要。","status":"resolved","linked_segment_ids":["evt_demo_pricing_2"]}},{"op":"add","item":{"id":"an_price_2","kind":"risk","severity":"medium","title":"中小顧客の解約リスク","body":"中小顧客への値上げは解約につながる懸念がある。","status":"open","linked_segment_ids":["evt_demo_pricing_2"]}},{"op":"add","item":{"id":"an_price_3","kind":"question","severity":"low","title":"更新タイミングの確認","body":"顧客ごとの契約更新月にばらつきがあり、一斉適用が難しい。","status":"updated","linked_segment_ids":["evt_demo_pricing_5"]}}]}',
 '2026-06-25T05:30:05Z'),
-- 新機能ロードマップ レビュー
('m_demo_roadmap', 1, 'tree.update', (EXTRACT(EPOCH FROM TIMESTAMPTZ '2026-06-27T02:46:00Z') * 1000)::BIGINT,
 '{"version":1,"mode":"snapshot","nodes":[{"id":"n_road_topic","kind":"topic","label":"Q3ロードマップ"},{"id":"n_road_accuracy","kind":"issue","label":"リアルタイム分析の精度向上"},{"id":"n_road_cost","kind":"risk","label":"Azure OpenAI APIコスト"},{"id":"n_road_mobile","kind":"question","label":"モバイル表示の検証"},{"id":"n_road_decision","kind":"decision","label":"精度向上を第一マイルストーンに"}],"edges":[{"id":"e_road_1","source":"n_road_topic","target":"n_road_accuracy","kind":"decomposes"},{"id":"e_road_2","source":"n_road_accuracy","target":"n_road_cost","kind":"depends_on"},{"id":"e_road_3","source":"n_road_topic","target":"n_road_mobile","kind":"raises"},{"id":"e_road_4","source":"n_road_topic","target":"n_road_decision","kind":"concludes"}]}',
 '2026-06-27T02:46:00Z'),
('m_demo_roadmap', 2, 'analysis.delta', (EXTRACT(EPOCH FROM TIMESTAMPTZ '2026-06-27T02:46:05Z') * 1000)::BIGINT,
 '{"items":[{"op":"add","item":{"id":"an_road_1","kind":"issue","severity":"high","title":"精度向上が最優先","body":"リアルタイム分析の精度向上を第一マイルストーンに据える。","status":"resolved","linked_segment_ids":["evt_demo_roadmap_1"]}},{"op":"add","item":{"id":"an_road_2","kind":"risk","severity":"medium","title":"APIコストの試算が必要","body":"Azure OpenAI 連携のコストが未試算で、予算超過のリスクがある。","status":"open","linked_segment_ids":["evt_demo_roadmap_2"]}},{"op":"add","item":{"id":"an_road_3","kind":"question","severity":"low","title":"モバイル表示の検証","body":"ノードツリーのモバイル表示検証が未完了。","status":"open","linked_segment_ids":["evt_demo_roadmap_3"]}}]}',
 '2026-06-27T02:46:05Z'),
-- 採用計画すり合わせ
('m_demo_hiring', 1, 'tree.update', (EXTRACT(EPOCH FROM TIMESTAMPTZ '2026-06-29T07:38:00Z') * 1000)::BIGINT,
 '{"version":1,"mode":"snapshot","nodes":[{"id":"n_hire_topic","kind":"topic","label":"下期採用計画"},{"id":"n_hire_roles","kind":"issue","label":"BE2名・デザイナー1名"},{"id":"n_hire_quality","kind":"risk","label":"基準を下げずに母集団確保"},{"id":"n_hire_process","kind":"question","label":"選考は現状2回でよいか"},{"id":"n_hire_decision","kind":"decision","label":"シニアBE2名を最優先"}],"edges":[{"id":"e_hire_1","source":"n_hire_topic","target":"n_hire_roles","kind":"decomposes"},{"id":"e_hire_2","source":"n_hire_roles","target":"n_hire_quality","kind":"depends_on"},{"id":"e_hire_3","source":"n_hire_topic","target":"n_hire_process","kind":"raises"},{"id":"e_hire_4","source":"n_hire_topic","target":"n_hire_decision","kind":"concludes"}]}',
 '2026-06-29T07:38:00Z'),
('m_demo_hiring', 2, 'analysis.delta', (EXTRACT(EPOCH FROM TIMESTAMPTZ '2026-06-29T07:38:05Z') * 1000)::BIGINT,
 '{"items":[{"op":"add","item":{"id":"an_hire_1","kind":"issue","severity":"medium","title":"採用人数と職種の確定","body":"バックエンド2名・デザイナー1名の想定で合意。","status":"resolved","linked_segment_ids":["evt_demo_hiring_1"]}},{"op":"add","item":{"id":"an_hire_2","kind":"risk","severity":"medium","title":"基準と母集団のトレードオフ","body":"採用基準を下げずに母集団を確保できるかが懸念。","status":"open","linked_segment_ids":["evt_demo_hiring_3"]}},{"op":"add","item":{"id":"an_hire_3","kind":"question","severity":"low","title":"選考プロセスの妥当性","body":"技術面接が現状2回で十分かを確認。","status":"updated","linked_segment_ids":["evt_demo_hiring_5"]}}]}',
 '2026-06-29T07:38:05Z')
ON CONFLICT (meeting_id, seq) DO NOTHING;
