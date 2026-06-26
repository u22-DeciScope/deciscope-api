# ローカル開発

## 起動

```powershell
Copy-Item .env.example .env
notepad .env
docker compose up --build -d
```

`.env.example` の設定では `http://localhost:9090` で起動します。
VMなど別端末から接続する場合は、`.env` の `DECISCOPE_BACKEND_ADDR` に
`<PC_TAILSCALE_IP>:9090` のような待受addressを設定できます。
`DECISCOPE_BACKEND_ADDR` が未設定の場合は `PORT` がfallbackとして使われます。
`DECISCOPE_TRANSCRIPT_ONLY=true` の場合は、PostgreSQLなしで `/healthz` と
`/api/v1/transcript-segments` だけを起動します。

```powershell
go run . migrate
go run . serve
```

起動時は `.env` を読み込み、その後 `.env.local` で上書きします。

## データベース設定

```env
DATABASE_URL=postgres://deciscope:change-me@localhost:5432/deciscope?sslmode=disable
DECISCOPE_TRANSCRIPT_STORE=postgres
```

- `DATABASE_URL`: PostgreSQL接続URLです。必須です。
- `DECISCOPE_TRANSCRIPT_STORE`: 既定は `postgres` です。SQLite fallback時だけ `sqlite` にします。
- `DECISCOPE_GO_SQLITE_PATH`: SQLite fallback用file pathです。

接続生成は `internal/infrastructure/database` の `database.Open`、
スキーマ更新は `go run . migrate` またはComposeの `migrate` serviceが担当します。
PostgreSQL向けSQLは `internal/adapter/repository/postgres` に隔離されています。
PostgreSQLへ接続できない場合、APIは起動に失敗します。

## その他の環境変数

```env
PORT=9090
DECISCOPE_BACKEND_ADDR=127.0.0.1:9090
DECISCOPE_TRANSCRIPT_ONLY=false
DECISCOPE_INGEST_API_KEY=change-me-change-me-change-me-1234
FIXTURE_DIR=./fixtures/meetings
UPLOAD_DIR=./uploads
FRONTEND_URL=http://localhost:5193
ALLOWED_ORIGINS=http://localhost:5193
```

- `DECISCOPE_BACKEND_ADDR`: 待受address。設定時は `PORT` より優先します。
- `DECISCOPE_TRANSCRIPT_ONLY`: `true` の場合はPostgreSQLを初期化しません。
- `DECISCOPE_INGEST_API_KEY`: `POST /api/v1/transcript-segments` 用API key。32文字以上が必要です。
- `FIXTURE_DIR`: fixture JSONLディレクトリ。
- `UPLOAD_DIR`: mock uploadの保存先。
- `FRONTEND_URL`: CORSの基準origin。未指定時は `http://localhost:5193`。
- `ALLOWED_ORIGINS`: CORS許可originのカンマ区切り。

## 動作確認

まずヘルスチェックを確認します。

```http
GET http://<PC_TAILSCALE_IP>:9090/v1/health
GET http://<PC_TAILSCALE_IP>:9090/healthz
GET http://<PC_TAILSCALE_IP>:9090/readyz
```

現在、ブラウザ用の `/debug` 画面は提供していません。以下のAPIとWebSocketを
使ってfixture replay、durable event、transcript segment、Markdown reportを
確認します。

## 手動クイックデモ

会議を作成します。

```http
POST http://localhost:9090/v1/workspaces/{workspace_code}/meetings
Content-Type: application/json

{
  "title": "Demo meeting",
  "source": "fixture_replay"
}
```

返された`id`でWebSocketへ接続します。

```text
ws://localhost:9090/v1/realtime?meeting_id={meeting_id}
```

接続後、必要に応じて `client.hello` を送信できます。

```json
{
  "type": "client.hello",
  "meeting_id": "{meeting_id}",
  "last_seq": 0
}
```

fixture replayを開始します。

```http
POST http://localhost:9090/v1/meetings/{meeting_id}/replay/start
Content-Type: application/json

{
  "fixture": "demo.jsonl"
}
```

イベント、発話、レポートを確認します。

```http
GET http://localhost:9090/v1/meetings/{meeting_id}/events?after_seq=0
GET http://localhost:9090/v1/meetings/{meeting_id}/segments?after_seq=0
GET http://localhost:9090/v1/meetings/{meeting_id}/report
Accept: text/markdown
```

EchoBot形式の文字起こしを保存します。

```powershell
$apiKey = Read-Host "DeciScope API key"

$headers = @{
    "X-DeciScope-Api-Key" = $apiKey
}

$body = @{
    eventId         = "manual-test-call:1"
    callId          = "manual-test-call"
    sequenceNo      = 1
    recognizedAtUtc = [DateTimeOffset]::UtcNow.ToString("O")
    offsetTicks     = 0
    durationTicks   = 10000000
    text            = "Go APIへの保存テストです。"
} | ConvertTo-Json

Invoke-RestMethod `
    -Method Post `
    -Uri "http://<PC_TAILSCALE_IP>:9090/api/v1/transcript-segments" `
    -Headers $headers `
    -ContentType "application/json; charset=utf-8" `
    -Body $body
```

## テスト

```powershell
go test ./...
go vet ./...
```

Repository契約テストはMemoryとPostgreSQLへ同じスイートを実行します。
ApplicationとHTTP HandlerはFake Port・Fake Use Caseでテストします。
Fixture、Realtime、サーバー結合、依存方向のテストも実行されます。

WindowsのApplication Controlが自動生成されたtest exeを拒否する環境では、
対象Packageを固定名でbuildして実行すると検証できます。

```powershell
go test -c -o .gotmp/realtime-check.exe ./internal/adapter/realtime
.\.gotmp\realtime-check.exe -test.v
```
