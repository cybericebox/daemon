package eventAnalytics

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
)

// maxTeamFailures bounds the failures listed per team; the counters cover
// all of them.
const maxTeamFailures = 20

// StandStore is the statement port of «Стенди».
type StandStore interface {
	StandTeams(ctx context.Context, eventID uuid.UUID) ([]eventAnalyticsRepo.StandTeam, error)
	StandTransitions(ctx context.Context, eventID uuid.UUID) ([]eventAnalyticsModel.StandTransition, error)
	StandResources(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period) ([]eventAnalyticsRepo.StandResources, error)
	VPNUsage(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period) ([]eventAnalyticsRepo.VPNUsage, error)
}

type (
	// StandsView is the «Стенди» report (§6.5).
	StandsView struct {
		// Available is false for an event without infrastructure: the rest
		// is empty and the section is hidden.
		Available bool
		Summary   StandsSummaryView
		Teams     []StandTeamView
		Period    PeriodView
	}

	StandsSummaryView struct {
		Teams, Ready, Creating, Failed, NotDeployed int64
		// Deploy times in seconds over the teams whose stand got ready.
		DeployAvg, DeployMedian, DeployMax *int64
		// Failures are all stand and task-lab failures; Unresolved are the
		// ones still open.
		Failures, Unresolved int64
		RecoveryAvg          *int64
		RecoveryMax          *int64
		Restarts             int64
		VPNTeams             int64
		VPNSessions          int64
		VPNRxBytes           int64
		VPNTxBytes           int64
	}

	StandTeamView struct {
		TeamID          uuid.UUID
		TeamName        string
		Status          string
		Reason          string
		StatusChangedAt *time.Time
		DeploySeconds   *int64
		Generations     int
		FailureCount    int
		Unresolved      int
		RecoveryAvg     *int64
		RecoveryMax     *int64
		// Failures are the latest ones, newest first.
		Failures  []StandFailureView
		Resources StandResourcesView
		VPN       StandVPNView
	}

	StandFailureView struct {
		// Source is "stand" or "lab".
		Source string
		// Task is the task of a lab failure.
		Task            string
		At              time.Time
		Reason          string
		RecoveredAt     *time.Time
		RecoverySeconds *int64
	}

	// StandResourcesView is the lab telemetry of a team: the sum of its
	// devices' peaks over the period.
	StandResourcesView struct {
		Devices          int64
		PeakCPUMillis    int64
		PeakMemoryBytes  int64
		Restarts         int64
		RestartedDevices int64
	}

	StandVPNView struct {
		Sessions int64
		// Users are the participants that connected; Members the team size.
		Users, Members int64
		Seconds        int64
		RxBytes        int64
		TxBytes        int64
		LastAt         *time.Time
	}
)

