// Package broadcastUseCase composes, sends and lists custom broadcasts. A
// broadcast goes out through the regular dispatch pipeline: every recipient is
// one dispatch (so it lands in the delivery journal) whose channels render the
// authored content instead of a stored template.
package broadcastUseCase

import (
	"context"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/broadcastRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
)

const (
	// chunkSize bounds the recipients one job run queues (River jobs time out
	// after a minute); the job queues itself again while recipients remain.
	chunkSize = 200
)

type (
	repoPort interface {
		broadcastRepo.Queries
		eventRepo.Queries
	}

	// Notifier is the dispatcher's enqueue side.
	Notifier interface {
		Notify(ctx context.Context, userID uuid.UUID, n notificationTypes.NotificationPayload, opts ...dispatchModel.NotifyOption) error
	}

	Dependencies struct {
		Repo     repoPort
		Notifier Notifier
		// Enqueue queues one sender run (the River enqueuer in production).
		Enqueue func(ctx context.Context, args jobsModel.BroadcastSendArgs) error
		// EventDomain is the domain event sites live under (<tag>.<domain>).
		EventDomain string
	}

	NotificationBroadcastUseCase struct {
		broadcasts *broadcastRepo.Repository
		events     *eventRepo.Repository
		notifier   Notifier
		enqueue    func(ctx context.Context, args jobsModel.BroadcastSendArgs) error
		domain     string
	}

	// SendInput is one broadcast to send. ScopeEventID nil is the platform.
	SendInput struct {
		ScopeEventID *uuid.UUID
		ActorID      uuid.UUID
		Content      broadcastModel.Content
		Audience     broadcastModel.Audience
	}
)

func NewNotificationBroadcastUseCase(deps Dependencies) *NotificationBroadcastUseCase {
	u := &NotificationBroadcastUseCase{
		broadcasts: broadcastRepo.New(deps.Repo),
		events:     eventRepo.New(deps.Repo),
		notifier:   deps.Notifier,
		enqueue:    deps.Enqueue,
		domain:     deps.EventDomain,
	}
	return u
}

// CountBroadcastAudience is the recipient count shown before sending.
func (u *NotificationBroadcastUseCase) CountBroadcastAudience(ctx context.Context, scopeEventID *uuid.UUID, a broadcastModel.Audience) (int, error) {
	if err := a.Validate(scopeEventID != nil); err != nil {
		return 0, err
	}
	recipients, err := u.broadcasts.Audience(ctx, scopeEventID, a)
	if err != nil {
		return 0, model.ErrPlatform.WithError(err).WithMessage("Failed to resolve the audience").Err()
	}
	return len(recipients), nil
}

// SendBroadcast validates the message, stores it and queues the sender. The
// recipients are dispatched by the sender job, not in the request.
func (u *NotificationBroadcastUseCase) SendBroadcast(ctx context.Context, in SendInput) (broadcastModel.Broadcast, error) {
	if err := in.Content.Validate(); err != nil {
		return broadcastModel.Broadcast{}, err
	}
	if err := in.Audience.Validate(in.ScopeEventID != nil); err != nil {
		return broadcastModel.Broadcast{}, err
	}
	recipients, err := u.broadcasts.Audience(ctx, in.ScopeEventID, in.Audience)
	if err != nil {
		return broadcastModel.Broadcast{}, model.ErrPlatform.WithError(err).WithMessage("Failed to resolve the audience").Err()
	}
	if len(recipients) == 0 {
		return broadcastModel.Broadcast{}, notificationModel.ErrBroadcastNoRecipients.Err()
	}
	b := broadcastModel.Broadcast{
		ID:             uuid.Must(uuid.NewV7()),
		ScopeEventID:   in.ScopeEventID,
		CreatedBy:      &in.ActorID,
		Content:        in.Content,
		Audience:       in.Audience,
		RecipientCount: int32(len(recipients)),
		Status:         broadcastModel.StatusSending,
	}
	if err = u.broadcasts.Create(ctx, b); err != nil {
		return broadcastModel.Broadcast{}, model.ErrPlatform.WithError(err).WithMessage("Failed to store the broadcast").Err()
	}
	if err = u.enqueue(ctx, jobsModel.BroadcastSendArgs{BroadcastID: b.ID}); err != nil {
		_ = u.broadcasts.Finish(ctx, b.ID, broadcastModel.StatusFailed, b.RecipientCount)
		return broadcastModel.Broadcast{}, model.ErrPlatform.WithError(err).WithMessage("Failed to queue the broadcast").Err()
	}
	return b, nil
}

