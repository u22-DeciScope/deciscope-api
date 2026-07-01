# Firebase Microsoft Authentication

DeciScope currently uses Firebase in two places.

- Web: Firebase Web SDK opens the Microsoft sign-in flow and obtains a Firebase ID token.
- Backend: Firebase Admin SDK verifies that ID token and returns the authenticated user.

Firebase is used by `POST /v1/auth/login` and the protected `/v1/auth/me`,
workspace, meeting, and WebSocket routes.

## Firebase Console Setup

1. Open the Firebase console for the project.
2. In Authentication, enable the Microsoft sign-in provider and set its OAuth client ID and secret.
3. In Project settings, add or select a Web app.
4. Copy the Web app config values into the `web` repo's `.env.local`.
5. In Project settings, Service accounts, generate a new private key for the Admin SDK.
6. Save the JSON file outside git, for example under the `web` repo's ignored `secrets` directory.
7. Point the backend `.env` to that JSON file.

## Web Env

Create `.env.local` in the `web` repo.

```env
VITE_API_BASE_URL=http://localhost:9090
VITE_WS_BASE_URL=ws://localhost:9090

VITE_FIREBASE_API_KEY=...
VITE_FIREBASE_AUTH_DOMAIN=deciscope-app.firebaseapp.com
VITE_FIREBASE_PROJECT_ID=deciscope-app
VITE_FIREBASE_APP_ID=...
VITE_FIREBASE_STORAGE_BUCKET=deciscope-app.firebasestorage.app
VITE_FIREBASE_MESSAGING_SENDER_ID=...
```

`VITE_FIREBASE_API_KEY`, `VITE_FIREBASE_AUTH_DOMAIN`, `VITE_FIREBASE_PROJECT_ID`, and `VITE_FIREBASE_APP_ID` are required by the current web app.

## Backend Env

Create or update `deciscope-api\.env`.

```env
DECISCOPE_BACKEND_ADDR=100.70.221.61:9090
DECISCOPE_TRANSCRIPT_ONLY=false
PORT=9090
DATABASE_URL=postgres://deciscope:change-me-change-me-change-me-1234@localhost:5432/deciscope?sslmode=disable
DECISCOPE_TRANSCRIPT_STORE=postgres
DECISCOPE_INGEST_API_KEY=change-me-change-me-change-me-1234
DECISCOPE_BOT_CONTROL_URL=http://<VM_TAILSCALE_IP>:<PORT>/internal/bot/join
DECISCOPE_BOT_CONTROL_TOKEN=change-me-bot-control-token
DECISCOPE_BOT_CONTROL_TIMEOUT_SECONDS=10
UPLOAD_DIR=./uploads
ALLOWED_ORIGINS=http://localhost:5193

AUTH_PROVIDER=firebase
FIREBASE_PROJECT_ID=deciscope-app
GOOGLE_APPLICATION_CREDENTIALS=<path-to-service-account-json>
```

Use a path that matches your local checkout. You can also use `FIREBASE_CREDENTIALS_JSON` instead of `GOOGLE_APPLICATION_CREDENTIALS`, but keeping the service account in a separate ignored file is usually easier locally.

When the backend runs in Docker Compose, the API container receives these
Firebase environment variables from `.env`. If you use a service account file
path, make sure that path exists inside the container, or use
`FIREBASE_CREDENTIALS_JSON` for local Docker testing. The Firebase Web
`VITE_FIREBASE_*` values alone are not enough for backend login because
`POST /v1/auth/login` verifies the ID token with the Firebase Admin SDK.

## Local Flow

1. Start the backend.

```powershell
cd <backend-repo>
go run . migrate
go run . serve
```

2. Start the web app.

```powershell
cd <web-repo>
npm run dev
```

3. Open the configured frontend URL, normally `http://localhost:5193`.
4. Click `Microsoft でログイン`.
5. The web app calls `POST /v1/auth/login` with the Firebase ID token.
6. The backend verifies the token with Firebase Admin SDK.
7. The backend returns the verified identity and the linked local user ID when available.

## Notes

- The Firebase Web config is public client config. It is still better to keep it in `.env.local` per environment.
- The service account JSON is secret. Never commit it.
- If the backend logs that Firebase auth is disabled, the Admin SDK credential env is missing or invalid.
- `/v1/auth/me`, workspace routes, meeting routes, and WebSocket routes require
  the authentication middleware.
- PostgreSQL must be available when the backend starts. Authentication state is
  persisted in PostgreSQL.
