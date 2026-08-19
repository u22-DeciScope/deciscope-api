# deciscope-core-api

DeciScopeのローカルMVP向け、Go + `chi`製バックエンドです。

会議API、WebSocketリアルタイム配信、PostgreSQL永続化、
Azure EchoBot向け文字起こし取り込みを
提供します。Teams音声のSTTはVM上のTeams Botが担当し、このAPIはBotから送られる
transcript segmentを受け取ります。Azure OpenAIを設定した場合は、会議中ライブ分析と
会議終了時の最終要約も生成します。raw audioのMedia IngressやファイルSTT/ffmpeg処理は
現在のプロダクト範囲には含めていません。

## Requirements

- Go 1.25+
- Docker / Docker Compose

## Architecture

Clean Architectureベースのモジュラーモノリスです。

```text
internal/app
  -> internal/adapter, internal/infrastructure
    -> internal/application
      -> internal/domain
```

- `internal/app`: 設定読込と具体実装の組み立て
- `internal/domain`: Domain Entity、Error、純粋なRule
- `internal/application`: Use CaseとOutbound Port
- `internal/adapter`: HTTP、WebSocket、Repository実装
- `internal/infrastructure`: DB接続・Migration、Azure OpenAI、Bot制御、
  クライアント診断ログ、メール、Firebase

詳細は [docs/backend-architecture.md](docs/backend-architecture.md) を参照してください。

## Database

会議・認証データとEchoBot文字起こし取り込みデータは、いずれもPostgreSQLに保存します。
`DECISCOPE_TRANSCRIPT_ONLY=true` にすると、会議・認証まわりのcore repositoryは初期化せず、
文字起こし取り込みAPIだけを起動できます（この場合もPostgreSQLへの接続は必要です）。

PostgreSQLの文字起こしテーブルは `transcript_segments` です。主な列は
`event_id`, `call_id`, `sequence_no`, `recognized_at_utc`, `offset_ticks`,
`duration_ticks`, `text`, `received_at_utc` で、`event_id` と
`(call_id, sequence_no)` に一意制約があります。同じデータの再送は重複として扱い、
2行目は作りません。

MigrationはAPI起動とは分離しています。同じDockerイメージで次を実行できます。

```powershell
./deciscope-api migrate
./deciscope-api serve
```

Docker Composeでは `migrate` serviceが先に成功してから `api` serviceが起動します。

## Environment

主な環境変数:

- `API_PORT`: Docker Composeでホストへ公開するAPI port。既定値は `9090`
- `PORT`: APIコンテナ内またはローカル実行時のlisten port。既定値は `9090`
- `DECISCOPE_BACKEND_ADDR`: listen address。設定時は `PORT` より優先
- `DATABASE_URL`: PostgreSQL connection URL。ローカル実行時は必須
- `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD`: Compose PostgreSQL設定
- `DECISCOPE_INGEST_API_KEY`: transcript ingest用共有API key。32文字以上、必須
- `DECISCOPE_TRANSCRIPT_ONLY`: `true` の場合は文字起こし取り込みAPIだけを起動
- `DECISCOPE_WS_ALLOWED_ORIGINS`: transcript WebSocketの許可Origin。カンマ区切り
- `DECISCOPE_BOT_CONTROL_URL`: Go APIからVM Botへ参加命令を送るURL。Tailscale IPを使います
- `DECISCOPE_BOT_CONTROL_TOKEN`: VM Bot制御API用token。フロントエンドへ渡しません
- `DECISCOPE_BOT_CONTROL_TIMEOUT_SECONDS`: VM Bot制御API呼び出しtimeout。既定値は `10`
- `FRONTEND_URL`, `ALLOWED_ORIGINS`: CORS設定
- `SESSION_COOKIE_SECURE`: `true` の場合、セッションCookieに `Secure` 属性を付与
- `AUTH_PROVIDER`, `FIREBASE_PROJECT_ID`, `GOOGLE_APPLICATION_CREDENTIALS`,
  `FIREBASE_CREDENTIALS_JSON`: Firebase認証設定。
  詳細は [docs/firebase-auth.md](docs/firebase-auth.md) を参照してください

完全な例は [.env.example](.env.example) を参照してください。`.env` はGit管理対象外です。

Bot/watchdog、文字起こしヘルス、クライアント診断、AIツリー分類を含む全設定の
用途と既定値は [docs/configuration.md](docs/configuration.md) を参照してください。

