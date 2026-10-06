package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

func TestCreateEventEmailTemplate_BindsScopeToEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().CreateEmailTemplate(gomock.Any(), gomock.Cond(func(arg postgres.CreateEmailTemplateParams) bool {
		return arg.ScopeEventID.Valid && arg.ScopeEventID.UUID == eventID &&
			arg.NotificationType == "participant.approval_registration.approved" && arg.Status == "draft"
	})).Return(postgres.NotificationEmailTemplate{
		ID:               uuid.Must(uuid.NewV7()),
		ScopeEventID:     uuid.NullUUID{UUID: eventID, Valid: true},
		NotificationType: "participant.approval_registration.approved",
		Status:           string(notificationModel.TemplateStatusDraft),
		Body:             []byte("[]"),
		Styling:          []byte("{}"),
		CreatedAt:        now,
		UpdatedAt:        now,
	}, nil)

	got, err := uc.CreateEventEmailTemplate(context.Background(), eventID, emailModel.CreateTemplateInput{
		NotificationType: "participant.approval_registration.approved", Subject: "Welcome", Body: []byte("[]"), Styling: []byte("{}"),
	})
	require.NoError(t, err)
	require.NotNil(t, got.ScopeEventID)
	require.Equal(t, eventID, *got.ScopeEventID)
}

func TestCreateEventEmailTemplate_RejectsVariableOutsideSignalContract(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)

	_, err := uc.CreateEventEmailTemplate(context.Background(), eventID, emailModel.CreateTemplateInput{
		NotificationType: "participant.approval_registration.approved",
		Subject:          "Welcome, {{.not_available}}",
		Body:             []byte("[]"),
		Styling:          []byte("{}"),
	})
	require.Error(t, err)
	require.True(t, notificationModel.ErrInvalidTemplateVariables.Err().Is(err))
}

func TestCreateEventEmailTemplate_RejectsNonEventScopedType(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)

	_, err := uc.CreateEventEmailTemplate(context.Background(), eventID, emailModel.CreateTemplateInput{
		NotificationType: "event.manager.assigned",
		Subject:          "Welcome",
		Body:             []byte("[]"),
		Styling:          []byte("{}"),
	})
	require.Error(t, err)
	require.True(t, notificationModel.ErrTemplateTypeNotEventScoped.Err().Is(err))
}

func TestCreateEventInAppTemplate_RejectsNonEventScopedType(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)

	_, err := uc.CreateEventInAppTemplate(context.Background(), eventID, inAppModel.CreateTemplateInput{
		NotificationType: "event.manager.assigned",
		Title:            "Welcome",
		Body:             "Welcome",
	})
	require.Error(t, err)
	require.True(t, notificationModel.ErrTemplateTypeNotEventScoped.Err().Is(err))
}

func emailRow(typ string, status notificationModel.TemplateStatus, scope *uuid.UUID, now time.Time) postgres.NotificationEmailTemplate {
	row := postgres.NotificationEmailTemplate{
		ID: uuid.Must(uuid.NewV7()), NotificationType: typ, Status: string(status),
		Subject: "Subject " + typ, Preheader: "Pre " + typ,
		Body: []byte(`[{"type":"text"}]`), Styling: []byte(`{"bg":"#fff"}`),
		CreatedAt: now, UpdatedAt: now,
	}
	if scope != nil {
		row.ScopeEventID = uuid.NullUUID{UUID: *scope, Valid: true}
	}
	if status == notificationModel.TemplateStatusPublished {
		row.PublishedAt = pgtype.Timestamptz{Time: now, Valid: true}
	}
	return row
}

