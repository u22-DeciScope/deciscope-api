# Realtime Events

## WebSocket

```text
WS /v1/realtime?meeting_id={meeting_id}
```

接続後、クライアントは任意で以下を送ります。

```json
{
  "type": "client.hello",
  "meeting_id": "m_xxxxx",
  "last_seq": 12
}
```

サーバーは `last_seq` より後の durable event を送ってからライブ配信に移ります。

## Common Shape

```json
{
  "type": "transcript.final",
  "meeting_id": "m_xxxxx",
  "seq": 3,
  "ts_ms": 1712345678901,
  "payload": {}
}
```

## Durable Events

以下はSQLiteに保存され、REST取得とWebSocket再接続復旧の対象です。

- `meeting.state`
- `transcript.final`
- `analysis.delta`
- `tree.update`
- `speaker.summary.delta`
- `report.ready`
- `error`

## Ephemeral Events

- `transcript.partial`

`transcript.partial` は保存せず、接続中のクライアントにだけ配信します。`seq` は付与しません。