### AI会議分析 (Azure OpenAI)

- `AZURE_OPENAI_ENDPOINT`, `AZURE_OPENAI_API_KEY`, `AZURE_OPENAI_DEPLOYMENT`: Azure OpenAI接続情報。
  いずれか1つでも未設定の場合、AI分析機能全体が自動的に無効化されます(起動時に警告ログを1行出力)。
  transcript取り込みや会議終了処理はAI機能の有無に関係なく動作し続けます
- `AZURE_OPENAI_API_VERSION`: Azure OpenAI REST APIのバージョン。既定値は `2024-10-21`
- `AZURE_OPENAI_{LIVE_EXTRACTION,CONTEXT_PLANNER,TREE_AUDIT,TREE_REORGANIZER,FINAL_TREE_REVIEW,FINAL_SUMMARY}_DEPLOYMENT`:
  AI task別のdeployment。未設定のtaskは既存の`AZURE_OPENAI_DEPLOYMENT`へfallbackします
- ライブ抽出は対応deploymentでAzure Structured Outputs（`json_schema`, `strict: true`）を使います。
  deploymentがこの形式を明示的に拒否した場合は、同一プロセス中は従来の`json_object`へフォールバックします
- `AI_LIVE_ANALYSIS_ENABLED`: 会議中ライブAI分析を行うか。既定値は `true`
- `AI_LIVE_ANALYSIS_INTERVAL_SECONDS`: 未分析finalを救済するfallback tick。既定値は `10`、最小値は `5`
- `AI_LIVE_ANALYSIS_DEBOUNCE_MILLISECONDS`: final確定イベントをまとめるdebounce。既定値は `2000`
- `AI_LIVE_ANALYSIS_COOLDOWN_SECONDS`: 同一sessionのライブAI呼び出し間隔。既定値は `8`
- `AI_LIVE_ANALYSIS_MAX_WAIT_SECONDS`: substantiveな未分析finalの最大待機時間。既定値は `18`
- `AI_LIVE_ANALYSIS_MIN_CHARS`: ライブ分析を実行する最小の新規文字数。既定値は `80`
- `AI_LIVE_ANALYSIS_MAX_INPUT_CHARS`: ライブ分析1回あたりに送る差分transcriptの最大文字数。既定値は `4000`
- `AI_FINAL_SUMMARY_ENABLED`: 会議終了時のAI最終要約生成を行うか。既定値は `true`
- `AI_FINAL_SUMMARY_MAX_INPUT_CHARS`: 最終要約に送るtranscriptの最大文字数(超過分は末尾優先で切り詰め)。既定値は `12000`
- `AI_REQUEST_TIMEOUT_SECONDS`: ライブ分析のAzure OpenAI呼び出しtimeout。既定値は `20`
- `AI_FINAL_SUMMARY_TIMEOUT_SECONDS`: 最終要約のAzure OpenAI呼び出しtimeout。既定値は `60`
- `AI_FINALIZATION_WAIT_TIMEOUT_SECONDS`: 実行中分析またはBot通知済み最終sequenceのDB到着を待つ上限。既定値は `10`
- `AI_FINALIZATION_QUIET_PERIOD_MILLISECONDS`: drain情報を送らない旧Bot向けのDB静穏判定。既定値は `750`、最小値は `100`
- `AI_FINAL_FLUSH_MAX_ATTEMPTS`: 終了時の未処理final抽出の最大試行回数。既定値は `3`
- `TREE_AUDIT_ENABLED`: GPT-5-mini向け議論ツリー監査schedulerを有効化するか。既定値は`true`
- `TREE_AUDIT_INTERVAL_VERSIONS`, `TREE_AUDIT_INTERVAL_SECONDS`, `TREE_AUDIT_MIN_INTERVAL_SECONDS`:
  version周期、時間周期、通常triggerのdebounce下限。既定値は順に`3`, `300`, `300`
- `TREE_AUDIT_MAX_RUNS_PER_SESSION`, `TREE_AUDIT_MAX_RUNS_PER_HOUR`:
  通常triggerのsession上限・1時間上限。既定値は`20`, `12`
- `TREE_AUDIT_HIGH_SEVERITY_MIN_INTERVAL_SECONDS`, `TREE_AUDIT_HIGH_SEVERITY_MAX_RUNS_PER_HOUR`:
  deterministicな重大異常triggerの別枠debounce・1時間上限。既定値は`60`, `4`
