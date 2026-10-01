package lab

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	labTraffic "github.com/cybericebox/daemon/internal/model/labTraffic"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
)

// TrafficSink receives the lab traffic reports of a monitoring update. They
// never reach lab_monitoring_current or event_lab_observations: those are
// admin-visible and kept for 90 days, the traffic aggregates are neither.
type TrafficSink interface {
	ApplyTraffic(ctx context.Context, reports []*labpb.TrafficReport) error
}

// WithTraffic sets where the traffic reports of the stream go.
func (r *Runner) WithTraffic(sink TrafficSink) *Runner {
	r.traffic = sink
	return r
}

// TrafficStore is what the ingest needs from storage.
type TrafficStore interface {
	IsUserInTeam(ctx context.Context, eventID, teamID, userID uuid.UUID) (bool, error)
	ApplyTouch(ctx context.Context, touch labTraffic.Touch) error
	RecordCoverage(ctx context.Context, eventID, teamID uuid.UUID, surface labTraffic.Surface, source, bootID string, span labTraffic.Coverage) error
}

const (
	membershipTTL   = 10 * time.Minute
	maxCacheEntries = 200_000
)

type memberKey struct{ event, team, user uuid.UUID }

type appliedKey struct {
	event, team, user, challenge uuid.UUID
	surface                      labTraffic.Surface
}

type appliedRow struct {
	attempts, bytesIn, bytesOut, packets int64
	lastSeenMs, respondedMs              int64
}

// TrafficIngest resolves collector reports to events, teams, users and tasks
// and stores them. It is used from the single monitoring goroutine.
type TrafficIngest struct {
	store TrafficStore
	now   func() time.Time

	members map[memberKey]time.Time
	applied map[appliedKey]appliedRow
}

func NewTrafficIngest(store TrafficStore) *TrafficIngest {
	return &TrafficIngest{
		store: store, now: func() time.Time { return time.Now().UTC() },
		members: map[memberKey]time.Time{}, applied: map[appliedKey]appliedRow{},
	}
}

// ApplyTraffic stores every report it can attribute. A report or row it cannot
// attribute (an unknown group, the test client, a user outside the team, an
// unresolvable lab) is dropped, never guessed. Only storage errors are returned.
func (t *TrafficIngest) ApplyTraffic(ctx context.Context, reports []*labpb.TrafficReport) error {
	if len(t.applied) > maxCacheEntries {
		t.applied = map[appliedKey]appliedRow{}
	}
	if len(t.members) > maxCacheEntries {
		t.members = map[memberKey]time.Time{}
	}
	for _, report := range reports {
		if err := t.applyReport(ctx, report); err != nil {
			return err
		}
	}
	return nil
}

func (t *TrafficIngest) applyReport(ctx context.Context, report *labpb.TrafficReport) error {
	eventID, teamID, ok := labBindingModel.ParseGroupName(report.GetLabGroupName())
	if !ok {
		return nil
	}
	var surface labTraffic.Surface
	switch report.GetKind() {
	case "vpn":
		surface = labTraffic.SurfaceVPN
	case "proxy":
		surface = labTraffic.SurfaceProxy
	default:
		return nil
	}
	source, boot := report.GetSource(), report.GetBootId()
	if source == "" || boot == "" {
		return nil
	}

	// Coverage first, and also when the ledger is empty: an idle team that was
	// watched is what makes «did not touch» a fact.
	if to := report.GetCoveredToUnixMs(); to > 0 {
		from := report.GetCoveredFromUnixMs()
		if from <= 0 || from > to {
			from = to
		}
		span := labTraffic.Coverage{From: time.UnixMilli(from).UTC(), To: time.UnixMilli(to).UTC(), Partial: report.GetPartial()}
		if err := t.store.RecordCoverage(ctx, eventID, teamID, surface, source, boot, span); err != nil {
			return err
		}
	}

	now := t.now()
	for _, row := range report.GetLedger() {
		if err := t.applyRow(ctx, eventID, teamID, surface, row, now); err != nil {
			return err
		}
	}
	return nil
}

func (t *TrafficIngest) applyRow(ctx context.Context, eventID, teamID uuid.UUID, surface labTraffic.Surface, row *labpb.TrafficTouch, now time.Time) error {
	userID, ok := labTraffic.UserFromSubject(surface, row.GetSubject())
	if !ok {
		return nil
	}
	challengeID, _, ok := labBindingModel.ParseLabName(row.GetLabName())
	if !ok {
		return nil
	}
	if row.GetAttempts() <= 0 || row.GetFirstSeenUnixMs() <= 0 {
		return nil
	}
	member, err := t.isMember(ctx, eventID, teamID, userID, now)
	if err != nil {
		return err
	}
	if !member {
		return nil
	}

	key := appliedKey{eventID, teamID, userID, challengeID, surface}
	first, last := time.UnixMilli(row.GetFirstSeenUnixMs()).UTC(), time.UnixMilli(row.GetLastSeenUnixMs()).UTC()
	if last.Before(first) {
		last = first
	}
	touch := labTraffic.Touch{
		EventID: eventID, TeamID: teamID, UserID: userID, EventChallengeID: challengeID, Surface: surface,
		Attempts: row.GetAttempts(), PacketsOut: row.GetPacketsOut(), PacketsIn: row.GetPacketsIn(),
		BytesOut: row.GetBytesOut(), BytesIn: row.GetBytesIn(), FirstSeenAt: first, LastSeenAt: last,
	}
	if ms := row.GetFirstRespondedUnixMs(); ms > 0 {
		responded := time.UnixMilli(ms).UTC()
		touch.FirstRespondAt = &responded
	}
	snapshot := appliedRow{
		attempts: touch.Attempts, bytesIn: touch.BytesIn, bytesOut: touch.BytesOut, packets: touch.PacketsIn + touch.PacketsOut,
		lastSeenMs: row.GetLastSeenUnixMs(), respondedMs: row.GetFirstRespondedUnixMs(),
	}
	if t.applied[key] == snapshot {
		return nil // the same state again, nothing new
	}
	if err := t.store.ApplyTouch(ctx, touch); err != nil {
		return err
	}
	t.applied[key] = snapshot
	return nil
}

func (t *TrafficIngest) isMember(ctx context.Context, eventID, teamID, userID uuid.UUID, now time.Time) (bool, error) {
	key := memberKey{eventID, teamID, userID}
	if until, ok := t.members[key]; ok && now.Before(until) {
		return true, nil
	}
	member, err := t.store.IsUserInTeam(ctx, eventID, teamID, userID)
	if err != nil {
		return false, err
	}
	if member {
		t.members[key] = now.Add(membershipTTL)
	}
	return member, nil
}