// ProcessBroadcast queues the next chunk of recipients that have no dispatch
// yet, paced by batch. It queues itself again while recipients remain and
// closes the broadcast when none do; re-running it after a failure never
// duplicates a message (one dispatch per recipient and broadcast).
func (u *NotificationBroadcastUseCase) ProcessBroadcast(ctx context.Context, args jobsModel.BroadcastSendArgs) error {
	b, err := u.broadcasts.Get(ctx, args.BroadcastID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil // deleted with its event: nothing to send
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to load the broadcast").Err()
	}
	if b.Status != broadcastModel.StatusSending {
		return nil
	}
	recipients, err := u.broadcasts.Audience(ctx, b.ScopeEventID, b.Audience)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to resolve the audience").Err()
	}
	queued, err := u.broadcasts.QueuedUserIDs(ctx, b.ID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to load the queued recipients").Err()
	}
	pending := make([]broadcastModel.Recipient, 0, len(recipients))
	for _, r := range recipients {
		if !queued[r.ID] {
			pending = append(pending, r)
		}
	}

	eventVars, err := u.eventVars(ctx, b.ScopeEventID)
	if err != nil {
		return err
	}
	role := inboxModel.RoleSubject
	if b.ScopeEventID != nil {
		role = inboxModel.RoleParticipant
	}
	channels := b.Content.Channels

	chunk := pending
	if len(chunk) > chunkSize {
		chunk = chunk[:chunkSize]
	}
	failed := 0
	// Queuing is cheap; the send rate is the SMTP transport's limit, enforced
	// where each message is sent.
	for _, r := range chunk {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		payload := notificationPayloads.BroadcastPayload{
			UserID: r.ID.String(), UserEmail: r.Email, UserFirstName: r.FirstName, UserLastName: r.LastName,
			UserName:  strings.TrimSpace(r.FirstName + " " + r.LastName),
			EventName: eventVars.name, EventTag: eventVars.tag, EventURL: eventVars.url,
		}
		opts := []dispatchModel.NotifyOption{
			dispatchModel.WithOverrideChannels(channels...),
			dispatchModel.WithBroadcastID(b.ID),
			dispatchModel.WithInbox(inboxModel.NewMeta(string(notificationTypes.NotificationTypeBroadcast), role, "")),
		}
		if b.ScopeEventID != nil {
			opts = append(opts, dispatchModel.WithEventScope(*b.ScopeEventID))
		}
		if err = u.notifier.Notify(ctx, r.ID, payload, opts...); err != nil {
			failed++
			log.Error().Err(err).Str("broadcast", b.ID.String()).Str("user", r.ID.String()).Msg("broadcast recipient was not queued")
		}
	}

	remaining := len(pending) - len(chunk) + failed
	if remaining > 0 && failed < len(chunk) {
		// More recipients (or retryable failures) remain: continue in a new run.
		if err = u.enqueue(ctx, jobsModel.BroadcastSendArgs{BroadcastID: b.ID}); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to queue the next broadcast chunk").Err()
		}
		return nil
	}
	status := broadcastModel.StatusDone
	if failed > 0 && failed == len(chunk) && len(queued) == 0 {
		status = broadcastModel.StatusFailed
	}
	if err = u.broadcasts.Finish(ctx, b.ID, status, int32(len(recipients))); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to close the broadcast").Err()
	}
	return nil
}

type eventVars struct{ name, tag, url string }

func (u *NotificationBroadcastUseCase) eventVars(ctx context.Context, eventID *uuid.UUID) (eventVars, error) {
	if eventID == nil {
		return eventVars{}, nil
	}
	e, err := u.events.GetByID(ctx, *eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventVars{}, nil
		}
		return eventVars{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load the event").Err()
	}
	v := eventVars{name: e.Name, tag: e.Tag}
	if e.Tag != "" && u.domain != "" {
		v.url = "https://" + e.Tag + "." + u.domain + "/"
	}
	return v, nil
}

// ListBroadcasts is one history page. scope is "" (all), "platform" or an
// Event id.
func (u *NotificationBroadcastUseCase) ListBroadcasts(ctx context.Context, f broadcastModel.ListFilter) ([]broadcastModel.Broadcast, error) {
	f.Limit = clampLimit(f.Limit)
	items, err := u.broadcasts.List(ctx, f)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list broadcasts").Err()
	}
	return items, nil
}

// GetBroadcast loads one broadcast; expectedEventID (when not nil) hides a
// broadcast of another scope behind a plain not-found.
func (u *NotificationBroadcastUseCase) GetBroadcast(ctx context.Context, id uuid.UUID, scopeEventID *uuid.UUID) (broadcastModel.Broadcast, error) {
	b, err := u.broadcasts.Get(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return broadcastModel.Broadcast{}, notificationModel.ErrBroadcastNotFound.Err()
		}
		return broadcastModel.Broadcast{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load the broadcast").Err()
	}
	if scopeEventID != nil && (b.ScopeEventID == nil || *b.ScopeEventID != *scopeEventID) {
		return broadcastModel.Broadcast{}, notificationModel.ErrBroadcastNotFound.Err()
	}
	return b, nil
}

// ListBroadcastDeliveries lists the recipients' outcomes, failures first.
func (u *NotificationBroadcastUseCase) ListBroadcastDeliveries(ctx context.Context, id uuid.UUID, limit, offset int32) ([]broadcastModel.Delivery, error) {
	items, err := u.broadcasts.Deliveries(ctx, id, clampLimit(limit), offset)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list broadcast deliveries").Err()
	}
	return items, nil
}

func clampLimit(limit int32) int32 {
	if limit <= 0 {
		return 50
	}
	return min(limit, 100)
}
