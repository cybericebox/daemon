package exercise

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
)

const (
	proposalPending  int16 = 0
	proposalApproved int16 = 1
	proposalRejected int16 = 2
)

// SetExerciseAccess changes which events may use a catalog exercise (admin
// route). The identity write goes first under the optimistic lock; the
// selected-events set follows.
func (u *ExerciseUseCase) SetExerciseAccess(ctx context.Context, actor Actor, id uuid.UUID, in SetAccessInput) (ExerciseView, error) {
	eventIDs := uniqueIDs(in.EventIDs)
	e, err := u.mutateExercise(ctx, id, func(e *exerciseModel.Exercise) error {
		if err := e.EnsureNotArchived(); err != nil {
			return err
		}
		return e.SetAccess(in.AccessLevel, len(eventIDs) > 0, in.UpdatedBy, time.Now())
	})
	if err != nil {
		return ExerciseView{}, err
	}
	if in.AccessLevel != exerciseModel.AccessSelectedEvents {
		eventIDs = nil
	}
	if err = u.exercises.ReplaceAccessEvents(ctx, e.ID, eventIDs); err != nil {
		if repositoryTools.IsForeignKeyViolation(err) {
			return ExerciseView{}, exerciseModel.ErrExerciseAccessInvalid.Err()
		}
		return ExerciseView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save exercise access").Err()
	}
	view, err := u.exerciseView(ctx, e)
	if err != nil {
		return ExerciseView{}, err
	}
	return u.decorateView(ctx, actor, view)
}

// ProposeExercise asks the platform to add a published event exercise to the
// catalog. Authorization (publish right on the exercise) happened in the
// route; one pending proposal per exercise.
func (u *ExerciseUseCase) ProposeExercise(ctx context.Context, actor Actor, id uuid.UUID, note string) (ProposalView, error) {
	e, err := u.loadEditable(ctx, id)
	if err != nil {
		return ProposalView{}, err
	}
	if !e.IsEventScoped() || !e.OwnerEventID.Valid || !e.PublishedVersionID.Valid {
		return ProposalView{}, exerciseModel.ErrExerciseProposalInvalid.Err()
	}
	created, err := u.exercises.CreateProposal(ctx, exerciseRepo.Proposal{
		ID: uuid.Must(uuid.NewV7()), ExerciseID: e.ID, EventID: e.OwnerEventID, Note: truncateRunes(note, 2000),
		ProposedBy: uuid.NullUUID{UUID: actor.UserID, Valid: actor.UserID != uuid.Nil}, ProposedAt: time.Now(),
	})
	if err != nil {
		if _, ok := repositoryTools.UniqueViolationError(err, exerciseModel.ErrExerciseProposalInvalid); ok {
			return ProposalView{}, exerciseModel.ErrExerciseProposalInvalid.Err()
		}
		return ProposalView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create exercise proposal").Err()
	}
	created.ExerciseName = e.Name
	u.notifyProposal(ctx, created, func(p inboxUseCase.Proposal) error {
		return u.proposals.ProposalSubmitted(ctx, p)
	})
	return toProposalView(created), nil
}

// ListProposals lists catalog proposals (admin route); status "" = all.
func (u *ExerciseUseCase) ListProposals(ctx context.Context, status string) ([]ProposalView, error) {
	var filter *int16
	switch status {
	case "pending":
		value := proposalPending
		filter = &value
	case "approved":
		value := proposalApproved
		filter = &value
	case "rejected":
		value := proposalRejected
		filter = &value
	}
	rows, err := u.exercises.ListProposals(ctx, filter)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list exercise proposals").Err()
	}
	out := make([]ProposalView, 0, len(rows))
	for _, row := range rows {
		out = append(out, toProposalView(row))
	}
	return out, nil
}

