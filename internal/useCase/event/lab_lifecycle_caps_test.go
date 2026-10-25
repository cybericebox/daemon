package event

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

var allLifecycleCaps = infraModel.LifecycleCapabilities{PerLabStop: true, RequiredSnapshot: true, ConfirmedRuntime: true, RetainedRestart: true, FullGroupStop: true}

// capsInfra is an agent whose lifecycle capabilities are set by the test (the
// production agent runs with all of them off).
type capsInfra struct {
	Infrastructure
	caps        infraModel.LifecycleCapabilities
	unknown     bool // no capability report (or the owner cannot be resolved)
	observeErr  error
	observation eventLabModel.Observation
	destroyed   []string
	labStops    int
	labStarts   int
}

func (i *capsInfra) LabLifecycleCapabilities(context.Context, string) (infraModel.LifecycleCapabilities, bool) {
	return i.caps, !i.unknown
}
func (i *capsInfra) DestroyLabGroup(_ context.Context, group string) error {
	i.destroyed = append(i.destroyed, group)
	return nil
}
func (i *capsInfra) StopLab(context.Context, eventLabModel.StopRequest) error {
	i.labStops++
	if !i.caps.PerLabStop {
		return fmt.Errorf("stop lab: lifecycle capability unavailable")
	}
	return nil
}
func (i *capsInfra) StartLab(context.Context, eventLabModel.Target) error {
	i.labStarts++
	return nil
}
func (i *capsInfra) ObserveLab(_ context.Context, ref eventLabModel.Ref) (eventLabModel.Observation, error) {
	o := i.observation
	o.Ref = ref
	return o, i.observeErr
}

func capsGroupRow(t *testing.T) (postgres.EventTeamGroupAllocation, uuid.UUID, uuid.UUID) {
	eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	name, err := labBindingModel.GroupName(eventID, teamID)
	require.NoError(t, err)
	return postgres.EventTeamGroupAllocation{EventID: eventID, EventTeamID: teamID, LabGroupName: name, AgentUid: "group-uid", AgentGeneration: 1, DesiredRevision: 1, DesiredState: "Running", ActualState: "Running", Ready: true, Allocation: []byte(`{}`)}, eventID, teamID
}

func capsLabRow(eventID, teamID uuid.UUID, group string) postgres.EventTeamLab {
	return postgres.EventTeamLab{ID: uuid.Must(uuid.NewV7()), EventID: eventID, EventTeamID: teamID, EventExerciseID: uuid.Must(uuid.NewV7()), LabGroupName: group, LabName: "lab-a", AgentUid: "lab-uid", AgentGeneration: 1, DesiredRevision: 2, ObservedRevision: 1, OperationID: uuid.Must(uuid.NewV7()), DesiredState: "Stopped", ActualState: "Running", CloseReason: pgtype.Text{String: "event", Valid: true}, SnapshotMode: "skip", SnapshotState: "Unknown", RetirementState: "Unknown", ObjectiveCount: 1, Allocation: []byte(`{"RuntimeState":"Allocated","StorageState":"None"}`)}
}

// expectSettle expects the group's labs and group to be settled as gone.
func expectSettle(q *postgresMocks.MockQuerier, teamID uuid.UUID, group postgres.EventTeamGroupAllocation, labs []postgres.EventTeamLab, settledLabs *[]postgres.UpdateEventTeamLabParams, settledGroup *postgres.UpdateEventTeamLabGroupParams) {
	q.EXPECT().LockEventTeamForLabAdmission(gomock.Any(), teamID).Return(teamID, nil)
	q.EXPECT().LockEventTeamLifecycleLabs(gomock.Any(), teamID).Return(labs, nil)
	q.EXPECT().UpdateEventTeamLab(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpdateEventTeamLabParams) (int64, error) {
		*settledLabs = append(*settledLabs, p)
		return 1, nil
	}).Times(len(labs))
	q.EXPECT().LockEventTeamLabGroup(gomock.Any(), teamID).Return(group, nil)
	q.EXPECT().UpdateEventTeamLabGroup(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpdateEventTeamLabGroupParams) (int64, error) {
		*settledGroup = p
		return 1, nil
	})
}

