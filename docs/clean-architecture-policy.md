# Clean Architecture Policy

## Dependency rule

Dependencies point inward:

```text
app -> adapter / infrastructure -> application -> domain
```

`internal/app` is the composition root. It may know concrete adapters.
`internal/domain` and `internal/application` must never know concrete adapters.

## Package responsibilities

- `internal/domain`: entities, domain errors, event names, and pure rules.
- `internal/application`: use cases and outbound port interfaces.
- `internal/adapter/http`: HTTP routing, request parsing, responses, and auth
  middleware.
- `internal/adapter/realtime`: WebSocket protocol and event publishing.
- `internal/adapter/fixture`: local fixture replay.
- `internal/adapter/repository`: persistence implementations and repository
  contract tests.
- `internal/infrastructure`: database setup, migrations, Firebase construction,
  and filesystem storage.
- `internal/app`: configuration, construction, dependency injection, startup.

## Placement decisions

- New business behavior starts in Domain or Application.
- New SQL belongs in a repository adapter or migration.
- A PostgreSQL implementation belongs in
  `internal/adapter/repository/postgres`.
- New external I/O requires an Application port before an implementation.
- HTTP DTOs and status-code decisions belong in the HTTP adapter.
- Concrete implementations are wired only in `internal/app`.
- Environment variables and environment-file loading belong in `internal/app`.
- HTTP handlers define the narrow use case interfaces they consume.
- Tests for Application and HTTP use fakes at their respective boundaries.
- Public HTTP and WebSocket contracts must be preserved during refactoring.
- A database implementation must pass the shared repository contract tests.

## Prohibited changes

- Do not import adapters or infrastructure from Domain or Application.
- Do not execute SQL, filesystem operations, Firebase calls, or HTTP handling in
  Application.
- Do not introduce a broad `core`, `shared`, `common`, or `utils` package.
- Do not put business decisions in the composition root.
- Do not expose Domain Entity directly as an HTTP or WebSocket response.
- Do not enable an unsupported database driver and rely on Memory fallback as
  a production migration path.

The automated rules in `internal/architecture/dependency_test.go` enforce the
most important dependency constraints, including Application test isolation,
Adapter family isolation, and environment-read placement.
