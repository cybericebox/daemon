# Event shared Lab lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. The owner already selected parallel agents in the current directories; preserve that method. Laboratory, daemon and frontend agents work in parallel after producer contracts are frozen. Do not reopen the execution-method choice.

**Goal:** Close a team's fully solved shared laboratory immediately, stop its actual runtime asynchronously with required snapshot protection and confirmed resource accounting, then complete stage, progressive manual and retention behavior without resurrecting solved laboratories.

**Architecture:** One canonical backend Lab aggregate owns every dependent question of one team's Lab generation; question bindings reference its stable ID. Its desired revision is a durable outbox, processed by River, while agent observations independently acknowledge actual stop and allocation release. Subsequent tasks reuse existing stage timing and `TaskRevealMode`, maintain one team LabGroup, and explicitly prepare only required unresolved Labs when group services restart.

**Tech Stack:** Go 1.27 toolchain, Gin, pgx/v5, sqlc 1.31.1, golang-migrate, River 0.48.0, gomock, PostgreSQL/testcontainers, Laboratory generated protobuf/gRPC.

**Spec:** `/Volumes/Projects/My/CyberICEBox/laboratory/docs/superpowers/specs/2026-10-08-lab-lifecycle-design.md`, owner-approved commit `f5c8bfb`. Read that specification and this plan before implementation. The Laboratory producer plan owns native stop/capture/restore, protocol generation and lower-limit experiments; this plan consumes those contracts.

## Global Constraints

- One LabGroup per participation unit/team; no group pooling or academy portal.
- Stop exactly one team's shared Lab generation only when every pinned question/flag that depends on that generation is complete.
- A solved laboratory is terminal for participant runtime; group resume, new stage, deploy retry and annulment never start it again.
- Stage closure stops, not immediately deletes. Retention expiry initiates deletion with separate confirmation.
- Required configured snapshots are mandatory; failure preserves every workload and runtime allocation.
- Participant closure is settled immediately; internal Snapshotting/Stopping is operator information. Preserve existing challenge `Closed` and returnable `Practice` meanings.
- Capacity credit requires UID, operation, revision and generation matching actual release confirmation. Unknown/stale observations are not free capacity.
- Preserve existing protobuf tags, readiness, scheduling, snapshot and traffic fields; new LabStatus tags 41/42, Lab.uid tag13, LabGroup.uid tag14, FeaturesResponse.lifecycle tag13, StopLabItem.terminal tag4 and later group status tags 9/10 belong to the producer.
- Preserve existing LabGroup suspended semantics. Full group service stop is a separate lifecycle operation, after all children stop and no starts remain pending.
- Reuse `TaskRevealMode` `all_ready` and `as_ready`; never equate it with rolling-roster JoinPolicy.
- Use planned participants, or configured maximum when membership may grow. Five is a documented sizing fallback, not an immutable product limit.
- Supported sizes remain conservative until the actual smaller-limit Linux tests pass; VPN80Mi/50m and gateway32Mi/25m are candidates, not supported defaults.
- Exercise-author test environments retain their existing policy. No moderator forced per-team stop API.
- Branch selected: `feat/event-lab-lifecycle`. Product and database edits start only after the owner reviews this plan. This plan itself is an authorized docs-only edit and is not committed yet.
- Local commits only after targeted checks and primary review. No push, PR, release, cloud resource creation, pseudo-version invention or unrelated cleanup.

## Review Focus

1. Two simultaneous final answers to different questions must create one stop intent, without deadlock or missed completion; Task3 real-Postgres concurrent test.
2. A late link response or stale lifecycle observation must never reopen access or credit allocation; Tasks4/5 link revision race and UID/revision/generation tests.
3. A stopped unresolved Lab must stay stopped when group services resume, while a solved Lab is never selected for stage preparation; Tasks4/8 explicit child-selection tests.
4. Node/agent outage, force-deleted Kubernetes objects and missing monitoring must leave allocation held/unknown; Tasks2/5 identity and freshness tests plus producer native proof.
5. Stage close and next-stage preparation can overlap; no group stop may race a pending start or delete retained data early; Tasks8/9 transaction/admission/TTL boundary tests.

## Ownership and dependency gates

| Work | Owner | Can run in parallel | Integration gate |
|---|---|---|---|
| StopLabs/StartLabs, required snapshot barrier, actual allocation/resource fields | Laboratory agent | Daemon domain/SQL and frontend schema preparation | Generated producer methods and fields frozen; native proof before end-to-end claim |
| Tasks1–6 below | Daemon agent | Producer and frontend tasks | Adapter begins after producer generation; shared DTO frozen before frontend integration |
| Participant settled closure and manager/admin actual views | Event/admin frontend agent | Daemon model/SQL work | REST schema in this plan; no one-question closure inference |
| Tasks7–9 below | Daemon + producer + frontend follow-up | Each owns its files | First delivery end-to-end passes before subsequent behavior is enabled |

The first delivery consists of Tasks1–6 plus the corresponding producer/frontend tasks. Tasks7–9 are also requested work; completing Task6 does not complete the broader model.

## File map

New domain package `internal/model/eventLab`: lifecycle aggregate, allocation value objects and unique error registry. New repository `internal/delivery/repository/eventLabRepo`, query `postgres/queries/event_labs.sql`, migration0160 for shared identity/outbox/pinned objectives. Existing `labBindingRepo` remains question mapping. New `internal/useCase/event/lab_lifecycle.go` owns transactional completion and background convergence, `lab_lifecycle_views.go` owns participant/admin projections, and `lab_lifecycle_access.go` owns environment guards. New River args/worker `internal/model/jobs/lab_lifecycle.go` and `internal/jobs/lablifecycle/lablifecycle.go`.

Later `lab_lifecycle_policy.go` owns configurable limits/snapshot/retention, `stage_lifecycle.go` owns stage/group desired transitions, and `lab_lifecycle_manual.go` owns explicit participant actions. `internal/model/infrastructure/fit.go` and adapter `features.go` consume versioned sizing; resource calendar remains reservation owner. Keep SQL/json/protobuf conversions in delivery packages. Do not restructure existing unrelated event code.

## Frozen backend and REST contract

Use `eventLabModel` for `github.com/cybericebox/daemon/internal/model/eventLab`. Application ports have no protobuf types:

```go
type LabLifecycleInfrastructure interface {
    StopLab(context.Context, eventLabModel.StopRequest) error
    StartLab(context.Context, eventLabModel.Target) error
    ObserveLab(context.Context, eventLabModel.Ref) (eventLabModel.Observation, error)
}
type GroupLifecycleInfrastructure interface {
    StopGroup(context.Context, eventLabModel.GroupTarget) error
    StartGroup(context.Context, eventLabModel.GroupTarget) error
    ObserveGroup(context.Context, string) (eventLabModel.GroupObservation, error)
}
```

Nil error on a command means accepted intent only. StopLab maps one item to producer StopLabs; StartLab maps to StartLabs. `Target` contains Ref, expected UID, operation UUID and revision; never select by event-wide labels for a stop.

Participant `Lab` is a nullable object for static questions/submissions; `EventExerciseID` is required on every question, including static ones. Runtime Lab is nonnull. Revision is a positive decimal string, never a JSON int64 number:

```go
type ParticipantLabView struct {
    ID, EventExerciseID uuid.UUID
    Revision string
    LogicalClosed bool
    CloseReason *string // solved|manual|stage|event; nil while open
    ClosedAt *time.Time
    RuntimeState string // preparing|ready|closed|unavailable
    CanStop, CanRestart bool
    SnapshotPolicy string // none|required; configured promise, never snapshot success
    RetentionUntil *time.Time
}
type SubmitChallengeResult struct {
    Correct, FirstSolve, Practice bool
    Lab *ParticipantLabView
}
type LabLinkView struct {
    URL string
    ExpiresAt time.Time
    LabID uuid.UUID
    Revision string
}
```

Serialize all object fields with the existing PascalCase convention; null fields are present, not omitted. `LogicalClosed=true` always produces `RuntimeState="closed"`, regardless of Snapshotting/Stopping/StopFailed. First delivery returns both manual capabilities false. Board questions add `EventExerciseID` and `Lab`; runtime Data retains existing fields and adds `Lab`; submission Data is `{Correct,FirstSolve,Practice,Lab}`. Link Data adds `LabID` and `Revision` to the existing URL/expiry shape. A closed runtime response has Ready=false and Access=[] without a live agent call; history/board remain readable.

Later manual routes use the current permission-gated `/api/events/:id/teams` router, with membership derived from the authenticated session:

```text
POST /api/events/:id/teams/labs/:labID/stop
POST /api/events/:id/teams/labs/:labID/restart
Body: {"Revision":"7","IdempotencyKey":"UUID"}
Data: {"Lab": ParticipantLabView}
```

