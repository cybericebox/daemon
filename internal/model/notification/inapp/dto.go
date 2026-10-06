package inAppModel

import (
	"encoding/json"

	"github.com/gofrs/uuid"
)

type CreateTemplateInput struct {
	ScopeEventID     *uuid.UUID
	NotificationType string
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
	UpdatedBy        uuid.UUID
}

type UpdateTemplateInput struct {
	ID            uuid.UUID
	Title         string
	Body          string
	Link          string
	Icon          string
	Tone          string
	AccentColor   string
	Surface       string
	AutoDismissMs *int32
	Actions       json.RawMessage
	Dismissible   bool
	UpdatedBy     uuid.UUID
}

// ListFilter filters the ListInAppTemplates query.
type ListFilter struct {
	Type         string
	Status       string
	ScopeEventID *uuid.UUID
}

// ListResult is the return shape for ListInAppTemplates. MissingActiveFor contains
// notification types that are enabled for in-app but have no published template version.
type ListResult struct {
	Templates        []InAppTemplate
	MissingActiveFor []string
}

// TypeVersions holds the draft, published, and unpublished versions of templates
// for a single notification type.
type TypeVersions struct {
	NotificationType string
	Draft            *InAppTemplate
	Published        *InAppTemplate
	Unpublished      *InAppTemplate
}
