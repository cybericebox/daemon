// Package eventActivityModel is the vocabulary of the event activity log
// (docs/EVENT-ANALYTICS.md §3, D1/D2): what participants did on the event
// site. Participation changes (status, team, captain) are logged by database
// triggers with the same kinds; this package builds the rows the application
// writes itself.
package eventActivityModel

import (
	"errors"
	"time"

	"github.com/gofrs/uuid"

	challengeAttempt "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

type Kind string

const (
	KindTaskOpened               Kind = "task_opened"
	KindAttachmentDownloaded     Kind = "attachment_downloaded"
	KindHintViewed               Kind = "hint_viewed"
	KindTeamJoined               Kind = "team_joined"
	KindTeamLeft                 Kind = "team_left"
	KindCaptainChanged           Kind = "captain_changed"
	KindParticipantStatusChanged Kind = "participant_status_changed"
	KindAttemptRejected          Kind = "attempt_rejected"
)

// RejectReason is why a flag submission was refused before it became an
// attempt (D2). The rejected answer itself is never logged.
type RejectReason string

const (
	RejectRateLimit       RejectReason = "rate_limit"
	RejectNotStarted      RejectReason = "not_started"
	RejectAfterFinish     RejectReason = "after_finish"
	RejectNotApproved     RejectReason = "not_approved"
	RejectTeamNotAdmitted RejectReason = "team_not_admitted"
	RejectFormRequired    RejectReason = "form_required"
	RejectLocked          RejectReason = "locked"
	RejectUnavailable     RejectReason = "unavailable"
)

// TaskOpenWindow is the dedupe window of task opens: at most one row per
// user and task in it, however often the page is opened.
const TaskOpenWindow = time.Minute

// Activity is one log row. TeamID and SubjectID are optional; Data holds
// small kind-specific details.
type Activity struct {
	EventID   uuid.UUID
	UserID    uuid.UUID
	TeamID    *uuid.UUID
	Kind      Kind
	SubjectID *uuid.UUID
	At        time.Time
	Data      map[string]any
}

// TaskOpened: the participant opened a task of the team's board.
func TaskOpened(eventID, userID, teamID, challengeID uuid.UUID, at time.Time) Activity {
	return Activity{EventID: eventID, UserID: userID, TeamID: &teamID, Kind: KindTaskOpened, SubjectID: &challengeID, At: at}
}

// AttachmentDownloaded: the participant downloaded a file of a task.
func AttachmentDownloaded(eventID, userID, teamID, challengeID, fileID uuid.UUID, at time.Time) Activity {
	return Activity{
		EventID: eventID, UserID: userID, TeamID: &teamID, Kind: KindAttachmentDownloaded, SubjectID: &challengeID, At: at,
		Data: map[string]any{"file_id": fileID.String()},
	}
}

// AttemptRejected: a submission refused with reason. teamID is nil when the
// participant has no team yet.
func AttemptRejected(eventID, userID uuid.UUID, teamID *uuid.UUID, challengeID uuid.UUID, reason RejectReason, at time.Time) Activity {
	return Activity{
		EventID: eventID, UserID: userID, TeamID: teamID, Kind: KindAttemptRejected, SubjectID: &challengeID, At: at,
		Data: map[string]any{"reason": string(reason)},
	}
}

// ClassifyRejection maps a submission error to its reason; ok is false for
// errors that are not a refusal (platform failures, idempotency conflicts).
// lifecycle is read only when the runtime was closed, to tell a submission
// before the start from one after the finish.
func ClassifyRejection(err error, at time.Time, lifecycle func() (eventModel.Lifecycle, bool)) (RejectReason, bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, challengeAttempt.ErrTooManyAttempts.Err()):
		return RejectRateLimit, true
	case errors.Is(err, eventModel.ErrEventRuntimeNotOpen.Err()):
		if l, ok := lifecycle(); ok && l.HasStarted(at) {
			return RejectAfterFinish, true
		}
		return RejectNotStarted, true
	case errors.Is(err, participantModel.ErrParticipantNotApproved.Err()):
		return RejectNotApproved, true
	case errors.Is(err, eventTeamModel.ErrEventTeamNotAdmitted.Err()):
		return RejectTeamNotAdmitted, true
	case errors.Is(err, participantModel.ErrEventFormRequired.Err()):
		return RejectFormRequired, true
	case errors.Is(err, teamChallengeModel.ErrTeamChallengePrerequisites.Err()):
		return RejectLocked, true
	case errors.Is(err, teamChallengeModel.ErrTeamChallengeTransition.Err()):
		return RejectUnavailable, true
	default:
		return "", false
	}
}
