package postgres_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

var paiBase = time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

// paiTransition inserts one status log row (the triggers write the same
// shape).
func paiTransition(t *testing.T, db *testhelpers.TestDB, event, team uuid.UUID, source string, to int16, reason string, at time.Time) {
	t.Helper()
	rtExec(t, db, `INSERT INTO event_stand_transitions (event_id, team_id, source, generation, to_status, reason, at) VALUES ($1, $2, $3, 0, $4, $5, $6)`,
		event, team, source, to, reason, at)
}

func paiCapacity(t *testing.T, db *testhelpers.TestDB, agent string, seq int64, at time.Time, payload string) {
	t.Helper()
	rtExec(t, db, `INSERT INTO platform_lab_capacity_observations (id, agent_id, sequence, observed_at, received_at, schema_version, snapshot, payload) VALUES ($1, $2, $3, $4, $4, 1, true, $5::jsonb)`,
		uuid.Must(uuid.NewV7()), agent, seq, at, payload)
}

func paiSeedInfra(t *testing.T, db *testhelpers.TestDB) (eventA, eventB uuid.UUID) {
	t.Helper()
	eventA = mustSeedEventForParticipants(t, db, "paia").ID
	eventB = mustSeedEventForParticipants(t, db, "paib").ID
	t1, t2, t3, t4 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	// Event A: t1 active 01:00-05:00 (creating, ready, removed), t2 active
	// 03:00-04:00, t4 becomes active at 23:00 of the last day and never ends.
	paiTransition(t, db, eventA, t1, "stand", 1, "", paiBase.Add(1*time.Hour))
	paiTransition(t, db, eventA, t1, "stand", 2, "", paiBase.Add(2*time.Hour))
	paiTransition(t, db, eventA, t1, "stand", 4, "", paiBase.Add(5*time.Hour))
	paiTransition(t, db, eventA, t2, "stand", 2, "", paiBase.Add(3*time.Hour))
	paiTransition(t, db, eventA, t2, "stand", 4, "", paiBase.Add(4*time.Hour))
	paiTransition(t, db, eventA, t4, "stand", 1, "", paiBase.Add(47*time.Hour))
	// Event B: t3 was ready before the period and is removed at 01:30.
	paiTransition(t, db, eventB, t3, "stand", 2, "", paiBase.Add(-time.Hour))
	paiTransition(t, db, eventB, t3, "stand", 4, "", paiBase.Add(90*time.Minute))
	// Failures: two lab failures, one stand failure, a lab that became ready
	// (not a failure) and a failure before the period.
	paiTransition(t, db, eventA, t1, "lab", 2, "ImagePullBackOff: web", paiBase.Add(2*time.Hour))
	paiTransition(t, db, eventA, t2, "lab", 2, "Not ready after 20m0s; devices not ready: db", paiBase.Add(3*time.Hour))
	paiTransition(t, db, eventA, t1, "lab", 1, "", paiBase.Add(6*time.Hour))
	paiTransition(t, db, eventA, t1, "stand", 3, "ImagePullBackOff: web", paiBase.Add(7*time.Hour))
	paiTransition(t, db, eventB, t3, "lab", 2, "Deploy failed: rpc error", paiBase.Add(-24*time.Hour))
	return eventA, eventB
}

// Stand-hours sum the active time (creating + ready) per event inside the
// period, clip at the period bounds and at now, and rank the events.
func TestPlatformAnalytics_InfraStandHours(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := platformAnalyticsRepo.New(db.Queries)
	eventA, eventB := paiSeedInfra(t, db)
	now := paiBase.AddDate(0, 0, 10)

	got, err := repo.InfraStandHours(ctx, paiBase, paiBase.Add(48*time.Hour), now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Top) != 2 || got.TotalEvents != 2 {
		t.Fatalf("report = %+v", got)
	}
	if a := got.Top[0]; a.EventID != eventA || a.Stands != 3 || math.Abs(a.Hours-6) > 1e-6 {
		t.Fatalf("event A = %+v, want 6 h over 3 stands (4 + 1 + 1)", a)
	}
	if b := got.Top[1]; b.EventID != eventB || b.Stands != 1 || math.Abs(b.Hours-1.5) > 1e-6 {
		t.Fatalf("event B = %+v, want 1.5 h (clipped at the period start)", b)
	}
	if math.Abs(got.TotalHours-7.5) > 1e-6 {
		t.Fatalf("total = %v, want 7.5", got.TotalHours)
	}

	// The limit cuts the ranking, not the totals.
	top1, err := repo.InfraStandHours(ctx, paiBase, paiBase.Add(48*time.Hour), now, 1)
	if err != nil || len(top1.Top) != 1 || top1.TotalEvents != 2 || math.Abs(top1.TotalHours-7.5) > 1e-6 {
		t.Fatalf("top 1 = %+v err=%v", top1, err)
	}

	// An open stand stops counting at now: t4 has been active since 23:00.
	early, err := repo.InfraStandHours(ctx, paiBase.Add(46*time.Hour), paiBase.Add(48*time.Hour), paiBase.Add(47*time.Hour+30*time.Minute), 10)
	if err != nil || len(early.Top) != 1 || math.Abs(early.Top[0].Hours-0.5) > 1e-6 {
		t.Fatalf("clipped at now = %+v err=%v", early, err)
	}

	// Empty period: no rows, no error.
	empty, err := repo.InfraStandHours(ctx, paiBase.AddDate(0, -3, 0), paiBase.AddDate(0, -3, 1), now, 10)
	if err != nil || len(empty.Top) != 0 || empty.TotalHours != 0 {
		t.Fatalf("empty = %+v err=%v", empty, err)
	}
}