// GetEventAnalyticsStands is the «Стенди» report. The period bounds the
// telemetry and VPN figures; the deploy and failure history is the event's.
func (u *EventAnalyticsUseCase) GetEventAnalyticsStands(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (StandsView, error) {
	e, err := u.event(ctx, eventID)
	if err != nil {
		return StandsView{}, err
	}
	if !e.InfrastructureAllowed {
		return StandsView{}, nil
	}
	finish := e.Lifecycle.EffectiveFinishAt()
	period, err := eventAnalyticsModel.NewPeriod(from, to, e.Lifecycle.StartAt, finish, u.now())
	if err != nil {
		return StandsView{}, err
	}
	key := fmt.Sprintf("stands:%s:%d:%d", eventID, period.From.Unix(), period.To.Unix())
	return cachedReportOf(ctx, u.cache, key, func(ctx context.Context) (StandsView, error) {
		return u.loadStands(ctx, eventID, period)
	})
}

func (u *EventAnalyticsUseCase) loadStands(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period) (StandsView, error) {
	fail := func(err error, what string) (StandsView, error) {
		return StandsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read " + what).Err()
	}
	teams, err := u.store.StandTeams(ctx, eventID)
	if err != nil {
		return fail(err, "event stands")
	}
	log, err := u.store.StandTransitions(ctx, eventID)
	if err != nil {
		return fail(err, "event stand history")
	}
	resources, err := u.store.StandResources(ctx, eventID, period)
	if err != nil {
		return fail(err, "event stand telemetry")
	}
	vpn, err := u.store.VPNUsage(ctx, eventID, period)
	if err != nil {
		return fail(err, "event VPN usage")
	}
	return buildStands(teams, eventAnalyticsModel.AnalyzeStandTransitions(log), resources, vpn, period), nil
}

func buildStands(teams []eventAnalyticsRepo.StandTeam, histories map[uuid.UUID]eventAnalyticsModel.StandHistory,
	resources []eventAnalyticsRepo.StandResources, vpn []eventAnalyticsRepo.VPNUsage, period eventAnalyticsModel.Period) StandsView {
	resourceOf := map[uuid.UUID]eventAnalyticsRepo.StandResources{}
	for _, r := range resources {
		resourceOf[r.TeamID] = r
	}
	vpnOf := map[uuid.UUID]eventAnalyticsRepo.VPNUsage{}
	for _, v := range vpn {
		vpnOf[v.TeamID] = v
	}

	view := StandsView{Available: true, Teams: make([]StandTeamView, 0, len(teams)), Period: PeriodView{From: period.From, To: period.To}}
	var deploys, recoveries []int64
	for _, t := range teams {
		history := histories[t.TeamID]
		tv := StandTeamView{
			TeamID: t.TeamID, TeamName: t.TeamName, Status: eventStandModel.Status(t.Status).String(), Reason: t.Reason,
			StatusChangedAt: t.StatusChangedAt, DeploySeconds: history.DeploySeconds, Generations: history.Generations,
			FailureCount: len(history.Failures), Failures: []StandFailureView{},
		}
		var teamRecoveries []int64
		for _, f := range history.Failures {
			if seconds, ok := f.RecoverySeconds(); ok {
				teamRecoveries = append(teamRecoveries, seconds)
			} else {
				tv.Unresolved++
			}
		}
		tv.RecoveryAvg, tv.RecoveryMax = avgMax(teamRecoveries)
		recoveries = append(recoveries, teamRecoveries...)
		for i := len(history.Failures) - 1; i >= 0 && len(tv.Failures) < maxTeamFailures; i-- {
			f := history.Failures[i]
			fv := StandFailureView{Source: f.Source, Task: f.ChallengeName, At: f.At, Reason: f.Reason, RecoveredAt: f.RecoveredAt}
			if seconds, ok := f.RecoverySeconds(); ok {
				fv.RecoverySeconds = &seconds
			}
			tv.Failures = append(tv.Failures, fv)
		}
		if r, ok := resourceOf[t.TeamID]; ok {
			tv.Resources = StandResourcesView{
				Devices: r.Devices, PeakCPUMillis: r.PeakCPUMillis, PeakMemoryBytes: r.PeakMemoryBytes,
				Restarts: r.Restarts, RestartedDevices: r.RestartedDevices,
			}
		}
		tv.VPN.Members = t.MemberCount
		if v, ok := vpnOf[t.TeamID]; ok {
			last := v.LastAt
			tv.VPN = StandVPNView{Sessions: v.Sessions, Users: v.Users, Members: t.MemberCount, Seconds: v.Seconds, RxBytes: v.RxBytes, TxBytes: v.TxBytes, LastAt: &last}
			view.Summary.VPNTeams++
			view.Summary.VPNSessions += v.Sessions
			view.Summary.VPNRxBytes += v.RxBytes
			view.Summary.VPNTxBytes += v.TxBytes
		}

		s := &view.Summary
		s.Teams++
		switch eventStandModel.Status(t.Status) {
		case eventStandModel.StatusReady:
			s.Ready++
		case eventStandModel.StatusCreating:
			s.Creating++
		case eventStandModel.StatusFailed:
			s.Failed++
		default:
			s.NotDeployed++
		}
		s.Failures += int64(tv.FailureCount)
		s.Unresolved += int64(tv.Unresolved)
		s.Restarts += tv.Resources.Restarts
		if history.DeploySeconds != nil {
			deploys = append(deploys, *history.DeploySeconds)
		}
		view.Teams = append(view.Teams, tv)
	}
	view.Summary.DeployAvg, view.Summary.DeployMax = avgMax(deploys)
	if len(deploys) > 0 {
		median := standMedian(deploys)
		view.Summary.DeployMedian = &median
	}
	view.Summary.RecoveryAvg, view.Summary.RecoveryMax = avgMax(recoveries)
	return view
}

// avgMax returns the mean and the maximum of the values (nil for none).
func avgMax(values []int64) (avg, maximum *int64) {
	if len(values) == 0 {
		return nil, nil
	}
	var sum, top int64
	for _, v := range values {
		sum += v
		top = max(top, v)
	}
	mean := sum / int64(len(values))
	return &mean, &top
}

func standMedian(values []int64) int64 {
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}
