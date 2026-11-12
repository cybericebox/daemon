package imageRegistryUseCase

import (
	"context"
	"errors"
	"strings"

	"github.com/rs/zerolog/log"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/pkg/ociregistry"
)

// Why the start configuration of an image is not known (ImageServiceInfo.Reason): what the author can do about it is to
// upload the image to the platform.
const (
	// ServiceNotRead: a platform image uploaded before configurations were kept, or the store is not there.
	ServiceNotRead = "not_read"
	// ServiceUnreachable: the registry did not answer, or answered with an error or a limit.
	ServiceUnreachable = "unreachable"
	// ServiceUnauthorized: a private image with no credential the platform holds for it (an event registry secret).
	ServiceUnauthorized = "unauthorized"
	// ServiceNotFound: the registry has no such repository or tag.
	ServiceNotFound = "not_found"
	// ServicePlatformDependent: the image's user differs between linux/amd64 and linux/arm64.
	ServicePlatformDependent = "platform_dependent"
	// ServiceUnsupported: the reference is not valid, or the manifest is not an image of linux/amd64 or linux/arm64 that the
	// platform can read.
	ServiceUnsupported = "unsupported"
)

// externalKey is the normalized form of an external image reference: the key its configuration is stored under, so
// "nginx" and "docker.io/library/nginx:latest" are one.
func externalKey(ref imageRegistryModel.ImageRef) string {
	if ref.Digest != "" {
		return ref.Host + "/" + ref.Repository + "@" + ref.Digest
	}
	return ref.Host + "/" + ref.Repository + ":" + ref.Tag
}

// scopeKeys are the credential scopes a configuration may have been read in that this scope may use: the public one, and
// the event's own.
func scopeKeys(scope Scope) []string {
	if scope.EventID == nil {
		return []string{""}
	}
	return []string{"", scope.EventID.String()}
}

// sameServiceUser compares the users two platforms of one image declare, "" and "root" and "0" being the same account.
func sameServiceUser(a, b string) bool {
	root := func(s string) string {
		switch s {
		case "", "root", "0":
			return "0"
		}
		return s
	}
	return root(a) == root(b)
}

// serviceOfConfigs reads the start configuration out of the config blobs of the platforms of an image. When the image has
// both linux/amd64 and linux/arm64 and the user they declare differs, the configuration is not one: the platform does not
// guess which the lab runs. Otherwise the linux/amd64 configuration is used (the only one if the image has one).
func serviceOfConfigs(configs ociregistry.ImageConfigs) (exerciseModel.ImageService, string) {
	var chosen *exerciseModel.ImageService
	for _, platform := range configs.Platforms {
		service, err := exerciseModel.ParseImageService(platform.Config)
		if err != nil {
			return exerciseModel.ImageService{}, ServiceUnsupported
		}
		if chosen == nil {
			chosen = &service
			continue
		}
		if !sameServiceUser(chosen.User, service.User) {
			return exerciseModel.ImageService{}, ServicePlatformDependent
		}
		if platform.Arch == "amd64" {
			chosen = &service
		}
	}
	if chosen == nil {
		return exerciseModel.ImageService{}, ServiceUnsupported
	}
	return *chosen, ""
}

// reasonOfRegistryError maps what the registry client says to a reason the author can act on.
func reasonOfRegistryError(err error) string {
	switch {
	case errors.Is(err, ociregistry.ErrUnauthorized):
		return ServiceUnauthorized
	case errors.Is(err, ociregistry.ErrNotFound):
		return ServiceNotFound
	case errors.Is(err, ociregistry.ErrInvalidReference), errors.Is(err, ociregistry.ErrUnsupportedManifest),
		errors.Is(err, ociregistry.ErrNoLinuxPlatform), errors.Is(err, ociregistry.ErrBlobTooLarge),
		errors.Is(err, ociregistry.ErrManifestTooLarge):
		return ServiceUnsupported
	}
	return ServiceUnreachable
}