func inAppRow(typ string, status notificationModel.TemplateStatus, scope *uuid.UUID, now time.Time) postgres.NotificationInAppTemplate {
	row := postgres.NotificationInAppTemplate{
		ID: uuid.Must(uuid.NewV7()), NotificationType: typ, Status: string(status),
		Title: "Title " + typ, Body: "Body " + typ, Link: "/x", Icon: "bell", Tone: "info",
		Actions: []byte("[]"), Dismissible: true, CreatedAt: now, UpdatedAt: now,
	}
	if scope != nil {
		row.ScopeEventID = uuid.NullUUID{UUID: *scope, Valid: true}
	}
	return row
}

func isScope(want *uuid.UUID) func(uuid.NullUUID) bool {
	return func(got uuid.NullUUID) bool {
		if want == nil {
			return !got.Valid
		}
		return got.Valid && got.UUID == *want
	}
}

func TestListEventEmailTemplates_InheritsPlatformForEveryEventScopedType(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	platform := []postgres.NotificationEmailTemplate{
		emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, nil, now),
		emailRow("participant.invitation.sent", notificationModel.TemplateStatusPublished, nil, now),
		// platform-only type must never reach an Event list
		emailRow("event.manager.assigned", notificationModel.TemplateStatusPublished, nil, now),
	}
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().ListEmailTemplates(gomock.Any(), gomock.Cond(func(arg postgres.ListEmailTemplatesParams) bool {
		return isScope(&eventID)(arg.ScopeEventID)
	})).Return(nil, nil)
	q.EXPECT().ListEmailTemplates(gomock.Any(), gomock.Cond(func(arg postgres.ListEmailTemplatesParams) bool {
		return isScope(nil)(arg.ScopeEventID) && arg.StatusFilter == string(notificationModel.TemplateStatusPublished)
	})).Return(platform, nil)

	got, err := uc.ListEventEmailTemplates(context.Background(), eventID, emailModel.ListFilter{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	types := map[string]string{}
	for _, item := range got {
		types[item.NotificationType] = item.Source
		require.Equal(t, event.TemplateSourcePlatform, item.Source)
		require.Nil(t, item.ScopeEventID)
	}
	require.Contains(t, types, "participant.approval_registration.approved")
	require.Contains(t, types, "participant.invitation.sent")
}

func TestListEventEmailTemplates_EventRowsOverrideTheirType(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	eventDraft := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, &eventID, now)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().ListEmailTemplates(gomock.Any(), gomock.Cond(func(arg postgres.ListEmailTemplatesParams) bool {
		return isScope(&eventID)(arg.ScopeEventID)
	})).Return([]postgres.NotificationEmailTemplate{eventDraft}, nil)
	q.EXPECT().ListEmailTemplates(gomock.Any(), gomock.Cond(func(arg postgres.ListEmailTemplatesParams) bool {
		return isScope(nil)(arg.ScopeEventID)
	})).Return([]postgres.NotificationEmailTemplate{
		emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, nil, now),
		emailRow("participant.invitation.sent", notificationModel.TemplateStatusPublished, nil, now),
	}, nil)

	got, err := uc.ListEventEmailTemplates(context.Background(), eventID, emailModel.ListFilter{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	byType := map[string]event.EventEmailTemplateView{}
	for _, item := range got {
		byType[item.NotificationType] = item
	}
	require.Equal(t, event.TemplateSourceEvent, byType["participant.approval_registration.approved"].Source)
	require.Equal(t, eventDraft.ID, byType["participant.approval_registration.approved"].ID)
	require.Equal(t, event.TemplateSourcePlatform, byType["participant.invitation.sent"].Source)
}

func TestListEventEmailTemplates_StatusFilterAppliesToEventRowsOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	// participant.enrolled is overridden (published Event row) — a draft
	// filter must not make it fall back to the platform version.
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().ListEmailTemplates(gomock.Any(), gomock.Cond(func(arg postgres.ListEmailTemplatesParams) bool {
		return isScope(&eventID)(arg.ScopeEventID)
	})).Return([]postgres.NotificationEmailTemplate{
		emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, &eventID, now),
	}, nil)
	q.EXPECT().ListEmailTemplates(gomock.Any(), gomock.Cond(func(arg postgres.ListEmailTemplatesParams) bool {
		return isScope(nil)(arg.ScopeEventID)
	})).Return([]postgres.NotificationEmailTemplate{
		emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, nil, now),
		emailRow("participant.invitation.sent", notificationModel.TemplateStatusPublished, nil, now),
	}, nil)

	got, err := uc.ListEventEmailTemplates(context.Background(), eventID, emailModel.ListFilter{Status: "draft"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "participant.invitation.sent", got[0].NotificationType)
	require.Equal(t, event.TemplateSourcePlatform, got[0].Source)
}

