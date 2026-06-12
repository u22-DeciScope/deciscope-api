# ローカル開発

## 起動

```powershell
go run .
```

既定では `http://localhost:9090` で起動します。

```powershell
$env:PORT="18080"
go run .
```

起動時は `.env` を読み込み、その後 `.env.local` で上書きします。

## データベース設定

```env
DATABASE_DRIVER=sqlite
DATABASE_URL=./db.sqlite
```

- `DATABASE_DRIVER`: 現在は `sqlite` を指定します。
- `DATABASE_URL`: SQLiteファイルパスです。
- `SQLITE_PATH` と `AUTH_SQLITE_PATH` は互換用のフォールバックです。
- 未指定の場合は `./db.sqlite` を使用します。

接続生成は `internal/infrastructure/database` の `database.Open`、
スキーマ更新は埋め込みMigrationを実行する `database.Migrate` が担当します。
SQLite向けSQLは `internal/adapter/repository/sqlite` に隔離されています。

SQLiteを開けない環境では、会議APIはMemory Repositoryへフォールバックします。
この場合、永続化はされませんがfixture replayとAPI確認は実行できます。

## その他の環境変数

```env
PORT=9090
FIXTURE_DIR=./fixtures/meetings
UPLOAD_DIR=./uploads
FRONTEND_URL=http://localhost:5193
ALLOWED_ORIGINS=http://localhost:5193
```

- `FIXTURE_DIR`: fixture JSONLディレクトリ。
- `UPLOAD_DIR`: mock uploadの保存先。
- `FRONTEND_URL`: CORSの基準origin。未指定時は `http://localhost:5193`。
- `ALLOWED_ORIGINS`: CORS許可originのカンマ区切り。

## 動作確認

まずヘルスチェックを確認します。

```http
GET http://localhost:9090/v1/health
```

現在、ブラウザ用の `/debug` 画面は提供していません。以下のAPIとWebSocketを
使ってfixture replay、durable event、transcript segment、Markdown reportを
確認します。

## 手動クイックデモ

会議を作成します。

```http
POST http://localhost:9090/v1/meetings
Content-Type: application/json

{
  "title": "Demo meeting",
  "source": "fixture_replay"
}
```

返された`id`でWebSocketへ接続します。

```text
ws://localhost:9090/v1/realtime?meeting_id={meeting_id}
```

接続後、必要に応じて `client.hello` を送信できます。

```json
{
  "type": "client.hello",
  "meeting_id": "{meeting_id}",
  "last_seq": 0
}
```

fixture replayを開始します。

```http
POST http://localhost:9090/v1/meetings/{meeting_id}/replay/start
Content-Type: application/json

{
  "fixture": "demo.jsonl"
}
```

イベント、発話、レポートを確認します。

```http
GET http://localhost:9090/v1/meetings/{meeting_id}/events?after_seq=0
GET http://localhost:9090/v1/meetings/{meeting_id}/segments?after_seq=0
GET http://localhost:9090/v1/meetings/{meeting_id}/report
Accept: text/markdown
```

## テスト

```powershell
go test ./...
go vet ./...
```

Repository契約テストはMemoryとSQLiteへ同じスイートを実行します。
ApplicationとHTTP HandlerはFake Port・Fake Use Caseでテストします。
Fixture、Realtime、サーバー結合、依存方向のテストも実行されます。

WindowsのApplication Controlが自動生成されたtest exeを拒否する環境では、
対象Packageを固定名でbuildして実行すると検証できます。

```powershell
go test -c -o .gotmp/realtime-check.exe ./internal/adapter/realtime
.\.gotmp\realtime-check.exe -test.v
```
