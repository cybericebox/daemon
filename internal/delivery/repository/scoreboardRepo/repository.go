package scoreboardRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

type Entry struct {
	TeamID   uuid.UUID
	TeamName string
	// NameIsReal: TeamName is a participant's real name (individual team, no pseudonym shown).
	NameIsReal  bool
	Points      int64
	Solved      int64
	LastSolveAt *time.Time
}

// ManageEntry is one team of the moderators' results: every team except the
// moderators team, with its hidden/admitted marks and real name.
type ManageEntry struct {
	TeamID                       uuid.UUID
	Individual, Hidden, Admitted bool
	// Moderators marks the hidden team of the event managers.
	Moderators                      bool
	PublicName, RealName, Pseudonym string
	Points, Solved                  int64
	LastSolveAt                     *time.Time
}

// ManageSolve is one solve in the moderators' results. FirstBlood marks the
// first solve of the challenge among the ranked teams.
type ManageSolve struct {
	TeamID, ChallengeID uuid.UUID
	ChallengeName       string
	Points              int32
	SolvedAt            time.Time
	FirstBlood          bool
}

// HintTotal is what one team spent on hints: opened hints and charged points.
type HintTotal struct {
	Hints, Charged int64
}

// Cut selects the scores a reader sees. The zero value reads live scores. With
// At set, scores are computed as if only solves before At existed, plus every
// solve of IncludeTeam (a frozen viewer's own team).
type Cut struct {
	At          *time.Time
	IncludeTeam *uuid.UUID
}

func (c Cut) params() (pgtype.Timestamptz, uuid.NullUUID) {
	var at pgtype.Timestamptz
	if c.At != nil {
		at = pgtype.Timestamptz{Time: *c.At, Valid: true}
	}
	var team uuid.NullUUID
	if c.IncludeTeam != nil {
		team = uuid.NullUUID{UUID: *c.IncludeTeam, Valid: true}
	}
	return at, team
}

type TimelineEntry struct {
	EventChallengeID uuid.UUID
	Points           int32
	SolvedAt         time.Time
}
type EventTimelineEntry struct {
	EventTeamID      uuid.UUID
	EventChallengeID uuid.UUID
	ChallengeName    string
	Points           int32
	SolvedAt         time.Time
}
type Queries interface {
	ListEventScoreTimeline(context.Context, postgres.ListEventScoreTimelineParams) ([]postgres.ListEventScoreTimelineRow, error)
	ListEventScoreboard(context.Context, postgres.ListEventScoreboardParams) ([]postgres.ListEventScoreboardRow, error)
	ListManageScoreboard(context.Context, uuid.UUID) ([]postgres.ListManageScoreboardRow, error)
	ListManageScoreSolves(context.Context, uuid.UUID) ([]postgres.ListManageScoreSolvesRow, error)
	ListManageHintTotals(context.Context, uuid.UUID) ([]postgres.ListManageHintTotalsRow, error)
	ListTeamScoreTimeline(context.Context, uuid.UUID) ([]postgres.ListTeamScoreTimelineRow, error)
	ListParticipationSolves(context.Context, postgres.ListParticipationSolvesParams) ([]postgres.ListParticipationSolvesRow, error)
	ListParticipationMembers(context.Context, postgres.ListParticipationMembersParams) ([]postgres.ListParticipationMembersRow, error)
	ListModeratorsParticipationMembers(context.Context, postgres.ListModeratorsParticipationMembersParams) ([]postgres.ListModeratorsParticipationMembersRow, error)
	GetParticipationTeamTotals(context.Context, postgres.GetParticipationTeamTotalsParams) (postgres.GetParticipationTeamTotalsRow, error)
}

func (r *Repository) EventTimeline(ctx context.Context, eventID uuid.UUID, cut Cut) ([]EventTimelineEntry, error) {
	at, team := cut.params()
	rows, err := r.q.ListEventScoreTimeline(ctx, postgres.ListEventScoreTimelineParams{EventID: eventID, Cutoff: at, IncludeTeam: team})
	if err != nil {
		return nil, err
	}
	out := make([]EventTimelineEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, EventTimelineEntry{EventTeamID: row.EventTeamID, EventChallengeID: row.EventChallengeID, ChallengeName: row.ChallengeName, Points: row.Points, SolvedAt: row.SolvedAt})
	}
	return out, nil
}

func (r *Repository) Timeline(ctx context.Context, teamID uuid.UUID) ([]TimelineEntry, error) {
	rows, err := r.q.ListTeamScoreTimeline(ctx, teamID)
	if err != nil {
		return nil, err
	}
	out := make([]TimelineEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, TimelineEntry{EventChallengeID: row.EventChallengeID, Points: row.Points, SolvedAt: row.SolvedAt})
	}
	return out, nil
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }
func (r *Repository) List(ctx context.Context, eventID uuid.UUID, cut Cut) ([]Entry, error) {
	at, team := cut.params()
	rows, err := r.q.ListEventScoreboard(ctx, postgres.ListEventScoreboardParams{EventID: eventID, Cutoff: at, IncludeTeam: team})
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(rows))
	for _, row := range rows {
		out = append(out, Entry{TeamID: row.TeamID, TeamName: row.TeamName, NameIsReal: row.NameIsReal, Points: row.Points, Solved: row.Solved, LastSolveAt: lastSolve(row.LastSolveAt)})
	}
	return out, nil
}

func (r *Repository) ListManage(ctx context.Context, eventID uuid.UUID) ([]ManageEntry, error) {
	rows, err := r.q.ListManageScoreboard(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]ManageEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, ManageEntry{TeamID: row.TeamID, Individual: row.Individual, Hidden: row.Hidden, Admitted: row.Admitted, Moderators: row.Moderators,
			PublicName: row.PublicName, RealName: row.RealName, Pseudonym: row.Pseudonym, Points: row.Points, Solved: row.Solved, LastSolveAt: lastSolve(row.LastSolveAt)})
	}
	return out, nil
}

func (r *Repository) ListManageSolves(ctx context.Context, eventID uuid.UUID) ([]ManageSolve, error) {
	rows, err := r.q.ListManageScoreSolves(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]ManageSolve, 0, len(rows))
	for _, row := range rows {
		out = append(out, ManageSolve{TeamID: row.EventTeamID, ChallengeID: row.EventChallengeID, ChallengeName: row.ChallengeName, Points: row.Points, SolvedAt: row.SolvedAt, FirstBlood: row.FirstBlood})
	}
	return out, nil
}

// HintTotals is keyed by team; teams without opened hints are absent.
func (r *Repository) HintTotals(ctx context.Context, eventID uuid.UUID) (map[uuid.UUID]HintTotal, error) {
	rows, err := r.q.ListManageHintTotals(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]HintTotal, len(rows))
	for _, row := range rows {
		out[row.EventTeamID] = HintTotal{Hints: row.Hints, Charged: row.Charged}
	}
	return out, nil
}

// lastSolve forgives the driver's untyped MAX(timestamptz) result.
func lastSolve(value any) *time.Time {
	switch v := value.(type) {
	case time.Time:
		x := v
		return &x
	case pgtype.Timestamptz:
		if v.Valid {
			x := v.Time
			return &x
		}
	}
	return nil
}
