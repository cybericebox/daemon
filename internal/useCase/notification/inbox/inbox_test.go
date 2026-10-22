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

func TestInboxSince_BaselineDoesNotReplayOldUnread(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo})
	uid, id := tools.NewUUIDv7(), tools.NewUUIDv7()
	created := time.Now().UTC()
	repo.EXPECT().GetLatestInboxCursor(gomock.Any(), postgres.GetLatestInboxCursorParams{UserID: uid}).Return(postgres.GetLatestInboxCursorRow{ID: id, CreatedAt: created}, nil)
	repo.EXPECT().CountUnreadInbox(gomock.Any(), postgres.CountUnreadInboxParams{UserID: uid}).Return(int64(3), nil)
	repo.EXPECT().CountInboxByCategory(gomock.Any(), postgres.CountInboxByCategoryParams{UserID: uid}).Return(postgres.CountInboxByCategoryRow{}, nil)

	result, err := uc.InboxSince(context.Background(), uid, nil, nil)
	require.NoError(t, err)
	require.Empty(t, result.NewInbox)
	require.NotNil(t, result.Cursor)
	assert.Equal(t, id, result.Cursor.ID)
	assert.Equal(t, int64(3), result.UnreadCount)
}

func TestInboxSince_EmptyBaselineHasNoCursor(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo})
	uid := tools.NewUUIDv7()
	repo.EXPECT().GetLatestInboxCursor(gomock.Any(), postgres.GetLatestInboxCursorParams{UserID: uid}).Return(postgres.GetLatestInboxCursorRow{}, pgx.ErrNoRows)
	repo.EXPECT().CountUnreadInbox(gomock.Any(), postgres.CountUnreadInboxParams{UserID: uid}).Return(int64(0), nil)
	repo.EXPECT().CountInboxByCategory(gomock.Any(), postgres.CountInboxByCategoryParams{UserID: uid}).Return(postgres.CountInboxByCategoryRow{}, nil)

	result, err := uc.InboxSince(context.Background(), uid, nil, nil)
	require.NoError(t, err)
	assert.Nil(t, result.Cursor)
	assert.Empty(t, result.NewInbox)
}

func TestInboxSince_DeltaAdvancesCursorAndIncludesReadState(t *testing.T) {
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

	result, err := uc.InboxSince(context.Background(), uid, nil, since)
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

// TestInboxSince_PlatformScopeReachesSQL: the platform-only scope reaches every
// poll query as the nil-uuid filter, which matches only items without an Event.
func TestInboxSince_PlatformScopeReachesSQL(t *testing.T) {
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
	got, err := uc.InboxSince(context.Background(), uid, &scope, nil)
	require.NoError(t, err)
	assert.Nil(t, got.Cursor)
	assert.Equal(t, int64(3), got.UnreadCount)
	require.NoError(t, uc.MarkAllRead(context.Background(), uid, &scope, ""))
}

// TestInboxSince_ReturnsTabCounts: the tab badges and the other-Events count
// come from one query scoped like the poll.
func TestInboxSince_ReturnsTabCounts(t *testing.T) {
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

	got, err := uc.InboxSince(context.Background(), uid, &eventID, nil)
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

// TestInboxPulse_OneQuery: the pulse is one query for the cursor, the unread count and the personal
// banner stamp; an empty scope has no cursor.
func TestInboxPulse_OneQuery(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo})
	uid, id, eventID := tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7()
	created := time.Now().UTC()

	repo.EXPECT().GetInboxPulse(gomock.Any(), postgres.GetInboxPulseParams{UserID: uid, EventFilter: eventID.String()}).
		Return(postgres.GetInboxPulseRow{LatestID: id, LatestCreatedAt: created, UnreadCount: 4, BannerCount: 2, BannerLatestAt: created}, nil)
	got, err := uc.InboxPulse(context.Background(), uid, &eventID)
	require.NoError(t, err)
	require.NotNil(t, got.Cursor)
	assert.Equal(t, id, got.Cursor.ID)
	assert.Equal(t, int64(4), got.Unread)
	assert.Equal(t, int64(2), got.PersonalBanners)
	assert.True(t, got.PersonalBannersLatest.Equal(created))

	repo.EXPECT().GetInboxPulse(gomock.Any(), postgres.GetInboxPulseParams{UserID: uid, EventFilter: uuid.Nil.String()}).
		Return(postgres.GetInboxPulseRow{LatestCreatedAt: time.Unix(0, 0)}, nil)
	platform := inboxModel.PlatformScope
	empty, err := uc.InboxPulse(context.Background(), uid, &platform)
	require.NoError(t, err)
	assert.Nil(t, empty.Cursor)
	assert.Zero(t, empty.Unread)
	assert.Zero(t, empty.PersonalBanners)
}

