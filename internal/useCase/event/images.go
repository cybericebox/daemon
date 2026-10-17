package event

import (
	"context"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
)

// ImageAccess checks a lab's images right before its deploy (or prewarm) and
// gives the topology to send (platform references rewritten) with exactly the
// pull credentials it needs (satisfied by imageRegistryUseCase.ImageRegistryUseCase).
type ImageAccess interface {
	PrepareDeployImages(ctx context.Context, scope imageRegistryUseCase.Scope, topo exerciseModel.Topology) (imageRegistryUseCase.DeployImages, error)
}

// SetImageAccess wires the image registry; nil (narrow constructions) deploys
// and prewarms the topology as stored, without checks or credentials.
func (u *EventUseCase) SetImageAccess(a ImageAccess) { u.images = a }

// prepareEventLabImages checks the images of one of the event's labs with the
// event's own scope (its organizer secrets, the platform registry, anonymous).
// An unavailable image returns ErrImagesUnavailable with the list in
// DeployImages.Unavailable.
func (u *EventUseCase) prepareEventLabImages(ctx context.Context, eventID uuid.UUID, topo exerciseModel.Topology) (imageRegistryUseCase.DeployImages, error) {
	if u.images == nil {
		return imageRegistryUseCase.DeployImages{Topology: topo}, nil
	}
	id := eventID
	return u.images.PrepareDeployImages(ctx, imageRegistryUseCase.Scope{EventID: &id}, topo)
}
