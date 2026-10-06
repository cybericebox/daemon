package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventManagerRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/cybericebox/daemon/internal/model/rbac"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
)

// ListEventManagers lists every event-local management membership. The event
// lookup makes an empty manager list distinguishable from an unknown event.
func (u *EventUseCase) ListEventManagers(ctx context.Context, eventID uuid.UUID) ([]EventManagerView, error) {
	if _, err := u.events.GetByID(ctx, eventID); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, eventModel.ErrEventNotFound.Err()
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	items, err := u.managers.List(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event managers").Err()
	}
	views := make([]EventManagerView, 0, len(items))
	for _, item := range items {
		views = append(views, EventManagerView{UserID: item.UserID, Role: int16(item.Role), CreatedAt: item.CreatedAt})
	}
	return views, nil
}

// SetEventManager grants or changes a non-owner management membership. The
// original owner is immutable through this API, preventing an administrator
// from accidentally orphaning an event's management boundary.
func (u *EventUseCase) SetEventManager(ctx context.Context, eventID uuid.UUID, in SetEventManagerInput) (EventManagerView, error) {
	role := eventManagerModel.Role(in.Role)
	if role == eventManagerModel.RoleOwner {
		return EventManagerView{}, eventManagerModel.ErrEventManagerOwnerProtected.Err()
	}
	membership, err := eventManagerModel.New(eventID, in.UserID, role, time.Now())
	if err != nil {
		return EventManagerView{}, err
	}
	if role == eventManagerModel.RoleViewer {
		if err = u.rejectRedundantViewer(ctx, in.UserID); err != nil {
			return EventManagerView{}, err
		}
	}
	if u.signalPublishers == nil {
		value, err := u.managers.Upsert(ctx, membership)
		if err != nil {
			if repositoryTools.IsObjectNotFoundError(err) {
				return EventManagerView{}, eventManagerModel.ErrEventManagerOwnerProtected.Err()
			}
			return EventManagerView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to set event manager").Err()
		}
		return EventManagerView{UserID: value.UserID, Role: int16(value.Role), CreatedAt: value.CreatedAt}, u.requestModeratorsLabAccess(ctx, eventID)
	}
	if u.uow == nil {
		return EventManagerView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return EventManagerView{}, err
	}
	defer unit.Restore()
	managers := eventManagerRepo.New(txRepo)
	old, err := managers.Get(txCtx, eventID, in.UserID)
	if err != nil && !repositoryTools.IsObjectNotFoundError(err) {
		return EventManagerView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event manager").Err()
	}
	if err == nil {
		if old.Role == eventManagerModel.RoleOwner {
			return EventManagerView{}, eventManagerModel.ErrEventManagerOwnerProtected.Err()
		}
		if old.Role == role {
			return EventManagerView{UserID: old.UserID, Role: int16(old.Role), CreatedAt: old.CreatedAt}, nil
		}
	}
	e, err := eventRepo.New(txRepo).GetByID(txCtx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventManagerView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventManagerView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event for manager signal").Err()
	}
	value, err := managers.Upsert(txCtx, membership)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventManagerView{}, eventManagerModel.ErrEventManagerOwnerProtected.Err()
		}
		return EventManagerView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to set event manager").Err()
	}
	roleCode, roleName := "moderator", "модератором"
	if role == eventManagerModel.RoleViewer {
		roleCode, roleName = "observer", "спостерігачем"
	}
	publisher := u.signalPublishers(txRepo)
	if publisher == nil {
		return EventManagerView{}, model.ErrPlatform.WithMessage("Event signal publisher is not configured").Err()
	}
	if err = publisher.Publish(txCtx, signalModel.TypeEventManagerAssigned, &signalModel.EventManagerPayload{
		ScopeEventID: e.ID, SubjectUserID: value.UserID, EventTag: e.Tag, EventName: e.Name,
		ManagerRole: roleCode, RoleName: roleName,
	}); err != nil {
		return EventManagerView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to publish event manager signal").Err()
	}
	if err = unit.Save(); err != nil {
		return EventManagerView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to set event manager").Err()
	}
	return EventManagerView{UserID: value.UserID, Role: int16(value.Role), CreatedAt: value.CreatedAt}, u.requestModeratorsLabAccess(ctx, eventID)
}

// rejectRedundantViewer refuses the viewer role for platform staff with
// events.read: they already read every event, so the assignment adds nothing.
func (u *EventUseCase) rejectRedundantViewer(ctx context.Context, userID uuid.UUID) error {
	if u.userProfiles == nil {
		return nil
	}
	user, err := u.userProfiles.GetUserByID(ctx, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event manager user").Err()
	}
	if rbac.Role(user.Role).HasPermission(rbac.PermEventsRead) {
		return eventManagerModel.ErrEventManagerViewerRedundant.Err()
	}
	return nil
}

func (u *EventUseCase) RemoveEventManager(ctx context.Context, eventID, userID uuid.UUID) error {
	membership, err := u.managers.Get(ctx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventManagerModel.ErrEventManagerNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event manager").Err()
	}
	if membership.Role == eventManagerModel.RoleOwner {
		return eventManagerModel.ErrEventManagerOwnerProtected.Err()
	}
	affected, err := u.managers.DeleteNonOwner(ctx, eventID, userID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to remove event manager").Err()
	}
	if affected == 0 {
		return eventManagerModel.ErrEventManagerNotFound.Err()
	}
	if team, teamErr := u.stands.GetModeratorsTeam(ctx, eventID); teamErr == nil {
		u.dropMemberLabClient(ctx, eventID, team.ID, userID)
	}
	return u.requestModeratorsLabAccess(ctx, eventID)
}

// requestModeratorsLabAccess re-derives the moderators team VPN clients after
// a manager change; a no-op while the event has no moderators team.
func (u *EventUseCase) requestModeratorsLabAccess(ctx context.Context, eventID uuid.UUID) error {
	if !u.supportsLabAccessPolicy() {
		return nil
	}
	if err := u.labAccessSyncs.RequestModerators(ctx, eventID, time.Now()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to request moderators laboratory access sync").Err()
	}
	return nil
}
