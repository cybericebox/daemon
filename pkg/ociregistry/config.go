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
	"regexp"
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
		Variant      string `json:"variant"`
	} `json:"platform"`
}

type imageManifestDoc struct {
	MediaType string           `json:"mediaType"`
	Config    *manifestEntry   `json:"config"`
	Manifests *[]manifestEntry `json:"manifests"`
}

var (
	digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	// tagPattern is the tag grammar of the distribution spec.
	tagPattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)
)

func isIndexType(mediaType string) bool {
	return mediaType == "application/vnd.oci.image.index.v1+json" ||
		mediaType == "application/vnd.docker.distribution.manifest.list.v2+json"
}

func isManifestType(mediaType string) bool {
	return mediaType == "application/vnd.oci.image.manifest.v1+json" ||
		mediaType == "application/vnd.docker.distribution.manifest.v2+json"
}

// validReference reports whether the reference is a tag or a sha256 digest by the distribution grammar, so it is safe in a URL path.
func validReference(reference string) bool {
	return digestPattern.MatchString(reference) || tagPattern.MatchString(reference)
}

// parseManifest decides what a manifest body is from the body itself: its mediaType must be present and, when the registry
// states a Content-Type, equal to it. A document that mixes the shapes of a manifest and an index is refused.
func parseManifest(body []byte, headerType string) (doc imageManifestDoc, isIndex bool, err error) {
	if json.Unmarshal(body, &doc) != nil {
		return doc, false, fmt.Errorf("get manifest: %w", ErrUnsupportedManifest)
	}
	if doc.MediaType == "" || (headerType != "" && headerType != doc.MediaType) {
		return doc, false, fmt.Errorf("get manifest: %w: media type missing or differs from the Content-Type", ErrUnsupportedManifest)
	}
	switch {
	case isManifestType(doc.MediaType) && doc.Manifests == nil:
		return doc, false, nil
	case isIndexType(doc.MediaType) && doc.Config == nil:
		return doc, true, nil
	}
	return doc, false, fmt.Errorf("get manifest: %w", ErrUnsupportedManifest)
}

// getManifest reads one manifest (a tag or a digest), at most MaxManifestBytes. Its sha256 must equal expected when expected is
// given, and always equals the reference when that is a digest. It returns the body, the Content-Type and its digest.
func (c *Client) getManifest(ctx context.Context, reference, expected string) (body []byte, mediaType, digest string, err error) {
	if !validReference(reference) {
		return nil, "", "", fmt.Errorf("get manifest: %w: malformed reference", ErrUnsupportedManifest)
	}
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
	if expected == "" && digestPattern.MatchString(reference) {
		expected = reference
	}
	if expected != "" && expected != digest {
		return nil, "", "", fmt.Errorf("get manifest: %w: content does not match its digest", ErrUnreachable)
	}
	mediaType, _, _ = strings.Cut(resp.Header.Get("Content-Type"), ";")
	return body, strings.TrimSpace(mediaType), digest, nil
}

// wantedPlatform reports whether an index entry is a platform the laboratories run (linux/amd64, linux/arm64 with no variant or
// v8) and which architecture it is.
func wantedPlatform(e manifestEntry) (arch string, ok bool) {
	p := e.Platform
	if p == nil || p.OS != "linux" {
		return "", false
	}
	switch {
	case p.Architecture == "amd64" && p.Variant == "":
		return "amd64", true
	case p.Architecture == "arm64" && (p.Variant == "" || p.Variant == "v8"):
		return "arm64", true
	}
	return "", false
}

// ImageConfigs reads the start configuration of an image without pulling a layer: the manifest of the reference, for an
// index the manifests of linux/amd64 and linux/arm64 (the platforms the laboratories run), and the config blob of each.
// Only those few small documents are read, each within its limit and verified against its digest, and the kind of every
// document is decided from its verified body. It fails closed: an entry for a wanted platform of an unsupported kind, a
// platform listed twice or an index too long to read whole refuses the image. An image with neither platform gives
// ErrNoLinuxPlatform; a manifest of another kind ErrUnsupportedManifest.
func (c *Client) ImageConfigs(ctx context.Context, reference string) (ImageConfigs, error) {
	body, headerType, digest, err := c.getManifest(ctx, reference, "")
	if err != nil {
		return ImageConfigs{}, err
	}
	doc, isIndex, err := parseManifest(body, headerType)
	if err != nil {
		return ImageConfigs{}, err
	}
	out := ImageConfigs{Digest: digest}
	if !isIndex {
		// A single manifest has no platform of its own: the config says it.
		platform, err := c.platformConfig(ctx, digest, doc)
		if err != nil {
			return ImageConfigs{}, err
		}
		if platform.OS == "linux" && (platform.Arch == "amd64" || platform.Arch == "arm64") {
			out.Platforms = append(out.Platforms, platform)
		}
	} else {
		if len(*doc.Manifests) > maxIndexScan {
			return ImageConfigs{}, fmt.Errorf("get manifest: %w: index too long", ErrUnsupportedManifest)
		}
		seen := map[string]bool{}
		for _, entry := range *doc.Manifests {
			arch, ok := wantedPlatform(entry)
			if !ok {
				continue
			}
			if seen[arch] || !isManifestType(entry.MediaType) || !digestPattern.MatchString(entry.Digest) {
				return ImageConfigs{}, fmt.Errorf("get manifest: %w: duplicate or unsupported entry for linux/%s", ErrUnsupportedManifest, arch)
			}
			seen[arch] = true
			childBody, childType, childDigest, err := c.getManifest(ctx, entry.Digest, entry.Digest)
			if err != nil {
				return ImageConfigs{}, err
			}
			child, childIsIndex, err := parseManifest(childBody, childType)
			if err != nil {
				return ImageConfigs{}, err
			}
			if childIsIndex {
				return ImageConfigs{}, fmt.Errorf("get manifest: %w: nested index", ErrUnsupportedManifest)
			}
			platform, err := c.platformConfig(ctx, childDigest, child)
			if err != nil {
				return ImageConfigs{}, err
			}
			platform.OS, platform.Arch = "linux", arch
			out.Platforms = append(out.Platforms, platform)
		}
	}
	if len(out.Platforms) == 0 {
		return ImageConfigs{}, fmt.Errorf("get manifest: %w", ErrNoLinuxPlatform)
	}
	return out, nil
}

// platformConfig reads the config blob an image manifest names and the platform its config states.
func (c *Client) platformConfig(ctx context.Context, manifestDigest string, doc imageManifestDoc) (PlatformConfig, error) {
	if doc.Config == nil || !digestPattern.MatchString(doc.Config.Digest) {
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
