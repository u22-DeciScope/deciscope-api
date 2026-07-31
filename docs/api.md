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
- 保護された `/v1` Routeは、`/v1/auth/login` が発行する
  `deciscope_session` Cookieで認証します。現在の実装にdev bearer fallbackはありません。
  ローカルでブラウザUIを使う場合もFirebase Admin設定を用意してください。
- `POST /api/v1/transcript-segments` と `/api/v1/meeting-sessions` などのVM Bot/互換ルートは、
  session Cookieではなく `X-DeciScope-Api-Key` で保護します。ブラウザ向けのレガシー
  transcript GET/WSルートは登録されません。

`/health`、`/debug`、`/login`、`/register` は現在のRouterには
登録されていません。

## クライアント診断

```http
POST /internal/client-diagnostics
```

- `DECISCOPE_CLIENT_DIAGNOSTICS_ENABLED=true` のときだけ登録されます。
- `deciscope_session` Cookieによる認証、workspace所属、`sessionId` がその
  workspaceに属することの検査を行います。
- `workspaceId`、`sessionId`、1件以上の`events`を含むJSONを受け取り、
  `accepted`、`rejected`、`suppressed`、`reasons`を`202 Accepted`で返します。
- 議論ツリーの描画観測には`tree_render_state`、`tree_render_anomaly`、
  `tree_render_recovery`を使用します。`details`にはcanonical/store/filter/layout/
  React Flow/DOMのnode・edge件数、container/ResizeObserver寸法、viewport、
  layout・commit状態、LKG復旧結果を含められます。異常と復旧イベントは
  高頻度抑制の対象外で、JSONLとAPIの構造化標準出力の両方へ記録されます。
- リクエスト上限、保存先、ローテーション、保持期間は
  [configuration.md](./configuration.md) のクライアント診断設定を参照してください。

## Workspace

