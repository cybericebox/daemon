package http

import (
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
)

// newOriginPolicy is the allow-list of browser origins for CORS and the origin guard: the platform
// hosts, and event sites whose tag belongs to an existing event in any lifecycle state (archived
// included, so an archived event's site keeps reading its public pages and results), cached in
// memory. The event use case drops the cache when an event is created, deleted or retagged.
func newOriginPolicy(deps Dependencies) middleware.OriginPolicy {
	tags := middleware.NewEventTagCache(deps.UseCase.EventTagExists)
	deps.UseCase.SetTagListener(tags)
	return middleware.OriginPolicy{Hosts: deps.AuthConfig.Hosts, Tags: tags}
}
