# REST API

このドキュメントは、現在の `deciscope-core-api` が提供しているHTTP APIの一覧です。

別端末から接続する場合のベースURL例:

```text
http://<PC_TAILSCALE_IP>:9090
```

## ヘルスチェックと認証

```http
GET  /v1/health
GET  /healthz
GET  /readyz
POST /v1/auth/login
GET  /v1/auth/me
POST /v1/auth/logout
PUT  /v1/session/current-workspace
```

- `GET /v1/health` はJSONで `status` と現在時刻を返します。
- `GET /healthz` はGoプロセスが応答可能な場合にJSONで `status` を返します。
- `GET /readyz` は現在の永続化先へPingし、接続できない場合は `503` を返します。
- `POST /v1/auth/login` は `idToken` を受け取り、Firebase認証結果を返します。
- `/v1/auth/me`、`/v1/auth/logout`、workspace配下、meeting配下、WebSocketは
  認証ミドルウェアの対象です。
- Firebaseが無効なローカル環境では、保護Routeで
  `Authorization: Bearer dev:<uid>` を使用できます。

`/health`、`/debug`、`/login`、`/register` は現在のRouterには
登録されていません。

## Workspace

```http
GET    /v1/workspaces
GET    /v1/workspaces/{workspace_code}
PATCH  /v1/workspaces/{workspace_code}
GET    /v1/workspaces/{workspace_code}/members
PATCH  /v1/workspaces/{workspace_code}/members/{member_id}
DELETE /v1/workspaces/{workspace_code}/members/{member_id}
GET    /v1/workspaces/{workspace_code}/invitations
POST   /v1/workspaces/{workspace_code}/invitations
DELETE /v1/workspaces/{workspace_code}/invitations/{invitation_id}
```

- すべて認証必須です。`{workspace_code}` 配下はさらにworkspaceへのアクセス権が必要です。
- `PATCH /` (workspace名更新)、members/invitationsの変更系は、workspaceの
  admin/ownerロールが必要です。
- `RemoveMember` はworkspace member削除時に、そのmemberの既存接続を切断します。

## EchoBot文字起こし取り込み

```http
POST /api/v1/transcript-segments
Content-Type: application/json
X-DeciScope-Api-Key: <shared secret>
```

```json
{
  "sessionId": "session_...",
  "eventId": "06008080-91e3-4b88-a8ff-9af629265ced:1",
  "callId": "06008080-91e3-4b88-a8ff-9af629265ced",
  "sequenceNo": 1,
  "speakerId": "8:orgid:xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
  "speakerName": "佐藤さん",
  "recognizedAtUtc": "2026-06-25T13:20:01.1234567+00:00",
  "offsetTicks": 20300000,
  "durationTicks": 18000000,
  "text": "本日の会議を開始します。"
}
```

- API keyは `DECISCOPE_INGEST_API_KEY` と定数時間比較します。
- `sessionId` は任意です。会議セッション作成APIから返ったIDを付けると、
  保存・履歴取得・WebSocket配信で同じセッションに紐づけられます。
- `speakerId` と `speakerName` は任意です。Botが話者別文字起こし情報を送った場合、
  PostgreSQLの `speaker_id` / `speaker_name` に保存し、履歴取得・WebSocket配信にも含めます。
- 互換性のため、表示名は `speakerLabel`, `speakerDisplayName`, `speaker_label`,
  `participantName`, `userName` でも受け付け、`speakerName` として扱います。
- `recognizedAtUtc` はUTC offsetのみ受け付け、保存時はUTC/RFC3339Nanoへ正規化します。
- 新規保存は `201 Created`、同一内容の再送は `200 OK` と `duplicate: true` です。
- 同じ `eventId` の内容違い、または同じ `callId` + `sequenceNo` の別 `eventId` は
  `409 Conflict` です。
- 標準構成ではPostgreSQLの `transcript_segments` tableへ保存します。
- body上限は64KiBです。

