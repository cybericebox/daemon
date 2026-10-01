package emailUseCase

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model/notification/branding"
	"github.com/cybericebox/daemon/internal/model/notification/types"
)

// Brand is the resolved appearance and inline logo for one email. Preview and
// dispatch use the same value so the editor shows the email recipients receive.
type Brand struct {
	Colors          branding.Context
	Logo            []byte
	LogoContentType string
	// LogoAlt is the alt text of the logo (the event name for an event logo).
	LogoAlt string
}

func PlatformBrand() Brand {
	return Brand{Colors: branding.Platform(), Logo: branding.LogoPNG(), LogoContentType: branding.LogoContentType}
}

// EventBrandResolver is supplied by the Event application layer. The email
// channel owns rendering but does not read Event settings or media directly.
type EventBrandResolver interface {
	ResolveEventEmailBrand(ctx context.Context, eventID uuid.UUID) (Brand, error)
}

// resolveEmailBrand applies the event brand only to participant-facing
// event-scoped types; everything else (account flows, manager assignment,
// moderator messages) is sent in platform branding even when the dispatch
// carries an event scope.
func resolveEmailBrand(ctx context.Context, t notificationTypes.NotificationType, eventID *uuid.UUID, resolver EventBrandResolver) (Brand, error) {
	if eventID == nil || resolver == nil || !notificationTypes.IsEventScoped(t) {
		return PlatformBrand(), nil
	}
	return resolver.ResolveEventEmailBrand(ctx, *eventID)
}
