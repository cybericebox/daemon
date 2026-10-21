package event

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/challengeAttemptRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventResultRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/mailRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	challengeAttemptModel "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
)

// GetResultsSettings is the moderator «Налаштування результатів» read.
func (u *EventUseCase) GetResultsSettings(ctx context.Context, eventID uuid.UUID) (ResultsSettingsView, error) {
	// GetEventConfig self-heals a missing config row.
	if _, err := u.GetEventConfig(ctx, eventID); err != nil {
		return ResultsSettingsView{}, err
	}
	cfg, err := u.configs.Get(ctx, eventID)
	if err != nil {
		return ResultsSettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	return u.resultsSettingsView(ctx, cfg)
}

// UpdateResultsSettings saves the results page settings and the scoreboard
// visibility; live clients reload since the freeze or the presentation moved.
func (u *EventUseCase) UpdateResultsSettings(ctx context.Context, eventID uuid.UUID, visibility eventConfigModel.Visibility, in eventConfigModel.ResultsSettings, by uuid.UUID) (ResultsSettingsView, error) {
	now := time.Now()
	cfg, err := u.mutateEventConfig(ctx, eventID, func(c *eventConfigModel.EventConfig) error {
		return c.SetResultsSettings(visibility, in, now, by)
	})
	if err != nil {
		return ResultsSettingsView{}, err
	}
	if err = u.announceScoreboardReload(ctx, eventID, now); err != nil {
		return ResultsSettingsView{}, err
	}
	return u.resultsSettingsView(ctx, cfg)
}

// SetResultsOpened ends the freeze («Відкрити підсумки») or restores it.
// Opening announces participant.event.results_published (participants and
// managers, Activity), but only while the participants can see the results.
func (u *EventUseCase) SetResultsOpened(ctx context.Context, eventID uuid.UUID, opened bool, by uuid.UUID) (ResultsSettingsView, error) {
	now := time.Now()
	var newlyOpened bool
	cfg, err := u.mutateEventConfig(ctx, eventID, func(c *eventConfigModel.EventConfig) error {
		newlyOpened = opened && c.Results.OpenedAt == nil
		c.SetResultsOpened(opened, now, by)
		return nil
	})
	if err != nil {
		return ResultsSettingsView{}, err
	}
	if err = u.announceScoreboardReload(ctx, eventID, now); err != nil {
		return ResultsSettingsView{}, err
	}
	if newlyOpened {
		u.publishResultsOpened(ctx, eventID, cfg.ScoreboardVisibility)
	}
	return u.resultsSettingsView(ctx, cfg)
}

// publishResultsOpened is best-effort: the results are already open, so a
// failed announcement is logged, not returned. While the participants cannot
// see the results (unpublished, not started, hidden) the notice is noise.
func (u *EventUseCase) publishResultsOpened(ctx context.Context, eventID uuid.UUID, visibility eventConfigModel.Visibility) {
	if u.signalPublishers == nil || u.uow == nil {
		return
	}
	e, err := u.events.GetByID(ctx, eventID)
	if err == nil && !participantsSeeResults(visibility, e.Lifecycle, time.Now()) {
		return
	}
	if err == nil {
		payload := signalModel.EventNoticePayload{ScopeEventID: e.ID, EventTag: e.Tag, EventName: e.Name, EventURL: u.eventURL(e.Tag)}
		err = u.publishNotice(ctx, signalModel.TypeParticipantEventResultsPublished, &payload, func(context.Context, *mailRepo.Repository) error {
			return nil
		})
	}
	if err != nil {
		log.Error().Err(err).Str("event_id", eventID.String()).Msg("Failed to announce opened results")
	}
}

// participantsSeeResults: the event is running or finished and its results
// are not hidden, so participants read the board now.
func participantsSeeResults(visibility eventConfigModel.Visibility, lifecycle eventModel.Lifecycle, now time.Time) bool {
	if visibility == eventConfigModel.VisibilityHidden {
		return false
	}
	status := lifecycle.Status(now)
	return status == eventModel.LifecycleStarted || status == eventModel.LifecycleFinished
}

func (u *EventUseCase) announceScoreboardReload(ctx context.Context, eventID uuid.UUID, now time.Time) error {
	if _, err := u.results.Advance(ctx, eventID, eventResultRepo.Change{Kind: eventResultRepo.ChangeScoreboardRecalculated, Payload: json.RawMessage(`{}`), CreatedAt: now.UTC()}); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to record result change").Err()
	}
	return nil
}

