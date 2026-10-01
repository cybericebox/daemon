package event

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model/rbac"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

// TestResultsPolicyMatrix walks every audience × visibility × phase × freeze
// combination through the read gate and the freeze of the results page.
func TestResultsPolicyMatrix(t *testing.T) {
	now := time.Now().UTC()
	phases := map[string][2]time.Duration{ // start, finish relative to now
		"before": {time.Hour, 5 * time.Hour},
		"during": {-time.Hour, 30 * time.Minute},
		"after":  {-5 * time.Hour, -time.Hour},
	}
	audiences := []string{"guest", "applicant", "participant", "staff"}
	visibilities := []eventConfigModel.Visibility{eventConfigModel.VisibilityHidden, eventConfigModel.VisibilityPrivate, eventConfigModel.VisibilityPublic}
	for _, audience := range audiences {
		for _, visibility := range visibilities {
			for phase, offsets := range phases {
				for _, freezeOn := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%d/%s/freeze=%v", audience, visibility, phase, freezeOn), func(t *testing.T) {
						finish := now.Add(offsets[1])
						policy := resultsPolicy{
							cfg:     eventConfigModel.EventConfig{ScoreboardVisibility: visibility, Results: eventConfigModel.ResultsSettings{FreezeEnabled: freezeOn, FreezeMinutes: 60}},
							event:   eventModel.Event{Lifecycle: eventModel.Lifecycle{Configured: true, PublishAt: now.Add(-24 * time.Hour), StartAt: now.Add(offsets[0]), FinishAt: &finish}},
							manager: audience == "staff",
						}
						switch audience {
						case "applicant":
							policy.participant = &participantModel.Participant{Status: participantModel.StatusPending}
						case "participant":
							policy.participant = &participantModel.Participant{Status: participantModel.StatusApproved}
						}
						started := phase != "before"
						sees := audience == "staff" || visibility == eventConfigModel.VisibilityPublic || (visibility == eventConfigModel.VisibilityPrivate && audience == "participant")

						want := ResultsAvailable
						switch {
						case audience == "staff":
						case visibility == eventConfigModel.VisibilityHidden:
							want = ResultsHidden
						case !sees:
							want = ResultsParticipantsOnly
						case !started:
							want = ResultsNotStarted
						}
						if got := policy.availability(now); got != want {
							t.Fatalf("availability = %s, want %s", got, want)
						}
						// Before the start the table is readable (teams, no points).
						if err := policy.readErr(now, false); (err == nil) != (want == ResultsAvailable || want == ResultsNotStarted) {
							t.Fatalf("read gate = %v for %s", err, want)
						}
						// The freeze window is the last hour: only "during" is inside it.
						active := freezeOn && phase == "during"
						view := policy.freeze(now, false)
						if view.Active != active || view.Applied != (active && audience != "staff") {
							t.Fatalf("freeze = %+v", view)
						}
						if (view.cutoff() != nil) != view.Applied {
							t.Fatalf("cutoff must follow the applied freeze: %+v", view)
						}
						// The live screen stays staff-only (L1).
						if err := policy.readErr(now, true); (err == nil) != (audience == "staff") {
							t.Fatalf("live screen gate = %v", err)
						}
					})
				}
			}
		}
	}
}

// A screen link reads the staff view of its event without any session,
// role or lookup; a public reader without it does not.
func TestScreenAccessIsTheStaffViewOnly(t *testing.T) {
	u := &EventUseCase{}
	if viewer := u.loadResultsViewer(context.Background(), uuid.Must(uuid.NewV7()), LiveScreenResultsAccess); !viewer.manager || viewer.participant != nil {
		t.Fatalf("screen viewer = %+v", viewer)
	}
	if LiveScreenResultsAccess.Role != rbac.RolePublic || LiveScreenResultsAccess.UserID != nil {
		t.Fatalf("screen access must carry no role or user: %+v", LiveScreenResultsAccess)
	}
	if viewer := u.loadResultsViewer(context.Background(), uuid.Must(uuid.NewV7()), ResultsAccess{Role: rbac.RolePublic}); viewer.manager {
		t.Fatal("a public reader is not staff")
	}
}
