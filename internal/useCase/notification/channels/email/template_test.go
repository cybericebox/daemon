package emailUseCase_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
	"github.com/cybericebox/daemon/pkg/tools"
)

func sampleEmailTemplate(notifType string, status notificationModel.TemplateStatus) postgres.NotificationEmailTemplate {
	return postgres.NotificationEmailTemplate{
		ID:               tools.NewUUIDv7(),
		NotificationType: notifType,
		Status:           string(status),
		Subject:          "Test Subject",
		Preheader:        "Test Preheader",
		Body:             []byte("[]"),
		Styling:          []byte("{}"),
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
		PublishedAt:      pgtype.Timestamptz{},
		UpdatedByUserID:  uuid.NullUUID{},
	}
}

func TestCreateEmailTemplate_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())
	ctx := context.Background()

	expected := sampleEmailTemplate("email_confirmation", notificationModel.TemplateStatusDraft)
	repo.EXPECT().
		CreateEmailTemplate(gomock.Any(), gomock.AssignableToTypeOf(postgres.CreateEmailTemplateParams{})).
		DoAndReturn(
			func(_ context.Context, arg postgres.CreateEmailTemplateParams) (
				postgres.NotificationEmailTemplate,
				error,
			) {
				assert.NotEqual(t, uuid.Nil, arg.ID, "ID must be set")
				assert.Equal(
					t,
					string(notificationModel.TemplateStatusDraft),
					arg.Status,
					"Status must default to draft",
				)
				assert.Equal(t, "email_confirmation", arg.NotificationType)
				return expected, nil
			},
		)

	got, err := uc.CreateEmailTemplate(
		ctx, emailModel.CreateTemplateInput{
			NotificationType: "email_confirmation",
			Subject:          "Test Subject",
			Preheader:        "Test Preheader",
			Body:             []byte("[]"),
			Styling:          []byte("{}"),
		},
	)
	require.NoError(t, err)
	assert.Equal(t, expected.ID, got.ID)
	assert.Equal(t, notificationModel.TemplateStatusDraft, got.Status)
}

func TestCreateEmailTemplate_RejectsUnavailableVariable(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())

	_, err := uc.CreateEmailTemplate(context.Background(), emailModel.CreateTemplateInput{
		NotificationType: "email_confirmation",
		Subject:          "Confirm {{.not_available}}",
		Body:             []byte("[]"),
		Styling:          []byte("{}"),
	})
	require.Error(t, err)
	assert.True(t, notificationModel.ErrInvalidTemplateVariables.Err().Is(err))
}

func TestUpdateEmailTemplate_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())
	ctx := context.Background()

	id := tools.NewUUIDv7()
	repo.EXPECT().GetEmailTemplate(gomock.Any(), id).
		Return(postgres.NotificationEmailTemplate{}, pgx.ErrNoRows)

	_, err := uc.UpdateEmailTemplate(
		ctx, emailModel.UpdateTemplateInput{
			ID:        id,
			Subject:   "New Subject",
			Preheader: "New Preheader",
			Body:      []byte("[]"),
			Styling:   []byte("{}"),
		},
	)
	require.Error(t, err)
	assert.True(t, notificationModel.ErrTemplateNotFound.Err().Is(err))
}

func TestUpdateEmailTemplate_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())
	ctx := context.Background()

	id := tools.NewUUIDv7()
	expected := sampleEmailTemplate("email_confirmation", notificationModel.TemplateStatusDraft)
	expected.ID = id
	expected.Subject = "New Subject"
	repo.EXPECT().GetEmailTemplate(gomock.Any(), id).Return(expected, nil)

	repo.EXPECT().
		UpdateEmailTemplate(gomock.Any(), gomock.AssignableToTypeOf(postgres.UpdateEmailTemplateParams{})).
		Return(expected, nil)

	got, err := uc.UpdateEmailTemplate(
		ctx, emailModel.UpdateTemplateInput{
			ID:        id,
			Subject:   "New Subject",
			Preheader: "New Preheader",
			Body:      []byte("[]"),
			Styling:   []byte("{}"),
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "New Subject", got.Subject)
}

