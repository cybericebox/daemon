// Package inboxRepo is the repository for a user's in-app notification inbox:
// domain shapes in and out, sqlc rows and metadata mapping only inside.
// MarkRead/MarkAllRead are narrow set-scoped updates by design (ownership is
// enforced in SQL, not by loading the aggregate).
package inboxRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	"github.com/cybericebox/daemon/pkg/tools"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	CreateInApp(ctx context.Context, arg postgres.CreateInAppParams) error
	ListInAppByUser(ctx context.Context, arg postgres.ListInAppByUserParams) ([]postgres.ListInAppByUserRow, error)
	GetLatestInboxCursor(ctx context.Context, arg postgres.GetLatestInboxCursorParams) (postgres.GetLatestInboxCursorRow, error)
	ListNewInboxSince(ctx context.Context, arg postgres.ListNewInboxSinceParams) ([]postgres.ListNewInboxSinceRow, error)
	CountUnreadInbox(ctx context.Context, arg postgres.CountUnreadInboxParams) (int64, error)
	ListActiveBannersByUser(ctx context.Context, arg postgres.ListActiveBannersByUserParams) ([]postgres.ListActiveBannersByUserRow, error)
	MarkInAppRead(ctx context.Context, arg postgres.MarkInAppReadParams) (int64, error)
	MarkAllInAppReadByUser(ctx context.Context, arg postgres.MarkAllInAppReadByUserParams) error
	DismissInAppBanner(ctx context.Context, arg postgres.DismissInAppBannerParams) (int64, error)
	CountInboxByCategory(ctx context.Context, arg postgres.CountInboxByCategoryParams) (postgres.CountInboxByCategoryRow, error)
	GetInboxItemForUser(ctx context.Context, arg postgres.GetInboxItemForUserParams) (postgres.GetInboxItemForUserRow, error)
	ResolveInboxBySubjectRef(ctx context.Context, arg postgres.ResolveInboxBySubjectRefParams) (int64, error)
	ResolveInboxItem(ctx context.Context, arg postgres.ResolveInboxItemParams) (int64, error)
	ResolveInboxBySubjectPattern(ctx context.Context, arg postgres.ResolveInboxBySubjectPatternParams) (int64, error)
}

// Every read takes the inbox scope: nil lists all items, an Event id lists
// that Event's items plus items without an Event (M5), and
// inboxModel.PlatformScope lists only items without an Event.

func (r *Repository) ListBannersByUser(ctx context.Context, userID uuid.UUID, scope *uuid.UUID) ([]inboxModel.InAppNotification, error) {
	rows, err := r.q.ListActiveBannersByUser(ctx, postgres.ListActiveBannersByUserParams{UserID: userID, EventFilter: eventFilter(scope)})
	if err != nil {
		return nil, err
	}
	out := make([]inboxModel.InAppNotification, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomain(inboxRow(row)))
	}
	return out, nil
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

// ListByUser lists one page older than before; category "" lists every tab.
func (r *Repository) ListByUser(ctx context.Context, userID uuid.UUID, scope *uuid.UUID, category inboxModel.Category, before inboxModel.Cursor) ([]inboxModel.InAppNotification, error) {
	rows, err := r.q.ListInAppByUser(ctx, postgres.ListInAppByUserParams{
		UserID: userID, EventFilter: eventFilter(scope), CategoryFilter: string(category),
		BeforeCreatedAt: before.CreatedAt, BeforeID: before.ID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]inboxModel.InAppNotification, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomain(inboxRow(row)))
	}
	return out, nil
}

func (r *Repository) LatestCursor(ctx context.Context, userID uuid.UUID, scope *uuid.UUID) (*inboxModel.Cursor, error) {
	row, err := r.q.GetLatestInboxCursor(ctx, postgres.GetLatestInboxCursorParams{UserID: userID, EventFilter: eventFilter(scope)})
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &inboxModel.Cursor{ID: row.ID, CreatedAt: row.CreatedAt}, nil
}

func (r *Repository) ListNewSince(ctx context.Context, userID uuid.UUID, scope *uuid.UUID, since inboxModel.Cursor) ([]inboxModel.InAppNotification, error) {
	rows, err := r.q.ListNewInboxSince(ctx, postgres.ListNewInboxSinceParams{
		UserID: userID, EventFilter: eventFilter(scope), SinceCreatedAt: since.CreatedAt, SinceID: since.ID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]inboxModel.InAppNotification, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomain(inboxRow(row)))
	}
	return out, nil
}

