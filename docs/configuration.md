# Configuration

`deciscope-core-api` が読み取る環境変数の一覧です。ローカル実行では `.env`、
続いて `.env.local` を読み込み、後者を優先します。Docker Compose用の設定例は
[.env.example](../.env.example) を参照してください。

## Runtime, database, authentication

| 変数 | 既定値 | 用途 |
| --- | --- | --- |
| `PORT` | `9090` | ローカル実行・コンテナ内のlisten port |
| `DECISCOPE_BACKEND_ADDR` | 未設定 | listen address全体。設定時は`PORT`より優先 |
| `DATABASE_URL` | なし（必須） | PostgreSQL接続URL |
| `DECISCOPE_INGEST_API_KEY` | なし（必須） | VM Bot/互換API用の32文字以上の共有API key |
| `DECISCOPE_TRANSCRIPT_ONLY` | `false` | 文字起こし取り込みだけを起動 |
| `DECISCOPE_WS_ALLOWED_ORIGINS` | localhost/127.0.0.1の3000, 5173, 5193 | transcript WebSocketの許可Origin（カンマ区切り） |
| `FRONTEND_URL` | `http://localhost:5193` | 招待URLと既定CORS Origin |
| `ALLOWED_ORIGINS` | `FRONTEND_URL` | HTTP CORSの追加許可Origin（カンマ区切り） |
| `SESSION_COOKIE_SECURE` | `false` | Session Cookieへ`Secure`を付与 |
| `DECISCOPE_ENV` | `development` | `development`または`production` |
| `DECISCOPE_CREATE_SAMPLE_MEETING_ON_FIRST_WORKSPACE` | development=`true`, production=`false` | 初回workspaceへのサンプル会議投入 |
| `AUTH_PROVIDER` | 未設定 | `firebase`でFirebase認証を有効化 |
| `FIREBASE_PROJECT_ID` | 未設定 | Firebase project ID |
| `GOOGLE_APPLICATION_CREDENTIALS` | 未設定 | Firebase service account JSONのパス |
| `FIREBASE_CREDENTIALS_JSON` | 未設定 | Firebase credential JSON本体 |

共有トークンを使うブラウザWebSocket経路は削除済みです。
`DECISCOPE_WS_CLIENT_TOKEN` は読み取りません。

## Bot control

| 変数 | 既定値 | 用途 |
| --- | --- | --- |
| `DECISCOPE_BOT_CONTROL_URL` | 未設定 | Bot参加API（通常`/internal/bot/join`） |
| `DECISCOPE_BOT_CONTROL_TOKEN` | 未設定 | Bot制御APIの共有token |
| `DECISCOPE_BOT_CONTROL_TIMEOUT_SECONDS` | `10` | 参加・終了命令のtimeout |

終了命令は設定URL末尾の`/join`を除いたベースへ
`/meeting-sessions/{sessionId}/end`を追加したURLへ送ります。

## Session and transcript watchdog

| 変数 | 既定値 | 用途 |
| --- | --- | --- |
| `DECISCOPE_SESSION_WATCHDOG_ENABLED` | `true` | watchdogの有効化 |
| `DECISCOPE_SESSION_WATCHDOG_INTERVAL_SECONDS` | `15`（最小`5`） | 検査間隔 |
| `DECISCOPE_SESSION_BOT_LOST_AFTER_SECONDS` | `60`（最小`30`） | Bot unhealthy判定 |
| `DECISCOPE_SESSION_BOT_END_AFTER_SECONDS` | `180` | Bot無応答による自動終了 |
| `DECISCOPE_TRANSCRIPT_DELAYED_AFTER_SECONDS` | `30`（最小`5`） | transcript遅延判定 |
| `DECISCOPE_TRANSCRIPT_STALLED_AFTER_SECONDS` | `60` | transcript停止判定 |
| `DECISCOPE_AUDIO_SILENCE_AFTER_SECONDS` | `30`（最小`5`） | non-zero音声がない場合のsilent判定 |
| `DECISCOPE_AUDIO_STALLED_AFTER_SECONDS` | `60`（最小`5`） | 音声フレーム停止判定 |
| `DECISCOPE_SPEECH_STALLED_AFTER_SECONDS` | `60`（最小`5`） | 音声あり・文字起こしなしの停止判定 |

