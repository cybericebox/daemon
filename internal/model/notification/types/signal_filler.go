package notificationTypes

import (
	"encoding/json"

	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// SignalPayloadFiller converts a durable signal payload into final variables
// for one recipient. The dispatcher therefore receives only a ready personal
// payload and remains independent of Event-domain routing and enrichment.
type SignalPayloadFiller func(json.RawMessage, userModel.NotificationProfile) (NotificationPayload, error)

var signalPayloadFillers = map[NotificationType]SignalPayloadFiller{}

// RegisterSignalPayloadFiller registers the code-owned contract for a signal
// notification type. Registration happens during application bootstrap; a
// later registration deliberately replaces the same type's factory so every
// fresh planner sees the current immutable contract.
func RegisterSignalPayloadFiller(t NotificationType, filler SignalPayloadFiller) {
	signalPayloadFillers[t] = filler
}

// FillSignalPayload creates the ready recipient-specific payload for a
// durable signal. Unknown types intentionally return false: other hooks can
// still process a valid signal that has no notification representation.
func FillSignalPayload(t NotificationType, raw json.RawMessage, recipient userModel.NotificationProfile) (NotificationPayload, bool, error) {
	filler, ok := signalPayloadFillers[t]
	if !ok {
		return nil, false, nil
	}
	payload, err := filler(raw, recipient)
	return payload, true, err
}
