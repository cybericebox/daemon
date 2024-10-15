package tools

import (
	"context"
	"fmt"
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
)

const (
	UserIDCtxKey    = "userID"
	UserRoleCtxKey  = "userRole"
	SubdomainCtxKey = "subdomain"
	EventIDCtxKey   = "eventID"
	ErrorCtxKey     = "error"
)

func GetCurrentUserIDFromContext(ctx context.Context) (uuid.UUID, error) {
	userID := ctx.Value(UserIDCtxKey)

	if userID == nil {
		return uuid.Nil, model.ErrPlatformUserNotFoundInContext.Cause()
	}

	parsedID, ok := userID.(uuid.UUID)
	if !ok {
		return uuid.Nil, model.ErrPlatformUserNotFoundInContext.Cause()
	}

	return parsedID, nil
}

func GetCurrentUserRoleFromContext(ctx context.Context) (string, error) {
	userRole := ctx.Value(UserRoleCtxKey)

	if userRole == nil {
		return "", model.ErrPlatformUserRoleNotFoundInContext.Cause()
	}

	parsedRole, ok := userRole.(string)
	if !ok {
		return "", model.ErrPlatformUserRoleNotFoundInContext.Cause()
	}

	return parsedRole, nil
}

func GetSubdomainFromContext(ctx context.Context) (string, error) {
	subdomain := ctx.Value(SubdomainCtxKey)

	if subdomain == nil {
		return "", model.ErrPlatformSubdomainNotFoundInContext.Cause()
	}
	return subdomain.(string), nil
}

func GetErrorFromContext(ctx context.Context) appError.Error {
	errFromContext := ctx.Value(ErrorCtxKey)

	if errFromContext == nil {
		return nil
	}

	parsedError, ok := errFromContext.(appError.Error)
	if !ok {
		errCommonParsed, ok := errFromContext.(error)
		if !ok {
			return model.ErrPlatform.WithMessage(fmt.Sprintf("Error in context is not of type error: got [%v]", errFromContext)).Cause()
		}
		return model.ErrPlatform.WithError(errCommonParsed).WithMessage(errCommonParsed.Error()).Cause()
	}

	return parsedError
}
