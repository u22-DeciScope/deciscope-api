# ローカル開発

## 起動

```powershell
go run .
```

既定では `http://localhost:8080` で起動します。`PORT` を指定するとポートを変更できます。

```powershell
$env:PORT="18080"
go run .
```

起動時には `.env` を読み込み、その後に `.env.local` を上書き読み込みします。同じ環境変数がある場合は `.env.local` が優先されます。

## 推奨の動作確認

ブラウザで次を開きます。

```text
http://localhost:8080/debug
```

`Run full check` ボタンを押すと、以下の流れをまとめて確認できます。

1. `/v1/health` の疎通確認。
2. fixture 一覧の取得。
3. 会議作成。
4. WebSocket 接続。
5. fixture replay 開始。
6. durable event と transcript segment の取得。
7. Markdown レポートの取得。

## 手動クイックデモ

1. 会議を作成します。

```http
POST http://localhost:8080/v1/meetings
Content-Type: application/json

{
  "title": "Demo meeting",
  "source": "fixture_replay"
}
```

2. 返ってきた `id` で WebSocket に接続します。

```text
ws://localhost:8080/v1/realtime?meeting_id={meeting_id}
```

接続後、必要に応じて次の hello を送ります。

```json
{
  "type": "client.hello",
  "meeting_id": "{meeting_id}",
  "last_seq": 0
}
```

3. fixture replay を開始します。

```http
POST http://localhost:8080/v1/meetings/{meeting_id}/replay/start
Content-Type: application/json

{
  "fixture": "demo.jsonl"
}
```

4. イベントと発話を確認します。

```http
GET http://localhost:8080/v1/meetings/{meeting_id}/events?after_seq=0
GET http://localhost:8080/v1/meetings/{meeting_id}/segments?after_seq=0
```

5. レポートを確認します。

```http
GET http://localhost:8080/v1/meetings/{meeting_id}/report
Accept: text/markdown
```

## curl 例

PowerShell では `curl` が `Invoke-WebRequest` のエイリアスになることがあります。必要に応じて `curl.exe` を使ってください。

```powershell
curl.exe -X POST http://localhost:8080/v1/meetings -H "Content-Type: application/json" -d "{\"title\":\"Demo meeting\",\"source\":\"fixture_replay\"}"
```

## REST Client 例

VS Code の REST Client を使う場合は、リポジトリ直下の `DeciScope_API_Test.http` から主要 API を呼び出せます。`{{meeting_id}}` は、会議作成レスポンスの `id` に置き換えてください。

## よく使う環境変数

```env
PORT=8080
SQLITE_PATH=./db.sqlite
FIXTURE_DIR=./fixtures/meetings
UPLOAD_DIR=./uploads
ALLOWED_ORIGINS=http://localhost:5173
```

- `SQLITE_PATH`: SQLite ファイルパスです。未指定の場合は `AUTH_SQLITE_PATH`、それも未指定なら `./db.sqlite` を使います。
- `FIXTURE_DIR`: fixture JSONL のディレクトリです。
- `UPLOAD_DIR`: mock upload の保存先です。
- `ALLOWED_ORIGINS`: CORS 許可 origin のカンマ区切りです。未指定の場合は `FRONTEND_URL`、localhost、127.0.0.1 をローカル向けに許可します。

## SQLite が使えない場合

`go-sqlite3` が使えない環境では、`/v1` API はインメモリストアにフォールバックします。この場合でも `/debug` と fixture replay の確認はできます。

ただし、legacy の `/register` は SQLite の `t_Users` テーブルを使うため、SQLite が使えない場合は `503` を返します。
