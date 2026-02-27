# deciscope-core-api

Go + `chi` API for Firebase token exchange and DB-backed cookie sessions.

## Requirements

- Go 1.26+

## Environment

- `AUTH_SQLITE_PATH` (optional): SQLite file path. Default: `deciscope_auth.db`
- `FIREBASE_CREDENTIALS_JSON` (optional): Firebase service account JSON string
- `GOOGLE_APPLICATION_CREDENTIALS` (optional): Firebase service account file path
- `FIREBASE_PROJECT_ID` (optional): explicit Firebase project ID
- `ALLOWED_ORIGINS` (optional): comma-separated CORS allowlist for frontend testing

If no Firebase credentials are provided, the server falls back to a dev verifier and accepts `Authorization: Bearer dev:<uid>`.

## Run

```powershell
go run .
```

If `go` is not on PATH yet:

```powershell
& 'C:\Program Files\Go\bin\go.exe' run .
```

Server starts on `http://localhost:8080`.
