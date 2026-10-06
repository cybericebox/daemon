package emailModel

import (
	"encoding/json"

	"github.com/gofrs/uuid"
)

type CreateTemplateInput struct {
	ScopeEventID     *uuid.UUID
	NotificationType string
	Subject          string
	Preheader        string
	Body             json.RawMessage
	Styling          json.RawMessage
	UpdatedBy        uuid.UUID
}

type UpdateTemplateInput struct {
	ID        uuid.UUID
	Subject   string
	Preheader string
	Body      json.RawMessage
	Styling   json.RawMessage
	UpdatedBy uuid.UUID
}

// ListFilter filters the ListEmailTemplates query.
type ListFilter struct {
	Type         string
	Status       string
	ScopeEventID *uuid.UUID
}

// ListResult is the return shape for ListEmailTemplates. MissingActiveFor contains
// notification types that are L2-enabled for email but have no published template version.
type ListResult struct {
	Templates        []EmailTemplate
	MissingActiveFor []string
}

// TypeVersions holds the draft, published, and unpublished versions of templates
// for a single notification type.
type TypeVersions struct {
	NotificationType string
	Draft            *EmailTemplate
	Published        *EmailTemplate
	Unpublished      *EmailTemplate
}
