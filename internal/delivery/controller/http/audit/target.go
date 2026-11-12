// Package audit lets a handler attach the concrete object an administrative
// action addressed to the audit entry the protection middleware records.
package audit

import (
	"github.com/gin-gonic/gin"

	adminAuditUseCase "github.com/cybericebox/daemon/internal/useCase/adminAudit"
)

const targetKey = "adminAuditTarget"

// SetTarget records what the request acted on (ids only, never request body
// content). The middleware stores it next to the route template.
func SetTarget(ctx *gin.Context, target string) { ctx.Set(targetKey, target) }

// Target returns the value set by SetTarget, or "".
func Target(ctx *gin.Context) string { return ctx.GetString(targetKey) }

const changesKey = "adminAuditChanges"

// SetChanges records which fields the request changed (see adminAuditUseCase.Diff). Secret fields are cut out
// when the entry is written, so a handler may pass whole settings structs.
func SetChanges(
	ctx *gin.Context,
	changes []adminAuditUseCase.Change,
) {
	ctx.Set(changesKey, changes)
}

// Changes returns the value set by SetChanges, or nil.
func Changes(ctx *gin.Context) []adminAuditUseCase.Change {
	changes, _ := ctx.Get(changesKey)
	out, _ := changes.([]adminAuditUseCase.Change)
	return out
}
