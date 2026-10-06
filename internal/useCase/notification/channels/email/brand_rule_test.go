package emailUseCase

import (
	"context"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	_ "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
)

type recordingBrands struct{ called bool }

func (r *recordingBrands) ResolveEventEmailBrand(context.Context, uuid.UUID) (Brand, error) {
	r.called = true
	return Brand{Logo: []byte("event")}, nil
}

// TestBrandRule_OnlyParticipantTypesGetEventBranding walks every registered
// notification type: event branding (and the event sender) belongs to the
// participant-facing event-scoped types alone; account flows and everything
// sent to moderators, managers or the platform stay platform-branded even
// when the dispatch carries an event.
func TestBrandRule_OnlyParticipantTypesGetEventBranding(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	moderatorOrPlatform := []notificationTypes.NotificationType{
		notificationTypes.NotificationType(signalModel.TypeEventManagerAssigned),
		notificationTypes.NotificationType(signalModel.TypeEventLabFailed),
		notificationTypes.NotificationTypeEmailConfirmation,
	}
	for _, typ := range moderatorOrPlatform {
		require.False(t, notificationTypes.IsEventScoped(typ), "%s must not be in event-scoped lists", typ)
	}
	for _, info := range notificationTypes.Types() {
		scoped := notificationTypes.IsEventScoped(info.Type)
		if scoped {
			require.True(t, strings.HasPrefix(string(info.Type), "participant."), "%s is event-scoped but not participant-facing", info.Type)
		}
		resolver := &recordingBrands{}
		brand, err := resolveEmailBrand(context.Background(), info.Type, &eventID, resolver)
		require.NoError(t, err)
		require.Equal(t, scoped, resolver.called, info.Type)
		if !scoped {
			require.Equal(t, PlatformBrand().Logo, brand.Logo, "%s is platform-branded", info.Type)
		}
	}
}
