package middleware

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	authModel "github.com/cybericebox/daemon/internal/model/auth"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// SeesEventPersonalData tells whether the caller sees the participants' personal data of the event in full (email
// addresses, the SMTP username): the owner and write moderators (requireManage passes) and platform staff,
// admin_viewer included. A read-only moderator gets them masked or hidden (owner decision 2026-10-19).
func SeesEventPersonalData(
	ctx *gin.Context, eventID uuid.UUID, requireManage func(context.Context, uuid.UUID, uuid.UUID) error,
) (bool, error) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		return false, nil
	}
	if claims.Role.HasPermission(rbac.PermEventsRead) {
		return true, nil
	}
	err := requireManage(ctx.Request.Context(), eventID, claims.UserID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, eventManagerModel.ErrEventManagementForbidden.Err()) || errors.Is(err, authModel.ErrInsufficientPermission.Err()) {
		return false, nil
	}
	return false, err
}