// readExternalService reads the start configuration of an external image from its registry: anonymously first (Docker Hub,
// GHCR, Quay: the token challenge is followed without a credential), and when that is refused and the scope is an event with
// a registry secret matching the image, with that secret, the way the availability check does. Nothing is stored here. It
// returns the configuration, the digest the reference had, the credential scope it was read in ("" public, else the event),
// or the reason it is not known.
func (u *ImageRegistryUseCase) readExternalService(ctx context.Context, scope Scope, image string) (exerciseModel.ImageService, string, string, string) {
	ref, err := imageRegistryModel.ParseImageRef(image)
	if err != nil {
		return exerciseModel.ImageService{}, "", "", ServiceUnsupported
	}
	reference := ref.Digest
	if reference == "" {
		reference = ref.Tag
	}
	ctx, cancel := context.WithTimeout(ctx, u.callTimeout)
	defer cancel()
	read := func(client *ociregistry.Client) (ociregistry.ImageConfigs, error) {
		hctx, stop := context.WithTimeout(ctx, u.headTimeout)
		defer stop()
		return client.ImageConfigs(hctx, reference)
	}
	scopeUsed := ""
	configs, err := read(u.external(ref.Host, ref.Repository, "", ""))
	if err != nil && scope.EventID != nil {
		secrets, tokens, secretsErr := u.EventRegistrySecretsForDeploy(ctx, *scope.EventID)
		if secretsErr == nil {
			if kind, secret := imageRegistryModel.Classify(image, secrets); kind == imageRegistryModel.KindOrganizer && secret != nil &&
				!agentRefuses(secret.Username, tokens[secret.ID]) {
				if withSecret, secretErr := read(u.external(ref.Host, ref.Repository, secret.Username, tokens[secret.ID])); secretErr == nil {
					configs, err, scopeUsed = withSecret, nil, scope.EventID.String()
				}
			}
		}
	}
	if err != nil {
		return exerciseModel.ImageService{}, "", "", reasonOfRegistryError(err)
	}
	service, reason := serviceOfConfigs(configs)
	if reason != "" {
		return exerciseModel.ImageService{}, "", "", reason
	}
	return service, configs.Digest, scopeUsed, ""
}

// storedExternalServices gives the stored configurations of the external references among the images, for the scopes.
func (u *ImageRegistryUseCase) storedExternalServices(ctx context.Context, scope Scope, images []string) map[string]exerciseModel.ImageService {
	out := map[string]exerciseModel.ImageService{}
	if u.extConfigs == nil {
		return out
	}
	keys := map[string]string{} // image -> normalized reference
	var references []string
	for _, image := range images {
		if strings.HasPrefix(image, "platform:") || image == "" {
			continue
		}
		ref, err := imageRegistryModel.ParseImageRef(image)
		if err != nil {
			continue
		}
		key := externalKey(ref)
		keys[image] = key
		references = append(references, key)
	}
	if len(references) == 0 {
		return out
	}
	stored, err := u.extConfigs.Get(ctx, references, scopeKeys(scope))
	if err != nil {
		log.Warn().Err(err).Msg("image config: the stored start configurations of the external images cannot be read")
		return out
	}
	for image, key := range keys {
		if entry, found := stored[key]; found {
			out[image] = entry.Service
		}
	}
	return out
}

// ResolveImageServices is ImageServices for a publish: the stored configurations (platform images, and external images
// already read in this scope), and for every other image that is by reference to an external registry, a read of its
// manifest and config blob (no layer) that is then stored by reference and scope, so a deploy never asks the registry. An
// image that cannot be read stays unknown with the reason (ImageServiceInfo.Reason). Platform images are never read here.
func (u *ImageRegistryUseCase) ResolveImageServices(ctx context.Context, scope Scope, images []string) []ImageServiceInfo {
	out := u.ImageServices(ctx, images)
	stored := u.storedExternalServices(ctx, scope, images)
	for i := range out {
		info := &out[i]
		if info.Known {
			continue
		}
		if strings.HasPrefix(info.Image, "platform:") {
			info.Reason = ServiceNotRead
			continue
		}
		if service, found := stored[info.Image]; found {
			info.Known, info.Service = true, service
			continue
		}
		info.Reason = u.resolveAndStore(ctx, scope, info)
	}
	return out
}

// resolveAndStore reads an external image and stores what it said; it returns "" when the configuration is known now, else
// the reason it is not.
func (u *ImageRegistryUseCase) resolveAndStore(ctx context.Context, scope Scope, info *ImageServiceInfo) string {
	if u.extConfigs == nil {
		return ServiceNotRead
	}
	service, digest, scopeUsed, reason := u.readExternalService(ctx, scope, info.Image)
	if reason != "" {
		return reason
	}
	ref, err := imageRegistryModel.ParseImageRef(info.Image)
	if err != nil {
		return ServiceUnsupported
	}
	if err = u.extConfigs.Save(ctx, externalKey(ref), scopeUsed, digest, service, u.now()); err != nil {
		// A configuration that was not kept cannot serve a deploy: not known, whatever the registry said.
		log.Warn().Err(err).Msg("image config: the start configuration of an external image was not stored")
		return ServiceNotRead
	}
	info.Known, info.Service = true, service
	return ""
}
