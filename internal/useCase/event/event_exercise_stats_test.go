package event_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

// The event's task list carries the pinned version's average difficulty, subtask count and names; a version
// without a variant has no count and no names, one without a difficulty no average.
func TestListEventExercises_PinnedStats(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListEventExerciseDetails(gomock.Any(), eventID).Return([]postgres.ListEventExerciseDetailsRow{
		{ID: uuid.Must(uuid.NewV7()), ExerciseID: uuid.Must(uuid.NewV7()), ExerciseVersionID: versionID, ExerciseName: "Web",
			HasAvgDifficulty: true, AvgDifficulty: 2.5, HasTaskCount: true, TaskNames: []string{"Recon", "Login"}},
		{ID: uuid.Must(uuid.NewV7()), ExerciseID: uuid.Must(uuid.NewV7()), ExerciseVersionID: uuid.Must(uuid.NewV7()), ExerciseName: "Empty"},
	}, nil)
	q.EXPECT().ListVersionVariantDevices(gomock.Any(), gomock.Any()).Return(nil, nil)
	// The flag warnings are advisory: a failed read only logs.
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{}, errors.New("not needed here")).AnyTimes()

	items, err := uc.ListEventExercises(context.Background(), eventID)
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	web, empty := items[0], items[1]
	if web.AvgDifficulty == nil || *web.AvgDifficulty != 2.5 || web.TaskCount == nil || *web.TaskCount != 2 || len(web.TaskNames) != 2 || web.TaskNames[0] != "Recon" {
		t.Fatalf("pinned stats: %+v", web)
	}
	if empty.AvgDifficulty != nil || empty.TaskCount != nil || len(empty.TaskNames) != 0 {
		t.Fatalf("no variant: %+v", empty)
	}
}
