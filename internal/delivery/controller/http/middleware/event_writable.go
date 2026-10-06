package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

// EventWriteGuard is implemented by the event use case: it refuses writes to an archived event.
type EventWriteGuard interface {
	RequireEventWritable(ctx context.Context, eventID uuid.UUID) error
}

// CheckEventWritable is the one archived-event guard of the manage routes: a read (GET, HEAD, OPTIONS) always
// passes, any other method needs an event that is not archived. A use case without the guard (a test double)
// leaves the route unguarded.
func CheckEventWritable(ctx *gin.Context, useCase any, eventID uuid.UUID) error {
	switch ctx.Request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}
	guard, ok := useCase.(EventWriteGuard)
	if !ok {
		return nil
	}
	return guard.RequireEventWritable(ctx, eventID)
}

// RequireEventWritable aborts the request with the guard's error; it reports whether the request may go on.
func RequireEventWritable(ctx *gin.Context, useCase any, eventID uuid.UUID) bool {
	if err := CheckEventWritable(ctx, useCase, eventID); err != nil {
		response.AbortWithError(ctx, err)
		return false
	}
	return true
}
