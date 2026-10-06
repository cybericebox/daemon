package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// 0107: dynamic scoring may decay to 0 (min ≥ 0, max > min), event and task.
func TestDynamicScoringMinimumMayBeZero(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "scoringzero")
	challenge := mustCreateScoringChallenge(t, db.Queries, event.ID)
	if _, err := db.Pool.Exec(ctx, `UPDATE events SET scoring_mode = 1, dynamic_min_points = 0, dynamic_max_points = 100, dynamic_floor_at_percent = 50 WHERE id = $1`, event.ID); err != nil {
		t.Fatalf("event min 0: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE event_challenges SET scoring_mode = 1, dynamic_algorithm = 0, dynamic_min_points = 0, dynamic_max_points = 10, dynamic_floor_at_percent = 50 WHERE id = $1`, challenge.ID); err != nil {
		t.Fatalf("task min 0: %v", err)
	}
	_, err := db.Pool.Exec(ctx, `UPDATE events SET dynamic_min_points = -1 WHERE id = $1`, event.ID)
	assertCheckViolation(t, err)
	_, err = db.Pool.Exec(ctx, `UPDATE event_challenges SET dynamic_max_points = 0 WHERE id = $1`, challenge.ID)
	assertCheckViolation(t, err)
}

// Infrastructure is the admin's creation-time decision: no event write
// (settings, schedule, publication) ever changes it afterwards.
func TestEventWritesNeverChangeInfrastructureFlag(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventRepo.New(db.Queries)
	created := mustCreateEvent(t, repo, "infraflag", "Infra Flag", evNow, evNow.AddDate(0, 1, 0), uuid.Nil, evNow)
	if created.InfrastructureAllowed {
		t.Fatal("seed event must start without infrastructure")
	}
	changed := created
	changed.AllowInfrastructure(true)
	changed.UpdatedAt = evNow.Add(time.Minute)
	if affected, err := repo.Update(ctx, changed, created.UpdatedAt); err != nil || affected != 1 {
		t.Fatalf("update: affected=%d err=%v", affected, err)
	}
	current, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.AllowInfrastructure(true)
	next := current
	next.UpdatedAt = evNow.Add(2 * time.Minute)
	if affected, updateErr := repo.UpdateLifecycle(ctx, next, current.UpdatedAt); updateErr != nil || affected != 1 {
		t.Fatalf("publish: affected=%d err=%v", affected, updateErr)
	}
	stored, err := repo.GetByID(ctx, created.ID)
	if err != nil || stored.InfrastructureAllowed {
		t.Fatalf("infrastructure flag changed after creation: %+v err=%v", stored.InfrastructureAllowed, err)
	}
}
