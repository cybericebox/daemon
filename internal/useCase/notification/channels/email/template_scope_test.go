package emailUseCase_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
	"github.com/cybericebox/daemon/pkg/tools"
)

// Event-scoped rows belong to the Event API; the platform API must treat
// them as absent for every by-id operation (no write query may run).
func TestPlatformEmailTemplate_RejectsEventScopedRows(t *testing.T) {
	actor := tools.NewUUIDv7()
	calls := map[string]func(uc *emailUseCase.NotificationEmailTemplateUseCase, id uuid.UUID) error{
		"get": func(uc *emailUseCase.NotificationEmailTemplateUseCase, id uuid.UUID) error {
			_, err := uc.GetEmailTemplate(context.Background(), id)
			return err
		},
		"update": func(uc *emailUseCase.NotificationEmailTemplateUseCase, id uuid.UUID) error {
			_, err := uc.UpdateEmailTemplate(context.Background(), emailModel.UpdateTemplateInput{ID: id, Subject: "x", Body: []byte("[]"), Styling: []byte("{}")})
			return err
		},
		"delete": func(uc *emailUseCase.NotificationEmailTemplateUseCase, id uuid.UUID) error {
			return uc.DeleteEmailTemplate(context.Background(), id)
		},
		"publish": func(uc *emailUseCase.NotificationEmailTemplateUseCase, id uuid.UUID) error {
			_, err := uc.PublishEmailTemplate(context.Background(), id, actor)
			return err
		},
		"rollback": func(uc *emailUseCase.NotificationEmailTemplateUseCase, id uuid.UUID) error {
			_, err := uc.RollbackEmailTemplate(context.Background(), id, actor)
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := postgresMocks.NewMockQuerier(ctrl)
			uc := emailUseCase.NewNotificationEmailTemplateUseCase(repo, newFakeTemplateMedia())
			row := sampleEmailTemplate("participant.approval_registration.approved", notificationModel.TemplateStatusDraft)
			row.ScopeEventID = uuid.NullUUID{UUID: tools.NewUUIDv7(), Valid: true}
			repo.EXPECT().GetEmailTemplate(gomock.Any(), row.ID).Return(row, nil)

			err := call(uc, row.ID)
			require.Error(t, err)
			require.True(t, notificationModel.ErrTemplateNotFound.Err().Is(err))
		})
	}
}
