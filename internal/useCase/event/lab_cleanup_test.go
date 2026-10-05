package event_test

import (
	"context"
	"errors"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"testing"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
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
