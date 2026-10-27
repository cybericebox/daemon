package eventAnalyticsRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

// LabTelemetryQueries are the reads of the lab telemetry rollups of one team.
type LabTelemetryQueries interface {
	ListEventTeamLabUsageMinutes(context.Context, postgres.ListEventTeamLabUsageMinutesParams) ([]postgres.ListEventTeamLabUsageMinutesRow, error)
	ListEventTeamLabMinutes(context.Context, postgres.ListEventTeamLabMinutesParams) ([]postgres.ListEventTeamLabMinutesRow, error)
	ListEventTeamVPNClientMinutes(context.Context, postgres.ListEventTeamVPNClientMinutesParams) ([]postgres.ListEventTeamVPNClientMinutesRow, error)
	ListEventTeamLabTransitions(context.Context, postgres.ListEventTeamLabTransitionsParams) ([]postgres.ListEventTeamLabTransitionsRow, error)
}

type (
	// DeviceMinute is one device of a lab in one minute.
	DeviceMinute struct {
		LabGroupName, LabName, DeviceName string
		BucketAt                          time.Time
		Samples                           int32
		CPUSamples                        int32
		CPUSum                            int64
		CPUMax                            *int64
		MemorySamples                     int32
		MemorySum                         int64
		MemoryMax                         *int64
		Restarts                          int64
		ReadySamples                      int32
		LastPodPhase                      string
	}

	// LabMinute is one lab in one minute.
	LabMinute struct {
		LabGroupName, LabName string
		BucketAt              time.Time
		Samples, ReadySamples int32
		VPNReadySamples       int32
	}

	// ClientMinute is one VPN client (a participant's peer) in one minute.
	ClientMinute struct {
		LabGroupName, ClientName  string
		BucketAt                  time.Time
		Samples, ConnectedSamples int32
		RxBytes, TxBytes          int64
	}

	// LabTransition is a journal row with, for lab_ready, the platform's time
	// of marking the lab ready (nil: not found).
	LabTransition struct {
		ID                     uuid.UUID
		LabGroupName, LabName  string
		DeviceName, ClientName *string
		Kind                   string
		At, ReceivedAt         time.Time
		Detail                 *string
		PlatformReadyAt        *time.Time
	}
)

// TeamDeviceMinutes reads at most limit device minutes of a team in [from, to).
func (r *Repository) TeamDeviceMinutes(
	ctx context.Context, eventID, teamID uuid.UUID, period eventAnalyticsModel.Period, limit int32,
) ([]DeviceMinute, error) {
	rows, err := r.q.ListEventTeamLabUsageMinutes(ctx, postgres.ListEventTeamLabUsageMinutesParams{
		EventID: eventID, TeamID: teamID, FromAt: period.From, ToAt: period.To, RowLimit: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]DeviceMinute, 0, len(rows))
	for _, row := range rows {
		m := DeviceMinute{
			LabGroupName: row.LabGroupName, LabName: row.LabName, DeviceName: row.DeviceName, BucketAt: row.BucketAt,
			Samples: row.Samples, CPUSamples: row.CpuSamples, CPUSum: row.CpuSum, MemorySamples: row.MemorySamples,
			MemorySum: row.MemorySum, Restarts: row.RestartMax, ReadySamples: row.ReadySamples, LastPodPhase: row.LastPodPhase.String,
		}
		if row.CpuMax.Valid {
			m.CPUMax = &row.CpuMax.Int64
		}
		if row.MemoryMax.Valid {
			m.MemoryMax = &row.MemoryMax.Int64
		}
		out = append(out, m)
	}
	return out, nil
}

// TeamLabMinutes reads at most limit lab minutes of a team in [from, to).
func (r *Repository) TeamLabMinutes(
	ctx context.Context, eventID, teamID uuid.UUID, period eventAnalyticsModel.Period, limit int32,
) ([]LabMinute, error) {
	rows, err := r.q.ListEventTeamLabMinutes(ctx, postgres.ListEventTeamLabMinutesParams{
		EventID: eventID, TeamID: teamID, FromAt: period.From, ToAt: period.To, RowLimit: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]LabMinute, 0, len(rows))
	for _, row := range rows {
		out = append(out, LabMinute{
			LabGroupName: row.LabGroupName, LabName: row.LabName, BucketAt: row.BucketAt, Samples: row.Samples,
			ReadySamples: row.ReadySamples, VPNReadySamples: row.VpnReadySamples,
		})
	}
	return out, nil
}

// TeamClientMinutes reads at most limit VPN client minutes of a team in [from, to).
func (r *Repository) TeamClientMinutes(
	ctx context.Context, eventID, teamID uuid.UUID, period eventAnalyticsModel.Period, limit int32,
) ([]ClientMinute, error) {
	rows, err := r.q.ListEventTeamVPNClientMinutes(ctx, postgres.ListEventTeamVPNClientMinutesParams{
		EventID: eventID, TeamID: teamID, FromAt: period.From, ToAt: period.To, RowLimit: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ClientMinute, 0, len(rows))
	for _, row := range rows {
		out = append(out, ClientMinute{
			LabGroupName: row.LabGroupName, ClientName: row.ClientName, BucketAt: row.BucketAt, Samples: row.Samples,
			ConnectedSamples: row.ConnectedSamples, RxBytes: row.RxBytes, TxBytes: row.TxBytes,
		})
	}
	return out, nil
}

// TeamLabTransitions reads at most limit journal rows of a team in [from, to),
// oldest first, optionally only the given kinds.
func (r *Repository) TeamLabTransitions(
	ctx context.Context, eventID, teamID uuid.UUID, period eventAnalyticsModel.Period, kinds []string, limit int32,
) ([]LabTransition, error) {
	if kinds == nil {
		kinds = []string{}
	}
	rows, err := r.q.ListEventTeamLabTransitions(ctx, postgres.ListEventTeamLabTransitionsParams{
		EventID: eventID, TeamID: teamID, FromAt: period.From, ToAt: period.To, Kinds: kinds, RowLimit: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]LabTransition, 0, len(rows))
	for _, row := range rows {
		t := LabTransition{
			ID: row.ID, LabGroupName: row.LabGroupName, LabName: row.LabName, Kind: row.Kind,
			At: row.At, ReceivedAt: row.ReceivedAt, PlatformReadyAt: timePtr(row.PlatformReadyAt),
		}
		if row.DeviceName.Valid {
			t.DeviceName = &row.DeviceName.String
		}
		if row.ClientName.Valid {
			t.ClientName = &row.ClientName.String
		}
		if row.Detail.Valid {
			t.Detail = &row.Detail.String
		}
		out = append(out, t)
	}
	return out, nil
}
