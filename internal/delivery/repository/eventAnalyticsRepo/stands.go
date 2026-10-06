package eventAnalyticsRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

// StandsQueries are the statements of «Стенди» (§6.5); Queries embeds it.
type StandsQueries interface {
	ListEventStandTeams(context.Context, uuid.UUID) ([]postgres.ListEventStandTeamsRow, error)
	ListEventStandTransitions(context.Context, uuid.UUID) ([]postgres.ListEventStandTransitionsRow, error)
	ListEventStandResources(context.Context, postgres.ListEventStandResourcesParams) ([]postgres.ListEventStandResourcesRow, error)
	ListEventVPNUsage(context.Context, postgres.ListEventVPNUsageParams) ([]postgres.ListEventVPNUsageRow, error)
}

type (
	// StandTeam is a team with the current status of its stand (0: none yet).
	StandTeam struct {
		TeamID          uuid.UUID
		TeamName        string
		MemberCount     int64
		Status          int16
		Reason          string
		Generation      int32
		StatusChangedAt *time.Time
	}

	// StandResources is a team's lab telemetry over a period: the sum of its
	// devices' peaks.
	StandResources struct {
		TeamID           uuid.UUID
		Devices          int64
		PeakCPUMillis    int64
		PeakMemoryBytes  int64
		Restarts         int64
		RestartedDevices int64
	}

	// VPNUsage is a team's VPN usage over a period.
	VPNUsage struct {
		TeamID           uuid.UUID
		Sessions, Users  int64
		Seconds          int64
		RxBytes, TxBytes int64
		FirstAt, LastAt  time.Time
	}
)

func (r *Repository) StandTeams(ctx context.Context, eventID uuid.UUID) ([]StandTeam, error) {
	rows, err := r.q.ListEventStandTeams(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]StandTeam, 0, len(rows))
	for _, row := range rows {
		out = append(out, StandTeam{
			TeamID: row.TeamID, TeamName: row.TeamName, MemberCount: row.MemberCount, Status: row.Status,
			Reason: row.Reason, Generation: row.Generation, StatusChangedAt: timePtr(row.StatusChangedAt),
		})
	}
	return out, nil
}

func (r *Repository) StandTransitions(ctx context.Context, eventID uuid.UUID) ([]eventAnalyticsModel.StandTransition, error) {
	rows, err := r.q.ListEventStandTransitions(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]eventAnalyticsModel.StandTransition, 0, len(rows))
	for _, row := range rows {
		t := eventAnalyticsModel.StandTransition{
			TeamID: row.TeamID, Source: row.Source, ChallengeName: row.ChallengeName, Generation: row.Generation,
			To: row.ToStatus, Reason: row.Reason, At: row.At,
		}
		if row.ChallengeID.Valid {
			challengeID := row.ChallengeID.UUID
			t.ChallengeID = &challengeID
		}
		out = append(out, t)
	}
	return out, nil
}

func (r *Repository) StandResources(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period) ([]StandResources, error) {
	rows, err := r.q.ListEventStandResources(ctx, postgres.ListEventStandResourcesParams{EventID: eventID, FromAt: period.From, ToAt: period.To})
	if err != nil {
		return nil, err
	}
	out := make([]StandResources, 0, len(rows))
	for _, row := range rows {
		out = append(out, StandResources{
			TeamID: row.TeamID, Devices: row.Devices, PeakCPUMillis: row.PeakCpuMillicores,
			PeakMemoryBytes: row.PeakMemoryBytes, Restarts: row.Restarts, RestartedDevices: row.RestartedDevices,
		})
	}
	return out, nil
}

func (r *Repository) VPNUsage(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period) ([]VPNUsage, error) {
	rows, err := r.q.ListEventVPNUsage(ctx, postgres.ListEventVPNUsageParams{EventID: eventID, FromAt: period.From, ToAt: period.To})
	if err != nil {
		return nil, err
	}
	out := make([]VPNUsage, 0, len(rows))
	for _, row := range rows {
		out = append(out, VPNUsage{
			TeamID: row.TeamID, Sessions: row.Sessions, Users: row.Users, Seconds: row.Seconds,
			RxBytes: row.RxBytes, TxBytes: row.TxBytes, FirstAt: row.FirstAt, LastAt: row.LastAt,
		})
	}
	return out, nil
}
