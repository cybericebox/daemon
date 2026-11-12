package ociregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

// Limits of reading an image's start configuration without its layers.
const (
	// MaxManifestBytes bounds a manifest or an index read whole.
	MaxManifestBytes = 4 << 20
	// MaxConfigBytes bounds an image config blob read whole (a few KiB in practice).
	MaxConfigBytes = 1 << 20
	// maxIndexScan bounds how many entries of an index are looked at.
	maxIndexScan = 256
)

var (
	// ErrUnsupportedManifest: the manifest is not an OCI or Docker v2 image manifest or index, or is malformed.
	ErrUnsupportedManifest = errors.New("registry manifest is not an image manifest or index")
	// ErrManifestTooLarge: a manifest or an index is larger than MaxManifestBytes.
	ErrManifestTooLarge = errors.New("registry manifest too large")
	// ErrNoLinuxPlatform: the image has no linux/amd64 or linux/arm64 manifest, the platforms the laboratories run.
	ErrNoLinuxPlatform = errors.New("image has no linux/amd64 or linux/arm64 manifest")
)

// PlatformConfig is the config blob of one platform of an image.
type PlatformConfig struct {
	OS, Arch string
	// ManifestDigest is the digest of the manifest of this platform.
	ManifestDigest string
	// Config is the config blob, verified against its digest.
	Config []byte
}

// ImageConfigs is what a reference resolved to: the digest of the manifest or index the reference points at, and the
// config of every platform the laboratories run (linux/amd64, linux/arm64) that the image has.
type ImageConfigs struct {
	Digest    string
	Platforms []PlatformConfig
}

type manifestEntry struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  *struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"platform"`
}

type imageManifestDoc struct {
	MediaType string          `json:"mediaType"`
	Config    *manifestEntry  `json:"config"`
	Manifests []manifestEntry `json:"manifests"`
}

func isIndexType(mediaType string) bool {
	return mediaType == "application/vnd.oci.image.index.v1+json" ||
		mediaType == "application/vnd.docker.distribution.manifest.list.v2+json"
}

func isManifestType(mediaType string) bool {
	return mediaType == "application/vnd.oci.image.manifest.v1+json" ||
		mediaType == "application/vnd.docker.distribution.manifest.v2+json"
}

// getManifest reads one manifest (a tag or a digest), at most MaxManifestBytes. When the reference is a digest the body is
// checked against it. It returns the body, its media type and its digest.
func (c *Client) getManifest(ctx context.Context, reference string) (body []byte, mediaType, digest string, err error) {
	repo, err := c.repo()
	if err != nil {
		return nil, "", "", err
	}
	hc, err := c.http(ctx, repo, transport.PullScope)
	if err != nil {
		return nil, "", "", c.wrap("get manifest", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base(repo)+"/manifests/"+reference, nil)
	if err != nil {
		return nil, "", "", err
	}
	req.Header.Set("Accept", manifestAccept)
	resp, err := c.do(hc, req, "get manifest")
	if err != nil {
		return nil, "", "", err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return nil, "", "", statusFailure("get manifest", resp)
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, MaxManifestBytes+1))
	if err != nil {
		return nil, "", "", c.wrap("get manifest", err)
	}
	if len(body) > MaxManifestBytes {
		return nil, "", "", fmt.Errorf("get manifest: %w", ErrManifestTooLarge)
	}
	sum := sha256.Sum256(body)
	digest = "sha256:" + hex.EncodeToString(sum[:])
	if strings.HasPrefix(reference, "sha256:") && reference != digest {
		return nil, "", "", fmt.Errorf("get manifest: %w: content does not match its digest", ErrUnreachable)
	}
	mediaType, _, _ = strings.Cut(resp.Header.Get("Content-Type"), ";")
	return body, strings.TrimSpace(mediaType), digest, nil
}

// ImageConfigs reads the start configuration of an image without pulling a layer: the manifest of the reference, for an
// index the manifests of linux/amd64 and linux/arm64 (the platforms the laboratories run), and the config blob of each.
// Only those few small documents are read, each within its limit and verified against its digest. An image with neither
// platform gives ErrNoLinuxPlatform; a manifest of another kind ErrUnsupportedManifest.
func (c *Client) ImageConfigs(ctx context.Context, reference string) (ImageConfigs, error) {
	body, mediaType, digest, err := c.getManifest(ctx, reference)
	if err != nil {
		return ImageConfigs{}, err
	}
	var doc imageManifestDoc
	if json.Unmarshal(body, &doc) != nil {
		return ImageConfigs{}, fmt.Errorf("get manifest: %w", ErrUnsupportedManifest)
	}
	if mediaType == "" {
		mediaType = doc.MediaType
	}
	out := ImageConfigs{Digest: digest}
	switch {
	case isManifestType(mediaType):
		// A single manifest has no platform of its own: the config says it.
		platform, err := c.platformConfig(ctx, digest, doc)
		if err != nil {
			return ImageConfigs{}, err
		}
		if platform.OS == "linux" && (platform.Arch == "amd64" || platform.Arch == "arm64") {
			out.Platforms = append(out.Platforms, platform)
		}
	case isIndexType(mediaType):
		seen := map[string]bool{}
		for i, entry := range doc.Manifests {
			if i >= maxIndexScan {
				break
			}
			if entry.Platform == nil || entry.Platform.OS != "linux" ||
				(entry.Platform.Architecture != "amd64" && entry.Platform.Architecture != "arm64") ||
				seen[entry.Platform.Architecture] || !isManifestType(entry.MediaType) {
				continue
			}
			seen[entry.Platform.Architecture] = true
			childBody, _, childDigest, err := c.getManifest(ctx, entry.Digest)
			if err != nil {
				return ImageConfigs{}, err
			}
			var child imageManifestDoc
			if json.Unmarshal(childBody, &child) != nil {
				return ImageConfigs{}, fmt.Errorf("get manifest: %w", ErrUnsupportedManifest)
			}
			platform, err := c.platformConfig(ctx, childDigest, child)
			if err != nil {
				return ImageConfigs{}, err
			}
			platform.OS, platform.Arch = entry.Platform.OS, entry.Platform.Architecture
			out.Platforms = append(out.Platforms, platform)
		}
	default:
		return ImageConfigs{}, fmt.Errorf("get manifest: %w", ErrUnsupportedManifest)
	}
	if len(out.Platforms) == 0 {
		return ImageConfigs{}, fmt.Errorf("get manifest: %w", ErrNoLinuxPlatform)
	}
	return out, nil
}

// platformConfig reads the config blob an image manifest names and the platform its config states.
func (c *Client) platformConfig(ctx context.Context, manifestDigest string, doc imageManifestDoc) (PlatformConfig, error) {
	if doc.Config == nil || !strings.HasPrefix(doc.Config.Digest, "sha256:") {
		return PlatformConfig{}, fmt.Errorf("get manifest: %w", ErrUnsupportedManifest)
	}
	if doc.Config.Size > MaxConfigBytes {
		return PlatformConfig{}, fmt.Errorf("get blob: %w", ErrBlobTooLarge)
	}
	config, err := c.GetBlob(ctx, doc.Config.Digest, MaxConfigBytes)
	if err != nil {
		return PlatformConfig{}, err
	}
	var stated struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	}
	_ = json.Unmarshal(config, &stated)
	return PlatformConfig{OS: stated.OS, Arch: stated.Architecture, ManifestDigest: manifestDigest, Config: config}, nil
}