No `/self` route segment or caller-supplied team ID. Existing challenge lab/link routes remain. Revision conflict returns409; solved restart and all_ready manual action return403 with unique registered domain codes. Idempotency mismatch/in-progress retains existing409 patterns. Static/foreign Lab ID is404. Do not introduce moderator force-stop routes.

Admin/manager Lab view contains stable ID/set identity and the following detailed object, distinct from participant Lab:

```go
type ManagedLabView struct {
    ID, EventExerciseID, TeamID uuid.UUID
    ExerciseName string
    Revision, ObservedRevision string
    Generation int32
    AgentUID string
    DesiredState string // Running|Stopped|Deleted
    ActualState string // Running|Snapshotting|Stopping|Stopped|StopFailed|Starting|Unknown|Deleting|Deleted
    CloseReason *string
    ClosedAt, ActualStoppedAt, RetentionUntil, ObservedAt *time.Time
    SnapshotState string // NotRequired|Pending|Succeeded|Failed|Unknown
    FailureCode, FailureMessage string
    Resources AllocationView
}
type ComputeView struct { CPUMillicores, MemoryBytes string }
type AllocationView struct {
    ConfiguredRequests, ConfiguredLimits, AllocatedRequests ComputeView
    ReleasedRequests ComputeView
    RuntimeState string // Allocated|Releasing|Released|Unknown
    ObservedAt, ReleasedAt *time.Time
    UsageAvailable bool
    Used ComputeView
    SnapshotQuotaBytes string
    StorageState string // None|Retained|DeleteRequested|CleanupPending|Deleted|Unknown
    PhysicalStorageBytesAvailable bool
    PhysicalStorageBytes string
}
```

All new int64 quantities use decimal strings. Current runtime/archive allocation survives desired-state changes. Failure details are available to existing event managers/admins only, using current route permissions.

Manager/admin `Labs` returns one row per canonical shared Lab, with `ChallengeID`/`ChallengeName` as the lowest UUID pinned question for existing reset/rescue route compatibility, `Questions:[{EventChallengeID,Name}]`, `Lab:ManagedLabView`, `Live:null|existing runtime`, existing `Status`/`Reason`. Do not duplicate allocation per question. StandDetail adds `Group:{Name,Revision,ObservedRevision,AgentUID,DesiredState,ActualState,Ready,ObservedAt,FailureCode,FailureMessage,Resources}`; desired/actual enums and Resources match managed Lab, and Ready requires actual group-services/network readiness. Participant views omit Group actual progress.

ManageResources/admin Stats add `Observation:{ObservedAt:null|RFC3339,Complete:boolean,Held:{CPUMillicores:string,MemoryBytes:string,SnapshotQuotaBytes:string},PendingStarts:ComputeView,GroupServices:ComputeView,PhysicalStorageBytesAvailable:boolean,PhysicalStorageBytes:string}`. Held includes PendingStarts/GroupServices already. Complete=false preserves conservative held ledger for stale/unknown contributors. Existing InUse/Free numeric compute fields remain compatible and use this held ledger; measured Used remains distinct. ReleasedRequests is credited only for identity-matching explicit release, never command acceptance.

### Task1: Shared aggregate, pinned membership and policy snapshot

**Files:** Create `internal/model/eventLab/lab.go`, `allocation.go`, `policy.go`, `errors.go`, `lab_test.go`, `policy_test.go`; create migrations `0160_event_labs.up.sql`/`.down.sql`, `queries/event_labs.sql`, `eventLabRepo/repository.go`, `repository_test.go`, `postgres/event_labs_integration_test.go`; modify `model/labBinding/binding.go`, `labBindingRepo/repository.go`, `queries/lab_bindings.sql`, `model/eventConfig/config.go`, configRepo/queries and handler config DTO, `useCase/event/useCase.go`, `useCase/event/stands.go:217-299`; regenerate postgres/models, queries, Querier and mocks.

**Interfaces:** Produces the types below and repository methods `Get(ctx,id) (Lab,error)`, `GetForChallenge(ctx,team,challenge) (Lab,error)`, `Lock(ctx,id) (Lab,error)`, `Create(ctx,lab,objectiveIDs) error`, `Update(ctx,lab,expectedRevision) (bool,error)`, `Complete(ctx,id) (bool,error)`, `ListDirty(ctx,now,limit) ([]Lab,error)`, `RecordObservation(ctx,id,observation) (bool,error)`. `GetForChallenge` is nonlocking; canonical Lock precedes every question lock. Single aggregate Update writes every mutable aggregate field in one statement; observations are the documented conditional narrow-write exception.

Full signatures live in `eventLabRepo` with context.Context, UUID identifiers, time.Time now, int32 limit, `eventLabModel` values and bool conditional-write results. Add `RecordInitialIdentity(ctx context.Context,id uuid.UUID,ref eventLabModel.Ref,uid string,generation int64,now time.Time) (bool,error)` solely when current AgentUID is empty, desired revision1/Running/not closed and deployment ref/generation match; this may adopt initial UID before any lifecycle operation exists, never a recreated UID. ListDirty excludes completed Stopped+Released and Deleted+confirmed retired rows; storage retention is processed by Task9 rather than treating every Stopped row as forever dirty. Failed/stale/unconfirmed operations stay due until retry/ack.

- [ ] Write pure domain tests first. Use fixed time and IDs; include this terminal transition test:

```go
func TestSolvedLabCannotRestart(t *testing.T) {
    now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
    lab := Lab{ID: uuid.Must(uuid.NewV7()), Revision: 1, DesiredState: "Running"}
    if err := lab.Close("solved", uuid.Must(uuid.NewV7()), now); err != nil { t.Fatal(err) }
    if lab.Revision != 2 || lab.ClosedAt == nil || lab.CloseReason != "solved" { t.Fatal(lab) }
    if err := lab.Start(uuid.Must(uuid.NewV7()), now.Add(time.Minute)); !errors.Is(err, ErrSolvedTerminal.Err()) { t.Fatal(err) }
    if lab.DesiredState != "Stopped" || lab.Revision != 2 { t.Fatal(lab) }
}
```

- [ ] Run `go test ./internal/model/eventLab -run TestSolvedLabCannotRestart -count=1`; expect compilation failure before the new package implementation.
- [ ] Implement domain fields and pointer-receiver methods. `Close` idempotently keeps an identical current close; first close increments revision once. `Start` rejects solved, requires retained state and a newer explicit operation; it clears logical closure for manual/stage restart only. `Observe` never changes desired intent and requires exact identity matching before release credit.

```go
type Ref struct { Group, Lab string }
type Target struct { Ref Ref; ExpectedUID string; OperationID uuid.UUID; Revision int64 }
type StopRequest struct { Target Target; SnapshotMode string; RetentionUntil *time.Time; Terminal bool }
type Compute struct { CPUMillicores, MemoryBytes int64 }
type Allocation struct {
    ConfiguredRequests, ConfiguredLimits, AllocatedRequests, Used Compute
    RuntimeState, StorageState string
    ObservedAt, ReleasedAt *time.Time
    UsageAvailable bool
    SnapshotQuotaBytes, PhysicalStorageBytes int64
    PhysicalStorageBytesAvailable bool
}
type Observation struct {
    Ref Ref; UID string; OperationID uuid.UUID; Revision, ObservedGeneration int64
    DesiredState, ActualState, SnapshotState, FailureCode, FailureMessage string
    StoppedAt, ObservedAt *time.Time
    RuntimeReady bool
    Allocation Allocation
    AccessFenced bool; AccessFencedAt *time.Time; AccessFenceVPNBootID string
}
type Lab struct {
    ID, EventID, TeamID, EventExerciseID uuid.UUID
    Ref Ref; VariantIndex, Generation int32; AgentUID string; AgentGeneration int64
    Revision, ObservedRevision int64; OperationID uuid.UUID
    DesiredState, ActualState, CloseReason, SnapshotMode, SnapshotState string
    ClosedAt, RetentionUntil, ProtectedUntil, ActualStoppedAt, ObservedAt *time.Time
    ObjectiveCount int32; Materialized bool
    RuntimeReady bool
    Allocation Allocation; FailureCode, FailureMessage string
    CreatedAt, UpdatedAt time.Time
}
type Policy struct { SnapshotMode string; MaxActiveLabsPerTeam *int32; RetentionMinutes int32 }
type NewInput struct {
    EventID, TeamID, EventExerciseID uuid.UUID
    Ref Ref; VariantIndex, Generation int32; ObjectiveIDs []uuid.UUID
    Policy Policy; RetentionUntil *time.Time
}
func New(in NewInput, now time.Time) (Lab,error)
```

