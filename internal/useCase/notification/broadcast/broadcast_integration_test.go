package broadcastUseCase_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/dispatchRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
	broadcastUseCase "github.com/cybericebox/daemon/internal/useCase/notification/broadcast"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
	inAppUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/inapp"
	dispatcherUseCase "github.com/cybericebox/daemon/internal/useCase/notification/dispatcher"
	"github.com/cybericebox/daemon/pkg/email"
	"github.com/cybericebox/daemon/pkg/worker"
)

type mailbox struct {
	messages []email.Message
	routes   []*uuid.UUID
	failFor  string
}

func (m *mailbox) Deliver(_ context.Context, eventID *uuid.UUID, msg email.Message) error {
	if msg.To == m.failFor {
		return errors.New("mailbox unavailable")
	}
	m.messages = append(m.messages, msg)
	m.routes = append(m.routes, eventID)
	return nil
}

// e2eNotifier is the real dispatch pipeline run synchronously: it creates the
// journal row like Notify does and then processes it with the real channel
// handlers (what the River worker does in production).
type e2eNotifier struct {
	dispatches *dispatchRepo.Repository
	dispatcher *dispatcherUseCase.NotificationDispatcher
}

func (n *e2eNotifier) Notify(ctx context.Context, userID uuid.UUID, p notificationTypes.NotificationPayload, opts ...dispatchModel.NotifyOption) error {
	o := dispatchModel.ApplyNotifyOptions(opts)
	id := uuid.Must(uuid.NewV7())
	if err := n.dispatches.CreateForBroadcast(ctx, id, string(p.NotificationType()), userID, o.ScopeEventID, o.BroadcastID); err != nil {
		return err
	}
	raw, err := p.Marshal()
	if err != nil {
		return err
	}
	vars := map[string]any{}
	if err = json.Unmarshal(raw, &vars); err != nil {
		return err
	}
	return n.dispatcher.ProcessNotification(ctx, dispatchModel.ProcessInput{
		DispatchID: id, UserID: userID, Type: string(p.NotificationType()), Vars: vars,
		OverrideChannels: o.OverrideChannels, ScopeEventID: o.ScopeEventID, Inbox: o.Inbox, BroadcastID: o.BroadcastID,
	})
}

func seedUser(t *testing.T, db *testhelpers.TestDB, mail, first, last string) uuid.UUID {
	t.Helper()
	u := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), mail, time.Now())
	u.FirstName, u.LastName = first, last
	created, err := userRepo.New(db.Queries).Create(context.Background(), u)
	require.NoError(t, err)
	_, err = db.Pool.Exec(context.Background(), `UPDATE users SET status = 'active', first_name = $2, last_name = $3 WHERE id = $1`, created.ID, first, last)
	require.NoError(t, err)
	return created.ID
}

func harness2(t *testing.T) (*testhelpers.TestDB, *mailbox, *broadcastUseCase.NotificationBroadcastUseCase, *[]jobsModel.BroadcastSendArgs) {
	t.Helper()
	db := testhelpers.SetupTestDB(t)
	box := &mailbox{}
	dispatcher := dispatcherUseCase.NewNotificationDispatcher(dispatcherUseCase.Dependencies{
		Repo: db.Queries, Enqueuer: worker.NewEnqueuer(), RetryDelay: time.Millisecond,
		Handlers: []dispatcherUseCase.Handler{inAppUseCase.NewHandler(db.Queries), emailUseCase.NewHandler(db.Queries, box, nil)},
	})
	var queued []jobsModel.BroadcastSendArgs
	uc := broadcastUseCase.NewNotificationBroadcastUseCase(broadcastUseCase.Dependencies{
		Repo:     db.Queries,
		Notifier: &e2eNotifier{dispatches: dispatchRepo.New(db.Queries), dispatcher: dispatcher},
		Enqueue: func(_ context.Context, a jobsModel.BroadcastSendArgs) error {
			queued = append(queued, a)
			return nil
		},
		EventDomain: "example.org",
	})
	return db, box, uc, &queued
}

func emailAndInApp() broadcastModel.Content {
	return broadcastModel.Content{
		Channels: []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail, notificationTypes.NotificationChannelInApp},
		Subject:  "Hello {{.user_first_name}}",
		EmailBody: json.RawMessage(`[{"type":"rich_text","content":{"root":{"type":"root","children":[
			{"type":"paragraph","children":[{"type":"text","text":"Dear ","format":0},{"type":"variable","varName":"user_name"}]},
			{"type":"paragraph","children":[{"type":"text","text":"Welcome to ","format":0},{"type":"variable","varName":"event_name"}]}]}}}]`),
		InAppTitle: "Hi {{.user_first_name}}", InAppBody: "<p>News for {{.user_name}}</p>",
	}
}

