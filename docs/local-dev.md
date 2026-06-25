# ローカル開発

## 起動

```powershell
docker compose up -d postgres
go run .
```

`.env.example` の設定では `http://127.0.0.1:8080` で起動します。
VMなど別端末から接続する場合は、`.env` の `DECISCOPE_BACKEND_ADDR` に
`<PC_TAILSCALE_IP>:8080` のような待受addressを設定してください。
`DECISCOPE_BACKEND_ADDR` が未設定の場合は `PORT` がfallbackとして使われます。
`DECISCOPE_TRANSCRIPT_ONLY=true` の場合は、PostgreSQLなしで `/healthz` と
`/api/v1/transcript-segments` だけを起動します。

```powershell
$env:DECISCOPE_BACKEND_ADDR="127.0.0.1:18080"
go run .
```

起動時は `.env` を読み込み、その後 `.env.local` で上書きします。

## データベース設定

```env
DATABASE_URL=postgres://deciscope:deciscope@localhost:5432/deciscope?sslmode=disable
DECISCOPE_GO_SQLITE_PATH=C:\U-22\deciscope-core-api\data\deciscope-go.db
```

- `DATABASE_URL`: PostgreSQL接続URLです。必須です。
- `DECISCOPE_GO_SQLITE_PATH`: EchoBot transcript ingest用SQLite file pathです。必須です。

接続生成は `internal/infrastructure/database` の `database.Open`、
スキーマ更新は埋め込みMigrationを実行する `database.Migrate` が担当します。
PostgreSQL向けSQLは `internal/adapter/repository/postgres` に隔離されています。
PostgreSQLへ接続できない場合、APIは起動に失敗します。

## その他の環境変数

```env
PORT=9090
DECISCOPE_BACKEND_ADDR=127.0.0.1:8080
DECISCOPE_TRANSCRIPT_ONLY=true
DECISCOPE_INGEST_API_KEY=REPLACE_WITH_A_LONG_RANDOM_SECRET
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
GET http://<PC_TAILSCALE_IP>:8080/v1/health
GET http://<PC_TAILSCALE_IP>:8080/healthz
```

現在、ブラウザ用の `/debug` 画面は提供していません。以下のAPIとWebSocketを
使ってfixture replay、durable event、transcript segment、Markdown reportを
確認します。

## 手動クイックデモ

会議を作成します。

```http
POST http://localhost:9090/v1/meetings
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
    -Uri "http://<PC_TAILSCALE_IP>:8080/api/v1/transcript-segments" `
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
