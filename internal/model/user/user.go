package userModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/lib/pkg/err"
	"github.com/gofrs/uuid"
	"time"
)

type (
	User struct {
		ID             uuid.UUID `binding:"omitempty,uuid"`
		GoogleID       string    `binding:"omitempty"`
		Email          string    `binding:"omitempty,email"`
		Name           string    `binding:"omitempty,max=255,min=3"`
		Password       string    `binding:"omitempty,max=255,min=8"`
		HashedPassword string
		Picture        string `binding:"omitempty,uuid|url"`
		Role           string `binding:"omitempty,oneof=Користувач Адміністратор"`
		LastSeen       time.Time
		UpdatedAt      time.Time
		UpdatedBy      uuid.NullUUID
		CreatedAt      time.Time
	}

	UserInfo struct {
		ID            uuid.UUID
		ConnectGoogle bool
		Name          string
		Picture       string
		Email         string
		Role          string
		LastSeen      time.Time
		UpdatedAt     time.Time
		UpdatedBy     uuid.NullUUID
		CreatedAt     time.Time
	}

	// InviteUsers is a struct that contains users emails to invite
	InviteUsers struct {
		Role   string   `binding:"required,oneof=Користувач Адміністратор"`
		Emails []string `binding:"required,min=1,dive,email"`
	}
)

// errors for user
var (
	ErrUser = err.ErrInternal.WithObjectCode(model.UserObjectCode)

	ErrUserUserNotFound = err.ErrObjectNotFound.WithObjectCode(model.UserObjectCode).WithMessage("User not found").WithDetailCode(1) // 30901

	ErrUserUserExists = err.ErrInvalidData.WithObjectCode(model.UserObjectCode).WithMessage("User already exists").WithDetailCode(1) // 20901

	ErrUserUserDataStale = err.ErrConflict.WithObjectCode(model.UserObjectCode).WithMessage("User data is stale").WithDetailCode(1) // 70901
)

// constants for user

// User roles
const (
	UserRole          = "Користувач"
	AdministratorRole = "Адміністратор"
)
