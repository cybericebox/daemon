package emailUseCase_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
	"github.com/cybericebox/daemon/pkg/tools"
)

func TestCreateEmailTemplate_SetsImageReferences(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	m := newFakeTemplateMedia()
	file := m.addFile("image/png", 100)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, m)
	body := imageBody(file.String())

	row := sampleEmailTemplate("email_confirmation", notificationModel.TemplateStatusDraft)
	row.Body = body
	repo.EXPECT().CreateEmailTemplate(gomock.Any(), gomock.Any()).Return(row, nil)

	_, err := uc.CreateEmailTemplate(context.Background(), emailModel.CreateTemplateInput{
		NotificationType: "email_confirmation", Subject: "S", Body: body, Styling: []byte("{}"),
	})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{file}, m.replaced[row.ID])
}

func TestCreateEmailTemplate_RejectsUnknownImageFile(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl) // CreateEmailTemplate must NOT be called
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())

	for _, id := range []string{"not-a-uuid", uuid.Must(uuid.NewV7()).String()} {
		_, err := uc.CreateEmailTemplate(context.Background(), emailModel.CreateTemplateInput{
			NotificationType: "email_confirmation", Subject: "S", Body: imageBody(id), Styling: []byte("{}"),
		})
		assert.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "%s: %v", id, err)
	}
}

func TestUpdateEmailTemplate_ReplacesImageReferences(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	m := newFakeTemplateMedia()
	file := m.addFile("image/jpeg", 100)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, m)
	id := tools.NewUUIDv7()
	body := imageBody(file.String())

	repo.EXPECT().GetEmailTemplate(gomock.Any(), id).Return(platformEmailRow(id), nil)
	updated := platformEmailRow(id)
	updated.Body = body
	repo.EXPECT().UpdateEmailTemplate(gomock.Any(), gomock.Any()).Return(updated, nil)

	_, err := uc.UpdateEmailTemplate(context.Background(), emailModel.UpdateTemplateInput{
		ID: id, Subject: "S", Body: body, Styling: []byte("{}"),
	})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{file}, m.replaced[id])
}

func TestUpdateEmailTemplate_RejectsUnknownImageFile(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl) // UpdateEmailTemplate must NOT be called
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())
	id := tools.NewUUIDv7()
	repo.EXPECT().GetEmailTemplate(gomock.Any(), id).Return(platformEmailRow(id), nil)

	_, err := uc.UpdateEmailTemplate(context.Background(), emailModel.UpdateTemplateInput{
		ID: id, Subject: "S", Body: imageBody(uuid.Must(uuid.NewV7()).String()), Styling: []byte("{}"),
	})
	assert.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "%v", err)
}

func TestRollbackEmailTemplate_SetsDraftImageReferences(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	m := newFakeTemplateMedia()
	file := uuid.Must(uuid.NewV7())
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, m)
	src := tools.NewUUIDv7()

	repo.EXPECT().GetEmailTemplate(gomock.Any(), src).Return(platformEmailRow(src), nil)
	draft := sampleEmailTemplate("email_confirmation", notificationModel.TemplateStatusDraft)
	draft.Body = imageBody(file.String())
	repo.EXPECT().RollbackEmailTemplate(gomock.Any(), gomock.Any()).Return(draft, nil)

	_, err := uc.RollbackEmailTemplate(context.Background(), src, tools.NewUUIDv7())
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{file}, m.replaced[draft.ID])
}

func TestDeleteEmailTemplate_RemovesImageReferences(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	m := newFakeTemplateMedia()
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, m)
	id := tools.NewUUIDv7()

	repo.EXPECT().GetEmailTemplate(gomock.Any(), id).Return(platformEmailRow(id), nil)
	repo.EXPECT().DeleteEmailTemplate(gomock.Any(), id).Return(int64(1), nil)

	require.NoError(t, uc.DeleteEmailTemplate(context.Background(), id))
	assert.Equal(t, []uuid.UUID{id}, m.removed)
}

func TestPublishEmailTemplate_RejectsInlinePayloadOverLimit(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl) // PublishEmailTemplate must NOT be called
	m := newFakeTemplateMedia()
	big := m.addFile("image/png", emailUseCase.MaxInlineBytes)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, m)
	id := tools.NewUUIDv7()

	row := platformEmailRow(id)
	row.Body = []byte(`[{"type":"logo"},{"type":"image","file_id":"` + big.String() + `"}]`)
	repo.EXPECT().GetEmailTemplate(gomock.Any(), id).Return(row, nil)

	_, err := uc.PublishEmailTemplate(context.Background(), id, tools.NewUUIDv7())
	assert.True(t, notificationModel.ErrTemplateInlineTooLarge.Err().Is(err), "%v", err)
}

func TestPublishEmailTemplate_AllowsInlinePayloadWithinLimit(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	m := newFakeTemplateMedia()
	file := m.addFile("image/png", 200<<10)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, m)
	id := tools.NewUUIDv7()

	row := platformEmailRow(id)
	row.Body = []byte(`[{"type":"logo"},{"type":"image","file_id":"` + file.String() + `"}]`)
	repo.EXPECT().GetEmailTemplate(gomock.Any(), id).Return(row, nil)
	repo.EXPECT().PublishEmailTemplate(gomock.Any(), gomock.AssignableToTypeOf(postgres.PublishEmailTemplateParams{})).Return(row, nil)

	_, err := uc.PublishEmailTemplate(context.Background(), id, tools.NewUUIDv7())
	require.NoError(t, err)
}