```http
GET    /v1/workspaces
POST   /v1/workspaces
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
- ログイン時のワークスペース自動作成・固定デモワークスペースへの自動参加は廃止しました。
  所属0件のユーザーは `GET /v1/workspaces` で空配列を受け取り、フロントエンドが
  ワークスペース作成画面 (`/workspaces/new`) へ誘導します。
- `POST /v1/workspaces` は `{ "name": "...", "description": "..." }` を受け取り、
  作成者を owner として `workspace_members` に登録します。`name` 空文字は `400` です。
  所属0件のユーザーによる最初の作成時のみ、サンプル会議を1件投入します
  (`DECISCOPE_CREATE_SAMPLE_MEETING_ON_FIRST_WORKSPACE`、development 既定で有効)。
  サンプル会議の作成に失敗してもワークスペース作成自体は成功します。
- `PATCH /` (workspace更新)は `name` / `description` を個別に更新できます
  (JSONに含めたフィールドだけ更新)。admin/ownerロールが必要です。
- members/invitationsの変更系は admin/owner ロールが必要です。
  ただし `PATCH /members/{member_id}` (ロール変更)だけは owner のみ実行できます。
  owner 自身のロール変更・owner の削除は常に拒否されるため、owner は最低1人残ります。
- `RemoveMember` はworkspace member削除時に、そのmemberの既存接続
  (`/v1/realtime` とworkspace経由のtranscript WebSocket) を切断します。
  以後のAPI・WebSocketアクセスはリクエストごとの membership チェックで拒否されます。

## Workspace招待 (メール招待)

```http
POST   /v1/workspaces/{workspace_code}/invitations   (owner/admin)
GET    /v1/workspaces/{workspace_code}/invitations   (owner/admin)
DELETE /v1/workspaces/{workspace_code}/invitations/{invitation_id} (owner/admin)
GET    /v1/invitations/preview?token=...             (認証不要)
POST   /v1/invitations/accept                        (認証必須)
```

- `POST /invitations` は `{ "email": "...", "role": "viewer|admin" }` を受け取り、
  pending 招待を作成して招待リンクを通知します。owner ロールは指定できません (`400`)。
  既にメンバーのメールアドレスは `409`、同じメール宛の pending 招待がある場合も `409` です。
- 招待リンクは `{FRONTEND_URL}/invitations/accept?token=<生token>` 形式で、有効期限は72時間です。
  DBには生tokenを保存せず、SHA-256の `token_hash` のみ保存します。
  `token_hash` はいかなるAPIレスポンスにも含めません。
- 招待リンクの通知に失敗した場合、作成した招待は削除 (rollback) され `500` を返します。
- `GET /v1/invitations/preview` は承認前確認用にワークスペース名・招待先メール・ロール・
  status・有効期限のみ返します (会議情報・メンバー一覧などの機密情報は返しません)。
- `POST /v1/invitations/accept` は `{ "token": "..." }` を受け取り、
  ログイン中ユーザーの正規化済みメールアドレスと招待先メールの一致を必須とします。
  結果: 不一致 `403` / token不正 `404` / 期限切れ・取り消し済み `410` / 使用済み `409`。
  成功時は `workspace_members` に追加し、招待を `accepted` に更新して参加ログを出力します。
- ログインによる自動参加 (旧仕様) は廃止しました。参加は招待リンクの明示的な承諾のみです。

招待リンク通知の設定:

```env
DECISCOPE_ENV=development
```

- `DECISCOPE_ENV=development` (既定) では dev fallback として招待URL
  (生tokenを含む) をログに出力します。productionではログに出さず、通知基盤がないため
  招待作成は失敗します。

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

レガシーの `GET /api/v1/transcript-segments`、
`GET /api/v1/meeting-sessions/{session_id}/transcript-segments`、
`WS /api/v1/ws/transcript-segments` は登録されません。履歴取得とリアルタイム配信は、
後述のworkspace-scoped APIをSession Cookieで利用してください。

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
          {
            "id": "risk-db-migration",
            "kind": "risk",
            "label": "ダウンタイム懸念",
            "status": "open",
            "description": "DB移行中にサービス停止が発生する可能性を確認している。",
            "relatedItemIds": ["risk-db-migration"]
          }
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
- live分析の内部動作: モデルは各ラウンドで差分（新規・変化したitem、親topicの提案、
  解消済みidの `resolvedIds`）のみを申告し、サーバーが前回状態へ決定論的にマージします。
  保存・配信されるpayloadは常にマージ後の完全な状態なので、クライアントは差分を意識する必要はありません
- live payloadの `items[].kind` は `issue | risk | fact | decision | todo` です。
  `kind: "issue"` の場合は `subtype` が `discussion | confirmation | question | investigation`
  のいずれかになり、論点・確認事項・質問・調査事項を表します。`severity` は
  `low | medium | high`、`status` は `open`（新規）| `updated`（更新）|
  `resolved`（解決済）です。未解決/解決済みはkindではなくstatusで表現します。
  旧payloadの `open_issue` / `question` / `confirmation` / `investigation` は、DB migrationと
  読み出し時互換処理によりcanonicalな `issue + subtype` へ変換されます。
- `items[].informationStatus` は `grounded | tentative` です。主題を発言から復元できない
  substantiveなissueは即時削除せず `tentative` で保持し、後続発言で主題が確定した時点で
  同じcanonical itemを `grounded` へ更新します。
  未解決（`status` が `resolved` 以外）のitemと解決済み（`status: "resolved"`）のitemは
  それぞれ独立に最大50件までで、超過時は各区分ごとに最も古いitemから除去されます
  （解決済みitemが未解決itemの流入で追い出されること、およびその逆はありません）
- `items[].evidenceSequenceNos` は、そのitemを直接裏付けた発言のsequence番号をJSON整数で保持します。
  モデル互換のため受信時はnumeric stringも整数へ正規化しますが、不正文字列・小数・当該ラウンドに
  実在しないsequenceは値単位で除外し、保存・配信時は整数だけになります
- live extraction v18以降では、モデル出力の各itemに`evidenceSnippets`を必須とし、引用した
  final transcript sequence内に正規化後も実在して中心命題を支持する短い抜粋だけを保存します。
  `evidenceSequenceNos`が構造上正しくても、subject / predicate / entity / qualifierが発言に
  groundingされなければ表示可能itemにはなりません。事前入力、agenda metadata、semantic hints、
  existing treeは分類・親候補には利用しますが、detail itemの一次証拠にはなりません
- live extraction v19以降では、完了した作業を`fact`、担当・期限・commitmentのある将来行動を
  `todo`として区別します。対象物の満了日・失効日は作業期限として扱わず、同じ発言に状態・
  将来悪影響・対策が含まれる場合は、それぞれ`fact`・`risk`・`todo`へ分離します。
  IssueとTodoは相互上書きせず、担当・期限・証拠sequenceは該当する行動節だけから引き継ぎます
- 担当者（話者本人を含む）・具体的な将来行動・行動対象・commitmentが同一節に揃う場合は、
  モデル結果とは独立した決定論的safety netでも`todo`を補完します。1 sequenceあたり3件を
  上限とし、候補密度の異常は本文なしの構造化ログへ記録します。方針の採用・必須化・
  適用開始は担当作業ではなく`decision`として分離します。低情報発話と同一batchにある後続Todoも
  対象で、追加のAI呼び出しは行いません。明示訂正の置換Factが一時的にgroundingされない場合は
  旧Itemをtentative/inactive候補として退避し、final repairで置換を再構築して旧Itemと
  tombstoneを監査可能な形で保持します
- `items[].groundingDecision`（`accepted | rewritten`）、
  `groundingConfidence`、`groundingSourceTypes`、`groundingUnsupportedAtoms`は任意の
  サーバー所有監査メタデータです。`groundingUnsupportedAtoms`には本文ではなくcategory付きhashを
  格納します。`createdThroughSequenceNo`と`initialEvidenceMaxSequenceNo`は、同一roundの別文や
  split fragmentを後続証拠と誤認しないための生成時点情報です。すべてadditiveかつ`omitempty`で、
  旧payload・旧クライアントとの契約は変わりません
- 解消・回答・完了した論点は、モデルが `resolvedIds`（解消済みitemのid配列）で申告します。
  `resolvedIds` はモデル→サーバー間の指示用フィールドで、サーバーが該当itemと同じidのtreeノードを
  `status: "resolved"` にした後にクリアするため、保存・配信されるpayloadには現れません
- `tree` は現在の議論構造です。通常は最大深さ4の
  `root → topic(agenda/dynamic) → group → subgroup → detail item` を使います。
  3件以上の直接detailという強い根拠がある場合だけ深さ5を許可し、絶対に深さ5を超えません。
  深くできるのはgroupだけで、detail同士の親子化は禁止です。groupを作る根拠が2件未満なら
  detail itemをtopic/group直下に置きます。ノードの `kind`
  （`topic | group | issue | risk | fact | decision | todo`）は
  `tree.update` イベント（[events.md](./events.md)）と同じ語彙です。topicノードと未解決
  （`status` が `resolved` 以外）の非topicノードは合わせて最大36個で、超過時はtopicノードを
  残して最も古い非topicノードから除去されます。解決済み（`status: "resolved"`）の非topicノードは
  これとは別枠で最大36個までで、超過時は最も古い解決済みノードから除去されます（解決済みノードが
  未解決ノードの流入で追い出されることはありません）。除去されたノードへの接続edgeも連動除去されます。
  対応するitemがあるノードはitemと同じ `id` を共有します。ノードが無い場合 `tree` は `null` です。
  topicノードが無い場合は `currentTopic` から `topic-current` ノードをサーバー側で補完し、
  topicノードがある場合も親を持たない非topicノードを主topicへ接続します
- `tree.nodes[].description` は任意の短い説明文です。サーバー側で前後空白を除去し、長すぎる場合は
  切り詰めます。`tree.nodes[].relatedItemIds` は関連する `items[].id` の配列です。存在しないitem idや
  重複idはサーバー側で除外されます。既存互換のため、ノードidがitem idと一致する場合は
  `relatedItemIds` が空でも関連カードとして扱えます。`tree.nodes[].status` は任意で、
  `resolved` の場合もノードは削除されず、解決済みとして残ります。issueノードの
  `subtype` は対応itemと同じ値です
- `tree.relations[]` は表示親を増やさない任意の意味関係です。`source` / `target` は
  canonical item IDで、`kind` は現在 `mitigates`（todoがriskを緩和）、
  `addresses`（todoがissueへ対応）、`supports`（factがissueを裏付ける）を使います。
  旧payloadと未対応クライアントでは省略可能で、`parentId` / `edges`の契約は変わりません
- `items[].relatedAgendaIds` は横断agendaへの副次的な参照です。canonicalな親は
  `tree.nodes[].parentId` の1つだけで、`relatedAgendaIds` から複数親edgeは作りません。
  `agendaRole: "action_summary"` のagenda nodeは、activeなTODO・未解決itemを
  `relatedItemIds` でも参照できるため、内容別topicの所属を失わず横断表示できます。
  action-summary agendaが事前入力に無い会議では、activeで分類済みのTODO・未解決itemに
  参照専用ID `action-summary-fallback` を付けます。これはagenda anchorやtree nodeではなく、
  canonical itemを複製しないAction Summary projectionのview keyです。resolved・inactive・
  tentative・unclassified itemはfallback projectionに含めません
- `treeVersion` はtreeを生成したlive analysis versionです。`treeChanges` はそのversionで
  サーバーが算出した構造差分で、`newNodeIds` / `updatedNodeIds` / `reparentedNodeIds` /
  `resolvedNodeIds` / `promotedNodeIds` を必要なものだけ含みます。旧payloadでは省略されます
- tree auditorが検証済みpatchを適用したversionでは、任意の
  `changeSource: "tree_auditor"`, `auditRunId`, `basedOnTreeVersion` が追加されます。
  `treeChanges.source` / `treeChanges.auditRunId`も同じ由来を示します。旧クライアントは無視できます
- tree auditorの`rename_topic`は既存topicの`label`だけを安全に更新します。
  node `id`、`parentId`、children、`agendaRefs`は維持され、更新したtopic IDは
  `treeChanges.updatedNodeIds`に含まれます

## Teams Bot会議セッション

フロントエンドはVM Botを直接呼びません。Teams会議URLをGo APIへ登録し、
Go APIがTailscale内のVM Bot制御APIへ参加命令を送ります。通常のブラウザUIは
この後に記載するworkspace配下の会議セッションAPIを使い、次の `/api/v1` ルートは
VM Bot連携や手動確認向けの互換APIとして残しています。

```http
POST /api/v1/meeting-sessions
Content-Type: application/json
X-DeciScope-Api-Key: <shared secret>
```

- 非workspace版の作成・取得・終了 (`POST /api/v1/meeting-sessions`、
  `GET /api/v1/meeting-sessions/{sessionId}`、`POST .../{sessionId}/end`) は
  `DECISCOPE_INGEST_API_KEY` が必須です。ブラウザからは
  `/v1/workspaces/{workspace_code}/meeting-sessions` (認証 + role チェックあり) を使ってください。

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
- 参加命令は設定した `DECISCOPE_BOT_CONTROL_URL`（通常
  `/internal/bot/join`）へ送ります。終了命令は同じURLの末尾の`/join`を除き、
  `/internal/bot/meeting-sessions/{sessionId}/end`へ送ります。Go APIホストから
  Botホストへのfirewall/proxyでは両方のパスを許可してください。

取得 (APIキー必須):

```http
GET /api/v1/meeting-sessions/{sessionId}
X-DeciScope-Api-Key: <shared secret>
```

Botからの状態更新:

```http
PATCH /api/v1/bot/meeting-sessions/{sessionId}/status
Content-Type: application/json
X-DeciScope-Api-Key: <shared secret>
```

```json
{
  "status": "ended",
  "botCallId": "09005080-cce6-4132-9404-1e823df47ff9",
  "message": "transcript queue drained",
  "lastFinalSequenceNo": 27,
  "transcriptQueueDrained": true
}
```

`status` は `pending_join`, `command_sent`, `joining`, `joined`, `recording`,
`speech_throttled`, `speech_error`, `ending`, `ended`, `failed` のいずれかです。
Speech認識の一時停止時は `speech_throttled` または `speech_error` として保存され、
復帰時はBotが `recording` を再送します。失敗時やSpeech停止時の詳細は `lastError` に保存されます。
終了時は、BotがSpeech認識を停止して転送queueをdrainした後、可能なら
`lastFinalSequenceNo` と `transcriptQueueDrained: true` を付けて `ended` を通知します。
APIは通知されたsequenceがDBへ到着するまで（既定最大10秒）待ち、未処理finalを抽出してから
最終tree・summaryを保存します。これらの任意フィールドを送らない旧Botでは、DBの静穏判定へfallbackします。

Botからのmetadata更新:

```http
PATCH /api/v1/bot/meeting-sessions/{sessionId}/metadata
Content-Type: application/json
X-DeciScope-Api-Key: <shared secret>
```

会議名などTeams会議のmetadataだけを更新します（statusは変更しません）。

Botからのハートビート:

```http
POST /api/v1/bot/meeting-sessions/{sessionId}/heartbeat
Content-Type: application/json
X-DeciScope-Api-Key: <shared secret>
```

```json
{
  "botCallId": "09005080-cce6-4132-9404-1e823df47ff9"
}
```

- BotがVM上で生存している間、定期的に（既定20秒間隔）呼び出します。ボディは省略可・未知フィールド許容です。
- `last_bot_status_at` と `updated_at` を現在時刻に更新するだけで、**statusは変更せず、WebSocket配信も行いません**
  （高頻度に飛んでくるため、全クライアントへ毎回配信するとスパムになります）。
- terminal状態（`ended` / `failed` / `stale`）のセッションは更新されず、現在のセッションをそのまま200で返します
  （終了済みセッションを誤って復活させないため）。存在しないセッションは404です。
- Bot接続の生死判定・自動終了は次項の常駐watchdogが行います。

Bot接続死活監視（常駐watchdog）:

- Go APIは常駐goroutineで、status が `joined` / `active` / `recording` / `speech_error` /
  `speech_throttled` / `ending` のいずれかで `last_bot_status_at` が記録済みのセッションを定期的に走査します。
- `last_bot_status_at` からの経過が `DECISCOPE_SESSION_BOT_LOST_AFTER_SECONDS`
  （既定60秒）以上になった時点で、WebSocketで `meeting_session.bot_health_changed`
  （`healthy: false`）を1回だけ配信します（遷移時のみ）。ハートビートが再開して閾値を下回ると
  `healthy: true` を1回だけ配信します。
- さらに `DECISCOPE_SESSION_BOT_END_AFTER_SECONDS`（既定180秒）以上ハートビートが
  途絶した場合、セッションを `endReason: "bot_unresponsive"` として自動的に `ended` にします
  （VM Botのプロセス即死・強制停止でBotからの通知が届かないケースに対応するため）。
- 環境変数:
  - `DECISCOPE_SESSION_WATCHDOG_ENABLED`（既定 `true`）
  - `DECISCOPE_SESSION_WATCHDOG_INTERVAL_SECONDS`（既定15、最小5）
  - `DECISCOPE_SESSION_BOT_LOST_AFTER_SECONDS`（既定60、最小30）
  - `DECISCOPE_SESSION_BOT_END_AFTER_SECONDS`（既定180。`LOST_AFTER` 以下の値を指定した場合は
    自動的に補正されます）
- **設定上の注意（footgun）**: Bot側のハートビート送信間隔 `DECISCOPE_BOT_HEARTBEAT_SECONDS`
  （既定20秒、EchoBot側の環境変数）は、`DECISCOPE_SESSION_BOT_LOST_AFTER_SECONDS` の
  1/3以下に設定することを推奨します。送信間隔を `LOST_AFTER`（既定60秒）以上にすると、
  正常に稼働中でも喪失/復旧の `bot_health_changed` がフラッピングします。さらに送信間隔を
  `END_AFTER`（既定180秒）以上にすると、正常に進行中の会議が誤って自動終了します。
- **旧EchoBotとの組み合わせに関する注意**: ハートビート未対応の旧バージョンのEchoBotと、
  watchdog有効（`DECISCOPE_SESSION_WATCHDOG_ENABLED=true`）の本APIを組み合わせると、
  ハートビートが一切届かないため会議中のセッションが `DECISCOPE_SESSION_BOT_END_AFTER_SECONDS`
  （既定約3分）で誤って自動終了します。デプロイ時はBot側のハートビート対応版を先行してリリースするか、
  切替期間中は `DECISCOPE_SESSION_WATCHDOG_ENABLED=false` にしてください。

WebSocketイベント形式（[events.md](./events.md) と同じ配信経路。`sessionId` で購読しているクライアントにのみ届きます）:

```json
{
  "type": "meeting_session.bot_health_changed",
  "sentAtUtc": "2026-07-07T00:01:00.000000000Z",
  "data": {
    "sessionId": "session_...",
    "healthy": false,
    "lastBotStatusAtUtc": "2026-07-07T00:00:00.000000000Z"
  }
}
```

文字起こしの流入状態が変化した場合は、Botの生死とは別に次を配信します。
`transcriptHealth` は通常 `ok` / `transcript_delayed` /
`transcript_stalled` で、Botの音声メトリクスが利用できる場合は `silent` /
`audio_stalled` / `speech_stalled` に細分類されます。

```json
{
  "type": "meeting_session.transcript_health_changed",
  "sentAtUtc": "2026-07-07T00:01:30.000000000Z",
  "data": {
    "sessionId": "session_...",
    "transcriptHealth": "transcript_stalled",
    "secondsSinceLastTranscript": 90
  }
}
```

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
GET  /v1/workspaces/{workspace_code}/meeting-sessions/final-summaries
POST /v1/workspaces/{workspace_code}/meeting-sessions
GET  /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}
POST /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/end
DELETE /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/
GET  /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/transcript-segments
GET  /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/transcript-stream
GET  /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/ai-analyses
PATCH /v1/workspaces/{workspace_code}/meeting-sessions/{session_id}/agenda-progress
```

