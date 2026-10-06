package scoreboardRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

// ParticipationSolve is one solved task of a team. SolvedBy is uuid.Nil when
// the solving attempt is unknown.
type ParticipationSolve struct {
	EventChallengeID        uuid.UUID
	ChallengeName, Category string
	Points                  int32
	SolvedAt                time.Time
	SolvedBy                uuid.UUID
	SolvedByName            string
	FirstBlood              bool
}

// ParticipationMember is one member of a team with their own counters.
type ParticipationMember struct {
	UserID                           uuid.UUID
	Role                             int16
	Name                             string
	JoinedAt                         time.Time
	Attempts, CorrectAttempts, Hints int64
}

// ParticipationTotals are the team-wide counters, members who left included.
type ParticipationTotals struct {
	Attempts, CorrectAttempts, Hints int64
}

func (r *Repository) ParticipationSolves(ctx context.Context, eventID, teamID uuid.UUID) ([]ParticipationSolve, error) {
	rows, err := r.q.ListParticipationSolves(ctx, postgres.ListParticipationSolvesParams{EventID: eventID, EventTeamID: teamID})
	if err != nil {
		return nil, err
	}
	out := make([]ParticipationSolve, 0, len(rows))
	for _, row := range rows {
		out = append(out, ParticipationSolve{EventChallengeID: row.EventChallengeID, ChallengeName: row.ChallengeName, Category: row.Category, Points: row.Points,
			SolvedAt: row.SolvedAt, SolvedBy: row.SolvedBy, SolvedByName: row.SolvedByName, FirstBlood: row.FirstBlood})
	}
	return out, nil
}

func (r *Repository) ParticipationMembers(ctx context.Context, eventID, teamID uuid.UUID) ([]ParticipationMember, error) {
	rows, err := r.q.ListParticipationMembers(ctx, postgres.ListParticipationMembersParams{EventID: eventID, EventTeamID: teamID})
	if err != nil {
		return nil, err
	}
	out := make([]ParticipationMember, 0, len(rows))
	for _, row := range rows {
		out = append(out, ParticipationMember{UserID: row.UserID, Role: row.TeamRole.Int16, Name: row.DisplayName, JoinedAt: row.JoinedAt,
			Attempts: row.Attempts, CorrectAttempts: row.CorrectAttempts, Hints: row.Hints})
	}
	return out, nil
}

// ModeratorsParticipationMembers is the roster of the moderators team: the
// event managers, owner first, with what each did as the team.
func (r *Repository) ModeratorsParticipationMembers(ctx context.Context, eventID, teamID uuid.UUID) ([]ParticipationMember, error) {
	rows, err := r.q.ListModeratorsParticipationMembers(ctx, postgres.ListModeratorsParticipationMembersParams{EventID: eventID, EventTeamID: teamID})
	if err != nil {
		return nil, err
	}
	out := make([]ParticipationMember, 0, len(rows))
	for _, row := range rows {
		out = append(out, ParticipationMember{UserID: row.UserID, Role: row.TeamRole, Name: row.DisplayName, JoinedAt: row.JoinedAt,
			Attempts: row.Attempts, CorrectAttempts: row.CorrectAttempts, Hints: row.Hints})
	}
	return out, nil
}

func (r *Repository) ParticipationTotals(ctx context.Context, eventID, teamID uuid.UUID) (ParticipationTotals, error) {
	row, err := r.q.GetParticipationTeamTotals(ctx, postgres.GetParticipationTeamTotalsParams{EventID: eventID, EventTeamID: teamID})
	if err != nil {
		return ParticipationTotals{}, err
	}
	return ParticipationTotals{Attempts: row.Attempts, CorrectAttempts: row.CorrectAttempts, Hints: row.Hints}, nil
}
