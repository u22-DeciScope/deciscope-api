# Backend Development Rules

Before changing backend code, read `docs/clean-architecture-policy.md` and
`docs/backend-architecture.md`.

## Required dependency direction

```text
internal/app
  -> internal/adapter, internal/infrastructure
    -> internal/application
      -> internal/domain
```

- `internal/domain` must not import another project package.
- `internal/application` may import only `internal/domain` from this project.
- Define outbound ports in `internal/application`; implement them in adapters or
  infrastructure.
- Construct concrete implementations only in `internal/app`.
- Keep SQL in repository adapters and migrations.
- Keep HTTP concerns in `internal/adapter/http`.
- Keep environment reads and concrete construction in `internal/app`.
- Do not import one Adapter family from another Adapter family.
- Test Application with Fake Ports and HTTP handlers with Fake Use Cases.
- Do not recreate generic packages such as `core`, `common`, `shared`, or
  `utils`.

## Change checklist

1. Preserve the existing HTTP and WebSocket contracts unless the task explicitly
   changes them.
2. Add or update focused tests at the layer being changed.
3. Run `go test ./...` and `go vet ./...`.
4. Confirm `internal/architecture` dependency tests pass.
5. Do not bypass an application port by calling a database, filesystem, or
   external SDK from a use case.
6. Compare HTTP status codes, JSON field names, WebSocket messages, and error
   behavior before and after a refactor.
7. Keep documentation aligned with registered routes, current defaults, and
   implemented database drivers.
