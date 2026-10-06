package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

// sweepInfrastructure lists a fixed set of groups and records what is destroyed.
type sweepInfrastructure struct {
	recordingCleanupInfrastructure
	listed    []infraModel.LabGroupInfo
	destroyed []string
}

func (f *sweepInfrastructure) ListSweepableGroups(context.Context) ([]infraModel.LabGroupInfo, int, error) {
	return f.listed, 0, nil
}

func (f *sweepInfrastructure) DestroyLabGroupOn(_ context.Context, _ uuid.UUID, group string) error {
	f.destroyed = append(f.destroyed, group)
	return nil
}

var sweepNow = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func standGroup(name string, eventID, teamID uuid.UUID, age time.Duration) infraModel.LabGroupInfo {
	return infraModel.LabGroupInfo{Name: name, CreatedAt: sweepNow.Add(-age), Labels: map[string]string{
		infraModel.LabelKind: infraModel.KindStand, infraModel.LabelEvent: eventID.String(), infraModel.LabelTeam: teamID.String(),
	}}
}

func testGroup(name string, age time.Duration) infraModel.LabGroupInfo {
	return infraModel.LabGroupInfo{Name: name, CreatedAt: sweepNow.Add(-age), Labels: map[string]string{infraModel.LabelKind: infraModel.KindTest}}
}

