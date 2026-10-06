package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The manager's detail view reads one participant with the team role and the
// participant's own attempt figures.
func TestParticipantDetail(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := participantRepo.New(db.Queries)
	f := anSeed(t, db, "pdetail")
	anAttempt(t, db, f, f.team, f.teamChallenge, false, anStart.Add(time.Minute))
	anAttempt(t, db, f, f.team, f.teamChallenge, true, anStart.Add(2*time.Minute))
	anAttempt(t, db, f, f.team, f.teamChallenge, true, anStart.Add(3*time.Minute))

	got, err := repo.Detail(ctx, f.event, f.user)
	if err != nil {
		t.Fatal(err)
	}
	if got.Attempts != 3 || got.Solves != 1 || got.TeamName != "Blue" || got.Participant.TeamRole == nil || *got.Participant.TeamRole != 0 || got.Email == "" {
		t.Fatalf("detail: %+v", got)
	}

	invited := mustSeedUser(t, db, "pdetail-invited@test.test")
	apExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, invited) VALUES ($1, $2, 1, $3, true)`, f.event, invited, anStart)
	one, err := repo.Detail(ctx, f.event, invited)
	if err != nil || !one.Participant.Invited || one.Attempts != 0 || one.Solves != 0 || one.Participant.TeamRole != nil {
		t.Fatalf("invitee: %+v %v", one, err)
	}
	if _, err = repo.Detail(ctx, f.event, uuid.Must(uuid.NewV7())); err == nil {
		t.Fatal("an unknown user is an error")
	}
}
