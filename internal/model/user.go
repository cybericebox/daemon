package model

import (
	"github.com/cybericebox/daemon/internal/appError"
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
		UpdatedBy      uuid.UUID
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
		UpdatedBy     uuid.UUID
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
	ErrUser             = appError.ErrInternal.WithObjectCode(userObjectCode)
	ErrUserUserNotFound = appError.ErrObjectNotFound.WithObjectCode(userObjectCode).WithMessage("User not found")
	ErrUserUserExists   = appError.ErrInvalidData.WithObjectCode(userObjectCode).WithMessage("User already exists")
)

// constants for user

// User roles
const (
	UserRole          = "Користувач"
	AdministratorRole = "Адміністратор"
)