func TestDeleteEmailTemplate_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())
	ctx := context.Background()

	id := tools.NewUUIDv7()
	repo.EXPECT().GetEmailTemplate(gomock.Any(), id).Return(platformEmailRow(id), nil)
	repo.EXPECT().DeleteEmailTemplate(gomock.Any(), id).Return(int64(1), nil)

	err := uc.DeleteEmailTemplate(ctx, id)
	require.NoError(t, err)
}

func TestDeleteEmailTemplate_NotDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())
	ctx := context.Background()

	id := tools.NewUUIDv7()
	// 0 rows affected means the template exists but is not in draft status (or doesn't exist)
	repo.EXPECT().GetEmailTemplate(gomock.Any(), id).Return(platformEmailRow(id), nil)
	repo.EXPECT().DeleteEmailTemplate(gomock.Any(), id).Return(int64(0), nil)

	err := uc.DeleteEmailTemplate(ctx, id)
	require.Error(t, err)
	assert.True(t, notificationModel.ErrTemplateNotFound.Err().Is(err))
}

func TestListEmailTemplates_MissingPublishedWarning(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())
	ctx := context.Background()

	// Two templates: one draft for email_confirmation, one published for team_invite.
	templates := []postgres.NotificationEmailTemplate{
		sampleEmailTemplate("email_confirmation", notificationModel.TemplateStatusDraft),
		sampleEmailTemplate("team_invite", notificationModel.TemplateStatusPublished),
	}

	// Settings: both types enabled for emailUseCase.
	settings := []postgres.NotificationSetting{
		{NotificationType: "email_confirmation", Channel: "email", Enabled: true},
		{NotificationType: "team_invite", Channel: "email", Enabled: true},
	}

	repo.EXPECT().ListEmailTemplates(gomock.Any(), gomock.Any()).Return(templates, nil)
	repo.EXPECT().ListNotificationSettings(gomock.Any()).Return(settings, nil)

	result, err := uc.ListEmailTemplates(ctx, emailModel.ListFilter{})
	require.NoError(t, err)
	assert.Len(t, result.Templates, 2)
	// email_confirmation has no published template → should be in MissingActiveFor
	assert.Contains(t, result.MissingActiveFor, "email_confirmation")
	// team_invite has a published template → should NOT be in MissingActiveFor
	assert.NotContains(t, result.MissingActiveFor, "team_invite")
}

func TestPublishEmailTemplate_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())

	id := tools.NewUUIDv7()
	by := tools.NewUUIDv7()
	published := sampleEmailTemplate("email_confirmation", notificationModel.TemplateStatusPublished)
	published.ID = id
	repo.EXPECT().GetEmailTemplate(gomock.Any(), id).Return(published, nil)
	repo.EXPECT().
		PublishEmailTemplate(gomock.Any(), gomock.AssignableToTypeOf(postgres.PublishEmailTemplateParams{})).
		DoAndReturn(func(_ context.Context, arg postgres.PublishEmailTemplateParams) (postgres.NotificationEmailTemplate, error) {
			assert.Equal(t, id, arg.ID)
			assert.True(t, arg.UpdatedByUserID.Valid)
			assert.Equal(t, by, arg.UpdatedByUserID.UUID)
			return published, nil
		})

	got, err := uc.PublishEmailTemplate(context.Background(), id, by)
	require.NoError(t, err)
	assert.Equal(t, notificationModel.TemplateStatusPublished, got.Status)
}

func TestPublishEmailTemplate_NotDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())

	id := tools.NewUUIDv7()
	repo.EXPECT().GetEmailTemplate(gomock.Any(), id).Return(postgres.NotificationEmailTemplate{}, pgx.ErrNoRows)

	_, err := uc.PublishEmailTemplate(context.Background(), id, tools.NewUUIDv7())
	require.Error(t, err)
	assert.True(t, notificationModel.ErrTemplateNotFound.Err().Is(err))
}

