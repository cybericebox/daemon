package emailModel

import (
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"

	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
)

type EmailTemplate struct {
	ID               uuid.UUID
	ScopeEventID     *uuid.UUID
	NotificationType string
	Status           notificationModel.TemplateStatus
	Subject          string
	Preheader        string
	Body             json.RawMessage
	Styling          json.RawMessage
	PublishedAt      *time.Time
	UpdatedByUserID  *uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// NewDraft is the domain factory for a fresh template version: the id and the
// draft status are set here, not by the caller or the database.
func NewDraft(in CreateTemplateInput) EmailTemplate {
	return EmailTemplate{
		ID:               uuid.Must(uuid.NewV7()),
		ScopeEventID:     in.ScopeEventID,
		NotificationType: in.NotificationType,
		Status:           notificationModel.TemplateStatusDraft,
		Subject:          in.Subject,
		Preheader:        in.Preheader,
		Body:             in.Body,
		Styling:          in.Styling,
		UpdatedByUserID:  new(in.UpdatedBy),
	}
}

func (t *EmailTemplate) IsDraft() bool {
	return t.Status == notificationModel.TemplateStatusDraft
}

func (t *EmailTemplate) IsPublished() bool {
	return t.Status == notificationModel.TemplateStatusPublished
}

func (t *EmailTemplate) IsUnpublished() bool {
	return t.Status == notificationModel.TemplateStatusUnpublished
}