func (r *Repository) UnreadCount(ctx context.Context, userID uuid.UUID, scope *uuid.UUID) (int64, error) {
	return r.q.CountUnreadInbox(ctx, postgres.CountUnreadInboxParams{UserID: userID, EventFilter: eventFilter(scope)})
}

// MarkRead flips one notification read, scoped to the owning user. Returns
// rows affected so the caller can turn 0 into a clean not-found.
func (r *Repository) MarkRead(ctx context.Context, userID, id uuid.UUID) (int64, error) {
	return r.q.MarkInAppRead(ctx, postgres.MarkInAppReadParams{ID: id, UserID: userID})
}

func (r *Repository) MarkAllRead(ctx context.Context, userID uuid.UUID, scope *uuid.UUID, category inboxModel.Category) error {
	return r.q.MarkAllInAppReadByUser(ctx, postgres.MarkAllInAppReadByUserParams{
		UserID: userID, EventFilter: eventFilter(scope), CategoryFilter: string(category),
	})
}

// Counts returns the tab badges for the scope and, for an Event-id scope,
// the attention items of other Events.
func (r *Repository) Counts(ctx context.Context, userID uuid.UUID, scope *uuid.UUID) (inboxModel.Counts, int64, error) {
	row, err := r.q.CountInboxByCategory(ctx, postgres.CountInboxByCategoryParams{UserID: userID, EventFilter: eventFilter(scope)})
	if err != nil {
		return inboxModel.Counts{}, 0, err
	}
	counts := inboxModel.Counts{
		All: row.AllCount, Requests: row.RequestsCount, Personal: row.PersonalCount, Activity: row.ActivityCount,
	}
	return counts, row.OtherEventsCount, nil
}

// RequestItem is the recipient-owned view of one inbox item that the resolve
// flow needs.
type RequestItem struct {
	ID             uuid.UUID
	Type           string
	ActionRequired bool
	SubjectRef     string
	Resolved       bool
}

// GetForUser loads one inbox item owned by userID (not found otherwise).
func (r *Repository) GetForUser(ctx context.Context, userID, id uuid.UUID) (RequestItem, error) {
	row, err := r.q.GetInboxItemForUser(ctx, postgres.GetInboxItemForUserParams{ID: id, UserID: userID})
	if err != nil {
		return RequestItem{}, err
	}
	return RequestItem{
		ID: row.ID, Type: row.NotificationType, ActionRequired: row.ActionRequired,
		SubjectRef: row.SubjectRef.String, Resolved: row.ResolvedAt.Valid,
	}, nil
}

// ResolveBySubject closes every open copy of the request for all recipients;
// by is nil for system resolutions.
func (r *Repository) ResolveBySubject(ctx context.Context, subjectRef string, resolution inboxModel.Resolution, by *uuid.UUID) (int64, error) {
	return r.q.ResolveInboxBySubjectRef(ctx, postgres.ResolveInboxBySubjectRefParams{
		SubjectRef: subjectRef, Resolution: string(resolution), ResolvedBy: nullableUUID(by),
	})
}

// ResolveByPattern closes, without a person, every open request whose
// subject matches the SQL LIKE pattern.
func (r *Repository) ResolveByPattern(ctx context.Context, pattern string, resolution inboxModel.Resolution) (int64, error) {
	return r.q.ResolveInboxBySubjectPattern(ctx, postgres.ResolveInboxBySubjectPatternParams{
		SubjectPattern: pattern, Resolution: string(resolution),
	})
}

// ResolveItem closes one recipient's copy of a request without a subject.
func (r *Repository) ResolveItem(ctx context.Context, userID, id uuid.UUID, resolution inboxModel.Resolution, by *uuid.UUID) (int64, error) {
	return r.q.ResolveInboxItem(ctx, postgres.ResolveInboxItemParams{
		ID: id, UserID: userID, Resolution: string(resolution), ResolvedBy: nullableUUID(by),
	})
}

