package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/labTrafficRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/retentionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	labTraffic "github.com/cybericebox/daemon/internal/model/labTraffic"
	labMonitoring "github.com/cybericebox/daemon/internal/monitoring/lab"
	"github.com/cybericebox/daemon/internal/testhelpers"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
)

type ltFixture struct {
	anFixture
	group, lab string
	other      uuid.UUID // a second member of the team
}

func ltSeed(t *testing.T, db *testhelpers.TestDB, tag string) ltFixture {
	t.Helper()
	f := anSeed(t, db, tag)
	group, err := labBindingModel.GroupName(f.event, f.team)
	if err != nil {
		t.Fatal(err)
	}
	other := mustSeedUser(t, db, tag+"-other@test.test")
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, team_id, team_role) VALUES ($1, $2, 2, $3, $4, 1)`, f.event, other, anStart, f.team)
	return ltFixture{anFixture: f, group: group, lab: labBindingModel.LabName(f.challenge, 0), other: other}
}

func ms(t time.Time) int64 { return t.UnixMilli() }

type ltRow struct {
	subject   string
	lab       string
	attempts  int64
	first     time.Time
	last      time.Time
	responded time.Time // zero = never
	bytesIn   int64
}

func (f ltFixture) report(boot string, coveredFrom, coveredTo time.Time, rows ...ltRow) *labpb.TrafficReport {
	report := &labpb.TrafficReport{
		LabGroupName: f.group, Namespace: "labgroup-x", Source: "vpn", Kind: "vpn", Instance: "vpn-1", BootId: boot,
		CoveredFromUnixMs: ms(coveredFrom), CoveredToUnixMs: ms(coveredTo),
	}
	for _, r := range rows {
		lab := r.lab
		if lab == "" {
			lab = f.lab
		}
		touch := &labpb.TrafficTouch{
			Subject: r.subject, LabName: lab, Attempts: r.attempts,
			BytesIn: r.bytesIn, FirstSeenUnixMs: ms(r.first), LastSeenUnixMs: ms(r.last),
		}
		if !r.responded.IsZero() {
			touch.FirstRespondedUnixMs = ms(r.responded)
		}
		report.Ledger = append(report.Ledger, touch)
	}
	return report
}

type ltTouch struct {
	Attempts   int64
	First      time.Time
	Last       time.Time
	Responded  *time.Time
	BytesIn    int64
	UserID     uuid.NullUUID
	ChallengeI uuid.UUID
}

func ltTouches(t *testing.T, db *testhelpers.TestDB, event uuid.UUID) []ltTouch {
	t.Helper()
	rows, err := db.Pool.Query(context.Background(), `
