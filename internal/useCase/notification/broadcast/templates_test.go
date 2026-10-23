package broadcastUseCase_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

func templateInput() broadcastModel.TemplateInput {
	return broadcastModel.TemplateInput{Name: "  Reminder  ", Content: broadcastModel.Content{
		Channels: []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelInApp},
		// The email part of a channel that is not chosen is dropped, not stored.
		Subject: "left over", EmailBody: json.RawMessage(`[{"type":"paragraph"}]`),
		InAppTitle: "Hello {{.user_name}}", InAppBody: "<p>Body</p>",
	}}
}

func TestCreateBroadcastTemplate_StoresTheChosenChannelsInItsScope(t *testing.T) {
	h := setup(t)
	event := uuid.Must(uuid.NewV7())
	actor := uuid.Must(uuid.NewV7())
	var stored postgres.CreateNotificationBroadcastTemplateParams
	h.repo.EXPECT().CreateNotificationBroadcastTemplate(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateNotificationBroadcastTemplateParams) (uuid.UUID, error) {
			stored = arg
			return arg.ID, nil
		})
	h.repo.EXPECT().GetNotificationBroadcastTemplate(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.GetNotificationBroadcastTemplateParams) (postgres.GetNotificationBroadcastTemplateRow, error) {
			require.Equal(t, event.String(), arg.ScopeFilter)
			return postgres.GetNotificationBroadcastTemplateRow{ID: arg.ID, Name: stored.Name, Channels: stored.Channels, InappTitle: stored.InappTitle,
				ScopeEventID: uuid.NullUUID{UUID: event, Valid: true}, EmailBody: stored.EmailBody, EmailStyling: stored.EmailStyling}, nil
		})

	got, err := h.uc.CreateBroadcastTemplate(context.Background(), &event, actor, templateInput())
	require.NoError(t, err)
	require.Equal(t, "Reminder", stored.Name)
	require.Equal(t, uuid.NullUUID{UUID: event, Valid: true}, stored.ScopeEventID)
	require.Equal(t, uuid.NullUUID{UUID: actor, Valid: true}, stored.Actor)
	require.Equal(t, []string{"in_app"}, stored.Channels)
	require.Empty(t, stored.Subject)
	require.JSONEq(t, `[]`, string(stored.EmailBody))
	require.Equal(t, "Reminder", got.Name)
	require.Equal(t, event, *got.ScopeEventID)
}

func TestCreateBroadcastTemplate_RejectsInvalidContentBeforeStoring(t *testing.T) {
	h := setup(t)
	in := templateInput()
	in.Content.InAppTitle = ""
	_, err := h.uc.CreateBroadcastTemplate(context.Background(), nil, uuid.Must(uuid.NewV7()), in)
	require.True(t, notificationModel.ErrBroadcastInvalid.Err().Is(err), "err = %v", err)
}

func TestCreateBroadcastTemplate_DuplicateNameIsAConflict(t *testing.T) {
	h := setup(t)
	h.repo.EXPECT().CreateNotificationBroadcastTemplate(gomock.Any(), gomock.Any()).
		Return(uuid.Nil, &pgconn.PgError{Code: "23505", Detail: "Key (name)=(reminder) already exists."})
	_, err := h.uc.CreateBroadcastTemplate(context.Background(), nil, uuid.Must(uuid.NewV7()), templateInput())
	require.True(t, notificationModel.ErrBroadcastTemplateNameTaken.Err().Is(err), "err = %v", err)
}

func TestBroadcastTemplate_AnotherScopeIsNotFound(t *testing.T) {
	h := setup(t)
	event := uuid.Must(uuid.NewV7())
	id := uuid.Must(uuid.NewV7())
	h.repo.EXPECT().UpdateNotificationBroadcastTemplate(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpdateNotificationBroadcastTemplateParams) (int64, error) {
			require.Equal(t, event.String(), arg.ScopeFilter)
			return 0, nil
		})
	_, err := h.uc.UpdateBroadcastTemplate(context.Background(), &event, id, uuid.Must(uuid.NewV7()), templateInput())
	require.True(t, notificationModel.ErrBroadcastTemplateNotFound.Err().Is(err), "update err = %v", err)

	h.repo.EXPECT().DeleteNotificationBroadcastTemplate(gomock.Any(), postgres.DeleteNotificationBroadcastTemplateParams{ID: id, ScopeFilter: "platform"}).Return(int64(0), nil)
	err = h.uc.DeleteBroadcastTemplate(context.Background(), nil, id)
	require.True(t, notificationModel.ErrBroadcastTemplateNotFound.Err().Is(err), "delete err = %v", err)

	h.repo.EXPECT().GetNotificationBroadcastTemplate(gomock.Any(), gomock.Any()).Return(postgres.GetNotificationBroadcastTemplateRow{}, pgx.ErrNoRows)
	_, err = h.uc.GetBroadcastTemplate(context.Background(), &event, id)
	require.True(t, notificationModel.ErrBroadcastTemplateNotFound.Err().Is(err), "get err = %v", err)
}

func TestListBroadcastTemplates_PlatformScope(t *testing.T) {
	h := setup(t)
	h.repo.EXPECT().ListNotificationBroadcastTemplates(gomock.Any(), postgres.ListNotificationBroadcastTemplatesParams{ScopeFilter: "platform", LimitVal: broadcastModel.MaxTemplates}).
		Return([]postgres.ListNotificationBroadcastTemplatesRow{{ID: uuid.Must(uuid.NewV7()), Name: "A", Channels: []string{"email"}}}, nil)
	items, err := h.uc.ListBroadcastTemplates(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Nil(t, items[0].ScopeEventID)
}
