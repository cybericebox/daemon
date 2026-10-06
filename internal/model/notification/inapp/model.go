package inAppModel

import (
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"

	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
)

type InAppTemplate struct {
	ID               uuid.UUID
	ScopeEventID     *uuid.UUID
	NotificationType string
	Status           notificationModel.TemplateStatus
	Title            string
	Body             string
	Link             string
	Icon             string
	Tone             string
	AccentColor      string
	Surface          string
	AutoDismissMs    *int32
	Actions          json.RawMessage
	Dismissible      bool
	PublishedAt      *time.Time
	UpdatedByUserID  *uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// NewDraft is the domain factory for a fresh template version: the id and the
// draft status are set here, not by the caller or the database.
func NewDraft(in CreateTemplateInput) InAppTemplate {
	return InAppTemplate{
		ID:               uuid.Must(uuid.NewV7()),
		ScopeEventID:     in.ScopeEventID,
		NotificationType: in.NotificationType,
		Status:           notificationModel.TemplateStatusDraft,
		Title:            in.Title,
		Body:             in.Body,
		Link:             in.Link,
		Icon:             in.Icon,
		Tone:             in.Tone,
		AccentColor:      in.AccentColor,
		Surface:          in.Surface,
		AutoDismissMs:    in.AutoDismissMs,
		Actions:          in.Actions,
		Dismissible:      in.Dismissible,
		UpdatedByUserID:  new(in.UpdatedBy),
	}
}

func (t *InAppTemplate) IsDraft() bool {
	return t.Status == notificationModel.TemplateStatusDraft
}

func (t *InAppTemplate) IsPublished() bool {
	return t.Status == notificationModel.TemplateStatusPublished
}

func (t *InAppTemplate) IsUnpublished() bool {
	return t.Status == notificationModel.TemplateStatusUnpublished
}
