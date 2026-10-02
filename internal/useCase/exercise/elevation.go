package exercise

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
)

// IElevations is the data port of the resource elevation requests (satisfied by *exerciseRepo.Repository).
type IElevations interface {
	CreateElevation(ctx context.Context, e resourcesModel.Elevation) error
	GetElevation(ctx context.Context, id uuid.UUID) (resourcesModel.Elevation, error)
	// DecideElevation writes the decision of a pending request; 0 rows: it was decided meanwhile.
	DecideElevation(ctx context.Context, e resourcesModel.Elevation) (int64, error)
	// ListElevations lists requests newest first; status and exercise narrow it.
	ListElevations(ctx context.Context, status *resourcesModel.ElevationStatus, exerciseID uuid.NullUUID) ([]resourcesModel.ElevationListed, error)
	// LatestElevation is the open request of the exercise, else its latest decided one.
	LatestElevation(ctx context.Context, exerciseID uuid.UUID) (resourcesModel.Elevation, error)
	// ApprovedFor is the approved values of every approved elevation, per exercise.
	ApprovedFor(ctx context.Context, exerciseIDs []uuid.UUID) (map[uuid.UUID][]resourcesModel.Approval, error)
}

// IElevationInbox tells the platform admins about a request and the author about its decision (satisfied by
// inbox.RequestRouter).
type IElevationInbox interface {
	ElevationRequested(ctx context.Context, e inboxUseCase.Elevation) error
	ElevationDecided(ctx context.Context, e inboxUseCase.Elevation, approved bool, by uuid.UUID) error
}

// SetElevationInbox wires the elevation notifications after the dispatcher exists.
func (u *ExerciseUseCase) SetElevationInbox(inbox IElevationInbox) {
	u.elevationInbox = inbox
}

// ElevationDevice is one device of a request: what it asks for, or what was approved.
type ElevationDevice struct {
	DeviceID      uuid.UUID
	Name          string
	CPUMillicores int64
	MemoryBytes   int64
}

// ElevationView is one elevation request.
type ElevationView struct {
	ID           uuid.UUID
	ExerciseID   uuid.UUID
	ExerciseName string
	VersionID    *uuid.UUID
	// Status is pending, approved or rejected.
	Status string
	Reason string
	// Requested are the devices as the author asked; Approved what the admin allowed (empty until approved).
	Requested       []ElevationDevice
	Approved        []ElevationDevice
	DecisionNote    string
	RequestedBy     *uuid.UUID
	RequestedByName string
	RequestedAt     time.Time
	DecidedBy       *uuid.UUID
	DecidedByName   string
	DecidedAt       *time.Time
}

func toElevationDevices(in []resourcesModel.Approval) []ElevationDevice {
	out := make([]ElevationDevice, 0, len(in))
	for _, a := range in {
		out = append(out, ElevationDevice{DeviceID: a.DeviceID, Name: a.Name, CPUMillicores: a.CPUMillicores, MemoryBytes: a.MemoryBytes})
	}
	return out
}

func (u *ExerciseUseCase) toElevationView(ctx context.Context, e resourcesModel.Elevation, exerciseName string) ElevationView {
	names := u.authorNames(ctx, e.RequestedBy, e.DecidedBy)
	return ElevationView{
		ID: e.ID, ExerciseID: e.ExerciseID, ExerciseName: exerciseName, VersionID: uuidPtr(e.VersionID), Status: e.Status.String(), Reason: e.Reason,
		Requested: toElevationDevices(e.Requested), Approved: toElevationDevices(e.Approved), DecisionNote: e.DecisionNote,
		RequestedBy: uuidPtr(e.RequestedBy), RequestedByName: names[e.RequestedBy.UUID], RequestedAt: e.RequestedAt,
		DecidedBy: uuidPtr(e.DecidedBy), DecidedByName: names[e.DecidedBy.UUID], DecidedAt: e.DecidedAt,
	}
}

// describeApprovals is the devices as text for a notification: "db: 500m / 2Gi, web: 250m / 1Gi".
func describeApprovals(devices []resourcesModel.Approval) string {
	parts := make([]string, 0, len(devices))
	for _, d := range devices {
		parts = append(parts, d.Name+": "+strconv.FormatInt(d.CPUMillicores, 10)+"m / "+formatBytes(d.MemoryBytes))
	}
	return strings.Join(parts, ", ")
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30 && b%(1<<30) == 0:
		return strconv.FormatInt(b>>30, 10) + "Gi"
	case b%(1<<20) == 0:
		return strconv.FormatInt(b>>20, 10) + "Mi"
	}
	return strconv.FormatInt(b, 10)
}

