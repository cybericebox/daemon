package event

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventChallengeGroupRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventChallengeRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventExerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	eventChallengeGroupModel "github.com/cybericebox/daemon/internal/model/eventChallengeGroup"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
)

// ListEventChallenges returns the immutable board snapshot for one explicit
// event-local bundle revision. Looking up the attachment first prevents a
// manager of event A from probing challenge ids belonging to event B.
func (u *EventUseCase) ListEventChallenges(ctx context.Context, eventID, eventExerciseID uuid.UUID) ([]EventChallengeView, error) {
	if _, err := u.eventExercises.GetByID(ctx, eventID, eventExerciseID); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, eventExerciseModel.ErrEventExerciseNotFound.Err()
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event exercise").Err()
	}
	rows, err := u.eventChallenges.List(ctx, eventExerciseID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
	}
	availability, err := u.teamChallenges.AvailabilityForExercise(ctx, eventExerciseID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event challenge availability").Err()
	}
	prerequisites, err := u.eventChallenges.PrerequisitesByChallenge(ctx, eventExerciseID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenge prerequisites").Err()
	}
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	items := make([]EventChallengeView, 0, len(rows))
	for _, row := range rows {
		prerequisiteIDs := prerequisites[row.ID]
		if prerequisiteIDs == nil {
			prerequisiteIDs = []uuid.UUID{}
		}
		view := toEventChallengeView(row, prerequisiteIDs)
		view.EffectivePoints = e.EffectiveStaticPoints(row.Points, row.ScoringOverride)
		if status, found := availability[row.ID]; found {
			view.Availability = ChallengeAvailability{Preparing: status.Preparing, Ready: status.Ready, Available: status.Available, Failed: status.Failed, Total: status.Total}
		}
		items = append(items, view)
	}
	return items, nil
}

// BulkUpdateChallengeScoring replaces (or clears) a complete local scoring
// override for a selected set. All ownership checks happen before the first
// write, inside one transaction, so a foreign or stale ID changes nothing.
func (u *EventUseCase) BulkUpdateChallengeScoring(ctx context.Context, eventID, eventExerciseID uuid.UUID, in BulkUpdateChallengeScoringInput) error {
	if len(in.ChallengeIDs) == 0 {
		return eventChallengeModel.ErrEventChallengeNotFound.Err()
	}
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	attachment, err := eventExerciseRepo.New(txRepo).GetByID(txCtx, eventID, eventExerciseID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventExerciseModel.ErrEventExerciseNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event exercise").Err()
	}
	if attachment.Status != eventExerciseModel.StatusActive {
		return eventExerciseModel.ErrEventExerciseNotActive.Err()
	}
	e, err := eventRepo.New(txRepo).GetByID(txCtx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.ErrEventNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if in.Override != nil {
		normalized := in.Override.Normalized()
		in.Override = &normalized
		if err = in.Override.ValidateFor(e.Lifecycle); err != nil {
			return err
		}
	}
	challenges := eventChallengeRepo.New(txRepo)
	items := make([]eventChallengeModel.EventChallenge, 0, len(in.ChallengeIDs))
	seen := make(map[uuid.UUID]struct{}, len(in.ChallengeIDs))
	for _, id := range in.ChallengeIDs {
		if _, duplicate := seen[id]; duplicate {
			return eventChallengeModel.ErrEventChallengeNotFound.Err()
		}
		seen[id] = struct{}{}
		item, getErr := challenges.GetByID(txCtx, eventExerciseID, id)
		if getErr != nil {
			if repositoryTools.IsObjectNotFoundError(getErr) {
				return eventChallengeModel.ErrEventChallengeNotFound.Err()
			}
			return model.ErrPlatform.WithError(getErr).WithMessage("Failed to get event challenge").Err()
		}
		items = append(items, item)
	}
	for i := range items {
		items[i].SetScoringOverride(in.Override)
		if _, err = challenges.UpdateScoring(txCtx, items[i]); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to update event challenge scoring").Err()
		}
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update event challenge scoring").Err()
	}
	return nil
}

