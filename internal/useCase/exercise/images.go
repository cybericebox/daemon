package exercise

import (
	"context"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
)

// IImageAccess checks a lab's images right before its deploy and gives the
// topology to send (platform references rewritten) with exactly the pull
// credentials it needs (satisfied by imageRegistryUseCase.ImageRegistryUseCase).
type IImageAccess interface {
	ExerciseImageScope(ctx context.Context, exerciseID uuid.UUID) (imageRegistryUseCase.Scope, error)
	PrepareDeployImages(ctx context.Context, scope imageRegistryUseCase.Scope, topo exerciseModel.Topology) (imageRegistryUseCase.DeployImages, error)
}

// SetImageAccess wires the image registry; nil (narrow constructions) deploys
// the topology as stored, without checks or credentials.
func (u *ExerciseUseCase) SetImageAccess(a IImageAccess) { u.images = a }

// prepareLabImages checks the images of a test or preview lab before anything
// is created. An event preview pulls with its event's scope; a test lab with
// the exercise's own (the owner event of an event-owned exercise, none for a
// catalog one). An unavailable image returns ErrImagesUnavailable as it is.
func (u *ExerciseUseCase) prepareLabImages(ctx context.Context, eventID *uuid.UUID, exerciseID uuid.UUID, topo exerciseModel.Topology) (exerciseModel.Topology, []infraModel.RegistryCredential, error) {
	if u.images == nil {
		return topo, nil, nil
	}
	scope := imageRegistryUseCase.Scope{}
	if eventID != nil {
		id := *eventID
		scope.EventID = &id
	} else {
		s, err := u.images.ExerciseImageScope(ctx, exerciseID)
		if err != nil {
			return exerciseModel.Topology{}, nil, err
		}
		scope = s
	}
	prepared, err := u.images.PrepareDeployImages(ctx, scope, topo)
	if err != nil {
		return exerciseModel.Topology{}, nil, err
	}
	return prepared.Topology, prepared.Credentials, nil
}