// The moderators group (VPN only) is unauthorized outside its reservation.
// Without an advertised full group stop it is never stopped: no StopLabGroup,
// no error, the group stays Running until it is destroyed.
func TestReconcileGroupsWithoutFullGroupStopNeverStops(t *testing.T) {
	for _, capsOff := range []bool{true, false} {
		t.Run(fmt.Sprintf("caps off %v", capsOff), func(t *testing.T) {
			now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			g, eventID, teamID := capsGroupRow(t)
			g.ObservedAt = pgtype.Timestamptz{Time: now.Add(-time.Second), Valid: true}
			// The event has finished: the group is not authorized to run.
			e := postgres.Event{ID: eventID, InfrastructureAllowed: true, LifecycleConfigured: true, PublishAt: now.Add(-3 * time.Hour), StartAt: now.Add(-2 * time.Hour), FinishAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true}, WithdrawAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}}
			infra := &vpnLifecycleInfra{capsOff: capsOff, observation: eventLabModel.GroupObservation{Name: g.LabGroupName, UID: g.AgentUid, Generation: 1, ObservedAt: &now, DesiredState: "Running", InitialReady: true, Ready: true}}
			u := NewEventUseCase(Dependencies{Repo: q, UoW: vpnLifecycleWorker{q}, Infra: infra})
			q.EXPECT().ListEventLabGroupLifecycleWork(gomock.Any(), gomock.Any()).Return([]postgres.EventTeamGroupAllocation{g}, nil)
			q.EXPECT().LockEventTeamForLabAdmission(gomock.Any(), teamID).Return(teamID, nil)
			q.EXPECT().LockEventTeamLabGroup(gomock.Any(), teamID).Return(g, nil)
			q.EXPECT().LockEventTeamLifecycleLabs(gomock.Any(), teamID).Return(nil, nil)
			q.EXPECT().GetEventLabPendingStarts(gomock.Any(), teamID).Return(int32(0), nil)
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(e, nil)
			var written postgres.UpdateEventTeamLabGroupParams
			q.EXPECT().UpdateEventTeamLabGroup(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpdateEventTeamLabGroupParams) (int64, error) {
				written = p
				return 1, nil
			})
			q.EXPECT().ScheduleEventLabGroupRetry(gomock.Any(), gomock.Any()).Return(nil)
			require.NoError(t, u.reconcileGroups(context.Background(), now))
			if capsOff {
				require.Equal(t, "Running", written.DesiredState)
				require.EqualValues(t, 1, written.DesiredRevision)
				require.Zero(t, infra.stops)
			} else {
				require.Equal(t, "Stopped", written.DesiredState)
				require.Equal(t, 1, infra.stops)
			}
		})
	}
}

// A group already asked to stop (or to restart after it) on an agent that could
// never receive the command goes back to its initial running identity, so its
// labs can deploy; no stop or restart is dispatched.
func TestReconcileGroupsWithoutFullGroupStopForgetsUndeliveredStop(t *testing.T) {
	for _, desired := range []string{"Stopped", "Running"} {
		t.Run(desired, func(t *testing.T) {
			now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			g, eventID, teamID := capsGroupRow(t)
			g.DesiredState, g.DesiredRevision, g.ObservedRevision, g.Ready = desired, 3, 0, false
			e := postgres.Event{ID: eventID, InfrastructureAllowed: true, LifecycleConfigured: true, PublishAt: now.Add(-3 * time.Hour), StartAt: now.Add(-2 * time.Hour), FinishAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true}}
			infra := &vpnLifecycleInfra{capsOff: true, observation: eventLabModel.GroupObservation{Name: g.LabGroupName, UID: g.AgentUid, Generation: 1, ObservedAt: &now, DesiredState: "Running", InitialReady: true, Ready: true}}
			u := NewEventUseCase(Dependencies{Repo: q, UoW: vpnLifecycleWorker{q}, Infra: infra})
			q.EXPECT().ListEventLabGroupLifecycleWork(gomock.Any(), gomock.Any()).Return([]postgres.EventTeamGroupAllocation{g}, nil)
			q.EXPECT().LockEventTeamForLabAdmission(gomock.Any(), teamID).Return(teamID, nil)
			q.EXPECT().LockEventTeamLabGroup(gomock.Any(), teamID).Return(g, nil)
			q.EXPECT().LockEventTeamLifecycleLabs(gomock.Any(), teamID).Return(nil, nil)
			q.EXPECT().GetEventLabPendingStarts(gomock.Any(), teamID).Return(int32(0), nil)
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(e, nil)
			var written postgres.UpdateEventTeamLabGroupParams
			q.EXPECT().UpdateEventTeamLabGroup(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpdateEventTeamLabGroupParams) (int64, error) {
				require.EqualValues(t, 3, p.ExpectedRevision)
				written = p
				return 1, nil
			})
			q.EXPECT().ScheduleEventLabGroupRetry(gomock.Any(), gomock.Any()).Return(nil)
			require.NoError(t, u.reconcileGroups(context.Background(), now))
			require.Equal(t, "Running", written.DesiredState)
			require.EqualValues(t, 1, written.DesiredRevision)
			require.False(t, written.RetentionUntil.Valid)
			require.Zero(t, infra.stops)
			require.Zero(t, infra.starts)
		})
	}
}

