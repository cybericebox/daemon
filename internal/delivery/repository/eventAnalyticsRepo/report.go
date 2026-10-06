package eventAnalyticsRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

// ReportQueries are the statements of «Звіт по заходу» (§6.8); Queries
// embeds it.
type ReportQueries interface {
	ListEventReportRanking(context.Context, uuid.UUID) ([]postgres.ListEventReportRankingRow, error)
	ListEventReportTasks(context.Context, uuid.UUID) ([]postgres.ListEventReportTasksRow, error)
	GetEventReportFunnel(context.Context, uuid.UUID) (postgres.GetEventReportFunnelRow, error)
}

type (
	// ReportTeam is a row of the final ranking (unranked: rank comes from
	// the order).
	ReportTeam struct {
		TeamID      uuid.UUID
		Name        string
		Individual  bool
		MemberCount int64
		Points      int64
		Solved      int64
		LastSolveAt *time.Time
		Attempts    int64
	}

	// ReportTask is the per-task statistics row.
	ReportTask struct {
		ChallengeID                                      uuid.UUID
		Name                                             string
		Points                                           int64
		TeamsOpened, TeamsAttempted                      int64
		Attempts, CorrectAttempts, Solves, HintsUnlocked int64
		FirstSolveAt                                     *time.Time
		FirstSolveTeam                                   string
	}

	// ReportFunnel is the participation funnel below the approved
	// participants.
	ReportFunnel struct {
		ParticipantsOpened, ParticipantsAttempted int64
		TeamsAttempted, TeamsSolved               int64
	}
)

func (r *Repository) ReportRanking(ctx context.Context, eventID uuid.UUID) ([]ReportTeam, error) {
	rows, err := r.q.ListEventReportRanking(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]ReportTeam, 0, len(rows))
	for _, row := range rows {
		out = append(out, ReportTeam{
			TeamID: row.TeamID, Name: row.TeamName, Individual: row.Individual, MemberCount: row.MemberCount,
			Points: row.Points, Solved: row.Solved, LastSolveAt: unixPtr(row.LastSolveUnix), Attempts: row.Attempts,
		})
	}
	return out, nil
}

func (r *Repository) ReportTasks(ctx context.Context, eventID uuid.UUID) ([]ReportTask, error) {
	rows, err := r.q.ListEventReportTasks(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]ReportTask, 0, len(rows))
	for _, row := range rows {
		out = append(out, ReportTask{
			ChallengeID: row.ChallengeID, Name: row.Name, Points: row.Points,
			TeamsOpened: row.TeamsOpened, TeamsAttempted: row.TeamsAttempted, Attempts: row.Attempts,
			CorrectAttempts: row.CorrectAttempts, Solves: row.Solves, HintsUnlocked: row.HintsUnlocked,
			FirstSolveAt: unixPtr(row.FirstSolveUnix), FirstSolveTeam: row.FirstSolveTeam,
		})
	}
	return out, nil
}

func (r *Repository) ReportFunnel(ctx context.Context, eventID uuid.UUID) (ReportFunnel, error) {
	row, err := r.q.GetEventReportFunnel(ctx, eventID)
	if err != nil {
		return ReportFunnel{}, err
	}
	return ReportFunnel{
		ParticipantsOpened: row.ParticipantsOpened, ParticipantsAttempted: row.ParticipantsAttempted,
		TeamsAttempted: row.TeamsAttempted, TeamsSolved: row.TeamsSolved,
	}, nil
}

// unixPtr reads epoch seconds where 0 means none.
func unixPtr(seconds int64) *time.Time {
	if seconds <= 0 {
		return nil
	}
	t := time.Unix(seconds, 0).UTC()
	return &t
}
