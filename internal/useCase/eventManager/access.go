// Package eventManager implements event-local authorization. It deliberately
// takes the event ID explicitly and never derives it from Origin or Host.
package eventManager

import (
	"context"
	"errors"

	"github.com/gofrs/uuid"

	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// MembershipReader is the narrow port needed to authorize a management action.
// It is implemented by the event-manager repository in the next persistence slice.
type MembershipReader interface {
	Get(ctx context.Context, eventID, userID uuid.UUID) (eventManagerModel.EventManager, error)
}

type AccessUseCase struct {
	memberships MembershipReader
}

func NewAccessUseCase(memberships MembershipReader) *AccessUseCase {
	return &AccessUseCase{memberships: memberships}
}

// RequireManage permits only an owner or manager membership for the exact
// requested event. Missing membership is deliberately presented as forbidden,
// preventing event-management membership enumeration.
func (u *AccessUseCase) RequireManage(ctx context.Context, eventID, userID uuid.UUID) error {
	membership, err := u.memberships.Get(ctx, eventID, userID)
	if err != nil {
		if errors.Is(err, eventManagerModel.ErrEventManagerNotFound.Err()) {
			return eventManagerModel.ErrEventManagementForbidden.Err()
		}
		return err
	}
	if !membership.CanManage() {
		return eventManagerModel.ErrEventManagementForbidden.Err()
	}
	return nil
}

// RequireRead permits owner, manager, and viewer memberships for the exact
// event. Platform staff holding events.read read every event without being
// assigned (an implicit viewer); this never grants write access. Missing
// membership remains indistinguishable from forbidden access.
func (u *AccessUseCase) RequireRead(ctx context.Context, eventID, userID uuid.UUID) error {
	if claims, ok := rbac.CurrentUserSessionFromContext(ctx); ok && claims.UserID == userID && claims.Role.HasPermission(rbac.PermEventsRead) {
		return nil
	}
	membership, err := u.memberships.Get(ctx, eventID, userID)
	if err != nil {
		if errors.Is(err, eventManagerModel.ErrEventManagerNotFound.Err()) {
			return eventManagerModel.ErrEventManagementForbidden.Err()
		}
		return err
	}
	if !membership.CanRead() {
		return eventManagerModel.ErrEventManagementForbidden.Err()
	}
	return nil
}
