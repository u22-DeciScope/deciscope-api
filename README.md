# deciscope-core-api

DeciScopeのローカルMVP向け、Go + `chi`製バックエンドです。

会議API、WebSocketリアルタイム配信、fixture replay、PostgreSQL永続化、
Azure EchoBot向け文字起こし取り込み、mock upload/job、Markdownレポート生成を
提供します。外部STT、外部LLMには接続しません。

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
- `internal/adapter`: HTTP、WebSocket、fixture、Repository実装
- `internal/infrastructure`: DB接続、Migration、Firebase、filesystem storage

詳細は [docs/backend-architecture.md](docs/backend-architecture.md) を参照してください。

## Database

標準構成では、会議・認証データとEchoBot文字起こし取り込みデータを
PostgreSQLに保存します。文字起こし取り込みだけをローカルSQLiteへ保存したい場合は、
`DECISCOPE_TRANSCRIPT_ONLY=true` または `DECISCOPE_TRANSCRIPT_STORE=sqlite` と
`DECISCOPE_GO_SQLITE_PATH` を設定してください。

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
- `DECISCOPE_TRANSCRIPT_STORE`: `postgres` または `sqlite`
- `DECISCOPE_TRANSCRIPT_ONLY`: `true` の場合は文字起こし取り込みAPIだけを起動
- `DECISCOPE_WS_CLIENT_TOKEN`: transcript WebSocket/履歴GET用client token。未設定時は開発用に認証なし
- `DECISCOPE_WS_ALLOWED_ORIGINS`: transcript WebSocketの許可Origin。カンマ区切り
- `DECISCOPE_BOT_CONTROL_URL`: Go APIからVM Botへ参加命令を送るURL。Tailscale IPを使います
- `DECISCOPE_BOT_CONTROL_TOKEN`: VM Bot制御API用token。フロントエンドへ渡しません
- `DECISCOPE_BOT_CONTROL_TIMEOUT_SECONDS`: VM Bot制御API呼び出しtimeout。既定値は `10`
- `DECISCOPE_GO_SQLITE_PATH`: SQLite fallback用file path
- `FIXTURE_DIR`: fixture JSONL directory
- `UPLOAD_DIR`: local upload directory
- `FRONTEND_URL`, `ALLOWED_ORIGINS`: CORS設定

完全な例は [.env.example](.env.example) を参照してください。`.env` はGit管理対象外です。

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

```text
WS  /api/v1/ws/transcript-segments
WS  /api/v1/ws/transcript-segments?callId={call_id}
WS  /api/v1/ws/transcript-segments?sessionId={session_id}
GET /api/v1/transcript-segments?callId={call_id}&limit=100
GET /api/v1/transcript-segments?sessionId={session_id}&limit=100
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

フロントエンドはVM Botを直接叩きません。Teams会議URLはGo APIの
`POST /api/v1/meeting-sessions` へ送信し、Go APIがTailscale内のVM Bot制御APIへ
参加命令を送ります。

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

`DECISCOPE_WS_CLIENT_TOKEN` を設定している場合、WebSocketと履歴GETには
`?token=...` が必要です。この値はフロントエンド検証用の別tokenであり、
`DECISCOPE_INGEST_API_KEY` をブラウザへ渡さないでください。

```text
ws://localhost:9090/api/v1/ws/transcript-segments?token=dev-ws-token
ws://localhost:9090/api/v1/ws/transcript-segments?callId=09005080-cce6-4132-9404-1e823df47ff9&token=dev-ws-token
http://localhost:9090/api/v1/transcript-segments?callId=09005080-cce6-4132-9404-1e823df47ff9&limit=100&token=dev-ws-token
```

フロントエンドもDocker Composeで起動する場合、フロントエンドコンテナから
Go APIへは `http://api:9090` で到達できます。ただしブラウザで実行される
JavaScriptから `api:9090` は解決できないため、ブラウザのWebSocket URLは
ホスト公開ポートかフロントエンド側proxyを使います。

```text
ブラウザから直接: ws://localhost:9090/api/v1/ws/transcript-segments
Tailscale経由:    ws://100.70.221.61:9090/api/v1/ws/transcript-segments
Compose内部proxy: http://api:9090/api/v1/ws/transcript-segments
```

手動確認の流れ:

```powershell
# 1) WebSocket clientを接続
# 例: ws://localhost:9090/api/v1/ws/transcript-segments?token=dev-ws-token

# 2) 別shellから既存HTTP POSTでsegmentを送信
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
- `GET /api/v1/transcript-segments?callId={call_id}&limit=100`
- `WS /api/v1/ws/transcript-segments?callId={call_id}`
- `POST /api/v1/meeting-sessions`
- `GET /api/v1/meeting-sessions/{session_id}`
- `PATCH /api/v1/bot/meeting-sessions/{session_id}/status`
- `GET /v1/workspaces/{workspace_code}/meetings`
- `POST /v1/workspaces/{workspace_code}/meetings`
- `GET /v1/meetings/{meeting_id}`
- `GET /v1/meetings/{meeting_id}/events?after_seq=0`
- `GET /v1/meetings/{meeting_id}/segments?after_seq=0`
- `GET /v1/meetings/{meeting_id}/report`
- `WS /v1/realtime?meeting_id={meeting_id}`
- `GET /v1/fixtures`
- `POST /v1/meetings/{meeting_id}/replay/start`

API一覧は [docs/api.md](docs/api.md)、ローカル起動手順は
[docs/local-dev.md](docs/local-dev.md) を参照してください。

## Test

```powershell
go test ./...
go vet ./...
```

Repository契約テストはMemoryとPostgreSQLに同じSuiteを実行します。依存方向、
Adapter間依存、環境変数読込の配置もArchitecture Testで検査します。
