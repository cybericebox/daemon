package eventAnalytics

import (
	"context"
	"errors"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// Level is the analytics access a route needs.
type Level int

const (
	// LevelSections: every analytics section and the report download.
	LevelSections Level = iota
	// LevelSensitive: wrong answer texts and the integrity section.
	LevelSensitive
)

// RequireEventAnalytics authorizes the viewer for one analytics route of the
// event (§7). Without any access the answer is the same whether or not the
// event exists.
func (u *EventAnalyticsUseCase) RequireEventAnalytics(ctx context.Context, eventID uuid.UUID, viewer rbac.Claims, level Level) error {
	access, err := u.EventAnalyticsAccess(ctx, eventID, viewer)
	if err != nil {
		return err
	}
	if !access.Sections {
		return eventAnalyticsModel.ErrEventAnalyticsForbidden.Err()
	}
	if level == LevelSensitive && !access.Sensitive {
		return eventAnalyticsModel.ErrEventAnalyticsSensitiveForbidden.Err()
	}
	return nil
}

// EventAnalyticsAccess resolves what the viewer may see in the event's
// analytics: the event role (if any) and the platform role.
func (u *EventAnalyticsUseCase) EventAnalyticsAccess(ctx context.Context, eventID uuid.UUID, viewer rbac.Claims) (eventAnalyticsModel.Access, error) {
	var membership *eventManagerModel.EventManager
	found, err := u.memberships.Get(ctx, eventID, viewer.UserID)
	switch {
	case err == nil:
		membership = &found
	case !isNotFound(err):
		return eventAnalyticsModel.Access{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event membership").Err()
	}
	return eventAnalyticsModel.ResolveAccess(membership, viewer.Role), nil
}

func isNotFound(err error) bool {
	return errors.Is(err, eventManagerModel.ErrEventManagerNotFound.Err()) || repositoryTools.IsObjectNotFoundError(err)
}
