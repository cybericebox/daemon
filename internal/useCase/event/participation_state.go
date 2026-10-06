package event

import (
	"time"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

// ParticipationReason says why a participation action is unavailable. The
// codes are part of the API: the event site maps each one to a message, so it
// never re-derives a rule from dates on its own.
type ParticipationReason string

const (
	ReasonNone ParticipationReason = ""
	// ReasonNotSignedIn: the viewer is a guest.
	ReasonNotSignedIn ParticipationReason = "not_signed_in"
	// ReasonStaff: owners and moderators of the event run it, they do not compete.
	ReasonStaff ParticipationReason = "staff_cannot_participate"
	// ReasonNotPublished: the event is not published yet (or never scheduled).
	ReasonNotPublished ParticipationReason = "not_published"
	// ReasonRegistrationClosed: the organizers closed self-registration.
	ReasonRegistrationClosed ParticipationReason = "registration_closed"
	// ReasonClosedAtStart: the event started and does not accept late joiners.
	ReasonClosedAtStart ParticipationReason = "closed_at_start"
	ReasonFinished      ParticipationReason = "event_finished"
	ReasonWithdrawn     ParticipationReason = "event_withdrawn"
	// ReasonPending / ReasonRejected / ReasonAlreadyParticipant: the caller's own status blocks a new registration.
	ReasonPending            ParticipationReason = "pending"
	ReasonRejected           ParticipationReason = "rejected"
	ReasonAlreadyParticipant ParticipationReason = "already_participant"
	ReasonNotApproved        ParticipationReason = "not_approved"
	// ReasonRosterFrozen: teams no longer change (the event started without late join).
	ReasonRosterFrozen ParticipationReason = "roster_frozen_at_start"
	ReasonNotTeamEvent ParticipationReason = "not_team_event"
	ReasonHasTeam      ParticipationReason = "already_in_team"
	ReasonNoTeam       ParticipationReason = "no_team"
	ReasonNotCaptain   ParticipationReason = "not_captain"
	// ReasonCaptainMustTransfer: a captain hands the team over before leaving.
	ReasonCaptainMustTransfer ParticipationReason = "captain_must_transfer"
	// ReasonTeamFormed: the team is formed, its roster is closed for good (no
	// joining by link). ReasonTeamSwitchLocked: a formed team cannot be
	// disbanded, which would free its members to switch teams. ReasonFormsAtStart:
	// the event has no late join, so the start forms every team by itself.
	ReasonTeamFormed       ParticipationReason = "team_formed"
	ReasonTeamSwitchLocked ParticipationReason = "team_switch_locked"
	// ReasonTeamLeaveLocked: a member of a formed team cannot leave it on their own.
	ReasonTeamLeaveLocked ParticipationReason = "team_leave_locked"
	ReasonFormsAtStart    ParticipationReason = "forms_at_start"
	// ReasonTeamNotFormed: tasks open after the captain confirms the roster.
	// ReasonBelowMinimum: the team is smaller than the event minimum.
	ReasonTeamNotFormed ParticipationReason = "team_not_formed"
	ReasonBelowMinimum  ParticipationReason = "below_minimum"
	// ReasonNotStarted: the competition has not started, so tasks and answers are closed.
	ReasonNotStarted ParticipationReason = "not_started"
)

// Capability is one participation action: allowed, or the reason it is not.
type Capability struct {
	Allowed bool
	Reason  ParticipationReason
}

func allow() Capability                     { return Capability{Allowed: true} }
func deny(r ParticipationReason) Capability { return Capability{Reason: r} }
func gate(ok bool, r ParticipationReason) Capability {
	if ok {
		return allow()
	}
	return deny(r)
}

// ParticipationActor is who is asking. Staff means an event-local membership
// (owner, manager or viewer); the platform administrator role is not a
// participation role, so an administrator without a membership is an ordinary
// signed-in user here.
type ParticipationActor struct {
	Authenticated bool
	Staff         bool
	Status        participantModel.Status
	HasTeam       bool
	Captain       bool
	// TeamFormed is the actor's team formation (team.Formed).
	TeamFormed bool
	// TeamMembers is the actor's team size, for the minimum of the confirmation.
	TeamMembers int32
}

// ParticipationInput is everything the participation rules depend on.
type ParticipationInput struct {
	Lifecycle     eventModel.Lifecycle
	Registration  eventConfigModel.Registration
	Participation *eventConfigModel.Participation
	// MinTeamSize is the event's minimum team size (at least 1).
	MinTeamSize int32
	Actor       ParticipationActor
	Now         time.Time
}

// ParticipationState is the single computed answer to «what can this viewer do
// with this event right now». The server enforces the same rules through the
// same functions, and the event site only renders this block.
type ParticipationState struct {
	Phase eventModel.LifecycleStatus
	// RegistrationClosesAt is the shared close of registration and of the team
	// roster (nil: never closes on its own).
	RegistrationClosesAt *time.Time
	Staff                bool

	Register   Capability
	CreateTeam Capability
	JoinTeam   Capability
	LeaveTeam  Capability
	// ManageTeam covers the captain's roster actions: rename, kick, transfer
	// captaincy, disband and reissue the join link.
	ManageTeam  Capability
	EditAnswers Capability
	Submit      Capability
	SeeTasks    Capability
	// RemoveMember is the captain's kick. From a formed team it removes the person
	// from the event, never to another team.
	RemoveMember Capability
	// DisbandTeam is the captain's disband; not for a formed team.
	DisbandTeam Capability
	// FormTeam is the captain's «confirm the roster» (late-join events only).
	FormTeam Capability
	// TeamFormed says the actor's team is formed.
	TeamFormed bool
	// RosterOpen is the event-level fact behind the team capabilities.
	RosterOpen bool
	// RegistrationWindowOpen is the event-level fact behind Register.
	RegistrationWindowOpen bool
	// RegistrationReason and RosterReason say, for anyone, why registration or
	// the roster is closed (ReasonNone while open); RegistrationReason also
	// covers the organizers' «registration closed» mode.
	RegistrationReason ParticipationReason
	RosterReason       ParticipationReason
}

// registrationReason is the one place that decides whether self-registration
// is possible for someone who is not on the staff and has no row yet.
func registrationReason(l eventModel.Lifecycle, registration eventConfigModel.Registration, now time.Time) ParticipationReason {
	if !l.RegistrationOpen(now) {
		switch l.Status(now) {
		case eventModel.LifecycleFinished:
			return ReasonFinished
		case eventModel.LifecycleWithdrawn:
			return ReasonWithdrawn
		case eventModel.LifecycleStarted:
			return ReasonClosedAtStart
		default:
			return ReasonNotPublished
		}
	}
	if registration == eventConfigModel.RegistrationClose {
		return ReasonRegistrationClosed
	}
	return ReasonNone
}

// rosterReason explains a frozen roster.
func rosterReason(l eventModel.Lifecycle, now time.Time) ParticipationReason {
	if l.RosterOpen(now) {
		return ReasonNone
	}
	switch l.Status(now) {
	case eventModel.LifecycleFinished:
		return ReasonFinished
	case eventModel.LifecycleWithdrawn:
		return ReasonWithdrawn
	case eventModel.LifecycleStarted, eventModel.LifecyclePublished:
		return ReasonRosterFrozen
	default:
		return ReasonNotPublished
	}
}

// tasksAvailable is the moment tasks open for teams: from the start on, and
// they stay readable after the finish.
func tasksAvailable(l eventModel.Lifecycle, now time.Time) bool {
	s := l.Status(now)
	return s == eventModel.LifecycleStarted || s == eventModel.LifecycleFinished
}

// ComputeParticipation derives every participation capability of one viewer.
func ComputeParticipation(in ParticipationInput) ParticipationState {
	l, now, a := in.Lifecycle, in.Now, in.Actor
	state := ParticipationState{
		Phase:                  l.Status(now),
		RegistrationClosesAt:   l.JoinClosesAt(),
		Staff:                  a.Staff,
		RosterOpen:             l.RosterOpen(now),
		RegistrationWindowOpen: l.RegistrationOpen(now),
		RegistrationReason:     registrationReason(l, in.Registration, now),
		RosterReason:           rosterReason(l, now),
	}

	// Register.
	switch {
	case a.Staff:
		state.Register = deny(ReasonStaff)
	case !a.Authenticated:
		state.Register = deny(ReasonNotSignedIn)
	case a.Status == participantModel.StatusPending:
		state.Register = deny(ReasonPending)
	case a.Status == participantModel.StatusApproved:
		state.Register = deny(ReasonAlreadyParticipant)
	case a.Status == participantModel.StatusRejected:
		state.Register = deny(ReasonRejected)
	default:
		reason := registrationReason(l, in.Registration, now)
		state.Register = gate(reason == ReasonNone, reason)
	}

	approved := a.Authenticated && a.Status == participantModel.StatusApproved
	notApproved := func() ParticipationReason {
		if !a.Authenticated {
			return ReasonNotSignedIn
		}
		return ReasonNotApproved
	}

	// Team roster actions (team events only, approved participants only,
	// only while the roster is open).
	teamEvent := in.Participation != nil && *in.Participation == eventConfigModel.ParticipationTeam
	rosterBlock := func(base Capability) Capability {
		if !base.Allowed {
			return base
		}
		return gate(state.RosterOpen, rosterReason(l, now))
	}
	roster := func(base Capability) Capability {
		switch {
		case !approved:
			return deny(notApproved())
		case !teamEvent:
			return deny(ReasonNotTeamEvent)
		}
		return rosterBlock(base)
	}
	state.CreateTeam = roster(gate(!a.HasTeam, ReasonHasTeam))
	state.JoinTeam = state.CreateTeam
	state.LeaveTeam = roster(func() Capability {
		switch {
		case !a.HasTeam:
			return deny(ReasonNoTeam)
		case a.Captain:
			return deny(ReasonCaptainMustTransfer)
		case a.TeamFormed:
			// A formed team has a closed roster: a member does not leave on their own.
			return deny(ReasonTeamLeaveLocked)
		}
		return allow()
	}())
	state.ManageTeam = roster(func() Capability {
		switch {
		case !a.HasTeam:
			return deny(ReasonNoTeam)
		case !a.Captain:
			return deny(ReasonNotCaptain)
		}
		return allow()
	}())

	state.TeamFormed = a.HasTeam && a.TeamFormed
	state.RemoveMember = state.ManageTeam
	state.DisbandTeam = state.ManageTeam
	if state.DisbandTeam.Allowed && a.TeamFormed {
		state.DisbandTeam = deny(ReasonTeamSwitchLocked)
	}
	state.FormTeam = state.ManageTeam
	if state.FormTeam.Allowed {
		switch {
		case a.TeamFormed:
			state.FormTeam = deny(ReasonTeamFormed)
		case l.JoinPolicy != eventModel.JoinPolicyRolling:
			state.FormTeam = deny(ReasonFormsAtStart)
		case a.TeamMembers < max(in.MinTeamSize, 1):
			state.FormTeam = deny(ReasonBelowMinimum)
		}
	}

	// Answers are editable until the effective finish.
	state.EditAnswers = func() Capability {
		if !approved {
			return deny(notApproved())
		}
		if !participantModel.AnswersEditable(l.EffectiveFinishAt(), now) {
			return deny(ReasonFinished)
		}
		if state.Phase == eventModel.LifecycleWithdrawn {
			return deny(ReasonWithdrawn)
		}
		return allow()
	}()

	// Tasks are visible from the start on (kept visible after the finish);
	// answers are accepted only while the runtime is open.
	taskVisible := tasksAvailable(l, now)
	state.SeeTasks = func() Capability {
		switch {
		case !approved:
			return deny(notApproved())
		case !a.HasTeam:
			return deny(ReasonNoTeam)
		case state.Phase == eventModel.LifecycleWithdrawn:
			return deny(ReasonWithdrawn)
		case !taskVisible:
			return deny(ReasonNotStarted)
		case !a.TeamFormed:
			return deny(ReasonTeamNotFormed)
		}
		return allow()
	}()
	state.Submit = func() Capability {
		if !state.SeeTasks.Allowed {
			return state.SeeTasks
		}
		if !l.RuntimeOpen(now) {
			return deny(ReasonFinished)
		}
		return allow()
	}()
	return state
}