// UpdateEventChallenge changes only event-owned presentation and scoring
// switches. A superseded bundle is deliberately read-only: changing history
// after a correction would invalidate already-started teams' evidence.
func (u *EventUseCase) UpdateEventChallenge(ctx context.Context, eventID, eventExerciseID, challengeID uuid.UUID, in UpdateEventChallengeInput) (EventChallengeView, error) {
	attachment, err := u.eventExercises.GetByID(ctx, eventID, eventExerciseID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventChallengeView{}, eventExerciseModel.ErrEventExerciseNotFound.Err()
		}
		return EventChallengeView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event exercise").Err()
	}
	if attachment.Status != eventExerciseModel.StatusActive {
		return EventChallengeView{}, eventExerciseModel.ErrEventExerciseNotActive.Err()
	}
	challenge, err := u.eventChallenges.GetByID(ctx, eventExerciseID, challengeID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventChallengeView{}, eventChallengeModel.ErrEventChallengeNotFound.Err()
		}
		return EventChallengeView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event challenge").Err()
	}
	if err = challenge.SetPoints(in.Points); err != nil {
		return EventChallengeView{}, err
	}
	challenge.SetHintsEnabled(in.HintsEnabled)
	if err = challenge.SetMaxFlagAttempts(in.MaxFlagAttempts.Or(challenge.MaxFlagAttempts)); err != nil {
		return EventChallengeView{}, err
	}
	// Visibility is per set (SetEventExerciseVisibility); a task keeps it.
	updated, err := u.eventChallenges.Update(ctx, challenge)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventChallengeView{}, eventChallengeModel.ErrEventChallengeNotFound.Err()
		}
		return EventChallengeView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to update event challenge").Err()
	}
	view := toEventChallengeView(updated)
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		return EventChallengeView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	view.EffectivePoints = e.EffectiveStaticPoints(updated.Points, updated.ScoringOverride)
	return view, nil
}

// publishReadyBoardChallenge makes a manager's board-publish action effective
// at once for teams whose assignment is already ready, under the same rule as
// the stand engine: static challenges open immediately, infrastructure ones
// only after the event's strict barrier opened.
func (u *EventUseCase) publishReadyBoardChallenge(ctx context.Context, eventID, _ uuid.UUID) error {
	return u.publishAvailableChallenges(ctx, eventID)
}

// ReorderEventChallenges assigns a complete manager-provided order to an
// active bundle. Orders are first moved to a disjoint negative range, then
// written back sequentially in one transaction, so swaps cannot trip the
// database uniqueness constraint midway through the operation.
func (u *EventUseCase) ReorderEventChallenges(ctx context.Context, eventID, eventExerciseID uuid.UUID, in ReorderEventChallengesInput) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	attachment, err := eventExerciseRepo.New(txRepo).GetByID(txCtx, eventID, eventExerciseID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventExerciseModel.ErrEventExerciseNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event exercise").Err()
	}
	if attachment.Status != eventExerciseModel.StatusActive {
		return eventExerciseModel.ErrEventExerciseNotActive.Err()
	}
	challenges := eventChallengeRepo.New(txRepo)
	current, err := challenges.List(txCtx, eventExerciseID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
	}
	if !sameChallengeSet(current, in.ChallengeIDs) {
		return eventChallengeModel.ErrEventChallengeOrderInvalid.Err()
	}
	if err = challenges.VacateOrders(txCtx, eventExerciseID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to prepare event challenge order").Err()
	}
	for order, id := range in.ChallengeIDs {
		affected, setErr := challenges.SetOrder(txCtx, eventExerciseID, id, int32(order))
		if setErr != nil {
			return model.ErrPlatform.WithError(setErr).WithMessage("Failed to reorder event challenge").Err()
		}
		if affected != 1 {
			return eventChallengeModel.ErrEventChallengeOrderInvalid.Err()
		}
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to reorder event challenges").Err()
	}
	return nil
}

