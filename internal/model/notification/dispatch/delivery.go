package dispatchModel

import (
	"context"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
)

// TypeSMTPTest is the journal kind of an SMTP test send from the mail
// settings; it is not a template-backed notification type.
const TypeSMTPTest = "smtp_test"

// Transport values recorded for email targets: the SMTP route that made the
// final attempt.
const (
	TransportEvent    = "event"
	TransportPlatform = "platform"
	TransportEnv      = "env"
)

// DeliveryNote is filled by a channel handler during one delivery attempt so
// the dispatcher can journal how the message went out. Handlers that do not
// know about it simply leave it empty.
type DeliveryNote struct {
	Transport     string
	Recipient     string
	FallbackError string
	// ExtraAttempts counts sends beyond the dispatcher's own call (an Event
	// SMTP failure retried via the platform transport adds one).
	ExtraAttempts int32
}

type deliveryNoteKey struct{}

// WithDeliveryNote carries note to the channel handler of one attempt.
func WithDeliveryNote(ctx context.Context, note *DeliveryNote) context.Context {
	return context.WithValue(ctx, deliveryNoteKey{}, note)
}

// DeliveryNoteFrom returns the note of the current attempt, or nil when the
// caller does not journal (previews, tests).
func DeliveryNoteFrom(ctx context.Context) *DeliveryNote {
	note, _ := ctx.Value(deliveryNoteKey{}).(*DeliveryNote)
	return note
}

type inboxMetaKey struct{}

// WithInboxMeta carries the in-app classification of one dispatch to the
// channel handler (the handler interface stays channel-agnostic).
func WithInboxMeta(ctx context.Context, meta *inboxModel.Meta) context.Context {
	if meta == nil {
		return ctx
	}
	return context.WithValue(ctx, inboxMetaKey{}, *meta)
}

// InboxMetaFrom returns the dispatch's inbox classification, if any.
func InboxMetaFrom(ctx context.Context) (inboxModel.Meta, bool) {
	meta, ok := ctx.Value(inboxMetaKey{}).(inboxModel.Meta)
	return meta, ok
}
