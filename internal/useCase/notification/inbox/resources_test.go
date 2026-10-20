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

func TestRequestRouter_ResourceChangeGoesToAdminsAndTheDecisionToTheOrganizer(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	notifier := &fakeNotifier{}
	router := inboxUseCase.NewRequestRouter(repo, notifier)
	organizer, admin, changeID := tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7()
	repo.EXPECT().GetUserByID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID) (postgres.User, error) {
		return postgres.User{ID: id, FirstName: "Name", Email: "x@example.org"}, nil
	}).AnyTimes()
	repo.EXPECT().ListSuperAdminUserIDs(gomock.Any()).Return([]uuid.UUID{admin, organizer}, nil)

	c := inboxUseCase.ResourceChange{ID: changeID, EventID: tools.NewUUIDv7(), EventName: "CTF", RequestedBy: organizer, Reason: "more teams", Summary: "size 8 / 16Gi"}
	require.NoError(t, router.ResourceChangeRequested(context.Background(), c))
	require.Len(t, notifier.sent, 1, "the organizer never decides their own request")
	got := notifier.sent[0]
	assert.Equal(t, admin, got.userID)
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeResourceChangeRequested), got.typ)
	assert.Equal(t, "size 8 / 16Gi", got.vars["summary"])
	assert.Equal(t, inboxModel.CategoryRequests, got.options.Inbox.Category)
	assert.Equal(t, inboxModel.ResourceChangeRef(changeID), got.options.Inbox.SubjectRef)

	notifier.sent = nil
	repo.EXPECT().ResolveInboxBySubjectRef(gomock.Any(), postgres.ResolveInboxBySubjectRefParams{
		SubjectRef: inboxModel.ResourceChangeRef(changeID), Resolution: "rejected", ResolvedBy: uuid.NullUUID{UUID: admin, Valid: true},
	}).Return(int64(2), nil)
	require.NoError(t, router.ResourceChangeDecided(context.Background(), c, false, admin))
	require.Len(t, notifier.sent, 1)
	assert.Equal(t, organizer, notifier.sent[0].userID)
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeResourceChangeRejected), notifier.sent[0].typ)
	assert.Equal(t, inboxModel.CategoryPersonal, notifier.sent[0].options.Inbox.Category)
}

func TestRequestRouter_ResourceAlarmGoesToEveryAdminAndEscalationReplacesTheCopy(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	notifier := &fakeNotifier{}
	router := inboxUseCase.NewRequestRouter(repo, notifier)
	admin1, admin2, alarmID := tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7()
	repo.EXPECT().GetUserByID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID) (postgres.User, error) {
		return postgres.User{ID: id, Email: "x@example.org"}, nil
	}).AnyTimes()
	repo.EXPECT().ListSuperAdminUserIDs(gomock.Any()).Return([]uuid.UUID{admin1, admin2}, nil).Times(2)

	router.SetAdminURL("https://admin.example.org/")
	a := inboxUseCase.ResourceAlarm{ID: alarmID, Kind: "not_connected", EventName: "CTF", AgentName: "main", Units: 3, Stage: 2, RaisedAt: time.Now()}
	require.NoError(t, router.ResourceAlarmRaised(context.Background(), a))
	require.Len(t, notifier.sent, 2)
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeResourceAlarmRaised), notifier.sent[0].typ)
	assert.Equal(t, "3", notifier.sent[0].vars["units"])
	assert.Equal(t, "агент не на звʼязку", notifier.sent[0].vars["alarm_kind"], "the reason is text, never the raw enum")
	assert.Equal(t, "https://admin.example.org/", notifier.sent[0].vars["admin_url"], "the link variable of the template")
	assert.True(t, notifier.sent[0].options.Inbox.ActionRequired)
	assert.Equal(t, inboxModel.ResourceAlarmRef(alarmID), notifier.sent[0].options.Inbox.SubjectRef)

	notifier.sent = nil
	repo.EXPECT().ResolveInboxBySubjectRef(gomock.Any(), postgres.ResolveInboxBySubjectRefParams{
		SubjectRef: inboxModel.ResourceAlarmRef(alarmID), Resolution: "resolved",
	}).Return(int64(2), nil)
	a.Escalated, a.Stage = true, 3
	require.NoError(t, router.ResourceAlarmRaised(context.Background(), a))
	assert.Len(t, notifier.sent, 2, "a new copy per admin after the old one was closed")
}

