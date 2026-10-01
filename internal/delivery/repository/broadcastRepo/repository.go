// Package broadcastRepo is the repository for custom broadcasts and their
// audiences: domain shapes in and out, sqlc rows only inside.
package broadcastRepo

import (
	"context"
	"encoding/json"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	CreateNotificationBroadcast(ctx context.Context, arg postgres.CreateNotificationBroadcastParams) (postgres.NotificationBroadcast, error)
	GetNotificationBroadcast(ctx context.Context, id uuid.UUID) (postgres.GetNotificationBroadcastRow, error)
	ListNotificationBroadcasts(ctx context.Context, arg postgres.ListNotificationBroadcastsParams) ([]postgres.ListNotificationBroadcastsRow, error)
	FinishNotificationBroadcast(ctx context.Context, arg postgres.FinishNotificationBroadcastParams) error
	ListBroadcastQueuedUserIDs(ctx context.Context, broadcastID uuid.NullUUID) ([]uuid.UUID, error)
	ListBroadcastDeliveries(ctx context.Context, arg postgres.ListBroadcastDeliveriesParams) ([]postgres.ListBroadcastDeliveriesRow, error)
	ListPlatformBroadcastAudience(ctx context.Context, arg postgres.ListPlatformBroadcastAudienceParams) ([]postgres.ListPlatformBroadcastAudienceRow, error)
	ListEventBroadcastAudience(ctx context.Context, arg postgres.ListEventBroadcastAudienceParams) ([]postgres.ListEventBroadcastAudienceRow, error)
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository { return &Repository{q: q} }

// Create stores a broadcast in the sending state.
func (r *Repository) Create(ctx context.Context, b broadcastModel.Broadcast) error {
	audience, err := json.Marshal(b.Audience)
	if err != nil {
		return err
	}
	channels := make([]string, 0, len(b.Content.Channels))
	for _, ch := range b.Content.Channels {
		channels = append(channels, string(ch))
	}
	_, err = r.q.CreateNotificationBroadcast(ctx, postgres.CreateNotificationBroadcastParams{
		ID:             b.ID,
		ScopeEventID:   nullUUID(b.ScopeEventID),
		CreatedBy:      nullUUID(b.CreatedBy),
		Channels:       channels,
		Subject:        b.Content.Subject,
		Preheader:      b.Content.Preheader,
		EmailBody:      orDefault(b.Content.EmailBody, "[]"),
		EmailStyling:   orDefault(b.Content.EmailStyling, "{}"),
		InappTitle:     b.Content.InAppTitle,
		InappBody:      b.Content.InAppBody,
		InappLink:      b.Content.InAppLink,
		Audience:       audience,
		RecipientCount: b.RecipientCount,
	})
	return err
}

// Get loads one broadcast. Not found propagates raw for the caller to classify.
func (r *Repository) Get(ctx context.Context, id uuid.UUID) (broadcastModel.Broadcast, error) {
	row, err := r.q.GetNotificationBroadcast(ctx, id)
	if err != nil {
		return broadcastModel.Broadcast{}, err
	}
	return toBroadcast(postgres.ListNotificationBroadcastsRow(row))
}

// Content loads the message of a broadcast for the channel handlers.
func (r *Repository) Content(ctx context.Context, id uuid.UUID) (dispatchModel.BroadcastContent, error) {
	b, err := r.Get(ctx, id)
	if err != nil {
		return dispatchModel.BroadcastContent{}, err
	}
	return dispatchModel.BroadcastContent{
		ID: b.ID, ScopeEventID: b.ScopeEventID,
		Subject: b.Content.Subject, Preheader: b.Content.Preheader,
		EmailBody: b.Content.EmailBody, EmailStyling: b.Content.EmailStyling,
		InAppTitle: b.Content.InAppTitle, InAppBody: b.Content.InAppBody, InAppLink: b.Content.InAppLink,
	}, nil
}

// List returns one history page, newest first.
func (r *Repository) List(ctx context.Context, f broadcastModel.ListFilter) ([]broadcastModel.Broadcast, error) {
	rows, err := r.q.ListNotificationBroadcasts(ctx, postgres.ListNotificationBroadcastsParams{
		ScopeFilter: f.Scope, CursorFilter: f.Cursor, LimitVal: f.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]broadcastModel.Broadcast, 0, len(rows))
	for _, row := range rows {
		b, err := toBroadcast(row)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// Finish records the final status and the resolved recipient count.
func (r *Repository) Finish(ctx context.Context, id uuid.UUID, status broadcastModel.Status, recipients int32) error {
	return r.q.FinishNotificationBroadcast(ctx, postgres.FinishNotificationBroadcastParams{
		ID: id, Status: string(status), RecipientCount: recipients,
	})
}

// QueuedUserIDs are the recipients that already have a dispatch, so a re-run
// of the sender skips them.
func (r *Repository) QueuedUserIDs(ctx context.Context, id uuid.UUID) (map[uuid.UUID]bool, error) {
	ids, err := r.q.ListBroadcastQueuedUserIDs(ctx, uuid.NullUUID{UUID: id, Valid: true})
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]bool, len(ids))
	for _, uid := range ids {
		out[uid] = true
	}
	return out, nil
}

// Deliveries lists the recipients' outcomes, failures first.
func (r *Repository) Deliveries(ctx context.Context, id uuid.UUID, limit, offset int32) ([]broadcastModel.Delivery, error) {
	rows, err := r.q.ListBroadcastDeliveries(ctx, postgres.ListBroadcastDeliveriesParams{
		BroadcastID: uuid.NullUUID{UUID: id, Valid: true}, LimitVal: limit, OffsetVal: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]broadcastModel.Delivery, 0, len(rows))
	for _, row := range rows {
		out = append(out, broadcastModel.Delivery{
			DispatchID: row.DispatchID, RecipientUserID: row.RecipientUserID, RecipientEmail: row.RecipientEmail,
			DispatchStatus: row.DispatchStatus, Channel: row.Channel, TargetStatus: row.TargetStatus, Error: row.Error,
		})
	}
	return out, nil
}

// Audience resolves the recipients of an audience definition. eventID nil is
// the platform scope.
func (r *Repository) Audience(ctx context.Context, eventID *uuid.UUID, a broadcastModel.Audience) ([]broadcastModel.Recipient, error) {
	if eventID == nil {
		rows, err := r.q.ListPlatformBroadcastAudience(ctx, postgres.ListPlatformBroadcastAudienceParams{
			Kind: string(a.Kind), Roles: nonNil(a.Roles), UserIds: nonNilIDs(a.UserIDs),
		})
		if err != nil {
			return nil, err
		}
		out := make([]broadcastModel.Recipient, 0, len(rows))
		for _, row := range rows {
			out = append(out, broadcastModel.Recipient{ID: row.ID, Email: row.Email, FirstName: row.FirstName, LastName: row.LastName})
		}
		return out, nil
	}
	rows, err := r.q.ListEventBroadcastAudience(ctx, postgres.ListEventBroadcastAudienceParams{
		EventID: *eventID, Kind: string(a.Kind), TeamIds: nonNilIDs(a.TeamIDs), UserIds: nonNilIDs(a.UserIDs),
	})
	if err != nil {
		return nil, err
	}
	out := make([]broadcastModel.Recipient, 0, len(rows))
	for _, row := range rows {
		out = append(out, broadcastModel.Recipient{ID: row.ID, Email: row.Email, FirstName: row.FirstName, LastName: row.LastName})
	}
	return out, nil
}

func toBroadcast(row postgres.ListNotificationBroadcastsRow) (broadcastModel.Broadcast, error) {
	var audience broadcastModel.Audience
	if err := json.Unmarshal(row.Audience, &audience); err != nil {
		return broadcastModel.Broadcast{}, err
	}
	channels := make([]notificationTypes.NotificationChannel, 0, len(row.Channels))
	for _, ch := range row.Channels {
		channels = append(channels, notificationTypes.NotificationChannel(ch))
	}
	b := broadcastModel.Broadcast{
		ID: row.ID, EventName: row.EventName, CreatedByName: row.CreatedByName,
		Content: broadcastModel.Content{
			Channels: channels, Subject: row.Subject, Preheader: row.Preheader,
			EmailBody: row.EmailBody, EmailStyling: row.EmailStyling,
			InAppTitle: row.InappTitle, InAppBody: row.InappBody, InAppLink: row.InappLink,
		},
		Audience: audience, RecipientCount: row.RecipientCount, SentCount: row.SentCount, FailedCount: row.FailedCount,
		Status: broadcastModel.Status(row.Status), CreatedAt: row.CreatedAt,
	}
	if row.ScopeEventID.Valid {
		b.ScopeEventID = new(row.ScopeEventID.UUID)
	}
	if row.CreatedBy.Valid {
		b.CreatedBy = new(row.CreatedBy.UUID)
	}
	if row.FinishedAt.Valid {
		b.FinishedAt = new(row.FinishedAt.Time)
	}
	return b, nil
}

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func orDefault(raw json.RawMessage, fallback string) []byte {
	if len(raw) == 0 {
		return []byte(fallback)
	}
	return raw
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func nonNilIDs(v []uuid.UUID) []uuid.UUID {
	if v == nil {
		return []uuid.UUID{}
	}
	return v
}
