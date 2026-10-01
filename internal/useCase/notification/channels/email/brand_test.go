package emailUseCase_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/emailTemplateRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/model/notification/branding"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

type fixedEventBrand struct{ brand emailUseCase.Brand }

func (f fixedEventBrand) ResolveEventEmailBrand(context.Context, uuid.UUID) (emailUseCase.Brand, error) {
	return f.brand, nil
}

func TestEventBrand_PreviewMatchesDispatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	eventID := uuid.Must(uuid.NewV7())
	resolver := fixedEventBrand{brand: emailUseCase.Brand{
		Colors: branding.Context{Brand: "#123456", Accent: "#E0FFFF", OnAccent: "#000000"},
		Logo:   []byte("event-logo"), LogoContentType: "image/png",
	}}
	body := json.RawMessage(`[{"type":"logo"},{"type":"button","label":"Open","url":"https://example.org"}]`)
	styling := json.RawMessage(`{"cta_bg_color":"theme:accent","cta_text_color":"theme:on_accent"}`)

	dispatchRepo := postgresMocks.NewMockQuerier(ctrl)
	dispatchRepo.EXPECT().GetPublishedEmailTemplate(gomock.Any(), gomock.Any()).Return(postgres.NotificationEmailTemplate{
		NotificationType: string(previewType), Subject: "Hi", Body: body, Styling: styling,
	}, nil)
	dispatchRepo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil)
	mailer := &fakeMailer{}
	err := emailUseCase.NewHandler(dispatchRepo, mailer, &fakeMedia{}, resolver).Handle(
		context.Background(), userModel.User{Email: "a@example.org"}, previewType, nil, nil, &eventID)
	require.NoError(t, err)
	require.Len(t, mailer.last.Inline, 1)
	require.Equal(t, []byte("event-logo"), mailer.last.Inline[0].Data)
	require.Contains(t, mailer.last.HTML, "#E0FFFF")
	require.Contains(t, mailer.last.HTML, "#000000")
	require.Equal(t, &eventID, mailer.lastEvent, "participant mail is sent as the Event")

	previewRepo := postgresMocks.NewMockQuerier(ctrl)
	previewRepo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil)
	preview, err := emailUseCase.Preview(context.Background(), emailTemplateRepo.New(previewRepo), nil, nil,
		&emailUseCase.EventPreviewScope{EventID: eventID, Brands: resolver},
		emailUseCase.PreviewInput{NotificationType: string(previewType), Subject: "Hi", Body: body, Styling: styling})
	require.NoError(t, err)
	require.Equal(t, strings.ReplaceAll(mailer.last.HTML, "cid:logo@cybericebox", dataURI("image/png", []byte("event-logo"))), preview.HTML)
}

func TestEventBrand_NotAppliedToNonParticipantTypes(t *testing.T) {
	ctrl := gomock.NewController(t)
	eventID := uuid.Must(uuid.NewV7())
	resolver := fixedEventBrand{brand: emailUseCase.Brand{
		Colors: branding.Context{Brand: "#123456", Accent: "#E0FFFF", OnAccent: "#000000"},
		Logo:   []byte("event-logo"), LogoContentType: "image/png",
	}}
	managerType := notificationTypes.NotificationType(signalModel.TypeEventManagerAssigned)
	body := json.RawMessage(`[{"type":"logo"},{"type":"button","label":"Open","url":"https://example.org"}]`)
	styling := json.RawMessage(`{"cta_bg_color":"theme:accent","cta_text_color":"theme:on_accent"}`)

	repo := postgresMocks.NewMockQuerier(ctrl)
	repo.EXPECT().GetPublishedEmailTemplate(gomock.Any(), gomock.Any()).Return(postgres.NotificationEmailTemplate{
		NotificationType: string(managerType), Subject: "Hi", Body: body, Styling: styling,
	}, nil)
	repo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil)
	mailer := &fakeMailer{}
	err := emailUseCase.NewHandler(repo, mailer, &fakeMedia{}, resolver).Handle(
		context.Background(), userModel.User{Email: "m@example.org"}, managerType, nil, nil, &eventID)
	require.NoError(t, err)
	require.Len(t, mailer.last.Inline, 1)
	require.Equal(t, branding.LogoPNG(), mailer.last.Inline[0].Data)
	require.NotContains(t, mailer.last.HTML, "#E0FFFF")
	require.Nil(t, mailer.lastEvent, "moderator mail goes out as the platform")
}
