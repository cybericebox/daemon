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
	KindOrganizer             // may be pulled with an event secret (see Classify)
)

// EventSecret is an organizer-supplied registry credential, usable only
// inside its event. The token is stored sealed.
type EventSecret struct {
	ID, EventID                                       uuid.UUID
	Host, RepositoryPrefix, Username, TokenCiphertext string
	CreatedAt, UpdatedAt                              time.Time
	// Check is the last result of the periodic credential check; Edit and SetToken leave it alone.
	Check Check
}

var (
	hostRe   = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*$`)
	prefixRe = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
)

// validateSecretFields normalizes the editable fields of an event secret. A
// username with ":" is refused: the laboratory agent rejects such a credential.
func validateSecretFields(host, prefix, username string) (h, pfx, user string, err error) {
	h, ok := parseSecretHost(host)
	pfx = strings.ToLower(strings.Trim(strings.TrimSpace(prefix), "/"))
	user = strings.TrimSpace(username)
	if !ok || user == "" || strings.Contains(user, ":") || (pfx != "" && !prefixRe.MatchString(pfx)) {
		return "", "", "", ErrInvalidEventSecret.Err()
	}
	return h, pfx, user, nil
}

func NewEventSecret(eventID, host, prefix, username string, now time.Time) (EventSecret, error) {
	eid, err := uuid.FromString(eventID)
	if err != nil {
		return EventSecret{}, ErrInvalidEventSecretEvent.Err()
	}
	h, pfx, user, err := validateSecretFields(host, prefix, username)
	if err != nil {
		return EventSecret{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return EventSecret{}, err
	}
	return EventSecret{
		ID: id, EventID: eid, Host: h, RepositoryPrefix: pfx, Username: user,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

// Edit replaces host, repository prefix and username (validated like on
// creation); a refused edit changes nothing.
func (s *EventSecret) Edit(host, prefix, username string, now time.Time) error {
	h, pfx, user, err := validateSecretFields(host, prefix, username)
	if err != nil {
		return err
	}
	s.Host, s.RepositoryPrefix, s.Username, s.UpdatedAt = h, pfx, user, now
	return nil
}

// SetToken stores the sealed token.
func (s *EventSecret) SetToken(ciphertext string, now time.Time) {
	s.TokenCiphertext, s.UpdatedAt = ciphertext, now
}

// parseSecretHost accepts host[:port] only: no scheme, no path.
func parseSecretHost(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	name, port, hasPort := strings.Cut(s, ":")
	if !hostRe.MatchString(name) {
		return "", false
	}
	name = normalizeHost(name)
	if !hasPort {
		return name, true
	}
	if port == "" || strings.Trim(port, "0123456789") != "" {
		return "", false
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", false
	}
	return name + ":" + strconv.Itoa(n), true
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

// Classify decides which credentials an image may need: the platform registry,
// none (public) or the best-matching organizer secret. An organizer match is a
// candidate only: the check tries the image anonymously first and uses the
// secret only when the anonymous pull fails, so public images on a secret's
// host stay public.
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
