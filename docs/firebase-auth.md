# Firebase Microsoft Authentication

DeciScope currently uses Firebase in two places.

- Web: Firebase Web SDK opens the Microsoft sign-in flow and obtains a Firebase ID token.
- Backend: Firebase Admin SDK verifies that ID token and returns the authenticated user.

The local MVP `/v1` meeting APIs still work without Firebase. Firebase is required for Microsoft login and protected legacy `/api` routes.

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
VITE_API_BASE_URL=http://localhost:8080
VITE_WS_BASE_URL=ws://localhost:8080

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
PORT=8080
SQLITE_PATH=./db.sqlite
FIXTURE_DIR=./fixtures/meetings
UPLOAD_DIR=./uploads
ALLOWED_ORIGINS=http://localhost:5173

AUTH_PROVIDER=firebase
FIREBASE_PROJECT_ID=deciscope-app
GOOGLE_APPLICATION_CREDENTIALS=<path-to-service-account-json>
```

Use a path that matches your local checkout. You can also use `FIREBASE_CREDENTIALS_JSON` instead of `GOOGLE_APPLICATION_CREDENTIALS`, but keeping the service account in a separate ignored file is usually easier locally.

## Local Flow

1. Start the backend.

```powershell
cd <backend-repo>
go run .
```

2. Start the web app.

```powershell
cd <web-repo>
npm run dev
```

3. Open `http://localhost:5173`.
4. Click `Microsoft でログイン`.
5. The web app calls `POST /login` with the Firebase ID token.
6. The backend verifies the token with Firebase Admin SDK.
7. The web app syncs the signed-in Microsoft account to the backend with `POST /login`.

## Notes

- The Firebase Web config is public client config. It is still better to keep it in `.env.local` per environment.
- The service account JSON is secret. Never commit it.
- If the backend logs that Firebase auth is disabled, the Admin SDK credential env is missing or invalid.
