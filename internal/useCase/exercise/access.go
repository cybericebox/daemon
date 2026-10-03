package exercise

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// Actor is the caller of an exercise route: platform RBAC plus, for event
// managers, the event memberships the policy below derives rights from.
type Actor struct {
	UserID uuid.UUID
	Role   rbac.Role
}

func (a Actor) has(perm rbac.Permission) bool { return a.Role.HasPermission(perm) }

// Action is what a route does to one exercise.
type Action int

const (
	// ActionReadPublished: the card and the published version (what a
	// manager may see of an available catalog exercise before forking it).
	ActionReadPublished Action = iota
	// ActionRead: everything — working copy, history, checkpoints, usage.
	ActionRead
	ActionWrite
	ActionPublish
	ActionDelete
)

// rbacPermission is the platform permission that grants an action on any
// exercise (admins).
func (a Action) rbacPermission() rbac.Permission {
	switch a {
	case ActionWrite:
		return rbac.PermExercisesWrite
	case ActionPublish:
		return rbac.PermExercisesPublish
	case ActionDelete:
		return rbac.PermExercisesDelete
	default:
		return rbac.PermExercisesRead
	}
}

// Access is the outcome of AuthorizeExercise: Full is false when the caller
// may only see the published version.
type Access struct {
	Full bool
}

// AuthorizeExercise is the single data-dependent policy of the exercise
// routes (gated by PermSelf): platform RBAC grants everything; otherwise
// rights come from the membership of the owner event (read for any member,
// the rest for owners/managers); a published catalog exercise available to one
// of the caller's events is readable (published version only). Denials are
// 403 without saying why (category B).
func (u *ExerciseUseCase) AuthorizeExercise(ctx context.Context, actor Actor, id uuid.UUID, action Action) (Access, error) {
	if actor.has(action.rbacPermission()) {
		return Access{Full: true}, nil
	}
	e, err := u.exercises.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return Access{}, exerciseModel.ErrExerciseNotFound.Err()
		}
		return Access{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise").Err()
	}
	if e.IsEventScoped() {
		if !e.OwnerEventID.Valid {
			return Access{}, exerciseModel.ErrExerciseForbidden.Err()
		}
		role, member, roleErr := u.membershipRole(ctx, e.OwnerEventID.UUID, actor.UserID)
		if roleErr != nil {
			return Access{}, roleErr
		}
		if !member || (action > ActionRead && !canManage(role)) {
			return Access{}, exerciseModel.ErrExerciseForbidden.Err()
		}
		return Access{Full: true}, nil
	}
	if action != ActionReadPublished {
		return Access{}, exerciseModel.ErrExerciseForbidden.Err()
	}
	readable, err := u.exercises.ReadableBy(ctx, id, actor.UserID)
	if err != nil {
		return Access{}, model.ErrPlatform.WithError(err).WithMessage("Failed to check exercise access").Err()
	}
	if !readable {
		return Access{}, exerciseModel.ErrExerciseForbidden.Err()
	}
	return Access{}, nil
}

// RequireTestLabAuthor lets through who may run a test deploy: exercises.write, or a manage membership of an
// event that has infrastructure. The test-lab room check and bookings are for these authors only.
func (u *ExerciseUseCase) RequireTestLabAuthor(ctx context.Context, actor Actor) error {
	if actor.has(rbac.PermExercisesWrite) {
		return nil
	}
	memberships, err := u.exercises.Memberships(ctx, actor.UserID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event memberships").Err()
	}
	for _, m := range memberships {
		if canManage(m.Role) && m.InfrastructureAllowed {
			return nil
		}
	}
	return exerciseModel.ErrExerciseForbidden.Err()
}

func canManage(role int16) bool {
	return eventManagerModel.Role(role) == eventManagerModel.RoleOwner || eventManagerModel.Role(role) == eventManagerModel.RoleManager
}

func (u *ExerciseUseCase) membershipRole(ctx context.Context, eventID, userID uuid.UUID) (int16, bool, error) {
	memberships, err := u.exercises.Memberships(ctx, userID)
	if err != nil {
		return 0, false, model.ErrPlatform.WithError(err).WithMessage("Failed to get event memberships").Err()
	}
	for _, m := range memberships {
		if m.EventID == eventID {
			return m.Role, true, nil
		}
	}
	return 0, false, nil
}

// AccessEvent is one event the caller works with in the exercises app.
type AccessEvent struct {
	ID                    uuid.UUID
	Name                  string
	Tag                   string
	CanWrite              bool
	InfrastructureAllowed bool
}

// AccessSummary tells the exercises app what the caller may do.
type AccessSummary struct {
	IsAdmin          bool
	CanCreateCatalog bool
	CanPublish       bool
	CanDelete        bool
	CanExport        bool
	Events           []AccessEvent
}

