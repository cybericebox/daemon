package dispatchModel

import (
	"context"
	"encoding/json"

	"github.com/gofrs/uuid"

	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

// BroadcastContent is the custom message of one broadcast. Channel handlers
// use it instead of a stored template: the message is authored per send, not
// per notification type.
type BroadcastContent struct {
	ID           uuid.UUID
	ScopeEventID *uuid.UUID
	Subject      string
	Preheader    string
	EmailBody    json.RawMessage
	EmailStyling json.RawMessage
	InAppTitle   string
	InAppBody    string
	InAppLink    string
}

// AsEmailTemplate presents the broadcast as an in-memory published email
// template so the regular composer renders it.
func (b BroadcastContent) AsEmailTemplate() emailModel.EmailTemplate {
	return emailModel.EmailTemplate{
		ID:               b.ID,
		ScopeEventID:     b.ScopeEventID,
		NotificationType: string(notificationTypes.NotificationTypeBroadcast),
		Status:           notificationModel.TemplateStatusPublished,
		Subject:          b.Subject,
		Preheader:        b.Preheader,
		Body:             b.EmailBody,
		Styling:          b.EmailStyling,
	}
}

// AsInAppTemplate presents the broadcast as an in-memory in-app template: an
// inbox item, dismissible, neutral tone.
func (b BroadcastContent) AsInAppTemplate() inAppModel.InAppTemplate {
	return inAppModel.InAppTemplate{
		ID:               b.ID,
		ScopeEventID:     b.ScopeEventID,
		NotificationType: string(notificationTypes.NotificationTypeBroadcast),
		Status:           notificationModel.TemplateStatusPublished,
		Title:            b.InAppTitle,
		Body:             b.InAppBody,
		Link:             b.InAppLink,
		Tone:             "neutral",
		Surface:          "inbox",
		Actions:          json.RawMessage(`[]`),
		Dismissible:      true,
	}
}

type broadcastKey struct{}

// WithBroadcast carries the broadcast content of one dispatch to the channel
// handlers.
func WithBroadcast(ctx context.Context, content *BroadcastContent) context.Context {
	if content == nil {
		return ctx
	}
	return context.WithValue(ctx, broadcastKey{}, *content)
}

// BroadcastFrom returns the broadcast content of the current dispatch, if any.
func BroadcastFrom(ctx context.Context) (BroadcastContent, bool) {
	content, ok := ctx.Value(broadcastKey{}).(BroadcastContent)
	return content, ok
}
