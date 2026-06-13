# Backend Architecture

DeciScope API is a modular monolith using Clean Architecture boundaries. It
currently runs with SQLite and can fall back to an in-memory repository when
the database is unavailable.

## Request flow

```text
HTTP/WebSocket
  -> adapter
  -> application service
  -> application port
  -> repository/storage adapter
  -> SQLite/filesystem/external SDK
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
  adapter/repository/  Memory/SQLite repositories and contract tests
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

## Database portability

Application use cases depend only on repository interfaces declared in
`internal/application/ports.go`. SQLite-specific SQL and transaction behavior
are isolated under `internal/adapter/repository/sqlite`.

To add PostgreSQL:

1. Add a PostgreSQL database opener and migrations under `internal/infrastructure/database`.
2. Implement the existing repository ports under
   `internal/adapter/repository/postgres`.
3. Run the shared repository contract tests against PostgreSQL.
4. Select the implementation in `internal/app/server.go`.

No HTTP handler, domain type, or application use case should require a
PostgreSQL-specific change.

PostgreSQL is not connected yet. Driver-specific migration placeholders,
repository ports, and reusable contract tests are prepared; PostgreSQL SQL,
migrations, and the driver remain future work.

`DATABASE_DRIVER=postgres` must not be enabled yet. `database.Open` supports
only `sqlite`; an open failure currently causes meeting-related repositories to
fall back to Memory Repository.

## Runtime boundaries

- HTTP response DTOs live in `internal/adapter/http`; Domain Entity does not own
  HTTP JSON tags.
- WebSocket messages use protocol DTOs in `internal/adapter/realtime`.
- Application receives individual Repository/Publisher/ObjectStorage ports.
- SQLite keeps durable event sequence allocation and related writes inside its
  transaction boundary.
- Environment files and variables are read in `internal/app`.

## Current limitations

- Meeting, fixture, upload, and realtime routes are not protected by auth.
- Firebase login uses SQLite UserRepository when available; Memory fallback
  verifies identity without creating a local user ID.
- Upload storage is local filesystem only.
- Queue/worker, external STT, external LLM, and PostgreSQL are not implemented.

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
