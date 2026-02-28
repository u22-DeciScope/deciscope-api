    # Go Chi Policy (Headless API)

## 1. Scope

- This service is a headless JSON API built with Go and chi.
- It does not render UI.
- It derives from the architectural principles defined in AGENTS.md.
- Framework / library choices must not affect Domain rules.

---

## 2. Architectural Layer Structure

The service is organized into four logical layers:

- Handler (HTTP boundary)
- Usecase (application / orchestration)
- Domain (business rules, pure logic)
- Infra (DB, external APIs, queues, storage)

Dependency direction must always move inward:

- Handler → Usecase
- Usecase → Domain
- Usecase → Infra (through interfaces defined inward)
- Domain must not depend on outer layers
- Infra must not leak into Handler

---

## 3. Package Layout (Suggested)

This is a suggestion, not a hard requirement. The rules matter more than names.

- /cmd/<service>/main.go
- /internal/handler        (chi router, handlers, middleware wiring)
- /internal/usecase        (use cases, application services)
- /internal/domain         (entities, value objects, domain services, policies)
- /internal/infra          (db, repository impl, external clients)
- /internal/contract       (interfaces/ports that outer layers implement)
- /internal/mapper         (explicit mapping helpers; no business logic)
- /internal/app            (composition root; DI wiring)

Rules:
- Domain must not import handler/usecase/infra.
- Handler must not import infra implementations directly (only contracts).

---

## 4. File Responsibility Principle

Each file must represent a single cohesive responsibility.

- One handler file should correspond to one endpoint responsibility.
- One usecase file should correspond to one use case.
- Domain files should represent one cohesive domain concept (not arbitrary utilities).
- Helper functions are allowed if they support the same responsibility.
- Do not group unrelated endpoints or use cases into a single file.
- Increasing file count is acceptable if it preserves structural clarity.
- If a file name requires “and” to describe its purpose, split it.

This principle prevents “god files” and helps preserve dependency direction.

---

## 5. Boundary Models: Request / Response / Domain

### Types
- Request models: HTTP boundary only (decode + validation inputs).
- Response models: external contract only (encode outputs).
- Domain models: internal meaning and business rules.
- Infra models: persistence / external SDK shapes only.

### Rules
- Always map explicitly:
  - Request → Domain input
  - Domain output → Response
  - Infra model ↔ Domain via dedicated mapping
- Structural similarity is not a reason to bypass mapping.
- Never expose DB rows / ORM structs / external SDK structs to clients.
- `snake_case` may exist only in HTTP/DB boundary models. Domain uses Go naming and meaning.

---

## 6. Handler Responsibilities (chi)

Handlers are HTTP boundaries only.

Handlers are responsible for:
- Routing (chi Router wiring)
- Authentication/authorization gate (via middleware; actual policy decisions may live in Usecase/Domain)
- Decoding request (JSON, path/query params)
- Input validation (syntax/shape; business rules belong to Usecase/Domain)
- Mapping to Domain inputs
- Calling Usecase
- Mapping Usecase result to Response
- Selecting HTTP status codes

Handlers must not:
- Contain business logic
- Call repositories directly
- Construct SQL or query builders
- Depend on infra implementations

---

## 7. Usecase Responsibilities

Usecases are the center of orchestration.

Usecases are responsible for:
- Implementing application flows (use cases)
- Coordinating domain operations
- Calling repositories/external systems via interfaces (ports)
- Enforcing application-level rules (idempotency, permissions as “can do X?” checks, etc.)
- Transaction boundaries (begin/commit/rollback through infra abstractions)

Usecases must not:
- Operate on HTTP Request/Response structs
- Return infra-specific errors or raw external responses

---

## 8. Domain Rules (Isolation)

Domain is the source of truth.

Domain must:
- Remain free of framework concerns (chi, net/http, context usage should be minimal and abstracted)
- Avoid importing infra packages or external SDK packages
- Prefer pure functions and explicit inputs

Domain may:
- Define interfaces (ports) that infra implements (e.g., Repository contracts)
- Define domain errors (meaningful, stable)

---

## 9. Contracts (Interfaces / Ports)

- Define ports as close to the domain/usecase as possible (inward).
- Infra implements ports; handler/usecase depend on ports.

Rules:
- Ports must express domain intent, not technology:
  - Good: UserRepository, TokenSigner, Clock
  - Avoid: SQLUserRepository, RedisCacheClient as a port name
- Ports must return Domain types or values that can be normalized into Domain types.

---

## 10. Error Handling Policy

All errors returned to clients must follow a single response format.

Minimum JSON structure:
- `code` (stable machine-readable identifier)
- `message` (client-facing text)
- `details` (optional)

Rules:
- Convert infra errors into application-level errors in Usecase layer.
- Handler maps application errors to HTTP responses.
- Never expose stack traces, SQL errors, or SDK raw messages to clients.
- Maintain stable error codes across versions.

Recommended structure:
- DomainError: domain meaning (e.g. "playlist_not_found")
- AppError: includes http status + domain code + safe message
- Infra errors: wrapped with context but not leaked

---

## 11. Transactions & Consistency

- Transaction boundaries belong to Usecase.
- Handler must not control transactions.
- Transaction scope must be minimal and explicit.
- Domain must not rely on transaction side effects.

Implementation notes:
- Prefer explicit unit-of-work abstraction if needed.
- Do not pass DB tx objects through Domain.

---

## 12. Context Usage (Go)

Rules:
- `context.Context` is used for cancellation/deadlines and request-scoped values only.
- Do not use context as a hidden dependency injection container.
- Do not put business-critical decisions into context values.

---

## 13. Constants & Util Policy

Constants must live near their responsibility:
- Business rules → near Domain logic
- Infra keys → near infra modules

`util` packages are restricted:
- Technical helpers only
- No business logic
- Must not introduce reverse dependencies

---

## 14. Testing Policy (Minimal)

- Domain: pure unit tests (no DB, no network)
- Usecase: tests with port mocks/fakes
- Handler: thin tests (routing + status + mapping)
- Infra: integration tests (DB/external), separate and explicit