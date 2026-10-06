package testDeployRepo

import (
	"context"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/gofrs/uuid"
	"time"
)

type Queries interface {
	CreateExerciseTestDeploy(context.Context, postgres.CreateExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error)
	GetOwnedExerciseTestDeploy(context.Context, postgres.GetOwnedExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error)
	DeleteOwnedExerciseTestDeploy(context.Context, postgres.DeleteOwnedExerciseTestDeployParams) (int64, error)
	ListExpiredExerciseTestDeploys(context.Context, time.Time) ([]postgres.ExerciseTestDeployment, error)
	ListOwnedExerciseTestDeploys(context.Context, uuid.UUID) ([]postgres.ExerciseTestDeployment, error)
	ListOwnedExerciseTestDeploysForExercise(context.Context, postgres.ListOwnedExerciseTestDeploysForExerciseParams) ([]postgres.ExerciseTestDeployment, error)
	ExtendOwnedExerciseTestDeploy(context.Context, postgres.ExtendOwnedExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error)
	MarkExerciseTestDeploySolved(context.Context, postgres.MarkExerciseTestDeploySolvedParams) (postgres.ExerciseTestDeployment, error)
	LockExerciseTestDeploysOf(context.Context, string) error
	CountActiveExerciseTestDeploys(context.Context, postgres.CountActiveExerciseTestDeploysParams) (int64, error)
}

// LockOwner serializes everything one author does to their test labs until the transaction ends.
func (r *Repository) LockOwner(ctx context.Context, owner uuid.UUID) error {
	return r.q.LockExerciseTestDeploysOf(ctx, owner.String())
}

func (r *Repository) ListOwned(ctx context.Context, owner uuid.UUID) ([]exerciseModel.TestDeploy, error) {
	rows, e := r.q.ListOwnedExerciseTestDeploys(ctx, owner)
	out := make([]exerciseModel.TestDeploy, 0, len(rows))
	for _, v := range rows {
		out = append(out, toDomain(v))
	}
	return out, e
}

// ListOwnedForExercise lists the owner's deploys of one exercise's versions.
func (r *Repository) ListOwnedForExercise(ctx context.Context, owner, exerciseID uuid.UUID) ([]exerciseModel.TestDeploy, error) {
	rows, e := r.q.ListOwnedExerciseTestDeploysForExercise(ctx, postgres.ListOwnedExerciseTestDeploysForExerciseParams{CreatedBy: owner, ExerciseID: exerciseID})
	out := make([]exerciseModel.TestDeploy, 0, len(rows))
	for _, v := range rows {
		out = append(out, toDomain(v))
	}
	return out, e
}
func (r *Repository) ExtendOwned(ctx context.Context, id, owner uuid.UUID, expiresAt time.Time) (exerciseModel.TestDeploy, error) {
	row, e := r.q.ExtendOwnedExerciseTestDeploy(ctx, postgres.ExtendOwnedExerciseTestDeployParams{ID: id, CreatedBy: owner, ExpiresAt: expiresAt})
	return toDomain(row), e
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }
func ToRow(v exerciseModel.TestDeploy) postgres.ExerciseTestDeployment {
	return postgres.ExerciseTestDeployment{ID: v.ID, GroupName: v.GroupName, LabName: v.LabName, VersionID: v.VersionID, VariantID: v.VariantID, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt, ExpiresAt: v.ExpiresAt, Flags: marshalFlags(v.Flags), Solved: marshalSolved(v.Solved)}
}
func toDomain(v postgres.ExerciseTestDeployment) exerciseModel.TestDeploy {
	return exerciseModel.TestDeploy{ID: v.ID, GroupName: v.GroupName, LabName: v.LabName, VersionID: v.VersionID, VariantID: v.VariantID, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt, ExpiresAt: v.ExpiresAt, Flags: unmarshalFlags(v.Flags), Solved: unmarshalSolved(v.Solved)}
}
func (r *Repository) Create(ctx context.Context, v exerciseModel.TestDeploy) (exerciseModel.TestDeploy, error) {
	row, e := r.q.CreateExerciseTestDeploy(ctx, postgres.CreateExerciseTestDeployParams{ID: v.ID, GroupName: v.GroupName, LabName: v.LabName, VersionID: v.VersionID, VariantID: v.VariantID, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt, ExpiresAt: v.ExpiresAt, Flags: marshalFlags(v.Flags)})
	return toDomain(row), e
}
func (r *Repository) GetOwned(ctx context.Context, id, owner uuid.UUID) (exerciseModel.TestDeploy, error) {
	row, e := r.q.GetOwnedExerciseTestDeploy(ctx, postgres.GetOwnedExerciseTestDeployParams{ID: id, CreatedBy: owner})
	return toDomain(row), e
}
func (r *Repository) DeleteOwned(ctx context.Context, id, owner uuid.UUID) (int64, error) {
	return r.q.DeleteOwnedExerciseTestDeploy(ctx, postgres.DeleteOwnedExerciseTestDeployParams{ID: id, CreatedBy: owner})
}
func (r *Repository) ListExpired(ctx context.Context, before time.Time) ([]exerciseModel.TestDeploy, error) {
	rows, e := r.q.ListExpiredExerciseTestDeploys(ctx, before)
	out := make([]exerciseModel.TestDeploy, 0, len(rows))
	for _, v := range rows {
		out = append(out, toDomain(v))
	}
	return out, e
}

