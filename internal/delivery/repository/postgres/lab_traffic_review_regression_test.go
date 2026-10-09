package postgres_test

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/labTrafficRepo"
	labTraffic "github.com/cybericebox/daemon/internal/model/labTraffic"
	"github.com/cybericebox/daemon/internal/testhelpers"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"testing"
	"time"
)

func TestTrafficInvalidCoverageSiblingKeepsUnknown(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	f := ltSeedBound(t, db, "reviewbad")
	r := f.report("boot", anStart, anStart.Add(time.Hour))
	r.Ledger = nil
	r.CoveredFromUnixMs, r.CoveredToUnixMs = 0, 0
	r.CoverageSpans = []*labpb.TrafficCoverageSpan{
		{FromUnixMs: anStart.UnixMilli(), ToUnixMs: anStart.Add(time.Hour).UnixMilli()},
		{FromUnixMs: anStart.Add(time.Hour).UnixMilli(), ToUnixMs: anStart.UnixMilli()},
	}
	if err := newIngest(db).ApplyTraffic(context.Background(), []*labpb.TrafficReport{r}); err != nil {
		t.Fatal(err)
	}
	answer, err := labTrafficRepo.New(db.Queries).Ask(context.Background(), labTraffic.Question{EventID: f.event, TeamID: f.team, EventChallengeID: f.challenge, Before: anStart.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if answer.Verdict != labTraffic.Unknown {
		t.Fatalf("invalid sibling must block absence: %+v", answer)
	}
}

func TestTrafficTeamMissingDatesKeepUnknown(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	f := ltSeedBound(t, db, "reviewnull")
	repo := labTrafficRepo.New(db.Queries)
	ctx := context.Background()
	for _, touch := range []labTraffic.Touch{
		{EventID: f.event, TeamID: f.team, UserID: f.user, EventChallengeID: f.challenge, Surface: labTraffic.SurfaceVPN, Attempts: 1, FirstSeenAt: anStart, LastSeenAt: anStart},
		{EventID: f.event, TeamID: f.team, UserID: f.other, EventChallengeID: f.challenge, Surface: labTraffic.SurfaceVPN, Attempts: 1},
	} {
		if err := repo.ApplyTouch(ctx, touch); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.RecordCoverage(ctx, f.event, f.team, labTraffic.SurfaceVPN, "collector", "boot", labTraffic.Coverage{From: anStart, To: anStart.Add(time.Hour), Explicit: true}); err != nil {
		t.Fatal(err)
	}
	q := labTraffic.Question{EventID: f.event, TeamID: f.team, EventChallengeID: f.challenge, Before: anStart.Add(time.Hour)}
	q.UserID = f.other
	individual, err := repo.Ask(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if individual.Verdict != labTraffic.Unknown {
		t.Fatalf("expected incomplete individual: %+v", individual)
	}
	q.UserID = [16]byte{}
	team, err := repo.Ask(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if team.Verdict != labTraffic.Unknown {
		t.Fatalf("team aggregation erased incomplete individual: %+v", team)
	}
}