// ApproveProposal copies the source's published version into a new catalog
// exercise with the chosen access level (the event keeps its own exercise),
// then marks the proposal approved. A failure after the copy was created
// removes the copy again.
func (u *ExerciseUseCase) ApproveProposal(ctx context.Context, actor Actor, proposalID uuid.UUID, in ApproveProposalInput) (ProposalView, error) {
	proposal, err := u.pendingProposal(ctx, proposalID)
	if err != nil {
		return ProposalView{}, err
	}
	source, err := u.exercises.GetByID(ctx, proposal.ExerciseID)
	if err != nil {
		return ProposalView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get proposed exercise").Err()
	}
	if !source.PublishedVersionID.Valid {
		return ProposalView{}, exerciseModel.ErrExerciseProposalInvalid.Err()
	}
	published, err := u.exercises.GetVersion(ctx, source.PublishedVersionID.UUID)
	if err != nil {
		return ProposalView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get proposed version").Err()
	}
	now := time.Now()
	eventIDs := uniqueIDs(in.EventIDs)
	copyEntity, err := exerciseModel.NewCatalogCopy(source, in.Name, in.AccessLevel, len(eventIDs) > 0, actor.UserID, now)
	if err != nil {
		return ProposalView{}, err
	}
	created, err := u.exercises.Create(ctx, copyEntity)
	if err != nil {
		return ProposalView{}, classifyExerciseWriteError(err, "create")
	}
	rollback := func(cause error) (ProposalView, error) {
		if _, deleteErr := u.exercises.Delete(ctx, created.ID); deleteErr != nil {
			return ProposalView{}, errors.Join(cause, deleteErr)
		}
		return ProposalView{}, cause
	}
	if in.AccessLevel == exerciseModel.AccessSelectedEvents {
		if err = u.exercises.ReplaceAccessEvents(ctx, created.ID, eventIDs); err != nil {
			return rollback(model.ErrPlatform.WithError(err).WithMessage("Failed to save exercise access").Err())
		}
	}
	version := exerciseModel.ExerciseVersion{
		ID: uuid.Must(uuid.NewV7()), ExerciseID: created.ID, Status: exerciseModel.VersionStatusPublished,
		AdminNote: published.AdminNote, Variants: published.Variants, CreatedAt: now,
		CreatedBy: uuid.NullUUID{UUID: actor.UserID, Valid: actor.UserID != uuid.Nil}, PublishedAt: &now,
	}
	if _, err = u.exercises.InsertImportedVersion(ctx, version); err != nil {
		return rollback(model.ErrPlatform.WithError(err).WithMessage("Failed to copy exercise version").Err())
	}
	if err = u.exercises.SetImportedPointers(ctx, created.ID, uuid.NullUUID{}, uuid.NullUUID{UUID: version.ID, Valid: true}); err != nil {
		return rollback(model.ErrPlatform.WithError(err).WithMessage("Failed to finalize exercise copy").Err())
	}
	decidedAt := now
	proposal.Status, proposal.DecidedAt, proposal.DecisionNote = proposalApproved, &decidedAt, truncateRunes(in.Note, 2000)
	proposal.DecidedBy = uuid.NullUUID{UUID: actor.UserID, Valid: actor.UserID != uuid.Nil}
	proposal.CatalogExerciseID = uuid.NullUUID{UUID: created.ID, Valid: true}
	if affected, decideErr := u.exercises.DecideProposal(ctx, proposal); decideErr != nil {
		return rollback(model.ErrPlatform.WithError(decideErr).WithMessage("Failed to approve exercise proposal").Err())
	} else if affected == 0 {
		return rollback(exerciseModel.ErrExerciseProposalDecided.Err())
	}
	if err = u.media.ReplaceReferences(ctx, mediaModel.RefTypeExerciseVersion, version.ID, exerciseModel.CollectFileIDs(version.Variants)); err != nil {
		return ProposalView{}, err
	}
	proposal.ExerciseName = source.Name
	u.notifyProposal(ctx, proposal, func(p inboxUseCase.Proposal) error {
		return u.proposals.ProposalDecided(ctx, p, true, actor.UserID)
	})
	return toProposalView(proposal), nil
}

