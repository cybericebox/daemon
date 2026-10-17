package imageRegistryModel

import (
	"errors"
	"regexp"
	"strings"
)

const platformPrefix = "platform:"

var (
	platformNameRe = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	digestRe       = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	invalidRunRe   = regexp.MustCompile(`[^a-z0-9._-]+`)
	sepRunRe       = regexp.MustCompile(`[._-]{2,}`)
)

const maxNameLen = 64

// PlatformRef is a reference to an image held in the platform registry:
// platform:<name>@sha256:<hex>. Digest holds the full "sha256:<hex>".
type PlatformRef struct{ Name, Digest string }

func NewPlatformRef(name, digest string) (PlatformRef, error) {
	if len(name) > maxNameLen || !platformNameRe.MatchString(name) || !digestRe.MatchString(digest) {
		return PlatformRef{}, ErrInvalidPlatformRef.Err()
	}
	return PlatformRef{Name: name, Digest: digest}, nil
}

func ParsePlatformRef(s string) (PlatformRef, bool) {
	rest, ok := strings.CutPrefix(s, platformPrefix)
	if !ok {
		return PlatformRef{}, false
	}
	name, digest, ok := strings.Cut(rest, "@")
	if !ok {
		return PlatformRef{}, false
	}
	ref, err := NewPlatformRef(name, digest)
	return ref, err == nil
}

func (r PlatformRef) String() string { return platformPrefix + r.Name + "@" + r.Digest }

// Tag is the registry tag pushed for the reference: <name>-<first 12 hex>.
func (r PlatformRef) Tag() string {
	if !digestRe.MatchString(r.Digest) || !platformNameRe.MatchString(r.Name) {
		return ""
	}
	return r.Name + "-" + strings.TrimPrefix(r.Digest, "sha256:")[:12]
}

// SanitizeName turns arbitrary text into a valid platform image name.
func SanitizeName(s string) string {
	s = invalidRunRe.ReplaceAllString(strings.ToLower(s), "-")
	s = sepRunRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "._-")
	if len(s) > maxNameLen {
		s = strings.Trim(s[:maxNameLen], "._-")
	}
	if s == "" {
		return "image"
	}
	return s
}

// ErrInvalidImageReference is returned by ParseImageRef; callers map it to
// ReasonInvalidReference.
var ErrInvalidImageReference = errors.New("invalid image reference")

const dockerHubHost = "docker.io"

// ImageRef is a normalized container image reference.
type ImageRef struct{ Host, Repository, Tag, Digest string }

// normalizeHost lowercases a registry host and maps Docker Hub aliases to docker.io.
func normalizeHost(h string) string {
	h = strings.ToLower(h)
	switch h {
	case "docker.io", "index.docker.io", "registry-1.docker.io":
		return dockerHubHost
	}
	return h
}

func ParseImageRef(s string) (ImageRef, error) {
	if s == "" || strings.ContainsAny(s, " \t\r\n") || strings.Contains(s, "://") {
		return ImageRef{}, ErrInvalidImageReference
	}
	var ref ImageRef
	name := s
	if n, digest, ok := strings.Cut(s, "@"); ok {
		if !digestRe.MatchString(digest) {
			return ImageRef{}, ErrInvalidImageReference
		}
		name, ref.Digest = n, digest
	}
	host := dockerHubHost
	if first, rest, ok := strings.Cut(name, "/"); ok &&
		(strings.ContainsAny(first, ".:") || first == "localhost") {
		host, name = normalizeHost(first), rest
	}
	if i := strings.LastIndex(name, ":"); i >= 0 && !strings.Contains(name[i:], "/") {
		ref.Tag = name[i+1:]
		name = name[:i]
		if ref.Tag == "" {
			return ImageRef{}, ErrInvalidImageReference
		}
	}
	if name == "" {
		return ImageRef{}, ErrInvalidImageReference
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" {
			return ImageRef{}, ErrInvalidImageReference
		}
	}
	if host == dockerHubHost && !strings.Contains(name, "/") {
		name = "library/" + name
	}
	if ref.Tag == "" && ref.Digest == "" {
		ref.Tag = "latest"
	}
	ref.Host, ref.Repository = host, strings.ToLower(name)
	return ref, nil
}
