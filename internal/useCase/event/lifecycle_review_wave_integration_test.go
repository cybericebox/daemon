package event_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/eventself"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRevealRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/resourceCalendarRepo"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/cybericebox/daemon/internal/model/rbac"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type waveProtection struct{}

func (waveProtection) RequirePermission(rbac.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {}
}
func waveProgressive(t *testing.T, f *standFixture) {
	t.Helper()
	ctx := context.Background()
	cfg, err := eventConfigRepo.New(f.db.Queries).Get(ctx, f.eventID)
	require.NoError(t, err)
	expected := cfg.UpdatedAt
	cfg.TaskRevealMode = eventConfigModel.RevealAsReady
	n, err := eventConfigRepo.New(f.db.Queries).Update(ctx, cfg, expected)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
}
func waveStopped(t *testing.T, f *standFixture, id uuid.UUID, reason string) eventLabModel.Lab {
	t.Helper()
	ctx := context.Background()
	repo := eventLabRepo.New(f.db.Queries)
	l, err := repo.Get(ctx, id)
	require.NoError(t, err)
	at := time.Now().UTC()
	if l.AgentUID == "" {
		ok, err := repo.RecordInitialIdentity(ctx, l.ID, l.Ref, "uid-"+l.Ref.Group+"/"+l.Ref.Lab, 1, at)
		require.NoError(t, err)
		require.True(t, ok)
		l, err = repo.Get(ctx, id)
		require.NoError(t, err)
	}
	before := l.Revision
	require.NoError(t, l.Close(reason, uuid.Must(uuid.NewV7()), at))
	ok, err := repo.Update(ctx, l, before)
	require.NoError(t, err)
	require.True(t, ok)
	at = at.Add(time.Millisecond)
	o := eventLabModel.Observation{Ref: l.Ref, UID: l.AgentUID, Generation: l.AgentGeneration, ObservedGeneration: l.AgentGeneration, OperationID: l.OperationID, Revision: l.Revision, DesiredState: "Stopped", ActualState: "Stopped", SnapshotState: "NotRequired", AccessFenced: true, AccessFencedAt: &at, AccessFenceVPNBootID: "fixture-boot", StoppedAt: &at, ObservedAt: &at, Allocation: eventLabModel.Allocation{RuntimeState: "Released", ReleasedAt: &at, ObservedAt: &at, StorageState: "None"}}
	ok, err = repo.RecordObservation(ctx, id, o)
	require.NoError(t, err)
	require.True(t, ok)
	l, err = repo.Get(ctx, id)
	require.NoError(t, err)
	return l
}
func TestReviewHistoricalManagedReadyDeniesRuntimeAndSigningUntilExactAck(t *testing.T) {
	f, set, user, _ := prepareLifecycle(t)
	ctx := context.Background()
	waveProgressive(t, f)
	signer := &recordingSessions{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)), Infra: webStatusAgent{f.agent}, InfrastructureCapability: standCapability{}, Topologies: standTopologies{deviceID: f.infraDevice, taskID: f.infraTaskID, sets: f.sets}, LabSessions: signer, VPN: &recordingVPNStore{config: "stored"}})
	uc.SetLifecycleControls(true)
	_, err := f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET observed_revision=0 WHERE event_team_id=$1`, f.blueID)
	require.NoError(t, err)
	runtime, err := uc.GetOwnChallengeRuntime(ctx, f.eventID, user, set.challenges[0])
	require.NoError(t, err)
	require.False(t, runtime.Status.Ready)
	require.Empty(t, runtime.Status.Access)
	require.False(t, runtime.Lab.CanStop)
	_, err = uc.OpenOwnLabLink(ctx, f.eventID, user, set.challenges[0], "web", 80)
	require.Error(t, err)
	require.Empty(t, signer.sessions)
	certifyLifecycleFixture(t, f)
	runtime, err = uc.GetOwnChallengeRuntime(ctx, f.eventID, user, set.challenges[0])
	require.NoError(t, err)
	require.True(t, runtime.Status.Ready)
	require.NotEmpty(t, runtime.Status.Access)
	require.True(t, runtime.Lab.CanStop)
	link, err := uc.OpenOwnLabLink(ctx, f.eventID, user, set.challenges[0], "web", 80)
	require.NoError(t, err)
	require.NotEmpty(t, link.URL)
	require.Len(t, signer.sessions, 1)
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	require.NoError(t, err)
	_, err = f.db.Pool.Exec(ctx, `UPDATE lab_bindings SET lab_id=NULL WHERE lab_id=$1`, lab.ID)
	require.NoError(t, err)
	runtime, err = uc.GetOwnChallengeRuntime(ctx, f.eventID, user, set.challenges[0])
	require.NoError(t, err)
	require.Nil(t, runtime.Lab)
	require.True(t, runtime.Status.Ready)
	_, err = uc.OpenOwnLabLink(ctx, f.eventID, user, set.challenges[0], "web", 80)
	require.NoError(t, err)
}
func TestReviewRealRuntimeRoutePreservesBoardCapabilitiesAtEqualRevision(t *testing.T) {
	f, set, user, _ := prepareLifecycle(t)
	ctx := context.Background()
	waveProgressive(t, f)
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	require.NoError(t, err)
	_, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET desired_revision=12,observed_revision=12 WHERE id=$1`, lab.ID)
	require.NoError(t, err)
	uc := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)), Infra: webStatusAgent{f.agent}, InfrastructureCapability: standCapability{}, Topologies: standTopologies{deviceID: f.infraDevice, taskID: f.infraTaskID, sets: f.sets}})
	uc.SetLifecycleControls(true)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(rbac.ContextWithCurrentUserSession(c.Request.Context(), rbac.Claims{UserID: user}))
	})
	eventself.NewEventSelfAPIHandler(uc, waveProtection{}).Init(router.Group("/api"), func(c *gin.Context) {})
	get := func(path string) json.RawMessage {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var body struct{ Data json.RawMessage }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		return body.Data
	}
	prefix := "/api/events/" + f.eventID.String() + "/teams/"
	var board struct {
		Challenges []struct {
			ID  uuid.UUID `json:"EventChallengeID"`
			Lab *event.ParticipantLabView
		}
	}
	require.NoError(t, json.Unmarshal(get(prefix+"challenges/mine"), &board))
	var expected *event.ParticipantLabView
	for _, q := range board.Challenges {
		if q.ID == set.challenges[0] {
			expected = q.Lab
		}
	}
	require.NotNil(t, expected)
	require.True(t, expected.CanStop)
	require.Equal(t, "12", expected.Revision)
	var runtime struct{ Lab *event.ParticipantLabView }
	require.NoError(t, json.Unmarshal(get(prefix+"challenges/"+set.challenges[0].String()+"/lab"), &runtime))
	require.Equal(t, expected, runtime.Lab)
	// The consumer's equal-revision latest-answer rule is safe because the actual
	// route supplies the same authoritative capabilities, without boolean OR.
	remembered := expected
	if runtime.Lab.Revision == remembered.Revision {
		remembered = runtime.Lab
	}
	require.True(t, remembered.CanStop)
	lifecycle, err := uc.GetOwnLabLifecycle(ctx, f.eventID, user, lab.ID)
	require.NoError(t, err)
	// Compare the complete public contract. UTC and Local can encode the same
	// instant identically while retaining different internal time.Location values.
	wantWire, err := json.Marshal(remembered)
	require.NoError(t, err)
	gotWire, err := json.Marshal(lifecycle)
	require.NoError(t, err)
	require.JSONEq(t, string(wantWire), string(gotWire))
}
func TestReviewRestartCannotSpendOtherTeamsPlacementSlot(t *testing.T) {
	f, ids := allocationFixture(t, 0)
	ctx := context.Background()
	need := eventLabModel.Compute{CPUMillicores: 750, MemoryBytes: 512 << 20}
	require.NoError(t, f.uc.AdmitEventLabStart(ctx, f.eventID, f.blueID, ids[0], need, 0))
	f.shiftLifecycle(t, -time.Minute, time.Hour)
	user := approveBlueCaptain(t, f, time.Now())
	waveProgressive(t, f)
	_, err := f.db.Pool.Exec(ctx, `UPDATE team_challenges SET readiness=2 WHERE event_team_id=$1`, f.blueID)
	require.NoError(t, err)
	l := waveStopped(t, f, ids[0], "manual")
	r, err := resourceCalendarRepo.New(f.db.Queries).GetEventReservation(ctx, f.eventID)
	require.NoError(t, err)
	at := time.Now()
	require.NoError(t, r.Recalculate(2, calModel.Amount{CPUMillicores: 1000, MemoryBytes: 896 << 20}, calModel.Amount{}, 0, calModel.Amount{}, at))
	r.SetPlacement([]calModel.Share{{AgentID: uuid.Must(uuid.NewV7()), Units: 1}, {AgentID: uuid.Must(uuid.NewV7()), Units: 1}}, 0, at)
	ok, err := resourceCalendarRepo.New(f.db.Queries).UpdateReservation(ctx, r)
	require.NoError(t, err)
	require.True(t, ok)
	f.uc.SetLifecycleControls(true)
	_, err = f.uc.RestartOwnLab(ctx, f.eventID, user, l.ID, event.ManualLabInput{ExpectedRevision: l.Revision, IdempotencyKey: uuid.Must(uuid.NewV7())})
	require.Error(t, err, "global2000fits1125, but exact team slot1000 does not")
	current, err := eventLabRepo.New(f.db.Queries).Get(ctx, l.ID)
	require.NoError(t, err)
	require.Equal(t, l.Revision, current.Revision)
	require.Equal(t, "Stopped", current.DesiredState)
	require.NoError(t, r.Recalculate(2, calModel.Amount{CPUMillicores: 1125, MemoryBytes: 896 << 20}, calModel.Amount{}, 0, calModel.Amount{}, at))
	ok, err = resourceCalendarRepo.New(f.db.Queries).UpdateReservation(ctx, r)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = f.uc.RestartOwnLab(ctx, f.eventID, user, l.ID, event.ManualLabInput{ExpectedRevision: l.Revision, IdempotencyKey: uuid.Must(uuid.NewV7())})
	require.NoError(t, err)
}

