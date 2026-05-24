# DeciScope Core API バックエンド基盤

このドキュメントは、現在の `deciscope-core-api` の実装方針と構成をまとめたものです。現状はローカル MVP0 向けで、Azure、Teams Bot、外部 STT、外部 LLM にはまだ接続していません。

## 現在の役割

- Go 単一プロセスで REST API、WebSocket 配信、会議ランタイム、fixture replay を動かす。
- `/v1` API は認証なしで動くローカル開発用の DeciScope 本体 API として扱う。
- legacy の `/login` と `/api` 配下は Firebase Google ログイン検証用として残す。
- SQLite をローカル永続ストアとして使う。
- `go-sqlite3` が使えない環境では `/v1` はインメモリストアへ自動フォールバックする。
- `/debug` でブラウザからバックエンドの主要フローを確認できる。

## 主なパッケージ

- `internal/core`
  - 会議、イベント、発話セグメント、ジョブ、アップロード、レポートの型と保存処理。
  - durable event に会議内で単調増加する `seq` を付与する。
  - `transcript.final` から発話セグメントを保存する。
- `internal/realtime`
  - 会議 room ごとの WebSocket 配信。
  - `last_seq` または `client.hello.last_seq` を使って durable event を catch-up する。
- `internal/fixture`
  - JSONL fixture の疑似リアルタイム再生。
  - `start` / `pause` / `resume` / `reset` を提供する。
- `internal/handlers`
  - `/v1` REST API、legacy 認証 API、`/debug` 検証画面を提供する。
- `internal/firebase`
  - Firebase Admin SDK の初期化と ID token 検証用クライアントを提供する。

## イベントモデル

現在の中核は「入力をイベント化して保存・配信する」設計です。

- `transcript.partial`
  - 低遅延表示用の ephemeral event。
  - 保存しないため `seq` は付きません。
  - WebSocket 接続中のクライアントにだけ配信されます。
- `transcript.final`
  - durable event として保存します。
  - 同時に `meeting_segments` に発話セグメントとして保存します。
- `analysis.delta` / `tree.update` / `speaker.summary.delta`
  - 分析・可視化用の durable event として保存します。
- `meeting.state` / `report.ready` / `error`
  - 会議状態、レポート生成、エラー通知用の durable event として保存します。

この設計は、本番で Azure Speech からリアルタイム認識結果を受け取る場合も、入力アダプタを差し替えれば活かせる想定です。

## ローカルデータ

既定値:

```text
DB:      ./db.sqlite
fixture: ./fixtures/meetings/demo.jsonl
upload:  ./uploads
```

環境変数で変更できます。

```text
PORT=8080
SQLITE_PATH=./db.sqlite
FIXTURE_DIR=./fixtures/meetings
UPLOAD_DIR=./uploads
ALLOWED_ORIGINS=http://localhost:5173,http://localhost:3000
```

`SQLITE_PATH` が空の場合は `AUTH_SQLITE_PATH` も参照します。どちらも空の場合は `./db.sqlite` を使います。

## 認証

MVP0 の `/v1` API はローカル開発を優先して認証なしで動きます。

既存互換の `/api` 配下は Firebase 認証ミドルウェアの対象です。Firebase が設定されていないローカル環境でもサーバー起動は止めず、開発用には次のヘッダーを使えます。

```text
Authorization: Bearer dev:local-user
```

`POST /login` は Firebase ID token を検証します。SQLite が利用できる場合は legacy user table にユーザーを自動登録し、SQLite が利用できない場合は `user_store: "none"` でログイン結果を返します。

## 本番化で差し替わる想定の部分

- `internal/fixture`
  - 本番では Azure Speech などのリアルタイム STT セッション管理に置き換わる想定です。
- SQLite
  - 複数インスタンスや本番運用では PostgreSQL などの共有 DB が必要になります。
- `internal/realtime`
  - 現在はプロセス内 Hub です。水平スケールする場合は Redis Streams、Pub/Sub、Service Bus などの共有配信基盤が必要になります。
- mock upload/job
  - 実音声抽出、STT、分析ワーカー、成果物保存は未実装です。

## MVP0 で未実装のもの

- 実音声入力
- Azure Speech / Teams Media Adapter 連携
- 実 LLM 分析
- Redis Streams / PostgreSQL / Object Storage
- 本格的なユーザー・会議権限
- 本番向け監査ログ、レート制限、観測性
