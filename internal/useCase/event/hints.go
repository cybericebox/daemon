package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

// UnlockHint opens one hint of a board challenge for the caller's whole
// team. Idempotent per team: a repeat returns the first unlock. The cost is
// fixed at unlock time — the effective event cost, or 0 once the team solved
// the challenge or the event finished. In balance mode a paid unlock changes
// the scoreboard at once.
func (u *EventUseCase) UnlockHint(ctx context.Context, eventID, userID, challengeID, hintID uuid.UUID) (OwnHintView, error) {
	if u.uow == nil {
		return OwnHintView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	now := time.Now()
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return OwnHintView{}, err
	}
	defer unit.Restore()
	p, err := participantRepo.New(txRepo).Get(txCtx, eventID, userID)
	if err != nil {
		return OwnHintView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if p.Status != participantModel.StatusApproved || p.TeamID == nil {
		return OwnHintView{}, participantModel.ErrParticipantNotApproved.Err()
	}
	if err = requireTeamAdmitted(txCtx, eventTeamRepo.New(txRepo), eventID, *p.TeamID); err != nil {
		return OwnHintView{}, err
	}
	e, err := eventRepo.New(txRepo).GetByID(txCtx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return OwnHintView{}, eventModel.ErrEventNotFound.Err()
		}
		return OwnHintView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	status := e.Lifecycle.Status(now)
	if status != eventModel.LifecycleStarted && status != eventModel.LifecycleFinished {
		return OwnHintView{}, eventModel.ErrEventRuntimeNotOpen.Err()
	}
	config, err := eventConfigRepo.New(txRepo).Get(txCtx, eventID)
	if err != nil {
		return OwnHintView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	teamChallenges := teamChallengeRepo.New(txRepo)
	state, err := teamChallenges.HintState(txCtx, *p.TeamID, challengeID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return OwnHintView{}, eventChallengeModel.ErrEventChallengeNotFound.Err()
		}
		return OwnHintView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get team challenge").Err()
	}
	if state.EventID != eventID || state.Readiness != teamChallengeModel.ReadinessPublished || !state.Published {
		return OwnHintView{}, eventChallengeModel.ErrEventChallengeNotFound.Err()
	}
	if !state.HintsEnabled || config.HintsDisabled {
		return OwnHintView{}, eventChallengeModel.ErrEventChallengeHintsDisabled.Err()
	}
	prerequisites, err := teamChallenges.Prerequisites(txCtx, *p.TeamID)
	if err != nil {
		return OwnHintView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list challenge prerequisites").Err()
	}
	solved := make([]bool, 0)
	for _, prerequisite := range prerequisites[challengeID] {
		solved = append(solved, prerequisite.Solved)
	}
	if teamChallengeModel.PrerequisitesLock(solved) {
		return OwnHintView{}, teamChallengeModel.ErrTeamChallengePrerequisites.Err()
	}
	board := eventChallengeModel.EventChallenge{Hints: state.BoardHints, HintCosts: state.HintCosts}
	cost, found := board.HintCost(hintID)
	text, hasText := teamChallengeModel.HintText(state.TeamHints, hintID)
	if !found || !hasText {
		return OwnHintView{}, eventChallengeModel.ErrEventChallengeHintNotFound.Err()
	}
	if state.Solved || status == eventModel.LifecycleFinished {
		cost = 0
	}
	unlock, err := teamChallenges.Unlock(txCtx, state, hintID, userID, now, cost)
	if err != nil {
		return OwnHintView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to unlock hint").Err()
	}
	if unlock.Created && unlock.Cost > 0 && config.HintChargeMode == eventConfigModel.HintChargeBalance {
		if err = recordScoreboardRecalculation(txCtx, txRepo, eventID); err != nil {
			return OwnHintView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record result change").Err()
		}
	}
	if err = unit.Save(); err != nil {
		return OwnHintView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to unlock hint").Err()
	}
	at := unlock.UnlockedAt
	view := OwnHintView{ID: hintID, Cost: unlock.Cost, Unlocked: true, Content: &text, UnlockedAt: &at}
	for _, hint := range state.BoardHints {
		if hint.ID == hintID {
			view.Level = string(hint.Level)
		}
	}
	return view, nil
}

type hintUnlockKey struct {
	challengeID, hintID uuid.UUID
}

func (u *EventUseCase) teamUnlocks(ctx context.Context, teamID uuid.UUID) (map[hintUnlockKey]teamChallengeRepo.HintUnlock, error) {
	rows, err := u.teamChallenges.TeamUnlocks(ctx, teamID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list hint unlocks").Err()
	}
	out := make(map[hintUnlockKey]teamChallengeRepo.HintUnlock, len(rows))
	for _, row := range rows {
		out[hintUnlockKey{row.EventChallengeID, row.HintID}] = row
	}
	return out, nil
}

// boardHints projects a challenge's hints for a board: the cost a team pays
// now (paid cost once unlocked), the text only when unlocked or revealed
// (moderators board). Hidden when hints are disabled or the challenge is
// locked.
func boardHints(row teamChallengeRepo.PublishedChallenge, unlocks map[hintUnlockKey]teamChallengeRepo.HintUnlock, reveal, locked bool) ([]OwnHintView, int32) {
	out := make([]OwnHintView, 0)
	if !row.HintsEnabled || locked {
		return out, 0
	}
	board := eventChallengeModel.EventChallenge{Hints: row.BoardHints, HintCosts: row.HintCosts}
	total := int32(0)
	for _, hint := range row.BoardHints {
		cost, _ := board.HintCost(hint.ID)
		view := OwnHintView{ID: hint.ID, Level: string(hint.Level), Cost: cost}
		text, hasText := teamChallengeModel.HintText(row.Challenge.Hints, hint.ID)
		if unlock, ok := unlocks[hintUnlockKey{row.Challenge.EventChallengeID, hint.ID}]; ok {
			at := unlock.UnlockedAt
			view.Unlocked, view.UnlockedAt, view.UnlockedByName, view.Cost = true, &at, unlock.UnlockedByName, unlock.Cost
			total += unlock.Cost
		}
		if hasText && (view.Unlocked || reveal) {
			content := text
			view.Content = &content
		}
		out = append(out, view)
	}
	return out, total
}
