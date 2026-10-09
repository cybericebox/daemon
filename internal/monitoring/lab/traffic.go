package lab

import (
	"context"
	"encoding/json"
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
	// LabChallenges names the tasks that share the team's Lab: a Lab belongs to an exercise, not to a task.
	LabChallenges(ctx context.Context, teamID uuid.UUID, group, lab string) ([]uuid.UUID, error)
	ApplyTouch(ctx context.Context, touch labTraffic.Touch) error
	RecordCoverage(ctx context.Context, eventID, teamID uuid.UUID, surface labTraffic.Surface, source, bootID string, span labTraffic.Coverage) error
}

const (
	membershipTTL   = 10 * time.Minute
	maxCacheEntries = 200_000
)

type memberKey struct{ event, team, user uuid.UUID }

type labKey struct {
	team       uuid.UUID
	group, lab string
}

type appliedKey struct {
	event, team, user, challenge uuid.UUID
	surface                      labTraffic.Surface
}

type appliedRow struct {
	attempts, labInitiated, bytesIn, bytesOut, packetsIn, packetsOut int64
	firstSeenMs, lastSeenMs, respondedMs                             int64
}

// TrafficIngest resolves collector reports to events, teams, users and tasks
// and stores them. It is used from the single monitoring goroutine.
type TrafficIngest struct {
	store TrafficStore
	now   func() time.Time

	members map[memberKey]time.Time
	applied map[appliedKey]appliedRow
	labs    map[labKey][]uuid.UUID
}

func NewTrafficIngest(store TrafficStore) *TrafficIngest {
	return &TrafficIngest{
		store: store, now: func() time.Time { return time.Now().UTC() },
		members: map[memberKey]time.Time{}, applied: map[appliedKey]appliedRow{}, labs: map[labKey][]uuid.UUID{},
	}
}

// ApplyTraffic stores every report it can attribute. A report or row it cannot
// attribute (an unknown group, the test client, a user outside the team, an
// unresolvable lab) is dropped, never guessed. Only storage errors are returned.
func (t *TrafficIngest) ApplyTraffic(ctx context.Context, reports []*labpb.TrafficReport) error {
	if len(t.applied) > maxCacheEntries {
		t.applied = map[appliedKey]appliedRow{}
	}
	if len(t.labs) > maxCacheEntries {
		t.labs = map[labKey][]uuid.UUID{}
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

	// Persist actual spans before rows, including idle heartbeats. An envelope
	// around disjoint epochs never proves observation during their downtime.
	if err := t.applyCoverage(ctx, eventID, teamID, surface, report); err != nil {
		return err
	}

	now := t.now()
	for _, row := range report.GetLedger() {
		if err := t.applyRow(ctx, eventID, teamID, report.GetLabGroupName(), surface, row, now); err != nil {
			return err
		}
	}
	return nil
}

func (t *TrafficIngest) applyCoverage(ctx context.Context, eventID, teamID uuid.UUID, surface labTraffic.Surface, report *labpb.TrafficReport) error {
	spans := report.GetCoverageSpans()
	invalidLedger := false
	for _, row := range report.GetLedger() {
		invalidLedger = invalidLedger || !validTrafficFacts(row)
	}
	explicit := len(spans) > 0
	invalidCoverage := false
	for _, span := range spans {
		invalidCoverage = invalidCoverage || span.GetFromUnixMs() <= 0 || span.GetToUnixMs() < span.GetFromUnixMs()
	}
	if !explicit {
		spans = []*labpb.TrafficCoverageSpan{{FromUnixMs: report.GetCoveredFromUnixMs(), ToUnixMs: report.GetCoveredToUnixMs()}}
	}
	for _, raw := range spans {
		from, to := raw.GetFromUnixMs(), raw.GetToUnixMs()
		// Discarding one malformed sibling cannot turn the remainder into
		// evidence that all of this report's traffic was observed.
		partial := raw.GetPartial() || report.GetPartial() || report.GetTruncated() || invalidLedger || invalidCoverage
		if from <= 0 || to <= 0 || from > to {
			// A malformed explicit interval must not fall back to a healthy
			// scalar envelope. Record the available envelope as incomplete.
			partial = true
			from, to = report.GetCoveredFromUnixMs(), report.GetCoveredToUnixMs()
			if to <= 0 {
				continue
			}
			if from <= 0 || from > to {
				from = to
			}
		}
		source, instance, boot := raw.GetSource(), raw.GetInstance(), raw.GetBootId()
		if source == "" {
			source = report.GetSource()
		}
		if instance == "" {
			instance = report.GetInstance()
		}
		if boot == "" {
			boot = report.GetBootId()
		}
		if explicit {
			// A tuple keeps replica identities distinct even if their strings
			// contain the separator another source uses.
			identity, _ := json.Marshal([2]string{source, instance})
			source = string(identity)
		}
		span := labTraffic.Coverage{From: time.UnixMilli(from).UTC(), To: time.UnixMilli(to).UTC(), Partial: partial, Explicit: explicit}
		if err := t.store.RecordCoverage(ctx, eventID, teamID, surface, source, boot, span); err != nil {
			return err
		}
	}
	return nil
}

// challengesOf resolves a lab name to the tasks that share it. Only a found Lab is remembered.
func (t *TrafficIngest) challengesOf(ctx context.Context, teamID uuid.UUID, group, lab string) ([]uuid.UUID, error) {
	key := labKey{teamID, group, lab}
	if ids, ok := t.labs[key]; ok {
		return ids, nil
	}
	ids, err := t.store.LabChallenges(ctx, teamID, group, lab)
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		t.labs[key] = ids
	}
	return ids, nil
}

