package emailUseCase_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/emailTemplateRepo"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	"github.com/cybericebox/daemon/internal/model/notification/branding"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

// fakeFooters records the route it was asked for and builds the real footer.
type fakeFooters struct {
	asked  []*uuid.UUID
	footer mailModel.Footer
}

func (f *fakeFooters) Footer(_ context.Context, eventID *uuid.UUID) (mailModel.Footer, error) {
	f.asked = append(f.asked, eventID)
	return f.footer, nil
}

// testFooter is a footer as the mail use case builds it: the preview only has to
// append it verbatim.
func testFooter(support string) mailModel.Footer {
	return mailModel.Footer{
		HTML: `<div>Cyber ICE Box <a href="mailto:` + support + `">` + support + `</a></div>`,
		Text: "—\nCyber ICE Box " + support,
	}
}

func TestPreviewEmail_EndsWithPlatformFooter(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	repo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil)
	footers := &fakeFooters{footer: testFooter("support@cybericebox.com")}
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())
	uc.SetFooterSource(footers)

	out, err := uc.PreviewEmail(context.Background(), emailUseCase.PreviewInput{
		NotificationType: string(previewType), Subject: "Hi", Body: json.RawMessage(`[{"type":"divider"}]`),
	})
	require.NoError(t, err)
	require.Contains(t, out.HTML, mailModel.KeepBrandHTML(footers.footer.HTML)+"</td>", "the footer sits in its slot below the card")
	require.NotContains(t, out.HTML, "cib-footer")
	require.Equal(t, []*uuid.UUID{nil}, footers.asked, "platform preview → platform footer")
}

func TestPreview_EventScopeAsksForEventFooter(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	repo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil)
	eventID := uuid.Must(uuid.NewV7())
	footers := &fakeFooters{footer: testFooter("org@uni.edu")}
	resolver := fixedEventBrand{brand: emailUseCase.Brand{Colors: branding.Context{Brand: "#123456", Accent: "#E0FFFF", OnAccent: "#000000"}}}

	out, err := emailUseCase.Preview(context.Background(), emailTemplateRepo.New(repo), nil, footers,
		&emailUseCase.EventPreviewScope{EventID: eventID, Brands: resolver},
		emailUseCase.PreviewInput{NotificationType: string(previewType), Subject: "Hi", Body: json.RawMessage(`[]`)})
	require.NoError(t, err)
	require.Contains(t, out.HTML, mailModel.KeepBrandHTML(footers.footer.HTML))
	require.Equal(t, []*uuid.UUID{&eventID}, footers.asked)
	require.Contains(t, out.HTML, `mailto:org@uni.edu`)
}
