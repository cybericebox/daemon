// Package dispatchRepo is the repository for notification dispatches and their
// per-channel targets: domain shapes in and out, sqlc rows only inside.
package dispatchRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	CreateDispatch(ctx context.Context, arg postgres.CreateDispatchParams) (postgres.NotificationDispatch, error)
	SetDispatchStatus(ctx context.Context, arg postgres.SetDispatchStatusParams) error
	UpsertDispatchTarget(ctx context.Context, arg postgres.UpsertDispatchTargetParams) error
	GetActiveChannels(ctx context.Context, arg postgres.GetActiveChannelsParams) ([]string, error)
	GetDispatch(ctx context.Context, id uuid.UUID) (postgres.GetDispatchRow, error)
	ListDispatches(ctx context.Context, arg postgres.ListDispatchesParams) ([]postgres.ListDispatchesRow, error)
	CountDispatches(ctx context.Context, arg postgres.CountDispatchesParams) (int64, error)
	ListDispatchTargets(ctx context.Context, dispatchID uuid.UUID) ([]postgres.NotificationDispatchTarget, error)
	ListDispatchTargetsByDispatches(ctx context.Context, dispatchIds []uuid.UUID) ([]postgres.NotificationDispatchTarget, error)
	CountDispatchesByStatusSince(ctx context.Context, createdAt time.Time) ([]postgres.CountDispatchesByStatusSinceRow, error)
	CountDispatchesByTypeSince(ctx context.Context, createdAt time.Time) ([]postgres.CountDispatchesByTypeSinceRow, error)
	CountTargetsByChannelStatusSince(ctx context.Context, createdAt time.Time) ([]postgres.CountTargetsByChannelStatusSinceRow, error)
	CountEmailDeliveredSince(ctx context.Context, arg postgres.CountEmailDeliveredSinceParams) (int64, error)
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

// Create inserts a pending dispatch row; scopeEventID ties it to an Event
// journal (nil = platform).
func (r *Repository) Create(ctx context.Context, id uuid.UUID, notificationType string, recipientUserID uuid.UUID, scopeEventID *uuid.UUID) error {
	return r.CreateForBroadcast(ctx, id, notificationType, recipientUserID, scopeEventID, nil)
}

// CreateForBroadcast is Create for one recipient of a broadcast: the row keeps
// the link to the broadcast (nil = not a broadcast dispatch).
func (r *Repository) CreateForBroadcast(ctx context.Context, id uuid.UUID, notificationType string, recipientUserID uuid.UUID, scopeEventID, broadcastID *uuid.UUID) error {
	scope := uuid.NullUUID{}
	if scopeEventID != nil {
		scope = uuid.NullUUID{UUID: *scopeEventID, Valid: true}
	}
	broadcast := uuid.NullUUID{}
	if broadcastID != nil {
		broadcast = uuid.NullUUID{UUID: *broadcastID, Valid: true}
	}
	_, err := r.q.CreateDispatch(ctx, postgres.CreateDispatchParams{
		ID:               id,
		NotificationType: notificationType,
		RecipientUserID:  recipientUserID,
		Status:           string(dispatchModel.DispatchStatusPending),
		ScopeEventID:     scope,
		BroadcastID:      broadcast,
	})
	return err
}

func (r *Repository) SetStatus(ctx context.Context, id uuid.UUID, status dispatchModel.DispatchStatus) error {
	return r.q.SetDispatchStatus(ctx, postgres.SetDispatchStatusParams{ID: id, Status: string(status)})
}

// TargetResult is the journal record of one channel of a dispatch run.
type TargetResult struct {
	Status   dispatchModel.TargetStatus
	Error    string
	Attempts int32
	Note     dispatchModel.DeliveryNote
}

