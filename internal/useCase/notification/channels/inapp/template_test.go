package inAppUseCase_test

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
	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
	inAppUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/inapp"
	"github.com/cybericebox/daemon/pkg/tools"
)

func sampleInAppTemplate(notifType string, status notificationModel.TemplateStatus) postgres.NotificationInAppTemplate {
	return postgres.NotificationInAppTemplate{
		ID:               tools.NewUUIDv7(),
		NotificationType: notifType,
		Status:           string(status),
		Title:            "Test Title",
		Body:             "Test Body",
		Link:             "https://example.com",
		Icon:             "",
		Tone:             "",
		AccentColor:      "",
		Surface:          "",
		AutoDismissMs:    pgtype.Int4{},
		Actions:          []byte("[]"),
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
		PublishedAt:      pgtype.Timestamptz{},
		UpdatedByUserID:  uuid.NullUUID{},
	}
}

func TestCreateInAppTemplate_WithStructuredFields(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inAppUseCase.NewNotificationInAppTemplateUseCase(repo)
	ctx := context.Background()

	expected := sampleInAppTemplate("flag_accepted", notificationModel.TemplateStatusDraft)
	expected.Icon = "flag"
	expected.Tone = "success"

	repo.EXPECT().
		CreateInAppTemplate(gomock.Any(), gomock.AssignableToTypeOf(postgres.CreateInAppTemplateParams{})).
		DoAndReturn(
			func(_ context.Context, arg postgres.CreateInAppTemplateParams) (
				postgres.NotificationInAppTemplate,
				error,
			) {
				assert.NotEqual(t, uuid.Nil, arg.ID, "ID must be set")
				assert.Equal(t, string(notificationModel.TemplateStatusDraft), arg.Status)
				assert.Equal(t, "flag_accepted", arg.NotificationType)
				assert.Equal(t, "flag", arg.Icon)
				assert.Equal(t, "success", arg.Tone)
				assert.True(t, arg.UpdatedByUserID.Valid)
				return expected, nil
			},
		)

	updatedBy := tools.NewUUIDv7()
	got, err := uc.CreateInAppTemplate(
		ctx, inAppModel.CreateTemplateInput{
			NotificationType: "flag_accepted",
			Title:            "Test Title",
			Body:             "Test Body",
			Link:             "https://example.com",
			Icon:             "flag",
			Tone:             "success",
			Actions:          []byte("[]"),
			UpdatedBy:        updatedBy,
		},
	)
	require.NoError(t, err)
	assert.Equal(t, expected.ID, got.ID)
	assert.Equal(t, notificationModel.TemplateStatusDraft, got.Status)
}

func TestCreateInAppTemplate_RejectsUnsupportedChannel(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inAppUseCase.NewNotificationInAppTemplateUseCase(repo)

	_, err := uc.CreateInAppTemplate(context.Background(), inAppModel.CreateTemplateInput{
		NotificationType: "email_confirmation",
		Title:            "Confirm",
		Body:             "Open your email",
		Link:             "/",
		Actions:          []byte("[]"),
	})
	require.Error(t, err)
	assert.True(t, notificationModel.ErrInvalidTemplateVariables.Err().Is(err))
}

func TestUpdateInAppTemplate_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inAppUseCase.NewNotificationInAppTemplateUseCase(repo)
	ctx := context.Background()

	id := tools.NewUUIDv7()
	repo.EXPECT().GetInAppTemplate(gomock.Any(), id).
		Return(postgres.NotificationInAppTemplate{}, pgx.ErrNoRows)

	_, err := uc.UpdateInAppTemplate(
		ctx, inAppModel.UpdateTemplateInput{
			ID:      id,
			Title:   "New Title",
			Body:    "New Body",
			Link:    "https://example.com",
			Actions: []byte("[]"),
		},
	)
	require.Error(t, err)
	assert.True(t, notificationModel.ErrTemplateNotFound.Err().Is(err))
}

func TestUpdateInAppTemplate_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inAppUseCase.NewNotificationInAppTemplateUseCase(repo)
	ctx := context.Background()

	id := tools.NewUUIDv7()
	expected := sampleInAppTemplate("flag_accepted", notificationModel.TemplateStatusDraft)
	expected.ID = id
	expected.Title = "New Title"
	repo.EXPECT().GetInAppTemplate(gomock.Any(), id).Return(expected, nil)

	repo.EXPECT().
		UpdateInAppTemplate(gomock.Any(), gomock.AssignableToTypeOf(postgres.UpdateInAppTemplateParams{})).
		Return(expected, nil)

	got, err := uc.UpdateInAppTemplate(
		ctx, inAppModel.UpdateTemplateInput{
			ID:      id,
			Title:   "New Title",
			Body:    "New Body",
			Link:    "https://example.com",
			Actions: []byte("[]"),
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "New Title", got.Title)
}