The factory rejects empty/duplicate objectiveIDs, invalid refs/policy and nil scope IDs, generates UUIDv7/operation IDs and timestamps in domain, and never uses database defaults for identities. `eventConfig.EventConfig.LabPolicy` is added now so the first delivery can configure REQUIRED before preparation. API bounds/defaults are specified in Task7; policy validation/capture eligibility is implemented in Task1, manual routes remain Task7.

- [ ] Add the migration using explicit constraints and persisted objective membership, not the current visible board:

```sql
CREATE TABLE event_team_labs (
 id uuid PRIMARY KEY,
 event_id uuid NOT NULL,
 event_team_id uuid NOT NULL,
 event_exercise_id uuid NOT NULL,
 variant_index integer NOT NULL CHECK (variant_index >= 0),
 generation integer NOT NULL CHECK (generation >= 0),
 lab_group_name text NOT NULL,
 lab_name text NOT NULL,
 agent_uid text NOT NULL DEFAULT '',
 agent_generation bigint NOT NULL DEFAULT 0,
 desired_revision bigint NOT NULL DEFAULT 1 CHECK (desired_revision > 0),
 observed_revision bigint NOT NULL DEFAULT 0,
 operation_id uuid NOT NULL,
 desired_state text NOT NULL DEFAULT 'Running' CHECK (desired_state IN ('Running','Stopped','Deleted')),
 actual_state text NOT NULL DEFAULT 'Unknown',
 runtime_ready boolean NOT NULL DEFAULT false,
 close_reason text,
 logical_closed_at timestamptz,
 snapshot_mode text NOT NULL CHECK (snapshot_mode IN ('skip','required')),
 snapshot_state text NOT NULL DEFAULT 'Unknown',
 retention_until timestamptz,
 protected_until timestamptz,
 actual_stopped_at timestamptz,
 observed_at timestamptz,
 objective_count integer NOT NULL CHECK (objective_count > 0),
 materialized boolean NOT NULL DEFAULT false,
 allocation jsonb NOT NULL DEFAULT '{}',
 failure_code text NOT NULL DEFAULT '',
 failure_message text NOT NULL DEFAULT '',
 next_attempt_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 UNIQUE (lab_group_name, lab_name),
 UNIQUE (id, event_team_id)
);
CREATE TABLE event_lab_objectives (
 lab_id uuid NOT NULL REFERENCES event_team_labs(id),
 event_challenge_id uuid NOT NULL,
 PRIMARY KEY (lab_id,event_challenge_id)
);
ALTER TABLE lab_bindings ADD COLUMN lab_id uuid;
ALTER TABLE lab_bindings ADD CONSTRAINT lab_bindings_shared_lab_fk
 FOREIGN KEY (lab_id,event_team_id) REFERENCES event_team_labs(id,event_team_id);
CREATE INDEX event_team_labs_dirty_idx ON event_team_labs(next_attempt_at,id)
 WHERE desired_state <> 'Running' OR observed_revision < desired_revision;
```

Do not cascade this durable lifecycle row/objective snapshot on event/team deletion: final cleanup must survive those deletes. Deleted rows stay as audit references until platform retention. Store Allocation JSON only in repository conversion, with explicit configured/held/storage fields from the domain. Migration adds nullable LabID for existing bindings; existing assignments are backfilled by `ensureEventTeamLabForAssignment(ctx context.Context,repo IRepository,eventID,teamID,eventExerciseID uuid.UUID,now time.Time) (eventLabModel.Lab,error)` in the stand engine, using the domain New factory. It groups bindings by team+group+name+generation, recovers exercise/variant through pinned team challenges, creates all expected objective IDs, attaches existing bindings and sets materialized only when complete in one transaction. Missing canonical identity blocks new access/automatic completion until this engine repair; never skip a final solve's lifecycle intent silently. Legacy per-question names first follow existing shared-name reconcile; inconsistent generations stay failed/not complete. Add nonlocking identity + `LockEventTeamForLabAdmission` SQL queries to support this initializer and later manual admission. No database UUID default or external migration executable is required.

- [ ] Implement SQL locking/completion without child row locks:

```sql
-- name: LockEventTeamLab :one
SELECT * FROM event_team_labs WHERE id=sqlc.arg(id) FOR UPDATE;
-- name: IsEventTeamLabComplete :one
SELECT lab.materialized AND lab.objective_count > 0
 AND (SELECT count(*) FROM event_lab_objectives o WHERE o.lab_id=lab.id)=lab.objective_count
 AND NOT EXISTS (
   SELECT 1 FROM event_lab_objectives o
   LEFT JOIN lab_bindings b ON b.lab_id=o.lab_id AND b.event_challenge_id=o.event_challenge_id
   LEFT JOIN team_challenges tc ON tc.event_team_id=lab.event_team_id AND tc.event_challenge_id=o.event_challenge_id
   LEFT JOIN team_challenge_solves s ON s.team_challenge_id=tc.id
   LEFT JOIN team_challenge_practice_solves p ON p.team_challenge_id=tc.id
   WHERE o.lab_id=lab.id AND (b.id IS NULL OR b.generation<>lab.generation OR tc.id IS NULL OR (s.team_challenge_id IS NULL AND p.team_challenge_id IS NULL))
 ) AS complete FROM event_team_labs lab WHERE lab.id=sqlc.arg(id);
```

- [ ] Change `prepareTeamAssignment` to create one canonical row and full objective membership in the same existing transaction, then attach every question binding to it and finally set Materialized=true. Pin snapshot mode from the configured policy and retention from the event deadline. Consume producer FeaturesResponse.lifecycle before preparation: REQUIRED is refused before provisioning if any required container lacks configured persistence/capture capability. No silent persistence retrofit or discovering this incompatibility only after final solve. At solve request on an existing canonical-missing assignment, initialize/lock it transactionally before question mutation, or return explicit retryable repair error; never commit a fully solved set with no durable closure intent.
- [ ] Run `make sqlcGenerate`, `go generate ./internal/delivery/repository/postgres/`, domain/repository tests and real `postgres` integration tests. Add tests `TestEventLabsPinnedMembership`, `TestEventLabsIncompleteMaterializationNeverCompletes`, `TestEventLabsSurviveEventDeletion`, `TestEventLabsBackfillPreservesSharedIdentity` with actual DB rows/counts. Inspect generated code and up/down SQL. Commit locally after primary review: `feat: add shared event lab lifecycle aggregate`.

### Task2: Identity-fenced agent adapter and confirmed observations

**Files:** Create `internal/delivery/infrastructure/labagent/lifecycle.go`, `lifecycle_test.go`; modify `deploy.go:288-305`, `fleet.go`, `features.go`, `internal/model/exercise/deploy.go:68-101`, `internal/useCase/event/lab_lifecycle.go`; producer-owned generated files are not edited in daemon.

**Interfaces:** Consumes Ref/Target/StopRequest/Observation and generated StopLabs/StartLabs/LabStatus lifecycle/resource fields. Produces `LabLifecycleInfrastructure` implementation on Client/Fleet; `ObserveLab` routes existing placement and never creates or forgets placement.

- [ ] Write fake LabManager tests before implementation: exact one item/reference, required vs skip mapping, response accepted with no released acknowledgement, wrong UID/revision, absent lifecycle resources, unreachable group, switch-only stopped Lab. Preserve raw phase/access/traffic mapping.
- [ ] Run `go test ./internal/delivery/infrastructure/labagent -run 'TestLifecycle' -count=1`; expect missing methods/types until producer generation exists and adapter is added.
- [ ] Implement the exact Stop mapping:

```go
func (c *Client) StopLab(ctx context.Context, in eventLabModel.StopRequest) error {
    mode := labpb.StopSnapshotMode_STOP_SNAPSHOT_MODE_SKIP
    if in.SnapshotMode == "required" { mode = labpb.StopSnapshotMode_STOP_SNAPSHOT_MODE_REQUIRED }
    if in.SnapshotMode != "skip" && in.SnapshotMode != "required" { return eventLabModel.ErrSnapshotPolicyInvalid.Err() }
    until := int64(0)
    if in.RetentionUntil != nil { until = in.RetentionUntil.UnixMilli() }
    out, err := c.StopLabs(ctx, &labpb.StopLabsRequest{Items: []*labpb.StopLabItem{{
        Target: &labpb.LabLifecycleTarget{
            Ref: &labpb.ItemRef{LabGroup: in.Target.Ref.Group, Name: in.Target.Ref.Lab},
            OperationId: in.Target.OperationID.String(), LifecycleRevision: in.Target.Revision,
            ExpectedLabUid: in.Target.ExpectedUID,
        }, SnapshotMode: mode, RetentionUntilUnixMs: until, Terminal:in.Terminal,
    }}})
    if err != nil { return agentErr("stop lab", err) }
    return oneResult("stop lab", out.GetResults())
}
```

