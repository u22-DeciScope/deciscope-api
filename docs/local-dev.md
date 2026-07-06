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
`DECISCOPE_TRANSCRIPT_ONLY=true` の場合も、PostgreSQLへの接続自体は必要です
（会議・認証まわりのcore repositoryだけ初期化しません）。

現在のDocker優先構成では、このリポジトリのComposeでPostgreSQL、migration、Go APIを起動し、
フロントエンドは `deciscope-web` 側で起動します。Teams BotはDocker Composeには含めず、
Windows VM上のBotへTailscale経由で接続します。

`docker compose up` を使わず `go run .` で直接起動することもできますが、
`compose.yaml` の `postgres` serviceはPCホストへportを公開していないため、
その場合は別途host側で到達可能なPostgreSQL（ローカルインストール、または
`compose.yaml` に一時的に `ports: ["5432:5432"]` を追加したもの）が必要です。

```powershell
go run . migrate
go run . serve
```

起動時は `.env` を読み込み、その後 `.env.local` が存在すれば上書きします
（`.env.local` はDocker Composeからは読み込まれません。`go run .` 専用です）。

## データベース設定

```env
DATABASE_URL=postgres://deciscope:change-me@localhost:5432/deciscope?sslmode=disable
DECISCOPE_TRANSCRIPT_STORE=postgres
```

- `DATABASE_URL`: PostgreSQL接続URLです。必須です。
- `DECISCOPE_TRANSCRIPT_STORE`: 既定は `postgres` です（省略可）。

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
DECISCOPE_WS_CLIENT_TOKEN=dev-ws-token
DECISCOPE_WS_ALLOWED_ORIGINS=http://localhost:3000,http://localhost:5173,http://127.0.0.1:3000,http://127.0.0.1:5173
DECISCOPE_BOT_CONTROL_URL=http://<VM_TAILSCALE_IP>:<PORT>/internal/bot/join
DECISCOPE_BOT_CONTROL_TOKEN=change-me-bot-control-token
DECISCOPE_BOT_CONTROL_TIMEOUT_SECONDS=10
UPLOAD_DIR=./uploads
FRONTEND_URL=http://localhost:5193
ALLOWED_ORIGINS=http://localhost:5193
DECISCOPE_ENV=development
DECISCOPE_SMTP_HOST=
DECISCOPE_SMTP_PORT=587
DECISCOPE_SMTP_USERNAME=
DECISCOPE_SMTP_PASSWORD=
DECISCOPE_SMTP_FROM=
```

- `DECISCOPE_BACKEND_ADDR`: 待受address。設定時は `PORT` より優先します。
- `DECISCOPE_TRANSCRIPT_ONLY`: `true` の場合は会議・認証まわりのcore repositoryを初期化せず、
  文字起こし取り込みAPIだけを起動します。この場合もPostgreSQL接続は必要です。
- `DECISCOPE_INGEST_API_KEY`: `POST /api/v1/transcript-segments` 用API key。32文字以上が必要です。
- `DECISCOPE_WS_CLIENT_TOKEN`: `GET /api/v1/transcript-segments` と
  `WS /api/v1/ws/transcript-segments` 用の開発client token。未設定なら認証なしです。
- `DECISCOPE_WS_ALLOWED_ORIGINS`: transcript WebSocketを許可するOriginのカンマ区切り。
- `DECISCOPE_BOT_CONTROL_URL`: Go APIからVM Botへ参加命令を送るURL。Tailscale IPを使います。
- `DECISCOPE_BOT_CONTROL_TOKEN`: VM Bot制御API用token。フロントエンドへ渡しません。
- `DECISCOPE_BOT_CONTROL_TIMEOUT_SECONDS`: VM Bot制御APIのHTTP timeout秒数。既定値は `10` です。
- `UPLOAD_DIR`: mock uploadの保存先。
- `FRONTEND_URL`: CORSの基準originであり、招待メール内の参加リンクのbase URLにも使います。
- `ALLOWED_ORIGINS`: CORS許可originのカンマ区切り。
- `DECISCOPE_ENV`: `development` (既定) / `production`。development でSMTP未設定の場合、
  ワークスペース招待メールは送信されず、招待URL (生tokenを含む) をログに出力します。
  production でSMTP未設定の場合は招待作成が失敗します。
- `DECISCOPE_SMTP_*`: ワークスペース招待メールのSMTP設定。`HOST` と `FROM` の両方を
  設定すると実際に送信します。`USERNAME` 未設定なら認証なしで接続します。
- `DECISCOPE_CREATE_SAMPLE_MEETING_ON_FIRST_WORKSPACE`: 所属0件のユーザーが最初の
  ワークスペースを作成したとき、サンプル会議 (終了済みTeams会議 + 文字起こし +
  議論ツリー / 分析カード) を1件投入します。未設定なら development で有効、production で無効。
- `DECISCOPE_SEED_DEMO_DATA`: 固定デモワークスペース (`ws_demo_deciscope`) を投入する
  旧式の開発用 seed。ログイン時の自動参加は廃止済みで、通常フローでは使いません。
  初回ログインフローの確認では `false` のままにしてください。

## 動作確認

まずヘルスチェックを確認します。

```http
GET http://<PC_TAILSCALE_IP>:9090/v1/health
GET http://<PC_TAILSCALE_IP>:9090/healthz
GET http://<PC_TAILSCALE_IP>:9090/readyz
```

現在、ブラウザ用の `/debug` 画面は提供していません。以下のAPIとWebSocketを
使ってdurable event、transcript segment、Markdown reportを確認します。

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

Teams会議URLを登録し、VM Botへ参加命令を送ります。ブラウザUIと同じ経路では
`POST /v1/workspaces/{workspace_code}/meeting-sessions` を使います。次の例は
API-keyで確認する互換/手動ルートです。

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

保存済み文字起こしを履歴取得します。

```http
GET http://<PC_TAILSCALE_IP>:9090/api/v1/transcript-segments?callId=manual-test-call&limit=100&token=dev-ws-token
```

リアルタイム配信は次へWebSocket接続します。VM BotはWebSocketへ接続せず、
既存のHTTP POSTだけを使います。

```text
ws://localhost:9090/api/v1/ws/transcript-segments?token=dev-ws-token
ws://localhost:9090/api/v1/ws/transcript-segments?callId=manual-test-call&token=dev-ws-token
```

フロントエンドもDocker Composeで動かす場合、フロントエンドコンテナからGo APIへは
`http://api:9090` で到達できます。ブラウザからは `api:9090` ではなく、
`ws://localhost:9090/...` の公開ポートかフロントエンド側proxyを使います。

## テスト

```powershell
go test ./...
go vet ./...
```

Repository契約テストはMemoryとPostgreSQLへ同じスイートを実行します。
ApplicationとHTTP HandlerはFake Port・Fake Use Caseでテストします。
Realtime、サーバー結合、依存方向のテストも実行されます。

WindowsのApplication Controlが自動生成されたtest exeを拒否する環境では、
対象Packageを固定名でbuildして実行すると検証できます。

```powershell
go test -c -o .gotmp/realtime-check.exe ./internal/adapter/realtime
.\.gotmp\realtime-check.exe -test.v
```
