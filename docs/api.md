# REST API

Base URL:

```text
http://localhost:8080
```

## Health

```http
GET /v1/health
```

## Meetings

```http
GET  /v1/meetings
POST /v1/meetings
GET  /v1/meetings/{meeting_id}
POST /v1/meetings/{meeting_id}/join-token
POST /v1/meetings/{meeting_id}/end
```

Create request:

```json
{
  "title": "価格改定会議",
  "source": "fixture_replay"
}
```

## Events And Segments

```http
GET /v1/meetings/{meeting_id}/events?after_seq=0
GET /v1/meetings/{meeting_id}/segments?after_seq=0
```

`events` は durable event のみを返します。`transcript.partial` は低遅延配信用の ephemeral event なので保存されません。

## Fixture Replay

```http
GET  /v1/fixtures
POST /v1/meetings/{meeting_id}/replay/start
POST /v1/meetings/{meeting_id}/replay/pause
POST /v1/meetings/{meeting_id}/replay/resume
POST /v1/meetings/{meeting_id}/replay/reset
```

Replay start request:

```json
{
  "fixture": "demo.jsonl"
}
```

## Report

```http
GET /v1/meetings/{meeting_id}/report
```

JSONで `artifact_id`, `format`, `content` を返します。`Accept: text/markdown` の場合はMarkdown本文を返します。

## Uploads And Jobs

```http
POST /v1/uploads
GET  /v1/jobs/{job_id}
```

`POST /v1/uploads` は multipart form の `file` フィールドを受けます。MVP0ではローカル保存し、mock job を完了状態にします。
