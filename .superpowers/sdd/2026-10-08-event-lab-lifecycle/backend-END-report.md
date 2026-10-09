# Backend END verification and reproduced fixes

Status: source/database/queue gates verified; independent whole-system review and actual native/API/browser/foreground-process proofs remain root-owned and pending. No push, PR, merge, release, host-trust change, or shared Kind-fixture mutation was performed.

Raw evidence root: `/Users/volodymyrporokhniak/.codex/outputs/2026-10-08-lifecycle-execution/backend-END`. Each named run preserves `.stdout`, `.stderr`, and `.json` with the exact argv, cwd, `GOWORK=off`, exit code and duration. Later bounded runs use `GOFLAGS=-p=2`; no runtime performance claim follows. `COUNTS.json`, `CHANGED-FILES.json`, `source-fixes.diff`, and the final `EVIDENCE-SHA256.json` preserve inventories.

## Reproduced failures and fixes

- Three sqlc nullable timestamp parameters and two imports prevented build; corrected adapter types/imports. Sizing test literals now use named resource fields; all eligible sizing fixtures explicitly describe both service pods.
- SDK provenance checker falsely rejected the actual grpc-generator bullet banner. SDK owner supplied the correction; final bundle pins producer `f6ad9ca2c5d45c7127d88e8dd17e53860b390796`. This source pin also includes final producer test fixture fixes.
- Migration test attempted to drop0160 while0161–0165 depended on it. Disposable PostgreSQL rollback now runs0165→0160 and reapplies0160→0165. This proves schema/queries, not production data rollback preservation.
- Route golden now records exactly the frozen stop/restart participant routes, gated by self and presence. Terminal teardown test expects no recreation work.
- New real PostgreSQL regression reproduced charging another group's immutable sizes after its exact stopped/released receipt. Admission/manual restart/cohort budget calculations now count current HeldCompute, and reserve the waking group's services once. Quota history is independent.
- Managed Create had no initial lifecycle spec. New adapter and pre-RPC PostgreSQL tests reproduced this gap. Creation now carries the persisted Running operation and revision1; UID is learned from the object. Generic Ready cannot publish managed Labs; exact UID/op/rev/generation Running+Allocated receipt is required before current ACL readiness. Running1 periodic observations also refresh canonical allocation.
- Historical managed RuntimeReady/revision1/observed0 rows bypassed SQL ACL/reveal guards. Raw PostgreSQL tests reproduced it. Three guards now require the exact observed revision. Explicit unmanaged lab_id=NULL compatibility is preserved. Two-team all_ready test stays closed until both exact revisions; lifecycle-pass test demonstrates historical row recovery. A legacy shared-Lab fixture now explicitly supplies synthetic current-operation receipts.

## Fresh verification

Final full suite:4329 test/subtest runs,4327 passes, no failures,2 skips. One skip is intentional email HTML previews (EMAIL_PREVIEW_DIR unset). The other was the unrelated IPAM integration container port timeout under host load; its isolated real-PostgreSQL retry passed with no skip. All event/repository lifecycle PostgreSQL tests executed. Do not describe the full invocation as zero-skip.

Race:17/17 original integrated cases and5/5 affected initial/admission/realRiver cases pass. These include real SQL row serialization, concurrent budget spends, final-solve/source changes, same-time allocation CAS, and real River periodic lost-wake/fresh-client recovery. The recording infrastructure boundary is explicit: this is database/queue proof, not native task/cgroup/network/browser or a fresh OS-process mTLS worker proof.

SDK module:9/9 cases pass; final missing bundle, altered source, and altered valid SHA provenance tests5/5 pass. Module resolution points into the tracked relative third_party SDK and uses no sibling. Final strict read/reveal/ACL targeted run38/38 plus legacy shared-Lab correction4/4 pass.

Build, vet/error-code/layer/route checks and error catalog check pass. Pinned sqlc regeneration changes only three query constants; no generated interface/model drift. Swagger generation passed and generated docs compile. The two standalone Docker builds passed using earlier source checkpoints; the final frozen source rebuild is deliberately deferred to root so independent review can start. Those earlier image digests are inventory evidence, not final-source build proof.

## Command results