func TestListEventInAppTemplates_InheritsAndOverrides(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	eventDraft := inAppRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, &eventID, now)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().ListInAppTemplates(gomock.Any(), gomock.Cond(func(arg postgres.ListInAppTemplatesParams) bool {
		return isScope(&eventID)(arg.ScopeEventID)
	})).Return([]postgres.NotificationInAppTemplate{eventDraft}, nil)
	q.EXPECT().ListInAppTemplates(gomock.Any(), gomock.Cond(func(arg postgres.ListInAppTemplatesParams) bool {
		return isScope(nil)(arg.ScopeEventID) && arg.StatusFilter == string(notificationModel.TemplateStatusPublished)
	})).Return([]postgres.NotificationInAppTemplate{
		inAppRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, nil, now),
		inAppRow("participant.invitation.sent", notificationModel.TemplateStatusPublished, nil, now),
		inAppRow("event.manager.assigned", notificationModel.TemplateStatusPublished, nil, now),
	}, nil)

	got, err := uc.ListEventInAppTemplates(context.Background(), eventID, inAppModel.ListFilter{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	byType := map[string]event.EventInAppTemplateView{}
	for _, item := range got {
		byType[item.NotificationType] = item
	}
	require.Equal(t, event.TemplateSourceEvent, byType["participant.approval_registration.approved"].Source)
	require.Equal(t, eventDraft.ID, byType["participant.approval_registration.approved"].ID)
	require.Equal(t, event.TemplateSourcePlatform, byType["participant.invitation.sent"].Source)
}

func TestGetEventEmailTemplate_ReturnsPlatformPublishedOfEventScopedType(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	row := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, nil, now)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEmailTemplate(gomock.Any(), row.ID).Return(row, nil)

	got, err := uc.GetEventEmailTemplate(context.Background(), eventID, row.ID)
	require.NoError(t, err)
	require.Equal(t, row.ID, got.ID)
	require.Equal(t, event.TemplateSourcePlatform, got.Source)
}

func TestGetEventEmailTemplate_ReturnsOwnEventRow(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	row := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, &eventID, now)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEmailTemplate(gomock.Any(), row.ID).Return(row, nil)

	got, err := uc.GetEventEmailTemplate(context.Background(), eventID, row.ID)
	require.NoError(t, err)
	require.Equal(t, event.TemplateSourceEvent, got.Source)
}

func TestGetEventEmailTemplate_RejectsForeignAndPlatformOnlyRows(t *testing.T) {
	now := time.Now().UTC()
	eventID := uuid.Must(uuid.NewV7())
	otherEvent := uuid.Must(uuid.NewV7())
	cases := map[string]postgres.NotificationEmailTemplate{
		"other event row":      emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, &otherEvent, now),
		"platform-only type":   emailRow("event.manager.assigned", notificationModel.TemplateStatusPublished, nil, now),
		"platform draft":       emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, nil, now),
		"platform unpublished": emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusUnpublished, nil, now),
	}
	for name, row := range cases {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			uc := newUC(q)
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
			q.EXPECT().GetEmailTemplate(gomock.Any(), row.ID).Return(row, nil)

			_, err := uc.GetEventEmailTemplate(context.Background(), eventID, row.ID)
			require.Error(t, err)
			require.True(t, notificationModel.ErrTemplateNotFound.Err().Is(err))
		})
	}
}

