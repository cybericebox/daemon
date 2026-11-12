package tools

import (
	"github.com/gofrs/uuid"
)

func NewUUIDv7() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}