func TestLatestEmailTemplates_FoldsByType(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())
	ctx := context.Background()

	// email_confirmation: draft, published, unpublished
	draftConfirm := sampleEmailTemplate("email_confirmation", notificationModel.TemplateStatusDraft)
	publishedConfirm := sampleEmailTemplate("email_confirmation", notificationModel.TemplateStatusPublished)
	unpublishedConfirm := sampleEmailTemplate("email_confirmation", notificationModel.TemplateStatusUnpublished)

	// password_reset: only published
	publishedReset := sampleEmailTemplate("password_reset", notificationModel.TemplateStatusPublished)

	templates := []postgres.NotificationEmailTemplate{
		draftConfirm,
		publishedConfirm,
		unpublishedConfirm,
		publishedReset,
	}

	repo.EXPECT().
		ListEmailTemplates(gomock.Any(), gomock.Any()).
		Return(templates, nil)

	got, err := uc.LatestEmailTemplates(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(got), 2)

	// Find email_confirmation and password_reset entries
	var confirmEntry, resetEntry *emailModel.TypeVersions
	for i := range got {
		if got[i].NotificationType == "email_confirmation" {
			confirmEntry = &got[i]
		} else if got[i].NotificationType == "password_reset" {
			resetEntry = &got[i]
		}
	}

	require.NotNil(t, confirmEntry, "email_confirmation entry not found")
	require.NotNil(t, resetEntry, "password_reset entry not found")
	assert.True(t, containsEmailType(got, "participant.invitation.sent"), "registered type without a template must be listed")

	// Assert email_confirmation has all three versions
	assert.NotNil(t, confirmEntry.Draft)
	assert.NotNil(t, confirmEntry.Published)
	assert.NotNil(t, confirmEntry.Unpublished)
	assert.Equal(t, notificationModel.TemplateStatusDraft, confirmEntry.Draft.Status)
	assert.Equal(t, notificationModel.TemplateStatusPublished, confirmEntry.Published.Status)
	assert.Equal(t, notificationModel.TemplateStatusUnpublished, confirmEntry.Unpublished.Status)

	// Assert password_reset has only published
	assert.Nil(t, resetEntry.Draft)
	assert.NotNil(t, resetEntry.Published)
	assert.Nil(t, resetEntry.Unpublished)
	assert.Equal(t, notificationModel.TemplateStatusPublished, resetEntry.Published.Status)
}

func containsEmailType(entries []emailModel.TypeVersions, kind string) bool {
	for _, entry := range entries {
		if entry.NotificationType == kind {
			return true
		}
	}
	return false
}

func TestRollbackEmailTemplate_CopiesToDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())

	src := tools.NewUUIDv7()
	repo.EXPECT().GetEmailTemplate(gomock.Any(), src).Return(platformEmailRow(src), nil)
	draft := sampleEmailTemplate("email_confirmation", notificationModel.TemplateStatusDraft)
	repo.EXPECT().
		RollbackEmailTemplate(gomock.Any(), gomock.AssignableToTypeOf(postgres.RollbackEmailTemplateParams{})).
		DoAndReturn(func(_ context.Context, arg postgres.RollbackEmailTemplateParams) (postgres.NotificationEmailTemplate, error) {
			assert.NotEqual(t, uuid.Nil, arg.NewID)
			assert.Equal(t, src, arg.SourceID)
			return draft, nil
		})

	got, err := uc.RollbackEmailTemplate(context.Background(), src, tools.NewUUIDv7())
	require.NoError(t, err)
	assert.Equal(t, notificationModel.TemplateStatusDraft, got.Status)
}

// platformEmailRow is a platform-family row with the given id, satisfying the
// by-id platform scope guard.
func platformEmailRow(id uuid.UUID) postgres.NotificationEmailTemplate {
	row := sampleEmailTemplate("email_confirmation", notificationModel.TemplateStatusDraft)
	row.ID = id
	return row
}

func TestCreateEmailTemplate_RejectsUnknownThemeToken(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())

	_, err := uc.CreateEmailTemplate(context.Background(), emailModel.CreateTemplateInput{
		NotificationType: "email_confirmation",
		Subject:          "Confirm",
		Body:             []byte("[]"),
		Styling:          []byte(`{"cta_bg_color":"theme:nope"}`),
	})
	require.Error(t, err)
	assert.True(t, notificationModel.ErrInvalidTemplateStyling.Err().Is(err))
}
