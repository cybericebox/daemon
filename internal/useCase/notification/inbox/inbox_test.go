package inboxUseCase_test

import (
	"context"
	"github.com/gofrs/uuid"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	"github.com/cybericebox/daemon/internal/useCase/notification/inbox"
	"github.com/cybericebox/daemon/pkg/tools"
)

func TestListInbox_PaginatesAndMapsReadState(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo})
	ctx := context.Background()

	uid := tools.NewUUIDv7()
	read := time.Now()
	rows := make([]postgres.ListInAppByUserRow, 11)
	for i := range rows {
		rows[i] = postgres.ListInAppByUserRow{ID: tools.NewUUIDv7(), UserID: uid, Title: "T", CreatedAt: time.Now().Add(-time.Duration(i) * time.Second)}
	}
	rows[0].ReadAt = pgtype.Timestamptz{Time: read, Valid: true}
	repo.EXPECT().ListInAppByUser(gomock.Any(), gomock.Cond(func(arg postgres.ListInAppByUserParams) bool {
		return arg.UserID == uid && arg.BeforeCreatedAt.Year() == 9999
	})).Return(rows, nil)

	got, err := uc.ListInbox(ctx, uid, nil, "", nil)
	require.NoError(t, err)
	require.Len(t, got.Items, 10)
	require.NotNil(t, got.NextCursor)
	assert.Equal(t, rows[9].ID, got.NextCursor.ID)
	require.NotNil(t, got.Items[0].ReadAt)
	assert.WithinDuration(t, read, *got.Items[0].ReadAt, time.Second)
	assert.Nil(t, got.Items[1].ReadAt)

	repo.EXPECT().ListInAppByUser(gomock.Any(), postgres.ListInAppByUserParams{
		UserID: uid, BeforeCreatedAt: got.NextCursor.CreatedAt, BeforeID: got.NextCursor.ID,
	}).Return(rows[10:], nil)
	older, err := uc.ListInbox(ctx, uid, nil, "", got.NextCursor)
	require.NoError(t, err)
	require.Len(t, older.Items, 1)
	assert.Nil(t, older.NextCursor)
}

func TestPollInbox_BaselineDoesNotReplayOldUnread(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo})
	uid, id := tools.NewUUIDv7(), tools.NewUUIDv7()
	created := time.Now().UTC()
	repo.EXPECT().GetLatestInboxCursor(gomock.Any(), postgres.GetLatestInboxCursorParams{UserID: uid}).Return(postgres.GetLatestInboxCursorRow{ID: id, CreatedAt: created}, nil)
	repo.EXPECT().CountUnreadInbox(gomock.Any(), postgres.CountUnreadInboxParams{UserID: uid}).Return(int64(3), nil)
	repo.EXPECT().CountInboxByCategory(gomock.Any(), postgres.CountInboxByCategoryParams{UserID: uid}).Return(postgres.CountInboxByCategoryRow{}, nil)

	result, err := uc.PollInbox(context.Background(), uid, nil, nil)
	require.NoError(t, err)
	require.Empty(t, result.NewInbox)
	require.NotNil(t, result.Cursor)
	assert.Equal(t, id, result.Cursor.ID)
	assert.Equal(t, int64(3), result.UnreadCount)
}

func TestPollInbox_EmptyBaselineHasNoCursor(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo})
	uid := tools.NewUUIDv7()
	repo.EXPECT().GetLatestInboxCursor(gomock.Any(), postgres.GetLatestInboxCursorParams{UserID: uid}).Return(postgres.GetLatestInboxCursorRow{}, pgx.ErrNoRows)
	repo.EXPECT().CountUnreadInbox(gomock.Any(), postgres.CountUnreadInboxParams{UserID: uid}).Return(int64(0), nil)
	repo.EXPECT().CountInboxByCategory(gomock.Any(), postgres.CountInboxByCategoryParams{UserID: uid}).Return(postgres.CountInboxByCategoryRow{}, nil)

	result, err := uc.PollInbox(context.Background(), uid, nil, nil)
	require.NoError(t, err)
	assert.Nil(t, result.Cursor)
	assert.Empty(t, result.NewInbox)
}

