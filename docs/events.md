# リアルタイムイベント仕様

DeciScope の画面更新は、会議ごとのイベントを REST と WebSocket で受け取る設計です。

## WebSocket

```text
WS /v1/realtime?meeting_id={meeting_id}
WS /v1/realtime?meeting_id={meeting_id}&last_seq={seq}
```

接続後、クライアントは任意で次の `client.hello` を送れます。

```json
{
  "type": "client.hello",
  "meeting_id": "m_xxxxx",
  "last_seq": 12
}
```

サーバーは `last_seq` より後の durable event を送ってから、ライブ配信に移ります。URL クエリと `client.hello` の両方に `last_seq` がある場合は、`client.hello` の値を優先します。

## 共通形式

```json
{
  "type": "transcript.final",
  "meeting_id": "m_xxxxx",
  "seq": 3,
  "ts_ms": 1712345678901,
  "payload": {}
}
```

- `type`: イベント種別です。
- `meeting_id`: 会議 ID です。
- `seq`: durable event にだけ付きます。会議内で 1 から増加します。
- `ts_ms`: サーバー側でイベントを作成した UTC epoch milliseconds です。
- `payload`: イベント種別ごとの JSON payload です。

## Durable Event

以下は保存され、REST 取得と WebSocket 再接続時の catch-up 対象になります。

- `meeting.state`
- `transcript.final`
- `analysis.delta`
- `tree.update`
- `speaker.summary.delta`
- `report.ready`
- `error`

REST では次の API から取得できます。

```http
GET /v1/meetings/{meeting_id}/events?after_seq=0
```

## Ephemeral Event

```text
transcript.partial
```

`transcript.partial` は低遅延表示用です。保存しないため `seq` は付かず、REST 取得や再接続時の catch-up 対象にもなりません。

## Payload 例

### meeting.state

```json
{
  "status": "started",
  "recording": true,
  "analyzing": true,
  "participants": ["Speaker A", "Speaker B"]
}
```

現在使われる主な `status` は `created`, `started`, `ended` です。

### transcript.partial

```json
{
  "partial_id": "p_001",
  "speaker_label": "Speaker A",
  "text": "今日の議題は価格改定です",
  "start_ms": 1000
}
```

本番で Azure Speech などから中間認識結果を受ける場合、このイベントに変換して画面へ配信する想定です。

### transcript.final

```json
{
  "segment_id": "seg_001",
  "speaker_label": "Speaker A",
  "text": "今日の議題は価格改定です。対象顧客を決めたいです。",
  "start_ms": 1000,
  "end_ms": 4300
}
```

`transcript.final` は durable event として保存され、同時に `meeting_segments` に保存されます。`segment_id` や `speaker_label` が空の場合はサーバー側で補完します。

### analysis.delta

```json
{
  "items": [
    {
      "op": "add",
      "item": {
        "id": "an_001",
        "kind": "question",
        "severity": "medium",
        "title": "対象顧客の確認",
        "body": "価格改定の対象顧客がまだ明確ではありません。",
        "linked_segment_ids": ["seg_001"],
        "status": "open"
      }
    }
  ]
}
```

現在の mock レポート生成では、`kind` が `issue`, `risk`, `question` の item を Markdown に反映します。

### tree.update

```json
{
  "version": 1,
  "mode": "snapshot",
  "nodes": [
    { "id": "n_topic_price", "kind": "topic", "label": "価格改定" }
  ],
  "edges": []
}
```

議論構造ツリーを画面に反映するためのイベントです。

### speaker.summary.delta

```json
{
  "speaker_label": "Speaker B",
  "summary": {
    "claims": ["エンタープライズに限定すれば影響は限定的"],
    "questions": ["既存契約の更新時期を確認したい"],
    "todos": ["既存契約の更新タイミングを確認する"]
  }
}
```

話者ごとの要約差分です。

### report.ready

```json
{
  "artifact_id": "art_xxxxx",
  "format": "markdown"
}
```

レポート生成が完了したことを通知します。本文は次の API で取得します。

```http
GET /v1/meetings/{meeting_id}/report
Accept: text/markdown
```

### error

```json
{
  "code": "fixture_parse_failed",
  "message": "invalid character ...",
  "retryable": false
}
```

fixture replay や将来の STT / 分析処理で発生したエラーを画面へ通知するためのイベントです。
