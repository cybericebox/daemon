package inboxUseCase_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
	"github.com/cybericebox/daemon/pkg/tools"
)

func TestExerciseImportDoneTellsOnlyTheStarterHowManyTasks(t *testing.T) {
	router, _, notifier := registryRouter(t)
	user := tools.NewUUIDv7()
	require.NoError(t, router.ExerciseImportDone(context.Background(), inboxUseCase.ImportEnd{User: user, Created: 12, At: registryAt}))
	require.Len(t, notifier.sent, 1)
	got := notifier.sent[0]
	assert.Equal(t, user, got.userID)
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeExerciseImportDone), got.typ)
	assert.Equal(t, "12", got.vars["count"])
	assert.Equal(t, inboxModel.CategoryPersonal, got.options.Inbox.Category)
	assert.False(t, got.options.Inbox.ActionRequired)
}

func TestExerciseImportFailedTellsTheStarter(t *testing.T) {
	router, _, notifier := registryRouter(t)
	user := tools.NewUUIDv7()
	require.NoError(t, router.ExerciseImportFailed(context.Background(), inboxUseCase.ImportEnd{User: user, At: registryAt}))
	require.Len(t, notifier.sent, 1)
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeExerciseImportFailed), notifier.sent[0].typ)
	assert.Equal(t, user, notifier.sent[0].userID)
}

func TestExerciseImportTypesSupportInApp(t *testing.T) {
	for _, typ := range []string{inboxModel.TypeExerciseImportDone, inboxModel.TypeExerciseImportFailed} {
		assert.True(t, notificationTypes.Supports(notificationTypes.NotificationType(typ), notificationTypes.NotificationChannelInApp), typ)
	}
}
