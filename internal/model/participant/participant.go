// Package participantModel is the DOMAIN layer of event participation: a
// per-(event,user) record with an approval status. A row exists once a user
// has attempted to join; a user who never joined reports StatusNone (no row).
package participantModel

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofrs/uuid"

	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	"github.com/cybericebox/daemon/pkg/tools"
)

type Status int32

const (
	StatusNone Status = iota // never persisted; reported when no row exists
	StatusPending
	StatusApproved
	StatusRejected
)

type TeamRole int16

const (
	TeamRoleCaptain TeamRole = iota
	TeamRoleMember
)

type Participant struct {
	EventID       uuid.UUID
	UserID        uuid.UUID
	Status        Status
	TeamID        *uuid.UUID
	TeamRole      *TeamRole
	CreatedAt     time.Time
	DecidedAt     *time.Time
	DecidedBy     uuid.NullUUID
	InvitedBy     uuid.NullUUID
	Invited       bool
	InvitedTeamID *uuid.UUID
	InvitedToTeam bool
	// Pseudonym is the optional per-event public name; nil means the profile
	// name is shown. InvitationSentAt is nil until an invitation email was
	// queued successfully.
	Pseudonym        *string
	InvitationSentAt *time.Time
}

const (
	pseudonymMinLen = 2
	pseudonymMaxLen = 32
)

// NormalizePseudonym trims the requested value; empty clears the pseudonym.
func NormalizePseudonym(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return nil, nil
	}
	length := utf8.RuneCountInString(value)
	if length < pseudonymMinLen || length > pseudonymMaxLen {
		return nil, ErrPseudonymInvalid.Err()
	}
	for _, r := range value {
		if tools.UnsafeDisplayRune(r) {
			return nil, ErrPseudonymInvalid.Err()
		}
	}
	return &value, nil
}

// SetPseudonym applies the organizer switch and the before-start rule. The
// per-event uniqueness is enforced by the repository's unique index.
func (p *Participant) SetPseudonym(raw *string, allowed, started bool) error {
	value, err := NormalizePseudonym(raw)
	if err != nil {
		return err
	}
	if value != nil && !allowed {
		return ErrPseudonymsDisabled.Err()
	}
	if started {
		return ErrPseudonymLocked.Err()
	}
	p.Pseudonym = value
	return nil
}

// AssignTeam records the participant's current competing unit. Only approved
// participants can enter a team; the use case is responsible for checking
// that the requested team belongs to the same event and has capacity.
func (p *Participant) AssignTeam(teamID uuid.UUID, role TeamRole) error {
	if p.Status != StatusApproved {
		return ErrParticipantNotApproved.Err()
	}
	if teamID == uuid.Nil || (role != TeamRoleCaptain && role != TeamRoleMember) {
		return ErrParticipantTeamInvalid.Err()
	}
	p.TeamID = &teamID
	p.TeamRole = &role
	return nil
}

// RemoveFromEvent revokes an approved participant's place in the event (they
// left, or a captain or moderator removed them from a formed team). The row
// stays as rejected, so registration cannot be repeated and only staff can
// approve the person again.
func (p *Participant) RemoveFromEvent(now time.Time) {
	p.Status = StatusRejected
	p.DecidedAt = &now
	p.LeaveTeam()
}

func (p *Participant) LeaveTeam() {
	p.TeamID = nil
	p.TeamRole = nil
}

// NewParticipant builds the participation row applying the event's registration
// policy: Open approves immediately, Approval starts pending, Close rejects the
// join (no row created). The after-start gate is enforced by the use case,
// which holds the Event window.
func NewParticipant(eventID, userID uuid.UUID, reg eventConfigModel.Registration, now time.Time) (Participant, error) {
	p := Participant{EventID: eventID, UserID: userID, CreatedAt: now}
	switch reg {
	case eventConfigModel.RegistrationOpen:
		p.Status = StatusApproved
		p.DecidedAt = &now
	case eventConfigModel.RegistrationApproval:
		p.Status = StatusPending
	default: // RegistrationClose (or any unknown) — no self-service join
		return Participant{}, ErrRegistrationClosed.Err()
	}
	return p, nil
}

// Approve transitions a pending request to approved.
func (p *Participant) Approve(now time.Time, by uuid.UUID) error {
	return p.decide(StatusApproved, now, by)
}

// Reject transitions a pending request to rejected.
func (p *Participant) Reject(now time.Time, by uuid.UUID) error {
	return p.decide(StatusRejected, now, by)
}

func (p *Participant) decide(to Status, now time.Time, by uuid.UUID) error {
	if p.Status != StatusPending {
		return ErrParticipantNotPending.Err()
	}
	p.Status = to
	p.DecidedAt = &now
	p.DecidedBy = uuid.NullUUID{UUID: by, Valid: by != uuid.Nil}
	return nil
}