- `POST`（作成）、`POST .../end`（終了）、`DELETE`、`PATCH .../agenda-progress`
  はworkspaceのadmin/ownerロールが必要です。
- `final-summaries` はworkspace内の完了済み最終要約を一括取得し、
  `{"items":[{"sessionId":"...","overview":"..."}]}` を返します。最終要約がない
  セッションは含まれず、AI分析が無効な場合は空配列です。
- `POST .../end` はまずセッションを `ending` にしてBotへ終了コマンドを送ります。Botのdrain通知後に
  final flush → tree再編成/snapshot → summaryの順で処理し、完了後にだけ `ended` へ進みます。
  Botへの終了コマンドがタイムアウト・エラーになった場合も、DB静穏判定を使ってfinalizationを開始し、
  最終的には `ended` として終了します（不完全時は`lastError`と`finalization`分析に記録）。
  Bot制御が未設定（`503 bot_control_not_configured`）の場合のみ終了に失敗します。
- `DELETE` は `ended` / `failed` / `stale` の終了済みセッションだけを、その
  transcriptとAI分析を含めて完全削除し、`204 No Content`を返します。進行中は`400`です。
- `transcript-segments` は保存済みSegmentの取得、`transcript-stream` はWebSocketでの
  リアルタイム配信です。どちらもSession Cookie認証とworkspace所属検査を通ります。
- `ai-analyses` は最新のライブ分析・最終要約・durable tree・finalization進行状態を返します。
  存在しない分析は `null` で、`404` にはなりません。`finalization.payload` にはtarget sequence、
  tree/summaryのcoverage、timeout、retry回数、不完全終了の有無が入ります。
- `agenda-progress` は手動上書きを1操作ずつ受け付けます。本文は
  `{"entryId":"agenda-1","manualStatus":"discussed"}`（`null`で解除）、または
  `{"manualCurrentTopicId":"agenda-1"}`（`null`で解除）のどちらか一方です。
  応答は更新後の`agendaProgress`です。

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
          {
            "id": "risk-db-migration",
            "kind": "risk",
            "label": "ダウンタイム懸念",
            "status": "open",
            "description": "DB移行中にサービス停止が発生する可能性を確認している。",
            "relatedItemIds": ["risk-db-migration"]
          }
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
- `end` は会議を終了状態にし、`meeting.state` eventを保存・配信します。
  レスポンスは保存されたeventの一覧 (`events`) です。

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
