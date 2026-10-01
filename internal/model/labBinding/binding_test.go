package labBindingModel

import (
	"testing"

	"github.com/gofrs/uuid"
)

func TestNamesUseChallengeIdentity(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	teamID := uuid.Must(uuid.NewV7())
	firstChallengeID := uuid.Must(uuid.NewV7())
	secondChallengeID := uuid.Must(uuid.NewV7())

	firstGroup, firstLab, err := Names(eventID, teamID, firstChallengeID)
	if err != nil {
		t.Fatalf("first Names: %v", err)
	}
	secondGroup, secondLab, err := Names(eventID, teamID, secondChallengeID)
	if err != nil {
		t.Fatalf("second Names: %v", err)
	}
	if firstGroup != secondGroup {
		t.Fatalf("groups = %q, %q; one team must retain one LabGroup", firstGroup, secondGroup)
	}
	if group, err := GroupName(eventID, teamID); err != nil || group != firstGroup {
		t.Fatalf("early VPN group = %q, %v; want %q", group, err, firstGroup)
	}
	if firstLab == secondLab {
		t.Fatalf("labs = %q, %q; distinct challenges must have distinct Labs", firstLab, secondLab)
	}
	if firstLab != "c-"+firstChallengeID.String() {
		t.Fatalf("first lab = %q, want challenge-derived name", firstLab)
	}
}

func TestLabNameChangesPerGeneration(t *testing.T) {
	challengeID := uuid.Must(uuid.FromString("01900000-0000-7000-8000-000000000001"))
	if got := LabName(challengeID, 0); got != "c-01900000-0000-7000-8000-000000000001" {
		t.Fatalf("generation 0 = %q; it must keep the legacy name", got)
	}
	if got := LabName(challengeID, 2); got != "c-01900000-0000-7000-8000-000000000001-g2" {
		t.Fatalf("generation 2 = %q", got)
	}
	if len(LabName(challengeID, 99999)) > 63 {
		t.Fatal("Lab names must stay valid Kubernetes names")
	}
}

func TestParseGroupAndLabNamesInvertTheBuilders(t *testing.T) {
	event, team, challenge := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	group, err := GroupName(event, team)
	if err != nil {
		t.Fatal(err)
	}
	gotEvent, gotTeam, ok := ParseGroupName(group)
	if !ok || gotEvent != event || gotTeam != team {
		t.Fatalf("ParseGroupName(%q) = %v %v %v", group, gotEvent, gotTeam, ok)
	}
	for _, generation := range []int32{0, 1, 7} {
		gotChallenge, gotGeneration, ok := ParseLabName(LabName(challenge, generation))
		if !ok || gotChallenge != challenge || gotGeneration != generation {
			t.Fatalf("generation %d: got %v %d %v", generation, gotChallenge, gotGeneration, ok)
		}
	}
	for _, bad := range []string{"", "e-", "test-abc", "e-" + event.String(), "e-x-t-y", "e-" + event.String() + "-t-"} {
		if _, _, ok := ParseGroupName(bad); ok {
			t.Errorf("ParseGroupName(%q) accepted", bad)
		}
	}
	for _, bad := range []string{"", "c-", "c-nope", "c-" + challenge.String() + "-g0", "c-" + challenge.String() + "-gx", "web"} {
		if _, _, ok := ParseLabName(bad); ok {
			t.Errorf("ParseLabName(%q) accepted", bad)
		}
	}
}