func (u *EventUseCase) resultsSettingsView(ctx context.Context, cfg eventConfigModel.EventConfig) (ResultsSettingsView, error) {
	finish, err := u.eventFinish(ctx, cfg.EventID)
	if err != nil {
		return ResultsSettingsView{}, err
	}
	s := cfg.Results
	return ResultsSettingsView{ScoreboardVisibility: cfg.ScoreboardVisibility, FreezeEnabled: s.FreezeEnabled, FreezeMinutes: s.FreezeMinutes, FreezeEndMode: s.FreezeEndMode.OrDefault(), LiveFreeze: s.LiveFreeze,
		FreezeBannerText: s.FreezeBannerText, FreezeBannerLevel: eventConfigModel.FreezeBannerLevelOrDefault(s.FreezeBannerLevel),
		ChartEnabled: s.ChartEnabled, ChartTeams: s.ChartTeams, RowsLimit: s.RowsLimit, OpenedAt: s.OpenedAt, Freeze: freezeView(s, finish, time.Now())}, nil
}

func (u *EventUseCase) eventFinish(ctx context.Context, eventID uuid.UUID) (*time.Time, error) {
	lifecycle, err := u.eventLifecycle(ctx, eventID)
	if err != nil {
		return nil, err
	}
	return lifecycle.EffectiveFinishAt(), nil
}

func (u *EventUseCase) eventLifecycle(ctx context.Context, eventID uuid.UUID) (eventModel.Lifecycle, error) {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.Lifecycle{}, eventModel.ErrEventNotFound.Err()
		}
		return eventModel.Lifecycle{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	return e.Lifecycle, nil
}