// ReorderGroupChallenges sets the board order inside one group across the
// event's active sets: the list must name every challenge of the group once.
// The participant board reads the same order.
func (u *EventUseCase) ReorderGroupChallenges(ctx context.Context, eventID uuid.UUID, in ReorderGroupChallengesInput) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	challenges := eventChallengeRepo.New(txRepo)
	current, err := challenges.GroupChallengeIDs(txCtx, eventID, in.GroupID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list group challenges").Err()
	}
	if !sameIDSet(current, in.ChallengeIDs) {
		return eventChallengeModel.ErrEventChallengeBoardOrderInvalid.Err()
	}
	for order, id := range in.ChallengeIDs {
		affected, setErr := challenges.SetBoardOrder(txCtx, eventID, id, int32(order))
		if setErr != nil {
			return model.ErrPlatform.WithError(setErr).WithMessage("Failed to reorder group challenge").Err()
		}
		if affected != 1 {
			return eventChallengeModel.ErrEventChallengeBoardOrderInvalid.Err()
		}
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to reorder group challenges").Err()
	}
	return nil
}

func sameIDSet(current, proposed []uuid.UUID) bool {
	if len(current) != len(proposed) {
		return false
	}
	known := make(map[uuid.UUID]struct{}, len(current))
	for _, id := range current {
		known[id] = struct{}{}
	}
	for _, id := range proposed {
		if _, ok := known[id]; !ok {
			return false
		}
		delete(known, id)
	}
	return len(known) == 0
}

// SetEventExerciseVisibility shows or hides a whole set on the participant
// board: its tasks share one infrastructure, so a set is all-or-nothing.
func (u *EventUseCase) SetEventExerciseVisibility(ctx context.Context, eventID, eventExerciseID uuid.UUID, published bool) error {
	link, err := u.activeAttachment(ctx, eventID, eventExerciseID)
	if err != nil {
		return err
	}
	board, err := u.eventChallenges.List(ctx, link.ID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
	}
	changed := false
	for _, challenge := range board {
		changed = changed || challenge.Published != published
	}
	if !changed {
		return nil
	}
	if err = u.eventChallenges.SetPublishedForExercise(ctx, link.ID, published); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update set visibility").Err()
	}
	if published {
		if err = u.publishReadyBoardChallenge(ctx, eventID, link.ID); err != nil {
			return err
		}
	}
	if u.supportsLabAccessPolicy() {
		return u.RequestEventLabAccessSyncs(ctx, eventID)
	}
	return nil
}

func sameChallengeSet(current []eventChallengeModel.EventChallenge, proposed []uuid.UUID) bool {
	if len(current) != len(proposed) {
		return false
	}
	known := make(map[uuid.UUID]struct{}, len(current))
	for _, challenge := range current {
		known[challenge.ID] = struct{}{}
	}
	for _, id := range proposed {
		if _, ok := known[id]; !ok {
			return false
		}
		delete(known, id)
	}
	return len(known) == 0
}