func elevationRequestRow(uid uuid.UUID, elevationID uuid.UUID) postgres.ListInAppByUserRow {
	return postgres.ListInAppByUserRow{
		ID: tools.NewUUIDv7(), UserID: uid, NotificationType: inboxModel.TypeElevationRequested, Category: "requests", ActionRequired: true,
		SubjectRef: pgtype.Text{String: inboxModel.ElevationRef(elevationID), Valid: true}, CreatedAt: time.Now(),
	}
}

func TestListInbox_ElevationRequestIsResolvableOnlyOnceItsElevationIsGoneOrDecided(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo, Elevations: repo})
	uid := tools.NewUUIDv7()
	gone, decided, pending := tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7()
	repo.EXPECT().ListInAppByUser(gomock.Any(), gomock.Any()).Return([]postgres.ListInAppByUserRow{
		elevationRequestRow(uid, gone), elevationRequestRow(uid, decided), elevationRequestRow(uid, pending),
	}, nil)
	repo.EXPECT().GetExerciseResourceElevation(gomock.Any(), gone).Return(postgres.ExerciseResourceElevation{}, pgx.ErrNoRows)
	repo.EXPECT().GetExerciseResourceElevation(gomock.Any(), decided).Return(postgres.ExerciseResourceElevation{ID: decided, Status: 2}, nil)
	repo.EXPECT().GetExerciseResourceElevation(gomock.Any(), pending).Return(postgres.ExerciseResourceElevation{ID: pending, Status: 0}, nil)

	got, err := uc.ListInbox(context.Background(), uid, nil, inboxModel.CategoryRequests, nil)
	require.NoError(t, err)
	require.Len(t, got.Items, 3)
	assert.True(t, got.Items[0].Resolvable(), "the elevation row is gone (task deleted)")
	assert.True(t, got.Items[1].Resolvable(), "the elevation was decided elsewhere")
	assert.False(t, got.Items[2].Resolvable(), "a pending elevation is decided on its page")
}

func TestResolveRequest_OrphanedElevationRequestCanBeClosedByHand(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo, Elevations: repo})
	uid, id, elevationID := tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7()
	ref := inboxModel.ElevationRef(elevationID)
	repo.EXPECT().GetInboxItemForUser(gomock.Any(), gomock.Any()).Return(postgres.GetInboxItemForUserRow{
		ID: id, NotificationType: inboxModel.TypeElevationRequested, ActionRequired: true, SubjectRef: pgtype.Text{String: ref, Valid: true},
	}, nil)
	repo.EXPECT().GetExerciseResourceElevation(gomock.Any(), elevationID).Return(postgres.ExerciseResourceElevation{}, pgx.ErrNoRows)
	repo.EXPECT().ResolveInboxBySubjectRef(gomock.Any(), postgres.ResolveInboxBySubjectRefParams{
		SubjectRef: ref, Resolution: "resolved", ResolvedBy: uuid.NullUUID{UUID: uid, Valid: true},
	}).Return(int64(2), nil)

	require.NoError(t, uc.ResolveRequest(context.Background(), uid, id))
}

func TestResolveRequest_PendingElevationRequestIsNotClosedByHand(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := inboxUseCase.NewNotificationInboxUseCase(inboxUseCase.Dependencies{Repo: repo, Elevations: repo})
	uid, id, elevationID := tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7()
	repo.EXPECT().GetInboxItemForUser(gomock.Any(), gomock.Any()).Return(postgres.GetInboxItemForUserRow{
		ID: id, NotificationType: inboxModel.TypeElevationRequested, ActionRequired: true,
		SubjectRef: pgtype.Text{String: inboxModel.ElevationRef(elevationID), Valid: true},
	}, nil)
	repo.EXPECT().GetExerciseResourceElevation(gomock.Any(), elevationID).Return(postgres.ExerciseResourceElevation{ID: elevationID, Status: 0}, nil)

	assert.ErrorIs(t, uc.ResolveRequest(context.Background(), uid, id), notificationModel.ErrInboxRequestNotResolvable.Err())
}
