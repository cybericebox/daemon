// Package eventNotificationRepo owns the Event-level notification subscription
// overrides. An Event inherits every (signal, channel) subscription from the
// platform defaults; an Event row only overrides one pair, and deleting it
// restores inheritance. It is deliberately separate from rendering and
// dispatching.
package eventNotificationRepo

import (
	"context"
	"encoding/json"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

type Queries interface {
	ListEffectiveEventSignalNotificationSubscriptions(context.Context, uuid.UUID) ([]postgres.ListEffectiveEventSignalNotificationSubscriptionsRow, error)
	UpsertEventSignalNotificationSubscription(context.Context, postgres.UpsertEventSignalNotificationSubscriptionParams) (postgres.UpsertEventSignalNotificationSubscriptionRow, error)
	DeleteEventSignalNotificationSubscription(context.Context, postgres.DeleteEventSignalNotificationSubscriptionParams) (int64, error)
}

type Repository struct{ q Queries }

// Source tells whether an effective subscription is inherited from the
// platform default or overridden by the Event.
const (
	SourcePlatform = "platform"
	SourceEvent    = "event"
)

type Subscription struct {
	SignalType string
	Channel    string
	Enabled    bool
	Audience   json.RawMessage
	// Config holds the per-signal options (e.g. days_before_start). On
	// Upsert an empty Config keeps the stored options.
	Config json.RawMessage
}

// EffectiveSubscription is a subscription as the Event sees it: its own
// override when present, otherwise the platform default.
type EffectiveSubscription struct {
	Subscription
	Source string
}

func New(q Queries) *Repository { return &Repository{q: q} }

// List returns the effective subscriptions of the Event, restricted to the
// Event-scoped notification types (platform-only types such as manager
// assignment are not configurable per Event).
func (r *Repository) List(ctx context.Context, eventID uuid.UUID) ([]EffectiveSubscription, error) {
	rows, err := r.q.ListEffectiveEventSignalNotificationSubscriptions(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]EffectiveSubscription, 0, len(rows))
	for _, row := range rows {
		if !notificationTypes.IsEventScoped(notificationTypes.NotificationType(row.SignalType)) {
			continue
		}
		out = append(out, EffectiveSubscription{
			Subscription: Subscription{SignalType: row.SignalType, Channel: row.Channel, Enabled: row.Enabled, Audience: row.Audience, Config: row.Config},
			Source:       row.Source,
		})
	}
	return out, nil
}

func (r *Repository) Upsert(ctx context.Context, eventID uuid.UUID, in Subscription) (Subscription, error) {
	row, err := r.q.UpsertEventSignalNotificationSubscription(ctx, postgres.UpsertEventSignalNotificationSubscriptionParams{
		ScopeEventID: eventID, SignalType: in.SignalType, Channel: in.Channel, Enabled: in.Enabled, Audience: in.Audience, Config: in.Config,
	})
	if err != nil {
		return Subscription{}, err
	}
	return Subscription{SignalType: row.SignalType, Channel: row.Channel, Enabled: row.Enabled, Audience: row.Audience, Config: row.Config}, nil
}

// Delete removes the Event override of one (signal, channel) pair so it
// inherits the platform default again. It reports the number of removed rows.
func (r *Repository) Delete(ctx context.Context, eventID uuid.UUID, signalType, channel string) (int64, error) {
	return r.q.DeleteEventSignalNotificationSubscription(ctx, postgres.DeleteEventSignalNotificationSubscriptionParams{
		ScopeEventID: eventID, SignalType: signalType, Channel: channel,
	})
}
