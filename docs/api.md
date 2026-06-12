# REST API

このドキュメントは、現在の `deciscope-core-api` が提供しているHTTP APIの一覧です。

既定のベースURL:

```text
http://localhost:9090
```

## ヘルスチェックと認証

```http
GET  /v1/health
POST /v1/auth/login
GET  /v1/auth/me
GET  /v1/auth/health
```

- `GET /v1/health` はJSONで `status` と現在時刻を返します。
- `POST /v1/auth/login` は `idToken` を受け取り、Firebase認証結果を返します。
- `/v1/auth/me` と `/v1/auth/health` は認証ミドルウェアの対象です。
- Firebaseが無効なローカル環境では、保護Routeで
  `Authorization: Bearer dev:<uid>` を使用できます。
- 現在、認証必須なのは `/v1/auth/me` と `/v1/auth/health` です。会議、fixture、
  upload、WebSocket Routeは認証必須ではありません。

`/health`、`/debug`、`/login`、`/api/*`、`/register` は現在のRouterには
登録されていません。

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
- 作成時に `meeting.state` eventが保存・配信されます。
- `join-token` はローカル開発用のダミートークンを返します。
- `end` は会議を終了状態にし、Markdown reportを生成して
  `report.ready` を保存・配信します。

## イベントと発話

```http
GET /v1/meetings/{meeting_id}/events?after_seq=0
GET /v1/meetings/{meeting_id}/segments?after_seq=0
```

- `events` はdurable eventのみを返します。
- `segments` は `transcript.final` から保存された発話Segmentを返します。
- `after_seq` を指定すると、その `seq` より後のデータだけを取得できます。
- `transcript.partial` はephemeral eventなので保存・catch-up対象になりません。

イベント仕様の詳細は [events.md](./events.md) を参照してください。

## リアルタイム配信

```text
WS /v1/realtime?meeting_id={meeting_id}
WS /v1/realtime?meeting_id={meeting_id}&last_seq={seq}
```

WebSocket接続後、Clientは任意で `client.hello` を送れます。Serverは
`last_seq` より後のdurable eventを再送してからlive event配信に移ります。

## Fixture再生

```http
GET  /v1/fixtures
POST /v1/meetings/{meeting_id}/replay/start
POST /v1/meetings/{meeting_id}/replay/pause
POST /v1/meetings/{meeting_id}/replay/resume
POST /v1/meetings/{meeting_id}/replay/reset
```

Replay開始リクエスト:

```json
{
  "fixture": "demo.jsonl"
}
```

- fixture名が空の場合は `demo.jsonl` が使われます。
- fixtureは `FIXTURE_DIR` 配下の `.jsonl` fileです。
- `start` は既存の同一会議Replayを停止してから開始します。
- `pause` / `resume` は実行中のReplayを一時停止・再開します。
- `reset` は会議のEvent、Segment、Reportを削除し、状態を `created` に戻します。

## レポート

```http
GET /v1/meetings/{meeting_id}/report
```

通常はJSONで `artifact_id`, `meeting_id`, `format`, `content`, `created_at`
を返します。`Accept: text/markdown` の場合はMarkdown本文を返します。保存済みの
Reportがない場合は、現在のSegmentと分析Eventから生成して保存します。

## アップロードとジョブ

```http
POST /v1/uploads
GET  /v1/jobs/{job_id}
```

`POST /v1/uploads` は `multipart/form-data` の `file` fieldを受け取ります。
現在はfileをlocalの `UPLOAD_DIR` に保存し、`file.extract_audio` のmock jobを
完了状態にします。実際の音声抽出、STT、LLM分析は未実装です。

## エラー形式

`/v1` APIの多くは、エラー時に次のJSONを返します。

```json
{
  "error": {
    "code": "not_found",
    "message": "resource not found"
  }
}
```

認証HandlerとMiddlewareの一部は、現在plain text errorを返します。