func TestDeleteInAppTemplate_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inAppUseCase.NewNotificationInAppTemplateUseCase(repo)
	ctx := context.Background()

	id := tools.NewUUIDv7()
	repo.EXPECT().GetInAppTemplate(gomock.Any(), id).Return(platformInAppRow(id), nil)
	repo.EXPECT().DeleteInAppTemplate(gomock.Any(), id).Return(int64(1), nil)

	err := uc.DeleteInAppTemplate(ctx, id)
	require.NoError(t, err)
}

func TestDeleteInAppTemplate_NotDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inAppUseCase.NewNotificationInAppTemplateUseCase(repo)
	ctx := context.Background()

	id := tools.NewUUIDv7()
	// 0 rows affected means the template exists but is not in draft status (or doesn't exist)
	repo.EXPECT().GetInAppTemplate(gomock.Any(), id).Return(platformInAppRow(id), nil)
	repo.EXPECT().DeleteInAppTemplate(gomock.Any(), id).Return(int64(0), nil)

	err := uc.DeleteInAppTemplate(ctx, id)
	require.Error(t, err)
	assert.True(t, notificationModel.ErrTemplateNotFound.Err().Is(err))
}

func TestListInAppTemplates_MissingPublishedWarning(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inAppUseCase.NewNotificationInAppTemplateUseCase(repo)
	ctx := context.Background()

	templates := []postgres.NotificationInAppTemplate{
		sampleInAppTemplate("flag_accepted", notificationModel.TemplateStatusDraft),
		sampleInAppTemplate("team_invite", notificationModel.TemplateStatusPublished),
	}

	settings := []postgres.NotificationSetting{
		{NotificationType: "flag_accepted", Channel: "in_app", Enabled: true},
		{NotificationType: "team_invite", Channel: "in_app", Enabled: true},
	}

	repo.EXPECT().ListInAppTemplates(gomock.Any(), gomock.Any()).Return(templates, nil)
	repo.EXPECT().ListNotificationSettings(gomock.Any()).Return(settings, nil)

	result, err := uc.ListInAppTemplates(ctx, inAppModel.ListFilter{})
	require.NoError(t, err)
	assert.Len(t, result.Templates, 2)
	assert.Contains(t, result.MissingActiveFor, "flag_accepted")
	assert.NotContains(t, result.MissingActiveFor, "team_invite")
}

func TestPublishInAppTemplate_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inAppUseCase.NewNotificationInAppTemplateUseCase(repo)

	id := tools.NewUUIDv7()
	by := tools.NewUUIDv7()
	published := sampleInAppTemplate("flag_accepted", notificationModel.TemplateStatusPublished)
	published.ID = id
	repo.EXPECT().GetInAppTemplate(gomock.Any(), id).Return(published, nil)
	repo.EXPECT().
		PublishInAppTemplate(gomock.Any(), gomock.AssignableToTypeOf(postgres.PublishInAppTemplateParams{})).
		DoAndReturn(func(_ context.Context, arg postgres.PublishInAppTemplateParams) (postgres.NotificationInAppTemplate, error) {
			assert.Equal(t, id, arg.ID)
			assert.True(t, arg.UpdatedByUserID.Valid)
			assert.Equal(t, by, arg.UpdatedByUserID.UUID)
			return published, nil
		})

	got, err := uc.PublishInAppTemplate(context.Background(), id, by)
	require.NoError(t, err)
	assert.Equal(t, notificationModel.TemplateStatusPublished, got.Status)
}

func TestPublishInAppTemplate_NotDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inAppUseCase.NewNotificationInAppTemplateUseCase(repo)

	id := tools.NewUUIDv7()
	repo.EXPECT().GetInAppTemplate(gomock.Any(), id).Return(postgres.NotificationInAppTemplate{}, pgx.ErrNoRows)

	_, err := uc.PublishInAppTemplate(context.Background(), id, tools.NewUUIDv7())
	require.Error(t, err)
	assert.True(t, notificationModel.ErrTemplateNotFound.Err().Is(err))
}

