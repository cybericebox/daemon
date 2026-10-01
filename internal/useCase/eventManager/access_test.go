package eventManager_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventManager "github.com/cybericebox/daemon/internal/useCase/eventManager"
)

type fakeMemberships struct {
	members map[string]eventManagerModel.EventManager
	err     error
}

func (f fakeMemberships) Get(_ context.Context, eventID, userID uuid.UUID) (eventManagerModel.EventManager, error) {
	if f.err != nil {
		return eventManagerModel.EventManager{}, f.err
	}
	m, ok := f.members[eventID.String()+":"+userID.String()]
	if !ok {
		return eventManagerModel.EventManager{}, eventManagerModel.ErrEventManagerNotFound.Err()
	}
	return m, nil
}

func managerKey(eventID, userID uuid.UUID) string { return eventID.String() + ":" + userID.String() }

func TestRequireManageAllowsOwnerAndManagerForTheirOwnEvent(t *testing.T) {
	firstEvent := uuid.Must(uuid.NewV7())
	secondEvent := uuid.Must(uuid.NewV7())
	owner := uuid.Must(uuid.NewV7())
	manager := uuid.Must(uuid.NewV7())
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

	ownerMembership, _ := eventManagerModel.New(firstEvent, owner, eventManagerModel.RoleOwner, now)
	managerMembership, _ := eventManagerModel.New(secondEvent, manager, eventManagerModel.RoleManager, now)
	uc := eventManager.NewAccessUseCase(fakeMemberships{members: map[string]eventManagerModel.EventManager{
		managerKey(firstEvent, owner):    ownerMembership,
		managerKey(secondEvent, manager): managerMembership,
	}})

	if err := uc.RequireManage(context.Background(), firstEvent, owner); err != nil {
		t.Fatalf("owner: %v", err)
	}
	if err := uc.RequireManage(context.Background(), secondEvent, manager); err != nil {
		t.Fatalf("manager: %v", err)
	}
}

func TestRequireManageRejectsViewerAndMembershipForAnotherEvent(t *testing.T) {
	firstEvent := uuid.Must(uuid.NewV7())
	secondEvent := uuid.Must(uuid.NewV7())
	viewer := uuid.Must(uuid.NewV7())
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	viewerMembership, _ := eventManagerModel.New(firstEvent, viewer, eventManagerModel.RoleViewer, now)
	uc := eventManager.NewAccessUseCase(fakeMemberships{members: map[string]eventManagerModel.EventManager{
		managerKey(firstEvent, viewer): viewerMembership,
	}})

	if err := uc.RequireManage(context.Background(), firstEvent, viewer); !errors.Is(err, eventManagerModel.ErrEventManagementForbidden.Err()) {
		t.Fatalf("viewer: got %v", err)
	}
	if err := uc.RequireManage(context.Background(), secondEvent, viewer); !errors.Is(err, eventManagerModel.ErrEventManagementForbidden.Err()) {
		t.Fatalf("other event: got %v", err)
	}
}

func TestRequireReadAllowsViewerForOwnEvent(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	viewer := uuid.Must(uuid.NewV7())
	membership, _ := eventManagerModel.New(eventID, viewer, eventManagerModel.RoleViewer, time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	uc := eventManager.NewAccessUseCase(fakeMemberships{members: map[string]eventManagerModel.EventManager{managerKey(eventID, viewer): membership}})
	if err := uc.RequireRead(context.Background(), eventID, viewer); err != nil {
		t.Fatalf("RequireRead: %v", err)
	}
	if err := uc.RequireManage(context.Background(), eventID, viewer); !errors.Is(err, eventManagerModel.ErrEventManagementForbidden.Err()) {
		t.Fatalf("RequireManage viewer: %v", err)
	}
}

func TestPlatformStaffWithEventsReadReadsEveryEventWithoutWriting(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	uc := eventManager.NewAccessUseCase(fakeMemberships{})
	for _, role := range []rbac.Role{rbac.RoleAdmin, rbac.RoleAdminViewer} {
		actor := uuid.Must(uuid.NewV7())
		ctx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{UserID: actor, Role: role})
		if err := uc.RequireRead(ctx, eventID, actor); err != nil {
			t.Fatalf("%s RequireRead: %v", role, err)
		}
		if err := uc.RequireManage(ctx, eventID, actor); !errors.Is(err, eventManagerModel.ErrEventManagementForbidden.Err()) {
			t.Fatalf("%s RequireManage: got %v", role, err)
		}
	}
}

func TestPlainUserWithoutMembershipStillCannotRead(t *testing.T) {
	eventID, actor := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := eventManager.NewAccessUseCase(fakeMemberships{})
	ctx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{UserID: actor, Role: rbac.RoleUser})
	if err := uc.RequireRead(ctx, eventID, actor); !errors.Is(err, eventManagerModel.ErrEventManagementForbidden.Err()) {
		t.Fatalf("got %v", err)
	}
	// Claims of another user never widen the checked user's access.
	admin := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{UserID: uuid.Must(uuid.NewV7()), Role: rbac.RoleAdmin})
	if err := uc.RequireRead(admin, eventID, actor); !errors.Is(err, eventManagerModel.ErrEventManagementForbidden.Err()) {
		t.Fatalf("other user's claims: got %v", err)
	}
}

func TestAssignedManagerAdminCanManage(t *testing.T) {
	eventID, actor := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	membership, _ := eventManagerModel.New(eventID, actor, eventManagerModel.RoleManager, time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	uc := eventManager.NewAccessUseCase(fakeMemberships{members: map[string]eventManagerModel.EventManager{managerKey(eventID, actor): membership}})
	ctx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{UserID: actor, Role: rbac.RoleAdmin})
	if err := uc.RequireManage(ctx, eventID, actor); err != nil {
		t.Fatalf("RequireManage: %v", err)
	}
	if err := uc.RequireRead(ctx, eventID, actor); err != nil {
		t.Fatalf("RequireRead: %v", err)
	}
}
