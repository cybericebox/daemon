package exercise_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/useCase/exercise"
)

func TestCatalogColumnsReadAndSave(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	actor := exercise.Actor{UserID: uuid.Must(uuid.NewV7())}
	key := postgres.GetUserUIPreferenceParams{UserID: actor.UserID, Key: "catalog.columns"}

	q.EXPECT().GetUserUIPreference(gomock.Any(), key).Return(nil, pgx.ErrNoRows)
	if columns, err := uc.CatalogColumns(context.Background(), actor); err != nil || columns != nil {
		t.Fatalf("never arranged: %v %v", columns, err)
	}
	// A value that no longer reads (or no longer validates) falls back to the default instead of failing the page.
	q.EXPECT().GetUserUIPreference(gomock.Any(), key).Return([]byte(`[{"key":"BAD"}]`), nil)
	if columns, err := uc.CatalogColumns(context.Background(), actor); err != nil || columns != nil {
		t.Fatalf("stale value: %v %v", columns, err)
	}

	want := []exercise.CatalogColumn{{Key: "status", Visible: true}, {Key: "owner"}}
	q.EXPECT().UpsertUserUIPreference(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpsertUserUIPreferenceParams) error {
		var stored []exercise.CatalogColumn
		if arg.UserID != actor.UserID || arg.Key != "catalog.columns" || json.Unmarshal(arg.Value, &stored) != nil || len(stored) != 2 || stored[1] != want[1] {
			t.Fatalf("stored %+v", arg)
		}
		return nil
	})
	if err := uc.SetCatalogColumns(context.Background(), actor, want); err != nil {
		t.Fatal(err)
	}
	q.EXPECT().GetUserUIPreference(gomock.Any(), key).Return([]byte(`[{"key":"status","visible":true},{"key":"owner","visible":false}]`), nil)
	if columns, err := uc.CatalogColumns(context.Background(), actor); err != nil || len(columns) != 2 || columns[0] != want[0] {
		t.Fatalf("read back: %v %v", columns, err)
	}
}