func TestSweepOrphanLabGroups(t *testing.T) {
	ev, team := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	old := time.Hour

	t.Run("owner present keeps the group", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		q := newFormGateMock(ctrl)
		infra := &sweepInfrastructure{listed: []infraModel.LabGroupInfo{standGroup("g1", ev, team, old), testGroup("tu-1", old)}}
		uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
		q.EXPECT().ListExistingEventIDs(gomock.Any(), gomock.Any()).Return([]uuid.UUID{ev}, nil)
		q.EXPECT().ListExistingEventTeamIDs(gomock.Any(), gomock.Any()).Return([]uuid.UUID{team}, nil)
		q.EXPECT().ListTestDeployGroupNames(gomock.Any(), []string{"tu-1"}).Return([]string{"tu-1"}, nil)
		if err := uc.SweepOrphanLabGroupsAt(context.Background(), sweepNow); err != nil {
			t.Fatal(err)
		}
		if len(infra.destroyed) != 0 {
			t.Fatalf("destroyed %v", infra.destroyed)
		}
	})

	t.Run("event gone deletes the group", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		q := newFormGateMock(ctrl)
		infra := &sweepInfrastructure{listed: []infraModel.LabGroupInfo{standGroup("g1", ev, team, old)}}
		uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
		q.EXPECT().ListExistingEventIDs(gomock.Any(), gomock.Any()).Return(nil, nil)
		q.EXPECT().ListExistingEventTeamIDs(gomock.Any(), gomock.Any()).Return(nil, nil)
		if err := uc.SweepOrphanLabGroupsAt(context.Background(), sweepNow); err != nil {
			t.Fatal(err)
		}
		if len(infra.destroyed) != 1 || infra.destroyed[0] != "g1" {
			t.Fatalf("destroyed %v", infra.destroyed)
		}
	})

	t.Run("team gone deletes the group", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		q := newFormGateMock(ctrl)
		infra := &sweepInfrastructure{listed: []infraModel.LabGroupInfo{standGroup("g1", ev, team, old)}}
		uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
		q.EXPECT().ListExistingEventIDs(gomock.Any(), gomock.Any()).Return([]uuid.UUID{ev}, nil)
		q.EXPECT().ListExistingEventTeamIDs(gomock.Any(), gomock.Any()).Return(nil, nil)
		if err := uc.SweepOrphanLabGroupsAt(context.Background(), sweepNow); err != nil {
			t.Fatal(err)
		}
		if len(infra.destroyed) != 1 {
			t.Fatalf("destroyed %v", infra.destroyed)
		}
	})

	t.Run("a test group without a deploy is deleted", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		q := newFormGateMock(ctrl)
		infra := &sweepInfrastructure{listed: []infraModel.LabGroupInfo{testGroup("tu-1", old)}}
		uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
		q.EXPECT().ListTestDeployGroupNames(gomock.Any(), gomock.Any()).Return(nil, nil)
		if err := uc.SweepOrphanLabGroupsAt(context.Background(), sweepNow); err != nil {
			t.Fatal(err)
		}
		if len(infra.destroyed) != 1 || infra.destroyed[0] != "tu-1" {
			t.Fatalf("destroyed %v", infra.destroyed)
		}
	})

	t.Run("a young group is kept without even asking the database", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		q := newFormGateMock(ctrl)
		infra := &sweepInfrastructure{listed: []infraModel.LabGroupInfo{standGroup("g1", ev, team, time.Minute), testGroup("tu-1", 14*time.Minute)}}
		uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
		if err := uc.SweepOrphanLabGroupsAt(context.Background(), sweepNow); err != nil {
			t.Fatal(err)
		}
		if len(infra.destroyed) != 0 {
			t.Fatalf("destroyed %v", infra.destroyed)
		}
	})

	t.Run("a group of unknown age is kept", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		q := newFormGateMock(ctrl)
		g := testGroup("tu-1", old)
		g.CreatedAt = time.Time{}
		infra := &sweepInfrastructure{listed: []infraModel.LabGroupInfo{g}}
		uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
		if err := uc.SweepOrphanLabGroupsAt(context.Background(), sweepNow); err != nil {
			t.Fatal(err)
		}
		if len(infra.destroyed) != 0 {
			t.Fatalf("destroyed %v", infra.destroyed)
		}
	})

	t.Run("a database error keeps every group", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		q := newFormGateMock(ctrl)
		infra := &sweepInfrastructure{listed: []infraModel.LabGroupInfo{standGroup("g1", ev, team, old), testGroup("tu-1", old)}}
		uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
		q.EXPECT().ListExistingEventIDs(gomock.Any(), gomock.Any()).Return(nil, errors.New("connection reset"))
		q.EXPECT().ListTestDeployGroupNames(gomock.Any(), gomock.Any()).Return(nil, errors.New("connection reset"))
		if err := uc.SweepOrphanLabGroupsAt(context.Background(), sweepNow); err != nil {
			t.Fatal(err)
		}
		if len(infra.destroyed) != 0 {
			t.Fatalf("destroyed %v", infra.destroyed)
		}
	})

	t.Run("a failed team read keeps the group even when the event is gone", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		q := newFormGateMock(ctrl)
		infra := &sweepInfrastructure{listed: []infraModel.LabGroupInfo{standGroup("g1", ev, team, old)}}
		uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
		q.EXPECT().ListExistingEventIDs(gomock.Any(), gomock.Any()).Return(nil, nil)
		q.EXPECT().ListExistingEventTeamIDs(gomock.Any(), gomock.Any()).Return(nil, errors.New("timeout"))
		if err := uc.SweepOrphanLabGroupsAt(context.Background(), sweepNow); err != nil {
			t.Fatal(err)
		}
		if len(infra.destroyed) != 0 {
			t.Fatalf("destroyed %v", infra.destroyed)
		}
	})

	t.Run("a stand group with an unreadable label is kept", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		q := newFormGateMock(ctrl)
		g := standGroup("g1", ev, team, old)
		g.Labels[infraModel.LabelTeam] = "not-a-uuid"
		infra := &sweepInfrastructure{listed: []infraModel.LabGroupInfo{g}}
		uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
		if err := uc.SweepOrphanLabGroupsAt(context.Background(), sweepNow); err != nil {
			t.Fatal(err)
		}
		if len(infra.destroyed) != 0 {
			t.Fatalf("destroyed %v", infra.destroyed)
		}
	})
}
