package event_test

import (
	"testing"
	"time"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

var matrixNow = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func matrixLifecycle(t *testing.T, policy eventModel.JoinPolicy, publish, start time.Duration, finish, withdraw *time.Duration) eventModel.Lifecycle {
	t.Helper()
	at := func(d time.Duration) time.Time { return matrixNow.Add(d) }
	var finishAt, withdrawAt *time.Time
	if finish != nil {
		f, w := at(*finish), at(*withdraw)
		finishAt, withdrawAt = &f, &w
	}
	l, err := eventModel.NewLifecycle(policy, at(publish), at(start), finishAt, withdrawAt, nil)
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	return l
}

func dur(d time.Duration) *time.Duration { return &d }

const hr = time.Hour

// The capability columns of the expectation strings, in this order:
// Register, CreateTeam, JoinTeam, LeaveTeam, ManageTeam, EditAnswers, SeeTasks, Submit.
func capabilityString(s event.ParticipationState) string {
	out := make([]byte, 0, 8)
	for _, c := range []event.Capability{s.Register, s.CreateTeam, s.JoinTeam, s.LeaveTeam, s.ManageTeam, s.EditAnswers, s.SeeTasks, s.Submit} {
		if c.Allowed {
			out = append(out, 'Y')
		} else {
			out = append(out, 'n')
		}
	}
	return string(out)
}

// TestParticipationMatrix pins what every kind of viewer may do in every
// phase of an open-registration team event. It is the executable form of
// docs/PARTICIPATION-RULES.md.
func TestParticipationMatrix(t *testing.T) {
	phases := []struct {
		name string
		l    eventModel.Lifecycle
		// want per actor, see capabilityString for the columns.
		want map[string]string
	}{
		{"not scheduled", eventModel.Lifecycle{}, map[string]string{
			"guest": "nnnnnnnn", "signed-in": "nnnnnnnn", "pending": "nnnnnnnn", "rejected": "nnnnnnnn",
			"approved-no-team": "nnnnnYnn", "member": "nnnnnYnn", "captain": "nnnnnYnn", "staff": "nnnnnnnn",
		}},
		{"scheduled, not yet published", matrixLifecycle(t, eventModel.JoinPolicyLockedAtStart, hr, 2*hr, dur(5*hr), dur(6*hr)), map[string]string{
			"guest": "nnnnnnnn", "signed-in": "nnnnnnnn", "pending": "nnnnnnnn", "rejected": "nnnnnnnn",
			"approved-no-team": "nYYnnYnn", "member": "nnnYnYnn", "captain": "nnnnYYnn", "staff": "nnnnnnnn",
		}},
		{"published, before start", matrixLifecycle(t, eventModel.JoinPolicyLockedAtStart, -hr, hr, dur(5*hr), dur(6*hr)), map[string]string{
			"guest": "nnnnnnnn", "signed-in": "Ynnnnnnn", "pending": "nnnnnnnn", "rejected": "nnnnnnnn",
			"approved-no-team": "nYYnnYnn", "member": "nnnYnYnn", "captain": "nnnnYYnn", "staff": "nnnnnnnn",
		}},
		{"started, late join off", matrixLifecycle(t, eventModel.JoinPolicyLockedAtStart, -3*hr, -hr, dur(5*hr), dur(6*hr)), map[string]string{
			"guest": "nnnnnnnn", "signed-in": "nnnnnnnn", "pending": "nnnnnnnn", "rejected": "nnnnnnnn",
			"approved-no-team": "nnnnnYnn", "member": "nnnnnYYY", "captain": "nnnnnYYY", "staff": "nnnnnnnn",
		}},
		{"started, late join on", matrixLifecycle(t, eventModel.JoinPolicyRolling, -3*hr, -hr, dur(5*hr), dur(6*hr)), map[string]string{
			"guest": "nnnnnnnn", "signed-in": "Ynnnnnnn", "pending": "nnnnnnnn", "rejected": "nnnnnnnn",
			"approved-no-team": "nYYnnYnn", "member": "nnnYnYYY", "captain": "nnnnYYYY", "staff": "nnnnnnnn",
		}},
		{"finished", matrixLifecycle(t, eventModel.JoinPolicyRolling, -5*hr, -4*hr, dur(-hr), dur(hr)), map[string]string{
			"guest": "nnnnnnnn", "signed-in": "nnnnnnnn", "pending": "nnnnnnnn", "rejected": "nnnnnnnn",
			"approved-no-team": "nnnnnnnn", "member": "nnnnnnYn", "captain": "nnnnnnYn", "staff": "nnnnnnnn",
		}},
		{"withdrawn or archived", matrixLifecycle(t, eventModel.JoinPolicyRolling, -5*hr, -4*hr, dur(-2*hr), dur(-hr)), map[string]string{
			"guest": "nnnnnnnn", "signed-in": "nnnnnnnn", "pending": "nnnnnnnn", "rejected": "nnnnnnnn",
			"approved-no-team": "nnnnnnnn", "member": "nnnnnnnn", "captain": "nnnnnnnn", "staff": "nnnnnnnn",
		}},
	}
	actors := map[string]event.ParticipationActor{
		"guest":            {},
		"signed-in":        {Authenticated: true},
		"pending":          {Authenticated: true, Status: participantModel.StatusPending},
		"rejected":         {Authenticated: true, Status: participantModel.StatusRejected},
		"approved-no-team": {Authenticated: true, Status: participantModel.StatusApproved},
		"member":           {Authenticated: true, Status: participantModel.StatusApproved, HasTeam: true, TeamFormed: true},
		"captain":          {Authenticated: true, Status: participantModel.StatusApproved, HasTeam: true, Captain: true, TeamFormed: true},
		"staff":            {Authenticated: true, Staff: true},
	}
	team := eventConfigModel.ParticipationTeam
	for _, phase := range phases {
		for name, actor := range actors {
			t.Run(phase.name+"/"+name, func(t *testing.T) {
				state := event.ComputeParticipation(event.ParticipationInput{
					Lifecycle: phase.l, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: actor, Now: matrixNow,
				})
				if got := capabilityString(state); got != phase.want[name] {
					t.Fatalf("Register/CreateTeam/JoinTeam/LeaveTeam/ManageTeam/EditAnswers/SeeTasks/Submit = %s, want %s\n%+v", got, phase.want[name], state)
				}
			})
		}
	}
}

// Registration and the roster close together: one shared join period, so the
// two can never disagree about the moment it ends (only publication differs).
func TestParticipationRegistrationAndRosterShareTheClose(t *testing.T) {
	manual := matrixNow.Add(2 * hr)
	rollingManual, err := eventModel.NewLifecycle(eventModel.JoinPolicyRolling, matrixNow.Add(-5*hr), matrixNow.Add(-4*hr), nil, nil, &manual)
	if err != nil {
		t.Fatal(err)
	}
	lifecycles := map[string]eventModel.Lifecycle{
		"locked":         matrixLifecycle(t, eventModel.JoinPolicyLockedAtStart, -hr, hr, dur(5*hr), dur(6*hr)),
		"rolling":        matrixLifecycle(t, eventModel.JoinPolicyRolling, -hr, hr, dur(5*hr), dur(6*hr)),
		"rolling manual": rollingManual,
		"permanent":      matrixLifecycle(t, eventModel.JoinPolicyRolling, -hr, hr, nil, nil),
	}
	for name, l := range lifecycles {
		closes := l.JoinClosesAt()
		for offset := -8 * hr; offset <= 8*hr; offset += 30 * time.Minute {
			now := matrixNow.Add(offset)
			registration, roster := l.RegistrationOpen(now), l.RosterOpen(now)
			published := l.Status(now) != eventModel.LifecycleNotPublished
			if roster != l.JoinPeriodOpen(now) || registration != (l.JoinPeriodOpen(now) && published) {
				t.Fatalf("%s at %s: registration=%v roster=%v period=%v", name, offset, registration, roster, l.JoinPeriodOpen(now))
			}
			if closes != nil && !now.Before(*closes) && (registration || roster) {
				t.Fatalf("%s at %s: still open after the join close %s", name, offset, closes)
			}
		}
	}
	if got := lifecycles["locked"].JoinClosesAt(); !got.Equal(matrixNow.Add(hr)) {
		t.Fatalf("late join off closes at the start, got %s", got)
	}
	if got := lifecycles["rolling"].JoinClosesAt(); !got.Equal(matrixNow.Add(5 * hr)) {
		t.Fatalf("late join on closes at the finish, got %s", got)
	}
	if got := lifecycles["rolling manual"].JoinClosesAt(); !got.Equal(manual) {
		t.Fatalf("a manual finish closes joining, got %s", got)
	}
	if lifecycles["permanent"].JoinClosesAt() != nil {
		t.Fatal("an event without a finish never closes joining on its own")
	}
}

func TestParticipationReasons(t *testing.T) {
	team, individual := eventConfigModel.ParticipationTeam, eventConfigModel.ParticipationIndividual
	published := matrixLifecycle(t, eventModel.JoinPolicyLockedAtStart, -hr, hr, dur(5*hr), dur(6*hr))
	startedLocked := matrixLifecycle(t, eventModel.JoinPolicyLockedAtStart, -3*hr, -hr, dur(5*hr), dur(6*hr))
	notPublished := matrixLifecycle(t, eventModel.JoinPolicyLockedAtStart, hr, 2*hr, dur(5*hr), dur(6*hr))
	finished := matrixLifecycle(t, eventModel.JoinPolicyRolling, -5*hr, -4*hr, dur(-hr), dur(hr))
	startedRolling := matrixLifecycle(t, eventModel.JoinPolicyRolling, -3*hr, -hr, dur(5*hr), dur(6*hr))
	approved := event.ParticipationActor{Authenticated: true, Status: participantModel.StatusApproved}
	captainOf := event.ParticipationActor{Authenticated: true, Status: participantModel.StatusApproved, HasTeam: true, Captain: true}
	signedIn := event.ParticipationActor{Authenticated: true}

	for _, tc := range []struct {
		name  string
		in    event.ParticipationInput
		pick  func(event.ParticipationState) event.Capability
		allow bool
		want  event.ParticipationReason
	}{
		{"staff is told why", event.ParticipationInput{Lifecycle: published, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: event.ParticipationActor{Authenticated: true, Staff: true}},
			func(s event.ParticipationState) event.Capability { return s.Register }, false, event.ReasonStaff},
		{"guest signs in first", event.ParticipationInput{Lifecycle: published, Registration: eventConfigModel.RegistrationOpen, Participation: &team},
			func(s event.ParticipationState) event.Capability { return s.Register }, false, event.ReasonNotSignedIn},
		{"before publication", event.ParticipationInput{Lifecycle: notPublished, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: signedIn},
			func(s event.ParticipationState) event.Capability { return s.Register }, false, event.ReasonNotPublished},
		{"registration mode closed", event.ParticipationInput{Lifecycle: published, Registration: eventConfigModel.RegistrationClose, Participation: &team, Actor: signedIn},
			func(s event.ParticipationState) event.Capability { return s.Register }, false, event.ReasonRegistrationClosed},
		{"approval mode is open to apply", event.ParticipationInput{Lifecycle: published, Registration: eventConfigModel.RegistrationApproval, Participation: &team, Actor: signedIn},
			func(s event.ParticipationState) event.Capability { return s.Register }, true, event.ReasonNone},
		{"late join off after start", event.ParticipationInput{Lifecycle: startedLocked, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: signedIn},
			func(s event.ParticipationState) event.Capability { return s.Register }, false, event.ReasonClosedAtStart},
		{"after finish", event.ParticipationInput{Lifecycle: finished, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: signedIn},
			func(s event.ParticipationState) event.Capability { return s.Register }, false, event.ReasonFinished},
		{"already in", event.ParticipationInput{Lifecycle: published, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: approved},
			func(s event.ParticipationState) event.Capability { return s.Register }, false, event.ReasonAlreadyParticipant},
		{"roster frozen at start", event.ParticipationInput{Lifecycle: startedLocked, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: approved},
			func(s event.ParticipationState) event.Capability { return s.CreateTeam }, false, event.ReasonRosterFrozen},
		{"roster frozen at finish", event.ParticipationInput{Lifecycle: finished, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: captainOf},
			func(s event.ParticipationState) event.Capability { return s.ManageTeam }, false, event.ReasonFinished},
		{"individual has no teams", event.ParticipationInput{Lifecycle: published, Registration: eventConfigModel.RegistrationOpen, Participation: &individual, Actor: approved},
			func(s event.ParticipationState) event.Capability { return s.CreateTeam }, false, event.ReasonNotTeamEvent},
		{"a captain hands over before leaving", event.ParticipationInput{Lifecycle: published, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: captainOf},
			func(s event.ParticipationState) event.Capability { return s.LeaveTeam }, false, event.ReasonCaptainMustTransfer},
		{"a formed team cannot be disbanded", event.ParticipationInput{Lifecycle: startedRolling, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: event.ParticipationActor{Authenticated: true, Status: participantModel.StatusApproved, HasTeam: true, Captain: true, TeamFormed: true, TeamMembers: 3}},
			func(s event.ParticipationState) event.Capability { return s.DisbandTeam }, false, event.ReasonTeamSwitchLocked},
		{"a captain may still kick from a formed team (it removes the person from the event)", event.ParticipationInput{Lifecycle: startedRolling, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: event.ParticipationActor{Authenticated: true, Status: participantModel.StatusApproved, HasTeam: true, Captain: true, TeamFormed: true, TeamMembers: 3}},
			func(s event.ParticipationState) event.Capability { return s.RemoveMember }, true, event.ReasonNone},
		{"the captain confirms a late-join team", event.ParticipationInput{Lifecycle: startedRolling, MinTeamSize: 2, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: event.ParticipationActor{Authenticated: true, Status: participantModel.StatusApproved, HasTeam: true, Captain: true, TeamMembers: 2}},
			func(s event.ParticipationState) event.Capability { return s.FormTeam }, true, event.ReasonNone},
		{"confirmation needs the minimum size", event.ParticipationInput{Lifecycle: startedRolling, MinTeamSize: 3, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: event.ParticipationActor{Authenticated: true, Status: participantModel.StatusApproved, HasTeam: true, Captain: true, TeamMembers: 2}},
			func(s event.ParticipationState) event.Capability { return s.FormTeam }, false, event.ReasonBelowMinimum},
		{"without late join the start forms teams", event.ParticipationInput{Lifecycle: published, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: captainOf},
			func(s event.ParticipationState) event.Capability { return s.FormTeam }, false, event.ReasonFormsAtStart},
		{"an unformed team gets no tasks", event.ParticipationInput{Lifecycle: startedRolling, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: event.ParticipationActor{Authenticated: true, Status: participantModel.StatusApproved, HasTeam: true}},
			func(s event.ParticipationState) event.Capability { return s.SeeTasks }, false, event.ReasonTeamNotFormed},
		{"a captain may kick before the start", event.ParticipationInput{Lifecycle: published, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: captainOf},
			func(s event.ParticipationState) event.Capability { return s.RemoveMember }, true, event.ReasonNone},
		{"late join stays open for people without a team", event.ParticipationInput{Lifecycle: startedRolling, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: approved},
			func(s event.ParticipationState) event.Capability { return s.JoinTeam }, true, event.ReasonNone},
		{"tasks wait for the start", event.ParticipationInput{Lifecycle: published, Registration: eventConfigModel.RegistrationOpen, Participation: &team, Actor: event.ParticipationActor{Authenticated: true, Status: participantModel.StatusApproved, HasTeam: true}},
			func(s event.ParticipationState) event.Capability { return s.SeeTasks }, false, event.ReasonNotStarted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Now = matrixNow
			got := tc.pick(event.ComputeParticipation(tc.in))
			if got.Allowed != tc.allow || got.Reason != tc.want {
				t.Fatalf("got %+v, want allowed=%v reason=%q", got, tc.allow, tc.want)
			}
		})
	}
}