func TestGetEventInAppTemplate_ScopeRules(t *testing.T) {
	now := time.Now().UTC()
	eventID := uuid.Must(uuid.NewV7())
	otherEvent := uuid.Must(uuid.NewV7())
	cases := []struct {
		name   string
		row    postgres.NotificationInAppTemplate
		source string
	}{
		{"platform published", inAppRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, nil, now), event.TemplateSourcePlatform},
		{"own event row", inAppRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, &eventID, now), event.TemplateSourceEvent},
		{"other event row", inAppRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, &otherEvent, now), ""},
		{"platform-only type", inAppRow("event.manager.assigned", notificationModel.TemplateStatusPublished, nil, now), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			uc := newUC(q)
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
			q.EXPECT().GetInAppTemplate(gomock.Any(), tc.row.ID).Return(tc.row, nil)

			got, err := uc.GetEventInAppTemplate(context.Background(), eventID, tc.row.ID)
			if tc.source == "" {
				require.True(t, notificationModel.ErrTemplateNotFound.Err().Is(err))
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.source, got.Source)
		})
	}
}

func TestEventEmailTemplateMutations_RejectPlatformRows(t *testing.T) {
	now := time.Now().UTC()
	eventID := uuid.Must(uuid.NewV7())
	actor := uuid.Must(uuid.NewV7())
	row := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, nil, now)
	calls := map[string]func(uc *event.EventUseCase) error{
		"update": func(uc *event.EventUseCase) error {
			_, err := uc.UpdateEventEmailTemplate(context.Background(), eventID, emailModel.UpdateTemplateInput{ID: row.ID, Subject: "x", Body: []byte("[]"), Styling: []byte("{}")})
			return err
		},
		"delete": func(uc *event.EventUseCase) error {
			return uc.DeleteEventEmailTemplate(context.Background(), eventID, row.ID)
		},
		"publish": func(uc *event.EventUseCase) error {
			_, err := uc.PublishEventEmailTemplate(context.Background(), eventID, row.ID, actor)
			return err
		},
		"rollback": func(uc *event.EventUseCase) error {
			_, err := uc.RollbackEventEmailTemplate(context.Background(), eventID, row.ID, actor)
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			uc := newUC(q)
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
			q.EXPECT().GetEmailTemplate(gomock.Any(), row.ID).Return(row, nil)
			// no write query expectations: any write call fails the test

			err := call(uc)
			require.Error(t, err)
			require.True(t, notificationModel.ErrTemplateNotFound.Err().Is(err))
		})
	}
}

func TestEventInAppTemplateMutations_RejectPlatformRows(t *testing.T) {
	now := time.Now().UTC()
	eventID := uuid.Must(uuid.NewV7())
	actor := uuid.Must(uuid.NewV7())
	row := inAppRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, nil, now)
	calls := map[string]func(uc *event.EventUseCase) error{
		"update": func(uc *event.EventUseCase) error {
			_, err := uc.UpdateEventInAppTemplate(context.Background(), eventID, inAppModel.UpdateTemplateInput{ID: row.ID, Title: "x", Body: "y"})
			return err
		},
		"delete": func(uc *event.EventUseCase) error {
			return uc.DeleteEventInAppTemplate(context.Background(), eventID, row.ID)
		},
		"publish": func(uc *event.EventUseCase) error {
			_, err := uc.PublishEventInAppTemplate(context.Background(), eventID, row.ID, actor)
			return err
		},
		"rollback": func(uc *event.EventUseCase) error {
			_, err := uc.RollbackEventInAppTemplate(context.Background(), eventID, row.ID, actor)
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			uc := newUC(q)
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
			q.EXPECT().GetInAppTemplate(gomock.Any(), row.ID).Return(row, nil)

			err := call(uc)
			require.Error(t, err)
			require.True(t, notificationModel.ErrTemplateNotFound.Err().Is(err))
		})
	}
}

