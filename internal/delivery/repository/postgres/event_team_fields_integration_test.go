package postgres_test

import (
	"context"
	"testing"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestTeamFieldsConfigAndAnswersRoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "teamfields")
	teams := eventTeamRepo.New(db.Queries)
	form := eventFormModel.Form{Enabled: true, Required: true, Document: eventContentModel.Document{Blocks: []eventContentModel.Block{{
		ID: "school", Type: eventContentModel.BlockField, Key: "school", Input: "text", Label: "School", Required: true,
	}}}}
	first, err := teams.PutFieldConfig(ctx, event.ID, form, itNow)
	if err != nil || first.Version != 1 {
		t.Fatalf("first config: %+v err=%v", first, err)
	}
	second, err := teams.PutFieldConfig(ctx, event.ID, form, itNow)
	if err != nil || second.Version != 2 {
		t.Fatalf("updated config: %+v err=%v", second, err)
	}
	stored, err := teams.GetFieldConfig(ctx, event.ID)
	if err != nil || stored.Version != 2 || stored.Document.Blocks[0].Key != "school" {
		t.Fatalf("stored config: %+v err=%v", stored, err)
	}
	captainID := mustSeedUser(t, db, "teamfields@test.test")
	team, err := eventTeamModel.New(event.ID, captainID, "School Team", "teamfields-code", itNow)
	if err != nil {
		t.Fatal(err)
	}
	team, err = teams.Create(ctx, team)
	if err != nil {
		t.Fatal(err)
	}
	if affected, saveErr := teams.UpdateExtraFields(ctx, event.ID, team.ID, map[string]any{"school": "Kyiv"}); saveErr != nil || affected != 1 {
		t.Fatalf("save team answers: affected=%d err=%v", affected, saveErr)
	}
	answers, err := teams.GetExtraFields(ctx, event.ID, team.ID)
	if err != nil || answers["school"] != "Kyiv" {
		t.Fatalf("stored team answers: %+v err=%v", answers, err)
	}
}
