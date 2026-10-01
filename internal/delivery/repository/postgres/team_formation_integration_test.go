package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The database and Go build the same LabGroup name, and it fits a Kubernetes label.
func TestLabGroupNameMatchesGo(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV4())
		var got string
		if err := db.Pool.QueryRow(ctx, `SELECT lab_group_name($1, $2)`, eventID, teamID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		want, err := labBindingModel.GroupName(eventID, teamID)
		if err != nil || got != want || len(got) > 63 {
			t.Fatalf("sql %q go %q (%d) err=%v", got, want, len(got), err)
		}
	}
	var edge string
	if err := db.Pool.QueryRow(ctx, `SELECT lab_short_id('ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid)`).Scan(&edge); err != nil {
		t.Fatal(err)
	}
	if want := labBindingModel.ShortID(uuid.FromStringOrNil("ffffffff-ffff-ffff-ffff-ffffffffffff")); edge != want {
		t.Fatalf("max uuid: sql %q go %q", edge, want)
	}
}

// A stored formation always forms; without one, an unknown event does not.
func TestEventTeamFormedRule(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	var formed, open bool
	if err := db.Pool.QueryRow(ctx, `SELECT event_team_formed($1, $2), event_team_formed($1, NULL)`, uuid.Must(uuid.NewV7()), time.Now()).Scan(&formed, &open); err != nil {
		t.Fatal(err)
	}
	if !formed || open {
		t.Fatalf("formed=%v open=%v", formed, open)
	}
}
