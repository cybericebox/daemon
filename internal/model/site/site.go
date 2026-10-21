// Package siteModel names the frontend a request comes from: one of the platform sites or an event site.
package siteModel

import (
	"github.com/gofrs/uuid"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
)

// Site kinds, as the clients name them in the site query parameter.
const (
	Main      = "main"
	ID        = "id"
	Admin     = "admin"
	Exercises = "exercises"
	Event     = "event"
)

// Site is the requesting frontend. EventID is set for the event kind only.
type Site struct {
	Kind    string
	EventID *uuid.UUID
}

// ValidKind reports whether kind is a site kind.
func ValidKind(kind string) bool {
	switch kind {
	case Main, ID, Admin, Exercises, Event:
		return true
	}
	return false
}

// InboxScope is the inbox scope of the site: an event site shows that event's items plus the
// platform ones, every other site only the items without an event.
func (s Site) InboxScope() *uuid.UUID {
	if s.EventID != nil {
		return s.EventID
	}
	return new(inboxModel.PlatformScope)
}
