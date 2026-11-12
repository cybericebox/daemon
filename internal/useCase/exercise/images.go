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

// imageServiceReader is the optional part of the image registry that gives the start configuration of images, reading and
// storing that of an external image the first time it is asked for.
type imageServiceReader interface {
	ResolveImageServices(ctx context.Context, scope imageRegistryUseCase.Scope, images []string) []imageRegistryUseCase.ImageServiceInfo
}

// requireServiceUsersKnown is the publish gate of the service user: a device that is started through the launcher (a
// bootstrap list, a command or arguments) runs as the user its image declares. The platform knows it for a platform image
// (stored when it was uploaded) and reads it for an image by reference to an external registry, from its manifest and config
// blob without a layer, at this moment, so a deploy never needs the registry. An image that cannot be read (the registry does
// not answer or refuses, a private image without a credential, a user that differs between linux/amd64 and linux/arm64)
// cannot be published with such a device: the platform does not guess the user. Drafts are not checked. The error names the
// variant, the device, field "image" and the reason. Skipped without an image registry that can tell (narrow constructions).
func (u *ExerciseUseCase) requireServiceUsersKnown(ctx context.Context, exerciseID uuid.UUID, variants []exerciseModel.Variant) error {
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
	scope, err := u.images.ExerciseImageScope(ctx, exerciseID)
	if err != nil {
		return err
	}
	configs := map[string]*exerciseModel.ImageService{}
	reasons := map[string]string{}
	for _, info := range reader.ResolveImageServices(ctx, scope, images) {
		if info.Known {
			service := info.Service
			configs[info.Image] = &service
		} else {
			reasons[info.Image] = info.Reason
		}
	}
	for position, variant := range variants {
		for _, device := range variant.Topology.Devices {
			if !device.NeedsServiceCommand() || device.Image == "" {
				continue
			}
			if _, known := device.ResolveServiceUser(configs[device.Image]); !known {
				failure := exerciseModel.ErrServiceUserUnknown.WithPublicContext("variant", position+1).
					WithPublicContext("device", device.Name).WithPublicContext("field", "image")
				if reason := reasons[device.Image]; reason != "" {
					failure = failure.WithPublicContext("reason", reason)
				}
				return failure.Err()
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
