# REST API

このドキュメントは、現在の `deciscope-core-api` が提供している HTTP API の一覧です。

ベース URL:

```text
http://localhost:8080
```

`/v1` 配下はローカル MVP0 用の DeciScope 本体 API です。現在はローカル開発を優先して認証なしで利用できます。Firebase 認証が必要なのは、Google ログイン用の `/login` と、既存互換の `/api` 配下です。

## 動作確認

```http
GET /health
GET /v1/health
GET /debug
```

- `GET /health` は legacy の疎通確認で、本文に `ok` を返します。
- `GET /v1/health` は JSON で `status` と現在時刻を返します。
- `GET /debug` は最小のブラウザ検証画面を返します。会議作成、WebSocket 接続、fixture replay、イベント取得、発話取得、Markdown レポート取得を画面から確認できます。

## 会議

```http
GET  /v1/meetings
POST /v1/meetings
GET  /v1/meetings/{meeting_id}
POST /v1/meetings/{meeting_id}/join-token
POST /v1/meetings/{meeting_id}/end
```

会議作成リクエスト:

```json
{
  "title": "価格改定会議",
  "source": "fixture_replay"
}
```

- `title` が空の場合は `Untitled meeting` になります。
- `source` が空の場合は `fixture_replay` になります。
- 作成時に `meeting.state` イベントが保存・配信されます。
- `join-token` はローカル開発用のダミートークンを返します。
- `end` は会議を終了状態にし、Markdown レポートを生成して `report.ready` を保存・配信します。

## イベントと発話

```http
GET /v1/meetings/{meeting_id}/events?after_seq=0
GET /v1/meetings/{meeting_id}/segments?after_seq=0
```

- `events` は durable event のみを返します。
- `segments` は `transcript.final` から保存された発話セグメントを返します。
- `after_seq` を指定すると、その `seq` より後のデータだけを取得できます。
- `transcript.partial` は低遅延配信用の ephemeral event なので保存されず、REST 取得や再接続時の catch-up 対象にもなりません。

イベント仕様の詳細は [events.md](./events.md) を参照してください。

## リアルタイム配信

```text
WS /v1/realtime?meeting_id={meeting_id}
WS /v1/realtime?meeting_id={meeting_id}&last_seq={seq}
```

WebSocket 接続後、クライアントは任意で `client.hello` を送れます。サーバーは `last_seq` より後の durable event を再送してから、ライブイベント配信に移ります。

## Fixture 再生

```http
GET  /v1/fixtures
POST /v1/meetings/{meeting_id}/replay/start
POST /v1/meetings/{meeting_id}/replay/pause
POST /v1/meetings/{meeting_id}/replay/resume
POST /v1/meetings/{meeting_id}/replay/reset
```

Replay 開始リクエスト:

```json
{
  "fixture": "demo.jsonl"
}
```

- fixture 名が空の場合は `demo.jsonl` が使われます。
- fixture は `FIXTURE_DIR` 配下の `.jsonl` ファイルです。
- `start` は既存の同一会議 replay があれば停止してから新しく開始します。
- `pause` / `resume` は実行中の replay を一時停止・再開します。
- `reset` は会議のイベント、発話、レポートを削除し、会議状態を `created` に戻します。

## レポート

```http
GET /v1/meetings/{meeting_id}/report
```

通常は JSON で `artifact_id`, `meeting_id`, `format`, `content`, `created_at` を返します。

`Accept: text/markdown` を指定した場合は Markdown 本文を返します。まだレポートが保存されていない場合は、現在の発話と分析イベントから Markdown レポートを生成して保存します。

## アップロードとジョブ

```http
POST /v1/uploads
GET  /v1/jobs/{job_id}
```

`POST /v1/uploads` は `multipart/form-data` の `file` フィールドを受け取ります。MVP0 ではファイルをローカルの `UPLOAD_DIR` に保存し、`file.extract_audio` の mock job を完了状態にします。実際の音声抽出、STT、LLM 分析はまだ実装していません。

## Firebase / 既存互換 API

```http
POST /login
GET  /api/me
POST /api/login
GET  /api/health
POST /register
GET  /register-form
```

- `POST /login` と `POST /api/login` は Firebase ID token を受け取ります。
- `/api/*` は Firebase 認証ミドルウェアの対象です。Firebase が無効なローカル環境では `Authorization: Bearer dev:<uid>` を使えます。
- `POST /api/login` は互換用に残っていますが、通常の Google ログイン確認では認証なしの `POST /login` を使ってください。
- `POST /register` は JSON の `name`, `email`, `password` を受け取り、SQLite の legacy user table に保存します。SQLite が使えない環境では `503` を返します。
- `GET /register-form` は古い疎通確認用の簡易 HTML です。現在の主な検証には `/debug` を使ってください。

Firebase 設定の詳細は [firebase-auth.md](./firebase-auth.md) を参照してください。

## エラー形式

`/v1` API の多くは、エラー時に次のような JSON を返します。

```json
{
  "error": {
    "code": "not_found",
    "message": "resource not found"
  }
}
```