SELECT attempts_count, first_seen_at, last_seen_at, first_responded_at, bytes_in, user_id, event_challenge_id
FROM event_lab_touches WHERE event_id = $1 ORDER BY first_seen_at, user_id`, event)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []ltTouch
	for rows.Next() {
		var r ltTouch
		if err := rows.Scan(&r.Attempts, &r.First, &r.Last, &r.Responded, &r.BytesIn, &r.UserID, &r.ChallengeI); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func newIngest(db *testhelpers.TestDB) *labMonitoring.TrafficIngest {
	return labMonitoring.NewTrafficIngest(labTrafficRepo.New(db.Queries))
}

// The aggregate keeps min(first), max(last) and the sum of the growth, and
// redelivery of the same cumulative state (a reconnect always re-sends it, also
// to a restarted daemon) never counts anything twice.
func TestLabTraffic_UpsertIsMinMaxSumAndIdempotent(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := ltSeed(t, db, "ltupsert")
	client := labBindingModel.ParticipantClientName(f.user)
	first, responded := anStart.Add(1*time.Minute), anStart.Add(2*time.Minute)

	ingest := newIngest(db)
	step1 := f.report("boot-1", anStart, anStart.Add(5*time.Minute),
		ltRow{subject: client, attempts: 3, first: first, last: anStart.Add(5 * time.Minute), responded: responded, bytesIn: 100})
	if err := ingest.ApplyTraffic(ctx, []*labpb.TrafficReport{step1}); err != nil {
		t.Fatal(err)
	}
	// The same report again, through the same and through a fresh (restarted) ingest.
	for _, in := range []*labMonitoring.TrafficIngest{ingest, newIngest(db)} {
		if err := in.ApplyTraffic(ctx, []*labpb.TrafficReport{step1}); err != nil {
			t.Fatal(err)
		}
	}
	rows := ltTouches(t, db, f.event)
	if len(rows) != 1 || rows[0].Attempts != 3 || rows[0].BytesIn != 100 {
		t.Fatalf("after redelivery: %+v", rows)
	}

	// The counters grow inside the same boot.
	step2 := f.report("boot-1", anStart, anStart.Add(20*time.Minute),
		ltRow{subject: client, attempts: 5, first: first, last: anStart.Add(20 * time.Minute), responded: responded, bytesIn: 250})
	if err := newIngest(db).ApplyTraffic(ctx, []*labpb.TrafficReport{step2}); err != nil {
		t.Fatal(err)
	}
	rows = ltTouches(t, db, f.event)
	if len(rows) != 1 || rows[0].Attempts != 5 || rows[0].BytesIn != 250 || !rows[0].Last.Equal(anStart.Add(20*time.Minute)) || !rows[0].First.Equal(first) {
		t.Fatalf("after growth: %+v", rows)
	}

	// A stale or restarted report with lower totals, an earlier first attempt and
	// a later reply: totals never go down, first/last are min/max, the earliest
	// answer stays.
	step3 := f.report("boot-2", anStart.Add(30*time.Minute), anStart.Add(40*time.Minute),
		ltRow{subject: client, attempts: 2, first: anStart.Add(-time.Minute), last: anStart.Add(40 * time.Minute), responded: anStart.Add(41 * time.Minute), bytesIn: 40})
	if err := newIngest(db).ApplyTraffic(ctx, []*labpb.TrafficReport{step3}); err != nil {
		t.Fatal(err)
	}
	rows = ltTouches(t, db, f.event)
	if len(rows) != 1 || rows[0].Attempts != 5 || rows[0].BytesIn != 250 {
		t.Fatalf("after a stale report: %+v", rows)
	}
	if !rows[0].First.Equal(anStart.Add(-time.Minute)) || !rows[0].Last.Equal(anStart.Add(40*time.Minute)) {
		t.Fatalf("first/last must be min/max: %+v", rows[0])
	}
	if rows[0].Responded == nil || !rows[0].Responded.Equal(responded) {
		t.Fatalf("the first response must stay the earliest: %+v", rows[0].Responded)
	}
}

func TestLabTraffic_DropsWhatItCannotAttribute(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := ltSeed(t, db, "ltdrop")
	outsider := uuid.Must(uuid.NewV7())
	at := anStart.Add(time.Minute)

	report := f.report("boot-1", anStart, anStart.Add(5*time.Minute),
		ltRow{subject: "tester", attempts: 1, first: at, last: at},                                                                      // the shared test client
		ltRow{subject: labBindingModel.ParticipantClientName(outsider), attempts: 1, first: at, last: at},                               // not in the team
		ltRow{subject: labBindingModel.ParticipantClientName(f.user), lab: "not-a-lab", attempts: 1, first: at, last: at},               // unresolvable lab
		ltRow{subject: labBindingModel.ParticipantClientName(f.user), attempts: 0, first: at, last: at},                                 // nothing happened
		ltRow{subject: labBindingModel.ParticipantClientName(f.user), attempts: 1, first: at, last: at, responded: at.Add(time.Second)}, // the only valid row
	)
	stranger := &labpb.TrafficReport{LabGroupName: "test-deploy-1", Kind: "vpn", Source: "vpn", BootId: "b", CoveredToUnixMs: ms(at), Ledger: report.Ledger}
	if err := newIngest(db).ApplyTraffic(ctx, []*labpb.TrafficReport{report, stranger}); err != nil {
		t.Fatal(err)
	}
	rows := ltTouches(t, db, f.event)
	if len(rows) != 1 || rows[0].UserID.UUID != f.user {
		t.Fatalf("only the attributable row may be stored: %+v", rows)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM event_lab_touches`); n != 1 {
		t.Fatalf("rows in the whole table = %d", n)
	}
}

func TestLabTraffic_UsersOfATeamAreSeparateRows(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	f := ltSeed(t, db, "ltusers")
	at := anStart.Add(time.Minute)
	report := f.report("boot-1", anStart, anStart.Add(5*time.Minute),
		ltRow{subject: labBindingModel.ParticipantClientName(f.user), attempts: 2, first: at, last: at},
		ltRow{subject: labBindingModel.ParticipantClientName(f.other), attempts: 4, first: at, last: at.Add(time.Minute)},
	)
	if err := newIngest(db).ApplyTraffic(context.Background(), []*labpb.TrafficReport{report}); err != nil {
		t.Fatal(err)
	}
	rows := ltTouches(t, db, f.event)
	if len(rows) != 2 || rows[0].Attempts+rows[1].Attempts != 6 {
		t.Fatalf("rows: %+v", rows)
	}
}

