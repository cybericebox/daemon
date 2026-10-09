# Cohesive backend review fix wave

Status: code, real PostgreSQL/consumer trigger cases, full Go suite and targeted race checks pass. Independent scoped re-review and actual native/API/browser/fresh OS-process proof remain root-owned. No producer qualification, release or external side effect is claimed.

Reviewed the complete final-backend-review.md. Original finding4 (no production writer) was retracted by the reviewer/root after verifying existing SetEventExerciseStage→retentionRepo.Select. Valid IDs addressed are1,2,3,5,6,7 and the confirmed runtime DTO capability finding8. The current branch selection remains approved.

## Changes and binding decisions

- **1:** ReadyForAccess is shared by participant projection, direct runtime access and signing/recheck. It requires current observed revision, Running/RuntimeReady, UID/live generation, operation, and recorded Allocated acknowledgement timestamps. Historical Ready1/observed0 fails closed. Explicit unmanaged lab_id=NULL runtime/link behavior stays available. The cost is temporary unavailability until a current managed acknowledgement.
- **8:** runtime DTO uses participantLabCapabilities before and after the live read, matching board/GetOwnLabLifecycle. The actual Gin route test verifies equal revision12 capabilities survive the latest-answer consumer rule without boolean OR. Unqualified and terminal conditions still suppress controls.
- **2:** mandatory internal BeforeCreate callback writes create_evidence before the actual Create RPC, after group resolution and exact SDK creation-definition hash. It preserves original Ref/group UID/hash/Running1 operation/revision/deployment generation. Request expected_group_uid8 fences the actual group. Lab.creation_receipt15 supplies immutable committed original namespace/birth ID/actual Lab UID. UID-less closed Starting/Unknown may adopt only that committed matching birth; raw JSON/revision/generation/reference CAS preserves desired stopped intent and holdings. Birth adoption is identity only; release still requires the exact stopped certificate. Missing/conflicting/historical-unrecorded evidence stays held. Managed committed birth already has Running1 intent, so wait for its acknowledgement rather than issuing the legacy Start adoption command.
- **3:** barrier identity is the authorized source revision/mode tuple; same tuple keeps its cohort immutable. Authorized source/mode change replaces preparation/opening state and recomputes its cohort transactionally. The opening query requires current source/mode, current pinned definition and current generation bindings, preventing old ready copies from opening a replacement cohort.
- **5:** private runtime_stage_id/known records the stage boundary at preparation. An authorized upcoming set move preserves that origin until a distinct consumed preparation authorizes a new stage. No opened-stage move power was added. Boundary reconciliation resolves null inheritance/zero/smaller/larger stage TTL and updates stopped/solved copies without reopening or changing history. Finish-less solved whole-event copies get their base deadline when the existing manual_finished_at field later becomes known. ProtectedUntil remains an independent maximum. This extra private state avoids relabeling a prewarmed artifact when its current set assignment moves.
- **6:** command and capability preview share the same candidate budget read. Held siblings plus own immutable service demand must fit TeamSlot and global/placement/window/storage constraints. Released other groups stay released. Spare reserved capacity on another team/node cannot fund the candidate slot.
- **7:** stage preparation records selected_revision and consumed revision/time. The event/calendar/team/Lab transaction rereads and locks the exact authorized membership/pin/stage/generation before admission. A manual revision after selection invalidates stale intent; an already consumed intent cannot restart a subsequent manual stop. A genuinely distinct later authorized stage can restore the same retained generation. Historical memberships receive selected_revision0 and fail closed rather than inventing consumption history.

## Verification and evidence

Raw evidence: `/Users/volodymyrporokhniak/.codex/outputs/2026-10-08-lifecycle-execution/backend-fix-wave`. Each run preserves stdout/stderr/argv/cwd/GOWORK=off/exit/duration in named files. Bounded builds use GOFLAGS=-p=2. COUNTS.json records runs, passes and skips. New tests were written during the owner-approved cohesive code-first wave; the first executed trigger run also contained fixture/UTC/dependency failures, not a separate historical production RED for every reviewed defect. That distinction is preserved.

