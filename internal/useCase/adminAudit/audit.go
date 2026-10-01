package adminAudit

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

type IRepository interface {
	CreateAdminAuditLog(context.Context, postgres.CreateAdminAuditLogParams) error
	ListAdminAuditLog(context.Context, postgres.ListAdminAuditLogParams) ([]postgres.AdminAuditLog, error)
}

type Entry struct {
	ActorID        uuid.UUID
	Permission     string
	Method         string
	Route          string
	ResponseStatus int
	// Target names the object the action addressed (route ids); empty when the
	// route template already says everything.
	Target string
}

type UseCase struct{ repo IRepository }

func New(repo IRepository) *UseCase { return &UseCase{repo: repo} }

// RecordAdminAction deliberately stores route templates and response metadata only:
// request bodies can contain passwords, tokens, challenge answers, and VPN material.
func (u *UseCase) RecordAdminAction(ctx context.Context, entry Entry) error {
	return u.repo.CreateAdminAuditLog(ctx, postgres.CreateAdminAuditLogParams{
		ID: uuid.Must(uuid.NewV7()), ActorID: entry.ActorID, Permission: entry.Permission,
		Method: entry.Method, Route: entry.Route, ResponseStatus: int32(entry.ResponseStatus), CreatedAt: time.Now(), Target: entry.Target,
	})
}

func (u *UseCase) ListAdminActions(ctx context.Context, actorID uuid.NullUUID, permission, route string, limit int32) ([]postgres.AdminAuditLog, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return u.repo.ListAdminAuditLog(ctx, postgres.ListAdminAuditLogParams{
		ActorID: actorID, Permission: pgtype.Text{String: permission, Valid: permission != ""},
		Route: pgtype.Text{String: route, Valid: route != ""}, LimitVal: limit,
	})
}