type notFoundGroupInfra struct {
	vpnLifecycleInfra
}

func (*notFoundGroupInfra) ObserveLabGroup(_ context.Context, group string) (eventLabModel.GroupObservation, error) {
	return eventLabModel.GroupObservation{Name: group, ActualState: "Unknown"}, fmt.Errorf("get lab group %q: %w", group, infraModel.ErrAgentObjectNotFound)
}

// The rows of a deleted event survive it. Once its group is gone from every
// agent, an agent without lifecycle settles them: no error, no stop, never
// picked again.
func TestReconcileGroupsSettlesGoneGroupWithoutCaps(t *testing.T) {
	now := time.Now().UTC()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	g, eventID, teamID := capsGroupRow(t)
	lab := capsLabRow(eventID, teamID, g.LabGroupName)
	infra := &notFoundGroupInfra{vpnLifecycleInfra{capsOff: true}}
	u := NewEventUseCase(Dependencies{Repo: q, UoW: vpnLifecycleWorker{q}, Infra: infra})
	u.SetLifecycleControls(true)
	q.EXPECT().ListEventLabGroupLifecycleWork(gomock.Any(), gomock.Any()).Return([]postgres.EventTeamGroupAllocation{g}, nil)
	var labs []postgres.UpdateEventTeamLabParams
	var group postgres.UpdateEventTeamLabGroupParams
	expectSettle(q, teamID, g, []postgres.EventTeamLab{lab}, &labs, &group)
	require.NoError(t, u.reconcileGroups(context.Background(), now))
	require.Len(t, labs, 1)
	require.Equal(t, "Deleted", labs[0].DesiredState)
	require.Equal(t, "Deleted", labs[0].ActualState)
	require.Equal(t, "Deleted", group.DesiredState)
	require.Equal(t, "Deleted", group.RetirementState)
	require.Zero(t, infra.stops)
}

// With all capabilities a gone group stays an error (unchanged behaviour).
func TestReconcileGroupsKeepsGoneGroupErrorWithCaps(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	g, _, _ := capsGroupRow(t)
	infra := &notFoundGroupInfra{}
	u := NewEventUseCase(Dependencies{Repo: q, UoW: vpnLifecycleWorker{q}, Infra: infra})
	u.SetLifecycleControls(true)
	q.EXPECT().ListEventLabGroupLifecycleWork(gomock.Any(), gomock.Any()).Return([]postgres.EventTeamGroupAllocation{g}, nil)
	q.EXPECT().ScheduleEventLabGroupRetry(gomock.Any(), gomock.Any()).Return(nil)
	require.Error(t, u.reconcileGroups(context.Background(), time.Now().UTC()))
}

// A stopped (closed) lab on an agent without per-lab stop is never sent a stop:
// it runs until it is destroyed, and that is no failure. With the capability it
// is stopped as before.
func TestReconcileStoppedLabWithoutPerLabStop(t *testing.T) {
	for _, capsOff := range []bool{true, false} {
		t.Run(fmt.Sprintf("caps off %v", capsOff), func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			g, eventID, teamID := capsGroupRow(t)
			row := capsLabRow(eventID, teamID, g.LabGroupName)
			infra := &capsInfra{caps: allLifecycleCaps}
			if capsOff {
				infra.caps = infraModel.LifecycleCapabilities{}
			}
			u := NewEventUseCase(Dependencies{Repo: q, UoW: vpnLifecycleWorker{q}, Infra: infra})
			u.SetLifecycleControls(true)
			// An observation without a timestamp records nothing; the lab is re-read.
			q.EXPECT().GetEventTeamLab(gomock.Any(), row.ID).Return(row, nil)
			lab, err := u.labs.Get(context.Background(), row.ID)
			require.NoError(t, err)
			q.EXPECT().GetEventTeamLab(gomock.Any(), row.ID).Return(row, nil)
			err = u.reconcileStoppedLab(context.Background(), lab)
			if capsOff {
				require.ErrorIs(t, err, errLifecycleUnsupported)
				require.Zero(t, infra.labStops)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, infra.labStops)
			}
		})
	}
}

