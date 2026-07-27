# リアルタイムイベント仕様

DeciScope の画面更新は、会議ごとのイベントを REST と WebSocket で受け取る設計です。

このドキュメントは `/v1/realtime` (meeting_id ベース) のイベントを扱います。Teams Bot
会議セッションの `transcript_segment.created` / `meeting_session.status_changed` /
`ai_analysis.updated` / `meeting_session.bot_health_changed` イベントは
`/v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/transcript-stream` で
配信され、詳細は [api.md](./api.md) を参照してください。

## AI snapshotの新旧契約

`ai_analysis.updated`と会議分析REST snapshotは、`analysisVersion`、
payload内の`treeVersion`、`updatedAtUtc`の順で新旧を判定します。
agenda reconciliationや手動progress overrideによりpayloadだけを補正する場合は、
`analysisVersion`と`treeVersion`を増やさず、サーバーが直前のlive projectionより
最低1ミリ秒新しい`updatedAtUtc`を付けます（ブラウザの`Date`比較精度に合わせた
契約です）。同一version・同一時刻の再送は
同一snapshotとして扱い、クライアントは採用しません。

したがって、WebSocketで補正済みsnapshotを採用した後に古いREST応答が到着しても
巻き戻りません。補正保存はanalysis historyを追加せず、保存・配信は各補正passで
1回だけです。クライアントはversion判定を弱めず、payload欠損やstatus-only更新では
最後の正常なtreeとagenda progressを保持します。

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
        "kind": "issue",
        "subtype": "question",
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

現在のmockレポート生成では、`decision`を決定事項へ、未解決の`risk`と
`issue`（subtypeを含む）をリスク・未解決事項へMarkdown出力します。

### tree.update

```json
{
  "version": 1,
  "mode": "snapshot",
  "nodes": [
    {
      "id": "topic-price-stable",
      "kind": "topic",
      "label": "価格改定",
      "status": "open",
      "description": "価格改定の対象と時期について整理している。",
      "relatedItemIds": ["an_001"],
      "agendaRefs": ["agenda-1"],
      "materialized": true
    }
  ],
  "edges": []
}
```

議論構造ツリーを画面に反映するためのイベントです。

ノードの `kind` は `topic` / `group` / `issue` / `risk` / `fact` / `decision` /
`todo` を使います。`issue` は `subtype`（`discussion` / `confirmation` / `question` /
`investigation`）で意味を細分化し、`status`（`open` / `updated` / `resolved`）とは分離します。
`group` はAIアシスタントカードではなく、agenda/dynamic topic配下で2件以上のdetail itemを
まとめる表示専用の中間ノードです。フロントエンドの議論ツリー（`DiscussionTree`）は
この語彙で色分けします。

live analysisのcanonical構造は通常 `root → topic → group → subgroup → detail`（soft limit 4）です。materialized/dynamic topicは必要に応じて別topic配下へreparentできます。十分な直接detailがある場合だけgroupをもう1段追加でき、hard limit 5を超えません。detailはtopic/group直下に置けますが、別detailの親にはできません。各nodeの表示親は`parentId`で一意に決まり、`edges`は`parentId`から導出されます。一子groupは2 version連続で不足した場合に平坦化し、ライブ更新の作成・削除振動を抑えます。

会議前アジェンダは`agendaAnchors`に独立した論理記録として保持され、`planned` / `materialized` / `discussed` / `merged` / `not_discussed`のstatusを持ちます。アジェンダ入力だけではtopicを作りません。根拠itemがある場合だけ、agenda IDとは異なる安定した`topic-*` node IDでtopicをmaterializeします。topic側の`agendaRefs`とanchor側の`materializedTopicIds`が双方向の関連を表し、ID値の一致や`agenda-` prefixは関連判定に使いません。`mergedFromNodeIds`はtopic統合履歴、`materialized`は明示的な表示状態です。一つのagendaを複数topicへ意図的に分割した場合だけ、各topicは同じ`agendaSplitGroupId`を持ちます。この明示情報がない複数materializeはintegrity違反です。会議終了時に未議論anchorは`not_discussed`となり、空のmaterialized topicは除去されます。

live analysisが未知parent、dynamic candidate、unclassified stagingを返した場合は、
dynamic topicを確定する前に全予定アジェンダのtitle、description、goal、
semantic hintsとfinal transcript/item evidenceを決定的に再照合します。
強く一意な一致だけがagenda topicへ入り、予定順序を飛び越す明示遷移では直前4 sequenceの
根拠itemを同じ基準で再検証します。終了時にも未着手anchorを全final transcriptと最終treeで
再検証し、採用時はprogressだけでなく`agendaRefs`とitem parentも修復します。
owner/adminの手動statusは別レコードに保存され、配信時の`manualStatus` /
`effectiveStatus`が自動算出した`computedStatus`より優先されます。

`status` は任意で、live analysis では `open` / `updated` / `resolved` を使います。`description` はノード内容を短く説明する任意フィールド、`relatedItemIds` は関連する `analysis.delta` / live analysis `items` のid配列です。live analysisでは存在しないitem idはサーバー側で除外されますが、解消済みitem idは `resolved` カードへの関連として保持されます。

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
  "code": "catchup_failed",
  "message": "failed to load missed events",
  "retryable": true
}
```

WebSocket再接続時のcatch-up失敗など、実行時エラーを画面へ通知するためのイベントです。