`BOT_END_AFTER` は `BOT_LOST_AFTER` より大きく、
`TRANSCRIPT_STALLED_AFTER` は `TRANSCRIPT_DELAYED_AFTER` より大きくなるよう
不正な値を自動補正します。

## Client diagnostics

| 変数 | 既定値 | 用途 |
| --- | --- | --- |
| `DECISCOPE_CLIENT_DIAGNOSTICS_ENABLED` | `true` | 診断受信APIの有効化 |
| `DECISCOPE_CLIENT_DIAGNOSTICS_DIR` | `logs/client-diagnostics` | `{sessionId}.jsonl`の保存先 |
| `DECISCOPE_CLIENT_DIAGNOSTICS_MAX_FILE_MB` | `10` | 1ファイルのローテーション閾値 |
| `DECISCOPE_CLIENT_DIAGNOSTICS_RETENTION_DAYS` | `7` | 保存日数 |
| `DECISCOPE_CLIENT_DIAGNOSTICS_MAX_EVENTS_PER_REQUEST` | `100` | 1リクエストのイベント上限 |
| `DECISCOPE_CLIENT_DIAGNOSTICS_THROTTLE_MS` | `1000` | 同一高頻度イベントの抑制窓。`0`で無効 |

## Azure OpenAI and meeting analysis

`AZURE_OPENAI_ENDPOINT`、`AZURE_OPENAI_API_KEY`、
`AZURE_OPENAI_DEPLOYMENT`のいずれかが空ならAI機能全体を無効化します。

| 変数 | 既定値 | 用途 |
| --- | --- | --- |
| `AZURE_OPENAI_ENDPOINT` | 未設定 | Azure OpenAI endpoint |
| `AZURE_OPENAI_API_KEY` | 未設定 | Azure OpenAI API key |
| `AZURE_OPENAI_DEPLOYMENT` | 未設定 | 全task共通のfallback deployment |
| `AZURE_OPENAI_API_VERSION` | `2024-10-21` | REST API version |
| `AZURE_OPENAI_CONTEXT_PLANNER_DEPLOYMENT` | 共通deployment | context planner |
| `AZURE_OPENAI_LIVE_EXTRACTION_DEPLOYMENT` | 共通deployment | live extraction |
| `AZURE_OPENAI_TREE_AUDIT_DEPLOYMENT` | 共通deployment | tree audit |
| `AZURE_OPENAI_TREE_REORGANIZER_DEPLOYMENT` | 共通deployment | tree reorganizer |
| `AZURE_OPENAI_FINAL_TREE_REVIEW_DEPLOYMENT` | 共通deployment | final tree review |
| `AZURE_OPENAI_FINAL_SUMMARY_DEPLOYMENT` | 共通deployment | final summary |
| `AI_LIVE_ANALYSIS_ENABLED` | `true` | live分析の有効化 |
| `AI_LIVE_ANALYSIS_INTERVAL_SECONDS` | `10`（最小`5`） | fallback tick |
| `AI_LIVE_ANALYSIS_DEBOUNCE_MILLISECONDS` | `2000`（最小`100`） | final確定イベントのdebounce |
| `AI_LIVE_ANALYSIS_COOLDOWN_SECONDS` | `8` | 同一sessionの呼び出し間隔 |
| `AI_LIVE_ANALYSIS_MAX_WAIT_SECONDS` | `18` | 未分析finalの最大待機時間 |
| `AI_LIVE_ANALYSIS_MIN_CHARS` | `80` | 実行に必要な新規文字数 |
| `AI_LIVE_ANALYSIS_MAX_INPUT_CHARS` | `4000` | live入力文字数上限 |
| `AI_FINAL_SUMMARY_ENABLED` | `true` | 最終要約の有効化 |
| `AI_FINAL_SUMMARY_MAX_INPUT_CHARS` | `12000` | 最終要約入力文字数上限 |
| `AI_REQUEST_TIMEOUT_SECONDS` | `20` | 通常AIリクエストtimeout |
| `AI_FINAL_SUMMARY_TIMEOUT_SECONDS` | `60` | 最終要約timeout |
| `AI_FINALIZATION_WAIT_TIMEOUT_SECONDS` | `10` | finalizationの待機上限 |
| `AI_FINALIZATION_QUIET_PERIOD_MILLISECONDS` | `750`（最小`100`） | 旧Bot向けDB静穏期間 |
| `AI_FINAL_FLUSH_MAX_ATTEMPTS` | `3` | 終了時final抽出の最大試行数 |
| `AI_ANALYSIS_DEBUG_DROPPED_NODES` | `false` | 破棄node詳細の開発ログ |