Start uses the same target in StartLabs; Observe lists exact existing Lab and maps UID/lifecycle/revision/observedGeneration/resources. Initial UID comes from new Lab.uid, including before a lifecycle exists; do not require a nonexisting stop observation to learn it. FeaturesResponse.lifecycle gates supported stop/required snapshot/group operations. Terminal=true only for solved closure and producer refuses any future start of that Lab UID; unresolved manual/stage closure uses false. Missing fields map Unknown and preserve held allocation. Do not fill Released because Ready=false, Access empty or Lab list entry missing. Require explicit node/runtime confirmation supplied by producer; force deletion on an unreachable node remains Unknown/Releasing.
- [ ] Add `ObservationMatches(lab Lab, o Observation) bool` in the domain with the following required comparison, then use it in repository conditional acknowledgement:

```go
func ObservationMatches(lab Lab, o Observation) bool {
    return lab.Ref == o.Ref && lab.AgentUID != "" && lab.AgentUID == o.UID &&
        lab.OperationID == o.OperationID && lab.Revision == o.Revision &&
        o.ObservedGeneration >= lab.AgentGeneration
}
```

Record actual initial UID/generation from first deploy observation before a stop command; an observation cannot adopt a different UID after binding generation replacement. Pin the expected observed-generation floor when the operation is accepted. Preserve preexisting allocation on any mismatch.
- [ ] Run local adapter tests in explicit producer workspace (Task6 commands); check absent field compatibility and `StopLab` returning nil leaves ActualState unchanged. Commit after review: `feat: adapt confirmed laboratory lifecycle`.

### Task3: Atomic all-objective completion and durable River worker

**Files:** Create `internal/useCase/event/lab_lifecycle.go`, `lab_lifecycle_test.go`, `lab_lifecycle_integration_test.go`, `internal/model/jobs/lab_lifecycle.go`, `internal/jobs/lablifecycle/lablifecycle.go`, `lablifecycle_test.go`; modify `team_challenge.go:95-284`, `solution_attempt.go:63-126`, `results_manage.go:194-246`, `useCase.go`, `internal/useCase/useCase.go`, `internal/jobs/registry.go`, `queries/event_lab_access_syncs.sql` and `queries/lab_bindings.sql`.

**Interfaces:** Produces `completeLabInTransaction(ctx context.Context, repo IRepository, teamID, challengeID uuid.UUID, now time.Time) (*ParticipantLabView,error)`, `lockChallengeLabInTransaction(ctx,repo,teamID,challengeID) (*eventLabModel.Lab,error)`, `ReconcilePendingLabLifecycles(ctx context.Context) error`, `SetLabLifecycleWake(func(context.Context) error)`. Static binding absence returns nil Lab without error. A deleted foreign challenge still follows existing not-found/authorization checks.

- [ ] Write red integration tests using existing `newStandFixture` and `attachSharedSet`. Extend its fake agent with `stopCalls []StopRequest`, `observations map[Ref]Observation`, `stopErr error`, `statusErr error`; methods only record/return accepted or explicit observations. Answer helper reads stored expected_flag and calls `SubmitChallenge` with fresh idempotency key and fixed ReceivedAt. Assert stopCalls remains zero during every SubmitChallenge, even on final answer.

```go
func submitStoredFlag(t *testing.T, f *standFixture, user, challenge uuid.UUID, at time.Time) event.SubmitChallengeResult {
    t.Helper()
    var flag string
    if err := f.db.Pool.QueryRow(context.Background(),
        `SELECT expected_flag FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=$2`,
        f.blueID, challenge).Scan(&flag); err != nil { t.Fatal(err) }
    out, err := f.uc.SubmitChallenge(context.Background(), f.eventID, user, challenge,
        event.SubmitChallengeInput{Answer:flag, IdempotencyKey:uuid.Must(uuid.NewV7()), ReceivedAt:at})
    if err != nil { t.Fatal(err) }; return out
}
```

Create the approved participant through this test-only helper before submitStoredFlag; the existing fixture already creates the captain user and formed/admitted team:

```go
func approveBlueCaptain(t *testing.T,f *standFixture,at time.Time) uuid.UUID {
    t.Helper(); ctx:=context.Background(); var user uuid.UUID
    if err:=f.db.Pool.QueryRow(ctx,`SELECT captain_id FROM event_teams WHERE id=$1`,f.blueID).Scan(&user);err!=nil { t.Fatal(err) }
    if _,err:=f.db.Pool.Exec(ctx,`INSERT INTO event_participants(event_id,user_id,status,team_id,team_role,created_at)
      VALUES($1,$2,2,$3,0,$4) ON CONFLICT(event_id,user_id) DO UPDATE SET status=2,team_id=$3,team_role=0`,
      f.eventID,user,f.blueID,at);err!=nil { t.Fatal(err) }
    return user
}
```

Assert first/second Lab.LogicalClosed=false, final true/reason solved; one desired revision advance; all three solves remain; red and another blue set unchanged. Add practice-only completion without scoreboard change, moderator accepted final solve, missing objective, wrong answer, replay, annulment staying terminal and agent outage.
- [ ] Run `go test ./internal/useCase/event -run 'TestLabLifecycle|TestSharedLabCompletion' -count=1`; expect missing fields/helper until implementation.
- [ ] Before GetTeamChallenge or attempt-for-decision locking, resolve/lock shared Lab. For `DecideSolutionAttempt`, add a nonlocking attempt identity query to find team/challenge first, lock Lab, then call existing locked decision query. Annulment and recreate use this same order. Multiple Labs are locked in UUID order; any event/team lock already required by admission precedes Lab locks. Never acquire question first then Lab in another path.
- [ ] After rated/practice solve write, evaluate Complete and invoke the exact transition:

```go
func (lab *Lab) Close(reason string, operationID uuid.UUID, now time.Time) error {
    if lab.CloseReason == "solved" || (lab.DesiredState == "Stopped" && lab.CloseReason == reason) { return nil }
    if lab.DesiredState == "Deleted" { return ErrLabDeleted.Err() }
    switch reason { case "solved", "manual", "stage", "event": default: return ErrCloseReasonInvalid.Err() }
    lab.DesiredState, lab.CloseReason = "Stopped", reason
    lab.OperationID, lab.Revision = operationID, lab.Revision+1
    lab.RuntimeReady = false
    lab.ClosedAt, lab.UpdatedAt = &now, now
    return nil
}
```

Save full aggregate + ACL dirty revision in the same transaction. Store Lab view in idempotent submission response and return it after commit. Replay refreshes the safe current Lab view under its stable ID if its stored view is older, so reopening a manually stopped unresolved Lab does not return an obsolete open/closed revision. Correct/FirstSolve/Practice replay values stay original.
- [ ] Use the aggregate's dirty desired revision as the durable work record. Add periodic bounded batch scan with NextAttemptAt; accepted stop remains dirty until actual acknowledgement. Worker observes current identity, requests the complete denied ACL revision, submits identical StopLab target and requires current-operation producer AccessFenced before capture/stop/release can be acknowledged. Producer stopped intent independently suppresses grants and waits for actual bidirectional conntrack revocation, so ACL background lag cannot let required capture pass before the access fence. Existing backend AppliedRevision means command accepted until Task4 changes its actual acknowledgement; it is not proof of physical denial. For unavailable/unsupported agent keep dirty and failure, never acknowledge stop. Failure on one Lab is collected and does not stop remaining items. Required snapshot failure stores StopFailed and retained allocation; retry preserves REQUIRED and operation identity.

```go
type LabLifecycleArgs struct{}
func (LabLifecycleArgs) Kind() string { return "lab_lifecycle" }
type IUseCase interface { ReconcilePendingLabLifecycles(context.Context) error }
type worker struct { river.WorkerDefaults[jobsModel.LabLifecycleArgs]; uc IUseCase }
func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.LabLifecycleArgs]) error {
    return w.uc.ReconcilePendingLabLifecycles(ctx)
}
func NewWorker(uc IUseCase) *worker { return &worker{uc:uc} }
```

Register only when laboratories configured, using 2-second default periodic interval, explicit config override, batch100 default and exponential durable retry10s to5m. Config names `EVENT_LAB_LIFECYCLE_INTERVAL`, `EVENT_LAB_LIFECYCLE_BATCH`, `EVENT_LAB_LIFECYCLE_RETRY_MIN`, `EVENT_LAB_LIFECYCLE_RETRY_MAX`. Wake after commit best effort; existing enqueuer uses Insert rather than InsertTx, so it cannot replace the durable row.
- [ ] Real concurrent test: two goroutines submit last two questions with separate transactions behind a start channel; timeout5s, both accepted, exactly one final stop revision/operation. Repeat lost wake/process restart by replacing UseCase and running periodic worker. Run integration with Docker and no mocks for SQL lock coverage. Commit after review: `feat: stop fully solved shared labs asynchronously`.

