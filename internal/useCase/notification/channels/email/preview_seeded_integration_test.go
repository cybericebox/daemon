package emailUseCase_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	_ "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	"github.com/cybericebox/daemon/internal/testhelpers"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

// The seeded inactive-account warning (migration 0089) opens in the admin
// editor and previews like any platform template: variables resolve from the
// payload defaults and the sign-in button carries the link.
func TestPreviewEmail_SeededAccountInactivityWarning(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	tpl, err := db.Queries.GetPublishedEmailTemplate(ctx, postgres.GetPublishedEmailTemplateParams{NotificationType: "account_inactivity_warning"})
	require.NoError(t, err)

	out, err := emailUseCase.NewNotificationEmailTemplateUseCase(db.Queries, newFakeTemplateMedia()).PreviewEmail(ctx, emailUseCase.PreviewInput{
		NotificationType: tpl.NotificationType,
		Subject:          tpl.Subject,
		Preheader:        tpl.Preheader,
		Body:             tpl.Body,
		Styling:          tpl.Styling,
	})
	require.NoError(t, err)
	require.Equal(t, "Ваш обліковий запис Cyber ICE Box буде видалено", out.Subject)
	require.Contains(t, out.HTML, "John Doe")
	require.Contains(t, out.HTML, "29.10.2026")
	require.Contains(t, out.HTML, `href="https://id.example.org/sign-in"`)
	require.False(t, strings.Contains(out.HTML, "{{"), "no unresolved variable: %s", out.HTML)
}
