package eventActivityModel_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	challengeAttempt "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventActivityModel "github.com/cybericebox/daemon/internal/model/eventActivity"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
	pkgerr "github.com/cybericebox/daemon/pkg/err"
)

var at = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func started() (eventModel.Lifecycle, bool) {
	return eventModel.Lifecycle{Configured: true, StartAt: at.Add(-time.Hour)}, true
}

func upcoming() (eventModel.Lifecycle, bool) {
	return eventModel.Lifecycle{Configured: true, StartAt: at.Add(time.Hour)}, true
}

func TestClassifyRejection(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		lifecycle func() (eventModel.Lifecycle, bool)
		want      eventActivityModel.RejectReason
		ok        bool
	}{
		{"rate limit with retry detail", challengeAttempt.ErrTooManyAttempts.WithDetail(pkgerr.DetailRetryAfterSeconds, 3).Err(), started, eventActivityModel.RejectRateLimit, true},
		{"closed after the start", eventModel.ErrEventRuntimeNotOpen.Err(), started, eventActivityModel.RejectAfterFinish, true},
		{"closed before the start", eventModel.ErrEventRuntimeNotOpen.Err(), upcoming, eventActivityModel.RejectNotStarted, true},
		{"not approved", participantModel.ErrParticipantNotApproved.Err(), started, eventActivityModel.RejectNotApproved, true},
		{"team not admitted", eventTeamModel.ErrEventTeamNotAdmitted.Err(), started, eventActivityModel.RejectTeamNotAdmitted, true},
		{"required form", participantModel.ErrEventFormRequired.Err(), started, eventActivityModel.RejectFormRequired, true},
		{"prerequisites", teamChallengeModel.ErrTeamChallengePrerequisites.Err(), started, eventActivityModel.RejectLocked, true},
		{"unpublished", teamChallengeModel.ErrTeamChallengeTransition.Err(), started, eventActivityModel.RejectUnavailable, true},
		{"idempotency conflict is not a refusal", challengeAttempt.ErrIdempotencyKeyConflict.Err(), started, "", false},
		{"platform failure is not a refusal", errors.New("db down"), started, "", false},
		{"success", nil, started, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := eventActivityModel.ClassifyRejection(tc.err, at, tc.lifecycle)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// The lifecycle is read only for a closed runtime.
func TestClassifyRejection_ReadsLifecycleLazily(t *testing.T) {
	read := false
	lifecycle := func() (eventModel.Lifecycle, bool) { read = true; return started() }
	eventActivityModel.ClassifyRejection(challengeAttempt.ErrTooManyAttempts.Err(), at, lifecycle)
	if read {
		t.Fatal("a rate limit must not read the lifecycle")
	}
	if got, _ := eventActivityModel.ClassifyRejection(eventModel.ErrEventRuntimeNotOpen.Err(), at, func() (eventModel.Lifecycle, bool) { return eventModel.Lifecycle{}, false }); got != eventActivityModel.RejectNotStarted {
		t.Fatalf("an unreadable lifecycle falls back to not_started, got %q", got)
	}
}

func TestActivityBuilders(t *testing.T) {
	eventID, userID, teamID, challengeID, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	open := eventActivityModel.TaskOpened(eventID, userID, teamID, challengeID, at)
	if open.Kind != eventActivityModel.KindTaskOpened || *open.SubjectID != challengeID || *open.TeamID != teamID || !open.At.Equal(at) {
		t.Fatalf("task opened: %+v", open)
	}
	download := eventActivityModel.AttachmentDownloaded(eventID, userID, teamID, challengeID, fileID, at)
	if download.Kind != eventActivityModel.KindAttachmentDownloaded || download.Data["file_id"] != fileID.String() {
		t.Fatalf("attachment downloaded: %+v", download)
	}
	rejected := eventActivityModel.AttemptRejected(eventID, userID, nil, challengeID, eventActivityModel.RejectRateLimit, at)
	if rejected.TeamID != nil || rejected.Data["reason"] != "rate_limit" || *rejected.SubjectID != challengeID {
		t.Fatalf("attempt rejected: %+v", rejected)
	}
}