- `TREE_AUDIT_UNAPPLIED_WARNING_THRESHOLD`: findingを検出したままoperation適用0件が
  連続した場合の警告閾値。既定値は`3`
- 入力・頻度・timeout上限の詳細は [docs/tree-auditor.md](docs/tree-auditor.md) を参照してください

`DECISCOPE_TRANSCRIPT_ONLY=true` のtranscript-onlyモードでは、AI分析機能は組み込まれません。

Teams会議名を Microsoft Graph の `/users/{id}/onlineMeetings` から取得するための候補は、
会議作成リクエストの主催者、作成者Microsoft user ID、作成者メールアドレスから組み立てて
Bot join commandへ渡します。ログには値そのものではなく件数とハッシュのみを出します。

## Docker Compose

PowerShellで `.env.example` から `.env` を作り、秘密値を置き換えます。

```powershell
Copy-Item .env.example .env
notepad .env
```

起動します。`postgres` はCompose内部ネットワークだけに公開され、PCホストへは公開されません。

```powershell
docker compose up --build -d
docker compose ps
docker compose logs -f api
```

Migrationだけを再実行する場合:

```powershell
docker compose run --rm migrate
```

ヘルスチェック:

```powershell
Invoke-RestMethod http://localhost:9090/healthz
Invoke-RestMethod http://localhost:9090/readyz
```

PostgreSQL内の文字起こしデータ確認:

```powershell
docker compose exec postgres psql -U deciscope -d deciscope
```

```sql
SELECT call_id, sequence_no, text, offset_ticks, duration_ticks, received_at_utc
FROM transcript_segments
ORDER BY received_at_utc DESC
LIMIT 20;
```

停止します。どちらもnamed volume `postgres-data` は残ります。

```powershell
docker compose stop
docker compose down
```

データも削除したい場合だけ、明示的に `-v` を付けます。

```powershell
docker compose down -v
```

## EchoBot Ingest Contract

既存のC# Bot向けHTTP契約は維持しています。

```http
POST /api/v1/transcript-segments
Content-Type: application/json
X-DeciScope-Api-Key: <DECISCOPE_INGEST_API_KEY>
```

```json
{
  "sessionId": "session_...",
  "eventId": "06008080-91e3-4b88-a8ff-9af629265ced:1",
  "callId": "06008080-91e3-4b88-a8ff-9af629265ced",
  "sequenceNo": 1,
  "speakerId": "8:orgid:xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
  "speakerName": "山田 太郎",
  "recognizedAtUtc": "2026-06-25T13:20:01.1234567+00:00",
  "offsetTicks": 20300000,
  "durationTicks": 18000000,
  "text": "本日の会議を開始します。"
}
```

`sessionId` は任意です。既存のVM Botや手動POSTが `sessionId` を送らなくても
従来どおり保存できます。会議セッション作成APIから返った `sessionId` を付けると、
履歴取得とWebSocketで `sessionId` による絞り込みができます。
`speakerId` / `speakerName` も任意です。Botが話者情報を送った場合は
PostgreSQLの `speaker_id` / `speaker_name` に保存し、履歴APIとWebSocket配信にも
camelCaseで含めます。

手動テスト:

```powershell
$apiKey = Read-Host "DeciScope API key"

$headers = @{
    "X-DeciScope-Api-Key" = $apiKey
}

$n = Get-Random -Minimum 1000 -Maximum 999999

$body = @{
    sessionId       = "manual-speaker-session"
    eventId         = "manual-speaker-test-$n"
    callId          = "manual-speaker-call"
    sequenceNo      = $n
    recognizedAtUtc = [DateTimeOffset]::UtcNow.ToString("O")
    offsetTicks     = 0
    durationTicks   = 10000000
    text            = "Go APIへの保存テストです。"
    speakerId       = "manual-speaker-001"
    speakerName     = "手動テスト太郎"
} | ConvertTo-Json

Invoke-RestMethod `
    -Method Post `
    -Uri "http://localhost:9090/api/v1/transcript-segments" `
    -Headers $headers `
    -ContentType "application/json; charset=utf-8" `
    -Body $body