func TestReviewOrdinaryStageRetentionOverridesIncludeStoppedAndSolved(t *testing.T) {
	for _, minutes := range []*int32{nil, waveMinutes(0), waveMinutes(15), waveMinutes(240)} {
		for _, reason := range []string{"running", "manual", "solved"} {
			t.Run(fmt.Sprintf("ttl-%v-%s", minutes, reason), func(t *testing.T) {
				f := newStandFixture(t)
				ctx := context.Background()
				now := time.Now().UTC().Truncate(time.Microsecond)
				stageID := uuid.Must(uuid.NewV7())
				closes := now.Add(20 * time.Minute)
				_, err := f.db.Pool.Exec(ctx, `INSERT INTO event_stages(id,event_id,name,opens_at,closes_at,returnable,lab_retention_minutes,created_at,updated_at) VALUES($1,$2,'Origin',$3,$4,true,$5,$6,$6)`, stageID, f.eventID, now.Add(10*time.Minute), closes, minutes, now)
				require.NoError(t, err)
				_, err = f.db.Pool.Exec(ctx, `UPDATE event_exercises SET stage_id=$2 WHERE id=(SELECT event_exercise_id FROM event_challenges WHERE id=$1)`, f.infraOne, stageID)
				require.NoError(t, err)
				f.pass(t)
				lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, f.infraOne)
				require.NoError(t, err)
				if reason != "running" {
					lab = waveStopped(t, f, lab.ID, reason)
				}
				revision := lab.Revision
				f.uc.SetLifecycleControls(true)
				require.NoError(t, f.uc.ReconcileStageLabLifecycle(ctx, f.eventID, closes.Add(time.Second)))
				current, err := eventLabRepo.New(f.db.Queries).Get(ctx, lab.ID)
				require.NoError(t, err)
				ttl := int32(60)
				if minutes != nil {
					ttl = *minutes
				}
				require.Equal(t, closes.Add(time.Duration(ttl)*time.Minute), current.RetentionUntil.UTC())
				if reason != "running" {
					require.Equal(t, revision, current.Revision)
					require.Equal(t, reason, current.CloseReason)
				}
				require.Empty(t, f.agent.deleted)
			})
		}
	}
}
func waveMinutes(v int32) *int32 { return &v }