// The periodic pass treats an unsupported stop as no failure and looks at the
// lab again only at the longest interval.
func TestReconcilePendingLabLifecyclesDefersUnsupportedStop(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	g, eventID, teamID := capsGroupRow(t)
	row := capsLabRow(eventID, teamID, g.LabGroupName)
	infra := &capsInfra{}
	u := NewEventUseCase(Dependencies{Repo: q, Infra: infra})
	q.EXPECT().ListPendingStoppedEventTeamLabs(gomock.Any(), gomock.Any()).Return([]postgres.EventTeamLab{row}, nil)
	q.EXPECT().GetEventTeamLab(gomock.Any(), row.ID).Return(row, nil)
	var scheduled postgres.ScheduleEventTeamLabLifecycleRetryParams
	q.EXPECT().ScheduleEventTeamLabLifecycleRetry(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.ScheduleEventTeamLabLifecycleRetryParams) (int64, error) {
		scheduled = p
		return 1, nil
	})
	require.NoError(t, u.ReconcilePendingLabLifecycles(context.Background()))
	require.Zero(t, infra.labStops)
	require.Equal(t, u.labLifecycleRetryMax, scheduled.NextAttemptAt.Sub(scheduled.Now))
}

// A lab of a deleted event whose group no agent knows any more is settled
// instead of being chased with "not found" errors.
func TestReconcileStoppedLabSettlesGoneGroupWithoutCaps(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	g, eventID, teamID := capsGroupRow(t)
	row := capsLabRow(eventID, teamID, g.LabGroupName)
	infra := &capsInfra{observeErr: fmt.Errorf("get lab group %q: %w", g.LabGroupName, infraModel.ErrAgentObjectNotFound)}
	u := NewEventUseCase(Dependencies{Repo: q, UoW: vpnLifecycleWorker{q}, Infra: infra})
	u.SetLifecycleControls(true)
	q.EXPECT().GetEventTeamLab(gomock.Any(), row.ID).Return(row, nil)
	lab, err := u.labs.Get(context.Background(), row.ID)
	require.NoError(t, err)
	var labs []postgres.UpdateEventTeamLabParams
	var group postgres.UpdateEventTeamLabGroupParams
	expectSettle(q, teamID, g, []postgres.EventTeamLab{row}, &labs, &group)
	require.NoError(t, u.reconcileStoppedLab(context.Background(), lab))
	require.Zero(t, infra.labStops)
	require.Len(t, labs, 1)
	require.Equal(t, "Deleted", labs[0].DesiredState)
	require.Equal(t, "Deleted", group.DesiredState)
}

// Same for a running lab whose group is gone.
func TestReconcileRunningLabSettlesGoneGroupWithoutCaps(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	g, eventID, teamID := capsGroupRow(t)
	row := capsLabRow(eventID, teamID, g.LabGroupName)
	row.DesiredState, row.DesiredRevision = "Running", 1
	infra := &capsInfra{observeErr: fmt.Errorf("get lab group %q: %w", g.LabGroupName, infraModel.ErrAgentObjectNotFound)}
	u := NewEventUseCase(Dependencies{Repo: q, UoW: vpnLifecycleWorker{q}, Infra: infra})
	u.SetLifecycleControls(true)
	q.EXPECT().GetEventTeamLab(gomock.Any(), row.ID).Return(row, nil)
	lab, err := u.labs.Get(context.Background(), row.ID)
	require.NoError(t, err)
	var labs []postgres.UpdateEventTeamLabParams
	var group postgres.UpdateEventTeamLabGroupParams
	expectSettle(q, teamID, g, []postgres.EventTeamLab{row}, &labs, &group)
	require.NoError(t, u.reconcileRunningLab(context.Background(), lab, time.Now()))
	require.Len(t, labs, 1)
}

