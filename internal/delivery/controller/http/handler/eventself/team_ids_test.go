package eventself

import (
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// Members' account ids reach the participant site only where the captain needs them.
func TestTeamResponsesWithholdOtherMembersIDs(t *testing.T) {
	captain := uuid.Must(uuid.NewV7())
	team := mustJSON(t, toOwnTeamResponse(eventUseCase.OwnTeamView{CaptainID: captain}))
	if strings.Contains(team, "CaptainID") || strings.Contains(team, captain.String()) {
		t.Fatalf("own team response carries the captain's account id: %s", team)
	}
	stats := mustJSON(t, toParticipationStatsResponse(eventUseCase.ParticipationStatsView{
		Me:   eventUseCase.ParticipationMemberView{UserID: captain, Name: "Me"},
		Team: eventUseCase.ParticipationTeamStatsView{Members: []eventUseCase.ParticipationMemberView{{Name: "Mate"}}},
	}))
	if !strings.Contains(stats, `"Me":{"UserID":"`+captain.String()) || !strings.Contains(stats, `"Members":[{"Name":"Mate"`) {
		t.Fatalf("a withheld member id must be omitted, the caller's own kept: %s", stats)
	}
}
