package exercise_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/pkg/secret"
)

// A draft with ':' in a device or variable name is refused when it is saved, before anything is sealed or written (the
// mock fails on any write): the two names are joined by ':' in the context of a secret variable.
func TestSaveDraft_RefusesTheSeparatorInANameBeforeWritingAnything(t *testing.T) {
	for name, mutate := range map[string]func(*exerciseModel.Device){
		"device": func(d *exerciseModel.Device) { d.Name = "a:b" },
		"env":    func(d *exerciseModel.Device) { d.Bootstrap.Env[0].Name = "DB:PASS" },
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := postgresMocks.NewMockQuerier(ctrl)
			cipher, _ := secret.New(testKey)
			uc := newTestExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: cipher, Media: newFakeMedia()})
			exID := uuid.Must(uuid.NewV7())
			q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{}, errNoRows()).AnyTimes()
			q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID}, nil).AnyTimes()

			variants := draftVariants("s3cret", true)
			mutate(&variants[0].Topology.Devices[0])
			_, err := uc.SaveDraft(context.Background(), exID, exercise.SaveDraftInput{Variants: variants})
			if !errors.Is(err, exerciseModel.ErrNameHasSeparator.Err()) {
				t.Fatalf("want ErrNameHasSeparator, got %v", err)
			}
		})
	}
}