// A platform broadcast reaches every chosen recipient on both channels, each one
// is a journal dispatch linked to the broadcast, and a failed mailbox is
// recorded per recipient without blocking the others.
func TestBroadcast_EndToEnd_PlatformScopeThroughTheJournal(t *testing.T) {
	db, box, uc, queued := harness2(t)
	ctx := context.Background()
	admin := seedUser(t, db, "admin@test.test", "Root", "Admin")
	ann := seedUser(t, db, "ann@test.test", "Ann", "Lee")
	bob := seedUser(t, db, "bob@test.test", "Bob", "Ray")
	box.failFor = "bob@test.test"

	b, err := uc.SendBroadcast(ctx, broadcastUseCase.SendInput{
		ActorID: admin, Content: emailAndInApp(),
		Audience: broadcastModel.Audience{Kind: broadcastModel.KindUsers, UserIDs: []uuid.UUID{ann, bob}},
	})
	require.NoError(t, err)
	require.Equal(t, int32(2), b.RecipientCount)
	require.Len(t, *queued, 1)

	require.NoError(t, uc.ProcessBroadcast(ctx, (*queued)[0]))

	require.Len(t, box.messages, 1, "only Ann's mailbox accepted the message")
	require.Equal(t, "ann@test.test", box.messages[0].To)
	require.Equal(t, "Hello Ann", box.messages[0].Subject, "variables are filled per recipient")
	require.Contains(t, box.messages[0].HTML, "Dear Ann Lee")
	require.NotContains(t, box.messages[0].HTML, "Welcome to", "a paragraph with an empty variable (no event on the platform) is dropped, like in templates")
	require.Nil(t, box.routes[0], "a platform broadcast is sent as the platform")

	var title, body string
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT title, body FROM in_app_notifications WHERE user_id = $1`, ann).Scan(&title, &body))
	require.Equal(t, "Hi Ann", title)
	require.Contains(t, body, "News for Ann Lee")

	got, err := uc.GetBroadcast(ctx, b.ID, nil)
	require.NoError(t, err)
	require.Equal(t, broadcastModel.StatusDone, got.Status)
	require.Equal(t, int64(1), got.SentCount)
	require.Equal(t, int64(1), got.FailedCount, "Bob's failed email counts as failed")

	deliveries, err := uc.ListBroadcastDeliveries(ctx, b.ID, 20, 0)
	require.NoError(t, err)
	require.Equal(t, "bob@test.test", deliveries[0].RecipientEmail, "failures come first")
	require.Equal(t, "error", deliveries[0].TargetStatus)
	require.Contains(t, deliveries[0].Error, "mailbox unavailable")

	// The journal shows every message as a broadcast dispatch.
	rows, total, err := dispatchRepo.New(db.Queries).List(ctx, dispatchModel.ListDispatchesFilter{Type: "broadcast", Limit: 10})
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	for _, r := range rows {
		require.NotNil(t, r.BroadcastID)
		require.Equal(t, b.ID, *r.BroadcastID)
	}

	// Re-running the sender (an interrupted job) does not send anything twice.
	require.NoError(t, uc.ProcessBroadcast(ctx, (*queued)[0]))
	require.Len(t, box.messages, 1)
}

// An Event broadcast goes out on the Event route with the Event variables, and
// stays in that Event's history.
func TestBroadcast_EndToEnd_EventScope(t *testing.T) {
	db, box, uc, queued := harness2(t)
	ctx := context.Background()
	owner := seedUser(t, db, "owner@test.test", "Own", "Er")
	player := seedUser(t, db, "player@test.test", "Pia", "Yer")
	now := time.Now()
	e, err := eventModel.NewEvent("spring", "Spring CTF", now, now.Add(24*time.Hour), owner, now)
	require.NoError(t, err)
	event, err := eventRepo.New(db.Queries).Create(ctx, e)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `INSERT INTO event_participants (event_id, user_id, status, created_at) VALUES ($1, $2, 2, now())`, event.ID, player)
	require.NoError(t, err)

	b, err := uc.SendBroadcast(ctx, broadcastUseCase.SendInput{
		ScopeEventID: &event.ID, ActorID: owner, Content: emailAndInApp(),
		Audience: broadcastModel.Audience{Kind: broadcastModel.KindApproved},
	})
	require.NoError(t, err)
	require.NoError(t, uc.ProcessBroadcast(ctx, (*queued)[0]))

	require.Len(t, box.messages, 1)
	require.Equal(t, "player@test.test", box.messages[0].To)
	require.Contains(t, box.messages[0].HTML, "Welcome to Spring CTF")
	require.NotNil(t, box.routes[0])
	require.Equal(t, event.ID, *box.routes[0], "participant mail uses the event sender")

	var scope uuid.NullUUID
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT scope_event_id FROM in_app_notifications WHERE user_id = $1`, player).Scan(&scope))
	require.Equal(t, event.ID, scope.UUID, "the in-app copy is filed under the event inbox")

	history, err := uc.ListBroadcasts(ctx, broadcastModel.ListFilter{Scope: event.ID.String(), Limit: 10})
	require.NoError(t, err)
	require.Len(t, history, 1)
	platform, err := uc.ListBroadcasts(ctx, broadcastModel.ListFilter{Scope: "platform", Limit: 10})
	require.NoError(t, err)
	require.Empty(t, platform, "an event broadcast is not in the platform history")
	require.Equal(t, b.ID, history[0].ID)
}
