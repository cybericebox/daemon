package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// TestParticipationModel_NamesAdmissionAndTabs covers the W2 SQL rules:
// public names resolved at read, the pseudonym switch and uniqueness, team
// admission by the effective minimum, and the moderation tab filters.
func TestParticipationModel_NamesAdmissionAndTabs(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "w2model")
	configs := eventConfigRepo.New(db.Queries)
	config := eventConfigModel.NewEventConfig(event.ID, epNow)
	team := eventConfigModel.ParticipationTeam
	config.Participation = &team
	config.AllowPseudonyms = true
	if _, err := configs.Create(ctx, config); err != nil {
		t.Fatalf("create config: %v", err)
	}
	captainID := mustSeedUser(t, db, "w2-captain@test.test")
	memberID := mustSeedUser(t, db, "w2-member@test.test")
	invitedID := mustSeedUser(t, db, "w2-invited@test.test")
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET first_name = 'Олена', last_name = 'Коваль' WHERE id = $1`, captainID); err != nil {
		t.Fatal(err)
	}
	participants := participantRepo.New(db.Queries)
	for _, userID := range []uuid.UUID{captainID, memberID} {
		p := mustNewPendingParticipant(t, event.ID, userID, epNow)
		created, _, err := participants.Upsert(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if err = created.Approve(epNow, captainID); err != nil {
			t.Fatal(err)
		}
		if _, err = participants.Update(ctx, created); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := participants.Invite(ctx, event.ID, invitedID, captainID, uuid.NullUUID{}, epNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Public name: profile name, then the pseudonym while pseudonyms are allowed.
	profile, err := participants.Profile(ctx, event.ID, captainID)
	if err != nil || profile.DisplayName != "Олена Коваль" {
		t.Fatalf("profile name = %+v, %v", profile, err)
	}
	pseudonym := "Frost"
	if _, err = participants.SetPseudonym(ctx, event.ID, captainID, &pseudonym); err != nil {
		t.Fatal(err)
	}
	if profile, _ = participants.Profile(ctx, event.ID, captainID); profile.DisplayName != "Frost" {
		t.Fatalf("pseudonym must be the public name: %+v", profile)
	}
	taken := "frost"
	if _, err = participants.SetPseudonym(ctx, event.ID, memberID, &taken); err == nil {
		t.Fatal("a case-insensitive duplicate pseudonym must be rejected")
	} else if _, ok := repositoryTools.UniqueViolationError(err, participantModel.ErrPseudonymTaken); !ok {
		t.Fatalf("duplicate pseudonym must be a unique violation: %v", err)
	}
	if profile, _ = participants.Profile(ctx, event.ID, memberID); profile.DisplayName != "Учасник" {
		t.Fatalf("a nameless profile falls back to «Учасник»: %+v", profile)
	}

	// Admission: team mode defaults to a minimum of two members.
	teams := eventTeamRepo.New(db.Queries)
	created, err := teams.Create(ctx, mustTeam(t, event.ID, captainID, "Blue Team"))
	if err != nil {
		t.Fatal(err)
	}
	if minimum, _ := teams.MinTeamSize(ctx, event.ID); minimum != 2 {
		t.Fatalf("default team-mode minimum = %d, want 2", minimum)
	}
	if admitted, _ := teams.Admitted(ctx, event.ID, created.ID); admitted {
		t.Fatal("a one-member team must not be admitted in team mode")
	}
	created.SetAdmittedManually(true, epNow.Add(time.Minute))
	if _, err = teams.Update(ctx, created, created.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if admitted, _ := teams.Admitted(ctx, event.ID, created.ID); !admitted {
		t.Fatal("manual admission must admit the team")
	}

	// Moderation tabs and counters.
	counts, err := participants.CountKinds(ctx, event.ID)
	if err != nil || counts.Participants != 2 || counts.Applications != 0 || counts.Invitations != 1 {
		t.Fatalf("tab counts = %+v, %v", counts, err)
	}
	invitations, err := participants.List(ctx, event.ID, -1, participantRepo.KindInvitations, "", nil, epCursorSentinelTime, epMaxUUID, 10)
	if err != nil || len(invitations) != 1 || invitations[0].Participant.UserID != invitedID || invitations[0].Participant.InvitationSentAt != nil {
		t.Fatalf("invitations tab = %+v, %v", invitations, err)
	}
	if affected, _ := participants.MarkInvitationSent(ctx, event.ID, invitedID, epNow); affected != 1 {
		t.Fatal("invitation delivery must be recorded")
	}
	if affected, _ := participants.DeleteInvitation(ctx, event.ID, captainID); affected != 0 {
		t.Fatal("an approved participant is not a revocable invitation")
	}
	if affected, _ := participants.DeleteInvitation(ctx, event.ID, invitedID); affected != 1 {
		t.Fatal("a pending invitation must be revocable")
	}
}

// TestParticipationModel_IndividualTeamUsesPublicName proves scoreboard reads
// never expose the technical Solo-xxxx name.
func TestParticipationModel_IndividualTeamUsesPublicName(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "w2solo")
	config := eventConfigModel.NewEventConfig(event.ID, epNow)
	individual := eventConfigModel.ParticipationIndividual
	config.Participation = &individual
	if _, err := eventConfigRepo.New(db.Queries).Create(ctx, config); err != nil {
		t.Fatal(err)
	}
	userID := mustSeedUser(t, db, "w2-solo@test.test")
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET first_name = 'Іван', last_name = 'Мельник' WHERE id = $1`, userID); err != nil {
		t.Fatal(err)
	}
	solo, err := eventTeamModel.NewIndividual(event.ID, userID, "Solo-12345678", "individual-join-code", epNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eventTeamRepo.New(db.Queries).Create(ctx, solo); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Queries.ListEventScoreboard(ctx, postgres.ListEventScoreboardParams{EventID: event.ID})
	if err != nil || len(rows) != 1 || rows[0].TeamName != "Іван Мельник" {
		t.Fatalf("scoreboard must show the participant name: %+v, %v", rows, err)
	}
}

func mustTeam(t *testing.T, eventID, captainID uuid.UUID, name string) eventTeamModel.EventTeam {
	t.Helper()
	team, err := eventTeamModel.New(eventID, captainID, name, "w2-team-join-code-"+name, epNow)
	if err != nil {
		t.Fatal(err)
	}
	return team
}
