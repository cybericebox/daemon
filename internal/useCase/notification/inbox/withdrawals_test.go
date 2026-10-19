package inboxUseCase_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
	"github.com/cybericebox/daemon/pkg/tools"
)

// A group withdrawn again and again reaches every platform admin once per withdrawal series, as a request.
func TestLabWithdrawalsRepeatedGoesToThePlatformAdminsOnce(t *testing.T) {
	router, repo, notifier := registryRouter(t)
	router.SetAdminURL("https://admin.example.org/")
	agent, admin := tools.NewUUIDv7(), tools.NewUUIDv7()
	alert := inboxUseCase.LabWithdrawalAlert{AgentID: agent, AgentName: "main", Group: "deploy/t-1", Attempt: 3, Series: 1792400060000, At: registryAt}
	ref := inboxModel.LabWithdrawalRef(agent, "deploy/t-1", 1792400060000)
	repo.EXPECT().InboxSubjectExists(gomock.Any(), ref).Return(false, nil)
	repo.EXPECT().ListPlatformAdminUserIDs(gomock.Any()).Return([]uuid.UUID{admin}, nil)

	require.NoError(t, router.LabWithdrawalsRepeated(context.Background(), alert))
	require.Len(t, notifier.sent, 1)
	got := notifier.sent[0]
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeLabWithdrawalsRepeated), got.typ)
	assert.Equal(t, "main", got.vars["agent_name"])
	assert.Equal(t, "deploy/t-1", got.vars["group"])
	assert.Equal(t, "3", got.vars["attempt"])
	assert.Equal(t, "https://admin.example.org/", got.vars["admin_url"], "the link variable of the template")
	assert.Equal(t, inboxModel.CategoryRequests, got.options.Inbox.Category)
	assert.Equal(t, ref, got.options.Inbox.SubjectRef)

	// The same series again (another lab of the group, a restart): nothing new.
	repo.EXPECT().InboxSubjectExists(gomock.Any(), ref).Return(true, nil)
	require.NoError(t, router.LabWithdrawalsRepeated(context.Background(), alert))
	assert.Len(t, notifier.sent, 1)
}