履歴取得:

```http
GET /api/v1/transcript-segments?callId=<call_id>&sessionId=<session_id>&limit=100
```

- `callId` が指定された場合はそのcallだけを返します。未指定の場合は全callIdを返します。
- `sessionId` が指定された場合はその会議セッションだけを返します。
- `limit` の既定値は `100`、上限は `500` です。
- `sequenceNo` 昇順で返します。
- `DECISCOPE_WS_CLIENT_TOKEN` が設定されている場合は `?token=...` または
  `Authorization: Bearer ...` が必要です。
- `DECISCOPE_INGEST_API_KEY` は読み取りAPIやブラウザへ渡しません。

文字起こしリアルタイム配信:

```text
WS /api/v1/ws/transcript-segments
WS /api/v1/ws/transcript-segments?callId=<call_id>
WS /api/v1/ws/transcript-segments?sessionId=<session_id>
WS /api/v1/ws/transcript-segments?callId=<call_id>&sessionId=<session_id>&token=<client_token>
```

- DB保存に成功した新規segmentだけを `transcript_segment.created` として配信します。
- 同一内容の再送は `duplicate: true` のHTTP応答になりますが、WebSocketへは配信しません。
- `callId` 指定時は、そのcallIdのsegmentだけを受信します。未指定なら全callIdを受信します。
- `sessionId` 指定時は、その会議セッションのsegmentとstatusだけを受信します。
- 会議セッションの状態変化は `meeting_session.status_changed` として配信します。
- `DECISCOPE_WS_CLIENT_TOKEN` が設定されている場合は `token` queryが必要です。
- Originは `DECISCOPE_WS_ALLOWED_ORIGINS` で許可します。未設定時はlocal development originだけを許可します。

WebSocket message:

```json
{
  "type": "transcript_segment.created",
  "sentAtUtc": "2026-06-27T00:00:00Z",
  "data": {
    "sessionId": "session_...",
    "eventId": "09005080-cce6-4132-9404-1e823df47ff9:6",
    "callId": "09005080-cce6-4132-9404-1e823df47ff9",
    "sequenceNo": 6,
    "speakerId": "8:orgid:xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    "speakerName": "佐藤さん",
    "recognizedAtUtc": "2026-06-27T00:00:00Z",
    "offsetTicks": 287000000,
    "durationTicks": 41200000,
    "text": "うーんってことは、まあ一旦大丈夫そうかな。",
    "duplicate": false
  }
}
```

会議セッション状態変更:

```json
{
  "type": "meeting_session.status_changed",
  "sentAtUtc": "2026-06-27T00:00:00Z",
  "data": {
    "sessionId": "session_...",
    "status": "joined",
    "botCallId": "09005080-cce6-4132-9404-1e823df47ff9"
  }
}
```

AI分析更新（`sessionId` 指定クライアントにのみ配信。`callId` のみのクライアントには配信しません）:

```json
{
  "type": "ai_analysis.updated",
  "sentAtUtc": "2026-06-27T00:00:00Z",
  "data": {
    "sessionId": "session_...",
    "analysisType": "live",
    "status": "completed",
    "version": 4,
    "payload": {
      "summary": "議論全体のこれまでの要約",
      "currentTopic": "現在の主なトピック",
      "items": [
        {
          "id": "risk-db-migration",
          "kind": "risk",
          "severity": "high",
          "title": "移行中のダウンタイム",
          "body": "DB移行作業でダウンタイムが発生する懸念。",
          "status": "open"
        }
      ],
      "tree": {
        "nodes": [
          { "id": "topic-db", "kind": "topic", "label": "DBマイグレーション" },
          { "id": "risk-db-migration", "kind": "risk", "label": "ダウンタイム懸念" }
        ],
        "edges": [
          { "source": "topic-db", "target": "risk-db-migration" }
        ]
      }
    },
    "model": "gpt-4o-mini",
    "updatedAtUtc": "2026-06-27T00:00:00Z",
    "intervalSeconds": 10
  }
}
```

