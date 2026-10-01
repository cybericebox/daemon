// Package exercise implements the versioned exercise catalog application
// layer: identity CRUD, draft/publish lifecycle and secret handling.
package exercise

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/testDeployRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
	"github.com/cybericebox/daemon/pkg/secret"
)

// IRepository is the narrow data port: the whole exerciseRepo query slice (which
// now includes the list/count/id-projection shapes), so the use case holds no
// postgres.* types. The gomock Querier and the real Queries both satisfy it.
type IRepository interface {
	exerciseRepo.Queries
	testDeployRepo.Queries
}

// IMedia is the media port (satisfied by *media.MediaUseCase).
type IMedia interface {
	ReplaceReferences(ctx context.Context, refType string, refID uuid.UUID, fileIDs []uuid.UUID) error
	RemoveReferences(ctx context.Context, refType string, refID uuid.UUID) error
	RemoveReferencesBatch(ctx context.Context, refType string, refIDs []uuid.UUID) error
}

type ExerciseUseCase struct {
	repo        IRepository
	exercises   *exerciseRepo.Repository
	testDeploys *testDeployRepo.Repository
	deployUoW   postgres.IUnitOfWorker[testDeployRepo.Queries]
	cipher      *secret.Cipher // nil when EXERCISE_SECRETS_KEY is unset
	media       IMedia
	infra       IInfrastructure // nil when no infrastructure agent is configured
	vpnStore    IVPNStore       // nil when secret storage is not configured
	flagConfig  config.ExerciseConfig
	sessions    ITestSessions  // nil when no proxy key is configured
	proposals   IProposalInbox // nil until wired; proposal inbox requests are then skipped
}

// IProposalInbox turns catalog proposals into inbox requests for platform
// admins and closes them on decision (satisfied by inbox.RequestRouter).
type IProposalInbox interface {
	ProposalSubmitted(ctx context.Context, p inboxUseCase.Proposal) error
	ProposalDecided(ctx context.Context, p inboxUseCase.Proposal, approved bool, by uuid.UUID) error
}

// SetProposalInbox wires the proposal inbox after the dispatcher exists.
func (u *ExerciseUseCase) SetProposalInbox(inbox IProposalInbox) {
	u.proposals = inbox
}

type Dependencies struct {
	Repo   IRepository
	Cipher *secret.Cipher
	Media  IMedia
	// Infra is nil when infrastructure is not configured; the per-variant test
	// deploy is then unavailable.
	Infra IInfrastructure
	// VPNStore persists a tester's VPN config; nil when secret storage is absent.
	VPNStore   IVPNStore
	FlagConfig config.ExerciseConfig
	// Sessions signs the proxy token of a test deploy; nil when unconfigured.
	Sessions ITestSessions
	// DeployUoW makes the "one active test lab per user" check and the reservation one
	// serialized transaction. Nil (tests): the check and the insert are separate calls.
	DeployUoW postgres.IUnitOfWorker[testDeployRepo.Queries]
}

type FlagPolicy struct {
	RandomHexLength int `json:"RandomHexLength"`
	RandomBits      int `json:"RandomBits"`
	WarningBits     int `json:"WarningBits"`
}

func (u *ExerciseUseCase) FlagPolicy() FlagPolicy {
	bytes := u.flagConfig.FlagRandomBytes
	if bytes == 0 { // tests and manually constructed use cases keep the default
		bytes = 20
	}
	warning := u.flagConfig.FlagWarningBits
	if warning == 0 {
		warning = 20
	}
	return FlagPolicy{RandomHexLength: bytes * 2, RandomBits: bytes * 8, WarningBits: warning}
}

func NewExerciseUseCase(deps Dependencies) *ExerciseUseCase {
	return &ExerciseUseCase{
		repo:        deps.Repo,
		exercises:   exerciseRepo.New(deps.Repo),
		testDeploys: testDeployRepo.New(deps.Repo),
		deployUoW:   deps.DeployUoW,
		cipher:      deps.Cipher,
		media:       deps.Media,
		infra:       deps.Infra,
		vpnStore:    deps.VPNStore,
		sessions:    deps.Sessions,
		flagConfig:  deps.FlagConfig,
	}
}

// mutateExercise: fetch → domain mutation → whole-identity write under the
// optimistic lock; zero rows re-reads to tell 404 from 409 (mutateUser twin).
func (u *ExerciseUseCase) mutateExercise(ctx context.Context, id uuid.UUID, mutate func(*exerciseModel.Exercise) error) (exerciseModel.Exercise, error) {
	e, err := u.exercises.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return exerciseModel.Exercise{}, exerciseModel.ErrExerciseNotFound.Err()
		}
		return exerciseModel.Exercise{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise").Err()
	}
	expectedUpdatedAt := e.UpdatedAt
	if err = mutate(&e); err != nil {
		return exerciseModel.Exercise{}, err
	}
	affected, err := u.exercises.Update(ctx, e, expectedUpdatedAt)
	if err != nil {
		return exerciseModel.Exercise{}, classifyExerciseWriteError(err, "update")
	}
	if affected == 0 {
		if _, err = u.exercises.GetByID(ctx, id); err != nil {
			return exerciseModel.Exercise{}, exerciseModel.ErrExerciseNotFound.Err()
		}
		return exerciseModel.Exercise{}, exerciseModel.ErrExerciseModified.Err()
	}
	return e, nil
}

// loadEditable fetches the exercise and refuses archived ones — the first
// step of every catalog-content mutation (working copy, publish, snapshots,
// restore). Identity edits get the same rule through Exercise.UpdateIdentity.
func (u *ExerciseUseCase) loadEditable(ctx context.Context, id uuid.UUID) (exerciseModel.Exercise, error) {
	e, err := u.exercises.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return exerciseModel.Exercise{}, exerciseModel.ErrExerciseNotFound.Err()
		}
		return exerciseModel.Exercise{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise").Err()
	}
	// Check-then-write: this archive check is not inside the same UoW/write
	// statement as the mutation that follows it. A concurrent archive between
	// this read and that write may let one in-flight write through. Accepted
	// as low impact (admin-only).
	if err = e.EnsureNotArchived(); err != nil {
		return exerciseModel.Exercise{}, err
	}
	return e, nil
}

// exerciseView builds the card read model. HasChanges asks SQL to compare the
// draft and published content only when both exist; the domain decides the
// rest (Exercise.HasUnpublishedChanges).
func (u *ExerciseUseCase) exerciseView(ctx context.Context, e exerciseModel.Exercise) (ExerciseView, error) {
	hasChanges, err := e.HasUnpublishedChanges(func() (bool, error) {
		differs, err := u.exercises.DraftDiffersFromPublished(ctx, e.ID)
		if repositoryTools.IsObjectNotFoundError(err) {
			// A pointer moved concurrently (publish cleared the draft):
			// nothing is left unpublished.
			return false, nil
		}
		return differs, err
	})
	if err != nil {
		return ExerciseView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to compare exercise working copy").Err()
	}
	v := toExerciseView(e)
	v.HasChanges = hasChanges
	return v, nil
}

// classifyExerciseWriteError maps a unique-violation on exercises.name to the
// domain 409; anything else becomes a platform error. The single call path
// for BOTH create and rename.
func classifyExerciseWriteError(err error, action string) error {
	if creator, ok := repositoryTools.UniqueViolationError(err, exerciseModel.ErrExerciseExists); ok {
		return creator.Err()
	}
	return model.ErrPlatform.WithError(err).WithMessage("Failed to " + action + " exercise").Err()
}
