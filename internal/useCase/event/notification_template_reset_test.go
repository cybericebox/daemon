package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

// TestResetEventEmailTemplateType_PublishedOverrideFallsBackToPlatform walks
// the spec §1 flow: a published Event override hides the platform template;
// resetting the type deletes every Event row of it, and the list then shows
// the platform version again.
func TestResetEventEmailTemplateType_PublishedOverrideFallsBackToPlatform(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	m := newTemplateMediaFake()
	uc := newTemplateUC(q, m)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	const typ = "participant.approval_registration.approved"

	platform := emailRow(typ, notificationModel.TemplateStatusPublished, nil, now)
	published := emailRow(typ, notificationModel.TemplateStatusPublished, &eventID, now)
	draft := emailRow(typ, notificationModel.TemplateStatusDraft, &eventID, now)
	eventRows := []postgres.NotificationEmailTemplate{published, draft}

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil).AnyTimes()
	q.EXPECT().ListEmailTemplates(gomock.Any(), gomock.Cond(func(arg postgres.ListEmailTemplatesParams) bool {
		return isScope(&eventID)(arg.ScopeEventID)
	})).DoAndReturn(func(context.Context, postgres.ListEmailTemplatesParams) ([]postgres.NotificationEmailTemplate, error) {
		return eventRows, nil
	}).Times(2)
	q.EXPECT().ListEmailTemplates(gomock.Any(), gomock.Cond(func(arg postgres.ListEmailTemplatesParams) bool {
		return isScope(nil)(arg.ScopeEventID)
	})).Return([]postgres.NotificationEmailTemplate{platform}, nil).Times(2)
	q.EXPECT().DeleteEventEmailTemplatesOfType(gomock.Any(), postgres.DeleteEventEmailTemplatesOfTypeParams{
		ScopeEventID: eventID, NotificationType: typ,
	}).DoAndReturn(func(context.Context, postgres.DeleteEventEmailTemplatesOfTypeParams) ([]uuid.UUID, error) {
		ids := []uuid.UUID{published.ID, draft.ID}
		eventRows = nil
		return ids, nil
	})

	before, err := uc.ListEventEmailTemplates(context.Background(), eventID, emailModel.ListFilter{Type: typ})
	require.NoError(t, err)
	require.Len(t, before, 2)
	require.Equal(t, event.TemplateSourceEvent, before[0].Source)

	require.NoError(t, uc.ResetEventEmailTemplateType(context.Background(), eventID, typ))
	require.ElementsMatch(t, []uuid.UUID{published.ID, draft.ID}, m.removed, "every deleted row drops its image references")

	after, err := uc.ListEventEmailTemplates(context.Background(), eventID, emailModel.ListFilter{Type: typ})
	require.NoError(t, err)
	require.Len(t, after, 1)
	require.Equal(t, event.TemplateSourcePlatform, after[0].Source)
	require.Equal(t, platform.ID, after[0].ID)
}

func TestResetEventEmailTemplateType_NothingToResetIsNoop(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	m := newTemplateMediaFake()
	uc := newTemplateUC(q, m)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil)
	q.EXPECT().DeleteEventEmailTemplatesOfType(gomock.Any(), gomock.Any()).Return([]uuid.UUID{}, nil)

	require.NoError(t, uc.ResetEventEmailTemplateType(context.Background(), eventID, "participant.approval_registration.approved"))
	require.Empty(t, m.removed)
}

func TestResetEventTemplateType_RejectsNonEventScopedType(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl) // no delete query may run
	uc := newTemplateUC(q, newTemplateMediaFake())
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil).Times(2)

	err := uc.ResetEventEmailTemplateType(context.Background(), eventID, "event.manager.assigned")
	require.True(t, notificationModel.ErrTemplateTypeNotEventScoped.Err().Is(err), "email: %v", err)
	err = uc.ResetEventInAppTemplateType(context.Background(), eventID, "event.manager.assigned")
	require.True(t, notificationModel.ErrTemplateTypeNotEventScoped.Err().Is(err), "in-app: %v", err)
}

func TestResetEventTemplateType_UnknownEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newTemplateUC(q, newTemplateMediaFake())
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{}, pgx.ErrNoRows).Times(2)

	err := uc.ResetEventEmailTemplateType(context.Background(), eventID, "participant.approval_registration.approved")
	require.True(t, eventModel.ErrEventNotFound.Err().Is(err), "email: %v", err)
	err = uc.ResetEventInAppTemplateType(context.Background(), eventID, "participant.approval_registration.approved")
	require.True(t, eventModel.ErrEventNotFound.Err().Is(err), "in-app: %v", err)
}

func TestResetEventInAppTemplateType_DeletesEventFamilyIdempotently(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newTemplateUC(q, newTemplateMediaFake())
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	const typ = "participant.approval_registration.approved"

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil).Times(3)
	params := postgres.DeleteEventInAppTemplatesOfTypeParams{ScopeEventID: eventID, NotificationType: typ}
	gomock.InOrder(
		q.EXPECT().DeleteEventInAppTemplatesOfType(gomock.Any(), params).Return(int64(2), nil),
		q.EXPECT().DeleteEventInAppTemplatesOfType(gomock.Any(), params).Return(int64(0), nil),
	)
	q.EXPECT().ListInAppTemplates(gomock.Any(), gomock.Cond(func(arg postgres.ListInAppTemplatesParams) bool {
		return isScope(&eventID)(arg.ScopeEventID)
	})).Return(nil, nil)
	q.EXPECT().ListInAppTemplates(gomock.Any(), gomock.Cond(func(arg postgres.ListInAppTemplatesParams) bool {
		return isScope(nil)(arg.ScopeEventID)
	})).Return([]postgres.NotificationInAppTemplate{inAppRow(typ, notificationModel.TemplateStatusPublished, nil, now)}, nil)

	require.NoError(t, uc.ResetEventInAppTemplateType(context.Background(), eventID, typ))
	require.NoError(t, uc.ResetEventInAppTemplateType(context.Background(), eventID, typ))

	after, err := uc.ListEventInAppTemplates(context.Background(), eventID, inAppModel.ListFilter{Type: typ})
	require.NoError(t, err)
	require.Len(t, after, 1)
	require.Equal(t, event.TemplateSourcePlatform, after[0].Source)
}

func TestResetEventEmailTemplateType_DeleteFailureIsPlatformError(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	m := newTemplateMediaFake()
	uc := newTemplateUC(q, m)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil)
	q.EXPECT().DeleteEventEmailTemplatesOfType(gomock.Any(), gomock.Any()).Return(nil, errors.New("db down"))

	require.Error(t, uc.ResetEventEmailTemplateType(context.Background(), eventID, "participant.approval_registration.approved"))
	require.Empty(t, m.removed)
}
