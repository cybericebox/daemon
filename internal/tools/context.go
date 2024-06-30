package tools

import (
	"context"
	"errors"
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
	ErrNoUserIDInContext    = NewError("no userID in context")
	ErrNoUserRoleInContext  = NewError("no user role in context")
	ErrNoSubdomainInContext = NewError("no subdomain in context")
)

func GetCurrentUserIDFromContext(ctx context.Context) (uuid.UUID, error) {
	userID := ctx.Value(UserIDCtxKey)

	if userID == nil {
		return uuid.Nil, ErrNoUserIDInContext
	}

	parsedID, ok := userID.(*uuid.UUID)
	if !ok {
		return uuid.Nil, ErrNoUserIDInContext
	}

	return *parsedID, nil
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
	eventIDStr := ctx.Value(EventIDCtxKey)

	eventID := uuid.FromStringOrNil(eventIDStr.(string))

	if eventID == uuid.Nil {
		return uuid.Nil, ErrNoUserIDInContext
	}
	return eventID, nil
}

func GetErrorFromContext(ctx context.Context) *CError {
	errFromContext := ctx.Value(ErrorCtxKey)

	if errFromContext == nil {
		return nil
	}

	errParsed, ok := errFromContext.(*CError)
	if !ok {
		return NewError("error from context is not of type CError")
	}

	var err *CError
	if ok = errors.As(errParsed, &err); !ok {
		return NewError("error from context is not of type CError")
	}

	return err
}
