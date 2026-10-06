package eventAnalytics

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

// ReportStore is the statement port of «Звіт по заході».
type ReportStore interface {
	ReportRanking(ctx context.Context, eventID uuid.UUID) ([]eventAnalyticsRepo.ReportTeam, error)
	ReportTasks(ctx context.Context, eventID uuid.UUID) ([]eventAnalyticsRepo.ReportTask, error)
	ReportFunnel(ctx context.Context, eventID uuid.UUID) (eventAnalyticsRepo.ReportFunnel, error)
}

// Funnel step keys; the UI translates them.
const (
	FunnelRegistered = "registered"
	FunnelApproved   = "approved"
	FunnelOpened     = "opened"
	FunnelAttempted  = "attempted"

	FunnelTeams         = "teams"
	FunnelTeamsAdmitted = "admitted"
	FunnelTeamsTried    = "tried"
	FunnelTeamsSolved   = "solved"
)

type (
	// ReportView is the event report (§6.8). Before the finish only
	// Available=false and the event's name are set.
	ReportView struct {
		Available bool
		EventName string
		StartAt   time.Time
		FinishAt  *time.Time
		// GeneratedAt is when the figures were read.
		GeneratedAt time.Time

		Participants ParticipantCountsView
		Teams        TeamCountsView
		Tasks        int64
		Attempts     int64
		// Correct counts effectively correct attempts (after decisions).
		Correct     int64
		Solves      int64
		HintsOpened int64
		HintPoints  int64

		Ranking  []ReportRankView
		TaskRows []ReportTaskView
		// ParticipantFunnel and TeamFunnel are the participation funnels.
		ParticipantFunnel []FunnelStepView
		TeamFunnel        []FunnelStepView
		// Series is the activity over the event's window.
		Series []SeriesPointView
		// Stands is set for an event with infrastructure.
		Stands *StandsSummaryView
	}

	ReportRankView struct {
		Rank        int
		TeamID      uuid.UUID
		Name        string
		Individual  bool
		Members     int64
		Points      int64
		Solved      int64
		Attempts    int64
		LastSolveAt *time.Time
	}

	ReportTaskView struct {
		ChallengeID     uuid.UUID
		Name            string
		Points          int64
		TeamsOpened     int64
		TeamsAttempted  int64
		Attempts        int64
		CorrectAttempts int64
		Solves          int64
		// SolveRate is solves over the teams that tried, in [0, 1].
		SolveRate      float64
		HintsUnlocked  int64
		FirstSolveAt   *time.Time
		FirstSolveTeam string
	}

	FunnelStepView struct {
		Key   string
		Count int64
	}
)

// GetEventAnalyticsReport composes the event report from the overview
// counters, the final ranking, the per-task statistics and the funnel. It is
// available only after the finish.
func (u *EventAnalyticsUseCase) GetEventAnalyticsReport(ctx context.Context, eventID uuid.UUID) (ReportView, error) {
	e, err := u.event(ctx, eventID)
	if err != nil {
		return ReportView{}, err
	}
	finish := e.Lifecycle.EffectiveFinishAt()
	now := u.now()
	if finish == nil || now.Before(*finish) {
		return ReportView{EventName: e.Name, StartAt: e.Lifecycle.StartAt, FinishAt: finish}, nil
	}
	key := fmt.Sprintf("report:%s:%d", eventID, finish.Unix())
	return cachedReportOf(ctx, u.cache, key, func(ctx context.Context) (ReportView, error) {
		return u.loadReport(ctx, eventID, e.Name, e.Lifecycle.StartAt, *finish, e.InfrastructureAllowed)
	})
}

