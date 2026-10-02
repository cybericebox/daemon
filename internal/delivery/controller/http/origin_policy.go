package http

import (
	"context"
	"errors"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

// newOriginPolicy is the allow-list of browser origins for CORS and the origin guard: the platform
// hosts, and event sites whose tag belongs to an existing event (the same lookup the event tenant
// resolution uses), cached in memory.
func newOriginPolicy(deps Dependencies) middleware.OriginPolicy {
	return middleware.OriginPolicy{
		Hosts: deps.AuthConfig.Hosts,
		Tags: middleware.NewEventTagCache(func(ctx context.Context, tag string) (bool, error) {
			_, err := deps.UseCase.ResolveEventByTag(ctx, tag, time.Now())
			if err == nil {
				return true, nil
			}
			if errors.Is(err, eventModel.ErrEventNotFound.Err()) {
				return false, nil
			}
			return false, err
		}),
	}
}