func TestResourceTypesSupportInApp(t *testing.T) {
	for _, typ := range []string{inboxModel.TypeResourceChangeRequested, inboxModel.TypeResourceChangeApproved, inboxModel.TypeResourceChangeRejected, inboxModel.TypeResourceAlarmRaised} {
		assert.True(t, notificationTypes.Supports(notificationTypes.NotificationType(typ), notificationTypes.NotificationChannelInApp), typ)
	}
	assert.True(t, inboxModel.ManuallyResolvable(inboxModel.TypeResourceAlarmRaised), "an admin closes an alarm request by hand")
}

func TestRequestRouter_CPUThrottledGoesToEveryPlatformAdmin(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	notifier := &fakeNotifier{}
	router := inboxUseCase.NewRequestRouter(repo, notifier)
	admin1, admin2 := tools.NewUUIDv7(), tools.NewUUIDv7()
	repo.EXPECT().GetUserByID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID) (postgres.User, error) {
		return postgres.User{ID: id, Email: "x@example.org"}, nil
	}).AnyTimes()
	repo.EXPECT().ListPlatformAdminUserIDs(gomock.Any()).Return([]uuid.UUID{admin1, admin2}, nil)
	repo.EXPECT().InboxSubjectExists(gomock.Any(), inboxModel.CPUThrottleRef("uid-1", "web")).Return(false, nil)

	router.SetAdminURL("https://admin.example.org/")
	require.NoError(t, router.CPUThrottled(context.Background(), inboxUseCase.CPUThrottle{
		AgentID: "a-1", LabRun: "uid-1", LabName: "lab-1", LabGroupName: "team-1", Device: "web", Percent: 70, WindowSeconds: 300, RaisedAt: time.Now(),
	}))
	require.Len(t, notifier.sent, 2)
	got := notifier.sent[0]
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeResourceCPUThrottled), got.typ)
	assert.Equal(t, "web", got.vars["device_name"])
	assert.Equal(t, "70", got.vars["percent"])
	assert.Equal(t, "5", got.vars["window_minutes"])
	assert.Equal(t, "https://admin.example.org/", got.vars["admin_url"])
	assert.Equal(t, inboxModel.CategoryRequests, got.options.Inbox.Category)
	assert.Equal(t, inboxModel.CPUThrottleRef("uid-1", "web"), got.options.Inbox.SubjectRef)
	assert.True(t, notificationTypes.Supports(notificationTypes.NotificationType(inboxModel.TypeResourceCPUThrottled), notificationTypes.NotificationChannelInApp))
	assert.True(t, inboxModel.ManuallyResolvable(inboxModel.TypeResourceCPUThrottled))
}

// The daemon forgets what it alerted when it restarts; the request already delivered (open, or closed by an admin)
// is what keeps one request per device per lab run.
func TestRequestRouter_CPUThrottledIsNotSentAgainForADeliveredRequest(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	notifier := &fakeNotifier{}
	router := inboxUseCase.NewRequestRouter(repo, notifier)
	repo.EXPECT().InboxSubjectExists(gomock.Any(), inboxModel.CPUThrottleRef("uid-1", "web")).Return(true, nil)

	require.NoError(t, router.CPUThrottled(context.Background(), inboxUseCase.CPUThrottle{
		AgentID: "a-1", LabRun: "uid-1", LabName: "lab-1", LabGroupName: "team-1", Device: "web", Percent: 70, WindowSeconds: 300, RaisedAt: time.Now(),
	}))
	assert.Empty(t, notifier.sent, "no request, and the admins are not even listed")
}