func TestPollInbox_DeltaAdvancesCursorAndIncludesReadState(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo})
	uid, oldID, newID := tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7()
	oldTime := time.Now().Add(-time.Minute).UTC()
	newTime := oldTime.Add(time.Second)
	since := &inboxModel.Cursor{ID: oldID, CreatedAt: oldTime}
	repo.EXPECT().ListNewInboxSince(gomock.Any(), postgres.ListNewInboxSinceParams{UserID: uid, SinceCreatedAt: oldTime, SinceID: oldID}).Return([]postgres.ListNewInboxSinceRow{{ID: newID, UserID: uid, Title: "Нове", CreatedAt: newTime}}, nil)
	repo.EXPECT().CountUnreadInbox(gomock.Any(), postgres.CountUnreadInboxParams{UserID: uid}).Return(int64(4), nil)
	repo.EXPECT().CountInboxByCategory(gomock.Any(), postgres.CountInboxByCategoryParams{UserID: uid}).Return(postgres.CountInboxByCategoryRow{}, nil)

	result, err := uc.PollInbox(context.Background(), uid, nil, since)
	require.NoError(t, err)
	require.Len(t, result.NewInbox, 1)
	assert.Equal(t, newID, result.Cursor.ID)
	assert.Equal(t, "Нове", result.NewInbox[0].Title)
	assert.Equal(t, int64(4), result.UnreadCount)
}

// TestMarkRead_OwnNotification_Success: a notification owned by the caller
// affects 1 row → no error.
func TestMarkRead_OwnNotification_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo})
	ctx := context.Background()

	uid := tools.NewUUIDv7()
	nid := tools.NewUUIDv7()
	repo.EXPECT().
		MarkInAppRead(gomock.Any(), postgres.MarkInAppReadParams{ID: nid, UserID: uid}).
		Return(int64(1), nil)

	err := uc.MarkRead(ctx, uid, nid)
	require.NoError(t, err)
}

// TestMarkRead_CrossUser_NotFound: a notification owned by another user is
// scoped out by the AND user_id clause → 0 rows → not-found error (IDOR guard).
func TestMarkRead_CrossUser_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo})
	ctx := context.Background()

	attacker := tools.NewUUIDv7()
	victimNotificationID := tools.NewUUIDv7()
	repo.EXPECT().
		MarkInAppRead(gomock.Any(), postgres.MarkInAppReadParams{ID: victimNotificationID, UserID: attacker}).
		Return(int64(0), nil)

	err := uc.MarkRead(ctx, attacker, victimNotificationID)
	require.Error(t, err)
	assert.ErrorIs(t, err, notificationModel.ErrInboxNotFound.Err())
}

// TestListInbox_EventScopeFiltersAndLabels: an Event scope reaches SQL as the
// event filter and rows keep their Event label (M5).
func TestListInbox_EventScopeFiltersAndLabels(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo})
	uid, eventID := tools.NewUUIDv7(), tools.NewUUIDv7()
	repo.EXPECT().ListInAppByUser(gomock.Any(), gomock.Cond(func(arg postgres.ListInAppByUserParams) bool {
		return arg.UserID == uid && arg.EventFilter == eventID.String()
	})).Return([]postgres.ListInAppByUserRow{
		{ID: tools.NewUUIDv7(), UserID: uid, ScopeEventID: uuid.NullUUID{UUID: eventID, Valid: true}, EventName: "CTF", EventTag: "ctf"},
		{ID: tools.NewUUIDv7(), UserID: uid},
	}, nil)

	got, err := uc.ListInbox(context.Background(), uid, &eventID, "", nil)
	require.NoError(t, err)
	require.Len(t, got.Items, 2)
	require.NotNil(t, got.Items[0].EventID)
	assert.Equal(t, eventID, *got.Items[0].EventID)
	assert.Equal(t, "CTF", got.Items[0].EventName)
	assert.Nil(t, got.Items[1].EventID)
}

