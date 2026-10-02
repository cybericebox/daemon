package event

import (
	"testing"

	"github.com/gofrs/uuid"

	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

func TestMayShowRealNames(t *testing.T) {
	approved := &participantModel.Participant{Status: participantModel.StatusApproved}
	pending := &participantModel.Participant{Status: participantModel.StatusPending}
	for _, tc := range []struct {
		name        string
		visibility  eventConfigModel.Visibility
		manager     bool
		participant *participantModel.Participant
		want        bool
	}{
		{"public guest", eventConfigModel.VisibilityPublic, false, nil, true},
		{"private guest", eventConfigModel.VisibilityPrivate, false, nil, false},
		{"private pending", eventConfigModel.VisibilityPrivate, false, pending, false},
		{"private approved", eventConfigModel.VisibilityPrivate, false, approved, true},
		{"hidden approved", eventConfigModel.VisibilityHidden, false, approved, false},
		{"hidden manager", eventConfigModel.VisibilityHidden, true, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := resultsPolicy{cfg: eventConfigModel.EventConfig{ParticipantsVisibility: tc.visibility}, manager: tc.manager, participant: tc.participant}
			if got := p.mayShowRealNames(); got != tc.want {
				t.Fatalf("mayShowRealNames = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMaskRealNamesKeepsPseudonymsAndOwnRow(t *testing.T) {
	own, other, alias := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	rows := []ScoreboardEntryView{
		{TeamID: own, TeamName: "Own Real", NameIsReal: true},
		{TeamID: other, TeamName: "Other Real", NameIsReal: true},
		{TeamID: alias, TeamName: "Frost"},
	}
	maskRealNames(rows, &own)
	if rows[0].TeamName != "Own Real" || rows[0].NameHidden {
		t.Fatalf("own row masked: %+v", rows[0])
	}
	if rows[1].TeamName != "" || !rows[1].NameHidden {
		t.Fatalf("other real name shown: %+v", rows[1])
	}
	if rows[2].TeamName != "Frost" || rows[2].NameHidden {
		t.Fatalf("pseudonym masked: %+v", rows[2])
	}
}
