package mailUseCase

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/pkg/email"
)

// Deliver sends one rendered message. eventID is set only for participant
// mail of an Event: it is sent as the Event (name, <tag>@<sending domain>,
// Event Reply-To) through the Event SMTP when one is stored, and retried once
// through the platform transport when that fails. Everything else goes out as
// the platform through the platform transport (database row, else env).
// Settings are read on every call, so edits apply without a restart.
func (u *MailUseCase) Deliver(ctx context.Context, eventID *uuid.UUID, msg email.Message) error {
	note := dispatchModel.DeliveryNoteFrom(ctx)
	if note == nil {
		note = &dispatchModel.DeliveryNote{}
	}
	note.Recipient = msg.To

	route, err := u.platformRoute(ctx)
	if err != nil {
		return err
	}
	eventSender := false
	if eventID != nil {
		eventTransport, hasEventSMTP, eventIdentity, err := u.eventRoute(ctx, *eventID, route)
		if err != nil {
			return err
		}
		msg, eventSender = withIdentity(msg, eventIdentity), true
		if hasEventSMTP {
			note.Transport = dispatchModel.TransportEvent
			eventErr := u.send(ctx, eventTransport, msg)
			if eventErr == nil {
				return nil
			}
			if _, deferred := dispatchModel.AsDeferred(eventErr); deferred || !route.configured {
				// A send limit waits for its window; the platform route must
				// not take the message over.
				return eventErr
			}
			note.FallbackError = eventErr.Error()
			note.ExtraAttempts++
		}
	}
	if !route.configured {
		return ErrNotConfigured
	}
	return u.sendPlatform(ctx, route, msg, eventSender, note)
}

// sendPlatform walks the platform route: the first provider that takes the
// message wins. A provider limit (rate, daily) or failure (authentication,
// quota, outage) moves on to the next one; a message the provider rejected on
// its merits (a bad recipient) fails at once, since every provider would say
// the same. Platform mail goes out with the sender of the provider that sends
// it; Event mail keeps the Event sender (eventSender).
func (u *MailUseCase) sendPlatform(ctx context.Context, route platformRoute, msg email.Message, eventSender bool, note *dispatchModel.DeliveryNote) error {
	if len(route.transports) == 0 {
		switch {
		case route.exhausted:
			return &dispatchModel.DeferredError{Message: dispatchModel.DeferredQuotaMessage, RetryAfter: quotaRetryAfter}
		case route.err != nil:
			return route.err
		}
		return ErrNotConfigured
	}
	var failure, deferred error
	for _, t := range route.transports {
		out := msg
		if !eventSender {
			out = withIdentity(msg, t.identity)
		}
		note.Transport = t.source
		err := u.send(ctx, t, out)
		if err == nil {
			return nil
		}
		if _, ok := dispatchModel.AsDeferred(err); ok {
			if deferred == nil {
				deferred = err
			}
			continue
		}
		if !mailModel.ProviderFailure(err) {
			return err
		}
		failure = err
		note.FallbackError = err.Error()
		note.ExtraAttempts++
	}
	if deferred != nil {
		// Retry later rather than report a failure another provider may fix.
		return deferred
	}
	return failure
}

// eventRoute resolves the Event sender and its own transport, if any.
func (u *MailUseCase) eventRoute(ctx context.Context, eventID uuid.UUID, platform platformRoute) (transport, bool, mailModel.Identity, error) {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		return transport{}, false, mailModel.Identity{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	own, err := u.mail.EventIdentity(ctx, eventID)
	if err != nil {
		return transport{}, false, mailModel.Identity{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load event sender").Err()
	}
	identity, err := mailModel.EventIdentity(platform.identity, platform.domain, e.Name, e.Tag, own)
	if err != nil {
		return transport{}, false, mailModel.Identity{}, fmt.Errorf("event sender: %w", err)
	}
	cfg, ok, err := u.mail.EventSMTP(ctx, eventID)
	if err != nil {
		return transport{}, false, mailModel.Identity{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load event SMTP settings").Err()
	}
	if !ok {
		return transport{}, false, identity, nil
	}
	conn, err := u.connection(cfg, "")
	if err != nil {
		return transport{}, false, mailModel.Identity{}, err
	}
	return transport{
		source: dispatchModel.TransportEvent, conn: conn, identity: identity,
		limits: cfg.StoredLimits(), eventID: &eventID,
	}, true, identity, nil
}

func withIdentity(msg email.Message, id mailModel.Identity) email.Message {
	msg.From = email.Address{Name: id.FromName, Email: id.FromAddress}
	msg.ReplyTo = email.Address{Name: id.ReplyToName, Email: id.ReplyToAddress}
	return msg
}
