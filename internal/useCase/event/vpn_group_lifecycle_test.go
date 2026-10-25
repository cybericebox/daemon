package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labAccessModel "github.com/cybericebox/daemon/internal/model/labAccess"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"testing"
	"time"
)

type vpnLifecycleUnit struct{}

func (vpnLifecycleUnit) Save() error    { return nil }
func (vpnLifecycleUnit) Restore() error { return nil }

type vpnLifecycleWorker struct{ q IRepository }

func (w vpnLifecycleWorker) UnitOfWork(ctx context.Context) (context.Context, IRepository, postgres.UoW, error) {
	return ctx, w.q, vpnLifecycleUnit{}, nil
}

type vpnLifecycleInfra struct {
	Infrastructure
	observation eventLabModel.GroupObservation
	stops       int
	starts      int
	capsOff     bool
}

func (i *vpnLifecycleInfra) LabLifecycleCapabilities(context.Context, string) (infraModel.LifecycleCapabilities, bool) {
	if i.capsOff {
		return infraModel.LifecycleCapabilities{}, true
	}
	return infraModel.LifecycleCapabilities{PerLabStop: true, RequiredSnapshot: true, ConfirmedRuntime: true, RetainedRestart: true, FullGroupStop: true}, true
}

func (i *vpnLifecycleInfra) ObserveLabGroup(context.Context, string) (eventLabModel.GroupObservation, error) {
	return i.observation, nil
}
func (i *vpnLifecycleInfra) StopLabGroup(context.Context, eventLabModel.GroupTarget) error {
	i.stops++
	return nil
}
func (i *vpnLifecycleInfra) StartLabGroup(context.Context, eventLabModel.GroupTarget) error {
	i.starts++
	return nil
}

// A reservation admits VPN services before any challenge exists. Its exact
// end, cancellation and team identity bound that exception to child runtime.
func TestVPNOnlyGroupLifecycleUsesFiniteReservation(t *testing.T) {
	for _, kind := range []string{"active before event", "before reservation", "reservation ended", "missing reservation", "canceled reservation", "unbounded reservation", "wrong group", "wrong team scope", "infrastructure disabled", "event finished", "unadmitted team", "unformed team"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
			eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			name, err := labBindingModel.GroupName(eventID, teamID)
			require.NoError(t, err)
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			g := postgres.EventTeamGroupAllocation{EventID: eventID, EventTeamID: teamID, LabGroupName: name, AgentUid: "vpn-group", AgentGeneration: 1, DesiredRevision: 1, DesiredState: "Running", ActualState: "Running", Ready: true, ObservedAt: pgtype.Timestamptz{Time: now.Add(-time.Second), Valid: true}, Allocation: []byte(`{}`)}
			e := postgres.Event{ID: eventID, InfrastructureAllowed: true, LifecycleConfigured: true, PublishAt: now.Add(-time.Hour), StartAt: now.Add(time.Hour), FinishAt: pgtype.Timestamptz{Time: now.Add(2 * time.Hour), Valid: true}, WithdrawAt: pgtype.Timestamptz{Time: now.Add(3 * time.Hour), Valid: true}}
			reservation := postgres.ResourceReservation{Kind: "event", EventID: uuid.NullUUID{UUID: eventID, Valid: true}, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)}
			var reservationErr error
			team := postgres.EventTeam{ID: teamID, EventID: eventID, Moderators: true}
			admitted, formed := true, true
			switch kind {
			case "before reservation":
				reservation.StartsAt = now.Add(time.Second)
			case "reservation ended":
				reservation.EndsAt = now
			case "missing reservation":
				reservationErr = pgx.ErrNoRows
			case "canceled reservation":
				reservation.CanceledAt = pgtype.Timestamptz{Time: now.Add(-time.Second), Valid: true}
			case "unbounded reservation":
				reservation.EndsAt = time.Time{}
			case "wrong group":
				g.LabGroupName = "foreign"
			case "wrong team scope":
				team.EventID = uuid.Must(uuid.NewV7())
			case "infrastructure disabled":
				e.InfrastructureAllowed = false
			case "event finished":
				e.PublishAt = now.Add(-3 * time.Hour)
				e.StartAt = now.Add(-2 * time.Hour)
				e.FinishAt = pgtype.Timestamptz{Time: now, Valid: true}
			case "unadmitted team":
				team.Moderators = false
				admitted = false
			case "unformed team":
				team.Moderators = false
				formed = false
			}
			infra := &vpnLifecycleInfra{observation: eventLabModel.GroupObservation{Name: g.LabGroupName, UID: g.AgentUid, Generation: 1, ObservedAt: &now, DesiredState: "Running", InitialReady: true, Ready: true}}
			u := NewEventUseCase(Dependencies{Repo: q, UoW: vpnLifecycleWorker{q}, Infra: infra})
			q.EXPECT().ListEventLabGroupLifecycleWork(gomock.Any(), gomock.Any()).Return([]postgres.EventTeamGroupAllocation{g}, nil)
			q.EXPECT().LockEventTeamForLabAdmission(gomock.Any(), teamID).Return(teamID, nil)
			q.EXPECT().LockEventTeamLabGroup(gomock.Any(), teamID).Return(g, nil)
			q.EXPECT().LockEventTeamLifecycleLabs(gomock.Any(), teamID).Return(nil, nil)
			q.EXPECT().GetEventLabPendingStarts(gomock.Any(), teamID).Return(int32(0), nil)
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(e, nil)
			q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(team, nil).AnyTimes()
			q.EXPECT().GetEventTeamAdmitted(gomock.Any(), gomock.Any()).Return(admitted, nil).AnyTimes()
			q.EXPECT().GetEventTeamFormed(gomock.Any(), gomock.Any()).Return(formed, nil).AnyTimes()
			q.EXPECT().GetEventResourceReservation(gomock.Any(), gomock.Any()).Return(reservation, reservationErr).AnyTimes()
			var written postgres.UpdateEventTeamLabGroupParams
			q.EXPECT().UpdateEventTeamLabGroup(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpdateEventTeamLabGroupParams) (int64, error) {
				written = p
				return 1, nil
			})
			q.EXPECT().ScheduleEventLabGroupRetry(gomock.Any(), gomock.Any()).Return(nil)
			require.NoError(t, u.reconcileGroups(context.Background(), now))
			if kind == "active before event" {
				require.Equal(t, "Running", written.DesiredState)
				require.True(t, written.Ready)
				require.Zero(t, infra.stops)
			} else {
				require.Equal(t, "Stopped", written.DesiredState)
				require.False(t, written.Ready)
				require.Equal(t, 1, infra.stops)
				require.True(t, written.RetentionUntil.Valid)
				require.Equal(t, now, written.RetentionUntil.Time)
			}
		})
	}
}