// RequestElevation asks the platform admins to let the devices of the exercise's working copy that pass the
// frame (and are not covered by an earlier approval) go above it. One request is open per exercise. Route
// gate: write access to the exercise.
func (u *ExerciseUseCase) RequestElevation(ctx context.Context, actor Actor, exerciseID uuid.UUID, reason string) (ElevationView, error) {
	if u.elevations == nil {
		return ElevationView{}, model.ErrPlatform.WithMessage("Resource elevations are not configured").Err()
	}
	e, err := u.loadEditable(ctx, exerciseID)
	if err != nil {
		return ElevationView{}, err
	}
	working, err := u.secretMergeSource(ctx, e)
	if err != nil {
		return ElevationView{}, err
	}
	if working == nil {
		return ElevationView{}, exerciseModel.ErrElevationNotNeeded.Err()
	}
	policy := u.Policy()
	for _, o := range policy.OutsideFrame(working.Variants) {
		if o.AboveCeiling {
			return ElevationView{}, exerciseModel.ErrDeviceResourcesAboveCeiling.WithContext("devices", describeOutside([]DeviceOutside{{Name: o.Name, CPUMillicores: o.CPUMillicores, MemoryBytes: o.MemoryBytes}})).
				WithContext("ceilingCpuMillicores", policy.Ceiling.CPUMillicores).WithContext("ceilingMemoryBytes", policy.Ceiling.MemoryBytes).Err()
		}
	}
	approved, err := u.approvals(ctx, exerciseID)
	if err != nil {
		return ElevationView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read the resource elevations").Err()
	}
	needs := policy.NeedsElevation(working.Variants, approved)
	if len(needs) == 0 {
		return ElevationView{}, exerciseModel.ErrElevationNotNeeded.Err()
	}
	by := uuid.NullUUID{UUID: actor.UserID, Valid: actor.UserID != uuid.Nil}
	elevation, err := resourcesModel.NewElevation(exerciseID, uuid.NullUUID{UUID: working.ID, Valid: working.ID != uuid.Nil}, reason, needs, by, time.Now())
	if err != nil {
		return ElevationView{}, err
	}
	if err = u.elevations.CreateElevation(ctx, elevation); err != nil {
		if _, ok := repositoryTools.UniqueViolationError(err, exerciseModel.ErrElevationPending); ok {
			return ElevationView{}, exerciseModel.ErrElevationPending.Err()
		}
		return ElevationView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save the resource elevation request").Err()
	}
	u.notifyElevation(ctx, elevation, e.Name, func(n inboxUseCase.Elevation) error { return u.elevationInbox.ElevationRequested(ctx, n) })
	return u.toElevationView(ctx, elevation, e.Name), nil
}

// ListExerciseElevations is the request history of one exercise, newest first. Route gate: read access to
// the exercise.
func (u *ExerciseUseCase) ListExerciseElevations(ctx context.Context, exerciseID uuid.UUID) ([]ElevationView, error) {
	if u.elevations == nil {
		return []ElevationView{}, nil
	}
	rows, err := u.elevations.ListElevations(ctx, nil, uuid.NullUUID{UUID: exerciseID, Valid: true})
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list the resource elevation requests").Err()
	}
	return u.elevationViews(ctx, rows), nil
}

// ListElevations is the platform admin's list of requests; status is pending, approved, rejected or empty for
// all. Route gate: exercises.elevations.read.
func (u *ExerciseUseCase) ListElevations(ctx context.Context, status string) ([]ElevationView, error) {
	if u.elevations == nil {
		return []ElevationView{}, nil
	}
	var filter *resourcesModel.ElevationStatus
	for _, s := range []resourcesModel.ElevationStatus{resourcesModel.ElevationPending, resourcesModel.ElevationApproved, resourcesModel.ElevationRejected} {
		if s.String() == status {
			filter = &s
		}
	}
	rows, err := u.elevations.ListElevations(ctx, filter, uuid.NullUUID{})
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list the resource elevation requests").Err()
	}
	return u.elevationViews(ctx, rows), nil
}

func (u *ExerciseUseCase) elevationViews(ctx context.Context, rows []resourcesModel.ElevationListed) []ElevationView {
	people := make([]uuid.NullUUID, 0, 2*len(rows))
	for _, r := range rows {
		people = append(people, r.RequestedBy, r.DecidedBy)
	}
	names := u.authorNames(ctx, people...)
	out := make([]ElevationView, 0, len(rows))
	for _, r := range rows {
		out = append(out, ElevationView{
			ID: r.ID, ExerciseID: r.ExerciseID, ExerciseName: r.ExerciseName, VersionID: uuidPtr(r.VersionID), Status: r.Status.String(), Reason: r.Reason,
			Requested: toElevationDevices(r.Requested), Approved: toElevationDevices(r.Approved), DecisionNote: r.DecisionNote,
			RequestedBy: uuidPtr(r.RequestedBy), RequestedByName: names[r.RequestedBy.UUID], RequestedAt: r.RequestedAt,
			DecidedBy: uuidPtr(r.DecidedBy), DecidedByName: names[r.DecidedBy.UUID], DecidedAt: r.DecidedAt,
		})
	}
	return out
}

