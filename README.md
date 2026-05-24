# deciscope-core-api

Go + `chi` backend for DeciScope local MVP0.

The current backend provides local-only meeting runtime APIs, WebSocket realtime delivery, fixture replay, SQLite persistence, mock uploads/jobs, and Markdown report generation. It does not connect to Azure, Teams, external STT, or external LLM services.

If `go-sqlite3` cannot open SQLite in the local Go runtime, `/v1` automatically falls back to an in-memory store so the fixture demo can still run.

## Requirements

- Go 1.25+

## Environment

- `SQLITE_PATH` or `AUTH_SQLITE_PATH` (optional): SQLite file path. Default: `./db.sqlite`
- `FIXTURE_DIR` (optional): fixture JSONL directory. Default: `./fixtures/meetings`
- `UPLOAD_DIR` (optional): local upload directory. Default: `./uploads`
- `FIREBASE_CREDENTIALS_JSON` (optional): Firebase service account JSON string
- `GOOGLE_APPLICATION_CREDENTIALS` (optional): Firebase service account file path
- `FIREBASE_PROJECT_ID` (optional): explicit Firebase project ID
- `ALLOWED_ORIGINS` (optional): comma-separated CORS allowlist for web app testing

If no Firebase credentials are provided, `/v1` still works without auth and protected legacy `/api` routes accept `Authorization: Bearer dev:<uid>`.

## Run

```powershell
go run .
```

If `go` is not on PATH yet:

```powershell
& 'C:\Program Files\Go\bin\go.exe' run .
```

Server starts on `http://localhost:8080`.

Open `http://localhost:8080/debug` to run a minimal browser-based backend check.

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

See `docs/` for the backend foundation, REST API, realtime event contract, and local demo flow.

For Google login setup, see `docs/firebase-auth.md`.
