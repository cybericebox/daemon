package tools

import (
	"context"
	"fmt"

	"github.com/cybericebox/lib/pkg/err"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
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
		return uuid.Nil, model.ErrUserNotFoundInContext.Err()
	}

	parsedID, ok := userID.(uuid.UUID)
	if !ok {
		return uuid.Nil, model.ErrUserNotFoundInContext.Err()
	}

	return parsedID, nil
}

func SetCurrentUserIDToContext(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, UserIDCtxKey, userID)
}

func GetCurrentUserRoleFromContext(ctx context.Context) (string, error) {
	userRole := ctx.Value(UserRoleCtxKey)

	if userRole == nil {
		return "", model.ErrUserRoleNotFoundInContext.Err()
	}

	parsedRole, ok := userRole.(string)
	if !ok {
		return "", model.ErrUserRoleNotFoundInContext.Err()
	}

	return parsedRole, nil
}

func GetSubdomainFromContext(ctx context.Context) (string, error) {
	subdomain := ctx.Value(SubdomainCtxKey)

	if subdomain == nil {
		return "", model.ErrSubdomainNotFoundInContext.Err()
	}
	return subdomain.(string), nil
}

func GetErrorFromContext(ctx context.Context) err.Error {
	errFromContext := ctx.Value(ErrorCtxKey)

	if errFromContext == nil {
		return nil
	}

	parsedError, ok := errFromContext.(err.Error)
	if !ok {
		errCommonParsed, ok := errFromContext.(error)
		if !ok {
			return model.ErrPlatform.WithMessage(
				fmt.Sprintf(
					"Error in context is not of type error: got [%v]",
					errFromContext,
				),
			).Err()
		}
		return model.ErrPlatform.WithError(errCommonParsed).WithMessage(errCommonParsed.Error()).Err()
	}

	return parsedError
}
