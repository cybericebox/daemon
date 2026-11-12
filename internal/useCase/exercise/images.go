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

// imageServiceReader is the optional part of the image registry that gives the stored start configuration of images.
type imageServiceReader interface {
	ImageServices(ctx context.Context, images []string) []imageRegistryUseCase.ImageServiceInfo
}

// requireServiceUsersKnown is the publish gate of the service user: a device that is started through the launcher (a
// bootstrap list, a command or arguments) runs as the user its image declares, which the platform knows only for a platform
// image whose configuration it stored. Any other image (an external registry reference, an old platform image) cannot be
// published with such a device: the platform does not guess the user. Drafts are not checked. The error names the variant
// and the device, field "image". Skipped without an image registry that can tell (narrow constructions).
func (u *ExerciseUseCase) requireServiceUsersKnown(ctx context.Context, variants []exerciseModel.Variant) error {
	reader, ok := u.images.(imageServiceReader)
	if !ok {
		return nil
	}
	var images []string
	for _, variant := range variants {
		for _, device := range variant.Topology.Devices {
			if device.NeedsServiceCommand() && device.Image != "" {
				images = append(images, device.Image)
			}
		}
	}
	if len(images) == 0 {
		return nil
	}
	configs := map[string]*exerciseModel.ImageService{}
	for _, info := range reader.ImageServices(ctx, images) {
		if info.Known {
			service := info.Service
			configs[info.Image] = &service
		}
	}
	for position, variant := range variants {
		for _, device := range variant.Topology.Devices {
			if !device.NeedsServiceCommand() || device.Image == "" {
				continue
			}
			if _, known := device.ResolveServiceUser(configs[device.Image]); !known {
				return exerciseModel.ErrServiceUserUnknown.WithPublicContext("variant", position+1).
					WithPublicContext("device", device.Name).WithPublicContext("field", "image").Err()
			}
		}
	}
	return nil
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
