# Build Guide

## Build binary

```powershell
go build .
```

If `go` is not on PATH yet:

```powershell
& 'C:\Program Files\Go\bin\go.exe' build .
```

## Install binary

Install to your Go bin directory (`GOBIN` or `GOPATH/bin`):

```powershell
go install .
```

If `go` is not on PATH yet:

```powershell
& 'C:\Program Files\Go\bin\go.exe' install .
```

## Run API

Set the database connection string, apply migrations, then start the API:

```powershell
$env:DATABASE_URL='postgres://deciscope:deciscope@localhost:5432/deciscope?sslmode=disable'
go run ./cmd/migrate up
go run .
```

If `go` is not on PATH yet:

```powershell
$env:DATABASE_URL='postgres://deciscope:deciscope@localhost:5432/deciscope?sslmode=disable'
& 'C:\Program Files\Go\bin\go.exe' run ./cmd/migrate up
& 'C:\Program Files\Go\bin\go.exe' run .
```

## Reset Development DB

Delete the local PostgreSQL container data and recreate an empty database:

```powershell
docker compose down -v
docker compose up -d
$env:DATABASE_URL='postgres://deciscope:deciscope@localhost:5432/deciscope?sslmode=disable'
go run ./cmd/migrate up
```

This removes all local development data. Do not use it against shared or production databases.

## Output

Windows binary file:

- `deciscope-core-api.exe`

## Common issue

- If port `8080` is already in use, running may fail with a bind error.
