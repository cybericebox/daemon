package event_test

import (
	"context"
	"errors"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

type recordingCleanupInfrastructure struct{ groups []string }

func (*recordingCleanupInfrastructure) DeployLab(context.Context, string, string, infraModel.LabMeta, exerciseModel.Topology) error {
	return nil
}
func (*recordingCleanupInfrastructure) LabStatus(context.Context, string, string) (exerciseModel.LabDeployStatus, error) {
	return exerciseModel.LabDeployStatus{}, nil
}
func (*recordingCleanupInfrastructure) EnsureLabClient(context.Context, string, string) (string, error) {
	return "", nil
}
func (*recordingCleanupInfrastructure) EnsureVPNGroup(context.Context, string) error { return nil }

func (f *recordingCleanupInfrastructure) DestroyLabGroup(_ context.Context, group string) error {
	f.groups = append(f.groups, group)
	return nil
}

func TestCleanupWithdrawnLaboratories_DeletesEachGroupOnceBeforeMarkingBindingsCleaned(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &recordingCleanupInfrastructure{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})

	firstID := uuid.Must(uuid.NewV7())
	secondID := uuid.Must(uuid.NewV7())
	q.EXPECT().QueueWithdrawnEmptyLabGroups(gomock.Any(), gomock.Any()).Return(nil)
	q.EXPECT().ListWithdrawnLabBindings(gomock.Any(), gomock.Any()).Return([]postgres.LabBinding{
		{ID: firstID, LabGroupName: "e-team-a"},
		{ID: secondID, LabGroupName: "e-team-a"},
	}, nil)
	q.EXPECT().MarkLabBindingDestroyed(gomock.Any(), firstID).Return(int64(1), nil)
	q.EXPECT().MarkLabBindingDestroyed(gomock.Any(), secondID).Return(int64(1), nil)

	if err := uc.CleanupWithdrawnLaboratories(context.Background()); err != nil {
		t.Fatalf("CleanupWithdrawnLaboratories: %v", err)
	}
	if len(infra.groups) != 1 || infra.groups[0] != "e-team-a" {
		t.Fatalf("destroy calls = %#v, want exactly e-team-a once", infra.groups)
	}
}

func TestCleanupQueuedLabGroups_RetriesDurableTeamDeletionRequests(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &recordingCleanupInfrastructure{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})

	q.EXPECT().ListPendingLabGroupCleanupRequests(gomock.Any()).Return([]string{"e-team-a"}, nil)
	q.EXPECT().MarkLabGroupCleanupRequestDestroyed(gomock.Any(), gomock.Any()).Return(int64(1), nil)

	if err := uc.CleanupQueuedLabGroups(context.Background()); err != nil {
		t.Fatalf("CleanupQueuedLabGroups: %v", err)
	}
	if len(infra.groups) != 1 || infra.groups[0] != "e-team-a" {
		t.Fatalf("destroy calls = %#v, want exactly e-team-a once", infra.groups)
	}
}

type flakyCleanupInfrastructure struct {
	recordingCleanupInfrastructure
	failures int
}

func (f *flakyCleanupInfrastructure) DestroyLabGroup(ctx context.Context, group string) error {
	if f.failures > 0 {
		f.failures--
		return errors.New("agent unavailable")
	}
	return f.recordingCleanupInfrastructure.DestroyLabGroup(ctx, group)
}

// A deleted event's group is only requested once (queued with the delete); an
// agent error leaves the request pending and the next pass destroys it, then
// marks it done, so repeated passes never re-destroy.
func TestCleanupQueuedLabGroups_AgentErrorIsRetriedForDeletedEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &flakyCleanupInfrastructure{failures: 1}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})

	group := "e-deleted-event-t-team"
	q.EXPECT().ListPendingLabGroupCleanupRequests(gomock.Any()).Return([]string{group}, nil).Times(2)
	q.EXPECT().MarkLabGroupCleanupRequestDestroyed(gomock.Any(), gomock.Any()).Return(int64(1), nil).Times(1)

	if err := uc.CleanupQueuedLabGroups(context.Background()); err == nil {
		t.Fatal("agent error must be returned so the job is retried")
	}
	if err := uc.CleanupQueuedLabGroups(context.Background()); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(infra.groups) != 1 || infra.groups[0] != group {
		t.Fatalf("destroy calls = %#v, want %s once", infra.groups, group)
	}
}

// calendarGate stands in for the resource calendar: the event has a reservation (or not) that ends at end.
type calendarGate struct {
	reserved bool
	end      time.Time
}

func (calendarGate) HoldsForAllTeams(context.Context, uuid.UUID, resourcesModel.Amount, int, resourcesModel.Amount) error {
	return nil
}
func (g calendarGate) HasEventReservation(context.Context, uuid.UUID) (bool, error) {
	return g.reserved, nil
}
func (g calendarGate) EventReservationEnd(context.Context, uuid.UUID) (time.Time, bool, error) {
	return g.end, g.reserved, nil
}

// The withdrawal removes the groups of an event that has no active reservation (none, a cancelled one) and leaves
// the groups of an event whose active reservation ends by the withdrawal to the end of that reservation. Several
// events are decided one by one.
func TestCleanupWithdrawnLaboratories_DecidesPerEvent(t *testing.T) {
	withdraw := time.Now().Add(-time.Hour)
	for name, tc := range map[string]struct {
		gate      calendarGate
		destroyed bool
	}{
		"no reservation or a cancelled one":      {calendarGate{}, true},
		"reservation ends before the withdrawal": {calendarGate{reserved: true, end: withdraw.Add(-time.Minute)}, false},
		"reservation ends at the withdrawal":     {calendarGate{reserved: true, end: withdraw}, false},
		"reservation outlives the withdrawal":    {calendarGate{reserved: true, end: withdraw.Add(time.Hour)}, true},
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			infra := &recordingCleanupInfrastructure{}
			uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
			uc.SetResourceGate(tc.gate)

			eventID, bindingID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			q.EXPECT().QueueWithdrawnEmptyLabGroups(gomock.Any(), gomock.Any()).Return(nil)
			q.EXPECT().ListWithdrawnLabBindings(gomock.Any(), gomock.Any()).Return([]postgres.LabBinding{{ID: bindingID, EventID: eventID, LabGroupName: "e-team-a"}}, nil)
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{
				ID: eventID, LifecycleConfigured: true,
				PublishAt: withdraw.Add(-6 * time.Hour), StartAt: withdraw.Add(-5 * time.Hour),
				FinishAt:   pgtype.Timestamptz{Time: withdraw.Add(-time.Hour), Valid: true},
				WithdrawAt: pgtype.Timestamptz{Time: withdraw, Valid: true},
			}, nil).AnyTimes()
			if tc.destroyed {
				q.EXPECT().MarkLabBindingDestroyed(gomock.Any(), bindingID).Return(int64(1), nil)
			}
			require.NoError(t, uc.CleanupWithdrawnLaboratories(context.Background()))
			if tc.destroyed {
				assert.Equal(t, []string{"e-team-a"}, infra.groups)
			} else {
				assert.Empty(t, infra.groups)
			}
		})
	}
}