func (u *EventAnalyticsUseCase) loadReport(ctx context.Context, eventID uuid.UUID, name string, start, finish time.Time, infrastructure bool) (ReportView, error) {
	fail := func(err error, what string) (ReportView, error) {
		return ReportView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read " + what).Err()
	}
	period, err := eventAnalyticsModel.NewPeriod(nil, nil, start, &finish, u.now())
	if err != nil {
		// A window beyond the series limit shows its last stretch.
		from := finish.Add(-eventAnalyticsModel.MaxPeriod)
		if period, err = eventAnalyticsModel.NewPeriod(&from, nil, start, &finish, u.now()); err != nil {
			return ReportView{}, err
		}
	}
	counts, err := u.store.Overview(ctx, eventID, u.now().Add(-eventAnalyticsModel.ActiveWindow))
	if err != nil {
		return fail(err, "event analytics counters")
	}
	points, err := u.store.Series(ctx, eventID, period)
	if err != nil {
		return fail(err, "event activity series")
	}
	ranking, err := u.store.ReportRanking(ctx, eventID)
	if err != nil {
		return fail(err, "event ranking")
	}
	tasks, err := u.store.ReportTasks(ctx, eventID)
	if err != nil {
		return fail(err, "event task statistics")
	}
	funnel, err := u.store.ReportFunnel(ctx, eventID)
	if err != nil {
		return fail(err, "event funnel")
	}

	view := ReportView{
		Available: true, EventName: name, StartAt: start, FinishAt: &finish, GeneratedAt: u.now(),
		Participants: ParticipantCountsView{
			Registered: counts.ParticipantsRegistered, Approved: counts.ParticipantsApproved,
			Pending: counts.ParticipantsPending, Invited: counts.ParticipantsInvited,
		},
		Teams:    TeamCountsView{Total: counts.TeamsTotal, Admitted: counts.TeamsAdmitted, Incomplete: counts.TeamsTotal - counts.TeamsAdmitted},
		Tasks:    int64(len(tasks)),
		Attempts: counts.Attempts, Correct: counts.AttemptsCorrect, Solves: counts.Solves,
		HintsOpened: counts.HintsOpened, HintPoints: counts.HintPoints,
		Series:   denseSeries(points, period),
		Ranking:  make([]ReportRankView, 0, len(ranking)),
		TaskRows: make([]ReportTaskView, 0, len(tasks)),
		ParticipantFunnel: []FunnelStepView{
			{FunnelRegistered, counts.ParticipantsRegistered},
			{FunnelApproved, counts.ParticipantsApproved},
			{FunnelOpened, funnel.ParticipantsOpened},
			{FunnelAttempted, funnel.ParticipantsAttempted},
		},
		TeamFunnel: []FunnelStepView{
			{FunnelTeams, counts.TeamsTotal},
			{FunnelTeamsAdmitted, counts.TeamsAdmitted},
			{FunnelTeamsTried, funnel.TeamsAttempted},
			{FunnelTeamsSolved, funnel.TeamsSolved},
		},
	}
	// Equal scores share a place (competition ranking: 1, 2, 2, 4).
	for i, t := range ranking {
		rank := i + 1
		if i > 0 && ranking[i-1].Points == t.Points && sameInstant(ranking[i-1].LastSolveAt, t.LastSolveAt) {
			rank = view.Ranking[i-1].Rank
		}
		view.Ranking = append(view.Ranking, ReportRankView{
			Rank: rank, TeamID: t.TeamID, Name: t.Name, Individual: t.Individual, Members: t.MemberCount,
			Points: t.Points, Solved: t.Solved, Attempts: t.Attempts, LastSolveAt: t.LastSolveAt,
		})
	}
	for _, t := range tasks {
		row := ReportTaskView{
			ChallengeID: t.ChallengeID, Name: t.Name, Points: t.Points, TeamsOpened: t.TeamsOpened,
			TeamsAttempted: t.TeamsAttempted, Attempts: t.Attempts, CorrectAttempts: t.CorrectAttempts, Solves: t.Solves,
			HintsUnlocked: t.HintsUnlocked, FirstSolveAt: t.FirstSolveAt, FirstSolveTeam: t.FirstSolveTeam,
		}
		if t.TeamsAttempted > 0 {
			row.SolveRate = float64(t.Solves) / float64(t.TeamsAttempted)
		}
		view.TaskRows = append(view.TaskRows, row)
	}
	if infrastructure {
		stands, err := u.loadStands(ctx, eventID, period)
		if err != nil {
			return ReportView{}, err
		}
		view.Stands = &stands.Summary
	}
	return view, nil
}

func sameInstant(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