### Task4: Authoritative safe DTOs and closed access fencing

**Files:** Create `internal/useCase/event/lab_lifecycle_views.go`, `lab_lifecycle_access.go`, matching tests; modify `views.go:591-644`, `participant_board.go:374-406`, `lab_binding.go:30-49,69-126`, `lab_session.go:32-107`, `stand_devices.go`, `eventself/dto.go`, `eventself/handler.go`, `eventself/handler_test.go`, `queries/team_challenges.sql:26-66`, `queries/event_lab_access_syncs.sql:88-118`; regenerate Swagger and sqlc.

**Interfaces:** Produces `GetOwnLabLifecycle(ctx,eventID,userID,labID) (ParticipantLabView,error)`, `participantLabView(lab Lab) ParticipantLabView`, and frozen REST contract. Runtime authorization checks approved/admitted/formed membership before returning safe Lab; closed state bypasses agent reads but never bypasses ownership.

- [ ] Add DTO tests for exact nullability/decimal revision/static Lab=null and shared identity on all questions. Add domain projection test with ActualState=Snapshotting/StopFailed and ClosedAt set; both serialize RuntimeState=closed, CanRestart=false if solved.
- [ ] Implement settled projection:

```go
func participantLabView(lab eventLabModel.Lab) ParticipantLabView {
    out := ParticipantLabView{ID:lab.ID, EventExerciseID:lab.EventExerciseID,
        Revision:strconv.FormatInt(lab.Revision,10), ClosedAt:lab.ClosedAt,
        RuntimeState:"preparing", LogicalClosed:lab.ClosedAt!=nil}
    if lab.CloseReason!="" { reason:=lab.CloseReason; out.CloseReason=&reason }
    if lab.SnapshotMode=="required" { out.SnapshotPolicy="required" } else { out.SnapshotPolicy="none" }
    out.RetentionUntil=lab.RetentionUntil
    if out.LogicalClosed { out.RuntimeState="closed"; return out }
    switch lab.ActualState {
    case "Running": if lab.RuntimeReady { out.RuntimeState="ready" }
    case "Unknown", "StopFailed": out.RuntimeState="unavailable"
    }
    return out
}
```

SnapshotPolicy/RetentionUntil precede the closed return. RuntimeReady is taken from the current producer LabStatus.Ready only after restore/group/network readiness and valid identity/freshness; every new stop/start intent resets it false, so a historical ready binding cannot privately reopen a restarting Lab. First slice manual flags remain false. Later Task7 computes capability through policy gate, not frontend inference.
- [ ] ACL SQL requires canonical desired Running and logical_closed_at NULL for every shared binding. Existing prerequisite/stage/publication checks still apply. Link, raw-IP/access DTO, reset/rescue action and new session paths reject logical closed. Status/board/history stays available for closed Labs. First delivery does not depend on later StopLabGroups/StartLabGroups: expected group identity is ordinary new LabGroup.uid metadata, and per-Lab stopped intent supplies its own access fence.
- [ ] Extend complete-policy replacement with exact revision identity. New domain `AccessTarget` has `{Group,ExpectedGroupUID string;OperationID uuid.UUID;Revision int64}`; `AccessFenceObservation` has `{Group,ExpectedGroupUID,PolicyUID string;OperationID uuid.UUID;DesiredRevision,AppliedRevision,Generation,ObservedGeneration int64;State,VPNBootID string;ObservedAt time.Time}`. Adapter optional port `ReconcileLabGroupAccessRevision(ctx context.Context,group string,policies []labAccessModel.ClientPolicy,target eventLabModel.AccessTarget) error` writes producer policy op/revision/group UID; the existing ACL worker remains the only complete-policy writer. Repository `ObserveAccessFence(ctx context.Context,teamID uuid.UUID) (eventLabModel.AccessFenceObservation,error)` parses the current Monitoring.access_policies payload; no nonexistent ListPolicy RPC is introduced.

Producer fields: LabGroupAccessPolicy operation_id5, desired_revision6, generation7, policy_uid8, expected_group_uid9; status applied_revision6, operation_id7, vpn_boot_id8, preserving existing labels10 and status state/observed_generation. Store stable operation ID/expected group UID/policy fingerprint beside desired ACL revision. A boundary-derived runtime/stage policy change materializes a new desired revision/operation once when its normalized complete-policy fingerprint changes, before sending the RPC; retries preserve identical op/revision/policy. This avoids same-revision conflicting rules when existing stage epoch changes without a Request call. MarkApplied becomes physical acknowledgement only after this exact predicate and current/fresh VPN boot observation:

```go
func AccessFenceMatches(want AccessTarget,got AccessFenceObservation,currentVPNBootID string) bool {
    return got.Group==want.Group && got.ExpectedGroupUID==want.ExpectedGroupUID && got.PolicyUID!="" &&
        got.OperationID==want.OperationID && got.DesiredRevision==want.Revision && got.AppliedRevision==want.Revision &&
        got.State=="Applied" && got.Generation==got.ObservedGeneration &&
        currentVPNBootID!="" && got.VPNBootID==currentVPNBootID
}
```

Missing/stale/mismatched observation keeps the revision dirty. VPN only writes this acknowledgement after bidirectional conntrack revocation succeeds. Lab lifecycle also exposes current-operation access_fenced13/access_fenced_unix_ms14/access_fence_vpn_boot_id15; required capture and actual stop wait on that independently of ACL dirty lag. Consume those fields in Observation; identity matching plus current producer AccessFenced is required before release credit. Do not advertise zero-latency physical revocation. Add `TestACLAcceptedIsNotPhysicalFence`, `TestACLBoundaryGetsNewRevisionOnce`, `TestACLVPNRestartInvalidatesFence`, `TestLabStopWaitsForCurrentAccessFence`; first-delivery smoke observes both logical close and later actual fence timestamp.
- [ ] Link race: read and pin Lab revision under short transaction; issue link; re-read canonical desired revision immediately before return. If changed or closed, reject and return no link. Include LabID/Revision in allowed response for frontend stale-response fencing. Do not hold DB lock across signing/agent network read. Existing access session is withdrawn through complete ACL; consumer must refuse a late link if cache already has newer/closed Lab.
- [ ] Tests `TestClosedLabCannotOpenLink`, `TestLateLinkAfterFinalSolveRejected`, `TestACLClosedSharedLabRemovesOnlyOwningLab`, `TestGroupServiceResumeDoesNotRestartStoppedLab`, `TestClosedRuntimeReadNeedsNoAgent`, `TestSnapshotFailureIsSettledForParticipant`. Run focused eventself/use-case/ACL tests and Swagger generation. Commit: `feat: expose authoritative shared lab closure`.

### Task5: Held allocation ledger and conservative sizing consumption

**Files:** Create `internal/model/eventLab/allocation_test.go`, `internal/useCase/event/lab_allocations.go`, `lab_allocations_test.go`, `internal/delivery/repository/postgres/queries/event_lab_allocations.sql`; modify `internal/useCase/calendar.go:46-69`, `resourceCalendar/useCase.go`, `resourceCalendar/gate.go`, `event/resource_plan.go`, `event/reservation_gate.go`, `internal/model/infrastructure/fit.go`, adapter `features.go`; add migration0161 storage reservations, update resourceCalendarRepo/queries/models and integration tests.

**Interfaces:** Produces `HeldCompute(ctx,eventID) (Compute,error)`, `HeldStorage(ctx,eventID) (StorageBudget,error)`, `AdmitEventLabStart(ctx,eventID,teamID,labID uuid.UUID,need Compute,storageBytes int64) error`; extends Usage with retained Storage. `StorageBudget` has `SnapshotQuotaBytes int64`, `PhysicalStorageBytes int64`, `PhysicalKnown bool`. HeldCompute sums each shared Lab once, each team group overhead once and pending admitted starts; preserved allocation survives unknown observations. Existing reservation owner supplies event budget; actual cluster placement can independently queue/refuse.

- [ ] Red test explicit release conditions, with fixed allocation750m/512Mi and retained snapshot1Gi. Wrong UID/revision/gen, older timestamp, observation absent or runtime Unknown leaves held compute750m/512Mi. Matching Released observation after confirmed stopped sets compute0 and retains quota1Gi. Group overhead never decreases because one child stopped.

```go
func HeldCompute(lab Lab) Compute {
    if lab.ActualState=="Stopped" && lab.Allocation.RuntimeState=="Released" &&
       lab.ObservedRevision==lab.Revision && lab.Allocation.ReleasedAt!=nil {
        return Compute{}
    }
    return lab.Allocation.AllocatedRequests
}
```