func TestCustomizeEventEmailTemplate_CopiesPlatformPublishedIntoEventDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	actor := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	platform := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, nil, now)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEmailTemplate(gomock.Any(), platform.ID).Return(platform, nil)
	expectNoEmailOverride(q, eventID, platform.NotificationType)
	var written postgres.CreateEmailTemplateParams
	q.EXPECT().CreateEmailTemplate(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateEmailTemplateParams) (postgres.NotificationEmailTemplate, error) {
			written = arg
			return postgres.NotificationEmailTemplate{
				ID: arg.ID, NotificationType: arg.NotificationType, Status: arg.Status, Subject: arg.Subject,
				Preheader: arg.Preheader, Body: arg.Body, Styling: arg.Styling, ScopeEventID: arg.ScopeEventID,
				UpdatedByUserID: arg.UpdatedByUserID, CreatedAt: now, UpdatedAt: now,
			}, nil
		})

	got, err := uc.CustomizeEventEmailTemplate(context.Background(), eventID, platform.ID, actor)
	require.NoError(t, err)
	require.NotEqual(t, platform.ID, written.ID)
	require.Equal(t, string(notificationModel.TemplateStatusDraft), written.Status)
	require.Equal(t, platform.NotificationType, written.NotificationType)
	require.Equal(t, platform.Subject, written.Subject)
	require.Equal(t, platform.Preheader, written.Preheader)
	require.JSONEq(t, string(platform.Body), string(written.Body))
	require.JSONEq(t, string(platform.Styling), string(written.Styling))
	require.True(t, isScope(&eventID)(written.ScopeEventID))
	require.True(t, written.UpdatedByUserID.Valid && written.UpdatedByUserID.UUID == actor)
	require.NotNil(t, got.ScopeEventID)
	require.Equal(t, eventID, *got.ScopeEventID)
}

func TestCustomizeEventEmailTemplate_RejectsNonPlatformPublishedSources(t *testing.T) {
	now := time.Now().UTC()
	eventID := uuid.Must(uuid.NewV7())
	cases := map[string]postgres.NotificationEmailTemplate{
		"event row":          emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, &eventID, now),
		"platform draft":     emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, nil, now),
		"platform-only type": emailRow("event.manager.assigned", notificationModel.TemplateStatusPublished, nil, now),
	}
	for name, row := range cases {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			uc := newUC(q)
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
			q.EXPECT().GetEmailTemplate(gomock.Any(), row.ID).Return(row, nil)

			_, err := uc.CustomizeEventEmailTemplate(context.Background(), eventID, row.ID, uuid.Must(uuid.NewV7()))
			require.True(t, notificationModel.ErrTemplateNotFound.Err().Is(err))
		})
	}
}

func TestCustomizeEventEmailTemplate_SecondDraftSurfacesWriteError(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	platform := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, nil, now)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEmailTemplate(gomock.Any(), platform.ID).Return(platform, nil)
	expectNoEmailOverride(q, eventID, platform.NotificationType)
	q.EXPECT().CreateEmailTemplate(gomock.Any(), gomock.Any()).Return(postgres.NotificationEmailTemplate{},
		&pgconn.PgError{Code: pgerrcode.UniqueViolation, Detail: "Key (notification_type, scope_event_id)=(participant.approval_registration.approved, x) already exists."})

	_, err := uc.CustomizeEventEmailTemplate(context.Background(), eventID, platform.ID, uuid.Must(uuid.NewV7()))
	require.Error(t, err)
	require.False(t, notificationModel.ErrTemplateNotFound.Err().Is(err))
	// classifyTemplateWriteError maps the one-draft-per-(type, scope) unique
	// index violation (concurrent customize/create) to a 409.
	require.True(t, notificationModel.ErrTemplateDraftExists.Err().Is(err))
}

