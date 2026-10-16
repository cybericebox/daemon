package exercise_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/internal/useCase/exercise"
)

// An event whose infrastructure is forbidden can still test a static variant (no devices); a lab variant of the
// same exercise stays refused.
func TestAuthorizeTestDeploy_EventWithoutInfrastructure(t *testing.T) {
	userID, eventID, exerciseID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	staticID, labID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	variants, _ := json.Marshal([]exerciseModel.Variant{
		{ID: staticID},
		{ID: labID, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "web"}}}},
	})
	for _, tc := range []struct {
		name    string
		variant uuid.UUID
		wantErr error
	}{
		{name: "static variant starts", variant: staticID},
		{name: "lab variant is refused", variant: labID, wantErr: exerciseModel.ErrExerciseInfrastructureNotAllowed.Err()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, ExerciseID: exerciseID, Variants: variants}, nil).AnyTimes()
			q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(postgres.Exercise{ID: exerciseID, Scope: 1, OwnerEventID: uuid.NullUUID{UUID: eventID, Valid: true}}, nil).AnyTimes()
			q.EXPECT().GetEventInfrastructureAllowed(gomock.Any(), eventID).Return(false, nil).AnyTimes()
			uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
			err := uc.AuthorizeTestDeploy(context.Background(), exercise.Actor{UserID: userID, Role: rbac.RoleSuperAdmin}, exerciseID, versionID, tc.variant)
			if !errors.Is(err, tc.wantErr) && (tc.wantErr != nil || err != nil) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}