// An agent outage is not a gone group: the error stays.
func TestReconcileStoppedLabKeepsOutageError(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	g, eventID, teamID := capsGroupRow(t)
	row := capsLabRow(eventID, teamID, g.LabGroupName)
	infra := &capsInfra{observeErr: errors.New("agent unavailable")}
	u := NewEventUseCase(Dependencies{Repo: q, UoW: vpnLifecycleWorker{q}, Infra: infra})
	u.SetLifecycleControls(true)
	q.EXPECT().GetEventTeamLab(gomock.Any(), row.ID).Return(row, nil)
	lab, err := u.labs.Get(context.Background(), row.ID)
	require.NoError(t, err)
	require.Error(t, u.reconcileStoppedLab(context.Background(), lab))
}

// Queued cleanups (event delete, team delete) and withdraw cleanup destroy a
// group the retention ledger owns when the agent cannot stop or retain
// anything, and settle its rows. With the capabilities the owned group is kept
// for its retained lifecycle, as before.
func TestCleanupDestroysOwnedGroupWithoutCaps(t *testing.T) {
	for _, path := range []string{"queued", "withdrawn"} {
		for _, capsOff := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s caps off %v", path, capsOff), func(t *testing.T) {
				q := postgresMocks.NewMockQuerier(gomock.NewController(t))
				g, eventID, teamID := capsGroupRow(t)
				row := capsLabRow(eventID, teamID, g.LabGroupName)
				infra := &capsInfra{caps: allLifecycleCaps}
				if capsOff {
					infra.caps = infraModel.LifecycleCapabilities{}
				}
				u := NewEventUseCase(Dependencies{Repo: q, UoW: vpnLifecycleWorker{q}, Infra: infra})
				u.SetLifecycleControls(true)
				bindingID := uuid.Must(uuid.NewV7())
				if path == "queued" {
					q.EXPECT().ListPendingLabGroupCleanupRequests(gomock.Any()).Return([]string{g.LabGroupName}, nil)
				} else {
					q.EXPECT().QueueWithdrawnEmptyLabGroups(gomock.Any(), gomock.Any()).Return(nil)
					q.EXPECT().ListWithdrawnLabBindings(gomock.Any(), gomock.Any()).Return([]postgres.LabBinding{{ID: bindingID, LabGroupName: g.LabGroupName}}, nil)
				}
				if capsOff {
					var labs []postgres.UpdateEventTeamLabParams
					var group postgres.UpdateEventTeamLabGroupParams
					expectSettle(q, teamID, g, []postgres.EventTeamLab{row}, &labs, &group)
					if path == "queued" {
						q.EXPECT().MarkLabGroupCleanupRequestDestroyed(gomock.Any(), gomock.Any()).Return(int64(1), nil)
						require.NoError(t, u.CleanupQueuedLabGroups(context.Background()))
					} else {
						q.EXPECT().MarkLabBindingDestroyed(gomock.Any(), bindingID).Return(int64(1), nil)
						require.NoError(t, u.CleanupWithdrawnLaboratories(context.Background()))
					}
					require.Equal(t, []string{g.LabGroupName}, infra.destroyed)
					require.Len(t, labs, 1)
					require.Equal(t, "Deleted", labs[0].DesiredState)
					require.Equal(t, "Deleted", group.DesiredState)
					return
				}
				q.EXPECT().IsOwnedRetainedLabGroup(gomock.Any(), g.LabGroupName).Return(true, nil)
				if path == "queued" {
					require.NoError(t, u.CleanupQueuedLabGroups(context.Background()))
				} else {
					require.NoError(t, u.CleanupWithdrawnLaboratories(context.Background()))
				}
				require.Empty(t, infra.destroyed)
			})
		}
	}
}