Initial pending starts record their admitted configured requests before dispatch; no empty allocation default is permission to launch. A stale release can only reduce held compute when observation matches Task2 identity and newer timestamp, in the same conditional DB write. Storage Deleted requires explicit producer GC proof if physical bytes were known; manifest-delete acceptance changes state only.
- [ ] Extend reservation storage with nonnegative bigint columns for per-team, dynamic and total snapshot quota; add matching change-request/test-pool/test-hold columns and explicit conflict arithmetic. Unknown physical bytes remains visible unknown, not0 known. Reserve logical quota limits, not idle measured disk; physically known bytes are separate reporting evidence. Existing reservations backfill storage0 known-as-unconfigured, and new persistent Lab admission requires configured quota or explicit platform capacity; do not label old missing storage measurement as physical zero.
- [ ] Extend GroupPlan with planned U/L/I/P plus producer flow/rate profile identifier; add versioned sizing projection. Use eligible-agent componentwise maximum and existing preset rounding. Actual participant count for frozen roster, configured max when roster may grow; fallback `DefaultMaxTeamSize()`5 only if planning absent. Derive L from maximum simultaneous planned active sets and I from their Internet enablement, count one per set not per question. Immutable existing group size must fit maximum; refuse reduced active limit/config if created size would be insufficient rather than silently resize.
- [ ] Producer-advertised v2 `Validated=false`, missing F/rate envelope or outside envelope selects existing supported fallback. Only a validated exact profile can produce lower supported recommendations. Test `TestSizingUsesPlannedULIP`, `TestUnvalidatedReducedProfileUsesSupportedFallback`, `TestSizingFallsBackToFiveOnlyWhenUsersUnknown`, `TestCreatedGroupCannotBeSilentlyUndersized` with real formula inputs and results80/32Mi retained as candidate evidence only.
- [ ] Run allocation/calendar/sizing unit and DB storage integration tests. Test event budget fits but node queue reports InsufficientResources; neither Ready nor publication is granted by budget alone. Commit: `feat: account confirmed lab allocations and retained storage`.

### Task6: Reproducible independent SDK snapshot and first-delivery integration

**Files:** Create `third_party/laboratory-sdk/go.mod`, `PROVENANCE.json`, `SHA256SUMS`, `README.md`, `LICENSE`, `NOTICE`, package snapshots `pkg/agent/protobuf/{agent.proto,agent.pb.go,agent_grpc.pb.go}`, `pkg/agent/client/{client.go,count.go,terminating.go,client_test.go}`, `pkg/tlsreload/{tlsreload.go,tlsreload_test.go}`, `pkg/vpnprobe/{probe.go,probe_test.go}`; create `.github/scripts/sync-laboratory-sdk.py`, `.github/scripts/check-laboratory-sdk.py`; modify daemon `go.mod`, `Dockerfile`, `.github/scripts/lab-pin.sh` and image provenance checks to distinguish SDK source commit from runtime image version; test lifecycle integration and existing sharedLab/stage/ACL suites. Keep go.mod require v1.0.0 explicit; an equally explicit local replace selects the SDK snapshot. No release or invented published version.

**Interfaces:** Consumes a locally committed, clean Laboratory generated-contract commit and frozen frontend DTOs. Produces an independently buildable daemon with exact checked-in SDK source/protocol provenance, plus first-delivery evidence. It does not claim that published Laboratory v1.0.0 contains the new RPCs or that Tasks7–9 are complete.

- [ ] Write `TestLaboratorySDKMatchesProvenance`/check script first; expect absent bundle to fail. Scope decision for this whole plan: snapshot only the four packages actually imported by daemon or transitively by its client (about395KB before new fields), original source/proto/tests/license/NOTICE, no controller/operator source. Existing direct dependencies match grpc1.84.0, protobuf1.36.12 and genproto/googleapis/rpc `v0.0.0-20260928230214-8a89bd6388cc`; no new transitive dependency family is needed.
- [ ] Implement sync script with required positional source path and commit. It refuses dirty producer worktree or non-commit revision, resolves the complete40-character SHA, reads each file using `git show SHA:path`, writes unchanged bytes, preserves generated banners and Apache2 notices, and records `{Repository,SourceCommit,Files:{path:sha256},Generator:{protoc,protocGenGo,protocGenGoGRPC},CopiedAt}` in PROVENANCE.json. Generator versions come from generated banners and producer generation recipe. The checker recomputes every hash and verifies copied proto+generated methods include StopLabs/StartLabs and matching lifecycle/resource tags. Tests tamper with one copied source and one source SHA to prove failure. README documents intentional scope, synchronization and removal of replace only after a real owner-authorized distribution pin.

```python
from pathlib import Path
from datetime import datetime, timezone
import hashlib, json, re, subprocess, sys
source, revision = Path(sys.argv[1]).resolve(), sys.argv[2]
if subprocess.check_output(["git","-C",str(source),"status","--porcelain"]).strip():
    raise SystemExit("Laboratory snapshot source must be clean")
commit = subprocess.check_output(["git","-C",str(source),"rev-parse",revision+"^{commit}"]).decode().strip()
files = ["LICENSE","NOTICE","pkg/agent/protobuf/agent.proto","pkg/agent/protobuf/agent.pb.go",
 "pkg/agent/protobuf/agent_grpc.pb.go","pkg/agent/client/client.go","pkg/agent/client/count.go",
 "pkg/agent/client/terminating.go","pkg/agent/client/client_test.go","pkg/tlsreload/tlsreload.go",
 "pkg/tlsreload/tlsreload_test.go","pkg/vpnprobe/probe.go","pkg/vpnprobe/probe_test.go"]
dest = Path("third_party/laboratory-sdk")
hashes = {}
for name in files:
    data = subprocess.check_output(["git","-C",str(source),"show",commit+":"+name])
    target = dest/name
    target.parent.mkdir(parents=True,exist_ok=True)
    target.write_bytes(data)
    hashes[name] = hashlib.sha256(data).hexdigest()
proto = (dest/"pkg/agent/protobuf/agent.proto").read_text()
for method in ("StopLabs","StartLabs"):
    if not re.search(r"rpc\s+"+method+r"\s*\(",proto): raise SystemExit("missing lifecycle method "+method)
generator = {}
for name in ("agent.pb.go","agent_grpc.pb.go"):
    banner = (dest/"pkg/agent/protobuf"/name).read_text().splitlines()[:12]
    for line in banner:
        match = re.search(r"(protoc(?:-gen-go(?:-grpc)?)?)\s+(v[0-9.]+)",line)
        if match: generator[match.group(1)] = match.group(2)
if not all(name in generator for name in ("protoc","protoc-gen-go","protoc-gen-go-grpc")):
    raise SystemExit("generated toolchain provenance incomplete")
manifest = """module github.com/cybericebox/laboratory
go 1.27.0
require (
 google.golang.org/grpc v1.84.0
 google.golang.org/protobuf v1.36.12
 google.golang.org/genproto/googleapis/rpc v0.0.0-20260928230214-8a89bd6388cc
)
"""
(dest/"go.mod").write_text(manifest)
bundle = {"go.mod":hashlib.sha256((dest/"go.mod").read_bytes()).hexdigest()}
provenance = {"Repository":"https://github.com/cybericebox/laboratory","SourceCommit":commit,
 "Files":hashes,"BundleFiles":bundle,"Generator":generator,"CopiedAt":datetime.now(timezone.utc).isoformat()}
(dest/"PROVENANCE.json").write_text(json.dumps(provenance,indent=2)+"\n")
(dest/"SHA256SUMS").write_text("".join(digest+"  "+name+"\n" for name,digest in sorted((hashes|bundle).items())))
```

The check script validates the same stored Files/BundleFiles SHA256 values, required method/tag schema and SourceCommit format; synchronization is rerun only from a reviewed actual producer commit. Do not regenerate copied files with an unrecorded toolchain. No fabricated future commit is entered in this plan: execution resolves the actual producer's local generated-contract commit after its review.
- [ ] Create the minimal local module manifest and explicit root replace:

```go
module github.com/cybericebox/laboratory
go 1.27.0
require (
    google.golang.org/grpc v1.84.0
    google.golang.org/protobuf v1.36.12
    google.golang.org/genproto/googleapis/rpc v0.0.0-20260928230214-8a89bd6388cc
)
```

```bash
GOWORK=off go mod edit -replace=github.com/cybericebox/laboratory=./third_party/laboratory-sdk
python3 .github/scripts/check-laboratory-sdk.py
GOWORK=off go test ./internal/model/eventLab ./internal/delivery/infrastructure/labagent ./internal/useCase/event ./internal/jobs/lablifecycle
GOWORK=off go vet ./...
GOWORK=off go build ./...
```

