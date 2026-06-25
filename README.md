# deciscope-core-api

DeciScopeのローカルMVP向け、Go + `chi`製バックエンドです。

会議API、WebSocketリアルタイム配信、fixture replay、PostgreSQL永続化、
Azure EchoBot向けSQLite文字起こし取り込み、mock upload/job、Markdownレポート生成を
提供します。外部STT、外部LLMには接続しません。

## Requirements

- Go 1.25+

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

既存の会議・認証データはPostgreSQL、EchoBotから受信する文字起こし取り込みは
ローカルSQLiteを使用します。

- `database.Open` creates the configured database connection.
- `database.Migrate` applies embedded, versioned migrations.
- Application services depend on purpose-specific Repository interfaces.
- PostgreSQL SQL is isolated under `internal/adapter/repository/postgres`.
- Database connection or migration failures stop API startup.
- Memory Repository remains available only as a test double.

主な環境変数:

- `DECISCOPE_BACKEND_ADDR`: listen address。設定時は `PORT` より優先
- `DECISCOPE_TRANSCRIPT_ONLY`: `true` の場合はSQLite文字起こし取り込みAPIだけを起動
- `PORT`: fallback listen port。既定値は `9090`
- `DATABASE_URL`: PostgreSQL connection URL。必須
- `DECISCOPE_GO_SQLITE_PATH`: transcript ingest用SQLite file path。必須
- `DECISCOPE_INGEST_API_KEY`: transcript ingest用共有API key。32文字以上、必須
- `FIXTURE_DIR`: fixture JSONL directory。既定値は `./fixtures/meetings`
- `UPLOAD_DIR`: local upload directory。既定値は `./uploads`
- `FRONTEND_URL`: CORSの基準origin。既定値は `http://localhost:5193`
- `ALLOWED_ORIGINS`: CORS許可originのカンマ区切り
- `AUTH_PROVIDER`, `FIREBASE_PROJECT_ID`, `GOOGLE_APPLICATION_CREDENTIALS`:
  Firebase Admin SDK設定

完全な例は [.env.example](.env.example) を参照してください。

## Run

```powershell
docker compose up -d postgres
go run .
```

VMなど別端末から接続する場合は、`.env` の `DECISCOPE_BACKEND_ADDR` に
`<PC_TAILSCALE_IP>:8080` のような待受addressを設定してください。

## Test

```powershell
go test ./...
go vet ./...
```

Repository契約テストはMemoryとPostgreSQLに同じSuiteを実行します。依存方向、
Adapter間依存、環境変数読込の配置もArchitecture Testで検査します。

## Main Local APIs

- `GET /v1/health`
- `GET /healthz`
- `POST /api/v1/transcript-segments`
- `POST /v1/meetings`
- `GET /v1/meetings/{meeting_id}`
- `GET /v1/meetings/{meeting_id}/events?after_seq=0`
- `GET /v1/meetings/{meeting_id}/segments?after_seq=0`
- `GET /v1/meetings/{meeting_id}/report`
- `WS /v1/realtime?meeting_id={meeting_id}`
- `GET /v1/fixtures`
- `POST /v1/meetings/{meeting_id}/replay/start`

API一覧は [docs/api.md](docs/api.md)、ローカル起動手順は
[docs/local-dev.md](docs/local-dev.md) を参照してください。
