package broadcastModel

import (
	"strings"
	"time"

	"github.com/gofrs/uuid"

	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
)

// MaxTemplates bounds the templates one scope lists (the picker shows them all).
const MaxTemplates = 200

// TemplateInput is a broadcast template as authored: a name and the message,
// exactly what the composer sends minus the audience, which is chosen when
// sending.
type TemplateInput struct {
	Name    string
	Content Content
}

// Normalized trims the name.
func (in TemplateInput) Normalized() TemplateInput {
	in.Name = strings.TrimSpace(in.Name)
	return in
}

// Validate checks the name and the content by the rules of a sent broadcast,
// so a template always fills a sendable message.
func (in TemplateInput) Validate() error {
	name := strings.TrimSpace(in.Name)
	if name == "" || len([]rune(name)) > MaxTextLen {
		return notificationModel.ErrBroadcastInvalid.WithMessage("Template name is required").Err()
	}
	return in.Content.Validate()
}

// Template is a stored broadcast template. ScopeEventID nil is the platform.
type Template struct {
	ID            uuid.UUID
	ScopeEventID  *uuid.UUID
	Name          string
	Content       Content
	CreatedBy     *uuid.UUID
	UpdatedBy     *uuid.UUID
	UpdatedByName string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ScopeFilter is the repository scope of a template: "platform" or the Event id.
func ScopeFilter(scopeEventID *uuid.UUID) string {
	if scopeEventID == nil {
		return "platform"
	}
	return scopeEventID.String()
}
