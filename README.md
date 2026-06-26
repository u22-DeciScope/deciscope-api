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
  "eventId": "06008080-91e3-4b88-a8ff-9af629265ced:1",
  "callId": "06008080-91e3-4b88-a8ff-9af629265ced",
  "sequenceNo": 1,
  "recognizedAtUtc": "2026-06-25T13:20:01.1234567+00:00",
  "offsetTicks": 20300000,
  "durationTicks": 18000000,
  "text": "本日の会議を開始します。"
}
```

手動テスト:

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
    -Uri "http://localhost:9090/api/v1/transcript-segments" `
    -Headers $headers `
    -ContentType "application/json; charset=utf-8" `
    -Body $body
```

VMから接続する場合、PostgreSQLコンテナや `postgres:5432` へ直接接続しません。
VM側の送信先はPCホストのTailscale IPとComposeで公開したAPI portです。

```text
http://100.70.221.61:9090/api/v1/transcript-segments
```

`API_PORT` を変えた場合は、上記の `9090` をそのホスト側公開portへ変更してください。

## Main Local APIs

- `GET /v1/health`
- `GET /healthz`
- `GET /readyz`
- `POST /api/v1/transcript-segments`
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
