# Local Development

## Run

```powershell
go run .
```

既定では `http://localhost:8080` で起動します。

## Quick Demo

1. 会議を作成します。

```http
POST http://localhost:8080/v1/meetings
Content-Type: application/json

{
  "title": "Demo meeting"
}
```

2. 返ってきた `id` で WebSocket に接続します。

```text
ws://localhost:8080/v1/realtime?meeting_id={meeting_id}
```

3. fixture replay を開始します。

```http
POST http://localhost:8080/v1/meetings/{meeting_id}/replay/start
Content-Type: application/json

{
  "fixture": "demo.jsonl"
}
```

4. 終了後、レポートを確認します。

```http
GET http://localhost:8080/v1/meetings/{meeting_id}/report
Accept: text/markdown
```

## Curl Examples

PowerShell では `curl` が `Invoke-WebRequest` のエイリアスになることがあります。必要に応じて `curl.exe` を使ってください。

```powershell
curl.exe -X POST http://localhost:8080/v1/meetings -H "Content-Type: application/json" -d "{\"title\":\"Demo meeting\"}"
```
