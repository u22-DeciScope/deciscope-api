
## Tool execution and concurrency

When operating in Code Mode:

* Within each bounded stage, run independent tool calls concurrently when they
  are available through `functions.exec`.
* Batch those calls into a single `functions.exec` invocation rather than
  splitting otherwise batchable inspections across separate top-level tool
  calls.
* Use `await Promise.allSettled([...])` when partial results remain useful, and
  inspect every fulfilled and rejected result.
* Use `await Promise.all([...])` only when any single failure should abort the
  entire batch.
* Keep the following operations sequential:

  * dependent operations;
  * waits, resumes, and approval steps;
  * conflicting or interdependent mutations;
  * adaptive investigations where one result may change the next action.
* Do not parallelize work merely for speed when doing so could change behavior,
  hide a failure, or create conflicting mutations.

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

## Discussion tree quality regression

When changing discussion-tree construction, live analysis, finalization,
validators, deterministic repairs, prompts, or AI response schemas:

1. Add or update a semantic scenario in
   `internal/application/testdata/qualityeval/scenarios.json` before changing a
   production repair for a newly found quality defect.
2. Run the normal tests and
   `go run ./cmd/meeting-quality-eval -suite deterministic -compare-baseline`.
3. Review every metric independently; an improvement in one axis does not
   approve a regression in another.
4. Never use sleeps, timeout increases, production threshold relaxation, or
   whole-tree golden snapshot replacement to make the suite pass.
5. Update the baseline only with the explicit `-update-baseline` command after
   the semantic expectation and metric change have been reviewed. A single
   real-model run is not a baseline source.

The opt-in real-deployment suite is documented in
`docs/meeting-quality-eval.md`. Normal `go test ./...` must remain network-free.
