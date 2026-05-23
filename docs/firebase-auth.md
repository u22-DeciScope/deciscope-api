# Firebase Google Authentication

DeciScope currently uses Firebase in two places.

- Frontend: Firebase Web SDK opens the Google sign-in flow and obtains a Firebase ID token.
- Backend: Firebase Admin SDK verifies that ID token and returns the authenticated user.

The local MVP `/v1` meeting APIs still work without Firebase. Firebase is required for Google login and protected legacy `/api` routes.

## Firebase Console Setup

1. Open the Firebase console for the project.
2. In Authentication, enable the Google sign-in provider.
3. In Project settings, add or select a Web app.
4. Copy the Web app config values into `C:\U-22\deciscope-web\.env.local`.
5. In Project settings, Service accounts, generate a new private key for the Admin SDK.
6. Save the JSON file outside git, for example `C:\U-22\secrets\firebase-service-account.json`.
7. Point the backend `.env` to that JSON file.

## Frontend Env

Create `C:\U-22\deciscope-web\.env.local`.

```env
VITE_API_BASE_URL=http://localhost:8080
VITE_WS_BASE_URL=ws://localhost:8080

VITE_FIREBASE_API_KEY=...
VITE_FIREBASE_AUTH_DOMAIN=deciscope-2733c.firebaseapp.com
VITE_FIREBASE_PROJECT_ID=deciscope-2733c
VITE_FIREBASE_APP_ID=...
VITE_FIREBASE_STORAGE_BUCKET=deciscope-2733c.appspot.com
VITE_FIREBASE_MESSAGING_SENDER_ID=...
```

`VITE_FIREBASE_API_KEY`, `VITE_FIREBASE_AUTH_DOMAIN`, `VITE_FIREBASE_PROJECT_ID`, and `VITE_FIREBASE_APP_ID` are required by the current frontend.

## Backend Env

Create or update `C:\U-22\deciscope-core-api\.env`.

```env
PORT=8080
SQLITE_PATH=./db.sqlite
FIXTURE_DIR=./fixtures/meetings
UPLOAD_DIR=./uploads
ALLOWED_ORIGINS=http://localhost:5173

AUTH_PROVIDER=firebase
FIREBASE_PROJECT_ID=deciscope-2733c
GOOGLE_APPLICATION_CREDENTIALS=C:\U-22\secrets\firebase-service-account.json
```

You can also use `FIREBASE_CREDENTIALS_JSON` instead of `GOOGLE_APPLICATION_CREDENTIALS`, but keeping the service account in a separate ignored file is usually easier locally.

## Local Flow

1. Start the backend.

```powershell
cd C:\U-22\deciscope-core-api
go run .
```

2. Start the frontend.

```powershell
cd C:\U-22\deciscope-web
npm run dev
```

3. Open `http://localhost:5173`.
4. Click `Googleでログイン`.
5. The frontend calls `POST /login` with the Firebase ID token.
6. The backend verifies the token with Firebase Admin SDK.
7. Click `Backend認証を確認` to call `GET /api/me` with `Authorization: Bearer <idToken>`.

## Notes

- The Firebase Web config is public client config. It is still better to keep it in `.env.local` per environment.
- The service account JSON is secret. Never commit it.
- If the backend logs that Firebase auth is disabled, the Admin SDK credential env is missing or invalid.