func TestLabTraffic_CoverageExtendsAndSplitsOnAGap(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := ltSeed(t, db, "ltcover")
	// Idle reports (no ledger) still extend the covered span.
	for _, to := range []int{1, 2, 3} {
		r := f.report("boot-1", anStart, anStart.Add(time.Duration(to)*time.Minute))
		if err := newIngest(db).ApplyTraffic(ctx, []*labpb.TrafficReport{r}); err != nil {
			t.Fatal(err)
		}
	}
	if n := rtCount(t, db, `SELECT count(*) FROM lab_traffic_coverage WHERE event_id = $1`, f.event); n != 1 {
		t.Fatalf("contiguous reports must share one segment, got %d", n)
	}
	// The collector was blind for 20 minutes: its report restarts the span.
	gap := f.report("boot-1", anStart.Add(23*time.Minute), anStart.Add(24*time.Minute))
	if err := newIngest(db).ApplyTraffic(ctx, []*labpb.TrafficReport{gap}); err != nil {
		t.Fatal(err)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM lab_traffic_coverage WHERE event_id = $1`, f.event); n != 2 {
		t.Fatalf("a gap must open a new segment, got %d", n)
	}
	// A report for a group that is not an event team's stores nothing.
	stray := &labpb.TrafficReport{LabGroupName: "e-nope", Kind: "vpn", Source: "vpn", BootId: "b", CoveredToUnixMs: ms(anStart)}
	if err := newIngest(db).ApplyTraffic(ctx, []*labpb.TrafficReport{stray}); err != nil {
		t.Fatal(err)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM lab_traffic_coverage`); n != 2 {
		t.Fatalf("coverage rows = %d", n)
	}
}

func TestLabTraffic_AskIsTriState(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := ltSeed(t, db, "ltask")
	repo := labTrafficRepo.New(db.Queries)
	rtExec(t, db, `INSERT INTO lab_bindings (id, event_id, event_team_id, event_challenge_id, lab_group_name, lab_name, created_at, deployed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`, uuid.Must(uuid.NewV7()), f.event, f.team, f.challenge, f.group, f.lab, anStart)

	ask := func(user uuid.UUID, before time.Time) labTraffic.Answer {
		t.Helper()
		answer, err := repo.Ask(ctx, labTraffic.Question{EventID: f.event, TeamID: f.team, EventChallengeID: f.challenge, UserID: user, Before: before})
		if err != nil {
			t.Fatal(err)
		}
		return answer
	}
	before := anStart.Add(60 * time.Minute)

	// No data and no coverage: unknown, never «untouched».
	if got := ask(f.user, before); got.Verdict != labTraffic.Unknown {
		t.Fatalf("no data: %s, want unknown", got.Verdict)
	}

	// The collector watched the whole span, nothing reached the lab.
	if err := newIngest(db).ApplyTraffic(ctx, []*labpb.TrafficReport{f.report("boot-1", anStart, anStart.Add(2*time.Hour))}); err != nil {
		t.Fatal(err)
	}
	if got := ask(f.user, before); got.Verdict != labTraffic.Untouched || got.Attempted {
		t.Fatalf("full coverage, no rows: %+v", got)
	}

	// The user only knocked (no reply), the teammate got an answer.
	knock, answered := anStart.Add(10*time.Minute), anStart.Add(20*time.Minute)
	report := f.report("boot-1", anStart, anStart.Add(2*time.Hour),
		ltRow{subject: labBindingModel.ParticipantClientName(f.user), attempts: 3, first: knock, last: knock},
		ltRow{subject: labBindingModel.ParticipantClientName(f.other), attempts: 2, first: answered, last: answered, responded: answered.Add(time.Second), bytesIn: 900},
	)
	if err := newIngest(db).ApplyTraffic(ctx, []*labpb.TrafficReport{report}); err != nil {
		t.Fatal(err)
	}
	if got := ask(f.user, before); got.Verdict != labTraffic.Untouched || !got.Attempted || got.FirstSeenAt == nil || !got.FirstSeenAt.Equal(knock) {
		t.Fatalf("knocked only: %+v", got)
	}
	teammate := ask(f.other, before)
	if teammate.Verdict != labTraffic.Touched || teammate.FirstRespondAt == nil || !teammate.FirstRespondAt.Equal(answered.Add(time.Second)) || teammate.BytesIn != 900 || teammate.Surface != labTraffic.SurfaceVPN {
		t.Fatalf("teammate: %+v", teammate)
	}
	// Team level (nil user): somebody touched it.
	if got := ask(uuid.Nil, before); got.Verdict != labTraffic.Touched {
		t.Fatalf("team level: %+v", got)
	}
	// Asked about a moment before the answer: not touched yet.
	if got := ask(f.other, answered); got.Verdict != labTraffic.Untouched {
		t.Fatalf("before the answer: %+v", got)
	}
	// Another task of the same team is separate.
	other, err := labTrafficRepo.New(db.Queries).Ask(ctx, labTraffic.Question{EventID: f.event, TeamID: f.team, EventChallengeID: uuid.Must(uuid.NewV7()), Before: before, Since: anStart})
	if err != nil || other.Verdict != labTraffic.Untouched {
		t.Fatalf("other task: %+v %v", other, err)
	}
	// A lab that was never deployed cannot be judged.
	undeployed, err := repo.Ask(ctx, labTraffic.Question{EventID: f.event, TeamID: f.team, EventChallengeID: uuid.Must(uuid.NewV7()), Before: before})
	if err != nil || undeployed.Verdict != labTraffic.Unknown {
		t.Fatalf("undeployed: %+v %v", undeployed, err)
	}
}