Full final run:4354 test/subtest runs,4353 passed,1 intentional EMAIL_PREVIEW_DIR skip; no DB integration skips. Race trigger gate9/9 passes. All review triggers pass through the full suite; the stronger source-switch test added current replacement bindings/new generation and both-team opening after the full run, and its isolated current-source-cohort-positive check passes. No production source changed for that test strengthening.

Real PostgreSQL migration test now rolls0166→0160 down and0160→0166 up; queries/models/mocks generated with pinned sqlc1.31.1. This is disposable-schema/consumer/queue proof, not physical native proof or production rollback data-preservation proof. Existing real River periodic/lost-wake/fresh-client cases execute in the full suite using recording infrastructure.

SDK checkpoint b73a505aafa49e211a748ab2a88295a8e3b135d9 was cherry-picked as80827be3 for compilation. It pins draft producer a9598fc066a6018db6f166d9fc4c3fc5ca60be5a CODE_ONLY_UNVERIFIED. The exact frozen getters/helper are compiled and provenance checker exercised by the suite; final producer provenance and native flags are not qualified. No Docker build/image refresh was performed in this wave. Public DTO schemas/routes did not change, so no new Swagger contract was required.

## Commands

| Evidence | Command | Exit |
|---|---|---|
|affected-suites|`go test -json -count=1 ./internal/useCase/event ./internal/model/eventLab ./internal/delivery/repository/postgres ./internal/delivery/infrastructure/labagent ./internal/delivery/controller/http/handler/eventself`|1|
|build-vet|`make build vet error-catalog-check`|0|
|compile-initial|`go build ./...`|1|
|consumed-intent-fixture-green|`go test -json -count=1 ./internal/useCase/event -run ^TestReviewConsumedStage`|0|
|current-source-cohort-positive|`go test -json -count=1 ./internal/useCase/event -run ^TestReviewSourceSwitch`|0|
|full-final|`go test -json -count=1 ./...`|0|
|generated-final|`python3 /Users/volodymyrporokhniak/.codex/outputs/2026-10-08-lifecycle-execution/backend-fix-wave/regenerate_sql.py`|0|
|managed-birth-initial-green|`go test -json -count=1 ./internal/useCase/event -run TestManagedInitialReadinessAdopts|TestReviewLostInitial`|0|
|sqlc-final|`python3 /Users/volodymyrporokhniak/.codex/outputs/2026-10-08-lifecycle-execution/backend-fix-wave/regenerate_sql.py`|0|
|sqlc-generation|`python3 /Users/volodymyrporokhniak/.codex/outputs/2026-10-08-lifecycle-execution/backend-fix-wave/regenerate_sql.py`|0|
|stage-origin-fixture-green|`go test -json -count=1 ./internal/useCase/event -run ^TestStageClosureRecords`|0|
|trigger-race|`go test -race -json -count=1 ./internal/useCase/event -run ^TestReview(LostInitial|RealRuntime|ConsumedStage|RestartCannot|HistoricalManaged)`|0|
|trigger-tests-fixtures-corrected|`go test -json -count=1 ./internal/useCase/event -run ^TestReview`|1|
|trigger-tests-initial|`go test -json -count=1 ./internal/useCase/event -run ^TestReview`|1|
|trigger-tests|`go test -json -count=1 ./internal/useCase/event -run ^TestReview`|1|

## Self-review and retained scope

Traced readiness callers/rechecks, capability projections and command admission, mandatory pre-Create persistence, immutable receipt adoption after closure, source/mode transaction boundaries, current binding/source cohort opening, stage policy/base deadline on already solved copies, effective future maximum, team/global holds, and consumed stage authorization. Every known-zero/Unknown/storage/native fence rule remains conservative; no lower profile/defaults or finite U/L envelope was introduced.

Owned persistent PostgreSQL165fe5f97e54 and backend-END/private/ fixture remain unchanged for the actual qualified-native/API/River/browser follow-up. No foreground backend/worker, sessions, namespace, native fixture, profile flags, images or private credential files were changed. Root still owns independent scoped re-review, final SDK pin/native proof, final standalone Docker, real authenticated API/browser and fresh OS-process mTLS River delivery.

Final source build/vet/error-code/layer/route/catalog check command `GOWORK=off GOFLAGS=-p=2 make build vet error-catalog-check` exited0 after the managed-initial wait correction. No checks remain running.