func (r *Repository) DismissBanner(ctx context.Context, userID, id uuid.UUID) (int64, error) {
	return r.q.DismissInAppBanner(ctx, postgres.DismissInAppBannerParams{ID: id, UserID: userID})
}

// inboxRow is the shared shape of every inbox read row (list, poll,
// banners); the sqlc row types have identical fields and convert to it.
type inboxRow postgres.ListInAppByUserRow

func eventFilter(scope *uuid.UUID) string {
	if scope == nil {
		return ""
	}
	return scope.String()
}

func toDomain(row inboxRow) inboxModel.InAppNotification {
	var readAt *time.Time
	if row.ReadAt.Valid {
		readAt = new(row.ReadAt.Time)
	}
	var eventID *uuid.UUID
	if row.ScopeEventID.Valid {
		eventID = new(row.ScopeEventID.UUID)
	}
	var resolvedBy *uuid.UUID
	if row.ResolvedBy.Valid {
		resolvedBy = new(row.ResolvedBy.UUID)
	}
	return inboxModel.InAppNotification{
		Type:           row.NotificationType,
		Category:       inboxModel.Category(row.Category),
		ActionRequired: row.ActionRequired,
		SubjectRef:     row.SubjectRef.String,
		ResolvedAt:     optionalTime(row.ResolvedAt),
		Resolution:     row.Resolution.String,
		ResolvedBy:     resolvedBy,
		ResolvedByName: row.ResolvedByName,
		EventID:        eventID,
		EventName:      row.EventName,
		EventTag:       row.EventTag,
		ID:             row.ID,
		UserID:         row.UserID,
		Title:          row.Title,
		Body:           row.Body,
		Link:           row.Link,
		Icon:           row.Icon,
		Tone:           row.Tone,
		AccentColor:    row.AccentColor,
		Surface:        row.Surface,
		AutoDismissMs:  optionalInt32(row.AutoDismissMs),
		Actions:        json.RawMessage(row.Actions),
		Dismissible:    row.Dismissible,
		ReadAt:         readAt,
		DismissedAt:    optionalTime(row.DismissedAt),
		CreatedAt:      row.CreatedAt,
	}
}

// Deliver inserts a rendered in-app notification (id generated here; the
// content is produced by the channel handler from the published template).
type Delivery struct {
	Title         string
	Body          string
	Link          string
	Icon          string
	Tone          string
	AccentColor   string
	Surface       string
	AutoDismissMs *int32
	Actions       json.RawMessage
	Dismissible   bool
	// ScopeEventID ties the item to an Event inbox; nil = account/system.
	ScopeEventID *uuid.UUID
	// Inbox is the server-computed classification (type, category, request).
	Inbox inboxModel.Meta
}

func (r *Repository) Deliver(ctx context.Context, userID uuid.UUID, d Delivery) error {
	return r.q.CreateInApp(ctx, postgres.CreateInAppParams{
		ID:            tools.NewUUIDv7(),
		UserID:        userID,
		Title:         d.Title,
		Body:          d.Body,
		Link:          d.Link,
		Icon:          d.Icon,
		Tone:          d.Tone,
		AccentColor:   d.AccentColor,
		Surface:       d.Surface,
		AutoDismissMs: nullableInt32(d.AutoDismissMs),
		Actions:       d.Actions,
		Dismissible:   d.Dismissible,
		ScopeEventID:  nullableUUID(d.ScopeEventID),
		// A zero Meta (legacy callers) stores the default tab: personal.
		NotificationType: d.Inbox.Type,
		Category:         string(d.Inbox.Category),
		ActionRequired:   d.Inbox.ActionRequired,
		SubjectRef:       pgtype.Text{String: d.Inbox.SubjectRef, Valid: d.Inbox.SubjectRef != ""},
		RaisedAt:         nullableTime(d.Inbox.RaisedAt),
	})
}

func optionalTime(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	return new(v.Time)
}
func nullableTime(v *time.Time) pgtype.Timestamptz {
	if v == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *v, Valid: true}
}

func optionalInt32(v pgtype.Int4) *int32 {
	if !v.Valid {
		return nil
	}
	return new(v.Int32)
}
func nullableInt32(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}

func nullableUUID(v *uuid.UUID) uuid.NullUUID {
	if v == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *v, Valid: true}
}