func TestLabTraffic_RetentionAndAccountDeletion(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := ltSeed(t, db, "ltretention")
	at := anStart.Add(time.Minute)
	report := f.report("boot-1", anStart, anStart.Add(5*time.Minute),
		ltRow{subject: labBindingModel.ParticipantClientName(f.user), attempts: 2, first: at, last: at, responded: at},
		ltRow{subject: labBindingModel.ParticipantClientName(f.other), attempts: 1, first: at, last: at, responded: at},
	)
	if err := newIngest(db).ApplyTraffic(ctx, []*labpb.TrafficReport{report}); err != nil {
		t.Fatal(err)
	}

	// Deleting an account keeps the fact, not the person.
	users := userRepo.New(db.Queries)
	u, err := users.GetByID(ctx, f.other)
	if err != nil {
		t.Fatal(err)
	}
	expected := u.UpdatedAt
	if err = u.SoftDelete(rtNow); err != nil {
		t.Fatal(err)
	}
	if _, err = users.Update(ctx, u, expected); err != nil {
		t.Fatal(err)
	}
	retention := retentionRepo.New(db.Queries)
	if n, err := retention.PurgeDeletedAccounts(ctx, rtNow, 10); err != nil || n != 1 {
		t.Fatalf("PurgeDeletedAccounts: n=%d err=%v", n, err)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM event_lab_touches WHERE user_id = $1`, f.other); n != 0 {
		t.Fatalf("the deleted account is still named on %d rows", n)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM event_lab_touches WHERE user_id IS NULL AND event_id = $1`, f.event); n != 1 {
		t.Fatalf("the anonymised row must stay, got %d", n)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM event_lab_touches WHERE user_id = $1`, f.user); n != 1 {
		t.Fatalf("the active account's row must stay, got %d", n)
	}

	// Event end + 365 days: an event that ended a year ago is purged, a running one is not.
	if n, err := retention.PurgeEventAnalytics(ctx, anStart.Add(-24*time.Hour), 100); err != nil {
		t.Fatalf("early cutoff: n=%d err=%v", n, err)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM event_lab_touches WHERE event_id = $1`, f.event); n != 2 {
		t.Fatalf("an event that has not ended before the cutoff lost its rows: %d", n)
	}
	for i := 0; i < 5; i++ {
		if n, err := retention.PurgeEventAnalytics(ctx, anStart.AddDate(1, 0, 0), 100); err != nil || n == 0 {
			break
		}
	}
	for _, table := range []string{"event_lab_touches", "lab_traffic_coverage"} {
		if n := rtCount(t, db, `SELECT count(*) FROM `+table+` WHERE event_id = $1`, f.event); n != 0 {
			t.Fatalf("%s: %d rows left after the retention period", table, n)
		}
	}
}