func (u *ExerciseUseCase) GetAccessSummary(ctx context.Context, actor Actor) (AccessSummary, error) {
	memberships, err := u.exercises.Memberships(ctx, actor.UserID)
	if err != nil {
		return AccessSummary{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event memberships").Err()
	}
	out := AccessSummary{
		IsAdmin: actor.has(rbac.PermExercisesRead), CanCreateCatalog: actor.has(rbac.PermExercisesWrite),
		CanPublish: actor.has(rbac.PermExercisesPublish), CanDelete: actor.has(rbac.PermExercisesDelete),
		CanExport: actor.has(rbac.PermExercisesExport), Events: make([]AccessEvent, 0, len(memberships)),
	}
	for _, m := range memberships {
		out.Events = append(out.Events, AccessEvent{ID: m.EventID, Name: m.Name, Tag: m.Tag, CanWrite: canManage(m.Role), InfrastructureAllowed: m.InfrastructureAllowed})
	}
	return out, nil
}

// permissionsFor computes what the caller may do with one exercise, given
// their memberships (one query per page).
func permissionsFor(actor Actor, e exerciseModel.Exercise, memberships map[uuid.UUID]int16) ExercisePermissions {
	if actor.has(rbac.PermExercisesRead) {
		write := actor.has(rbac.PermExercisesWrite)
		return ExercisePermissions{
			CanRead: true, CanEdit: write, CanPublish: actor.has(rbac.PermExercisesPublish),
			CanDelete: actor.has(rbac.PermExercisesDelete), CanManageAccess: write && !e.IsEventScoped(),
			CanPropose: false, CanExport: actor.has(rbac.PermExercisesExport),
		}
	}
	if !e.IsEventScoped() || !e.OwnerEventID.Valid {
		return ExercisePermissions{}
	}
	role, member := memberships[e.OwnerEventID.UUID]
	manage := member && canManage(role)
	return ExercisePermissions{CanRead: member, CanEdit: manage, CanPublish: manage, CanDelete: manage, CanPropose: manage && e.PublishedVersionID.Valid && e.ArchivedAt == nil}
}

func (u *ExerciseUseCase) membershipMap(ctx context.Context, actor Actor) (map[uuid.UUID]int16, error) {
	out := map[uuid.UUID]int16{}
	if actor.has(rbac.PermExercisesRead) {
		return out, nil
	}
	memberships, err := u.exercises.Memberships(ctx, actor.UserID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event memberships").Err()
	}
	for _, m := range memberships {
		out[m.EventID] = m.Role
	}
	return out, nil
}

// requireCreateScope checks who may create where: a catalog exercise needs
// exercises.write; an event exercise needs that (admin) or a manage
// membership of the owner event.
func (u *ExerciseUseCase) requireCreateScope(ctx context.Context, actor Actor, ownerEventID *uuid.UUID) error {
	if ownerEventID == nil {
		if !actor.has(rbac.PermExercisesWrite) {
			return exerciseModel.ErrExerciseForbidden.Err()
		}
		return nil
	}
	if _, err := u.eventInfrastructure(ctx, *ownerEventID); err != nil {
		return err
	}
	if actor.has(rbac.PermExercisesWrite) {
		return nil
	}
	role, member, err := u.membershipRole(ctx, *ownerEventID, actor.UserID)
	if err != nil {
		return err
	}
	if !member || !canManage(role) {
		return exerciseModel.ErrExerciseForbidden.Err()
	}
	return nil
}

func (u *ExerciseUseCase) eventInfrastructure(ctx context.Context, eventID uuid.UUID) (bool, error) {
	allowed, err := u.exercises.EventInfrastructureAllowed(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return false, exerciseModel.ErrExerciseForbidden.Err()
		}
		return false, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	return allowed, nil
}

// requireInfrastructureAllowed keeps lab topologies out of event exercises
// whose owner event does not allow infrastructure (saving and publishing).
func (u *ExerciseUseCase) requireInfrastructureAllowed(ctx context.Context, e exerciseModel.Exercise, variants []exerciseModel.Variant) error {
	if !e.IsEventScoped() || !exerciseModel.HasInfrastructure(variants) {
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

// AuthorizeFileUpload: admins, and anyone who manages at least one event.
func (u *ExerciseUseCase) AuthorizeFileUpload(ctx context.Context, actor Actor) error {
	if actor.has(rbac.PermExercisesWrite) {
		return nil
	}
	memberships, err := u.exercises.Memberships(ctx, actor.UserID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event memberships").Err()
	}
	for _, m := range memberships {
		if canManage(m.Role) {
			return nil
		}
	}
	return exerciseModel.ErrExerciseForbidden.Err()
}

// AuthorizeFileDownload: the uploader, and readers of an exercise whose
// versions reference the file (admins read every exercise). The media table is
// shared by every kind of file (answer files, avatars, event images), so an
// exercises.read role is NOT a licence for any media id: a file no exercise
// version references is not an exercise file, whoever asks.
func (u *ExerciseUseCase) AuthorizeFileDownload(ctx context.Context, actor Actor, file mediaModel.File) error {
	if file.CreatedBy.Valid && file.CreatedBy.UUID == actor.UserID {
		return nil
	}
	ids, err := u.exercises.FileExerciseIDs(ctx, file.ID, mediaModel.RefTypeExerciseVersion)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get file references").Err()
	}
	if len(ids) > 0 && actor.has(rbac.PermExercisesRead) {
		return nil
	}
	for _, id := range ids {
		if _, authErr := u.AuthorizeExercise(ctx, actor, id, ActionReadPublished); authErr == nil {
			return nil
		}
	}
	return mediaModel.ErrFileNotFound.Err()
}

// visibilityFor turns list filters into repository visibility: non-admins see
// only what they may read.
func visibilityFor(actor Actor, f ExercisesFilter) exerciseRepo.Visibility {
	v := exerciseRepo.Visibility{Scope: f.Scope, Infrastructure: f.Infrastructure}
	if v.Scope != "catalog" && v.Scope != "event" {
		v.Scope = ""
	}
	if v.Infrastructure != "yes" && v.Infrastructure != "no" {
		v.Infrastructure = ""
	}
	v.EventIDs = f.EventIDs
	if !actor.has(rbac.PermExercisesRead) {
		v.ViewerID = uuid.NullUUID{UUID: actor.UserID, Valid: true}
	}
	return v
}