// UpdateEventChallengeRelations atomically assigns an optional event-local
// group and replaces prerequisites. Every referenced challenge must belong to
// the same active bundle, preventing cross-event leakage through IDs.
func (u *EventUseCase) UpdateEventChallengeRelations(ctx context.Context, eventID, eventExerciseID, challengeID uuid.UUID, in UpdateEventChallengeRelationsInput) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	attachment, err := eventExerciseRepo.New(txRepo).GetByID(txCtx, eventID, eventExerciseID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventExerciseModel.ErrEventExerciseNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event exercise").Err()
	}
	if attachment.Status != eventExerciseModel.StatusActive {
		return eventExerciseModel.ErrEventExerciseNotActive.Err()
	}
	challenges := eventChallengeRepo.New(txRepo)
	current, err := challenges.List(txCtx, eventExerciseID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
	}
	known := make(map[uuid.UUID]struct{}, len(current))
	for _, challenge := range current {
		known[challenge.ID] = struct{}{}
	}
	if _, ok := known[challengeID]; !ok {
		return eventChallengeModel.ErrEventChallengeNotFound.Err()
	}
	seen := make(map[uuid.UUID]struct{}, len(in.PrerequisiteIDs))
	for _, id := range in.PrerequisiteIDs {
		if id == challengeID {
			return eventChallengeModel.ErrEventChallengePrerequisitesInvalid.Err()
		}
		if _, ok := known[id]; !ok {
			return eventChallengeModel.ErrEventChallengePrerequisitesInvalid.Err()
		}
		if _, duplicate := seen[id]; duplicate {
			return eventChallengeModel.ErrEventChallengePrerequisitesInvalid.Err()
		}
		seen[id] = struct{}{}
	}
	cyclic, cycleErr := introducesPrerequisiteCycle(txCtx, challenges, current, challengeID, in.PrerequisiteIDs)
	if cycleErr != nil {
		return model.ErrPlatform.WithError(cycleErr).WithMessage("Failed to validate challenge prerequisites").Err()
	}
	if cyclic {
		return eventChallengeModel.ErrEventChallengePrerequisitesCycle.Err()
	}
	if in.GroupID != nil {
		groups, groupErr := eventChallengeGroupRepo.New(txRepo).List(txCtx, eventID)
		if groupErr != nil {
			return model.ErrPlatform.WithError(groupErr).WithMessage("Failed to list challenge groups").Err()
		}
		found := false
		for _, group := range groups {
			if group.ID == *in.GroupID {
				found = true
				break
			}
		}
		if !found {
			return eventChallengeGroupModel.ErrChallengeGroupNotFound.Err()
		}
	}
	if affected, setErr := challenges.SetGroup(txCtx, eventExerciseID, challengeID, in.GroupID); setErr != nil {
		return model.ErrPlatform.WithError(setErr).WithMessage("Failed to assign challenge group").Err()
	} else if affected != 1 {
		return eventChallengeModel.ErrEventChallengeNotFound.Err()
	}
	if err = challenges.ReplacePrerequisites(txCtx, challengeID, in.PrerequisiteIDs); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to replace challenge prerequisites").Err()
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update challenge relations").Err()
	}
	// The lab ACL honours the prerequisite locks, so the teams recompute it.
	if u.supportsLabAccessPolicy() {
		return u.RequestEventLabAccessSyncs(ctx, eventID)
	}
	return nil
}

// introducesPrerequisiteCycle checks only the proposed replacement edge set.
// A cycle is created precisely when any new prerequisite can already reach the
// target challenge through existing prerequisite links.
func introducesPrerequisiteCycle(ctx context.Context, challenges *eventChallengeRepo.Repository, board []eventChallengeModel.EventChallenge, target uuid.UUID, proposed []uuid.UUID) (bool, error) {
	edges := make(map[uuid.UUID][]uuid.UUID, len(board))
	for _, challenge := range board {
		if challenge.ID == target {
			continue
		}
		prerequisites, err := challenges.Prerequisites(ctx, challenge.ID)
		if err != nil {
			return false, err
		}
		edges[challenge.ID] = prerequisites
	}
	for _, prerequisite := range proposed {
		if prerequisiteReachesTarget(edges, prerequisite, target, map[uuid.UUID]bool{}) {
			return true, nil
		}
	}
	return false, nil
}

func prerequisiteReachesTarget(edges map[uuid.UUID][]uuid.UUID, current, target uuid.UUID, visited map[uuid.UUID]bool) bool {
	if current == target {
		return true
	}
	if visited[current] {
		return false
	}
	visited[current] = true
	for _, next := range edges[current] {
		if prerequisiteReachesTarget(edges, next, target, visited) {
			return true
		}
	}
	return false
}

