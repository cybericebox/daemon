package inAppUseCase_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
	inAppUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/inapp"
	"github.com/cybericebox/daemon/pkg/tools"
)

// Event-scoped rows belong to the Event API; the platform API must treat
// them as absent for every by-id operation (no write query may run).
func TestPlatformInAppTemplate_RejectsEventScopedRows(t *testing.T) {
	actor := tools.NewUUIDv7()
	calls := map[string]func(uc *inAppUseCase.NotificationInAppTemplateUseCase, id uuid.UUID) error{
		"get": func(uc *inAppUseCase.NotificationInAppTemplateUseCase, id uuid.UUID) error {
			_, err := uc.GetInAppTemplate(context.Background(), id)
			return err
		},
		"update": func(uc *inAppUseCase.NotificationInAppTemplateUseCase, id uuid.UUID) error {
			_, err := uc.UpdateInAppTemplate(context.Background(), inAppModel.UpdateTemplateInput{ID: id, Title: "x", Body: "y", Actions: []byte("[]")})
			return err
		},
		"delete": func(uc *inAppUseCase.NotificationInAppTemplateUseCase, id uuid.UUID) error {
			return uc.DeleteInAppTemplate(context.Background(), id)
		},
		"publish": func(uc *inAppUseCase.NotificationInAppTemplateUseCase, id uuid.UUID) error {
			_, err := uc.PublishInAppTemplate(context.Background(), id, actor)
			return err
		},
		"rollback": func(uc *inAppUseCase.NotificationInAppTemplateUseCase, id uuid.UUID) error {
			_, err := uc.RollbackInAppTemplate(context.Background(), id, actor)
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := postgresMocks.NewMockQuerier(ctrl)
			uc := inAppUseCase.NewNotificationInAppTemplateUseCase(repo)
			row := sampleInAppTemplate("participant.approval_registration.approved", notificationModel.TemplateStatusDraft)
			row.ScopeEventID = uuid.NullUUID{UUID: tools.NewUUIDv7(), Valid: true}
			repo.EXPECT().GetInAppTemplate(gomock.Any(), row.ID).Return(row, nil)

			err := call(uc, row.ID)
			require.Error(t, err)
			require.True(t, notificationModel.ErrTemplateNotFound.Err().Is(err))
		})
	}
}