func (t *TrafficIngest) applyRow(ctx context.Context, eventID, teamID uuid.UUID, group string, surface labTraffic.Surface, row *labpb.TrafficTouch, now time.Time) error {
	userID, ok := labTraffic.UserFromSubject(surface, row.GetSubject())
	if !ok {
		return nil
	}
	if row.GetLabName() == "" {
		return nil
	}
	// Signed counters/timestamps are facts, never deltas. Invalid negative
	// facts are discarded; lab-initiated or timestamp-less rows remain useful.
	if !validTrafficFacts(row) {
		return nil
	}
	if row.GetAttempts() == 0 && row.GetLabInitiatedAttempts() == 0 && row.GetPacketsIn() == 0 && row.GetPacketsOut() == 0 && row.GetBytesIn() == 0 && row.GetBytesOut() == 0 {
		return nil
	}
	member, err := t.isMember(ctx, eventID, teamID, userID, now)
	if err != nil {
		return err
	}
	if !member {
		return nil
	}

	challenges, err := t.challengesOf(ctx, teamID, group, row.GetLabName())
	if err != nil {
		return err
	}
	for _, challengeID := range challenges {
		if err := t.applyChallenge(ctx, eventID, teamID, userID, challengeID, surface, row); err != nil {
			return err
		}
	}
	return nil
}

func validTrafficFacts(row *labpb.TrafficTouch) bool {
	for _, n := range []int64{row.GetAttempts(), row.GetLabInitiatedAttempts(), row.GetPacketsIn(), row.GetPacketsOut(), row.GetBytesIn(), row.GetBytesOut(), row.GetFirstSeenUnixMs(), row.GetLastSeenUnixMs(), row.GetFirstRespondedUnixMs()} {
		if n < 0 {
			return false
		}
	}
	return true
}

func (t *TrafficIngest) applyChallenge(ctx context.Context, eventID, teamID, userID, challengeID uuid.UUID, surface labTraffic.Surface, row *labpb.TrafficTouch) error {
	key := appliedKey{eventID, teamID, userID, challengeID, surface}
	var first, last time.Time
	if ms := row.GetFirstSeenUnixMs(); ms > 0 {
		first = time.UnixMilli(ms).UTC()
	}
	if ms := row.GetLastSeenUnixMs(); ms > 0 {
		last = time.UnixMilli(ms).UTC()
	}
	if !first.IsZero() && !last.IsZero() && last.Before(first) {
		last = first
	}
	touch := labTraffic.Touch{
		EventID: eventID, TeamID: teamID, UserID: userID, EventChallengeID: challengeID, Surface: surface,
		Attempts: row.GetAttempts(), LabInitiatedAttempts: row.GetLabInitiatedAttempts(), PacketsOut: row.GetPacketsOut(), PacketsIn: row.GetPacketsIn(),
		BytesOut: row.GetBytesOut(), BytesIn: row.GetBytesIn(), FirstSeenAt: first, LastSeenAt: last,
	}
	if ms := row.GetFirstRespondedUnixMs(); ms > 0 {
		responded := time.UnixMilli(ms).UTC()
		touch.FirstRespondAt = &responded
	}
	snapshot := appliedRow{
		attempts: touch.Attempts, labInitiated: touch.LabInitiatedAttempts, bytesIn: touch.BytesIn, bytesOut: touch.BytesOut, packetsIn: touch.PacketsIn, packetsOut: touch.PacketsOut,
		firstSeenMs: row.GetFirstSeenUnixMs(), lastSeenMs: row.GetLastSeenUnixMs(), respondedMs: row.GetFirstRespondedUnixMs(),
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