- `analysisType` は `live`（会議中）または `final`（会議終了時）です。
- `status` が `failed` の場合は `error` フィールドが追加されます。`payload` は直近の成功結果を保持します
- AI機能が無効な場合、このイベントは配信されません
- `intervalSeconds` はライブ分析のチェック間隔（秒）です。`analysisType` が `live` のとき設定され、
  「次回更新まで約N秒」の表示に使えます。`final` では付きません
- ライブ分析の生成開始時には、`status: "running"` のイベントを1回配信します（version/payloadは
  現在値のまま）。これはWebSocket配信のみのephemeralな通知で、DBには保存されないため
  `GET .../ai-analyses` には現れません
- live分析の内部動作: モデルは各ラウンドで差分（新規・変化したitem、追加・変化したtreeノード、
  新規edge、解消済みidの `resolvedIds`）のみを申告し、サーバーが前回状態へ決定論的にマージします。
  保存・配信されるpayloadは常にマージ後の完全な状態なので、クライアントは差分を意識する必要はありません
- live payloadの `items[].kind` は `issue | question | risk | decision | todo`、
  `severity` は `low | medium | high`、`status` は `open`（新規）| `updated`（更新）です。
  itemsは最大30件で、超過時は最も古いitemから除去されます
- 解消・回答・完了した論点は、モデルが `resolvedIds`（解消済みitemのid配列）で申告します。
  `resolvedIds` はモデル→サーバー間の指示用フィールドで、サーバーが該当itemと同じidのtreeノード・
  そのノードに接続するedgeを除去した後にクリアするため、保存・配信されるpayloadには現れません
- `tree` は現在の議論構造で、ノードの `kind`（`topic | issue | question | risk | decision`）は
  `tree.update` イベント（[events.md](./events.md)）と同じ語彙です。ノードは最大12個で、
  超過時はtopicノードを残して最も古い非topicノードから除去されます（接続edgeも連動除去）。
  対応するitemがあるノードはitemと同じ `id` を共有します。ノードが無い場合 `tree` は `null` です。
  topicノードが無い場合は `currentTopic` から `topic-current` ノードをサーバー側で補完し、
  親を持たないノードを `topic-current` に接続します

## Teams Bot会議セッション

フロントエンドはVM Botを直接呼びません。Teams会議URLをGo APIへ登録し、
Go APIがTailscale内のVM Bot制御APIへ参加命令を送ります。

```http
POST /api/v1/meeting-sessions
Content-Type: application/json
```

```json
{
  "joinUrl": "https://teams.microsoft.com/l/meetup-join/..."
}
```

成功時:

```json
{
  "sessionId": "session_...",
  "status": "command_sent"
}
```

- `joinUrl` は必須です。`https://teams.microsoft.com/...meetup-join...`
  などTeams会議URLらしいものだけ受け付けます。
- `joinUrl` 全文とBot制御tokenはログに出しません。ログには `sessionId` と
  `joinUrlHash` を使います。
- `DECISCOPE_BOT_CONTROL_URL` または `DECISCOPE_BOT_CONTROL_TOKEN` が未設定の場合は
  `503 bot_control_not_configured` を返します。
- Bot制御APIの4xx/5xx/timeout時は `meeting_sessions.status=failed` と
  `last_error` を保存し、HTTPでは `502 bot_control_command_failed` を返します。

取得:

```http
GET /api/v1/meeting-sessions/{sessionId}
```

Botからの状態更新:

```http
PATCH /api/v1/bot/meeting-sessions/{sessionId}/status
Content-Type: application/json
X-DeciScope-Api-Key: <shared secret>
```

```json
{
  "status": "joined",
  "botCallId": "09005080-cce6-4132-9404-1e823df47ff9",
  "message": "joined successfully"
}
```