// Peaks are the most stands active at once per bucket; buckets are dense,
// the level carries over a bucket start and ends leave before a bucket begins.
func TestPlatformAnalytics_InfraPeaks(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := platformAnalyticsRepo.New(db.Queries)
	paiSeedInfra(t, db)
	now := paiBase.AddDate(0, 0, 10)

	hourly, err := repo.InfraPeaks(ctx, paiBase, paiBase.Add(48*time.Hour), now, "hour")
	if err != nil {
		t.Fatal(err)
	}
	if len(hourly) != 48 {
		t.Fatalf("hourly buckets = %d, want 48", len(hourly))
	}
	want := map[int]int64{0: 1, 1: 2, 2: 1, 3: 2, 4: 1, 5: 0, 6: 0, 22: 0, 23: 0, 47: 1}
	for hour, peak := range want {
		if !hourly[hour].At.Equal(paiBase.Add(time.Duration(hour)*time.Hour)) || hourly[hour].Peak != peak {
			t.Fatalf("hour %d = %+v, want peak %d", hour, hourly[hour], peak)
		}
	}

	daily, err := repo.InfraPeaks(ctx, paiBase, paiBase.Add(48*time.Hour), now, "day")
	if err != nil || len(daily) != 2 || daily[0].Peak != 2 || daily[1].Peak != 1 {
		t.Fatalf("daily = %+v err=%v", daily, err)
	}

	quiet, err := repo.InfraPeaks(ctx, paiBase.AddDate(0, -3, 0), paiBase.AddDate(0, -3, 1), now, "hour")
	if err != nil || len(quiet) != 24 {
		t.Fatalf("quiet = %d buckets err=%v", len(quiet), err)
	}
	for _, p := range quiet {
		if p.Peak != 0 {
			t.Fatalf("quiet bucket %+v", p)
		}
	}
}

// Failures group lab and stand failures of the period by reason code.
func TestPlatformAnalytics_InfraFailures(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	repo := platformAnalyticsRepo.New(db.Queries)
	paiSeedInfra(t, db)

	got, err := repo.InfraFailures(context.Background(), paiBase, paiBase.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	byCode := map[string]platformAnalyticsRepo.InfraFailure{}
	for _, f := range got {
		byCode[f.Code] = f
	}
	if len(got) != 2 {
		t.Fatalf("failures = %+v", got)
	}
	if f := byCode["image_pull"]; f.Labs != 1 || f.Stands != 1 || f.Events != 1 {
		t.Fatalf("image_pull = %+v", f)
	}
	if f := byCode["deploy_timeout"]; f.Labs != 1 || f.Stands != 0 {
		t.Fatalf("deploy_timeout = %+v", f)
	}
	// The failure before the period and the lab becoming ready do not count.
	if _, ok := byCode["deploy_failed"]; ok {
		t.Fatal("a failure before the period was counted")
	}
}

// Capacity: numbers and numeric strings both read, agents summed inside a
// bucket, observations without capacity fields and outside the period left out.
func TestPlatformAnalytics_InfraCapacity(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	repo := platformAnalyticsRepo.New(db.Queries)
	paiCapacity(t, db, "a1", 1, paiBase.Add(10*time.Minute), `{"allocatableCpuMillicores":"4000","requestedCpuMillicores":1000,"allocatableMemoryBytes":"8000","requestedMemoryBytes":"2000"}`)
	paiCapacity(t, db, "a1", 2, paiBase.Add(20*time.Minute), `{"allocatableCpuMillicores":4000,"requestedCpuMillicores":"3000","allocatableMemoryBytes":8000,"requestedMemoryBytes":4000}`)
	paiCapacity(t, db, "a2", 1, paiBase.Add(10*time.Minute), `{"allocatableCpuMillicores":2000,"requestedCpuMillicores":500,"allocatableMemoryBytes":1000,"requestedMemoryBytes":100}`)
	paiCapacity(t, db, "a1", 3, paiBase.Add(30*time.Minute), `{"nodes":[]}`)
	paiCapacity(t, db, "a1", 4, paiBase.Add(2*time.Hour+time.Minute), `{"allocatableCpuMillicores":4000,"requestedCpuMillicores":0,"allocatableMemoryBytes":8000,"requestedMemoryBytes":0}`)
	paiCapacity(t, db, "a1", 5, paiBase.Add(-time.Hour), `{"allocatableCpuMillicores":9999}`)

	got, err := repo.InfraCapacity(context.Background(), paiBase, paiBase.Add(24*time.Hour), 3600)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("points = %+v", got)
	}
	first := got[0]
	if !first.At.Equal(paiBase) || first.Agents != 2 || first.AllocatableCPUMillis != 6000 || first.RequestedCPUMillis != 2500 ||
		first.AllocatableMemory != 9000 || first.RequestedMemory != 3100 {
		t.Fatalf("first bucket = %+v", first)
	}
	if !got[1].At.Equal(paiBase.Add(2*time.Hour)) || got[1].Agents != 1 || got[1].AllocatableCPUMillis != 4000 {
		t.Fatalf("second bucket = %+v", got[1])
	}
}

// The live counts reuse the admin summary statement.
func TestPlatformAnalytics_InfraStandCounts(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	repo := platformAnalyticsRepo.New(db.Queries)
	got, err := repo.InfraStandCounts(context.Background())
	if err != nil || got != (platformAnalyticsRepo.InfraStandCounts{}) {
		t.Fatalf("counts = %+v err=%v", got, err)
	}
}
