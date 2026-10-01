package emailUseCase_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
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

// ── helpers ──────────────────────────────────────────────────────────────────

func sampleBlockPresetRow(name string) postgres.NotificationEmailBlockPreset {
	return postgres.NotificationEmailBlockPreset{
		ID:          tools.NewUUIDv7(),
		Name:        name,
		Description: "desc",
		Blocks:      []byte(`[{"type":"footer"}]`),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestPresetCreate_GeneratesID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailPresetUseCase(repo, newFakeTemplateMedia())

	repo.EXPECT().
		CreateEmailBlockPreset(gomock.Any(), gomock.AssignableToTypeOf(postgres.CreateEmailBlockPresetParams{})).
		DoAndReturn(func(_ context.Context, arg postgres.CreateEmailBlockPresetParams) (postgres.NotificationEmailBlockPreset, error) {
			assert.NotEqual(t, uuid.Nil, arg.ID, "ID must be non-nil")
			assert.Equal(t, "footer", arg.Name)
			assert.Equal(t, []byte(`[]`), arg.Blocks)
			return postgres.NotificationEmailBlockPreset{
				ID:     arg.ID,
				Name:   arg.Name,
				Blocks: arg.Blocks,
			}, nil
		})

	got, err := uc.CreateEmailBlockPreset(context.Background(), emailModel.PresetInput{
		Name:   "footer",
		Blocks: []byte(`[]`),
	})
	require.NoError(t, err)
	assert.Equal(t, "footer", got.Name)
	assert.NotEqual(t, uuid.Nil, got.ID)
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestPresetGet_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailPresetUseCase(repo, newFakeTemplateMedia())

	row := sampleBlockPresetRow("header")
	repo.EXPECT().GetEmailBlockPreset(gomock.Any(), row.ID).Return(row, nil)

	got, err := uc.GetEmailBlockPreset(context.Background(), row.ID)
	require.NoError(t, err)
	assert.Equal(t, row.ID, got.ID)
	assert.Equal(t, "header", got.Name)
}

func TestPresetGet_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailPresetUseCase(repo, newFakeTemplateMedia())

	repo.EXPECT().
		GetEmailBlockPreset(gomock.Any(), gomock.Any()).
		Return(postgres.NotificationEmailBlockPreset{}, pgx.ErrNoRows)

	_, err := uc.GetEmailBlockPreset(context.Background(), tools.NewUUIDv7())
	require.Error(t, err)
	assert.True(t, notificationModel.ErrPresetNotFound.Err().Is(err))
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestPresetList_ReturnsMappedSlice(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailPresetUseCase(repo, newFakeTemplateMedia())

	rows := []postgres.NotificationEmailBlockPreset{
		sampleBlockPresetRow("alpha"),
		sampleBlockPresetRow("beta"),
	}
	repo.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(rows, nil)

	got, err := uc.ListEmailBlockPresets(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "alpha", got[0].Name)
	assert.Equal(t, "beta", got[1].Name)
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestPresetUpdate_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailPresetUseCase(repo, newFakeTemplateMedia())

	id := tools.NewUUIDv7()
	row := sampleBlockPresetRow("updated-name")
	row.ID = id

	repo.EXPECT().
		UpdateEmailBlockPreset(gomock.Any(), gomock.AssignableToTypeOf(postgres.UpdateEmailBlockPresetParams{})).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateEmailBlockPresetParams) (postgres.NotificationEmailBlockPreset, error) {
			assert.Equal(t, id, arg.ID)
			assert.Equal(t, "updated-name", arg.Name)
			return row, nil
		})

	got, err := uc.UpdateEmailBlockPreset(context.Background(), id, emailModel.PresetInput{Name: "updated-name", Blocks: []byte(`[]`)})
	require.NoError(t, err)
	assert.Equal(t, "updated-name", got.Name)
}

func TestPresetUpdate_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailPresetUseCase(repo, newFakeTemplateMedia())

	repo.EXPECT().
		UpdateEmailBlockPreset(gomock.Any(), gomock.Any()).
		Return(postgres.NotificationEmailBlockPreset{}, pgx.ErrNoRows)

	_, err := uc.UpdateEmailBlockPreset(context.Background(), tools.NewUUIDv7(), emailModel.PresetInput{Name: "x", Blocks: []byte(`[]`)})
	require.Error(t, err)
	assert.True(t, notificationModel.ErrPresetNotFound.Err().Is(err))
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestPresetDelete_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailPresetUseCase(repo, newFakeTemplateMedia())

	id := tools.NewUUIDv7()
	repo.EXPECT().DeleteEmailBlockPreset(gomock.Any(), id).Return(nil)

	err := uc.DeleteEmailBlockPreset(context.Background(), id)
	require.NoError(t, err)
}

func TestPresetDelete_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := emailUseCase.NewNotificationEmailPresetUseCase(repo, newFakeTemplateMedia())

	repo.EXPECT().
		DeleteEmailBlockPreset(gomock.Any(), gomock.Any()).
		Return(pgx.ErrNoRows)

	err := uc.DeleteEmailBlockPreset(context.Background(), tools.NewUUIDv7())
	require.Error(t, err)
	assert.True(t, notificationModel.ErrPresetNotFound.Err().Is(err))
}
