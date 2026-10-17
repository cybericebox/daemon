package imageRegistryModel

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid"
)

// Kind says where an image comes from, which decides the credentials used.
type Kind int

const (
	KindPlatform  Kind = iota // platform:<name>@sha256:<hex>, platform registry
	KindPublic                // pulled anonymously
	KindOrganizer             // pulled with an event secret
)

// EventSecret is an organizer-supplied registry credential, usable only
// inside its event. The token is stored sealed.
type EventSecret struct {
	ID, EventID                                       uuid.UUID
	Host, RepositoryPrefix, Username, TokenCiphertext string
	CreatedAt, UpdatedAt                              time.Time
}

var (
	hostRe   = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*$`)
	prefixRe = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
)

func NewEventSecret(eventID, host, prefix, username string, now time.Time) (EventSecret, error) {
	eid, err := uuid.FromString(eventID)
	if err != nil {
		return EventSecret{}, ErrInvalidEventSecret.Err()
	}
	h, ok := parseSecretHost(host)
	prefix = strings.ToLower(strings.Trim(strings.TrimSpace(prefix), "/"))
	username = strings.TrimSpace(username)
	if !ok || username == "" || (prefix != "" && !prefixRe.MatchString(prefix)) {
		return EventSecret{}, ErrInvalidEventSecret.Err()
	}
	id, err := uuid.NewV7()
	if err != nil {
		return EventSecret{}, err
	}
	return EventSecret{
		ID: id, EventID: eid, Host: h, RepositoryPrefix: prefix, Username: username,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

// parseSecretHost accepts host[:port] only: no scheme, no path.
func parseSecretHost(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	name, port, hasPort := strings.Cut(s, ":")
	if !hostRe.MatchString(name) {
		return "", false
	}
	if hasPort {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return "", false
		}
	}
	return normalizeHost(name) + map[bool]string{true: ":" + port}[hasPort], true
}

// Matches reports whether the secret applies to ref. A host-only secret has
// specificity 1; a prefix (matched at a "/" boundary) adds its length, so the
// longest prefix wins.
func (s EventSecret) Matches(ref ImageRef) (int, bool) {
	if s.Host != ref.Host {
		return 0, false
	}
	if s.RepositoryPrefix == "" {
		return 1, true
	}
	if ref.Repository == s.RepositoryPrefix || strings.HasPrefix(ref.Repository, s.RepositoryPrefix+"/") {
		return 1 + len(s.RepositoryPrefix), true
	}
	return 0, false
}

// Classify decides which credentials an image needs: the platform registry,
// none (public) or the best-matching organizer secret.
func Classify(image string, secrets []EventSecret) (Kind, *EventSecret) {
	if _, ok := ParsePlatformRef(image); ok {
		return KindPlatform, nil
	}
	ref, err := ParseImageRef(image)
	if err != nil {
		return KindPublic, nil
	}
	var best *EventSecret
	bestSpec := 0
	for i := range secrets {
		if spec, ok := secrets[i].Matches(ref); ok && spec > bestSpec {
			best, bestSpec = &secrets[i], spec
		}
	}
	if best == nil {
		return KindPublic, nil
	}
	cp := *best
	return KindOrganizer, &cp
}
