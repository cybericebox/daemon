package participantModel_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

var pNow = time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

func TestNewParticipantPolicy(t *testing.T) {
	eid, uid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	open, err := participantModel.NewParticipant(eid, uid, eventConfigModel.RegistrationOpen, pNow)
	if err != nil {
		t.Fatalf("open join must succeed: %v", err)
	}
	if open.Status != participantModel.StatusApproved {
		t.Fatalf("open registration must approve immediately, got %d", open.Status)
	}

	appr, err := participantModel.NewParticipant(eid, uid, eventConfigModel.RegistrationApproval, pNow)
	if err != nil {
		t.Fatalf("approval join must succeed: %v", err)
	}
	if appr.Status != participantModel.StatusPending {
		t.Fatalf("approval registration must be pending, got %d", appr.Status)
	}

	if _, err := participantModel.NewParticipant(eid, uid, eventConfigModel.RegistrationClose, pNow); err == nil {
		t.Fatalf("closed registration must reject the join")
	}
}

func TestApproveRejectFromPendingOnly(t *testing.T) {
	eid, uid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	actor := uuid.Must(uuid.NewV7())

	p, _ := participantModel.NewParticipant(eid, uid, eventConfigModel.RegistrationApproval, pNow)
	later := pNow.Add(time.Hour)
	if err := p.Approve(later, actor); err != nil {
		t.Fatalf("approve pending: %v", err)
	}
	if p.Status != participantModel.StatusApproved || p.DecidedAt == nil || !p.DecidedAt.Equal(later) {
		t.Fatalf("approve must set status + DecidedAt: %+v", p)
	}
	if !p.DecidedBy.Valid || p.DecidedBy.UUID != actor {
		t.Fatalf("approve must set DecidedBy")
	}
	// Second decision on a non-pending row must error.
	if err := p.Reject(later, actor); err == nil {
		t.Fatalf("reject on approved must error (not pending)")
	}

	p2, _ := participantModel.NewParticipant(eid, uid, eventConfigModel.RegistrationApproval, pNow)
	if err := p2.Reject(later, actor); err != nil {
		t.Fatalf("reject pending: %v", err)
	}
	if p2.Status != participantModel.StatusRejected {
		t.Fatalf("reject must set rejected")
	}
}

func TestAssignTeamRequiresApprovedParticipant(t *testing.T) {
	p := participantModel.Participant{Status: participantModel.StatusPending}
	if err := p.AssignTeam(uuid.Must(uuid.NewV7()), participantModel.TeamRoleMember); !errors.Is(err, participantModel.ErrParticipantNotApproved.Err()) {
		t.Fatalf("want ErrParticipantNotApproved, got %v", err)
	}
	p.Status = participantModel.StatusApproved
	teamID := uuid.Must(uuid.NewV7())
	if err := p.AssignTeam(teamID, participantModel.TeamRoleCaptain); err != nil {
		t.Fatalf("AssignTeam: %v", err)
	}
	if p.TeamID == nil || *p.TeamID != teamID || p.TeamRole == nil || *p.TeamRole != participantModel.TeamRoleCaptain {
		t.Fatalf("assignment was not recorded: %+v", p)
	}
	p.LeaveTeam()
	if p.TeamID != nil || p.TeamRole != nil {
		t.Fatalf("LeaveTeam did not clear assignment: %+v", p)
	}
}

func TestSetPseudonym(t *testing.T) {
	name := "  Frost  "
	p := participantModel.Participant{}
	if err := p.SetPseudonym(&name, true, false); err != nil || p.Pseudonym == nil || *p.Pseudonym != "Frost" {
		t.Fatalf("SetPseudonym trimmed = %v, %v", p.Pseudonym, err)
	}
	if err := p.SetPseudonym(&name, false, false); !errors.Is(err, participantModel.ErrPseudonymsDisabled.Err()) {
		t.Fatalf("disabled pseudonyms err = %v", err)
	}
	if err := p.SetPseudonym(&name, true, true); !errors.Is(err, participantModel.ErrPseudonymLocked.Err()) {
		t.Fatalf("after start err = %v", err)
	}
	short := "x"
	if err := p.SetPseudonym(&short, true, false); !errors.Is(err, participantModel.ErrPseudonymInvalid.Err()) {
		t.Fatalf("short pseudonym err = %v", err)
	}
	control := "ab\ncd"
	if err := p.SetPseudonym(&control, true, false); !errors.Is(err, participantModel.ErrPseudonymInvalid.Err()) {
		t.Fatalf("control pseudonym err = %v", err)
	}
	empty := " "
	if err := p.SetPseudonym(&empty, false, false); err != nil || p.Pseudonym != nil {
		t.Fatalf("clearing must be allowed when disabled: %v %v", p.Pseudonym, err)
	}
}

func TestAPseudonymWithBidiOrInvisibleCharactersIsRefused(t *testing.T) {
	for name, bad := range map[string]string{
		"bidi override": "ne" + string(rune(0x202e)) + "o", "zero width": "ne" + string(rune(0x200b)) + "o", "control": "ne" + string(rune(7)) + "o",
	} {
		value := bad
		if _, err := participantModel.NormalizePseudonym(&value); !errors.Is(err, participantModel.ErrPseudonymInvalid.Err()) {
			t.Errorf("%s: %v", name, err)
		}
	}
	ok := "neo_7"
	if got, err := participantModel.NormalizePseudonym(&ok); err != nil || *got != "neo_7" {
		t.Fatalf("got %v %v", got, err)
	}
}
