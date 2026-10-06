package labBindingModel

import (
	"testing"

	"github.com/gofrs/uuid"
)

func TestNamesUseExerciseAndVariantIdentity(t *testing.T) {
	eventID, teamID, exerciseID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	group, lab, err := Names(eventID, teamID, exerciseID, 1)
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	if want, _ := GroupName(eventID, teamID); group != want {
		t.Fatalf("group = %q, want the team group %q", group, want)
	}
	if lab != "x-"+ShortID(exerciseID)+"-v1" {
		t.Fatalf("lab = %q", lab)
	}
	// Every task of the exercise asks for the same name: no challenge is involved.
	_, again, _ := Names(eventID, teamID, exerciseID, 1)
	if again != lab {
		t.Fatalf("lab = %q then %q; the tasks of one exercise must share a Lab", lab, again)
	}
	_, other, _ := Names(eventID, teamID, uuid.Must(uuid.NewV7()), 1)
	_, otherVariant, _ := Names(eventID, teamID, exerciseID, 2)
	if other == lab || otherVariant == lab {
		t.Fatal("another exercise or variant must get another Lab")
	}
	if _, _, err = Names(eventID, teamID, uuid.Nil, 0); err == nil {
		t.Fatal("a nil exercise must be rejected")
	}
}

func TestLabNameChangesPerGeneration(t *testing.T) {
	exerciseID := uuid.Must(uuid.NewV7())
	base := LabName(exerciseID, 3, 0)
	if got := LabName(exerciseID, 3, 2); got != base+"-g2" {
		t.Fatalf("generation 2 = %q", got)
	}
	if got := NextLabName(base, 1); got != base+"-g1" {
		t.Fatalf("next of base = %q", got)
	}
	if got := NextLabName(base+"-g1", 2); got != base+"-g2" {
		t.Fatalf("next of g1 = %q", got)
	}
	if len(LabName(exerciseID, 2147483647, 2147483647)) > 63 {
		t.Fatal("Lab names must stay valid Kubernetes names")
	}
	if !IsLegacyLabName("c-"+exerciseID.String()) || IsLegacyLabName(base) {
		t.Fatal("legacy detection")
	}
}

func TestParseGroupNameInvertsTheBuilder(t *testing.T) {
	event, team := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	group, err := GroupName(event, team)
	if err != nil {
		t.Fatal(err)
	}
	gotEvent, gotTeam, ok := ParseGroupName(group)
	if !ok || gotEvent != event || gotTeam != team {
		t.Fatalf("ParseGroupName(%q) = %v %v %v", group, gotEvent, gotTeam, ok)
	}
	for _, bad := range []string{"", "e-", "test-abc", "e-" + event.String(), "e-x-t-y", "e-" + event.String() + "-t-"} {
		if _, _, ok := ParseGroupName(bad); ok {
			t.Errorf("ParseGroupName(%q) accepted", bad)
		}
	}
}