func toEventChallengeView(value eventChallengeModel.EventChallenge, prerequisiteIDs ...[]uuid.UUID) EventChallengeView {
	view := EventChallengeView{ID: value.ID, TaskID: value.TaskID, GroupID: value.GroupID, Order: value.Order, BoardOrder: value.BoardOrder, Points: value.Points, ScoringOverride: value.ScoringOverride, HintsEnabled: value.HintsEnabled, MaxFlagAttempts: value.MaxFlagAttempts, Published: value.Published, Snapshot: value.Snapshot,
		Hints: make([]ChallengeHintView, 0, len(value.Hints))}
	for _, hint := range value.Hints {
		cost, _ := value.HintCost(hint.ID)
		_, overridden := value.HintCosts[hint.ID]
		view.Hints = append(view.Hints, ChallengeHintView{ID: hint.ID, Text: hint.Text, Level: string(hint.Level), Cost: cost, Overridden: overridden})
	}
	if len(prerequisiteIDs) != 0 {
		view.PrerequisiteIDs = prerequisiteIDs[0]
	}
	return view
}

// UpdateChallengeHintCosts overrides (or resets) the cost of a board
// challenge's hints for this event. Existing unlocks keep what they cost.
func (u *EventUseCase) UpdateChallengeHintCosts(ctx context.Context, eventID, eventExerciseID, challengeID uuid.UUID, costs []HintCostInput) (EventChallengeView, error) {
	attachment, err := u.eventExercises.GetByID(ctx, eventID, eventExerciseID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventChallengeView{}, eventExerciseModel.ErrEventExerciseNotFound.Err()
		}
		return EventChallengeView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event exercise").Err()
	}
	if err = attachment.EnsureActive(); err != nil {
		return EventChallengeView{}, err
	}
	challenge, err := u.eventChallenges.GetByID(ctx, eventExerciseID, challengeID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventChallengeView{}, eventChallengeModel.ErrEventChallengeNotFound.Err()
		}
		return EventChallengeView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event challenge").Err()
	}
	next := make(map[uuid.UUID]*int32, len(challenge.HintCosts)+len(costs))
	for id, cost := range challenge.HintCosts {
		value := cost
		next[id] = &value
	}
	for _, item := range costs {
		next[item.HintID] = item.Cost
	}
	if err = challenge.SetHintCosts(next); err != nil {
		return EventChallengeView{}, err
	}
	if affected, setErr := u.eventChallenges.SetHintCosts(ctx, challenge); setErr != nil {
		return EventChallengeView{}, model.ErrPlatform.WithError(setErr).WithMessage("Failed to save hint costs").Err()
	} else if affected == 0 {
		return EventChallengeView{}, eventChallengeModel.ErrEventChallengeNotFound.Err()
	}
	return toEventChallengeView(challenge), nil
}

// ListHintUnlocks shows moderators who unlocked which hint, when and for how
// much (newest first).
func (u *EventUseCase) ListHintUnlocks(ctx context.Context, eventID uuid.UUID) ([]HintUnlockView, error) {
	rows, err := u.teamChallenges.EventUnlocks(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list hint unlocks").Err()
	}
	out := make([]HintUnlockView, 0, len(rows))
	for _, row := range rows {
		var by *uuid.UUID
		if row.UnlockedBy.Valid {
			id := row.UnlockedBy.UUID
			by = &id
		}
		out = append(out, HintUnlockView{TeamID: row.EventTeamID, TeamName: row.TeamName, EventChallengeID: row.EventChallengeID, ChallengeName: row.ChallengeName,
			HintID: row.HintID, HintIndex: row.HintIndex, UnlockedBy: by, UnlockedByName: row.UnlockedByName, UnlockedAt: row.UnlockedAt, Cost: row.Cost})
	}
	return out, nil
}
