package model

import (
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"net/http"
	"time"
)

type (
	User struct {
		ID             uuid.UUID
		GoogleID       string
		Email          string
		Name           string
		Password       string
		HashedPassword string
		Picture        string
		Role           string
		LastSeen       time.Time
	}

	UserInfo struct {
		ID       uuid.UUID
		Name     string
		Picture  string
		Email    string
		Role     string
		LastSeen time.Time
	}
)

// errors for user

var (
	ErrInvalidUserID = tools.NewError("invalid user id", http.StatusBadRequest)
)

// constants for user

// User roles
const (
	UserRole          = "Користувач"
	AdministratorRole = "Адміністратор"
)