```

DB確認:

```sql
SELECT session_id, call_id, sequence_no, speaker_id, speaker_name, text, recognized_at_utc
FROM transcript_segments
ORDER BY recognized_at_utc DESC
LIMIT 10;
```

履歴取得レスポンスにも話者情報が含まれます。

```json
{
  "items": [
    {
      "sessionId": "manual-speaker-session",
      "eventId": "manual-speaker-test-1234",
      "callId": "manual-speaker-call",
      "sequenceNo": 1234,
      "speakerId": "manual-speaker-001",
      "speakerName": "手動テスト太郎",
      "recognizedAtUtc": "2026-06-27T07:00:00Z",
      "offsetTicks": 0,
      "durationTicks": 10000000,
      "text": "Go APIへの保存テストです。",
      "receivedAtUtc": "2026-06-27T07:00:01Z"
    }
  ]
}
```

VMから接続する場合、PostgreSQLコンテナや `postgres:5432` へ直接接続しません。
VM側の送信先はPCホストのTailscale IPとComposeで公開したAPI portです。

```text
http://100.70.221.61:9090/api/v1/transcript-segments
```

`API_PORT` を変えた場合は、上記の `9090` をそのホスト側公開portへ変更してください。

## Transcript Realtime WebSocket

VM BotはWebSocketへ接続しません。従来どおり
`POST /api/v1/transcript-segments` へHTTP POSTし、Go APIがDB保存に成功した
新規segmentだけを接続中のフロントエンドへbroadcastします。同一内容の再送
`duplicate: true` は配信しません。

ブラウザはSession Cookieとworkspace所属検査で保護された次のAPIだけを利用します。

```text
WS  /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/transcript-stream
GET /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/transcript-segments
```

WebSocket message:

```json
{
  "type": "transcript_segment.created",
  "sentAtUtc": "2026-06-27T00:00:00Z",
  "data": {
    "sessionId": "session_...",
    "eventId": "09005080-cce6-4132-9404-1e823df47ff9:6",
    "callId": "09005080-cce6-4132-9404-1e823df47ff9",
    "sequenceNo": 6,
    "speakerId": "8:orgid:xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    "speakerName": "山田 太郎",
    "recognizedAtUtc": "2026-06-27T00:00:00Z",
    "offsetTicks": 287000000,
    "durationTicks": 41200000,
    "text": "うーんってことは、まあ一旦大丈夫そうかな。",
    "duplicate": false
  }
}
```

会議セッションの状態変化も同じWebSocketへ配信されます。

```json
{
  "type": "meeting_session.status_changed",
  "sentAtUtc": "2026-06-27T00:00:00Z",
  "data": {
    "sessionId": "session_...",
    "status": "joined",
    "botCallId": "09005080-cce6-4132-9404-1e823df47ff9"
  }
}
```

## Teams Bot Meeting Sessions

フロントエンドはVM Botを直接叩きません。通常のブラウザUIは認証済みの
`POST /v1/workspaces/{workspace_code}/meeting-sessions` へTeams会議URLを送信し、
Go APIがTailscale内のVM Bot制御APIへ参加命令を送ります。
`/api/v1/meeting-sessions` はAPI-keyを使う互換/手動確認用の非workspaceルートです。

VM Bot制御APIの設定:

```env
DECISCOPE_BOT_CONTROL_URL=http://<VM_TAILSCALE_IP>:<PORT>/internal/bot/join
DECISCOPE_BOT_CONTROL_TOKEN=change-me-bot-control-token
DECISCOPE_BOT_CONTROL_TIMEOUT_SECONDS=10
```

Go APIからVM Botへ送るリクエスト:

```http
POST <DECISCOPE_BOT_CONTROL_URL>
Content-Type: application/json
X-DeciScope-Bot-Control-Token: <DECISCOPE_BOT_CONTROL_TOKEN>
```

```json
{
  "sessionId": "session_...",
  "joinUrl": "https://teams.microsoft.com/l/meetup-join/..."
}
```

会議終了時は、設定URL末尾の `/join` を除いた同じベースURLへ次を送ります。

```http
POST http://<VM_TAILSCALE_IP>:<PORT>/internal/bot/meeting-sessions/{session_id}/end
Content-Type: application/json
X-DeciScope-Bot-Control-Token: <DECISCOPE_BOT_CONTROL_TOKEN>
```

Go APIホストからBotホストへのfirewall/proxyでは、参加用の
`/internal/bot/join` と終了用の `/internal/bot/meeting-sessions/{session_id}/end`
の両方を許可してください。

VM Botが2xxを返すと `meeting_sessions.status` は `command_sent` になります。
4xx/5xx/timeoutの場合は `failed` と `last_error` を保存します。
`joinUrl` 全文とtokenはログへ出さず、ログには `sessionId` と `joinUrlHash` を使います。

手動テスト:

```powershell
$body = @{
  joinUrl = "https://teams.microsoft.com/l/meetup-join/..."
} | ConvertTo-Json

