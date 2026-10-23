package exercise_test

import (
	"context"
	"encoding/json"
	"errors"
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

func TestCatalogColumnsKeepTheWidth(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	actor := exercise.Actor{UserID: uuid.Must(uuid.NewV7())}
	key := postgres.GetUserUIPreferenceParams{UserID: actor.UserID, Key: "catalog.columns"}

	var stored []byte
	q.EXPECT().UpsertUserUIPreference(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpsertUserUIPreferenceParams) error {
		stored = arg.Value
		return nil
	})
	if err := uc.SetCatalogColumns(context.Background(), actor, []exercise.CatalogColumn{{Key: "name", Visible: true, Width: 240}, {Key: "tags"}}); err != nil {
		t.Fatal(err)
	}
	if string(stored) != `[{"key":"name","visible":true,"width":240},{"key":"tags","visible":false}]` {
		t.Fatalf("stored %s: the width is kept, an unset one is left out", stored)
	}
	q.EXPECT().GetUserUIPreference(gomock.Any(), key).Return(stored, nil)
	columns, err := uc.CatalogColumns(context.Background(), actor)
	if err != nil || len(columns) != 2 || columns[0].Width != 240 || columns[1].Width != 0 {
		t.Fatalf("read back: %+v %v", columns, err)
	}
}

func TestValidateCatalogColumnsWidth(t *testing.T) {
	for width, valid := range map[int]bool{0: true, 60: true, 1600: true, 59: false, 1601: false, -1: false} {
		err := exercise.ValidateCatalogColumns([]exercise.CatalogColumn{{Key: "name", Width: width}})
		if (err == nil) != valid || (err != nil && !errors.Is(err, exercise.ErrCatalogColumnsInvalid)) {
			t.Errorf("width %d: %v, want valid=%v", width, err, valid)
		}
	}
}
