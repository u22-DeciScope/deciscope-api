# deciscope-core-api

DeciScopeのローカルMVP向け、Go + `chi`製バックエンドです。

会議API、WebSocketリアルタイム配信、fixture replay、SQLite永続化、
mock upload/job、Markdownレポート生成を提供します。Azure、Teams、外部STT、
外部LLMには接続しません。

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

現在サポートするDB driverはSQLiteのみです。

- `database.Open` creates the configured database connection.
- `database.Migrate` applies embedded, versioned migrations.
- Application services depend on purpose-specific Repository interfaces.
- SQLite SQL is isolated under `internal/adapter/repository/sqlite`.
- If SQLite cannot be opened, meeting APIs use the in-memory Repository
  implementation for local fixture testing.
- PostgreSQL向けにPort、Repository共通契約テスト、Migration管理SQLのplaceholder
  変換は準備済みです。
- PostgreSQL接続には、driver、Migration、Repository、UserRepositoryの追加が
  必要です。現時点で `DATABASE_DRIVER=postgres` は使用できません。

主な環境変数:

- `PORT`: listen port。既定値は `9090`
- `DATABASE_DRIVER`: 現在は `sqlite` のみ
- `DATABASE_URL`: SQLite file path。既定値は `./db.sqlite`
- `FIXTURE_DIR`: fixture JSONL directory。既定値は `./fixtures/meetings`
- `UPLOAD_DIR`: local upload directory。既定値は `./uploads`
- `FRONTEND_URL`: CORSの基準origin。既定値は `http://localhost:5193`
- `ALLOWED_ORIGINS`: CORS許可originのカンマ区切り
- `AUTH_PROVIDER`, `FIREBASE_PROJECT_ID`, `GOOGLE_APPLICATION_CREDENTIALS`:
  Firebase Admin SDK設定

完全な例は [.env.example](.env.example) を参照してください。

## Run

```powershell
go run .
```

既定では `http://localhost:9090` で起動します。

## Test

```powershell
go test ./...
go vet ./...
```

Repository契約テストはMemoryとSQLiteに同じSuiteを実行します。依存方向、
Adapter間依存、環境変数読込の配置もArchitecture Testで検査します。

## Main Local APIs

- `GET /v1/health`
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
