package exercise_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"github.com/cybericebox/daemon/internal/useCase/exercise"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
)

const rewrittenWeb = "ghcr.io/acme/tasks@sha256:1111111111111111111111111111111111111111111111111111111111111111"

var platformPull = infraModel.RegistryCredential{Registry: "ghcr.io", Username: "acme", Password: "pull-token-fixed"}

// fakeImageAccess stands in for the image registry: platform references are rewritten and the
// platform pull credential handed out, or the deploy is refused.
type fakeImageAccess struct {
	ownerEvent  *uuid.UUID
	scopeAsked  []uuid.UUID
	scopes      []imageRegistryUseCase.Scope
	topos       []exerciseModel.Topology
	unavailable []imageRegistryModel.Availability
}

func (f *fakeImageAccess) ExerciseImageScope(_ context.Context, exerciseID uuid.UUID) (imageRegistryUseCase.Scope, error) {
	f.scopeAsked = append(f.scopeAsked, exerciseID)
	return imageRegistryUseCase.Scope{EventID: f.ownerEvent}, nil
}

func (f *fakeImageAccess) PrepareDeployImages(_ context.Context, scope imageRegistryUseCase.Scope, topo exerciseModel.Topology) (imageRegistryUseCase.DeployImages, error) {
	f.scopes = append(f.scopes, scope)
	f.topos = append(f.topos, topo)
	if len(f.unavailable) > 0 {
		return imageRegistryUseCase.DeployImages{Unavailable: f.unavailable}, imageRegistryModel.ErrImagesUnavailable.
			WithPublicContext(imageRegistryModel.ContextImages, f.unavailable).Err()
	}
	out := topo
	out.Devices = slices.Clone(topo.Devices)
	var creds []infraModel.RegistryCredential
	for i, d := range out.Devices {
		if strings.HasPrefix(d.Image, "platform:") {
			out.Devices[i].Image = rewrittenWeb
			creds = []infraModel.RegistryCredential{platformPull}
		}
	}
	return imageRegistryUseCase.DeployImages{Topology: out, Credentials: creds}, nil
}

// imageFixture: one version of exerciseID with one variant whose web device uses image.
func imageFixture(t *testing.T, exerciseID uuid.UUID, image string) (*postgresMocks.MockQuerier, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	q.EXPECT().ListOwnedExerciseTestDeploys(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	variantID, versionID, ownerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	device := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: image,
		EnvVars: []exerciseModel.EnvVar{{Name: "MODE", Value: "prod"}}}
	variants, err := json.Marshal([]exerciseModel.Variant{{ID: variantID, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{device}}}})
	require.NoError(t, err)
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), versionID).Return(postgres.ExerciseVersion{ID: versionID, ExerciseID: exerciseID, Variants: variants}, nil).AnyTimes()
	return q, variantID, versionID, ownerID
}

func createdDeploy(q *postgresMocks.MockQuerier) {
	q.EXPECT().CreateExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
		return postgres.ExerciseTestDeployment{ID: p.ID, EventID: p.EventID, GroupName: p.GroupName, LabName: p.LabName, VersionID: p.VersionID, VariantID: p.VariantID, CreatedBy: p.CreatedBy, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt}, nil
	})
}

// An unavailable image refuses the test lab before anything is created: no calendar hold, no row, no agent call;
// the author gets ErrImagesUnavailable with the images as it is.
func TestDeployVariantTest_UnavailableImageRefusesBeforeAnythingIsCreated(t *testing.T) {
	exerciseID := uuid.Must(uuid.NewV7())
	q, variantID, versionID, ownerID := imageFixture(t, exerciseID, "platform:web@sha256:"+strings.Repeat("2", 64))
	infra := &fakeInfra{}
	gate := &fakeGate{}
	images := &fakeImageAccess{unavailable: []imageRegistryModel.Availability{{
		Image: "platform:web@sha256:" + strings.Repeat("2", 64), Status: imageRegistryModel.StatusUnavailable, Reason: imageRegistryModel.ReasonNotFound,
	}}}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	uc.SetTestLabGate(gate)
	uc.SetImageAccess(images)

	_, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID, 0)
	require.True(t, errors.Is(err, imageRegistryModel.ErrImagesUnavailable.Err()), "got %v", err)
	require.Zero(t, infra.deployCalls)
	require.Empty(t, gate.admitted)
}

// An available platform image: the agent gets the rewritten topology and exactly the platform credential.
func TestDeployVariantTest_SendsTheRewrittenTopologyAndThePlatformCredential(t *testing.T) {
	exerciseID := uuid.Must(uuid.NewV7())
	q, variantID, versionID, ownerID := imageFixture(t, exerciseID, "platform:web@sha256:"+strings.Repeat("1", 64))
	createdDeploy(q)
	infra := &fakeInfra{}
	images := &fakeImageAccess{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra})
	uc.SetImageAccess(images)

	_, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID, 0)
	require.NoError(t, err)
	require.Equal(t, 1, infra.deployCalls)
	require.Equal(t, rewrittenWeb, infra.deployedTopo.Devices[0].Image)
	require.Equal(t, "prod", infra.deployedTopo.Devices[0].EnvVars[0].Value)
	require.Equal(t, []infraModel.RegistryCredential{platformPull}, infra.meta.PullCredentials)
}

// A test lab of an exercise pulls with the exercise's own scope: the owner event of an event-owned exercise,
// none for a catalog one.
func TestDeployVariantTest_UsesTheExerciseImageScope(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner *uuid.UUID
	}{
		{"catalog exercise", nil},
		{"event-owned exercise", new(uuid.Must(uuid.NewV7()))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exerciseID := uuid.Must(uuid.NewV7())
			q, variantID, versionID, ownerID := imageFixture(t, exerciseID, "nginx:1")
			createdDeploy(q)
			images := &fakeImageAccess{ownerEvent: tc.owner}
			uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: &fakeInfra{}})
			uc.SetImageAccess(images)

			_, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID, 0)
			require.NoError(t, err)
			require.Equal(t, []uuid.UUID{exerciseID}, images.scopeAsked)
			require.Len(t, images.scopes, 1)
			require.Equal(t, tc.owner, images.scopes[0].EventID)
		})
	}
}

// An event preview pulls with the event's scope, whatever owns the exercise.
func TestEventPreviewDeploy_UsesTheEventScope(t *testing.T) {
	exerciseID, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q, variantID, versionID, ownerID := imageFixture(t, exerciseID, "platform:web@sha256:"+strings.Repeat("1", 64))
	createdDeploy(q)
	infra := &eventPreviewInfra{fakeInfra: fakeInfra{status: exerciseModel.LabDeployStatus{VPNConfig: "event-config"}}}
	images := &fakeImageAccess{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: &fakeVPNStore{}})
	uc.SetTestLabGate(&eventPreviewGate{until: time.Now().Add(time.Hour)})
	uc.SetImageAccess(images)

	_, err := uc.DeployEventPreviewVariantTest(context.Background(), eventID, ownerID, versionID, variantID, 0)
	require.NoError(t, err)
	require.Empty(t, images.scopeAsked)
	require.Len(t, images.scopes, 1)
	require.NotNil(t, images.scopes[0].EventID)
	require.Equal(t, eventID, *images.scopes[0].EventID)
	require.Equal(t, rewrittenWeb, infra.deployedTopo.Devices[0].Image)
	require.Equal(t, []infraModel.RegistryCredential{platformPull}, infra.meta.PullCredentials)
}