| Evidence prefix | Exact command | Exit |
|---|---|---|
| build-initial | `go build ./...` | 1 |
| build-adapter-fix | `go build ./...` | 1 |
| build-import-fix | `go build ./...` | 0 |
| vet-initial | `make vet` | 2 |
| vet-after-compile | `make vet` | 0 |
| final-build-vet | `make build vet error-catalog-check` | 0 |
| build-vet-after-initial-fix | `make build vet error-catalog-check` | 0 |
| strict-read-build-vet | `make build vet error-catalog-check` | 0 |
| sdk-final-provenance | `python3 .github/scripts/check-laboratory-sdk.py` | 0 |
| final-sdk-provenance | `python3 .github/scripts/check-laboratory-sdk.py` | 0 |
| final-sdk-provenance-and-tamper | `go test -json -count=1 ./tools/laboratorysdk` | 0 |
| sdk-tests | `go test -json -count=1 ./...` | 0 |
| sdk-module-resolution | `go list -m -json github.com/cybericebox/laboratory` | 0 |
| final-full-tests-go | `go test -json -count=1 ./...` | 0 |
| ipam-skip-recovery | `go test -json -count=1 ./pkg/ipam -run ^TestIPAManager_AcquireRelease$` | 0 |
| lifecycle-race | `go test -race -json -count=1 ./internal/useCase/event ./internal/delivery/repository/postgres ./internal/delivery/infrastructure/labagent -run TestTask6RealRiver|TestConcurrentEventAdmissions|TestObservationCannotOverwrite|TestLabLifecycleConcurrent|TestLabLifecycleSource|TestManualStopOwnership|TestFutureStagePin|TestStageClosure|TestInitialProvisioning` | 0 |
| initial-budget-race | `go test -race -json -count=1 ./internal/useCase/event -run TestManagedInitialReadinessAdopts|TestInitialProvisioningHoldsBefore|TestReleasedOtherGroup|TestConcurrentEventAdmissions|TestTask6RealRiver` | 0 |
| strict-read-sql-green | `go test -json -count=1 ./internal/useCase/event ./internal/delivery/repository/postgres ./internal/delivery/repository/labAccessSyncRepo -run TestLegacyManagedInitial|TestAllReadyBarrierRejects|TestLifecyclePassRefreshes|TestManagedInitialReadiness|TestLabAccess|TestEventLabAccess|TestEventLabs|TestPublish|TestReveal` | 0 |
| migration-teardown-fixed | `go test -json -count=1 ./internal/delivery/repository/postgres -run TestEventLabsMigrationDownThenUp|TestModeratorsTeamIsHidden` | 0 |
| released-group-budget-red | `go test -json -count=1 ./internal/useCase/event -run ^TestReleasedOtherGroupDoesNotConsumeNewTeamAdmission$` | 1 |
| released-group-budget-green | `go test -json -count=1 ./internal/useCase/event -run ^TestReleasedOtherGroupDoesNotConsumeNewTeamAdmission$` | 0 |
| managed-create-red | `go test -json -count=1 ./internal/delivery/infrastructure/labagent ./internal/useCase/event -run TestManagedCreateCarries|TestInitialProvisioningHoldsBefore` | 1 |
| managed-create-observe-fixed | `go test -json -count=1 ./internal/delivery/infrastructure/labagent ./internal/useCase/event -run TestManagedCreateCarries|TestInitialProvisioningHoldsBefore|TestManagedInitialReadinessAdopts|TestReleasedOtherGroup` | 0 |
| legacy-managed-read-sql-red | `go test -json -count=1 ./internal/useCase/event -run TestLegacyManagedInitial|TestAllReadyBarrierRejects` | 1 |
| strict-read-legacy-fixture-green | `go test -json -count=1 ./internal/useCase/event -run TestStandEngine_OneLabPerExerciseWithEveryTaskFlag|TestLegacyManagedInitial|TestAllReadyBarrierRejects|TestLifecyclePassRefreshes` | 0 |
| docker-standalone | `docker build --progress=plain --tag cib-daemon-lifecycle-end:owned-20261008 .` | 0 |
| docker-final | `docker build --progress=plain --tag cib-daemon-lifecycle-end:owned-final-20261008 .` | 0 |
| fixture-owned-postgres | `python3 /Users/volodymyrporokhniak/.codex/outputs/2026-10-08-lifecycle-execution/backend-END/prepare_fixture.py` | 0 |
| fixture-seed | `python3 /Users/volodymyrporokhniak/.codex/outputs/2026-10-08-lifecycle-execution/backend-END/run_fixture_seed.py` | 0 |

## Owned integration fixture, retained for follow-up

Owned persistent PostgreSQL container `cib-lifecycle-backend-end-20261008`, ID `165fe5f97e542edca48c04d75d88a7156c3c3042aed81628e7a779f8856f050e`, bound only to127.0.0.1:65430. Actual migrations through0165 applied. Normal existing seed machinery created4 activated accounts,2 participant teams and10 catalog exercises; only Persistent Box(3 questions/shared Lab) and Web Basics(unrelated Lab) remain active on event `01a11d08-e273-7d98-9957-3841f591d7e1` / `seedlifecycleend`. No Labs were created and no native dispatch or authenticated server has started.

`FIXTURE-INVENTORY.json` contains exact non-secret paths and ownership. DSN, account credentials, server environment, seed inventory and localhost/API SAN TLS key/certificate are mode0600 inside mode0700 private/. Do not print or commit their contents. Planned API URL is https://api.lifecycle.localtest.me:18443, still unstarted. No external SMTP/Telegram/OAuth/storage/enrollment endpoint is configured. No opaque login handles or browser fixture were fabricated.

Follow-up still needs actual native mTLS files and qualified feature response, actual agent enrollment/identity and supported reservation placement, Required fixture persistence/policy setup before generation materialization, normal server start and real login-session handles, tools/lifecyclefixture export of actual canonical IDs, and tools/lifecycleworker fresh OS-process periodic delivery/restart. Ordinary data seeding uses the existing seed-only labsAssumed port; it makes no native or capability proof. Seed process reported an existing pool-close timeout after migration; process exited0 and schema/data checks succeeded. This warning is retained, not relabeled as native failure/success.

No real physical GC proof exists in this backend evidence. Delete acceptance never certifies release; prior known physical bytes still require current known-zero certified cleanup. Unknown physical measurements remain unavailable. Closure before initial UID adoption stays conservative held/error, never guessed identity or names-only release.

## Self-review and outstanding owner gates

Reviewed production composition, adapter JSON shape, pending reservation before create, initial exact observation/write CAS, strict SQL publication/ACL guards, group budget re-hold once, migration ordering, route authorization, retained scope deletion and raw evidence skip counts. Actual initial managed JSON was confirmed against the current producer by the SDK/native owner. Independent whole-Lab/backend review, physical runtime/GC, actual authenticated UI/API and fresh OS worker proofs are outstanding owner gates; no final whole-system acceptance is claimed.

Final Swagger command `GOWORK=off GOFLAGS=-p=2 make swagger` exited0; `go build ./internal/delivery/controller/http/handler/apidocs` exited0. Generated documentation adds the two frozen participant endpoints and their DTO schemas. Final tested production source uses producer SDK f6ad9ca2c5d45c7127d88e8dd17e53860b390796; fresh full-suite/source and targeted checks above cover this pin.