// MarkSolved records a correctly checked task once; it is idempotent.
func (r *Repository) MarkSolved(ctx context.Context, id, owner, taskID uuid.UUID) (exerciseModel.TestDeploy, error) {
	row, e := r.q.MarkExerciseTestDeploySolved(ctx, postgres.MarkExerciseTestDeploySolvedParams{ID: id, CreatedBy: owner, TaskID: taskID.String()})
	return toDomain(row), e
}

func marshalSolved(ids []uuid.UUID) []byte {
	if len(ids) == 0 {
		return []byte("[]")
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return []byte("[]")
	}
	return raw
}

// unmarshalSolved forgives a bad stored value like unmarshalFlags does.
func unmarshalSolved(raw []byte) []uuid.UUID {
	var ids []uuid.UUID
	if len(raw) == 0 || json.Unmarshal(raw, &ids) != nil {
		return nil
	}
	return ids
}

func marshalFlags(flags []exerciseModel.DeployFlag) []byte {
	if len(flags) == 0 {
		return []byte("[]")
	}
	raw, err := json.Marshal(flags)
	if err != nil {
		return []byte("[]")
	}
	return raw
}

// unmarshalFlags forgives a bad stored value: the deploy is still usable.
func unmarshalFlags(raw []byte) []exerciseModel.DeployFlag {
	var flags []exerciseModel.DeployFlag
	if len(raw) == 0 || json.Unmarshal(raw, &flags) != nil {
		return nil
	}
	return flags
}

// CreateIfUnderLimit stores v unless its owner already has limit or more active deploys (ones
// that have not expired at now); the bool says whether it was stored. It must run inside one
// transaction: the per-owner advisory lock it takes lasts until that transaction ends, so
// concurrent creates of one owner are serialized and each sees the rows of the earlier ones.
func (r *Repository) CreateIfUnderLimit(ctx context.Context, v exerciseModel.TestDeploy, now time.Time, limit int) (exerciseModel.TestDeploy, bool, error) {
	if err := r.q.LockExerciseTestDeploysOf(ctx, v.CreatedBy.String()); err != nil {
		return exerciseModel.TestDeploy{}, false, err
	}
	active, err := r.q.CountActiveExerciseTestDeploys(ctx, postgres.CountActiveExerciseTestDeploysParams{CreatedBy: v.CreatedBy, Now: now})
	if err != nil || active >= int64(limit) {
		return exerciseModel.TestDeploy{}, false, err
	}
	created, err := r.Create(ctx, v)
	return created, err == nil, err
}
