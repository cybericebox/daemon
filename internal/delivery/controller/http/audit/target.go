// Package audit lets a handler attach the concrete object an administrative
// action addressed to the audit entry the protection middleware records.
package audit

import "github.com/gin-gonic/gin"

const targetKey = "adminAuditTarget"

// SetTarget records what the request acted on (ids only, never request body
// content). The middleware stores it next to the route template.
func SetTarget(ctx *gin.Context, target string) { ctx.Set(targetKey, target) }

// Target returns the value set by SetTarget, or "".
func Target(ctx *gin.Context) string { return ctx.GetString(targetKey) }
