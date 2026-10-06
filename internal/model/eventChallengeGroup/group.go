// Package eventChallengeGroup owns named, event-local board sections.
package eventChallengeGroup

import (
	"strings"
	"time"

	"github.com/gofrs/uuid"
)

const maxNameLen = 80

type Group struct {
	ID        uuid.UUID
	EventID   uuid.UUID
	Name      string
	Order     int32
	CreatedAt time.Time
}

func New(eventID uuid.UUID, name string, order int32, now time.Time) (Group, error) {
	if eventID == uuid.Nil {
		return Group{}, ErrChallengeGroupInvalid.Err()
	}
	if err := validateUpdate(name, order); err != nil {
		return Group{}, err
	}
	name = strings.TrimSpace(name)
	return Group{ID: uuid.Must(uuid.NewV7()), EventID: eventID, Name: name, Order: order, CreatedAt: now}, nil
}

// Update changes the mutable presentation fields of an event-local group.
// Group membership stays on the event challenge relation, so renaming or
// moving a group never rewrites the board itself.
func (g *Group) Update(name string, order int32) error {
	if err := validateUpdate(name, order); err != nil {
		return err
	}
	g.Name = strings.TrimSpace(name)
	g.Order = order
	return nil
}

func validateUpdate(name string, order int32) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxNameLen || order < 0 {
		return ErrChallengeGroupInvalid.Err()
	}
	return nil
}
