package participantRepo

import (
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

// M6: a pending invitation must not read out the profile name of the invited account.
func TestToListedHidesTheNameOfAnUnacceptedInvitation(t *testing.T) {
	row := postgres.ListEventParticipantsDetailedRow{
		UserID: uuid.Must(uuid.NewV7()), Invited: true, Status: int16(participantModel.StatusPending),
		FirstName: "Real", LastName: "Person", Email: "real@example.test", DisplayName: "Real Person",
	}
	got := toListed(row)
	if got.FirstName != "" || got.LastName != "" || got.DisplayName != "" {
		t.Fatalf("name leaked: %+v", got)
	}
	if got.Email != "real@example.test" {
		t.Fatalf("the address the organizer typed stays: %q", got.Email)
	}

	row.Status = int16(participantModel.StatusApproved) // accepted
	if got = toListed(row); got.FirstName != "Real" || got.DisplayName != "Real Person" {
		t.Fatalf("an accepted participant shows the name: %+v", got)
	}
	row.Status, row.Invited = int16(participantModel.StatusPending), false // an application, not an invitation
	if got = toListed(row); got.FirstName != "Real" {
		t.Fatalf("an application keeps the name: %+v", got)
	}
}
