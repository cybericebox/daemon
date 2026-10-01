package labBindingRepo_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

func TestCreate_MapsBinding(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	repo := labBindingRepo.New(q)
	now := time.Now()
	value, err := labBindingModel.New(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), "event-team", "exercise", now)
	if err != nil {
		t.Fatal(err)
	}
	q.EXPECT().CreateLabBinding(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateLabBindingParams) (postgres.LabBinding, error) {
		if arg.ID != value.ID || arg.EventID != value.EventID || arg.EventTeamID != value.EventTeamID || arg.EventChallengeID != value.EventChallengeID || arg.LabGroupName != value.LabGroupName || arg.LabName != value.LabName {
			t.Fatalf("unexpected binding params: %+v", arg)
		}
		return postgres.LabBinding{ID: arg.ID, EventID: arg.EventID, EventTeamID: arg.EventTeamID, EventChallengeID: arg.EventChallengeID, LabGroupName: arg.LabGroupName, LabName: arg.LabName, CreatedAt: arg.CreatedAt}, nil
	})

	created, wasCreated, err := repo.Create(context.Background(), value)
	if err != nil || !wasCreated || created != value {
		t.Fatalf("Create = %+v, %v, %v", created, wasCreated, err)
	}
}