func TestReviewSourceSwitchResetsImmutableAllReadyCohort(t *testing.T) {
	f, set, _, _ := prepareLifecycle(t)
	ctx := context.Background()
	f.uc.SetLifecycleControls(true)
	repo := eventLabRevealRepo.New(f.db.Queries)
	at := time.Now().UTC()
	cohort, err := repo.Freeze(ctx, f.eventID, set.exerciseID, at)
	require.NoError(t, err)
	require.Len(t, cohort, 2)
	next := publishLifecycleSource(t, f, set)
	_, err = f.uc.ReplaceEventExercise(ctx, f.eventID, set.exerciseID, event.ReplaceEventExerciseInput{ExerciseVersionID: next, RecreateStands: true}, f.ownerID)
	require.NoError(t, err)
	_, err = repo.Barrier(ctx, f.eventID, set.exerciseID)
	require.Error(t, err, "source change invalidates preparation without eagerly freezing another cohort")
	// Simulate the replacement assignment preparation boundary, where cohort
	// reservation/creation is actually authorized.
	_, err = repo.Freeze(ctx, f.eventID, set.exerciseID, at)
	require.NoError(t, err)
	barrier, err := repo.Barrier(ctx, f.eventID, set.exerciseID)
	require.NoError(t, err)
	require.Greater(t, barrier.Revision, int64(1))
	require.False(t, barrier.OpenedAt.Valid)
	require.Equal(t, cohort, barrier.EligibleTeamIds)
	// Current replacement bindings define the exact generation for each team.
	// Synthetic receipts below exercise only this SQL consumer boundary.
	for _, team := range []uuid.UUID{f.blueID, f.redID} {
		binding, err := labBindingRepo.New(f.db.Queries).Get(ctx, team, set.challenges[0])
		require.NoError(t, err)
		candidate, err := eventLabModel.New(eventLabModel.NewInput{EventID: f.eventID, TeamID: team, EventExerciseID: set.exerciseID, DefinitionVersionID: next, Ref: eventLabModel.Ref{Group: binding.LabGroupName, Lab: binding.LabName}, VariantIndex: 0, Generation: binding.Generation, ObjectiveIDs: set.challenges, Policy: eventLabModel.DefaultPolicy()}, at)
		require.NoError(t, err)
		candidate.MarkMaterialized(at)
		require.NoError(t, eventLabRepo.New(f.db.Queries).Create(ctx, candidate, set.challenges))
		_, err = eventLabRepo.New(f.db.Queries).AttachBindings(ctx, candidate.ID)
		require.NoError(t, err)
		_, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET agent_uid=$2,agent_generation=1,actual_state='Running',runtime_ready=true,observed_revision=desired_revision WHERE id=$1`, candidate.ID, "synthetic-"+team.String())
		require.NoError(t, err)
		_, err = f.db.Pool.Exec(ctx, `UPDATE lab_bindings SET readiness=1 WHERE lab_id=$1`, candidate.ID)
		require.NoError(t, err)
		require.NoError(t, repo.OpenReady(ctx, f.eventID, at))
		barrier, err = repo.Barrier(ctx, f.eventID, set.exerciseID)
		require.NoError(t, err)
		require.Equal(t, team == f.redID, barrier.OpenedAt.Valid, "partial replacement cohort publication")
	}
}

func TestReviewLostInitialResponseClosureAdoptsOnlyOriginalBirthThenStops(t *testing.T) {
	f, ids := allocationFixture(t, 0)
	ctx := context.Background()
	need := eventLabModel.Compute{CPUMillicores: 750, MemoryBytes: 512 << 20}
	require.NoError(t, f.uc.AdmitEventLabStart(ctx, f.eventID, f.blueID, ids[0], need, 0))
	repo := eventLabRepo.New(f.db.Queries)
	l, err := repo.Get(ctx, ids[0])
	require.NoError(t, err)
	at := time.Now().UTC()
	require.True(t, l.RecordCreateDispatch("original-group", "original-hash", l.OperationID, at))
	original := *l.CreateEvidence
	ok, err := repo.Update(ctx, l, l.Revision)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, l.Close("stage", uuid.Must(uuid.NewV7()), at))
	ok, err = repo.Update(ctx, l, l.Revision-1)
	require.NoError(t, err)
	require.True(t, ok)
	birth := eventLabModel.Observation{Ref: l.Ref, UID: "original-uid", Generation: 1, DesiredState: "Running", ActualState: "Starting", Creation: &eventLabModel.CreationReceipt{GroupUID: original.GroupUID, NamespaceUID: "namespace-uid", DefinitionHash: original.DefinitionHash, OperationID: original.OperationID, Revision: 1, CreationID: "durable-birth-id", LabUID: "original-uid", Committed: true}}
	for name, mutate := range map[string]func(*eventLabModel.Observation){"replacement uid": func(o *eventLabModel.Observation) { o.UID = "replacement" }, "different group": func(o *eventLabModel.Observation) { o.Creation.GroupUID = "replacement-group" }, "different operation": func(o *eventLabModel.Observation) { o.Creation.OperationID = uuid.Must(uuid.NewV7()) }, "uncommitted": func(o *eventLabModel.Observation) { o.Creation.Committed = false }} {
		t.Run(name, func(t *testing.T) {
			copy := birth
			receipt := *birth.Creation
			copy.Creation = &receipt
			mutate(&copy)
			ok, err := repo.RecordBirthIdentity(ctx, l.ID, copy, at)
			require.NoError(t, err)
			require.False(t, ok)
		})
	}
	f.agent.mu.Lock()
	f.agent.observations = map[eventLabModel.Ref]eventLabModel.Observation{l.Ref: birth}
	f.agent.mu.Unlock()
	// Fresh use case proves this original evidence survives process/use-case loss.
	fresh := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, Infra: f.agent, InfrastructureCapability: standCapability{}})
	require.NoError(t, fresh.ReconcilePendingLabLifecycles(ctx))
	current, err := repo.Get(ctx, l.ID)
	require.NoError(t, err)
	require.Equal(t, "original-uid", current.AgentUID)
	require.Equal(t, l.OperationID, current.OperationID)
	require.Equal(t, l.Revision, current.Revision)
	require.NotEqual(t, "Released", current.Allocation.RuntimeState)
	f.agent.mu.Lock()
	require.Len(t, f.agent.stopCalls, 1)
	require.Equal(t, "original-uid", f.agent.stopCalls[0].Target.ExpectedUID)
	observed := at.Add(time.Second)
	f.agent.observations[l.Ref] = eventLabModel.Observation{Ref: l.Ref, UID: current.AgentUID, Generation: 1, ObservedGeneration: 1, OperationID: current.OperationID, Revision: current.Revision, DesiredState: "Stopped", ActualState: "Stopped", AccessFenced: true, AccessFencedAt: &observed, AccessFenceVPNBootID: "current-boot", ObservedAt: &observed, Allocation: eventLabModel.Allocation{RuntimeState: "Released", ReleasedAt: &observed, ObservedAt: &observed}}
	f.agent.mu.Unlock()
	_, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET next_attempt_at=now() WHERE id=$1`, l.ID)
	require.NoError(t, err)
	require.NoError(t, fresh.ReconcilePendingLabLifecycles(ctx))
	current, err = repo.Get(ctx, l.ID)
	require.NoError(t, err)
	require.Equal(t, eventLabModel.Compute{}, current.HeldCompute())
}