Invoke-WebRequest `
  -Uri "http://localhost:9090/api/v1/meeting-sessions" `
  -Method POST `
  -ContentType "application/json" `
  -Body $body `
  -UseBasicParsing
```

状態取得:

```powershell
Invoke-RestMethod "http://localhost:9090/api/v1/meeting-sessions/<sessionId>"
```

ブラウザUIと同じ経路を確認する場合は、Firebase loginで発行された
`deciscope_session` Cookieを持つ状態で、workspace-scoped APIを使います。

VM Botからの状態更新:

```powershell
$apiKey = Read-Host "DeciScope ingest API key"
$body = @{
  status = "joined"
  botCallId = "09005080-cce6-4132-9404-1e823df47ff9"
  message = "joined successfully"
} | ConvertTo-Json

Invoke-RestMethod `
  -Method Patch `
  -Uri "http://localhost:9090/api/v1/bot/meeting-sessions/<sessionId>/status" `
  -Headers @{ "X-DeciScope-Api-Key" = $apiKey } `
  -ContentType "application/json" `
  -Body $body
```

レガシーの履歴GETと共有token WebSocketはルーターから削除されています。
`DECISCOPE_INGEST_API_KEY` はBotからのPOST専用で、ブラウザへ渡さないでください。

フロントエンドもDocker Composeで起動する場合、フロントエンドコンテナから
Go APIへは `http://api:9090` で到達できます。ただしブラウザで実行される
JavaScriptから `api:9090` は解決できないため、ブラウザのWebSocket URLは
ホスト公開ポートかフロントエンド側proxyを使います。

手動確認の流れ:

```powershell
# 認証済みフロントエンドを開き、別shellから既存HTTP POSTでsegmentを送信
$apiKey = Read-Host "DeciScope ingest API key"
$headers = @{ "X-DeciScope-Api-Key" = $apiKey }
$body = @{
    eventId         = "manual-ws-test:1"
    callId          = "manual-ws-test"
    sequenceNo      = 1
    recognizedAtUtc = [DateTimeOffset]::UtcNow.ToString("O")
    offsetTicks     = 0
    durationTicks   = 10000000
    text            = "WebSocket配信テストです。"
} | ConvertTo-Json

Invoke-RestMethod `
    -Method Post `
    -Uri "http://localhost:9090/api/v1/transcript-segments" `
    -Headers $headers `
    -ContentType "application/json; charset=utf-8" `
    -Body $body
```

## Main Local APIs

- `GET /v1/health`
- `GET /healthz`
- `GET /readyz`
- `POST /api/v1/transcript-segments`
- `POST /api/v1/meeting-sessions` (互換/手動確認用。API key必須)
- `GET /api/v1/meeting-sessions/{session_id}` (互換/手動確認用。API key必須)
- `PATCH /api/v1/bot/meeting-sessions/{session_id}/status`
- `POST /internal/client-diagnostics` (有効時。Session Cookie必須)
- `GET /v1/workspaces/{workspace_code}/meetings`
- `POST /v1/workspaces/{workspace_code}/meetings`
- `GET /v1/workspaces/{workspace_code}/meeting-sessions`
- `GET /v1/workspaces/{workspace_code}/meeting-sessions/final-summaries`
- `POST /v1/workspaces/{workspace_code}/meeting-sessions`
- `GET /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}`
- `POST /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/end`
- `DELETE /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/`
- `PATCH /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/agenda-progress`
- `GET /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/transcript-segments`
- `GET /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/transcript-stream`
- `GET /v1/meetings/{meeting_id}`
- `GET /v1/meetings/{meeting_id}/events?after_seq=0`
- `GET /v1/meetings/{meeting_id}/segments?after_seq=0`
- `WS /v1/realtime?meeting_id={meeting_id}`

API一覧は [docs/api.md](docs/api.md)、ローカル起動手順は
[docs/local-dev.md](docs/local-dev.md) を参照してください。

## Test

```powershell
go test ./...
go vet ./...
```

Repository契約テストはMemoryとPostgreSQLに同じSuiteを実行します。依存方向、
Adapter間依存、環境変数読込の配置もArchitecture Testで検査します。