// DecideElevationInput is the admin's decision. Devices are the approved values per requested device; empty
// approves exactly what was requested. Ignored when rejecting.
type DecideElevationInput struct {
	Approve bool
	Note    string
	Devices []ElevationDevice
}

// DecideElevation approves or rejects a pending request. The approval stores the approved values per device.
// Route gate: exercises.elevations.write.
func (u *ExerciseUseCase) DecideElevation(ctx context.Context, actor Actor, id uuid.UUID, in DecideElevationInput) (ElevationView, error) {
	if u.elevations == nil {
		return ElevationView{}, model.ErrPlatform.WithMessage("Resource elevations are not configured").Err()
	}
	elevation, err := u.elevations.GetElevation(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return ElevationView{}, exerciseModel.ErrElevationNotFound.Err()
		}
		return ElevationView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get the resource elevation request").Err()
	}
	now := time.Now()
	if in.Approve {
		var values []resourcesModel.Approval
		for _, d := range in.Devices {
			values = append(values, resourcesModel.Approval{DeviceID: d.DeviceID, Name: nameOf(elevation.Requested, d.DeviceID, d.Name), Amount: resourcesModel.Amount{CPUMillicores: d.CPUMillicores, MemoryBytes: d.MemoryBytes}})
		}
		err = elevation.Approve(values, u.Policy().Ceiling, actor.UserID, in.Note, now)
	} else {
		err = elevation.Reject(actor.UserID, in.Note, now)
	}
	if err != nil {
		return ElevationView{}, err
	}
	affected, err := u.elevations.DecideElevation(ctx, elevation)
	if err != nil {
		return ElevationView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save the resource elevation decision").Err()
	}
	if affected == 0 {
		return ElevationView{}, exerciseModel.ErrElevationDecided.Err()
	}
	name := ""
	if ex, getErr := u.exercises.GetByID(ctx, elevation.ExerciseID); getErr == nil {
		name = ex.Name
	}
	u.notifyElevation(ctx, elevation, name, func(n inboxUseCase.Elevation) error {
		return u.elevationInbox.ElevationDecided(ctx, n, in.Approve, actor.UserID)
	})
	return u.toElevationView(ctx, elevation, name), nil
}

func nameOf(requested []resourcesModel.Approval, id uuid.UUID, fallback string) string {
	for _, r := range requested {
		if r.DeviceID == id {
			return r.Name
		}
	}
	return fallback
}

// notifyElevation reports a request or decision to the inbox after it is stored. It is best-effort: the
// request itself already succeeded, so a delivery failure is logged, not returned.
func (u *ExerciseUseCase) notifyElevation(ctx context.Context, e resourcesModel.Elevation, exerciseName string, send func(inboxUseCase.Elevation) error) {
	if u.elevationInbox == nil {
		return
	}
	devices := e.Requested
	if e.Status == resourcesModel.ElevationApproved {
		devices = e.Approved
	}
	notice := inboxUseCase.Elevation{
		ID: e.ID, ExerciseID: e.ExerciseID, ExerciseName: exerciseName, RequestedBy: e.RequestedBy.UUID, RequestedAt: e.RequestedAt,
		Reason: e.Reason, DecisionNote: e.DecisionNote, Devices: describeApprovals(devices),
	}
	if err := send(notice); err != nil {
		log.Error().Err(err).Str("elevation_id", e.ID.String()).Msg("Failed to update the resource elevation inbox")
	}
}

// elevationView is the request an editor shows for the exercise: the open one, else the latest decided one;
// nil when there is none or the store is not configured.
func (u *ExerciseUseCase) elevationView(ctx context.Context, exerciseID uuid.UUID, exerciseName string) *ElevationView {
	if u.elevations == nil || exerciseID == uuid.Nil {
		return nil
	}
	latest, err := u.elevations.LatestElevation(ctx, exerciseID)
	if err != nil {
		if !repositoryTools.IsObjectNotFoundError(err) {
			log.Warn().Err(err).Str("exercise_id", exerciseID.String()).Msg("Exercise resource elevation unavailable")
		}
		return nil
	}
	view := u.toElevationView(ctx, latest, exerciseName)
	return &view
}
