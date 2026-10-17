package imageRegistryModel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
)

// Manifest media types the upload builds.
const (
	MediaTypeOCIManifest    = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"

	mediaTypeOCIConfig    = "application/vnd.oci.image.config.v1+json"
	mediaTypeDockerConfig = "application/vnd.docker.container.image.v1+json"
)

// MaxImageLayers bounds the layers of one uploaded image (Docker's own limit is far lower).
const MaxImageLayers = 500

// ociLayerTypes are the OCI layer media types an upload accepts: tar, tar+gzip and tar+zstd.
var ociLayerTypes = map[string]bool{
	"application/vnd.oci.image.layer.v1.tar":      true,
	"application/vnd.oci.image.layer.v1.tar+gzip": true,
	"application/vnd.oci.image.layer.v1.tar+zstd": true,
}

// dockerToOCI maps each accepted Docker media type (config and the three layer spellings) to its OCI equivalent.
var dockerToOCI = map[string]string{
	mediaTypeDockerConfig:                               mediaTypeOCIConfig,
	"application/vnd.docker.image.rootfs.diff.tar":      "application/vnd.oci.image.layer.v1.tar",
	"application/vnd.docker.image.rootfs.diff.tar.gzip": "application/vnd.oci.image.layer.v1.tar+gzip",
	"application/vnd.docker.image.rootfs.diff.tar.zstd": "application/vnd.oci.image.layer.v1.tar+zstd",
}

// Descriptor names one blob of an image: its media type, sha256 digest and size.
type Descriptor struct {
	MediaType string
	Digest    string
	Size      int64
}

// Manifest is a built image manifest, ready to push.
type Manifest struct {
	MediaType string
	Body      []byte
	// Digest is "sha256:<hex>" of Body: the digest the registry stores it under.
	Digest string
	// BlobBytes is the size of the config and the layers together.
	BlobBytes int64
}

type manifestDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// manifestJSON fixes the field order, so the same descriptors always give the same bytes and digest.
type manifestJSON struct {
	SchemaVersion int                  `json:"schemaVersion"`
	MediaType     string               `json:"mediaType"`
	Config        manifestDescriptor   `json:"config"`
	Layers        []manifestDescriptor `json:"layers"`
}

// BuildManifest builds the image manifest of an uploaded image. All Docker media types (config and every layer)
// give a Docker v2 schema 2 manifest with those types; all OCI types give an OCI image manifest; a mix gives an OCI
// image manifest with each Docker type mapped to its OCI equivalent. Only tar, tar+gzip and tar+zstd layers are
// accepted. Invalid descriptors: ErrInvalidImageArchive; config and layers together over MaxImageArchiveBytes:
// ErrImageTooLarge.
func BuildManifest(config Descriptor, layers []Descriptor) (Manifest, error) {
	allDocker, ok := descriptorsValid(config, layers)
	if !ok {
		return Manifest{}, ErrInvalidImageArchive.Err()
	}
	total := config.Size
	for _, l := range layers {
		total += l.Size
	}
	if total > MaxImageArchiveBytes {
		return Manifest{}, ErrImageTooLarge.Err()
	}

	mediaType := MediaTypeOCIManifest
	typeOf := func(t string) string {
		if mapped, ok := dockerToOCI[t]; ok {
			return mapped
		}
		return t
	}
	if allDocker {
		mediaType = MediaTypeDockerManifest
		typeOf = func(t string) string { return t }
	}
	m := manifestJSON{
		SchemaVersion: 2,
		MediaType:     mediaType,
		Config:        manifestDescriptor{MediaType: typeOf(config.MediaType), Digest: config.Digest, Size: config.Size},
		Layers:        make([]manifestDescriptor, 0, len(layers)),
	}
	for _, l := range layers {
		m.Layers = append(m.Layers, manifestDescriptor{MediaType: typeOf(l.MediaType), Digest: l.Digest, Size: l.Size})
	}
	body, err := json.Marshal(m)
	if err != nil {
		return Manifest{}, err
	}
	sum := sha256.Sum256(body)
	return Manifest{MediaType: mediaType, Body: body, Digest: "sha256:" + hex.EncodeToString(sum[:]), BlobBytes: total}, nil
}

// descriptorsValid checks the digests, sizes, layer count and media types; allDocker reports that every media type
// is a Docker one.
func descriptorsValid(config Descriptor, layers []Descriptor) (allDocker, ok bool) {
	if len(layers) == 0 || len(layers) > MaxImageLayers || !ValidDigest(config.Digest) || config.Size < 1 {
		return false, false
	}
	allDocker = config.MediaType == mediaTypeDockerConfig
	if !allDocker && config.MediaType != mediaTypeOCIConfig {
		return false, false
	}
	for _, l := range layers {
		if !ValidDigest(l.Digest) || l.Size < 1 {
			return false, false
		}
		mapped, docker := dockerToOCI[l.MediaType]
		switch {
		case docker && ociLayerTypes[mapped]:
			// a Docker layer type
		case ociLayerTypes[l.MediaType]:
			allDocker = false
		default:
			return false, false
		}
	}
	return allDocker, true
}

// ValidDigest reports whether s is "sha256:" and 64 lowercase hex characters.
func ValidDigest(s string) bool { return digestRe.MatchString(s) }

// PlatformImage is one image pushed to the platform registry: the tag the tag collector may remove once no
// exercise version references the digest and the upload is older than a day.
type PlatformImage struct {
	Digest, Tag, Name string
	// ExerciseID is the exercise it was uploaded for; UploadedBy who uploaded it (uuid.Nil: unknown).
	ExerciseID, UploadedBy uuid.UUID
	UploadedAt             time.Time
}

// NewPlatformImage records an upload of ref at now.
func NewPlatformImage(ref PlatformRef, exerciseID, uploadedBy uuid.UUID, now time.Time) PlatformImage {
	return PlatformImage{Digest: ref.Digest, Tag: ref.Tag(), Name: ref.Name, ExerciseID: exerciseID, UploadedBy: uploadedBy, UploadedAt: now}
}
