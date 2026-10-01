package broadcastUseCase_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	broadcastUseCase "github.com/cybericebox/daemon/internal/useCase/notification/broadcast"
)

type sent struct {
	userID  uuid.UUID
	payload notificationPayloads.BroadcastPayload
	opts    dispatchModel.NotifyOptions
}

type fakeNotifier struct {
	sent   []sent
	failOn map[uuid.UUID]bool
}

func (n *fakeNotifier) Notify(_ context.Context, userID uuid.UUID, p notificationTypes.NotificationPayload, opts ...dispatchModel.NotifyOption) error {
	if n.failOn[userID] {
		return errors.New("enqueue failed")
	}
	n.sent = append(n.sent, sent{userID: userID, payload: p.(notificationPayloads.BroadcastPayload), opts: dispatchModel.ApplyNotifyOptions(opts)})
	return nil
}

type harness struct {
	repo     *postgresMocks.MockQuerier
	notifier *fakeNotifier
	enqueued []jobsModel.BroadcastSendArgs
	uc       *broadcastUseCase.NotificationBroadcastUseCase
}

func setup(t *testing.T) *harness {
	t.Helper()
	h := &harness{repo: postgresMocks.NewMockQuerier(gomock.NewController(t)), notifier: &fakeNotifier{failOn: map[uuid.UUID]bool{}}}
	h.uc = broadcastUseCase.NewNotificationBroadcastUseCase(broadcastUseCase.Dependencies{
		Repo: h.repo, Notifier: h.notifier, EventDomain: "example.org",
		Enqueue: func(_ context.Context, args jobsModel.BroadcastSendArgs) error {
			h.enqueued = append(h.enqueued, args)
			return nil
		},
	})
	return h
}

func content() broadcastModel.Content {
	return broadcastModel.Content{
		Channels: []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail, notificationTypes.NotificationChannelInApp},
		Subject:  "Hello {{.user_name}}", EmailBody: json.RawMessage(`[{"type":"paragraph"}]`), InAppTitle: "Hello",
	}
}

func platformRows(n int) []postgres.ListPlatformBroadcastAudienceRow {
	rows := make([]postgres.ListPlatformBroadcastAudienceRow, 0, n)
	for i := range n {
		rows = append(rows, postgres.ListPlatformBroadcastAudienceRow{
			ID: uuid.Must(uuid.NewV7()), Email: "u" + string(rune('a'+i)) + "@test.test", FirstName: "U", LastName: string(rune('A' + i)),
		})
	}
	return rows
}

func storedRow(id uuid.UUID, event *uuid.UUID, aud broadcastModel.Audience) postgres.GetNotificationBroadcastRow {
	raw, _ := json.Marshal(aud)
	row := postgres.GetNotificationBroadcastRow{
		ID: id, Channels: []string{"email", "in_app"}, Subject: "Hello", EmailBody: []byte(`[{"type":"paragraph"}]`),
		EmailStyling: []byte(`{}`), InappTitle: "Hello", Audience: raw, Status: "sending", CreatedAt: time.Now(),
	}
	if event != nil {
		row.ScopeEventID = uuid.NullUUID{UUID: *event, Valid: true}
	}
	return row
}

func TestCountBroadcastAudience_ValidatesScopeAndCounts(t *testing.T) {
	h := setup(t)
	h.repo.EXPECT().ListPlatformBroadcastAudience(gomock.Any(), gomock.Any()).Return(platformRows(3), nil)
	n, err := h.uc.CountBroadcastAudience(context.Background(), nil, broadcastModel.Audience{Kind: broadcastModel.KindAll})
	require.NoError(t, err)
	require.Equal(t, 3, n)

	// An Event manager cannot reach every user of the platform.
	event := uuid.Must(uuid.NewV7())
	_, err = h.uc.CountBroadcastAudience(context.Background(), &event, broadcastModel.Audience{Kind: broadcastModel.KindAll})
	require.Error(t, err)
}

func TestSendBroadcast_StoresAndQueuesTheSender(t *testing.T) {
	h := setup(t)
	actor := uuid.Must(uuid.NewV7())
	h.repo.EXPECT().ListPlatformBroadcastAudience(gomock.Any(), gomock.Any()).Return(platformRows(3), nil)
	h.repo.EXPECT().CreateNotificationBroadcast(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateNotificationBroadcastParams) (postgres.NotificationBroadcast, error) {
			require.Equal(t, int32(3), arg.RecipientCount)
			require.Equal(t, []string{"email", "in_app"}, arg.Channels)
			require.Equal(t, actor, arg.CreatedBy.UUID)
			require.False(t, arg.ScopeEventID.Valid)
			return postgres.NotificationBroadcast{}, nil
		})

	b, err := h.uc.SendBroadcast(context.Background(), broadcastUseCase.SendInput{
		ActorID: actor, Content: content(), Audience: broadcastModel.Audience{Kind: broadcastModel.KindAll},
	})
	require.NoError(t, err)
	require.Equal(t, int32(3), b.RecipientCount)
	require.Equal(t, []jobsModel.BroadcastSendArgs{{BroadcastID: b.ID}}, h.enqueued)
	require.Empty(t, h.notifier.sent, "recipients are dispatched by the sender job, not in the request")
}

