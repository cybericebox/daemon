package tools

import (
	"context"
	"fmt"
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/gofrs/uuid"
)

const (
	UserIDCtxKey    = "userID"
	UserRoleCtxKey  = "userRole"
	SubdomainCtxKey = "subdomain"
	EventIDCtxKey   = "eventID"
	ErrorCtxKey     = "error"
)

var (
	ErrNoUserIDInContext = appError.NewError().
				WithCode(appError.NewCode().
					WithMessage("no userID in context"))
	ErrNoEventIDInContext = appError.NewError().
				WithCode(appError.NewCode().
					WithMessage("no eventID in context"))
	ErrNoUserRoleInContext = appError.NewError().
				WithCode(appError.NewCode().
					WithMessage("no userRole in context"))
	ErrNoSubdomainInContext = appError.NewError().
				WithCode(appError.NewCode().
					WithMessage("no subdomain in context"))
)

func GetCurrentUserIDFromContext(ctx context.Context) (uuid.UUID, error) {
	userID := ctx.Value(UserIDCtxKey)

	if userID == nil {
		return uuid.Nil, ErrNoUserIDInContext
	}

	parsedID, ok := userID.(uuid.UUID)
	if !ok {
		return uuid.Nil, ErrNoUserIDInContext
	}

	return parsedID, nil
}

func GetCurrentUserRoleFromContext(ctx context.Context) (string, error) {
	userRole := ctx.Value(UserRoleCtxKey)

	if userRole == nil {
		return "", ErrNoUserRoleInContext
	}

	parsedRole, ok := userRole.(string)
	if !ok {
		return "", ErrNoUserRoleInContext
	}

	return parsedRole, nil
}

func GetSubdomainFromContext(ctx context.Context) (string, error) {
	subdomain := ctx.Value(SubdomainCtxKey)

	if subdomain == nil {
		return "", ErrNoSubdomainInContext
	}
	return subdomain.(string), nil
}

func GetEventIDFromContext(ctx context.Context) (uuid.UUID, error) {
	eventID := ctx.Value(EventIDCtxKey)

	parsedID, ok := eventID.(uuid.UUID)
	if !ok {
		return uuid.Nil, ErrNoEventIDInContext
	}

	return parsedID, nil
}

func GetErrorFromContext(ctx context.Context) appError.IError {
	errFromContext := ctx.Value(ErrorCtxKey)

	if errFromContext == nil {
		return nil
	}

	parsedError, ok := errFromContext.(appError.IError)
	if !ok {
		errCommonParsed, ok := errFromContext.(error)
		if !ok {
			return appError.NewError().
				WithCode(appError.NewCode().
					WithMessage(fmt.Sprintf("error in context is not of type error: got [%v]", errFromContext)))
		}
		return appError.NewError().WithError(errCommonParsed).
			WithCode(appError.NewCode().
				WithMessage(errCommonParsed.Error()))
	}

	return parsedError
}
