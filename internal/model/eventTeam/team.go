// Package eventTeamModel owns the universal competing unit of an event. An
// individual participant is represented by a team with exactly one member,
// so downstream scoring, challenges, labs and VPN never branch by mode.
package eventTeamModel

import (
	"strings"
	"time"

	"github.com/gofrs/uuid"
)

const (
	nameMinLen = 3
	nameMaxLen = 64
	codeMinLen = 12
)

type EventTeam struct {
	ID       uuid.UUID
	EventID  uuid.UUID
	Name     string
	JoinCode string
	// JoinCodeExpiresAt limits the join link; nil keeps it valid until the
	// captain regenerates it.
	JoinCodeExpiresAt *time.Time
	CaptainID         uuid.UUID
	Hidden            bool
	MemberCount       int32
	// Individual marks the hidden single-member team of individual mode; it
	// is presented by its participant's public name, never by Name.
	Individual       bool
	AdmittedManually bool
	// AdmissionLocked keeps a team admitted once it competed with enough
	// members, so a moderator's later roster change never revokes access.
	AdmissionLocked bool
	// Moderators marks the hidden per-event team of the event managers. It
	// exists only for stands (same mechanism and timing as participants) and
	// never appears in participant, scoring or team-management reads.
	Moderators bool
	// FormedAt is the moment the roster was closed for good (captain's
	// confirmation or a moderator). Individual and moderators teams are formed
	// when created. Events without late join also form every team at their
	// start; that is not stored, see Formed.
	FormedAt *time.Time
	// FormedBy is the person who formed the team; nil when nobody did.
	FormedBy  *uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Formed says whether the roster is closed for good. formsAtStart is the event's
// fact that its teams form by themselves now (Lifecycle.FormsTeamsAtStart). A
// formed team is the only one that gets tasks, labs and VPN, and its members can
// no longer switch to another team.
func (t EventTeam) Formed(formsAtStart bool) bool { return t.FormedAt != nil || formsAtStart }

// Form closes the roster. Whoever forms as the captain needs the event's minimum
// team size; a moderator's force skips it (the stuck case). Forming twice is an
// error so a repeat is visible.
func (t *EventTeam) Form(by uuid.UUID, force bool, minTeamSize int32, formsAtStart bool, now time.Time) error {
	if t.Formed(formsAtStart) {
		return ErrEventTeamFormed.Err()
	}
	if !force {
		if !t.IsCaptain(by) {
			return ErrEventTeamCaptainRequired.Err()
		}
		if t.MemberCount < minTeamSize {
			return ErrEventTeamBelowMinimum.Err()
		}
	}
	t.FormedAt = &now
	t.FormedBy = &by
	t.UpdatedAt = now
	return nil
}

// New creates a team with its creator as captain and first member. Join-code
// generation is deliberately outside the domain so the application layer can
// use a cryptographically secure generator and handle database collisions.
func New(eventID, captainID uuid.UUID, name, joinCode string, now time.Time) (EventTeam, error) {
	if eventID == uuid.Nil || captainID == uuid.Nil {
		return EventTeam{}, ErrEventTeamIdentityInvalid.Err()
	}
	name, err := validateName(name)
	if err != nil {
		return EventTeam{}, err
	}
	if !validJoinCode(joinCode) {
		return EventTeam{}, ErrEventTeamJoinCodeInvalid.Err()
	}
	return EventTeam{
		ID: uuid.Must(uuid.NewV7()), EventID: eventID, Name: name, JoinCode: joinCode,
		CaptainID: captainID, MemberCount: 1, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// NewManaged creates a moderator-built team with an empty roster: the captain
// and members may still be pending invitees, so each one is counted only when
// the use case adds them (immediately for approved participants, on accept for
// invitees). An empty roster is a valid, not yet admitted team.
func NewManaged(eventID, captainID uuid.UUID, name, joinCode string, now time.Time) (EventTeam, error) {
	team, err := New(eventID, captainID, name, joinCode, now)
	if err != nil {
		return EventTeam{}, err
	}
	team.MemberCount = 0
	return team, nil
}

// ValidateName applies the team name rule without building a team, so a batch
// can report every invalid name before it writes anything.
func ValidateName(name string) (string, error) { return validateName(name) }

func (t *EventTeam) Rename(name string, by uuid.UUID, now time.Time) error {
	if !t.IsCaptain(by) {
		return ErrEventTeamCaptainRequired.Err()
	}
	name, err := validateName(name)
	if err != nil {
		return err
	}
	t.Name = name
	t.UpdatedAt = now
	return nil
}

// UpdateByModerator changes presentation fields after the management boundary
// has authorized the caller for this event. It deliberately does not alter
// captain, membership or the join code: each has its own invariant-preserving
// operation in the application layer.
func (t *EventTeam) UpdateByModerator(name string, hidden bool, now time.Time) error {
	name, err := validateName(name)
	if err != nil {
		return err
	}
	t.Name = name
	t.Hidden = hidden
	t.UpdatedAt = now
	return nil
}

// NewIndividual creates the hidden competing unit of an individual
// participant. It is always admitted and never shown under its stored name.
func NewIndividual(eventID, userID uuid.UUID, name, joinCode string, now time.Time) (EventTeam, error) {
	team, err := New(eventID, userID, name, joinCode, now)
	if err != nil {
		return EventTeam{}, err
	}
	team.Individual = true
	team.FormedAt = &now
	return team, nil
}

// SetAdmittedManually records the moderator's manual admission decision.
func (t *EventTeam) SetAdmittedManually(admitted bool, now time.Time) {
	t.AdmittedManually = admitted
	t.UpdatedAt = now
}

// Admitted applies the admission rule for a known effective minimum size.
func (t EventTeam) Admitted(minTeamSize int32) bool {
	return t.Individual || t.AdmittedManually || t.AdmissionLocked || t.MemberCount >= minTeamSize
}

// LockAdmissionBeforeShrink keeps an admitted team admitted when a member is
// removed after the start (only moderators can change a started roster).
func (t *EventTeam) LockAdmissionBeforeShrink(minTeamSize int32, started bool, now time.Time) bool {
	if !started || t.AdmissionLocked || !t.Admitted(minTeamSize) || t.Individual || t.AdmittedManually {
		return false
	}
	t.AdmissionLocked = true
	t.UpdatedAt = now
	return true
}

// JoinCodeExpiry is how long a freshly issued join link stays valid.
type JoinCodeExpiry string

const (
	JoinCodeExpiryNone  JoinCodeExpiry = "none"
	JoinCodeExpiryDay   JoinCodeExpiry = "day"
	JoinCodeExpiryWeek  JoinCodeExpiry = "week"
	JoinCodeExpiryStart JoinCodeExpiry = "start"
)

// ExpiresAt resolves the expiry policy to a moment. "start" ends the link at
// the event start, so it needs a start that is still ahead.
func (e JoinCodeExpiry) ExpiresAt(now, startAt time.Time) (*time.Time, error) {
	var at time.Time
	switch e {
	case "", JoinCodeExpiryNone:
		return nil, nil
	case JoinCodeExpiryDay:
		at = now.Add(24 * time.Hour)
	case JoinCodeExpiryWeek:
		at = now.Add(7 * 24 * time.Hour)
	case JoinCodeExpiryStart:
		if !startAt.After(now) {
			return nil, ErrEventTeamJoinCodeExpiryInvalid.Err()
		}
		at = startAt
	default:
		return nil, ErrEventTeamJoinCodeExpiryInvalid.Err()
	}
	return &at, nil
}

// RegenerateJoinCode replaces the join link, which invalidates the previous
// one. expiresAt nil issues a link without an expiry.
func (t *EventTeam) RegenerateJoinCode(code string, expiresAt *time.Time, by uuid.UUID, now time.Time) error {
	if !t.IsCaptain(by) {
		return ErrEventTeamCaptainRequired.Err()
	}
	if !validJoinCode(code) {
		return ErrEventTeamJoinCodeInvalid.Err()
	}
	t.JoinCode = code
	t.JoinCodeExpiresAt = expiresAt
	t.UpdatedAt = now
	return nil
}

// CheckJoinCodeActive rejects a join link past its expiry.
func (t EventTeam) CheckJoinCodeActive(now time.Time) error {
	if t.JoinCodeExpiresAt != nil && !now.Before(*t.JoinCodeExpiresAt) {
		return ErrEventTeamJoinCodeExpired.Err()
	}
	return nil
}

func (t *EventTeam) TransferCaptain(newCaptainID, by uuid.UUID, now time.Time) error {
	if !t.IsCaptain(by) {
		return ErrEventTeamCaptainRequired.Err()
	}
	if newCaptainID == uuid.Nil || newCaptainID == t.CaptainID {
		return ErrEventTeamCaptainInvalid.Err()
	}
	t.CaptainID = newCaptainID
	t.UpdatedAt = now
	return nil
}

func (t EventTeam) IsCaptain(userID uuid.UUID) bool {
	return userID != uuid.Nil && t.CaptainID == userID
}

// CanAcceptMember checks the event-configured maximum before a membership is
// written. A zero maximum means unbounded; min-size is a lifecycle rule and is
// checked when a team starts competing, not when a member joins.
func (t EventTeam) CanAcceptMember(maxTeamSize int32) error {
	if maxTeamSize > 0 && t.MemberCount >= maxTeamSize {
		return ErrEventTeamFull.Err()
	}
	return nil
}

func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if len(name) < nameMinLen || len(name) > nameMaxLen {
		return "", ErrEventTeamNameInvalid.Err()
	}
	return name, nil
}

func validJoinCode(code string) bool {
	return len(strings.TrimSpace(code)) >= codeMinLen
}
