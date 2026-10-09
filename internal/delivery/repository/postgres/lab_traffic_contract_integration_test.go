package postgres_test

import (
	"context"
	"math"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labTrafficRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	labTraffic "github.com/cybericebox/daemon/internal/model/labTraffic"
	"github.com/cybericebox/daemon/internal/testhelpers"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
)

func TestLabTrafficContract_LabOnlyHasNullableParticipantDates(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	f := ltSeedBound(t, db, "ctnull")
	ctx := context.Background()
	repo := labTrafficRepo.New(db.Queries)
	touch := labTraffic.Touch{EventID: f.event, TeamID: f.team, UserID: f.user, EventChallengeID: f.challenge,
		Surface: labTraffic.SurfaceVPN, LabInitiatedAttempts: 2, PacketsOut: 3, PacketsIn: 4, BytesOut: 100, BytesIn: 200}
	report := f.report("wire-boot", anStart, anStart.Add(time.Hour))
	report.Ledger = []*labpb.TrafficTouch{{Subject: labBindingModel.ParticipantClientName(f.user), LabName: f.lab,
		LabInitiatedAttempts: 2, PacketsOut: 3, PacketsIn: 4, BytesOut: 100, BytesIn: 200}}
	wire, err := proto.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded labpb.TrafficReport
	if err := proto.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := newIngest(db).ApplyTraffic(ctx, []*labpb.TrafficReport{&decoded}); err != nil {
		t.Fatal(err)
	}
	var noFirst, noLast, noResponse bool
	if err := db.Pool.QueryRow(ctx, `SELECT first_seen_at IS NULL, last_seen_at IS NULL, first_responded_at IS NULL
FROM event_lab_touches WHERE event_id=$1`, f.event).Scan(&noFirst, &noLast, &noResponse); err != nil {
		t.Fatal(err)
	}
	if !noFirst || !noLast || !noResponse {
		t.Fatalf("lab-only dates must be NULL: first=%v last=%v response=%v", noFirst, noLast, noResponse)
	}
	// A restart/replay neither invents dates nor adds cumulative counters.
	if err := labTrafficRepo.New(db.Queries).ApplyTouch(ctx, touch); err != nil {
		t.Fatal(err)
	}
	var attempts, labInitiated, out, in, bytesOut, bytesIn int64
	if err := db.Pool.QueryRow(ctx, `SELECT attempts_count,lab_initiated_attempts_count,packets_out,packets_in,bytes_out,bytes_in
FROM event_lab_touches WHERE event_id=$1`, f.event).Scan(&attempts, &labInitiated, &out, &in, &bytesOut, &bytesIn); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || labInitiated != 2 || out != 3 || in != 4 || bytesOut != 100 || bytesIn != 200 {
		t.Fatalf("cumulative facts changed: %d %d %d %d %d %d", attempts, labInitiated, out, in, bytesOut, bytesIn)
	}
	usage, err := eventAnalyticsRepo.New(db.Queries).UsageTouches(ctx, f.event)
	if err != nil || len(usage) != 1 || usage[0].LabInitiatedAttempts != 2 || !usage[0].FirstSeenAt.IsZero() || !usage[0].LastSeenAt.IsZero() {
		t.Fatalf("usage nullable mapping: %+v err=%v", usage, err)
	}
	users, err := eventAnalyticsRepo.New(db.Queries).UsageUsers(ctx, f.event, nil)
	if err != nil || len(users) != 2 || users[0].LastLabAt != nil || users[1].LastLabAt != nil {
		t.Fatalf("lab-only traffic invented last user activity: %+v err=%v", users, err)
	}
	activity, err := db.Queries.ListEventAnalyticsTeamActivity(ctx, postgres.ListEventAnalyticsTeamActivityParams{EventID: f.event, AsOf: anStart.Add(time.Hour)})
	if err != nil || len(activity) != 1 || activity[0].LastActivityAt.Unix() != 0 {
		t.Fatalf("lab-only traffic invented ranked-team activity: %+v err=%v", activity, err)
	}
	span := labTraffic.Coverage{From: anStart, To: anStart.Add(time.Hour), Explicit: true}
	if err := repo.RecordCoverage(ctx, f.event, f.team, labTraffic.SurfaceVPN, "collector", "boot", span); err != nil {
		t.Fatal(err)
	}
	q := labTraffic.Question{EventID: f.event, TeamID: f.team, UserID: f.user, EventChallengeID: f.challenge, Before: span.To}
	answer, err := repo.Ask(ctx, q)
	if err != nil || answer.Verdict != labTraffic.Untouched || answer.Attempted || answer.FirstSeenAt != nil {
		t.Fatalf("lab-only traffic classified as participant access: %+v err=%v", answer, err)
	}
}

