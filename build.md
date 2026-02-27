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

## Output

Windows binary file:

- `deciscope-core-api.exe`

## Common issue

- If port `8080` is already in use, running may fail with a bind error.