Plain GOWORK=off now genuinely builds from the self-contained checkout. This is explicit SDK snapshot selection, not a change to/mutation of the remote v1.0.0 tag. `go list -m -json github.com/cybericebox/laboratory` must show the relative replacement. Extend Docker builder with `COPY third_party/laboratory-sdk ./third_party/laboratory-sdk` before `RUN go mod download`; run `RUN cd third_party/laboratory-sdk && sha256sum -c SHA256SUMS` before build, using Alpine's existing sha256sum rather than adding Python to the image. Docker source build uses GOWORK=off and needs no sibling repository.
- [ ] Add image label `org.cybericebox.laboratory.contract.source` from PROVENANCE SourceCommit while retaining explicit runtime Laboratory image/version identity separately. Update local compatibility check to require the actual Laboratory runtime implements the SDK LifecycleFeature before enabling lifecycle behavior. `.github/scripts/lab-pin.sh require-release` explicitly refuses while the SDK snapshot replace is selected, with `SDK snapshot selected; release requires an owner-approved runtime and contract distribution`. This prevents an existing release path from treating require v1.0.0 as the new SDK/runtime contract. Release remains a separate owner decision; never relabel a remote tag.
- [ ] Cross-check SDK snapshot against actual producer in a temporary local workspace; this comparison detects sync drift rather than serving as independent-repo proof:

```bash
task_daemon_root="$PWD"
task_lab_root="$(cd ../laboratory && pwd)"
task_module_dir="$(mktemp -d /tmp/cybericebox-lifecycle-modules.XXXXXX)"
go -C "$task_module_dir" work init "$task_daemon_root"
go -C "$task_module_dir" work edit -replace="github.com/cybericebox/laboratory=$task_lab_root"
GOWORK="$task_module_dir/go.work" go test ./internal/model/eventLab ./internal/delivery/infrastructure/labagent ./internal/useCase/event ./internal/jobs/lablifecycle
GOWORK="$task_module_dir/go.work" go vet ./...
GOWORK="$task_module_dir/go.work" go build ./...
```

- [ ] Run `GOWORK=off make vet`, `GOWORK=off make test`, SDK module tests and independent Docker build. Integration tests require Docker; self-skip is not pass evidence for DB/concurrency. Inspect that suites execute. Do not broaden native load experiments; consume producer actual lower-limit proof already running. Keep owned temporary paths in evidence and remove only that temporary directory after review.
- [ ] End-to-end three questions/two sets/two teams: final correct answer returns closed and unchanged scores/history, existing proxy/IP access withdrawn, stop accepted then confirmed, retained snapshots accounted; restart app/worker and poll to prove no resurrection. Required snapshot failed run leaves workload/resources and visible operator failure; successful retry restores barrier. Include producer native switch-only and UID/GID restore proof, consumer late-link UI test and supported-vs-unvalidated sizing report.
- [ ] Save local smoke fixture JSON outside tracked repos with `{EventID,EventTag,Teams:[{TeamID,SessionHandle,URL,SharedLab:{ID,Revision,EventExerciseID,EventChallengeIDs:[threeUUIDs]},UnrelatedLab:{ID,Revision,EventChallengeIDs}}],AdminSessionHandle,APIURL,EventURL}`. SessionHandle is an opaque locally supplied handle, not a credential written to repo/plan/log. Frontend uses it for real browser validation; mocks alone are not end-to-end proof. Raw expected flags stay in protected fixture DB/access, never in this plan artifact.
- [ ] Primary review of first-delivery branch before local commit. Commit verified changes with `feat: complete shared lab auto stop vertical slice`; use `git add -f` only for this reviewed plan because existing docs/ is ignored. Explicitly distinguish independent SDK-bundle build, actual runtime contract verification and remote distribution status. Subsequent delivery remains outstanding.

### Task7: Existing mode wiring and progressive manual policy

**Files:** Create `internal/useCase/event/lab_lifecycle_policy.go`, `lab_lifecycle_manual.go`, tests; extend Task1 `eventLab/policy.go`/tests; modify `model/eventConfig/config.go`, `event/config.go`, configRepo/queries, `event/stands.go`, `queries/team_challenges.sql`, `eventself/handler.go`; migration0162 mode barriers; producer receives later StartLabs calls.

**Interfaces:** Adds policy and manual command:

```go
type ManualLabInput struct { ExpectedRevision int64; IdempotencyKey uuid.UUID }
func (u *EventUseCase) StopOwnLab(ctx context.Context,eventID,userID,labID uuid.UUID,in ManualLabInput) (ParticipantLabView,error)
func (u *EventUseCase) RestartOwnLab(ctx context.Context,eventID,userID,labID uuid.UUID,in ManualLabInput) (ParticipantLabView,error)
```

Event API config uses Task1 nested `LabPolicy`: SnapshotMode skip|required, MaxActiveLabsPerTeam number|null, RetentionMinutes number. Unspecified values resolve to skip, null(no policy active limit) and existing `StandTiming.TeardownDelayMinutes`60-minute fallback. An explicit retention value always wins;60 is never imposed over it. Platform defaults are `EVENT_LAB_SNAPSHOT_MODE`, `EVENT_LAB_MAX_ACTIVE` (0=null), `EVENT_LAB_RETENTION_MINUTES`. Range active1..1000 ornull, retention0..10080 minutes, immutable policy snapshot per deployed generation. Use one canonical retention source for legacy stand timing and new policy update; old field maps to fallback while explicit policy stays authoritative. Stages later add nullable retention override. Policy changes apply to newly prepared generations; retained operations cannot weaken REQUIRED. Future-needed retention pins in Task9 override expiration until scheduled work completes.

- [ ] Write red policy/manual tests for unauthorized/foreign team, all_ready manual deny, solved terminal deny even as_ready, as_ready unresolved stop preserves progress/score, stale revision, replay, active limit, retained expiry, event budget and physical cluster wait. Register unique errors in eventLab errors registry and update error-catalog via existing commands.
- [ ] Implement manual action in one transaction: auth/admitted/formed/team ownership and stage reachability; lock event/team admission row then shared Lab; reserve request idempotency; compare requested revision; reject solved/expired; check `TaskRevealMode==RevealAsReady`; close manual or record newer explicit Running intent. Restart reserves pending allocation before changing intent, rechecks active count including pending/unknown Labs, and queues group start before child start if group stopped. No RPC in route handler or mutation transaction.
- [ ] Pin admission/publication by mode using new per-set/stage readiness barrier rows, not existing event-level sticky barrier alone. `all_ready` snapshot the fixed eligible-team IDs for the set/stage before preparing and reserve all copies; open all ready copies in one SQL statement only when all frozen eligible copies ready with complete restore/network readiness. Solve releases of one team never private-publish another set. `as_ready` publishes ready own copies independently. Moderators remain hidden and policy-consistent; late roster policy is separate.

```go
func CanManual(mode eventConfigModel.TaskRevealMode, lab eventLabModel.Lab, reachable bool, retained bool) (stop,restart bool) {
    if mode!=eventConfigModel.RevealAsReady || !reachable || lab.CloseReason=="solved" || !retained { return false,false }
    return lab.DesiredState=="Running", lab.DesiredState=="Stopped" && lab.CloseReason=="manual"
}
```

Budget/active-limit/placement readiness additionally gates restart; CanRestart can be false with a participant-safe disabled reason in a later explicit contract extension, not raw agent details.
- [ ] Test actual two-team delayed-ready publication under each existing mode and no subset publication from solved resource release. Add HTTP tests for frozen exact routes/body and permission gates. Run domain, event, route/error lints and Swagger. Commit: `feat: wire event lab reveal and progressive controls`.

### Task8: Stage pause, full group services stop and explicit next-stage preparation

**Files:** Create `internal/useCase/event/stage_lifecycle.go`, tests, group domain/repo/query files and migration0163; modify `stages.go`, `stands_schedule.go`, `stands.go`, `lab_access.go`, `queries/event_lab_access_syncs.sql`, `eventLabRepo`, adapter lifecycle.go. Laboratory agent owns StopLabGroups/StartLabGroups and group tags9/10.

**Interfaces:** Produces `ReconcileStageLabLifecycle(ctx context.Context,eventID uuid.UUID,now time.Time) error`; consumes existing `PlanStageDeploy`, stage phase/epoch and GroupLifecycleInfrastructure. New GroupTarget `{Group string; ExpectedUID string; OperationID uuid.UUID; Revision int64}` and GroupObservation `{Group,UID string; OperationID uuid.UUID; Revision,ObservedGeneration int64; ActualState string; ObservedAt *time.Time; Allocation Allocation}` are declared in `eventLab/group.go`. Group desired row has explicit revision, UID, admitted pending-start count and held allocation.

