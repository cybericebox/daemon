package exerciseRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

// elevation maps a stored request to the domain entity. Never validates: a request written by an older version
// of the code loads as it is.
func elevation(row postgres.ExerciseResourceElevation) (resourcesModel.Elevation, error) {
	return elevationFromColumns(row.ID, row.ExerciseID, row.VersionID, row.Status, row.Reason, row.Requested, row.Approved,
		row.DecisionNote, row.RequestedBy, row.RequestedAt, row.DecidedBy, row.DecidedAt)
}

func elevationFromColumns(id, exerciseID uuid.UUID, versionID uuid.NullUUID, status int16, reason string, requested, approved []byte, note string,
	requestedBy uuid.NullUUID, requestedAtValue time.Time, decidedBy uuid.NullUUID, decidedAt pgtype.Timestamptz) (resourcesModel.Elevation, error) {
	e := resourcesModel.Elevation{
		ID: id, ExerciseID: exerciseID, VersionID: versionID, Status: resourcesModel.ElevationStatus(status), Reason: reason,
		DecisionNote: note, RequestedBy: requestedBy, RequestedAt: requestedAtValue, DecidedBy: decidedBy,
	}
	if decidedAt.Valid {
		t := decidedAt.Time
		e.DecidedAt = &t
	}
	if len(requested) > 0 {
		if err := json.Unmarshal(requested, &e.Requested); err != nil {
			return resourcesModel.Elevation{}, err
		}
	}
	if len(approved) > 0 {
		if err := json.Unmarshal(approved, &e.Approved); err != nil {
			return resourcesModel.Elevation{}, err
		}
	}
	return e, nil
}

// CreateElevation stores a new pending request. The partial unique index allows one pending request per
// exercise (a unique violation is the caller's to translate).
func (r *Repository) CreateElevation(ctx context.Context, e resourcesModel.Elevation) error {
	requested, err := json.Marshal(nonNilApprovals(e.Requested))
	if err != nil {
		return err
	}
	return r.q.CreateExerciseResourceElevation(ctx, postgres.CreateExerciseResourceElevationParams{
		ID: e.ID, ExerciseID: e.ExerciseID, VersionID: e.VersionID, Reason: e.Reason, Requested: requested,
		RequestedBy: e.RequestedBy, RequestedAt: e.RequestedAt,
	})
}

func (r *Repository) GetElevation(ctx context.Context, id uuid.UUID) (resourcesModel.Elevation, error) {
	row, err := r.q.GetExerciseResourceElevation(ctx, id)
	if err != nil {
		return resourcesModel.Elevation{}, err
	}
	return elevation(row)
}

// DecideElevation writes the decision of a pending request; 0 rows: it was decided meanwhile.
func (r *Repository) DecideElevation(ctx context.Context, e resourcesModel.Elevation) (int64, error) {
	var approved []byte
	if e.Status == resourcesModel.ElevationApproved {
		var err error
		if approved, err = json.Marshal(nonNilApprovals(e.Approved)); err != nil {
			return 0, err
		}
	}
	decidedAt := pgtype.Timestamptz{}
	if e.DecidedAt != nil {
		decidedAt = pgtype.Timestamptz{Time: *e.DecidedAt, Valid: true}
	}
	return r.q.DecideExerciseResourceElevation(ctx, postgres.DecideExerciseResourceElevationParams{
		ID: e.ID, Status: int16(e.Status), Approved: approved, DecisionNote: e.DecisionNote, DecidedBy: e.DecidedBy, DecidedAt: decidedAt,
	})
}

// ListElevations lists requests newest first with the exercise name; status and exercise narrow it.
func (r *Repository) ListElevations(ctx context.Context, status *resourcesModel.ElevationStatus, exerciseID uuid.NullUUID) ([]resourcesModel.ElevationListed, error) {
	params := postgres.ListExerciseResourceElevationsParams{ExerciseID: exerciseID}
	if status != nil {
		params.Status = pgtype.Int2{Int16: int16(*status), Valid: true}
	}
	rows, err := r.q.ListExerciseResourceElevations(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]resourcesModel.ElevationListed, 0, len(rows))
	for _, row := range rows {
		e, convErr := elevationFromColumns(row.ID, row.ExerciseID, row.VersionID, row.Status, row.Reason, row.Requested, row.Approved,
			row.DecisionNote, row.RequestedBy, row.RequestedAt, row.DecidedBy, row.DecidedAt)
		if convErr != nil {
			return nil, convErr
		}
		out = append(out, resourcesModel.ElevationListed{Elevation: e, ExerciseName: row.ExerciseName})
	}
	return out, nil
}

// LatestElevation is the open request of the exercise, else its latest decided one.
func (r *Repository) LatestElevation(ctx context.Context, exerciseID uuid.UUID) (resourcesModel.Elevation, error) {
	row, err := r.q.GetLatestExerciseResourceElevation(ctx, exerciseID)
	if err != nil {
		return resourcesModel.Elevation{}, err
	}
	return elevation(row)
}

// ApprovedFor is the approved values of every approved elevation of the exercises, per exercise.
func (r *Repository) ApprovedFor(ctx context.Context, exerciseIDs []uuid.UUID) (map[uuid.UUID][]resourcesModel.Approval, error) {
	out := make(map[uuid.UUID][]resourcesModel.Approval, len(exerciseIDs))
	if len(exerciseIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.ListApprovedExerciseResourceElevations(ctx, exerciseIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		var approved []resourcesModel.Approval
		if err = json.Unmarshal(row.Approved, &approved); err != nil {
			return nil, err
		}
		out[row.ExerciseID] = append(out[row.ExerciseID], approved...)
	}
	return out, nil
}

func nonNilApprovals(in []resourcesModel.Approval) []resourcesModel.Approval {
	if in == nil {
		return []resourcesModel.Approval{}
	}
	return in
}