func (r *Repository) UpsertTarget(
	ctx context.Context,
	dispatchID uuid.UUID,
	channel notificationTypes.NotificationChannel,
	result TargetResult,
) error {
	return r.q.UpsertDispatchTarget(ctx, postgres.UpsertDispatchTargetParams{
		DispatchID: dispatchID, Channel: string(channel), Status: string(result.Status), Error: result.Error,
		Attempts: result.Attempts, Transport: result.Note.Transport, Recipient: result.Note.Recipient,
		FallbackError: result.Note.FallbackError,
	})
}

// ActiveChannels resolves the enabled channels for a user + notification type
// (global settings overlaid with the user's own preferences, in SQL).
func (r *Repository) ActiveChannels(ctx context.Context, userID uuid.UUID, notificationType string) ([]notificationTypes.NotificationChannel, error) {
	chans, err := r.q.GetActiveChannels(ctx, postgres.GetActiveChannelsParams{
		UserID: userID, NotificationType: notificationType,
	})
	if err != nil {
		return nil, err
	}
	out := make([]notificationTypes.NotificationChannel, 0, len(chans))
	for _, ch := range chans {
		out = append(out, notificationTypes.NotificationChannel(ch))
	}
	return out, nil
}

// Get loads one dispatch. Not found propagates raw for the caller to classify.
func (r *Repository) Get(ctx context.Context, id uuid.UUID) (dispatchModel.DispatchInfo, error) {
	row, err := r.q.GetDispatch(ctx, id)
	if err != nil {
		return dispatchModel.DispatchInfo{}, err
	}
	return toInfo(journalRow(row)), nil
}

// List returns one journal page with every dispatch's targets.
func (r *Repository) List(ctx context.Context, f dispatchModel.ListDispatchesFilter) ([]dispatchModel.DispatchDetail, int64, error) {
	rows, err := r.q.ListDispatches(ctx, postgres.ListDispatchesParams{
		TypeFilter:      f.Type,
		StatusFilter:    f.Status,
		UserFilter:      f.User,
		EventFilter:     f.Event,
		ChannelFilter:   f.Channel,
		ResultFilter:    f.Result,
		TransportFilter: f.Transport,
		CursorFilter:    f.Cursor,
		LimitVal:        f.Limit,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := r.q.CountDispatches(ctx, postgres.CountDispatchesParams{
		TypeFilter: f.Type, StatusFilter: f.Status, UserFilter: f.User, EventFilter: f.Event,
		ChannelFilter: f.Channel, ResultFilter: f.Result, TransportFilter: f.Transport,
	})
	if err != nil {
		return nil, 0, err
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	targets := map[uuid.UUID][]dispatchModel.DispatchTarget{}
	if len(ids) > 0 {
		targetRows, err := r.q.ListDispatchTargetsByDispatches(ctx, ids)
		if err != nil {
			return nil, 0, err
		}
		for _, t := range targetRows {
			targets[t.DispatchID] = append(targets[t.DispatchID], toTarget(t))
		}
	}
	out := make([]dispatchModel.DispatchDetail, 0, len(rows))
	for _, row := range rows {
		ts := targets[row.ID]
		if ts == nil {
			ts = []dispatchModel.DispatchTarget{}
		}
		out = append(out, dispatchModel.DispatchDetail{DispatchInfo: toInfo(journalRow(row)), Targets: ts})
	}
	return out, total, nil
}

func (r *Repository) ListTargets(ctx context.Context, dispatchID uuid.UUID) ([]dispatchModel.DispatchTarget, error) {
	rows, err := r.q.ListDispatchTargets(ctx, dispatchID)
	if err != nil {
		return nil, err
	}
	out := make([]dispatchModel.DispatchTarget, 0, len(rows))
	for _, t := range rows {
		out = append(out, toTarget(t))
	}
	return out, nil
}

func (r *Repository) CountByStatusSince(ctx context.Context, since time.Time) ([]dispatchModel.KeyCount, error) {
	rows, err := r.q.CountDispatchesByStatusSince(ctx, since)
	if err != nil {
		return nil, err
	}
	out := make([]dispatchModel.KeyCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, dispatchModel.KeyCount{Key: row.Status, Count: row.Count})
	}
	return out, nil
}

func (r *Repository) CountByTypeSince(ctx context.Context, since time.Time) ([]dispatchModel.KeyCount, error) {
	rows, err := r.q.CountDispatchesByTypeSince(ctx, since)
	if err != nil {
		return nil, err
	}
	out := make([]dispatchModel.KeyCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, dispatchModel.KeyCount{Key: row.NotificationType, Count: row.Count})
	}
	return out, nil
}

func (r *Repository) CountTargetsByChannelStatusSince(ctx context.Context, since time.Time) ([]dispatchModel.ChannelStatusCount, error) {
	rows, err := r.q.CountTargetsByChannelStatusSince(ctx, since)
	if err != nil {
		return nil, err
	}
	out := make([]dispatchModel.ChannelStatusCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, dispatchModel.ChannelStatusCount{Channel: row.Channel, Status: row.Status, Count: row.Count})
	}
	return out, nil
}

