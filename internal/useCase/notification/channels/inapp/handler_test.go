package inAppUseCase_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/model/notification"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	"github.com/cybericebox/daemon/internal/model/notification/types"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	inAppUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/inapp"
	"github.com/cybericebox/daemon/pkg/tools"
)

func TestInAppHandler_Handle_NoTemplate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := postgresMocks.NewMockQuerier(ctrl)
	user := userModel.User{ID: tools.NewUUIDv7()}
	ctx := context.Background()

	repo.EXPECT().
		GetPublishedInAppTemplate(gomock.Any(), gomock.Any()).
		Return(postgres.NotificationInAppTemplate{}, pgx.ErrNoRows)

	h := inAppUseCase.NewHandler(repo)
	err := h.Handle(ctx, user, notificationTypes.NotificationTypeFlagAccepted, nil, nil)
	require.Error(t, err)
	require.True(
		t,
		notificationModel.ErrTemplateNotFound.Err().Is(err),
		"expected ErrTemplateNotFound, got: %v",
		err,
	)
}

func TestInAppHandler_Handle_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := postgresMocks.NewMockQuerier(ctrl)
	userID := tools.NewUUIDv7()
	eventID := tools.NewUUIDv7()
	user := userModel.User{ID: userID}
	ctx := context.Background()

	repo.EXPECT().
		GetPublishedInAppTemplate(gomock.Any(), gomock.Cond(func(arg postgres.GetPublishedInAppTemplateParams) bool {
			return arg.NotificationType == string(notificationTypes.NotificationTypeFlagAccepted) &&
				arg.ScopeEventID.Valid && arg.ScopeEventID.UUID == eventID
		})).
		Return(
			postgres.NotificationInAppTemplate{
				Title:         "Flag {{.Name}} accepted",
				Body:          "<p>Well done {{.Name}}</p>",
				Link:          "https://example.com/{{.Name}}",
				Icon:          "flag",
				Tone:          "success",
				AccentColor:   "green",
				Surface:       "banner",
				AutoDismissMs: pgtype.Int4{Int32: 5000, Valid: true},
			}, nil,
		)

	repo.EXPECT().
		CreateInApp(gomock.Any(), gomock.Any()).
		DoAndReturn(
			func(_ context.Context, arg postgres.CreateInAppParams) error {
				require.Equal(t, "Flag Alice accepted", arg.Title)
				require.Equal(t, "<p>Well done Alice</p>", arg.Body)
				require.Equal(t, "https://example.com/Alice", arg.Link)
				require.Equal(t, "flag", arg.Icon)
				require.Equal(t, "success", arg.Tone)
				require.Equal(t, "green", arg.AccentColor)
				require.Equal(t, "banner", arg.Surface)
				require.Equal(t, int32(5000), arg.AutoDismissMs.Int32)
				require.True(t, arg.AutoDismissMs.Valid)
				require.Equal(t, userID, arg.UserID)
				require.NotEqual(t, uuid.Nil, arg.ID)
				return nil
			},
		)

	h := inAppUseCase.NewHandler(repo)
	err := h.Handle(ctx, user, notificationTypes.NotificationTypeFlagAccepted, map[string]any{"Name": "Alice"}, nil, &eventID)
	require.NoError(t, err)
}

func TestInAppHandler_Handle_InboxDisplayDuration(t *testing.T) {
	cases := []struct {
		name       string
		configured pgtype.Int4
		expected   int32
	}{
		{name: "default", expected: 5000},
		{name: "minimum", configured: pgtype.Int4{Int32: 0, Valid: true}, expected: 3000},
		{name: "maximum", configured: pgtype.Int4{Int32: 15000, Valid: true}, expected: 10000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := postgresMocks.NewMockQuerier(ctrl)
			user := userModel.User{ID: tools.NewUUIDv7()}
			repo.EXPECT().GetPublishedInAppTemplate(gomock.Any(), gomock.Any()).Return(
				postgres.NotificationInAppTemplate{Title: "New message", Body: "Details", Surface: "inbox", AutoDismissMs: tc.configured}, nil,
			)
			repo.EXPECT().CreateInApp(gomock.Any(), gomock.Cond(func(arg postgres.CreateInAppParams) bool {
				return arg.AutoDismissMs.Valid && arg.AutoDismissMs.Int32 == tc.expected
			})).Return(nil)
			err := inAppUseCase.NewHandler(repo).Handle(context.Background(), user, notificationTypes.NotificationTypeFlagAccepted, nil, nil)
			require.NoError(t, err)
		})
	}
}

// TestInAppHandler_Handle_StoresInboxClassification: the dispatch's inbox meta
// is stored with the row; without one the type is classified for its subject.
func TestInAppHandler_Handle_StoresInboxClassification(t *testing.T) {
	ref := inboxModel.StandRef(tools.NewUUIDv7(), tools.NewUUIDv7())
	cases := []struct {
		name     string
		ctx      context.Context
		typ      notificationTypes.NotificationType
		category string
		action   bool
		ref      string
	}{
		{"planner meta", dispatchModel.WithInboxMeta(context.Background(), new(inboxModel.NewMeta("event.lab.failed", inboxModel.RoleManager, ref))),
			"event.lab.failed", "requests", true, ref},
		{"direct send", context.Background(), notificationTypes.NotificationTypePasswordReset, "personal", false, ""},
		{"activity without meta", context.Background(), "participant.event.finished", "activity", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := postgresMocks.NewMockQuerier(ctrl)
			repo.EXPECT().GetPublishedInAppTemplate(gomock.Any(), gomock.Any()).
				Return(postgres.NotificationInAppTemplate{Title: "T", Surface: "inbox"}, nil)
			repo.EXPECT().CreateInApp(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateInAppParams) error {
				require.Equal(t, string(c.typ), arg.NotificationType)
				require.Equal(t, c.category, arg.Category)
				require.Equal(t, c.action, arg.ActionRequired)
				require.Equal(t, c.ref, arg.SubjectRef.String)
				require.Equal(t, c.ref != "", arg.SubjectRef.Valid)
				return nil
			})
			h := inAppUseCase.NewHandler(repo)
			require.NoError(t, h.Handle(c.ctx, userModel.User{ID: tools.NewUUIDv7()}, c.typ, nil, nil))
		})
	}
}

// The product name never breaks across lines, whatever the stored template says.
func TestInAppHandler_Handle_BrandNameNeverBreaks(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := postgresMocks.NewMockQuerier(ctrl)
	repo.EXPECT().
		GetPublishedInAppTemplate(gomock.Any(), gomock.Any()).
		Return(postgres.NotificationInAppTemplate{Title: "Welcome to Cyber ICE Box", Body: "<p>Cyber ICE Box is ready</p>", Surface: "banner"}, nil)
	repo.EXPECT().
		CreateInApp(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.CreateInAppParams) error {
			require.Equal(t, "Welcome to Cyber ICE Box", arg.Title)
			require.Equal(t, "<p>Cyber ICE Box is ready</p>", arg.Body)
			return nil
		})

	h := inAppUseCase.NewHandler(repo)
	err := h.Handle(context.Background(), userModel.User{ID: tools.NewUUIDv7()}, notificationTypes.NotificationTypeFlagAccepted, nil, nil)
	require.NoError(t, err)
}
