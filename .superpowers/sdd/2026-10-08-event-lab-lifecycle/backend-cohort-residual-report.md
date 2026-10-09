# R1 bounded cohort preparation correction

Base69edd04a. Scope is the sole residual from final-backend-rereview.md; no second broad review/fix wave, SDK change, schema change, profile/default change, image build or native fixture mutation.

Authorized source/config edits now call InvalidateEventLabRevealBarrier inside their existing transactions. It deletes an existing barrier only when source revision or mode differs from the current authorized tuple. It never inserts a barrier or takes an eligible-team snapshot. A same tuple keeps its already prepared cohort unchanged. Freeze remains at the original prepareTeamAssignment boundary; a replacement source/mode has no current preparation until that boundary establishes its cohort.

RED: normal pre-preparation source edit with zero eligible teams wrote a premature barrier(count1). The normal same-value all_ready config edit reproduced count1 after the test body preserved its existing MinTeamSize; the first combined run also recorded that unrelated incomplete-body fixture error explicitly. No production RED is inferred from that body error. Raw source/config behavioral failures are retained separately.

GREEN: real PostgreSQL production-flow regression performs config or source edit with zero eligible teams, verifies no barrier, admits/forms Blue and Red, enters the real stand assignment-preparation path, and verifies the exact two-team cohort. A late admitted Small team cannot alter the same prepared tuple. The first matching ready team cannot open it; the second opens it. Existing current replacement-generation source-switch and strict historical-ACK cohort tests remain green.

Commands/evidence (all GOWORK=off; bounded Go commands use GOFLAGS=-p=2):

- R1-red: go test -json -count=1 ./internal/useCase/event -run '^TestCohortFreezesAtPreparation' — exit1 (source premature-row RED; config fixture error captured).
- R1-config-red: same package -run '^TestCohortFreezesAtPreparation/same' — exit1, config premature-row RED.
- R1-sqlc: pinned sqlc1.31.1 generation — exit0; generated SQL/Querier plus mocks refreshed. No model/migration changes.
- R1-green: same package -run '^TestCohortFreezesAtPreparation|^TestReviewSourceSwitch|^TestAllReadyBarrierRejects' — exit0,5/5 test/subtest runs pass, zero skips.
- R1-build-vet: make build vet error-catalog-check — exit0, including error-code/layer/route checks.

Exact raw stdout/stderr/argv/cwd/exit/duration files are in /Users/volodymyrporokhniak/.codex/outputs/2026-10-08-lifecycle-execution/backend-cohort-residual. Hash/file inventories accompany them. No broad passing suite was repeated. DTOs/routes did not change, so Swagger regeneration was unnecessary.

Self-review checked the two edit call sites, tuple-conditional deletion, absence of eager insertion, original preparation Freeze, immutable same tuple conflict behavior, current-source/current-binding opening and first/second-team results. Root adjudicates this residual from the concrete evidence. Existing draft SDK/native/provenance, final Docker, authenticated API/browser and fresh OS-process mTLS River proof remain explicitly pending. Owned backend-END PostgreSQL/private fixture is unchanged.