// TestPollInbox_PlatformScopeReachesSQL: the platform-only scope reaches every
// poll query as the nil-uuid filter, which matches only items without an Event.
func TestPollInbox_PlatformScopeReachesSQL(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo})
	uid := tools.NewUUIDv7()
	filter := uuid.Nil.String()
	repo.EXPECT().GetLatestInboxCursor(gomock.Any(), postgres.GetLatestInboxCursorParams{UserID: uid, EventFilter: filter}).
		Return(postgres.GetLatestInboxCursorRow{}, pgx.ErrNoRows)
	repo.EXPECT().CountUnreadInbox(gomock.Any(), postgres.CountUnreadInboxParams{UserID: uid, EventFilter: filter}).Return(int64(3), nil)
	repo.EXPECT().CountInboxByCategory(gomock.Any(), postgres.CountInboxByCategoryParams{UserID: uid, EventFilter: filter}).Return(postgres.CountInboxByCategoryRow{}, nil)
	repo.EXPECT().MarkAllInAppReadByUser(gomock.Any(), postgres.MarkAllInAppReadByUserParams{UserID: uid, EventFilter: filter}).Return(nil)

	scope := inboxModel.PlatformScope
	got, err := uc.PollInbox(context.Background(), uid, &scope, nil)
	require.NoError(t, err)
	assert.Nil(t, got.Cursor)
	assert.Equal(t, int64(3), got.UnreadCount)
	require.NoError(t, uc.MarkAllRead(context.Background(), uid, &scope, ""))
}

// TestPollInbox_ReturnsTabCounts: the tab badges and the other-Events count
// come from one query scoped like the poll.
func TestPollInbox_ReturnsTabCounts(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo})
	uid, eventID := tools.NewUUIDv7(), tools.NewUUIDv7()
	filter := eventID.String()
	repo.EXPECT().GetLatestInboxCursor(gomock.Any(), postgres.GetLatestInboxCursorParams{UserID: uid, EventFilter: filter}).
		Return(postgres.GetLatestInboxCursorRow{}, pgx.ErrNoRows)
	repo.EXPECT().CountUnreadInbox(gomock.Any(), postgres.CountUnreadInboxParams{UserID: uid, EventFilter: filter}).Return(int64(2), nil)
	repo.EXPECT().CountInboxByCategory(gomock.Any(), postgres.CountInboxByCategoryParams{UserID: uid, EventFilter: filter}).
		Return(postgres.CountInboxByCategoryRow{AllCount: 6, RequestsCount: 3, PersonalCount: 2, ActivityCount: 1, OtherEventsCount: 4}, nil)

	got, err := uc.PollInbox(context.Background(), uid, &eventID, nil)
	require.NoError(t, err)
	assert.Equal(t, inboxModel.Counts{All: 6, Requests: 3, Personal: 2, Activity: 1}, got.Counts)
	assert.Equal(t, int64(4), got.OtherEventsCount)
}

// TestListInbox_CategoryAndResolutionReachDomain: the tab filter reaches SQL
// and the stored classification and resolution are mapped.
func TestListInbox_CategoryAndResolutionReachDomain(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo})
	uid, resolver := tools.NewUUIDv7(), tools.NewUUIDv7()
	resolved := time.Now().UTC()
	repo.EXPECT().ListInAppByUser(gomock.Any(), gomock.Cond(func(arg postgres.ListInAppByUserParams) bool {
		return arg.UserID == uid && arg.CategoryFilter == "requests"
	})).Return([]postgres.ListInAppByUserRow{{
		ID: tools.NewUUIDv7(), UserID: uid, NotificationType: "event.lab.failed", Category: "requests", ActionRequired: true,
		SubjectRef: pgtype.Text{String: "stand:x:y", Valid: true}, ResolvedAt: pgtype.Timestamptz{Time: resolved, Valid: true},
		Resolution: pgtype.Text{String: "fixed", Valid: true}, ResolvedBy: uuid.NullUUID{UUID: resolver, Valid: true}, ResolvedByName: "Іван П.",
	}}, nil)

	got, err := uc.ListInbox(context.Background(), uid, nil, inboxModel.CategoryRequests, nil)
	require.NoError(t, err)
	require.Len(t, got.Items, 1)
	item := got.Items[0]
	assert.Equal(t, inboxModel.CategoryRequests, item.Category)
	assert.True(t, item.ActionRequired)
	assert.False(t, item.IsOpenRequest())
	assert.Equal(t, "fixed", item.Resolution)
	require.NotNil(t, item.ResolvedBy)
	assert.Equal(t, resolver, *item.ResolvedBy)
	assert.Equal(t, "Іван П.", item.ResolvedByName)
}