// RejectProposal closes a pending proposal with an optional note.
func (u *ExerciseUseCase) RejectProposal(ctx context.Context, actor Actor, proposalID uuid.UUID, note string) (ProposalView, error) {
	proposal, err := u.pendingProposal(ctx, proposalID)
	if err != nil {
		return ProposalView{}, err
	}
	now := time.Now()
	proposal.Status, proposal.DecidedAt, proposal.DecisionNote = proposalRejected, &now, truncateRunes(note, 2000)
	proposal.DecidedBy = uuid.NullUUID{UUID: actor.UserID, Valid: actor.UserID != uuid.Nil}
	affected, err := u.exercises.DecideProposal(ctx, proposal)
	if err != nil {
		return ProposalView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to reject exercise proposal").Err()
	}
	if affected == 0 {
		return ProposalView{}, exerciseModel.ErrExerciseProposalDecided.Err()
	}
	if source, sourceErr := u.exercises.GetByID(ctx, proposal.ExerciseID); sourceErr == nil {
		proposal.ExerciseName = source.Name
	}
	u.notifyProposal(ctx, proposal, func(p inboxUseCase.Proposal) error {
		return u.proposals.ProposalDecided(ctx, p, false, actor.UserID)
	})
	return toProposalView(proposal), nil
}

// notifyProposal reports a proposal change to the inbox after it is stored.
// It is best-effort: the proposal itself already succeeded, so a delivery
// failure is logged, not returned.
func (u *ExerciseUseCase) notifyProposal(ctx context.Context, p exerciseRepo.Proposal, send func(inboxUseCase.Proposal) error) {
	if u.proposals == nil {
		return
	}
	notice := inboxUseCase.Proposal{
		ID: p.ID, ExerciseID: p.ExerciseID, ExerciseName: p.ExerciseName, ProposedBy: p.ProposedBy.UUID, ProposedAt: p.ProposedAt,
		Note: p.Note, DecisionNote: p.DecisionNote, CatalogExerciseID: uuidPtr(p.CatalogExerciseID),
	}
	if err := send(notice); err != nil {
		log.Error().Err(err).Str("proposal_id", p.ID.String()).Msg("Failed to update exercise proposal inbox")
	}
}

func (u *ExerciseUseCase) pendingProposal(ctx context.Context, id uuid.UUID) (exerciseRepo.Proposal, error) {
	proposal, err := u.exercises.GetProposal(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return exerciseRepo.Proposal{}, exerciseModel.ErrExerciseProposalNotFound.Err()
		}
		return exerciseRepo.Proposal{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise proposal").Err()
	}
	if proposal.Status != proposalPending {
		return exerciseRepo.Proposal{}, exerciseModel.ErrExerciseProposalDecided.Err()
	}
	return proposal, nil
}

func toProposalView(p exerciseRepo.Proposal) ProposalView {
	status := "pending"
	switch p.Status {
	case proposalApproved:
		status = "approved"
	case proposalRejected:
		status = "rejected"
	}
	return ProposalView{ID: p.ID, ExerciseID: p.ExerciseID, ExerciseName: p.ExerciseName, EventID: uuidPtr(p.EventID), EventName: p.EventName,
		Status: status, Note: p.Note, ProposedBy: uuidPtr(p.ProposedBy), ProposedByName: p.ProposedByName, ProposedAt: p.ProposedAt,
		DecidedAt: p.DecidedAt, DecisionNote: p.DecisionNote, CatalogExerciseID: uuidPtr(p.CatalogExerciseID)}
}

func uniqueIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// AuthorizeTestDeploy: write access to the exercise, the version belongs to
// it, and an event exercise's owner event allows infrastructure.
func (u *ExerciseUseCase) AuthorizeTestDeploy(ctx context.Context, actor Actor, exerciseID, versionID uuid.UUID) error {
	if _, err := u.AuthorizeExercise(ctx, actor, exerciseID, ActionWrite); err != nil {
		return err
	}
	if _, err := u.GetVersion(ctx, exerciseID, versionID); err != nil {
		return err
	}
	e, err := u.exercises.GetByID(ctx, exerciseID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise").Err()
	}
	if !e.IsEventScoped() {
		return nil
	}
	if !e.OwnerEventID.Valid {
		return exerciseModel.ErrExerciseInfrastructureNotAllowed.Err()
	}
	allowed, err := u.eventInfrastructure(ctx, e.OwnerEventID.UUID)
	if err != nil {
		return err
	}
	if !allowed {
		return exerciseModel.ErrExerciseInfrastructureNotAllowed.Err()
	}
	return nil
}
