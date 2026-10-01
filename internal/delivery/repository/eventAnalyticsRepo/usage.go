package eventAnalyticsRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

// UsageQueries are the statements of «Використання»; Queries embeds it.
type UsageQueries interface {
	ListEventUsageUsers(context.Context, postgres.ListEventUsageUsersParams) ([]postgres.ListEventUsageUsersRow, error)
	ListEventUsageVPN(context.Context, postgres.ListEventUsageVPNParams) ([]postgres.ListEventUsageVPNRow, error)
	ListEventUsageSessions(context.Context, postgres.ListEventUsageSessionsParams) ([]postgres.ListEventUsageSessionsRow, error)
	ListEventUsageLiveHandshakes(context.Context, uuid.UUID) ([]postgres.ListEventUsageLiveHandshakesRow, error)
	ListEventUsageTouches(context.Context, uuid.UUID) ([]postgres.ListEventUsageTouchesRow, error)
}

type (
	// UsageUser is a participant of a team (outside the moderators team).
	UsageUser struct {
		UserID, TeamID     uuid.UUID
		TeamName, UserName string
		// LastSeenAt is the last request on the event, LastLabAt the last lab
		// access over the VPN or the proxy; nil when never.
		LastSeenAt, LastLabAt *time.Time
	}

	// UsageVPN is a participant's VPN sessions over a period.
	UsageVPN struct {
		UserID           uuid.UUID
		Sessions         int64
		Seconds          int64
		RxBytes, TxBytes int64
		FirstAt, LastAt  time.Time
	}

	// UsageSession is one VPN session of a participant.
	UsageSession struct {
		UserID           uuid.UUID
		StartedAt        time.Time
		EndedAt          time.Time
		RxBytes, TxBytes int64
	}

	// UsageHandshake is the live last handshake of a participant's peer.
	UsageHandshake struct {
		UserID    uuid.UUID
		Handshake time.Time
	}

	// UsageTouch is a participant's lab access counters for one task and one
	// access type (Surface is vpn or proxy).
	UsageTouch struct {
		UserID                  uuid.UUID
		ChallengeID             uuid.UUID
		Task                    string
		Surface                 string
		Attempts                int64
		BytesIn, BytesOut       int64
		FirstSeenAt, LastSeenAt time.Time
	}
)

// UsageUsers lists the participants of the event's teams, or of one team.
func (r *Repository) UsageUsers(ctx context.Context, eventID uuid.UUID, teamID *uuid.UUID) ([]UsageUser, error) {
	arg := postgres.ListEventUsageUsersParams{EventID: eventID}
	if teamID != nil {
		arg.TeamID = uuid.NullUUID{UUID: *teamID, Valid: true}
	}
	rows, err := r.q.ListEventUsageUsers(ctx, arg)
	if err != nil {
		return nil, err
	}
	out := make([]UsageUser, 0, len(rows))
	for _, row := range rows {
		out = append(out, UsageUser{UserID: row.UserID, TeamID: row.TeamID, TeamName: row.TeamName, UserName: row.UserName, LastSeenAt: usageTimePtr(row.LastSeenAt), LastLabAt: usageLabAt(row.LastLabAt)})
	}
	return out, nil
}

func (r *Repository) UsageVPN(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period) ([]UsageVPN, error) {
	rows, err := r.q.ListEventUsageVPN(ctx, postgres.ListEventUsageVPNParams{EventID: eventID, FromAt: period.From, ToAt: period.To})
	if err != nil {
		return nil, err
	}
	out := make([]UsageVPN, 0, len(rows))
	for _, row := range rows {
		out = append(out, UsageVPN{
			UserID: row.UserID, Sessions: row.Sessions, Seconds: row.Seconds, RxBytes: row.RxBytes, TxBytes: row.TxBytes,
			FirstAt: row.FirstAt, LastAt: row.LastAt,
		})
	}
	return out, nil
}

// UsageSessions returns at most perUser latest sessions of every participant.
func (r *Repository) UsageSessions(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period, perUser int32) ([]UsageSession, error) {
	rows, err := r.q.ListEventUsageSessions(ctx, postgres.ListEventUsageSessionsParams{
		EventID: eventID, FromAt: period.From, ToAt: period.To, SessionLimit: perUser,
	})
	if err != nil {
		return nil, err
	}
	out := make([]UsageSession, 0, len(rows))
	for _, row := range rows {
		out = append(out, UsageSession{UserID: row.UserID, StartedAt: row.StartedAt, EndedAt: row.EndedAt, RxBytes: row.RxBytes, TxBytes: row.TxBytes})
	}
	return out, nil
}

// UsageHandshakes returns the current last handshake of every participant's
// peer; a peer that never connected or is not a participant is left out.
func (r *Repository) UsageHandshakes(ctx context.Context, eventID uuid.UUID) ([]UsageHandshake, error) {
	rows, err := r.q.ListEventUsageLiveHandshakes(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]UsageHandshake, 0, len(rows))
	for _, row := range rows {
		userID, ok := eventAnalyticsModel.ClientUser(row.ClientName)
		if !ok || row.HandshakeUnix <= 0 {
			continue
		}
		out = append(out, UsageHandshake{UserID: userID, Handshake: time.Unix(row.HandshakeUnix, 0).UTC()})
	}
	return out, nil
}

func (r *Repository) UsageTouches(ctx context.Context, eventID uuid.UUID) ([]UsageTouch, error) {
	rows, err := r.q.ListEventUsageTouches(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]UsageTouch, 0, len(rows))
	for _, row := range rows {
		out = append(out, UsageTouch{
			UserID: row.UserID, ChallengeID: row.EventChallengeID, Task: row.ChallengeName, Surface: row.Surface,
			Attempts: row.AttemptsCount, BytesIn: row.BytesIn, BytesOut: row.BytesOut, FirstSeenAt: row.FirstSeenAt, LastSeenAt: row.LastSeenAt,
		})
	}
	return out, nil
}

func usageTimePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

// usageLabAt maps the epoch the query uses for "never" to nil.
func usageLabAt(value time.Time) *time.Time {
	if value.IsZero() || value.Unix() == 0 {
		return nil
	}
	return &value
}