`status` は `pending_join`, `command_sent`, `joining`, `joined`, `recording`,
`ended`, `failed` のいずれかです。失敗時は `message` が `lastError` として保存されます。

Botからのmetadata更新:

```http
PATCH /api/v1/bot/meeting-sessions/{sessionId}/metadata
Content-Type: application/json
X-DeciScope-Api-Key: <shared secret>
```

会議名などTeams会議のmetadataだけを更新します（statusは変更しません）。

保守/デバッグ用（`X-DeciScope-Api-Key` が必要）:

```http
GET  /api/v1/debug/meeting-sessions?limit=100
POST /api/v1/meeting-sessions/cleanup-stale
```

- `debug/meeting-sessions` は直近のセッション一覧を返す調査用APIです。
- `cleanup-stale` は放置されたセッションを `stale` 状態に遷移させ、対象件数を返します。

Workspace配下の会議セッション（認証必須、フロントエンドはこちらを使用）:

```http
GET  /v1/workspaces/{workspace_code}/meeting-sessions
POST /v1/workspaces/{workspace_code}/meeting-sessions
GET  /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}
POST /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/end
GET  /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/transcript-segments
GET  /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/transcript-stream
GET  /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/ai-analyses
```

- `POST`（作成）と `POST .../end`（終了）はworkspaceのadmin/ownerロールが必要です。
- `transcript-segments` は保存済みSegmentの取得、`transcript-stream` はWebSocketでの
  リアルタイム配信です。
- `ai-analyses` は最新のライブ分析・最終要約を返します。存在しない分析は `null` で、
  `404` にはなりません。AI機能が未設定の場合も `live`/`final` はともに `null` です

```json
{
  "sessionId": "session_...",
  "live": {
    "analysisType": "live",
    "status": "completed",
    "version": 4,
    "payload": {
      "summary": "議論全体のこれまでの要約",
      "currentTopic": "現在の主なトピック",
      "items": [
        { "id": "risk-db-migration", "kind": "risk", "severity": "high", "title": "移行中のダウンタイム", "body": "DB移行作業でダウンタイムが発生する懸念。", "status": "open" }
      ],
      "tree": {
        "nodes": [
          { "id": "topic-db", "kind": "topic", "label": "DBマイグレーション" },
          { "id": "risk-db-migration", "kind": "risk", "label": "ダウンタイム懸念" }
        ],
        "edges": [
          { "source": "topic-db", "target": "risk-db-migration" }
        ]
      }
    },
    "model": "gpt-4o-mini",
    "updatedAtUtc": "2026-06-27T00:00:00Z"
  },
  "final": null,
  "liveIntervalSeconds": 10
}
```

- `liveIntervalSeconds` はライブ分析のチェック間隔（秒）です。AI機能またはライブ分析が
  無効の場合は `0` になります
- 非workspace版の `GET /api/v1/meeting-sessions/{session_id}/transcript-segments`
  （認証なし、`{session_id}` のみで絞り込み）も引き続き利用できます。

## 会議

```http
GET  /v1/workspaces/{workspace_code}/meetings
POST /v1/workspaces/{workspace_code}/meetings
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

## レポート

```http
GET /v1/meetings/{meeting_id}/report
```

通常はJSONで `artifact_id`, `meeting_id`, `format`, `content`, `created_at`
を返します。`Accept: text/markdown` の場合はMarkdown本文を返します。保存済みの
Reportがない場合は、現在のSegmentと分析Eventから生成して保存します。

## アップロードとジョブ

```http
POST /v1/workspaces/{workspace_code}/uploads
GET  /v1/jobs/{job_id}
```

`POST /v1/workspaces/{workspace_code}/uploads` は `multipart/form-data` の
`file` fieldを受け取ります。現在はfileをlocalの `UPLOAD_DIR` に保存し、
`file.extract_audio` のmock jobを完了状態にします。実際の音声抽出、STT、LLM分析は未実装です。

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
