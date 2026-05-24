# DeciScope Core API Backend Foundation

このディレクトリは、DeciScope の要件をもとにしたローカルMVP0向けバックエンド基盤の実装メモです。

## 実装方針

- Azure、Teams Bot、外部STT、外部LLMには接続しない。
- Go単一プロセスで REST API、WebSocket Gateway、Meeting Runtime、fixture replay を動かす。
- SQLite をローカル永続ストアとして使う。`go-sqlite3` が使えない環境では `/v1` はインメモリストアに自動フォールバックする。
- `transcript.partial` は配信のみ、`transcript.final` や分析系イベントは durable event として保存する。
- WebSocket 再接続時は `last_seq` より後の durable event を再送する。

## 追加した主なパッケージ

- `internal/core`
  - 会議、イベント、発話セグメント、ジョブ、レポートの型と保存処理。
  - durable event に会議内単調増加 `seq` を付与する。
- `internal/realtime`
  - 会議 room ごとの WebSocket 配信。
  - `client.hello` の `last_seq` を使った catch-up。
- `internal/fixture`
  - JSONL fixture の疑似リアルタイム再生。
  - start / pause / resume / reset を提供。
- `internal/handlers`
  - `/v1` REST API。

## ローカルデータ

- DB: `./db.sqlite`（SQLiteが使えない場合はインメモリ）
- fixture: `./fixtures/meetings/demo.jsonl`
- upload: `./uploads`

環境変数で変更できます。

```text
PORT=8080
SQLITE_PATH=./db.sqlite
FIXTURE_DIR=./fixtures/meetings
UPLOAD_DIR=./uploads
ALLOWED_ORIGINS=http://localhost:5173,http://localhost:3000
```

## 認証について

MVP0 の DeciScope 本体 API (`/v1`) はローカル開発を優先して認証なしで動きます。

既存の `/api` 配下は従来の Firebase 認証ルートを残していますが、Firebase 設定がないローカル環境でもサーバー起動を止めないようにしています。開発用には以下を使えます。

```text
Authorization: Bearer dev:local-user
```

## MVP0で未実装のもの

- 実音声入力
- 実STT
- 実LLM分析
- Redis Streams / PostgreSQL / Object Storage
- Teams Media Adapter
- 本格的なユーザー・会議権限
