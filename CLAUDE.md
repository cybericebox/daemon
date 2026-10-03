# CyberICEBox AP Backend (daemon)

Go 1.26 · Gin · pgx/v5 + sqlc · golang-migrate · River (jobs) · gomock · testcontainers.

## Commands

- `make build` / `make vet` / `make test` — vet also runs the error-code lint (`make lint-errors`).
- `make sqlcGenerate` — regenerate sqlc after editing `queries/*.sql`; then `go generate ./internal/delivery/repository/postgres/` for mocks.
- `make swagger` — regenerate the spec after touching handler DTOs/annotations.
- Integration tests (`internal/delivery/repository/postgres/*_integration_test.go`) need Docker; they self-skip without it.

## Layers (go-ddd style, enforced by convention)

- `internal/model/*` — DOMAIN: entities, value objects, domain errors, and every mutation as an entity method. Must not import `internal/delivery` or `internal/useCase`.
- `internal/useCase/*` — APPLICATION: orchestration only (fetch → entity method → persist) plus read models (views) it produces for delivery (`views.go`, `admin_views.go`, `input.go`).
- `internal/delivery/*` — controllers (HTTP DTOs live in handler packages), repositories, sqlc, middleware, protection.

## Domain entities

- All mutations are pointer-receiver methods on the entity; they enforce invariants and touch `UpdatedAt` in one place. Never mix value and pointer receivers on one type.
- `now time.Time` is always a parameter — callers and tests control the clock. Entity defaults (UUIDv7 ids, timestamps) come from domain factories (`NewSession`, `NewIncompleteUser`, ...), never from DB defaults.
- Expensive checks an invariant needs are passed as lazy callbacks (see `User.CompleteSetup`'s `hasProvider`).
- Never validate on the read side: repositories must load any historical row (corrupt/legacy data is forgiven, e.g. session metadata).

## Repositories

- One whole-entity repository per aggregate (`userRepo`, `sessionRepo`): accepts/returns domain models; sqlc rows, `pgtype`, and JSON (de)serialization exist only inside it. Writes are ONE statement per aggregate (`UpdateUser`, `CreateSession`) — a single UPDATE is atomic, so no unit of work for single-aggregate flows.
- Deliberate narrow-query exceptions (documented in each repo's package comment):
  - `UpdateUserLastSeen` / `TouchSession` — async hot path (`protection.touchAsync`); they are excluded from the aggregate UPDATE column set so full-row writes cannot race them;
  - reads for lists/stats (`ListUsersCursor`, `Count*`) — query-side shapes;
  - `Delete*` — set operations, not aggregate mutations.
- The `mutateUser` helper in `useCase/auth` is the canonical write path: fetch → mutate → whole update, guarded by an optimistic lock (`expected_updated_at` = the UpdatedAt loaded BEFORE the mutation). Zero rows → re-read to discriminate: row gone → `ErrUserNotFound` (404), row present → `ErrUserModified` (409, client reloads).
- Multi-row aggregates stay in SQL: the notification template *version family* (draft/published/unpublished rows per type) enforces its invariants — one published per type, publish only from draft, rollback only from unpublished — atomically in the `Publish*`/`Rollback*` CTE queries. Do NOT re-implement these as Go fetch-mutate-write transitions (multi-row transactions, races, zero gain). The domain still owns the vocabulary: typed `TemplateStatus`, `NewDraft` factories, `IsDraft/IsPublished/IsUnpublished` predicates.

## Errors (`pkg/err`)

- One `Err*` var = one non-test call site = unique DetailCode within its object code. `tools/checkerrorcodes` (in `make vet`) fails on duplicates — `err.As` scopes detail-level matching to the object code (a detail-only target still matches by detailCode alone), so uniqueness within an object is what keeps `errors.Is` honest.
- Each `errors.go` header keeps the registry: `next free detail code: N`.
- HTTP status comes from the base (`ErrUnauthenticated`→401, `ErrObjectNotFound`→404, ...): the same fact needing two statuses is two different vars by definition.
- Allowed multi-site errors are annotated at the declaration:
  - category A — security-indistinguishable (anti-enumeration): the client must NOT be able to tell the cases apart; per-site reason goes to server logs via `WithError` only. Examples: `ErrAuthInvalidUserCredentials`, `ErrAuthInvalidSession` (covers revoked/deleted sessions too — never reveal that a session existed), `ErrInvalidToken`.
  - category B — RBAC guard (`ErrInsufficientPermission`, always 403).
- Changing a DetailCode changes the API FullCode — breaking for clients keying on it; call it out in the commit message.

## Identity / RBAC

- Request identity travels as ONE bundle: `rbac.Claims{SessionID, UserID, Role}` via `ContextWithCurrentUserSession` / `CurrentUserSessionFromContext`. No granular per-field producers or getters — do not reintroduce them. `authModel.AuthClaims` is a type alias of `rbac.Claims` (the struct lives in rbac to avoid the auth→rbac import cycle).
- Permissions are dotted namespaces with prefix coverage; super_admin holds `"*"`. Role inheritance resolves in `rbac`'s `init()` — never read `Role.Permissions()` during package-var initialization.
- Route gating is `RequirePermission(perm)` only, and it is the SINGLE authorization point: use cases do NOT re-check static permissions (data-dependent policy — per-setting `RequiredPermission`, `CanAssignRole`, domain invariants — stays in use cases). `tools/checkroutes` (in `make vet`) fails on any handler route without a gate unless it is explicitly allowlisted as public; `RequireCaptcha` does not count as a gate. `PermSelf` marks authenticated self-service routes.

## Testing

- TDD: tests first, red before green. Domain tests are pure (no mocks); use fixed `time.Time` values, never `time.Now()` in assertions.
- Use-case tests mock the sqlc `Querier` (gomock); aggregate-write tests assert the WRITTEN row (`UpdateUserParams`), not which narrow query was called.
- sqlc SQL itself is covered by testcontainers integration tests — mocks cannot catch SQL/schema drift (a real bug class: the `users.last_seen` NULL scan).
