package inboxUseCase

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/inboxRepo"
	"github.com/cybericebox/daemon/internal/model"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
)

type (
	Dependencies             struct{ Repo inboxRepo.Queries }
	NotificationInboxUseCase struct {
		inbox *inboxRepo.Repository
	}
)

func NewNotificationInboxUseCase(deps Dependencies) *NotificationInboxUseCase {
	return &NotificationInboxUseCase{inbox: inboxRepo.New(deps.Repo)}
}

// ListInbox returns up to ten inbox entries and a cursor for older entries.
// scope nil lists every item; an Event id lists that Event's items and items
// without an Event (the Event site inbox, M5); inboxModel.PlatformScope lists
// only items without an Event (every other site). category "" lists every tab.
func (u *NotificationInboxUseCase) ListInbox(ctx context.Context, userID uuid.UUID, scope *uuid.UUID, category inboxModel.Category, before *inboxModel.Cursor) (
	inboxModel.Page,
	error,
) {
	boundary := inboxModel.Cursor{ID: uuid.FromStringOrNil("ffffffff-ffff-ffff-ffff-ffffffffffff"), CreatedAt: time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)}
	if before != nil {
		boundary = *before
	}
	items, err := u.inbox.ListByUser(ctx, userID, scope, category, boundary)
	if err != nil {
		return inboxModel.Page{}, err
	}
	page := inboxModel.Page{Items: items}
	if len(items) > 10 {
		page.Items = items[:10]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &inboxModel.Cursor{ID: last.ID, CreatedAt: last.CreatedAt}
	}
	return page, nil
}

func (u *NotificationInboxUseCase) PollInbox(ctx context.Context, userID uuid.UUID, scope *uuid.UUID, since *inboxModel.Cursor) (inboxModel.PollResult, error) {
	result := inboxModel.PollResult{NewInbox: []inboxModel.InAppNotification{}}
	if since == nil {
		cursor, err := u.inbox.LatestCursor(ctx, userID, scope)
		if err != nil {
			return result, err
		}
		result.Cursor = cursor
	} else {
		items, err := u.inbox.ListNewSince(ctx, userID, scope, *since)
		if err != nil {
			return result, err
		}
		result.NewInbox = items
		result.Cursor = since
		if len(items) > 0 {
			last := items[len(items)-1]
			result.Cursor = &inboxModel.Cursor{ID: last.ID, CreatedAt: last.CreatedAt}
		}
	}
	count, err := u.inbox.UnreadCount(ctx, userID, scope)
	if err != nil {
		return result, err
	}
	result.UnreadCount = count
	result.Counts, result.OtherEventsCount, err = u.inbox.Counts(ctx, userID, scope)
	if err != nil {
		return result, err
	}
	return result, nil
}

// ListBanners returns all currently active banners, newest first. The client
// displays the first item; after it is dismissed the next item naturally
// becomes visible without rewriting older notifications.
func (u *NotificationInboxUseCase) ListBanners(ctx context.Context, userID uuid.UUID, scope *uuid.UUID) ([]inboxModel.InAppNotification, error) {
	return u.inbox.ListBannersByUser(ctx, userID, scope)
}

func (u *NotificationInboxUseCase) DismissBanner(ctx context.Context, userID, id uuid.UUID) error {
	affected, err := u.inbox.DismissBanner(ctx, userID, id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return notificationModel.ErrInboxNotFound.Err()
	}
	return nil
}

// MarkRead marks a single notification read, scoped to the owning user. A
// non-owner (or missing) notification affects 0 rows and yields a not-found
// error so a cross-user attempt is a clean 404.
func (u *NotificationInboxUseCase) MarkRead(ctx context.Context, userID, id uuid.UUID) error {
	affected, err := u.inbox.MarkRead(ctx, userID, id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return notificationModel.ErrInboxNotFound.Err()
	}
	return nil
}

// MarkAllRead marks the scope's items read, within one tab when category is
// set. Open requests stay open.
func (u *NotificationInboxUseCase) MarkAllRead(ctx context.Context, userID uuid.UUID, scope *uuid.UUID, category inboxModel.Category) error {
	return u.inbox.MarkAllRead(ctx, userID, scope, category)
}

// ResolveRequest closes a request by hand for every recipient. Only a
// recipient may do it (the item must be theirs), and only for types that
// have no domain decision of their own (a failed laboratory).
func (u *NotificationInboxUseCase) ResolveRequest(ctx context.Context, userID, id uuid.UUID) error {
	item, err := u.inbox.GetForUser(ctx, userID, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notificationModel.ErrInboxRequestNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get inbox request").Err()
	}
	if !item.ActionRequired || !inboxModel.ManuallyResolvable(item.Type) {
		return notificationModel.ErrInboxRequestNotResolvable.Err()
	}
	// An already resolved item skips the update and reports the conflict,
	// as does a request resolved concurrently between the read and the update.
	var affected int64
	switch {
	case item.Resolved:
	case item.SubjectRef != "":
		affected, err = u.inbox.ResolveBySubject(ctx, item.SubjectRef, inboxModel.ResolutionResolved, &userID)
	default:
		affected, err = u.inbox.ResolveItem(ctx, userID, id, inboxModel.ResolutionResolved, &userID)
	}
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to resolve inbox request").Err()
	}
	if affected == 0 {
		return notificationModel.ErrInboxRequestResolved.Err()
	}
	return nil
}