func TestCustomizeEventInAppTemplate_CopiesPlatformPublishedIntoEventDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	actor := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	platform := inAppRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, nil, now)
	platform.AccentColor, platform.Surface = "#211A52", "card"
	platform.AutoDismissMs = pgtype.Int4{Int32: 5000, Valid: true}

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetInAppTemplate(gomock.Any(), platform.ID).Return(platform, nil)
	expectNoInAppOverride(q, eventID, platform.NotificationType)
	var written postgres.CreateInAppTemplateParams
	q.EXPECT().CreateInAppTemplate(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateInAppTemplateParams) (postgres.NotificationInAppTemplate, error) {
			written = arg
			return postgres.NotificationInAppTemplate{
				ID: arg.ID, NotificationType: arg.NotificationType, Status: arg.Status, ScopeEventID: arg.ScopeEventID,
				CreatedAt: now, UpdatedAt: now,
			}, nil
		})

	got, err := uc.CustomizeEventInAppTemplate(context.Background(), eventID, platform.ID, actor)
	require.NoError(t, err)
	require.NotEqual(t, platform.ID, written.ID)
	require.Equal(t, string(notificationModel.TemplateStatusDraft), written.Status)
	require.Equal(t, platform.Title, written.Title)
	require.Equal(t, platform.Body, written.Body)
	require.Equal(t, platform.Link, written.Link)
	require.Equal(t, platform.Icon, written.Icon)
	require.Equal(t, platform.Tone, written.Tone)
	require.Equal(t, platform.AccentColor, written.AccentColor)
	require.Equal(t, platform.Surface, written.Surface)
	require.Equal(t, platform.AutoDismissMs, written.AutoDismissMs)
	require.Equal(t, platform.Dismissible, written.Dismissible)
	require.True(t, isScope(&eventID)(written.ScopeEventID))
	require.NotNil(t, got.ScopeEventID)
}

func expectNoEmailOverride(q *postgresMocks.MockQuerier, eventID uuid.UUID, typ string) {
	q.EXPECT().ListEmailTemplates(gomock.Any(), gomock.Cond(func(arg postgres.ListEmailTemplatesParams) bool {
		return isScope(&eventID)(arg.ScopeEventID) && arg.TypeFilter == typ && arg.StatusFilter == ""
	})).Return(nil, nil)
}

func expectNoInAppOverride(q *postgresMocks.MockQuerier, eventID uuid.UUID, typ string) {
	q.EXPECT().ListInAppTemplates(gomock.Any(), gomock.Cond(func(arg postgres.ListInAppTemplatesParams) bool {
		return isScope(&eventID)(arg.ScopeEventID) && arg.TypeFilter == typ && arg.StatusFilter == ""
	})).Return(nil, nil)
}

func uniqueDraftViolation() error {
	return &pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "uq_email_tpl_draft",
		Detail: "Key (notification_type, scope_event_id)=(participant.approval_registration.approved, x) already exists."}
}

func TestCustomizeEventEmailTemplate_RejectsTypeAlreadyOverridden(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	platform := emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, nil, now)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEmailTemplate(gomock.Any(), platform.ID).Return(platform, nil)
	// The Event already has a published override (no draft): customize must
	// not start a new draft from platform content. No CreateEmailTemplate.
	q.EXPECT().ListEmailTemplates(gomock.Any(), gomock.Any()).Return([]postgres.NotificationEmailTemplate{
		emailRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, &eventID, now),
	}, nil)

	_, err := uc.CustomizeEventEmailTemplate(context.Background(), eventID, platform.ID, uuid.Must(uuid.NewV7()))
	require.Error(t, err)
	require.True(t, notificationModel.ErrTemplateDraftExists.Err().Is(err))
}

