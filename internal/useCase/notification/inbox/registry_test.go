package inboxUseCase_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
	"github.com/cybericebox/daemon/pkg/tools"
)

var registryAt = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func registryRouter(t *testing.T) (*inboxUseCase.RequestRouter, *postgresMocks.MockQuerier, *fakeNotifier) {
	repo := postgresMocks.NewMockQuerier(gomock.NewController(t))
	notifier := &fakeNotifier{}
	repo.EXPECT().GetUserByID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID) (postgres.User, error) {
		return postgres.User{ID: id, Email: "x@example.org"}, nil
	}).AnyTimes()
	return inboxUseCase.NewRequestRouter(repo, notifier), repo, notifier
}

func TestRegistryCredentialBrokenPlatformTokenGoesToEveryPlatformAdmin(t *testing.T) {
	router, repo, notifier := registryRouter(t)
	admin1, admin2 := tools.NewUUIDv7(), tools.NewUUIDv7()
	repo.EXPECT().ListPlatformAdminUserIDs(gomock.Any()).Return([]uuid.UUID{admin1, admin2}, nil)

	require.NoError(t, router.RegistryCredentialBroken(context.Background(), inboxUseCase.RegistryCredentialAlert{
		Key: "platform_push", Kind: "platform_push", Label: "ghcr.io/acme/platform", Reason: "unauthorized", At: registryAt,
	}))
	require.Len(t, notifier.sent, 2)
	got := notifier.sent[0]
	assert.Equal(t, admin1, got.userID)
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeRegistryPushBroken), got.typ)
	assert.Equal(t, "ghcr.io/acme/platform", got.vars["label"])
	assert.Equal(t, inboxModel.CategoryRequests, got.options.Inbox.Category)
	assert.True(t, got.options.Inbox.ActionRequired)
	assert.Equal(t, inboxModel.RegistryCredentialRef("platform_push"), got.options.Inbox.SubjectRef)
	assert.Nil(t, got.options.ScopeEventID, "a platform token is not scoped to an event")
}

func TestRegistryCredentialBrokenSecretGoesToTheEventsManagersOnly(t *testing.T) {
	router, repo, notifier := registryRouter(t)
	eventID, owner, moderator, secretID := tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7()
	repo.EXPECT().ListEventWriteManagerUserIDs(gomock.Any(), eventID).Return([]uuid.UUID{owner, moderator}, nil)

	require.NoError(t, router.RegistryCredentialBroken(context.Background(), inboxUseCase.RegistryCredentialAlert{
		Key: "event_secret:" + secretID.String(), Kind: "event_secret", Label: "registry.example.org/acme",
		EventID: eventID, EventName: "Spring CTF", EventTag: "ctf-2026", Reason: "unauthorized", At: registryAt,
	}))
	require.Len(t, notifier.sent, 2)
	got := notifier.sent[1]
	assert.Equal(t, moderator, got.userID)
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeRegistrySecretBroken), got.typ)
	assert.Equal(t, "Spring CTF", got.vars["event_name"])
	assert.Equal(t, inboxModel.RoleManager, got.options.Inbox.Role)
	assert.Equal(t, inboxModel.CategoryRequests, got.options.Inbox.Category)
	require.NotNil(t, got.options.ScopeEventID)
	assert.Equal(t, eventID, *got.options.ScopeEventID)
}

func TestRegistryCredentialRestoredClosesTheRequestAndSendsOneNote(t *testing.T) {
	router, repo, notifier := registryRouter(t)
	admin := tools.NewUUIDv7()
	repo.EXPECT().ResolveInboxBySubjectRef(gomock.Any(), postgres.ResolveInboxBySubjectRefParams{
		SubjectRef: inboxModel.RegistryCredentialRef("platform_pull"), Resolution: "resolved",
	}).Return(int64(1), nil)
	repo.EXPECT().ListPlatformAdminUserIDs(gomock.Any()).Return([]uuid.UUID{admin}, nil)

	require.NoError(t, router.RegistryCredentialRestored(context.Background(), inboxUseCase.RegistryCredentialAlert{
		Key: "platform_pull", Kind: "platform_pull", Label: "ghcr.io/acme/platform", At: registryAt,
	}))
	require.Len(t, notifier.sent, 1)
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeRegistryPullRestored), notifier.sent[0].typ)
	assert.Equal(t, inboxModel.CategoryPersonal, notifier.sent[0].options.Inbox.Category)
	assert.False(t, notifier.sent[0].options.Inbox.ActionRequired)
}

func TestRegistryCredentialClosedOnlyResolves(t *testing.T) {
	router, repo, notifier := registryRouter(t)
	repo.EXPECT().ResolveInboxBySubjectRef(gomock.Any(), postgres.ResolveInboxBySubjectRefParams{
		SubjectRef: inboxModel.RegistryCredentialRef("platform_push"), Resolution: "resolved",
	}).Return(int64(0), nil)
	require.NoError(t, router.RegistryCredentialClosed(context.Background(), "platform_push"))
	assert.Empty(t, notifier.sent)
}

func TestRegistryTypesSupportInAppAndBrokenOnesAreDismissable(t *testing.T) {
	for _, typ := range []string{
		inboxModel.TypeRegistryPushBroken, inboxModel.TypeRegistryPushRestored, inboxModel.TypeRegistryPullBroken,
		inboxModel.TypeRegistryPullRestored, inboxModel.TypeRegistrySecretBroken, inboxModel.TypeRegistrySecretRestored,
	} {
		assert.True(t, notificationTypes.Supports(notificationTypes.NotificationType(typ), notificationTypes.NotificationChannelInApp), typ)
	}
	assert.True(t, inboxModel.ManuallyResolvable(inboxModel.TypeRegistrySecretBroken))
	assert.False(t, inboxModel.ManuallyResolvable(inboxModel.TypeRegistryPushRestored))
}