// Unknown capabilities (no report, owner not resolvable, lookup failed) keep
// the behaviour with the capabilities: the stop is dispatched (the agent client
// re-checks them live), a gone group is not settled, retention still holds.
func TestUnknownLifecycleCapabilitiesKeepCapableBehaviour(t *testing.T) {
	t.Run("stop dispatched", func(t *testing.T) {
		q := postgresMocks.NewMockQuerier(gomock.NewController(t))
		g, eventID, teamID := capsGroupRow(t)
		row := capsLabRow(eventID, teamID, g.LabGroupName)
		infra := &capsInfra{caps: allLifecycleCaps, unknown: true}
		u := NewEventUseCase(Dependencies{Repo: q, UoW: vpnLifecycleWorker{q}, Infra: infra})
		u.SetLifecycleControls(true)
		q.EXPECT().GetEventTeamLab(gomock.Any(), row.ID).Return(row, nil).Times(2)
		lab, err := u.labs.Get(context.Background(), row.ID)
		require.NoError(t, err)
		require.NoError(t, u.reconcileStoppedLab(context.Background(), lab))
		require.Equal(t, 1, infra.labStops)
	})
	t.Run("gone group not settled", func(t *testing.T) {
		q := postgresMocks.NewMockQuerier(gomock.NewController(t))
		g, eventID, teamID := capsGroupRow(t)
		row := capsLabRow(eventID, teamID, g.LabGroupName)
		infra := &capsInfra{unknown: true, observeErr: fmt.Errorf("get lab group %q: %w", g.LabGroupName, infraModel.ErrAgentObjectNotFound)}
		u := NewEventUseCase(Dependencies{Repo: q, UoW: vpnLifecycleWorker{q}, Infra: infra})
		u.SetLifecycleControls(true)
		q.EXPECT().GetEventTeamLab(gomock.Any(), row.ID).Return(row, nil)
		lab, err := u.labs.Get(context.Background(), row.ID)
		require.NoError(t, err)
		// No settle query is expected: the mock fails on any.
		require.ErrorIs(t, u.reconcileStoppedLab(context.Background(), lab), infraModel.ErrAgentObjectNotFound)
	})
	t.Run("retention holds", func(t *testing.T) {
		q := postgresMocks.NewMockQuerier(gomock.NewController(t))
		g, _, _ := capsGroupRow(t)
		infra := &capsInfra{unknown: true}
		u := NewEventUseCase(Dependencies{Repo: q, UoW: vpnLifecycleWorker{q}, Infra: infra})
		u.SetLifecycleControls(true)
		q.EXPECT().ListPendingLabGroupCleanupRequests(gomock.Any()).Return([]string{g.LabGroupName}, nil)
		q.EXPECT().IsOwnedRetainedLabGroup(gomock.Any(), g.LabGroupName).Return(true, nil)
		require.NoError(t, u.CleanupQueuedLabGroups(context.Background()))
		require.Empty(t, infra.destroyed)
	})
	t.Run("group stop dispatched", func(t *testing.T) {
		now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
		q := postgresMocks.NewMockQuerier(gomock.NewController(t))
		g, eventID, teamID := capsGroupRow(t)
		g.ObservedAt = pgtype.Timestamptz{Time: now.Add(-time.Second), Valid: true}
		e := postgres.Event{ID: eventID, InfrastructureAllowed: true, LifecycleConfigured: true, PublishAt: now.Add(-3 * time.Hour), StartAt: now.Add(-2 * time.Hour), FinishAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true}, WithdrawAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}}
		infra := &unknownCapsGroupInfra{vpnLifecycleInfra{observation: eventLabModel.GroupObservation{Name: g.LabGroupName, UID: g.AgentUid, Generation: 1, ObservedAt: &now, DesiredState: "Running", InitialReady: true, Ready: true}}}
		u := NewEventUseCase(Dependencies{Repo: q, UoW: vpnLifecycleWorker{q}, Infra: infra})
		q.EXPECT().ListEventLabGroupLifecycleWork(gomock.Any(), gomock.Any()).Return([]postgres.EventTeamGroupAllocation{g}, nil)
		q.EXPECT().LockEventTeamForLabAdmission(gomock.Any(), teamID).Return(teamID, nil)
		q.EXPECT().LockEventTeamLabGroup(gomock.Any(), teamID).Return(g, nil)
		q.EXPECT().LockEventTeamLifecycleLabs(gomock.Any(), teamID).Return(nil, nil)
		q.EXPECT().GetEventLabPendingStarts(gomock.Any(), teamID).Return(int32(0), nil)
		q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(e, nil)
		q.EXPECT().UpdateEventTeamLabGroup(gomock.Any(), gomock.Any()).Return(int64(1), nil)
		q.EXPECT().ScheduleEventLabGroupRetry(gomock.Any(), gomock.Any()).Return(nil)
		require.NoError(t, u.reconcileGroups(context.Background(), now))
		require.Equal(t, 1, infra.stops)
	})
}

type unknownCapsGroupInfra struct{ vpnLifecycleInfra }

func (*unknownCapsGroupInfra) LabLifecycleCapabilities(context.Context, string) (infraModel.LifecycleCapabilities, bool) {
	return infraModel.LifecycleCapabilities{}, false
}