func TestResolveRequest_ResolvesEverySubjectCopy(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo})
	uid, id := tools.NewUUIDv7(), tools.NewUUIDv7()
	repo.EXPECT().GetInboxItemForUser(gomock.Any(), postgres.GetInboxItemForUserParams{ID: id, UserID: uid}).
		Return(postgres.GetInboxItemForUserRow{ID: id, NotificationType: "event.lab.failed", ActionRequired: true,
			SubjectRef: pgtype.Text{String: "stand:e:t", Valid: true}}, nil)
	repo.EXPECT().ResolveInboxBySubjectRef(gomock.Any(), postgres.ResolveInboxBySubjectRefParams{
		SubjectRef: "stand:e:t", Resolution: "resolved", ResolvedBy: uuid.NullUUID{UUID: uid, Valid: true},
	}).Return(int64(3), nil)

	require.NoError(t, uc.ResolveRequest(context.Background(), uid, id))
}

func TestResolveRequest_WithoutSubjectResolvesOwnCopy(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo})
	uid, id := tools.NewUUIDv7(), tools.NewUUIDv7()
	repo.EXPECT().GetInboxItemForUser(gomock.Any(), gomock.Any()).
		Return(postgres.GetInboxItemForUserRow{ID: id, NotificationType: "event.lab.failed", ActionRequired: true}, nil)
	repo.EXPECT().ResolveInboxItem(gomock.Any(), postgres.ResolveInboxItemParams{
		ID: id, UserID: uid, Resolution: "resolved", ResolvedBy: uuid.NullUUID{UUID: uid, Valid: true},
	}).Return(int64(1), nil)

	require.NoError(t, uc.ResolveRequest(context.Background(), uid, id))
}

func TestResolveRequest_Rejections(t *testing.T) {
	uid, id := tools.NewUUIDv7(), tools.NewUUIDv7()
	cases := []struct {
		name string
		row  postgres.GetInboxItemForUserRow
		err  error
		want error
	}{
		{"not a recipient", postgres.GetInboxItemForUserRow{}, pgx.ErrNoRows, notificationModel.ErrInboxRequestNotFound.Err()},
		{"not a request", postgres.GetInboxItemForUserRow{ID: id, NotificationType: "event.manager.assigned"}, nil, notificationModel.ErrInboxRequestNotResolvable.Err()},
		{"decision-only request", postgres.GetInboxItemForUserRow{ID: id, NotificationType: inboxModel.TypeApplicationSubmitted, ActionRequired: true}, nil, notificationModel.ErrInboxRequestNotResolvable.Err()},
		{"already resolved", postgres.GetInboxItemForUserRow{ID: id, NotificationType: "event.lab.failed", ActionRequired: true,
			ResolvedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}, nil, notificationModel.ErrInboxRequestResolved.Err()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := postgresMocks.NewMockQuerier(ctrl)
			uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo})
			repo.EXPECT().GetInboxItemForUser(gomock.Any(), gomock.Any()).Return(c.row, c.err)
			assert.ErrorIs(t, uc.ResolveRequest(context.Background(), uid, id), c.want)
		})
	}
}
