package inboxModel

import (
	"testing"

	"github.com/gofrs/uuid"
)

func TestClassify_Matrix(t *testing.T) {
	cases := []struct {
		typ  string
		role RecipientRole
		want Category
	}{
		{"participant.approval_registration.submitted", RoleSubject, CategoryPersonal},
		{"participant.approval_registration.approved", RoleSubject, CategoryPersonal},
		{"participant.approval_registration.rejected", RoleSubject, CategoryPersonal},
		{TypeApplicationSubmitted, RoleManager, CategoryRequests},
		{"event.lab.failed", RoleSubject, CategoryRequests},
		{"event.lab.failed", RoleManager, CategoryRequests},
		{"event.lab.failed", RoleAdmin, CategoryRequests},
		{TypeProposalSubmitted, RoleAdmin, CategoryRequests},
		{TypeProposalSubmitted, RoleSubject, CategoryPersonal},
		{TypeProposalApproved, RoleSubject, CategoryPersonal},
		{TypeProposalRejected, RoleSubject, CategoryPersonal},
		{"participant.event.start_reminder", RoleParticipant, CategoryActivity},
		{"participant.event.start_reminder", RoleManager, CategoryActivity},
		{"participant.event.finished", RoleParticipant, CategoryActivity},
		{"participant.event.finished", RoleManager, CategoryActivity},
		{TypeResultsPublished, RoleParticipant, CategoryActivity},
		{"event.manager.assigned", RoleSubject, CategoryPersonal},
		{"participant.invitation.revoked", RoleSubject, CategoryPersonal},
		{"participant.invitation.expired", RoleSubject, CategoryPersonal},
		{"password_reset", RoleSubject, CategoryPersonal},
		{"account_inactivity_warning", RoleSubject, CategoryPersonal},
		{"unknown.type", RoleAdmin, CategoryPersonal},
	}
	for _, c := range cases {
		if got := Classify(c.typ, c.role); got != c.want {
			t.Errorf("Classify(%q, %q) = %q, want %q", c.typ, c.role, got, c.want)
		}
	}
}

func TestNewMeta_ActionRequiredOnlyForRequests(t *testing.T) {
	ref := StandRef(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()))
	request := NewMeta("event.lab.failed", RoleManager, ref)
	if !request.ActionRequired || request.Category != CategoryRequests || request.SubjectRef != ref {
		t.Fatalf("lab failure meta = %+v", request)
	}
	fyi := NewMeta("participant.approval_registration.submitted", RoleSubject, "application:x")
	if fyi.ActionRequired || fyi.SubjectRef != "" || fyi.Category != CategoryPersonal {
		t.Fatalf("applicant meta = %+v", fyi)
	}
	activity := NewMeta("participant.event.finished", RoleManager, "")
	if activity.ActionRequired || activity.Category != CategoryActivity {
		t.Fatalf("activity meta = %+v", activity)
	}
}

func TestParseCategory(t *testing.T) {
	for _, s := range []string{"requests", "personal", "activity"} {
		if c, ok := ParseCategory(s); !ok || string(c) != s {
			t.Errorf("ParseCategory(%q) = %q, %v", s, c, ok)
		}
	}
	if _, ok := ParseCategory("all"); ok {
		t.Error("ParseCategory(all) must be rejected")
	}
}

func TestManuallyResolvable(t *testing.T) {
	if !ManuallyResolvable("event.lab.failed") {
		t.Error("lab failure must be manually resolvable")
	}
	for _, typ := range []string{TypeApplicationSubmitted, TypeProposalSubmitted} {
		if ManuallyResolvable(typ) {
			t.Errorf("%s closes only through its decision", typ)
		}
	}
}

func TestSubjectRefs(t *testing.T) {
	e := uuid.FromStringOrNil("01900000-0000-7000-8000-000000000001")
	u := uuid.FromStringOrNil("01900000-0000-7000-8000-000000000002")
	if got := ApplicationRef(e, u); got != "application:"+e.String()+":"+u.String() {
		t.Errorf("ApplicationRef = %q", got)
	}
	if got := StandRef(e, u); got != "stand:"+e.String()+":"+u.String() {
		t.Errorf("StandRef = %q", got)
	}
	if got := ProposalRef(u); got != "proposal:"+u.String() {
		t.Errorf("ProposalRef = %q", got)
	}
}