func TestLabTrafficContract_TwoUserCountersSaturateWithoutQueryFailure(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	f := ltSeedBound(t, db, "ctsat")
	ctx := context.Background()
	repo := labTrafficRepo.New(db.Queries)
	for _, user := range []labTraffic.Touch{
		{UserID: f.user}, {UserID: f.other},
	} {
		user.EventID, user.TeamID, user.EventChallengeID = f.event, f.team, f.challenge
		user.Surface = labTraffic.SurfaceVPN
		user.Attempts, user.LabInitiatedAttempts = math.MaxInt64, math.MaxInt64
		user.PacketsOut, user.PacketsIn, user.BytesOut, user.BytesIn = math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64
		user.FirstSeenAt, user.LastSeenAt = anStart, anStart.Add(time.Minute)
		respond := anStart.Add(time.Second)
		user.FirstRespondAt = &respond
		if err := repo.ApplyTouch(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.Queries.SummarizeLabTouches(ctx, postgres.SummarizeLabTouchesParams{EventID: f.event, TeamID: f.team, EventChallengeID: f.challenge, Before: anStart.Add(time.Hour)})
	if err != nil || len(rows) != 1 {
		t.Fatalf("maximal legitimate counters must aggregate: %+v err=%v", rows, err)
	}
	r := rows[0]
	if r.Attempts != math.MaxInt64 || r.LabInitiatedAttempts != math.MaxInt64 || r.PacketsOut != math.MaxInt64 || r.PacketsIn != math.MaxInt64 || r.BytesOut != math.MaxInt64 || r.BytesIn != math.MaxInt64 {
		t.Fatalf("aggregate counters did not saturate: %+v", r)
	}
	answer, err := repo.Ask(ctx, labTraffic.Question{EventID: f.event, TeamID: f.team, EventChallengeID: f.challenge, Before: anStart.Add(time.Hour)})
	if err != nil || answer.Verdict != labTraffic.Touched || answer.BytesIn != math.MaxInt64 {
		t.Fatalf("maximal team observation: %+v err=%v", answer, err)
	}
}

func TestLabTrafficContract_NullReplayPreservesParticipantMinMax(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	f := ltSeedBound(t, db, "cttimes")
	ctx := context.Background()
	repo := labTrafficRepo.New(db.Queries)
	touch := labTraffic.Touch{EventID: f.event, TeamID: f.team, UserID: f.user, EventChallengeID: f.challenge, Surface: labTraffic.SurfaceVPN,
		Attempts: 2, LabInitiatedAttempts: 4, FirstSeenAt: anStart.Add(time.Minute), LastSeenAt: anStart.Add(2 * time.Minute), PacketsOut: 4, PacketsIn: 3, BytesOut: 200, BytesIn: 100}
	respond := anStart.Add(time.Minute + time.Second)
	touch.FirstRespondAt = &respond
	if err := repo.ApplyTouch(ctx, touch); err != nil {
		t.Fatal(err)
	}
	stale := touch
	stale.Attempts, stale.LabInitiatedAttempts, stale.PacketsOut, stale.PacketsIn, stale.BytesOut, stale.BytesIn = 1, 2, 1, 1, 1, 1
	stale.FirstSeenAt, stale.LastSeenAt = time.Time{}, time.Time{}
	stale.FirstRespondAt = nil
	if err := repo.ApplyTouch(ctx, stale); err != nil {
		t.Fatal(err)
	}
	rows := ltTouches(t, db, f.event)
	if len(rows) != 1 || !rows[0].First.Equal(touch.FirstSeenAt) || !rows[0].Last.Equal(touch.LastSeenAt) || rows[0].Responded == nil || !rows[0].Responded.Equal(respond) || rows[0].Attempts != 2 || rows[0].BytesIn != 100 {
		t.Fatalf("missing/stale metadata erased participant facts: %+v", rows)
	}
	later := touch
	later.FirstSeenAt, later.LastSeenAt = anStart, anStart.Add(3*time.Minute)
	if err := repo.ApplyTouch(ctx, later); err != nil {
		t.Fatal(err)
	}
	rows = ltTouches(t, db, f.event)
	if !rows[0].First.Equal(anStart) || !rows[0].Last.Equal(later.LastSeenAt) {
		t.Fatalf("nullable min/max changed: %+v", rows)
	}
}

func TestLabTrafficContract_LabOnlyTeammateDoesNotSupplyResponseEvidence(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	f := ltSeedBound(t, db, "ctmix")
	ctx := context.Background()
	repo := labTrafficRepo.New(db.Queries)
	base := labTraffic.Touch{EventID: f.event, TeamID: f.team, UserID: f.user, EventChallengeID: f.challenge, Surface: labTraffic.SurfaceVPN}
	participant := base
	participant.Attempts, participant.FirstSeenAt, participant.LastSeenAt = 1, anStart, anStart
	labOnly := base
	labOnly.UserID, labOnly.LabInitiatedAttempts, labOnly.PacketsOut, labOnly.PacketsIn, labOnly.BytesIn = f.other, 2, 3, 4, 100
	for _, touch := range []labTraffic.Touch{participant, labOnly} {
		if err := repo.ApplyTouch(ctx, touch); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.RecordCoverage(ctx, f.event, f.team, labTraffic.SurfaceVPN, "collector", "boot", labTraffic.Coverage{From: anStart, To: anStart.Add(time.Hour), Explicit: true}); err != nil {
		t.Fatal(err)
	}
	q := labTraffic.Question{EventID: f.event, TeamID: f.team, EventChallengeID: f.challenge, Before: anStart.Add(time.Hour)}
	answer, err := repo.Ask(ctx, q)
	if err != nil || answer.Verdict != labTraffic.Untouched || !answer.Attempted || answer.FirstSeenAt == nil || !answer.FirstSeenAt.Equal(anStart) {
		t.Fatalf("lab-only teammate packets supplied participant evidence: %+v err=%v", answer, err)
	}
	participant.PacketsIn = 1
	if err := repo.ApplyTouch(ctx, participant); err != nil {
		t.Fatal(err)
	}
	answer, err = repo.Ask(ctx, q)
	if err != nil || answer.Verdict != labTraffic.Unknown {
		t.Fatalf("participant packet without response was ignored: %+v err=%v", answer, err)
	}
}

func TestLabTrafficContract_Migration159RoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	f := ltSeedBound(t, db, "ctmigrate")
	ctx := context.Background()
	repo := labTrafficRepo.New(db.Queries)
	for _, touch := range []labTraffic.Touch{
		{EventID: f.event, TeamID: f.team, UserID: f.user, EventChallengeID: f.challenge, Surface: labTraffic.SurfaceVPN, Attempts: 1, FirstSeenAt: anStart, LastSeenAt: anStart.Add(time.Minute)},
		{EventID: f.event, TeamID: f.team, UserID: f.other, EventChallengeID: f.challenge, Surface: labTraffic.SurfaceVPN, LabInitiatedAttempts: 2, PacketsIn: 3},
	} {
		if err := repo.ApplyTouch(ctx, touch); err != nil {
			t.Fatal(err)
		}
	}
	down, err := os.ReadFile("migrations/0159_lab_traffic_contract.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, string(down)); err != nil {
		t.Fatal(err)
	}
	var notNull int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name='event_lab_touches' AND column_name IN ('first_seen_at','last_seen_at') AND is_nullable='NO'`).Scan(&notNull); err != nil {
		t.Fatal(err)
	}
	if notNull != 2 {
		t.Fatalf("legacy NOT NULL not restored: %d", notNull)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM information_schema.columns WHERE (table_name='event_lab_touches' AND column_name='lab_initiated_attempts_count') OR (table_name='lab_traffic_coverage' AND column_name='explicit')`); n != 0 {
		t.Fatalf("new fields survived down migration: %d", n)
	}
	var first, last time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT first_seen_at,last_seen_at FROM event_lab_touches WHERE user_id=$1`, f.user).Scan(&first, &last); err != nil {
		t.Fatal(err)
	}
	if !first.Equal(anStart) || !last.Equal(anStart.Add(time.Minute)) {
		t.Fatalf("legacy timestamps changed: %v %v", first, last)
	}
	up, err := os.ReadFile("migrations/0159_lab_traffic_contract.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, string(up)); err != nil {
		t.Fatal(err)
	}
	var noFirst, noLast bool
	if err := db.Pool.QueryRow(ctx, `SELECT first_seen_at IS NULL,last_seen_at IS NULL FROM event_lab_touches WHERE user_id=$1`, f.other).Scan(&noFirst, &noLast); err != nil {
		t.Fatal(err)
	}
	if !noFirst || !noLast {
		t.Fatal("forward migration left rollback sentinel as participant activity")
	}
}

func TestLabTrafficContract_ExplicitGapAndPartialReplica(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	f := ltSeedBound(t, db, "ctcov")
	ctx := context.Background()
	repo := labTrafficRepo.New(db.Queries)
	end := anStart.Add(time.Hour)
	for _, span := range []labTraffic.Coverage{
		{From: anStart, To: anStart.Add(time.Minute), Explicit: true},
		{From: anStart.Add(time.Minute + time.Second), To: end, Explicit: true},
	} {
		if err := repo.RecordCoverage(ctx, f.event, f.team, labTraffic.SurfaceVPN, "collector", "boot", span); err != nil {
			t.Fatal(err)
		}
	}
	if n := rtCount(t, db, `SELECT count(*) FROM lab_traffic_coverage`); n != 2 {
		t.Fatalf("one-second real downtime merged: segments=%d", n)
	}
	q := labTraffic.Question{EventID: f.event, TeamID: f.team, EventChallengeID: f.challenge, Before: end}
	answer, err := repo.Ask(ctx, q)
	if err != nil || answer.Verdict != labTraffic.Unknown {
		t.Fatalf("one-second gap: %+v err=%v", answer, err)
	}
	if err := repo.RecordCoverage(ctx, f.event, f.team, labTraffic.SurfaceVPN, "healthy-replica", "other-boot", labTraffic.Coverage{From: anStart, To: end, Explicit: true}); err != nil {
		t.Fatal(err)
	}
	answer, err = repo.Ask(ctx, q)
	if err != nil || answer.Verdict != labTraffic.Untouched {
		t.Fatalf("complete healthy replica: %+v err=%v", answer, err)
	}
	if err := repo.RecordCoverage(ctx, f.event, f.team, labTraffic.SurfaceVPN, "partial-replica", "third-boot", labTraffic.Coverage{From: anStart.Add(10 * time.Minute), To: end, Explicit: true, Partial: true}); err != nil {
		t.Fatal(err)
	}
	answer, err = repo.Ask(ctx, q)
	if err != nil || answer.Verdict != labTraffic.Unknown {
		t.Fatalf("partial ledger hidden by healthy replica: %+v err=%v", answer, err)
	}
}
