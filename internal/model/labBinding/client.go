package labBindingModel

import (
	"strings"

	"github.com/gofrs/uuid"
)

// ParticipantClientPrefix starts the name of a participant's LabGroupClient
// (VPN peer and proxy identity): "p-" + the user id as a 25-character short id,
// 27 characters in all, the same fixed-width encoding as group names.
const ParticipantClientPrefix = "p-"

// ParticipantClientName is the LabGroupClient name of a user. It is the only
// place that builds it.
func ParticipantClientName(userID uuid.UUID) string {
	return ParticipantClientPrefix + ShortID(userID)
}

// ParseParticipantClientName is the inverse of ParticipantClientName. The
// boolean is false for any other client (the shared "tester", staff clients).
func ParseParticipantClientName(name string) (uuid.UUID, bool) {
	rest, ok := strings.CutPrefix(name, ParticipantClientPrefix)
	if !ok {
		return uuid.Nil, false
	}
	return ParseShortID(rest)
}
