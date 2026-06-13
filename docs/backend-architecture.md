# Backend Architecture

DeciScope API is a modular monolith using Clean Architecture boundaries. It
runs with PostgreSQL. Database connection or migration failures stop API
startup instead of silently falling back to in-memory persistence.

## Request flow

```text
HTTP/WebSocket
  -> adapter
  -> application service
  -> application port
  -> repository/storage adapter
  -> PostgreSQL/filesystem/external SDK
```

`internal/app/server.go` is the composition root. It creates concrete database,
repository, storage, Firebase, realtime, fixture, and HTTP objects and injects
them into the application.

## Current package layout

```text
internal/
  app/                 configuration and composition root
  domain/              entities, errors, event names, pure rules
  application/         use cases and outbound ports
  application/auth/    authentication use case and ports
  adapter/http/        router, handlers, DTOs, middleware
  adapter/realtime/    hub, WebSocket handler/client, protocol
  adapter/fixture/     fixture loader and replay manager
  adapter/repository/  Memory/PostgreSQL repositories and contract tests
  infrastructure/      database, Firebase, local storage
  architecture/        automated dependency checks
```

Environment files, environment variables, and concrete configuration are read
only from `internal/app`. HTTP handlers depend on consumer-owned use case
interfaces. Application use cases depend on ports declared in
`internal/application`.

## Adapter responsibilities

- HTTP keeps request parsing, response DTOs, status codes, and routing.
- Realtime separates room publishing, WebSocket handling, connection clients,
  and protocol messages.
- Fixture separates fixture loading from replay state management.
- Repository implementations are split by port responsibility and share one
  contract test suite.

## Database persistence

Application use cases depend only on repository interfaces declared in
`internal/application/ports.go`. PostgreSQL-specific SQL and transaction
behavior are isolated under `internal/adapter/repository/postgres`.
Connection creation and embedded PostgreSQL migrations live under
`internal/infrastructure/database`. The Memory implementation remains available
only as a test double.

## Runtime boundaries

- HTTP response DTOs live in `internal/adapter/http`; Domain Entity does not own
  HTTP JSON tags.
- WebSocket messages use protocol DTOs in `internal/adapter/realtime`.
- Application receives individual Repository/Publisher/ObjectStorage ports.
- PostgreSQL keeps durable event sequence allocation and related writes inside its
  transaction boundary.
- Environment files and variables are read in `internal/app`.

## Current limitations

- Meeting, fixture, upload, and realtime routes are not protected by auth.
- Firebase login persists users, workspaces, and sessions through PostgreSQL.
- Upload storage is local filesystem only.
- Queue/worker, external STT, and external LLM are not implemented.

## Verification

Run:

```powershell
go test ./...
go vet ./...
```

Repository implementations must pass the shared contract tests. Dependency
direction, adapter isolation, concrete dependency use in Application tests, and
environment-read placement are checked by
`internal/architecture/dependency_test.go`.