// journalRow is the shared journal row shape (get and list rows have the
// same fields).
type journalRow postgres.GetDispatchRow

func toInfo(row journalRow) dispatchModel.DispatchInfo {
	var scope *uuid.UUID
	if row.ScopeEventID.Valid {
		scope = new(row.ScopeEventID.UUID)
	}
	var broadcast *uuid.UUID
	if row.BroadcastID.Valid {
		broadcast = new(row.BroadcastID.UUID)
	}
	return dispatchModel.DispatchInfo{
		BroadcastID:      broadcast,
		ID:               row.ID,
		NotificationType: row.NotificationType,
		RecipientUserID:  row.RecipientUserID,
		RecipientEmail:   row.RecipientEmail,
		RecipientName:    row.RecipientName,
		ScopeEventID:     scope,
		EventName:        row.EventName,
		Status:           row.Status,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}
}

func toTarget(t postgres.NotificationDispatchTarget) dispatchModel.DispatchTarget {
	return dispatchModel.DispatchTarget{
		Channel: t.Channel, Status: t.Status, Error: t.Error, Attempts: t.Attempts,
		Transport: t.Transport, Recipient: t.Recipient, FallbackError: t.FallbackError, UpdatedAt: t.UpdatedAt,
	}
}

// CountEmailDelivered is the number of emails delivered through transport
// since a moment (the rolling daily quota). eventID narrows it to one Event's
// own transport; nil counts everything that went through transport.
func (r *Repository) CountEmailDelivered(ctx context.Context, transport string, eventID *uuid.UUID, since time.Time) (int64, error) {
	arg := postgres.CountEmailDeliveredSinceParams{Transport: transport, Since: since}
	if eventID != nil {
		arg.EventID = uuid.NullUUID{UUID: *eventID, Valid: true}
	}
	return r.q.CountEmailDeliveredSince(ctx, arg)
}

// MailFunnelsFromRow maps the funnel statement's row; the event and platform
// analytics repositories share it.
func MailFunnelsFromRow(row postgres.GetMailFunnelsRow) dispatchModel.MailFunnels {
	return dispatchModel.MailFunnels{
		InvitationsSent: row.InvitationsSent, InvitationsAccepted: row.InvitationsAccepted,
		InvitationAcceptSamples:       row.InvitationAcceptSamples,
		InvitationAcceptMedianSeconds: row.InvitationAcceptMedianSeconds,
		RegistrationsStarted:          row.RegistrationsStarted, RegistrationsCompleted: row.RegistrationsCompleted,
		ApplicationsSubmitted: row.ApplicationsSubmitted, ApplicationsApproved: row.ApplicationsApproved,
		ApplicationsRejected:             row.ApplicationsRejected,
		ApplicationDecisionSamples:       row.ApplicationDecisionSamples,
		ApplicationDecisionMedianSeconds: row.ApplicationDecisionMedianSeconds,
	}
}