func TestCustomizeEventInAppTemplate_RejectsTypeAlreadyOverridden(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	platform := inAppRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, nil, now)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetInAppTemplate(gomock.Any(), platform.ID).Return(platform, nil)
	q.EXPECT().ListInAppTemplates(gomock.Any(), gomock.Any()).Return([]postgres.NotificationInAppTemplate{
		inAppRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, &eventID, now),
	}, nil)

	_, err := uc.CustomizeEventInAppTemplate(context.Background(), eventID, platform.ID, uuid.Must(uuid.NewV7()))
	require.Error(t, err)
	require.True(t, notificationModel.ErrTemplateDraftExists.Err().Is(err))
}

func TestCustomizeEventInAppTemplate_RejectsNonPlatformPublishedSources(t *testing.T) {
	now := time.Now().UTC()
	eventID := uuid.Must(uuid.NewV7())
	cases := map[string]postgres.NotificationInAppTemplate{
		"event row":          inAppRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, &eventID, now),
		"platform draft":     inAppRow("participant.approval_registration.approved", notificationModel.TemplateStatusDraft, nil, now),
		"platform-only type": inAppRow("event.manager.assigned", notificationModel.TemplateStatusPublished, nil, now),
	}
	for name, row := range cases {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			uc := newUC(q)
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
			q.EXPECT().GetInAppTemplate(gomock.Any(), row.ID).Return(row, nil)

			_, err := uc.CustomizeEventInAppTemplate(context.Background(), eventID, row.ID, uuid.Must(uuid.NewV7()))
			require.True(t, notificationModel.ErrTemplateNotFound.Err().Is(err))
		})
	}
}

func TestCustomizeEventInAppTemplate_ConcurrentDraftIsConflict(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	platform := inAppRow("participant.approval_registration.approved", notificationModel.TemplateStatusPublished, nil, now)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetInAppTemplate(gomock.Any(), platform.ID).Return(platform, nil)
	expectNoInAppOverride(q, eventID, platform.NotificationType)
	q.EXPECT().CreateInAppTemplate(gomock.Any(), gomock.Any()).Return(postgres.NotificationInAppTemplate{}, uniqueDraftViolation())

	_, err := uc.CustomizeEventInAppTemplate(context.Background(), eventID, platform.ID, uuid.Must(uuid.NewV7()))
	require.Error(t, err)
	require.True(t, notificationModel.ErrTemplateDraftExists.Err().Is(err))
}

func TestCreateEventEmailTemplate_SecondDraftIsConflict(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().CreateEmailTemplate(gomock.Any(), gomock.Any()).Return(postgres.NotificationEmailTemplate{}, uniqueDraftViolation())

	_, err := uc.CreateEventEmailTemplate(context.Background(), eventID, emailModel.CreateTemplateInput{
		NotificationType: "participant.approval_registration.approved", Subject: "Welcome", Body: []byte("[]"), Styling: []byte("{}"),
	})
	require.Error(t, err)
	require.True(t, notificationModel.ErrTemplateDraftExists.Err().Is(err))
}

func TestCreateEventInAppTemplate_SecondDraftIsConflict(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().CreateInAppTemplate(gomock.Any(), gomock.Any()).Return(postgres.NotificationInAppTemplate{}, uniqueDraftViolation())

	_, err := uc.CreateEventInAppTemplate(context.Background(), eventID, inAppModel.CreateTemplateInput{
		NotificationType: "participant.approval_registration.approved", Title: "Welcome", Body: "Welcome",
	})
	require.Error(t, err)
	require.True(t, notificationModel.ErrTemplateDraftExists.Err().Is(err))
}

func TestCreateEventEmailTemplate_RejectsUnknownThemeToken(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)

	_, err := uc.CreateEventEmailTemplate(context.Background(), eventID, emailModel.CreateTemplateInput{
		NotificationType: "participant.approval_registration.approved",
		Subject:          "Welcome",
		Body:             []byte("[]"),
		Styling:          []byte(`{"heading_color":"theme:nope"}`),
	})
	require.Error(t, err)
	require.True(t, notificationModel.ErrInvalidTemplateStyling.Err().Is(err))
}
