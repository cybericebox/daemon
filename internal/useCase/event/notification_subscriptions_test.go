package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

func TestUpsertNotificationSubscription_RejectsUnsupportedChannel(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil)
	_, err := uc.UpsertNotificationSubscription(context.Background(), eventID, event.UpsertNotificationSubscriptionInput{
		SignalType: "participant.approval_registration.approved",
		Channel:    "sms",
		Enabled:    true,
		Audience:   []byte(`{"kind":"signal_subject"}`),
	})
	require.Error(t, err)
}

func TestUpsertNotificationSubscription_RejectsNonEventScopedType(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil)
	_, err := uc.UpsertNotificationSubscription(context.Background(), eventID, event.UpsertNotificationSubscriptionInput{
		SignalType: "event.manager.assigned",
		Channel:    "email",
		Enabled:    true,
		Audience:   []byte(`{"kind":"signal_subject"}`),
	})
	require.Error(t, err)
}

func TestListNotificationSubscriptions_ReturnsSourceAndOnlyEventScopedTypes(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil)
	q.EXPECT().ListEffectiveEventSignalNotificationSubscriptions(gomock.Any(), eventID).Return([]postgres.ListEffectiveEventSignalNotificationSubscriptionsRow{
		{SignalType: "event.manager.assigned", Channel: "in_app", Enabled: true, Audience: []byte(`{"kind":"signal_subject"}`), Source: "platform"},
		{SignalType: "participant.approval_registration.approved", Channel: "email", Enabled: true, Audience: []byte(`{"kind":"all_captains"}`), Source: "event"},
		{SignalType: "participant.approval_registration.approved", Channel: "in_app", Enabled: false, Audience: []byte(`{"kind":"signal_subject"}`), Source: "platform"},
	}, nil)

	items, err := uc.ListNotificationSubscriptions(context.Background(), eventID)
	require.NoError(t, err)
	require.Equal(t, []event.NotificationSubscriptionView{
		{SignalType: "participant.approval_registration.approved", Channel: "email", Enabled: true, Audience: []byte(`{"kind":"all_captains"}`), Source: "event"},
		{SignalType: "participant.approval_registration.approved", Channel: "in_app", Enabled: false, Audience: []byte(`{"kind":"signal_subject"}`), Source: "platform"},
	}, items)
}

func TestResetNotificationSubscription_DeletesEventOverride(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil)
	q.EXPECT().DeleteEventSignalNotificationSubscription(gomock.Any(), postgres.DeleteEventSignalNotificationSubscriptionParams{
		ScopeEventID: eventID, SignalType: "participant.approval_registration.approved", Channel: "email",
	}).Return(int64(1), nil)

	require.NoError(t, uc.ResetNotificationSubscription(context.Background(), eventID, "participant.approval_registration.approved", "email"))
}

func TestResetNotificationSubscription_IsIdempotentWithoutOverride(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil)
	q.EXPECT().DeleteEventSignalNotificationSubscription(gomock.Any(), gomock.Any()).Return(int64(0), nil)

	require.NoError(t, uc.ResetNotificationSubscription(context.Background(), eventID, "participant.approval_registration.approved", "in_app"))
}

func TestResetNotificationSubscription_UnknownEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{}, pgx.ErrNoRows)

	err := uc.ResetNotificationSubscription(context.Background(), eventID, "participant.approval_registration.approved", "email")
	require.ErrorIs(t, err, eventModel.ErrEventNotFound.Err())
}

func TestListNotificationSubscriptions_InvitationEmailIsRequired(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil)
	q.EXPECT().ListEffectiveEventSignalNotificationSubscriptions(gomock.Any(), eventID).Return([]postgres.ListEffectiveEventSignalNotificationSubscriptionsRow{
		{SignalType: "participant.invitation.sent", Channel: "email", Enabled: false, Audience: []byte(`{"kind":"signal_subject"}`), Source: "platform"},
		{SignalType: "participant.invitation.sent", Channel: "in_app", Enabled: false, Audience: []byte(`{"kind":"signal_subject"}`), Source: "platform"},
		{SignalType: "participant.invitation.declined", Channel: "email", Enabled: true, Audience: []byte(`{"kind":"signal_subject"}`), Source: "event"},
	}, nil)

	items, err := uc.ListNotificationSubscriptions(context.Background(), eventID)
	require.NoError(t, err)
	require.Equal(t, []event.NotificationSubscriptionView{
		{SignalType: "participant.invitation.sent", Channel: "email", Enabled: true, Audience: []byte(`{"kind":"signal_subject"}`), Source: "platform", Required: true},
	}, items)
}

func TestUpsertNotificationSubscription_RejectsRequiredInvitation(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil)
	_, err := uc.UpsertNotificationSubscription(context.Background(), eventID, event.UpsertNotificationSubscriptionInput{
		SignalType: "participant.team_invitation.sent", Channel: "email", Enabled: false,
		Audience: []byte(`{"kind":"signal_subject"}`),
	})
	require.ErrorIs(t, err, mailModel.ErrRequiredNotification.Err())
}

func TestUpsertNotificationSubscription_ReminderDaysAreValidated(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	for name, tc := range map[string]struct {
		signal, config string
		ok             bool
	}{
		"days in range":      {"participant.event.start_reminder", `{"days_before_start":3}`, true},
		"zero days":          {"participant.event.start_reminder", `{"days_before_start":0}`, false},
		"too many days":      {"participant.event.start_reminder", `{"days_before_start":31}`, false},
		"unknown key":        {"participant.event.start_reminder", `{"hours":3}`, false},
		"options elsewhere":  {"participant.event.finished", `{"days_before_start":3}`, false},
		"empty options kept": {"participant.event.finished", `{}`, true},
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			uc := newUC(q)
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil)
			if tc.ok {
				q.EXPECT().UpsertEventSignalNotificationSubscription(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, p postgres.UpsertEventSignalNotificationSubscriptionParams) (postgres.UpsertEventSignalNotificationSubscriptionRow, error) {
						return postgres.UpsertEventSignalNotificationSubscriptionRow{SignalType: p.SignalType, Channel: p.Channel, Enabled: p.Enabled, Audience: p.Audience, Config: p.Config}, nil
					})
			}
			_, err := uc.UpsertNotificationSubscription(context.Background(), eventID, event.UpsertNotificationSubscriptionInput{
				SignalType: tc.signal, Channel: "email", Enabled: true,
				Audience: []byte(`{"kind":"all_participants"}`), Config: []byte(tc.config),
			})
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