// GetManageResults is the moderators' own live table, independent of the
// public visibility and the freeze. Hidden and not admitted teams are listed
// with marks and without a rank; the moderators team is listed too, as a hidden
// team without a name.
func (u *EventUseCase) GetManageResults(ctx context.Context, eventID uuid.UUID) (ManageResultsView, error) {
	now := time.Now()
	cfg, err := u.configs.Get(ctx, eventID)
	if err != nil {
		return ManageResultsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	lifecycle, err := u.eventLifecycle(ctx, eventID)
	if err != nil {
		return ManageResultsView{}, err
	}
	finish := lifecycle.EffectiveFinishAt()
	revision, err := u.results.CurrentRevision(ctx, eventID)
	if err != nil {
		return ManageResultsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event result revision").Err()
	}
	rows, err := u.scoreboard.ListManage(ctx, eventID)
	if err != nil {
		return ManageResultsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event results").Err()
	}
	solves, err := u.scoreboard.ListManageSolves(ctx, eventID)
	if err != nil {
		return ManageResultsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event solves").Err()
	}
	hints, err := u.scoreboard.HintTotals(ctx, eventID)
	if err != nil {
		return ManageResultsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count event hints").Err()
	}
	byTeam := make(map[uuid.UUID][]ManageResultsSolveView, len(rows))
	for _, solve := range solves {
		byTeam[solve.TeamID] = append(byTeam[solve.TeamID], ManageResultsSolveView{ChallengeID: solve.ChallengeID, ChallengeName: solve.ChallengeName,
			Points: solve.Points, SolvedAt: solve.SolvedAt, FirstBlood: solve.FirstBlood})
	}
	generatedAt := revision.UpdatedAt
	if generatedAt.IsZero() {
		generatedAt = now.UTC()
	}
	view := ManageResultsView{Revision: revision.Revision, GeneratedAt: generatedAt, Freeze: freezeView(cfg.Results, finish, now),
		ParticipantsSee: participantsSeeResults(cfg.ScoreboardVisibility, lifecycle, now), Teams: make([]ManageResultsTeamView, 0, len(rows))}
	var rank int32
	for _, row := range rows {
		team := ManageResultsTeamView{TeamID: row.TeamID, Name: row.PublicName, RealName: row.RealName, Individual: row.Individual, Hidden: row.Hidden, Admitted: row.Admitted, Moderators: row.Moderators,
			Points: row.Points, Solved: row.Solved, LastSolveAt: row.LastSolveAt,
			Hints: hints[row.TeamID].Hints, HintPoints: hints[row.TeamID].Charged, Solves: byTeam[row.TeamID]}
		if row.Moderators {
			team.Name, team.RealName = "", ""
		}
		if team.Solves == nil {
			team.Solves = []ManageResultsSolveView{}
		}
		if row.Pseudonym != "" {
			pseudonym := row.Pseudonym
			team.Pseudonym = &pseudonym
		}
		switch {
		case row.Hidden:
			view.Counts.Hidden++
		case !row.Admitted:
			view.Counts.NotAdmitted++
		default:
			rank++
			ranked := rank
			team.Rank = &ranked
			view.Counts.Ranked++
		}
		view.Teams = append(view.Teams, team)
	}
	return view, nil
}

// AnnulSolve rejects, in one transaction, every accepted attempt of one team
// on one challenge with the same reason. The solve disappears, scores are
// recomputed and live clients reload. Each rejection stays in the audit trail.
func (u *EventUseCase) AnnulSolve(ctx context.Context, eventID, teamID, challengeID uuid.UUID, reason string, by uuid.UUID) (AnnulSolveView, error) {
	if u.uow == nil {
		return AnnulSolveView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	now := time.Now()
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return AnnulSolveView{}, err
	}
	defer unit.Restore()
	// The team must be this event's before any row is locked: a team of another event gets the same answer as a
	// team without a solve, and its admission and lab rows are never held by this event's managers. The check is a
	// plain read, so the lock order (lab, then team challenge) stays as everywhere else.
	if _, err = eventTeamRepo.New(txRepo).GetByID(txCtx, eventID, teamID); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return AnnulSolveView{}, challengeAttemptModel.ErrNothingToAnnul.Err()
		}
		return AnnulSolveView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get team").Err()
	}
	if _, err = lockChallengeLabInTransaction(txCtx, txRepo, teamID, challengeID); err != nil {
		return AnnulSolveView{}, err
	}
	attempts := challengeAttemptRepo.New(txRepo)
	// A missing team challenge has nothing accepted either.
	var accepted []uuid.UUID
	teamChallengeID, err := attempts.LockTeamChallenge(txCtx, eventID, teamID, challengeID)
	switch {
	case err == nil:
		if accepted, err = attempts.EffectiveCorrectAttempts(txCtx, teamChallengeID); err != nil {
			return AnnulSolveView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list accepted attempts").Err()
		}
	case !repositoryTools.IsObjectNotFoundError(err):
		return AnnulSolveView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to lock team challenge").Err()
	}
	if len(accepted) == 0 {
		return AnnulSolveView{}, challengeAttemptModel.ErrNothingToAnnul.Err()
	}
	for _, attemptID := range accepted {
		decision, decisionErr := challengeAttemptModel.NewManualDecision(attemptID, challengeAttemptModel.DecisionRejected, reason, by, now)
		if decisionErr != nil {
			return AnnulSolveView{}, decisionErr
		}
		if err = attempts.RecordDecision(txCtx, decision); err != nil {
			return AnnulSolveView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record challenge attempt decision").Err()
		}
	}
	effective, err := attempts.EffectiveSolvedAt(txCtx, teamChallengeID)
	if err != nil {
		return AnnulSolveView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to calculate challenge result").Err()
	}
	if _, _, err = attempts.RefreshSolvedProjectionValue(txCtx, teamChallengeID, effective, nil); err != nil {
		return AnnulSolveView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to recalculate challenge result").Err()
	}
	// Removing a solve can move dynamic points of other solves: reload all.
	if err = recordScoreboardRecalculation(txCtx, txRepo, eventID); err != nil {
		return AnnulSolveView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record result change").Err()
	}
	// An annulled solve relocks the labs that need it as prerequisite.
	if err = u.requestLabAccessSyncInTransaction(txCtx, txRepo, teamID, now); err != nil {
		return AnnulSolveView{}, err
	}
	if err = unit.Save(); err != nil {
		return AnnulSolveView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to annul solve").Err()
	}
	return AnnulSolveView{TeamID: teamID, ChallengeID: challengeID, Rejected: len(accepted)}, nil
}

// GetSolutionAttemptsStamp is the change marker of the live attempts journal.
func (u *EventUseCase) GetSolutionAttemptsStamp(ctx context.Context, eventID uuid.UUID) (SolutionAttemptsStampView, error) {
	stamp, err := u.attempts.Stamp(ctx, eventID)
	if err != nil {
		return SolutionAttemptsStampView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read attempts journal state").Err()
	}
	return SolutionAttemptsStampView{Attempts: stamp.Attempts, Decisions: stamp.Decisions, HintUnlocks: stamp.HintUnlocks}, nil
}