旧名の `AI_MODEL_CONTEXT_PLANNER`、`AI_MODEL_LIVE_EXTRACTION`、
`AI_MODEL_TREE_AUDIT`、`AI_MODEL_TREE_REORGANIZER`、
`AI_MODEL_FINAL_TREE_REVIEW`、`AI_MODEL_FINAL_SUMMARY` も互換目的で読み取りますが、
対応する `AZURE_OPENAI_*_DEPLOYMENT` を優先します。新規設定では旧名を使わないでください。

## Tree classification

| 変数 | 既定値 | 用途 |
| --- | --- | --- |
| `AI_TREE_AGENDA_ASSIGNMENT_THRESHOLD` | `0.55` | agendaへ即時確定するconfidence境界 |
| `AI_TREE_TOPIC_PROMOTION_MIN_ITEMS` | `2` | dynamic topic昇格に必要なitem数 |
| `AI_TREE_TOPIC_PROMOTION_MIN_ROUNDS` | `2` | dynamic topic昇格に必要なround数 |
| `AI_TREE_MAX_DYNAMIC_TOPICS` | `6` | 1会議のdynamic topic上限 |

## Tree audit

| 変数 | 既定値 |
| --- | --- |
| `TREE_AUDIT_ENABLED` | `true` |
| `TREE_AUDIT_INTERVAL_VERSIONS` | `3` |
| `TREE_AUDIT_INTERVAL_SECONDS` | `300` |
| `TREE_AUDIT_MIN_INTERVAL_SECONDS` | `300` |
| `TREE_AUDIT_MAX_RUNS_PER_SESSION` | `20` |
| `TREE_AUDIT_MAX_RUNS_PER_HOUR` | `12` |
| `TREE_AUDIT_HIGH_SEVERITY_MIN_INTERVAL_SECONDS` | `60` |
| `TREE_AUDIT_HIGH_SEVERITY_MAX_RUNS_PER_HOUR` | `4` |
| `TREE_AUDIT_TIMEOUT_SECONDS` | `25` |
| `TREE_AUDIT_MAX_OUTPUT_TOKENS` | `2500` |
| `TREE_AUDIT_MAX_NODES` | `80` |
| `TREE_AUDIT_MAX_RECENT_SEGMENTS` | `16` |
| `TREE_AUDIT_MAX_EVIDENCE_SEGMENTS` | `24` |
| `TREE_AUDIT_MAX_INPUT_TOKENS` | `12000` |
| `TREE_AUDIT_MAX_PERSISTED_JSON_BYTES` | `262144` |
| `TREE_AUDIT_HIGH_CONFIDENCE_THRESHOLD` | `0.90` |
| `TREE_AUDIT_REQUIRED_IMPROVEMENT_MARGIN` | `0.18` |
| `TREE_AUDIT_COHESION_THRESHOLD` | `0.20` |
| `TREE_AUDIT_TENTATIVE_MAX_VERSIONS` | `3` |
| `TREE_AUDIT_UNAPPLIED_WARNING_THRESHOLD` | `3` |

挙動の詳細は [tree-auditor.md](./tree-auditor.md) を参照してください。
`TREE_AUDIT_MODE` はdeprecatedで、値を設定しても無視します。

## Compose-only variables

`API_PORT`、`POSTGRES_DB`、`POSTGRES_USER`、`POSTGRES_PASSWORD` は
アプリケーションではなく `compose.yaml` が読み取ります。