func TestReviewSolvedWholeEventLabGetsDeadlineWhenManualFinishBecomesKnown(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	set := f.attachSharedSet(t)
	f.shiftLifecycle(t, -time.Minute, time.Hour)
	_, err := f.db.Pool.Exec(ctx, `UPDATE events SET finish_at=NULL,withdraw_at=NULL WHERE id=$1`, f.eventID)
	require.NoError(t, err)
	f.pass(t)
	f.agent.setAll(f.agent.ready)
	f.pass(t)
	certifyLifecycleFixture(t, f)
	at := time.Now().UTC()
	user := approveBlueCaptain(t, f, at)
	before, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	require.NoError(t, err)
	require.Nil(t, before.RetentionUntil)
	for i, ch := range set.challenges {
		submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Millisecond))
	}
	solved, err := eventLabRepo.New(f.db.Queries).Get(ctx, before.ID)
	require.NoError(t, err)
	require.Equal(t, "solved", solved.CloseReason)
	require.Nil(t, solved.RetentionUntil)
	var score int
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM team_challenge_solves s JOIN team_challenges tc ON tc.id=s.team_challenge_id WHERE tc.event_team_id=$1`, f.blueID).Scan(&score))
	require.Equal(t, 3, score)
	// The existing manual-finish persistence field is supplied after a finish-less
	// generation was solved; no new manager endpoint or power is introduced.
	finished := at.Add(time.Second).Truncate(time.Microsecond)
	_, err = f.db.Pool.Exec(ctx, `UPDATE events SET manual_finished_at=$2 WHERE id=$1`, f.eventID, finished)
	require.NoError(t, err)
	f.uc.SetLifecycleControls(true)
	require.NoError(t, f.uc.ReconcileStageLabLifecycle(ctx, f.eventID, finished.Add(time.Second)))
	after, err := eventLabRepo.New(f.db.Queries).Get(ctx, solved.ID)
	require.NoError(t, err)
	require.Equal(t, solved.Revision, after.Revision)
	require.Equal(t, "solved", after.CloseReason)
	require.Equal(t, finished.Add(time.Hour), after.RetentionUntil.UTC())
	var retained int
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM team_challenge_solves s JOIN team_challenges tc ON tc.id=s.team_challenge_id WHERE tc.event_team_id=$1`, f.blueID).Scan(&retained))
	require.Equal(t, score, retained)
}
func TestReviewUpcomingSetMoveCreatesFourHourPinThroughProductionFlow(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	f.shiftLifecycle(t, 10*time.Minute, 6*time.Hour)
	now := time.Now().UTC().Truncate(time.Microsecond)
	first, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Origin", Returnable: true, LabRetentionMinutes: waveMinutes(30)})
	require.NoError(t, err)
	future, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Future", OpensAt: now.Add(4 * time.Hour), Returnable: false, LabRetentionMinutes: waveMinutes(30)})
	require.NoError(t, err)
	earlyClose := now.Add(time.Hour)
	_, err = f.uc.UpdateEventStage(ctx, f.eventID, first.ID, event.UpdateStageInput{ClosesAt: &earlyClose})
	require.NoError(t, err)
	var setID uuid.UUID
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT event_exercise_id FROM event_challenges WHERE id=$1`, f.infraOne).Scan(&setID))
	_, err = f.uc.SetEventExerciseStage(ctx, f.eventID, setID, &first.ID)
	require.NoError(t, err)
	reservation, err := calModel.NewEventReservation(calModel.EventInput{EventID: f.eventID, Window: calModel.Window{Start: now.Add(-time.Hour), End: now.Add(8 * time.Hour)}, Teams: 20, PerTeam: calModel.Amount{CPUMillicores: 10000, MemoryBytes: 10 << 30}}, f.ownerID, now)
	require.NoError(t, err)
	reservation.SetPlacement([]calModel.Share{{AgentID: uuid.Must(uuid.NewV7()), Units: 20}}, 0, now)
	require.NoError(t, resourceCalendarRepo.New(f.db.Queries).CreateReservation(ctx, reservation))
	f.uc.SetAllocationAccounting(true)
	f.pass(t)
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, f.infraOne)
	require.NoError(t, err)
	require.Equal(t, first.ID, *lab.RuntimeStageID)
	f.uc.SetLifecycleControls(true)
	_, err = f.uc.SetEventExerciseStage(ctx, f.eventID, setID, &future.ID)
	require.NoError(t, err)
	current, err := eventLabRepo.New(f.db.Queries).Get(ctx, lab.ID)
	require.NoError(t, err)
	require.Equal(t, first.ID, *current.RuntimeStageID)
	require.NotNil(t, current.ProtectedUntil)
	require.Equal(t, future.ClosesAt.UTC().Add(30*time.Minute), current.ProtectedUntil.UTC())
	require.NoError(t, f.uc.ReconcileStageLabLifecycle(ctx, f.eventID, earlyClose.Add(time.Second)))
	current, err = eventLabRepo.New(f.db.Queries).Get(ctx, lab.ID)
	require.NoError(t, err)
	require.Equal(t, "Stopped", current.DesiredState)
	require.Equal(t, earlyClose.Add(30*time.Minute), current.RetentionUntil.UTC())
	require.Equal(t, future.ClosesAt.UTC().Add(30*time.Minute), current.EffectiveRetentionUntil().UTC())
}

func TestReviewConsumedStagePreparationNeverUndoesLaterManualStop(t *testing.T) {
	f, ids := allocationFixture(t, 0)
	ctx := context.Background()
	need := eventLabModel.Compute{CPUMillicores: 750, MemoryBytes: 512 << 20}
	require.NoError(t, f.uc.AdmitEventLabStart(ctx, f.eventID, f.blueID, ids[0], need, 0))
	budget, err := resourceCalendarRepo.New(f.db.Queries).GetEventReservation(ctx, f.eventID)
	require.NoError(t, err)
	require.NoError(t, budget.Recalculate(20, calModel.Amount{CPUMillicores: 10000, MemoryBytes: 10 << 30}, calModel.Amount{}, 0, calModel.Amount{}, time.Now()))
	budget.SetPlacement([]calModel.Share{{AgentID: uuid.Must(uuid.NewV7()), Units: 20}}, 0, time.Now())
	changed, err := resourceCalendarRepo.New(f.db.Queries).UpdateReservation(ctx, budget)
	require.NoError(t, err)
	require.True(t, changed)
	f.shiftLifecycle(t, -time.Minute, time.Hour)
	waveProgressive(t, f)
	l := waveStopped(t, f, ids[0], "stage")
	now := time.Now().UTC().Truncate(time.Microsecond)
	stage := uuid.Must(uuid.NewV7())
	_, err = f.db.Pool.Exec(ctx, `INSERT INTO event_stages(id,event_id,name,opens_at,closes_at,returnable,created_at,updated_at) VALUES($1,$2,'Authorized preparation',$3,$4,true,$5,$5)`, stage, f.eventID, now.Add(-time.Minute), now.Add(time.Hour), now)
	require.NoError(t, err)
	f.uc.SetLifecycleControls(true)
	require.NoError(t, f.uc.SelectRetainedLabsForStage(ctx, f.eventID, stage, []uuid.UUID{l.ID}))
	require.NoError(t, f.uc.ReconcileEventStands(ctx))
	current, err := eventLabRepo.New(f.db.Queries).Get(ctx, l.ID)
	require.NoError(t, err)
	require.Equal(t, "Running", current.DesiredState)
	require.Equal(t, l.Revision+1, current.Revision)
	var consumed *time.Time
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT consumed_at FROM event_stage_lab_runtime_memberships WHERE stage_id=$1 AND lab_id=$2`, stage, l.ID).Scan(&consumed))
	require.NotNil(t, consumed)
	stopped := waveStopped(t, f, l.ID, "manual")
	for range 2 {
		require.NoError(t, f.uc.ReconcileEventStands(ctx))
	}
	current, err = eventLabRepo.New(f.db.Queries).Get(ctx, l.ID)
	require.NoError(t, err)
	require.Equal(t, "Stopped", current.DesiredState)
	require.Equal(t, stopped.Revision, current.Revision)
	// A genuinely distinct, explicitly authorized stage intent may later restore
	// the exact retained generation; the consumed earlier intent cannot.
	_, err = f.db.Pool.Exec(ctx, `UPDATE event_stages SET opens_at=$2,closes_at=$3 WHERE id=$1`, stage, now.Add(-10*time.Minute), now.Add(-2*time.Minute))
	require.NoError(t, err)
	later := uuid.Must(uuid.NewV7())
	_, err = f.db.Pool.Exec(ctx, `INSERT INTO event_stages(id,event_id,name,opens_at,closes_at,returnable,created_at,updated_at) VALUES($1,$2,'Later need',$3,$4,true,$5,$5)`, later, f.eventID, now.Add(-time.Minute), now.Add(time.Hour), now)
	require.NoError(t, err)
	require.NoError(t, f.uc.SelectRetainedLabsForStage(ctx, f.eventID, later, []uuid.UUID{l.ID}))
	_, err = f.db.Pool.Exec(ctx, `UPDATE event_lab_retention_pins SET needed_from=$3 WHERE stage_id=$1 AND lab_id=$2`, later, l.ID, now)
	require.NoError(t, err)
	require.NoError(t, f.uc.ReconcileEventStands(ctx))
	current, err = eventLabRepo.New(f.db.Queries).Get(ctx, l.ID)
	require.NoError(t, err)
	require.Equal(t, "Running", current.DesiredState)
	require.Equal(t, stopped.Revision+1, current.Revision)
}