func TestSendBroadcast_Refusals(t *testing.T) {
	h := setup(t)
	actor := uuid.Must(uuid.NewV7())

	bad := content()
	bad.Subject = ""
	_, err := h.uc.SendBroadcast(context.Background(), broadcastUseCase.SendInput{ActorID: actor, Content: bad, Audience: broadcastModel.Audience{Kind: broadcastModel.KindAll}})
	require.Error(t, err)

	h.repo.EXPECT().ListPlatformBroadcastAudience(gomock.Any(), gomock.Any()).Return(nil, nil)
	_, err = h.uc.SendBroadcast(context.Background(), broadcastUseCase.SendInput{ActorID: actor, Content: content(), Audience: broadcastModel.Audience{Kind: broadcastModel.KindAll}})
	require.True(t, notificationModel.ErrBroadcastNoRecipients.Err().Is(err), "an empty audience is refused: %v", err)
	require.Empty(t, h.enqueued)
}

func TestProcessBroadcast_DispatchesEachRecipientThroughTheJournalPipeline(t *testing.T) {
	h := setup(t)
	id := uuid.Must(uuid.NewV7())
	rows := platformRows(3)
	h.repo.EXPECT().GetNotificationBroadcast(gomock.Any(), id).Return(storedRow(id, nil, broadcastModel.Audience{Kind: broadcastModel.KindAll}), nil)
	h.repo.EXPECT().ListPlatformBroadcastAudience(gomock.Any(), gomock.Any()).Return(rows, nil)
	// The first recipient already has a dispatch (an earlier, interrupted run).
	h.repo.EXPECT().ListBroadcastQueuedUserIDs(gomock.Any(), uuid.NullUUID{UUID: id, Valid: true}).Return([]uuid.UUID{rows[0].ID}, nil)
	h.repo.EXPECT().FinishNotificationBroadcast(gomock.Any(), postgres.FinishNotificationBroadcastParams{ID: id, Status: "done", RecipientCount: 3}).Return(nil)

	require.NoError(t, h.uc.ProcessBroadcast(context.Background(), jobsModel.BroadcastSendArgs{BroadcastID: id}))

	require.Len(t, h.notifier.sent, 2, "the recipient queued earlier is not sent twice")
	for i, s := range h.notifier.sent {
		require.Equal(t, rows[i+1].ID, s.userID)
		require.Equal(t, notificationTypes.NotificationTypeBroadcast, s.payload.NotificationType())
		require.Equal(t, rows[i+1].FirstName+" "+rows[i+1].LastName, s.payload.UserName)
		require.Equal(t, rows[i+1].Email, s.payload.UserEmail)
		require.Empty(t, s.payload.EventName, "a platform broadcast has no event variables")
		require.Equal(t, id, *s.opts.BroadcastID, "the dispatch is linked to the broadcast")
		require.Equal(t, []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail, notificationTypes.NotificationChannelInApp}, s.opts.OverrideChannels)
		require.Nil(t, s.opts.ScopeEventID)
		require.Equal(t, inboxModel.RoleSubject, s.opts.Inbox.Role)
	}
	require.Empty(t, h.enqueued)
}

func TestProcessBroadcast_EventScopeUsesEventSenderAndVariables(t *testing.T) {
	h := setup(t)
	id, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	h.repo.EXPECT().GetNotificationBroadcast(gomock.Any(), id).Return(storedRow(id, &eventID, broadcastModel.Audience{Kind: broadcastModel.KindApproved}), nil)
	h.repo.EXPECT().ListEventBroadcastAudience(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ListEventBroadcastAudienceParams) ([]postgres.ListEventBroadcastAudienceRow, error) {
			require.Equal(t, eventID, arg.EventID)
			require.Equal(t, "approved", arg.Kind)
			return []postgres.ListEventBroadcastAudienceRow{{ID: uuid.Must(uuid.NewV7()), Email: "p@test.test", FirstName: "P", LastName: "Q"}}, nil
		})
	h.repo.EXPECT().ListBroadcastQueuedUserIDs(gomock.Any(), gomock.Any()).Return(nil, nil)
	h.repo.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Name: "Spring CTF", Tag: "spring"}, nil)
	h.repo.EXPECT().FinishNotificationBroadcast(gomock.Any(), gomock.Any()).Return(nil)

	require.NoError(t, h.uc.ProcessBroadcast(context.Background(), jobsModel.BroadcastSendArgs{BroadcastID: id}))

	require.Len(t, h.notifier.sent, 1)
	s := h.notifier.sent[0]
	require.Equal(t, "Spring CTF", s.payload.EventName)
	require.Equal(t, "https://spring.example.org/", s.payload.EventURL)
	require.Equal(t, eventID, *s.opts.ScopeEventID, "the event scope selects the event sender and brand")
	require.Equal(t, inboxModel.RoleParticipant, s.opts.Inbox.Role)
	require.Equal(t, inboxModel.CategoryActivity, s.opts.Inbox.Category)
}