type moderatorsInitInfra struct{ Infrastructure }

func (moderatorsInitInfra) SetLabGroupVPNDisabled(context.Context, string, bool) error {
	panic("initialization must not deploy infrastructure")
}
func (moderatorsInitInfra) SetLabGroupSuspended(context.Context, string, bool) error {
	panic("initialization must not deploy infrastructure")
}
func (moderatorsInitInfra) ReconcileLabGroupAccess(context.Context, string, []labAccessModel.ClientPolicy) error {
	panic("initialization must not deploy infrastructure")
}

func TestStandEngineInitializesModeratorsBeforeChallengeDeployWindow(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	u := NewEventUseCase(Dependencies{Repo: q, UoW: vpnLifecycleWorker{q}, Infra: moderatorsInitInfra{}})
	u.placement.plans[eventID] = cachedLeadPlan{at: now}
	e := postgres.Event{ID: eventID, InfrastructureAllowed: true, LifecycleConfigured: true, PublishAt: now.Add(-time.Hour), StartAt: now.Add(2 * time.Hour), FinishAt: pgtype.Timestamptz{Time: now.Add(3 * time.Hour), Valid: true}, WithdrawAt: pgtype.Timestamptz{Time: now.Add(4 * time.Hour), Valid: true}}
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(e, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID}, nil)
	q.EXPECT().GetEventStandRollout(gomock.Any(), eventID).Return(postgres.EventStandRollout{}, pgx.ErrNoRows)
	q.EXPECT().ListEventStages(gomock.Any(), eventID).Return(nil, nil)
	gomock.InOrder(
		q.EXPECT().GetModeratorsTeam(gomock.Any(), eventID).Return(postgres.EventTeam{}, pgx.ErrNoRows),
		q.EXPECT().GetModeratorsTeam(gomock.Any(), eventID).Return(postgres.EventTeam{}, pgx.ErrNoRows),
		q.EXPECT().LockEventForTeamChange(gomock.Any(), eventID).Return(eventID, nil),
		q.EXPECT().GetModeratorsTeam(gomock.Any(), eventID).Return(postgres.EventTeam{}, pgx.ErrNoRows),
		q.EXPECT().CreateModeratorsTeam(gomock.Any(), gomock.Any()).Return(nil),
		q.EXPECT().GetModeratorsTeam(gomock.Any(), eventID).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Moderators: true}, nil),
	)
	var queued postgres.RequestEventLabAccessSyncParams
	q.EXPECT().RequestEventLabAccessSync(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.RequestEventLabAccessSyncParams) (postgres.EventLabAccessSync, error) {
		queued = p
		return postgres.EventLabAccessSync{}, nil
	})
	require.NoError(t, u.reconcileEventStands(context.Background(), eventID, now))
	require.Equal(t, teamID, queued.EventTeamID)
	require.Equal(t, now, queued.UpdatedAt)
}