- [ ] Write red real-DB/fake-agent tests: stage close snapshots/stops unresolved children, solved children remain terminal, returnable question practice submission/scoring still works, no group stop while any child unknown/stopping/failed or start pending. Pause only when no whole-event/returnable runtime currently authorized and no next-stage preparation in progress; an active whole-event set prevents whole-group pause. Closing stage alone never DeleteLabs.
- [ ] At the nonreturnable/returnable stage boundary record Stage closure desired Stopped on only its running unresolved Labs, request ACL revision, pin retention, keep objective/score data. Returnable practice retains current score/submit semantics; do not introduce participant stage-restart powers. Restoration of stage-stopped unresolved Labs is the system's explicit next-stage selection only. At event finish close all active children event reason; solved reason never overwritten.
- [ ] Group stop uses current confirmed children and pending admission under a common group/team lock; only all Stopped+Released/no pending starts allows desired group Stopped. Require producer `require_all_labs_stopped=true`; old suspended toggles never substitute. ACL worker must respect full group stopped intent and not unconditionally call SetLabGroupSuspended(false)/EnsureVPNGroup to resume services.
- [ ] At existing `standSchedule`/PlanStageDeploy DueAt for the next stage, lock group and reserve intended starts. Request explicit group Running; wait full VPN/gateway/network readiness; select only exact next-stage unresolved needed Lab IDs, then explicitly request new child Running revisions. Solved IDs and manually stopped Labs not selected by that stage's explicit plan remain Stopped. Prepare new Labs or restore retained stopped Labs; pending allocation counted before commands. Preparation duration includes group services, snapshot restore and wiring; Running command acceptance or Pod Running never opens questions.
- [ ] Test race group-stop pass vs next-stage due pass; one common lock prevents stop after pending starts recorded. Add short break, future-stage additions, timezone-independent exact boundary, group restart child fence and unsupported group capability tests. Keep existing stage deployment lead tests and baseline returnable tests. Run producer group native proof plus daemon stage/ACL integration; commit `feat: pause team lab groups between stages`.

### Task9: Configured retention and confirmed deletion without score loss

**Files:** Create `internal/useCase/event/lab_lifecycle_retention.go`, tests; modify `lab_cleanup.go`, `lab_group_sweep.go`, `stands_manage.go:191-313`, `event_exercise_stands.go`, `labagent/fleet.go:339-354`, group/lab repo queries, stage DTO/config; migration0164 generation archives/future-dependency pins/audit retention.

**Interfaces:** Produces `ReconcileLabRetention(ctx context.Context,now time.Time) error`; stage API adds `LabRetentionMinutes *int32` (null inherits event policy). Base deadline is close time + explicit configured TTL for stage artifacts, and event effective finish + event TTL for whole-event sets;60-minute legacy default is only fallback. Effective deadline is max(base deadline, ProtectedUntil). Future scheduled dependence is pinned before stage closure, not first noticed near next-stage DueAt. Persist `event_lab_retention_pins(lab_id,stage_id,generation,needed_from,needed_until)` as soon as a future stage explicitly selects an unresolved retained Lab; needed_from=its preparation DueAt and needed_until=its stage closes_at plus its configured retention TTL. Group identity/services carry a matching pin through every future stage's closes_at+TTL. Recompute pins transactionally with schedule/set changes; solver terminal transition removes restart eligibility but keeps history retention. Retention sweeper requires no unexpired pin before initiating deletion. No resurrection to prolong TTL, no deleting a paused next-stage-needed Lab at stage end/old60min expiry. A manual request after actual unpinned expiry is refused, never pretends retained state exists.

- [ ] Red tests at now=deadline−1µs/deadline/deadline+1µs: no delete early; due delete accepted does not set Deleted or release storage/placement; explicit absent runtime plus producer storage GC confirms separate components. Event/team delete preserves durable row and exact original agent placement. Accepted DeleteRepo alone leaves storage CleanupPending. Annulment/score/history remain intact.
- [ ] Persist future-dependency pins and archived retained generations with the exact keys before enabling retention sweeps:

```sql
CREATE TABLE event_lab_retention_pins (
 lab_id uuid NOT NULL REFERENCES event_team_labs(id),
 stage_id uuid NOT NULL,
 generation integer NOT NULL,
 needed_from timestamptz NOT NULL,
 needed_until timestamptz NOT NULL,
 PRIMARY KEY(lab_id,stage_id,generation),
 CHECK(needed_until>=needed_from)
);
CREATE TABLE event_lab_generations (
 lab_id uuid NOT NULL REFERENCES event_team_labs(id),
 generation integer NOT NULL,
 lab_group_name text NOT NULL,
 lab_name text NOT NULL,
 agent_uid text NOT NULL,
 operation_id uuid NOT NULL,
 lifecycle_revision bigint NOT NULL,
 retention_until timestamptz,
 protected_until timestamptz,
 actual_state text NOT NULL,
 allocation jsonb NOT NULL,
 PRIMARY KEY(lab_id,generation),
 UNIQUE(lab_group_name,lab_name)
);
-- name: ListDueRetainedEventLabs :many
SELECT lab.* FROM event_team_labs lab
WHERE lab.desired_state='Stopped' AND lab.retention_until<=sqlc.arg(now)
 AND (lab.protected_until IS NULL OR lab.protected_until<=sqlc.arg(now))
 AND NOT EXISTS(SELECT 1 FROM event_lab_retention_pins pin WHERE pin.lab_id=lab.id
     AND pin.generation=lab.generation AND pin.needed_until>sqlc.arg(now))
ORDER BY lab.retention_until,lab.id LIMIT sqlc.arg(limit_val);
```

Stage deletion/update removes or changes only its exact dependency pins after recomputing all future needs in one transaction. A group pin table uses `(event_team_id,stage_id)` and the same needed times; retained group survives its last future scheduled need even with no current child running. Add tests scheduling a needed Lab four hours after a stage closes with explicit TTL30min and fallback60min; both remain retained through that future stage's closing+TTL, while an unneeded Lab expires as configured.
- [ ] Due retention transaction fences generation/revision, records desired Deleted and deletion operation, queues work; worker invokes existing explicit DeleteLab/DeleteGroup only after required stop barrier/actual state criteria. Observe exact UID/runtime gone and known registry cleanup separately. Preserve placement until actual absent group and all allocations retired. One aggregate's deletion failure does not block others.
- [ ] Recreate/source-version updates lock canonical Lab generation; solved remains terminal. New generations preserve history and explicitly track old retained generations so stale-lab cleanup excludes owned retained references. Moderator recreate cannot erase retention silently or restart solved runtime. Existing orphan sweep consumes retained group ownership until confirmed deletion.
- [ ] Run migration/generated checks, focused retention/cleanup/recreate/sweep SQL and event history tests, then whole affected suites under explicit local module integration. Independent whole-branch reviewer validates mode/stage/manual/snapshot/accounting/retention coverage; primary performs canonical merge-combination checks before local integration. Commit after review `feat: retain stopped labs until confirmed retirement`.

## Self-review and final proof checklist

Written-plan review performed on2026-10-08: all master-spec sections mapped to the tasks below; no unresolved-marker matches; participant Revision/nullable Lab/closed-state contract agreed with consumer agent; per-Lab terminal flag and pre-lifecycle UID source aligned with producer; SDK snapshot scoped to imported packages and independent Docker/off-workspace build; manual stage restart removed; future-stage retention pins added. This records review of the plan, not implementation/test completion.

- [ ] Every master-spec section is mapped: completion/tasks1–4; producer contract/task2; snapshot producer+tasks2/3/6; accounting/sizing/task5+producer proof; stage/modes/manual/tasks7/8; retention/task9; UI consumers/task4+separate frontend plan; no academy/moderator powers/global constraints.
- [ ] Scan plan/source for unresolved markers, mismatched ID/Revision fields and unsafe int64 JSON. Check every later method/type against the declarations above and producer generated names before writing consumers.
- [ ] Review Focus tests executed, including real two-final-answer lock and group-stop/start race; no testcontainer self-skip presented as proof.
- [ ] First delivery physically works locally and preserves current stage/practice behavior before later lifecycle stage behavior is enabled. Subsequent delivery has actual all_ready/as_ready behavior, manual unresolved restart, group pause/preparation and due confirmed retention deletion.
- [ ] Smaller resource envelope is proved on actual smaller limits or remains explicitly unvalidated/conservative; no finite promise based only on U/L.
- [ ] Report plain GOWORK=off/Docker SDK-bundle build, producer-workspace parity and actual runtime LifecycleFeature separately from unchanged remote v1.0.0 tag. Source hashes/license/NOTICE verified; no unbuildable pseudo-version or false release label, no release/push performed.
- [ ] Owner reviews this written plan before any product/database edits. Selected parallel execution method is preserved; primary coordinates contract integration and local commits.

Docs checkpoint when the primary approves its local commit (do not execute while this plan is still under review):

```bash
git add -f docs/superpowers/plans/2026-10-08-event-lab-lifecycle.md
git commit -m "docs: plan event shared lab lifecycle"
```

Do not edit the global docs ignore policy or include product/database changes in this docs-only checkpoint.