func TestProcessBroadcast_LongAudienceContinuesInAnotherRun(t *testing.T) {
	h := setup(t)
	id := uuid.Must(uuid.NewV7())
	h.repo.EXPECT().GetNotificationBroadcast(gomock.Any(), id).Return(storedRow(id, nil, broadcastModel.Audience{Kind: broadcastModel.KindAll}), nil)
	h.repo.EXPECT().ListPlatformBroadcastAudience(gomock.Any(), gomock.Any()).Return(platformRows(250), nil)
	h.repo.EXPECT().ListBroadcastQueuedUserIDs(gomock.Any(), gomock.Any()).Return(nil, nil)

	require.NoError(t, h.uc.ProcessBroadcast(context.Background(), jobsModel.BroadcastSendArgs{BroadcastID: id}))

	require.Len(t, h.notifier.sent, 200, "one run is bounded, River jobs time out")
	require.Equal(t, []jobsModel.BroadcastSendArgs{{BroadcastID: id}}, h.enqueued, "the sender queues itself while recipients remain")
}

func TestProcessBroadcast_FailedRecipientsAreRetriedByTheNextRun(t *testing.T) {
	h := setup(t)
	id := uuid.Must(uuid.NewV7())
	rows := platformRows(3)
	h.notifier.failOn[rows[1].ID] = true
	h.repo.EXPECT().GetNotificationBroadcast(gomock.Any(), id).Return(storedRow(id, nil, broadcastModel.Audience{Kind: broadcastModel.KindAll}), nil)
	h.repo.EXPECT().ListPlatformBroadcastAudience(gomock.Any(), gomock.Any()).Return(rows, nil)
	h.repo.EXPECT().ListBroadcastQueuedUserIDs(gomock.Any(), gomock.Any()).Return(nil, nil)

	require.NoError(t, h.uc.ProcessBroadcast(context.Background(), jobsModel.BroadcastSendArgs{BroadcastID: id}))

	require.Len(t, h.notifier.sent, 2)
	require.Len(t, h.enqueued, 1, "the broadcast is not closed while a recipient failed to queue")
}

func TestProcessBroadcast_NothingQueuedMarksItFailed(t *testing.T) {
	h := setup(t)
	id := uuid.Must(uuid.NewV7())
	rows := platformRows(2)
	h.notifier.failOn[rows[0].ID], h.notifier.failOn[rows[1].ID] = true, true
	h.repo.EXPECT().GetNotificationBroadcast(gomock.Any(), id).Return(storedRow(id, nil, broadcastModel.Audience{Kind: broadcastModel.KindAll}), nil)
	h.repo.EXPECT().ListPlatformBroadcastAudience(gomock.Any(), gomock.Any()).Return(rows, nil)
	h.repo.EXPECT().ListBroadcastQueuedUserIDs(gomock.Any(), gomock.Any()).Return(nil, nil)
	h.repo.EXPECT().FinishNotificationBroadcast(gomock.Any(), postgres.FinishNotificationBroadcastParams{ID: id, Status: "failed", RecipientCount: 2}).Return(nil)

	require.NoError(t, h.uc.ProcessBroadcast(context.Background(), jobsModel.BroadcastSendArgs{BroadcastID: id}))
	require.Empty(t, h.enqueued)
}

func TestProcessBroadcast_ClosedBroadcastIsIgnored(t *testing.T) {
	h := setup(t)
	id := uuid.Must(uuid.NewV7())
	row := storedRow(id, nil, broadcastModel.Audience{Kind: broadcastModel.KindAll})
	row.Status = "done"
	h.repo.EXPECT().GetNotificationBroadcast(gomock.Any(), id).Return(row, nil)
	require.NoError(t, h.uc.ProcessBroadcast(context.Background(), jobsModel.BroadcastSendArgs{BroadcastID: id}))
	require.Empty(t, h.notifier.sent)
}

func TestGetBroadcast_HidesAnotherScope(t *testing.T) {
	h := setup(t)
	id, eventID, otherEvent := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	h.repo.EXPECT().GetNotificationBroadcast(gomock.Any(), id).Return(storedRow(id, &eventID, broadcastModel.Audience{Kind: broadcastModel.KindApproved}), nil).Times(3)

	_, err := h.uc.GetBroadcast(context.Background(), id, &eventID)
	require.NoError(t, err)
	_, err = h.uc.GetBroadcast(context.Background(), id, &otherEvent)
	require.True(t, notificationModel.ErrBroadcastNotFound.Err().Is(err), "another event's broadcast is a plain not-found: %v", err)
	_, err = h.uc.GetBroadcast(context.Background(), id, nil)
	require.NoError(t, err, "the platform scope (nil) does not filter")
}
