package user

import (
	"github.com/gofrs/uuid"
)

type (
	User struct {
		ID       uuid.UUID
		GoogleID string
		Email    string
		Name     string
		Picture  string
		Password string
	}
)
