# Firebase Google 認証

現在の DeciScope では、Firebase を Google ログイン検証に使います。

- フロントエンド: Firebase Web SDK で Google ログインを行い、Firebase ID token を取得します。
- バックエンド: Firebase Admin SDK で ID token を検証し、認証済みユーザー情報を返します。

ローカル MVP0 の `/v1` 会議 API は Firebase なしでも動きます。Firebase が必要なのは、Google ログイン用の `/login` と、認証付き legacy API の `/api` 配下です。

## Firebase Console 設定

1. Firebase Console で対象プロジェクトを開きます。
2. Authentication で Google sign-in provider を有効化します。
3. Project settings で Web app を追加または選択します。
4. Web app config の値をフロントエンドの `.env.local` に設定します。
5. Project settings の Service accounts で Admin SDK 用の秘密鍵 JSON を生成します。
6. 秘密鍵 JSON は git 管理外に保存します。例: `C:\U-22\secrets\firebase-service-account.json`
7. バックエンドの `.env.local` または `.env` から、その JSON を参照します。

## フロントエンド環境変数

`C:\U-22\deciscope-web\.env.local` の例:

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

現在のフロントエンドでは、少なくとも `VITE_FIREBASE_API_KEY`, `VITE_FIREBASE_AUTH_DOMAIN`, `VITE_FIREBASE_PROJECT_ID`, `VITE_FIREBASE_APP_ID` が必要です。

## バックエンド環境変数

`C:\U-22\deciscope-core-api\.env.local` または `.env` の例:

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

バックエンドは Firebase 認証情報を次の順で参照します。

1. `FIREBASE_CREDENTIALS_JSON`: サービスアカウント JSON 文字列。
2. `FIREBASE_SERVICE_ACCOUNT_JSON`: サービスアカウント JSON ファイルパス。
3. `GOOGLE_APPLICATION_CREDENTIALS`: サービスアカウント JSON ファイルパス。
4. `AUTH_PROVIDER=firebase` かつ上記がない場合は、`FIREBASE_PROJECT_ID` だけで初期化を試みます。ただしローカルではサービスアカウント JSON を指定する方が確実です。

`AUTH_PROVIDER=firebase` を指定せず、認証情報もない場合は Firebase Auth を無効化して起動します。この場合でも `/v1` API は利用できます。

## ローカル確認手順

1. バックエンドを起動します。

```powershell
cd C:\U-22\deciscope-core-api
go run .
```

2. フロントエンドを起動します。

```powershell
cd C:\U-22\deciscope-web
npm run dev
```

3. `http://localhost:5173` を開きます。
4. `Googleでログイン` を実行します。
5. フロントエンドが Firebase ID token を取得し、`POST /login` に送ります。
6. バックエンドが Firebase Admin SDK で token を検証します。
7. `GET /api/me` を `Authorization: Bearer <idToken>` 付きで呼び、バックエンド認証を確認します。

## 開発用認証

Firebase が設定されていないローカル環境でも、`/api` 配下は次のヘッダーで通せます。

```text
Authorization: Bearer dev:local-user
```

この開発用認証は `/api/me` などの protected legacy API の疎通確認用です。`POST /login` は Firebase ID token の検証を行うため、Firebase Auth クライアントが必要です。

## 注意点

- Firebase Web config は公開クライアント設定ですが、環境ごとに `.env.local` で管理してください。
- サービスアカウント JSON は秘密情報です。絶対に commit しないでください。
- バックエンドログに `firebase auth disabled` が出る場合、Firebase Admin SDK の認証情報が未設定または無効です。
- `.env.local` は `.env` より後に読み込まれるため、同じ環境変数がある場合は `.env.local` が優先されます。