func TestRollbackInAppTemplate_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inAppUseCase.NewNotificationInAppTemplateUseCase(repo)

	src := tools.NewUUIDv7()
	repo.EXPECT().GetInAppTemplate(gomock.Any(), src).Return(platformInAppRow(src), nil)
	repo.EXPECT().RollbackInAppTemplate(gomock.Any(), gomock.Any()).
		Return(postgres.NotificationInAppTemplate{}, pgx.ErrNoRows)

	_, err := uc.RollbackInAppTemplate(context.Background(), src, tools.NewUUIDv7())
	require.Error(t, err)
	assert.True(t, notificationModel.ErrTemplateNotFound.Err().Is(err))
}

func TestRollbackInAppTemplate_CopiesToDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inAppUseCase.NewNotificationInAppTemplateUseCase(repo)

	src := tools.NewUUIDv7()
	repo.EXPECT().GetInAppTemplate(gomock.Any(), src).Return(platformInAppRow(src), nil)
	draft := sampleInAppTemplate("flag_accepted", notificationModel.TemplateStatusDraft)
	repo.EXPECT().
		RollbackInAppTemplate(gomock.Any(), gomock.AssignableToTypeOf(postgres.RollbackInAppTemplateParams{})).
		DoAndReturn(func(_ context.Context, arg postgres.RollbackInAppTemplateParams) (postgres.NotificationInAppTemplate, error) {
			assert.NotEqual(t, uuid.Nil, arg.NewID)
			assert.Equal(t, src, arg.SourceID)
			return draft, nil
		})

	got, err := uc.RollbackInAppTemplate(context.Background(), src, tools.NewUUIDv7())
	require.NoError(t, err)
	assert.Equal(t, notificationModel.TemplateStatusDraft, got.Status)
}

func TestLatestInAppTemplates_FoldsByType(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inAppUseCase.NewNotificationInAppTemplateUseCase(repo)
	ctx := context.Background()

	// flag_accepted: draft, published, unpublished
	draftConfirm := sampleInAppTemplate("flag_accepted", notificationModel.TemplateStatusDraft)
	publishedConfirm := sampleInAppTemplate("flag_accepted", notificationModel.TemplateStatusPublished)
	unpublishedConfirm := sampleInAppTemplate("flag_accepted", notificationModel.TemplateStatusUnpublished)

	// team_invite: only published
	publishedInvite := sampleInAppTemplate("team_invite", notificationModel.TemplateStatusPublished)

	templates := []postgres.NotificationInAppTemplate{
		draftConfirm,
		publishedConfirm,
		unpublishedConfirm,
		publishedInvite,
	}

	repo.EXPECT().
		ListInAppTemplates(gomock.Any(), gomock.Any()).
		Return(templates, nil)

	got, err := uc.LatestInAppTemplates(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(got), 2)

	var flagEntry, inviteEntry *inAppModel.TypeVersions
	for i := range got {
		if got[i].NotificationType == "flag_accepted" {
			flagEntry = &got[i]
		} else if got[i].NotificationType == "team_invite" {
			inviteEntry = &got[i]
		}
	}

	require.NotNil(t, flagEntry, "flag_accepted entry not found")
	require.NotNil(t, inviteEntry, "team_invite entry not found")
	assert.True(t, containsInAppType(got, "participant.invitation.sent"), "registered type without a template must be listed")

	assert.NotNil(t, flagEntry.Draft)
	assert.NotNil(t, flagEntry.Published)
	assert.NotNil(t, flagEntry.Unpublished)
	assert.Equal(t, notificationModel.TemplateStatusDraft, flagEntry.Draft.Status)
	assert.Equal(t, notificationModel.TemplateStatusPublished, flagEntry.Published.Status)
	assert.Equal(t, notificationModel.TemplateStatusUnpublished, flagEntry.Unpublished.Status)

	assert.Nil(t, inviteEntry.Draft)
	assert.NotNil(t, inviteEntry.Published)
	assert.Nil(t, inviteEntry.Unpublished)
	assert.Equal(t, notificationModel.TemplateStatusPublished, inviteEntry.Published.Status)
}

func containsInAppType(entries []inAppModel.TypeVersions, kind string) bool {
	for _, entry := range entries {
		if entry.NotificationType == kind {
			return true
		}
	}
	return false
}

// platformInAppRow is a platform-family row with the given id, satisfying the
// by-id platform scope guard.
func platformInAppRow(id uuid.UUID) postgres.NotificationInAppTemplate {
	row := sampleInAppTemplate("flag_accepted", notificationModel.TemplateStatusDraft)
	row.ID = id
	return row
}
