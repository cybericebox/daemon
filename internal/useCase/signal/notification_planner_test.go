package signalUseCase

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
)

type plannerQueries struct {
	rows       []postgres.ListEffectiveEnabledSignalNotificationSubscriptionsRow
	asked      *postgres.ListEffectiveEnabledSignalNotificationSubscriptionsParams
	recipients []postgres.ListEventFormRecipientCandidatesRow
	users      map[uuid.UUID]postgres.User
	managers   []uuid.UUID
}

func (q plannerQueries) ListEventManagerUserIDs(_ context.Context, _ uuid.UUID) ([]uuid.UUID, error) {
	return q.managers, nil
}

func (q plannerQueries) ListEventFormRecipientCandidates(_ context.Context, _ uuid.UUID) ([]postgres.ListEventFormRecipientCandidatesRow, error) {
	return q.recipients, nil
}

func (q plannerQueries) ListEffectiveEnabledSignalNotificationSubscriptions(_ context.Context, arg postgres.ListEffectiveEnabledSignalNotificationSubscriptionsParams) ([]postgres.ListEffectiveEnabledSignalNotificationSubscriptionsRow, error) {
	if q.asked != nil {
		*q.asked = arg
	}
	return q.rows, nil
}

func (q plannerQueries) GetUserByID(_ context.Context, id uuid.UUID) (postgres.User, error) {
	return q.users[id], nil
}

type plannerDispatch struct {
	userID  uuid.UUID
	payload notificationTypes.NotificationPayload
	options dispatchModel.NotifyOptions
	all     []uuid.UUID
	byUser  map[uuid.UUID]dispatchModel.NotifyOptions
}

func (d *plannerDispatch) Notify(_ context.Context, userID uuid.UUID, payload notificationTypes.NotificationPayload, opts ...dispatchModel.NotifyOption) error {
	d.userID, d.payload, d.options = userID, payload, dispatchModel.ApplyNotifyOptions(opts)
	d.all = append(d.all, userID)
	if d.byUser == nil {
		d.byUser = map[uuid.UUID]dispatchModel.NotifyOptions{}
	}
	d.byUser[userID] = d.options
	return nil
}

func TestNotificationPlanner_DispatchesEachParticipantAudienceIndividually(t *testing.T) {
	eventID, subjectID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	firstID, secondID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	raw, err := json.Marshal(signalModel.ParticipantPayload{ScopeEventID: eventID, SubjectUserID: subjectID, EventName: "Olympiad"})
	require.NoError(t, err)
	dispatch := &plannerDispatch{}
	planner := NewNotificationPlanner(plannerQueries{
		rows:       []postgres.ListEffectiveEnabledSignalNotificationSubscriptionsRow{{Channel: "in_app", Audience: []byte(`{"kind":"all_participants"}`)}},
		recipients: []postgres.ListEventFormRecipientCandidatesRow{{UserID: firstID}, {UserID: secondID}},
	}, signalModel.DefaultRegistry, dispatch)

	err = planner.Handle(context.Background(), signalModel.Signal{ID: uuid.Must(uuid.NewV7()), Type: signalModel.TypeParticipantApprovalRegistrationApproved, OccurredAt: time.Now(), Payload: raw})
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{firstID, secondID}, dispatch.all)
}

func TestNotificationPlanner_DispatchesSubjectWithEventScope(t *testing.T) {
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	payload := signalModel.ParticipantPayload{ScopeEventID: eventID, SubjectUserID: userID, EventName: "Olympiad"}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	dispatch := &plannerDispatch{}
	planner := NewNotificationPlanner(plannerQueries{rows: []postgres.ListEffectiveEnabledSignalNotificationSubscriptionsRow{
		{Channel: "email", Audience: []byte(`{"kind":"signal_subject"}`)},
		{Channel: "in_app", Audience: []byte(`{"kind":"signal_subject"}`)},
	}}, signalModel.DefaultRegistry, dispatch)

	err = planner.Handle(context.Background(), signalModel.Signal{
		ID: uuid.Must(uuid.NewV7()), Type: signalModel.TypeParticipantApprovalRegistrationApproved,
		OccurredAt: time.Now(), Payload: raw,
	})
	require.NoError(t, err)
	require.Equal(t, userID, dispatch.userID)
	require.NotNil(t, dispatch.options.ScopeEventID)
	require.Equal(t, eventID, *dispatch.options.ScopeEventID)
	require.ElementsMatch(t, []notificationTypes.NotificationChannel{"email", "in_app"}, dispatch.options.OverrideChannels)
	require.Equal(t, notificationTypes.NotificationType(signalModel.TypeParticipantApprovalRegistrationApproved), dispatch.payload.NotificationType())
}

func TestNotificationPlanner_FillsRecipientProfileIntoReadyPayload(t *testing.T) {
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	raw, err := json.Marshal(signalModel.ParticipantPayload{ScopeEventID: eventID, SubjectUserID: userID, EventName: "Olympiad"})
	require.NoError(t, err)
	dispatch := &plannerDispatch{}
	planner := NewNotificationPlanner(plannerQueries{
		rows:  []postgres.ListEffectiveEnabledSignalNotificationSubscriptionsRow{{Channel: "email", Audience: []byte(`{"kind":"signal_subject"}`)}},
		users: map[uuid.UUID]postgres.User{userID: {ID: userID, Email: "jane@example.test", FirstName: "Jane", LastName: "Doe"}},
	}, signalModel.DefaultRegistry, dispatch)

	err = planner.Handle(context.Background(), signalModel.Signal{ID: uuid.Must(uuid.NewV7()), Type: signalModel.TypeParticipantApprovalRegistrationApproved, OccurredAt: time.Now(), Payload: raw})
	require.NoError(t, err)
	encoded, err := dispatch.payload.Marshal()
	require.NoError(t, err)
	var vars map[string]any
	require.NoError(t, json.Unmarshal(encoded, &vars))
	require.Equal(t, "Jane", vars["user_first_name"])
	require.Equal(t, "Jane Doe", vars["user_name"])
	require.Equal(t, "Olympiad", vars["event_name"])
}

func TestNotificationPlannerSendsManagerAssignmentToAssignee(t *testing.T) {
	eventID, assigneeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	raw, err := json.Marshal(signalModel.EventManagerPayload{ScopeEventID: eventID, SubjectUserID: assigneeID, EventName: "Spring CTF", ManagerRole: "observer", RoleName: "спостерігачем"})
	require.NoError(t, err)
	dispatch := &plannerDispatch{}
	planner := NewNotificationPlanner(plannerQueries{
		rows:  []postgres.ListEffectiveEnabledSignalNotificationSubscriptionsRow{{Channel: "in_app", Audience: []byte(`{"kind":"signal_subject"}`)}},
		users: map[uuid.UUID]postgres.User{assigneeID: {ID: assigneeID, Email: "viewer@example.test"}},
	}, signalModel.DefaultRegistry, dispatch)
	require.NoError(t, planner.Handle(context.Background(), signalModel.Signal{Type: signalModel.TypeEventManagerAssigned, Payload: raw}))
	require.Equal(t, []uuid.UUID{assigneeID}, dispatch.all)
	require.Equal(t, notificationTypes.NotificationType(signalModel.TypeEventManagerAssigned), dispatch.payload.NotificationType())
	vars, err := dispatch.payload.Marshal()
	require.NoError(t, err)
	require.Contains(t, string(vars), `"role_name":"спостерігачем"`)
}

// The inheritance itself (Event override over platform default) is resolved in
// SQL and covered by the postgres integration tests; the planner must ask for
// the EFFECTIVE subscriptions of the signal's Event and dispatch exactly those.
func TestNotificationPlanner_UsesEffectiveSubscriptionsOfSignalEvent(t *testing.T) {
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	raw, err := json.Marshal(signalModel.ParticipantPayload{ScopeEventID: eventID, SubjectUserID: userID, EventName: "Olympiad"})
	require.NoError(t, err)
	var asked postgres.ListEffectiveEnabledSignalNotificationSubscriptionsParams
	dispatch := &plannerDispatch{}
	planner := NewNotificationPlanner(plannerQueries{
		asked: &asked,
		rows:  []postgres.ListEffectiveEnabledSignalNotificationSubscriptionsRow{{Channel: "email", Audience: []byte(`{"kind":"signal_subject"}`)}},
	}, signalModel.DefaultRegistry, dispatch)

	require.NoError(t, planner.Handle(context.Background(), signalModel.Signal{Type: signalModel.TypeParticipantApprovalRegistrationApproved, Payload: raw}))
	require.Equal(t, postgres.ListEffectiveEnabledSignalNotificationSubscriptionsParams{ScopeEventID: eventID, SignalType: string(signalModel.TypeParticipantApprovalRegistrationApproved)}, asked)
	require.Equal(t, []uuid.UUID{userID}, dispatch.all)
}

func TestNotificationPlanner_NoEffectiveSubscriptionMeansNoDispatch(t *testing.T) {
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	raw, err := json.Marshal(signalModel.ParticipantPayload{ScopeEventID: eventID, SubjectUserID: userID, EventName: "Olympiad"})
	require.NoError(t, err)
	dispatch := &plannerDispatch{}
	planner := NewNotificationPlanner(plannerQueries{}, signalModel.DefaultRegistry, dispatch)

	require.NoError(t, planner.Handle(context.Background(), signalModel.Signal{Type: signalModel.TypeParticipantApprovalRegistrationApproved, Payload: raw}))
	require.Empty(t, dispatch.all)
}

// Enrolled always accompanies an action signal that already notifies the
// participant; even an enabled subscription must not produce a second message.
func TestNotificationPlanner_EnrolledNeverDispatches(t *testing.T) {
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	raw, err := json.Marshal(signalModel.ParticipantPayload{ScopeEventID: eventID, SubjectUserID: userID, EventName: "Olympiad"})
	require.NoError(t, err)
	dispatch := &plannerDispatch{}
	planner := NewNotificationPlanner(plannerQueries{
		rows:  []postgres.ListEffectiveEnabledSignalNotificationSubscriptionsRow{{Channel: "email", Audience: []byte(`{"kind":"signal_subject"}`)}},
		users: map[uuid.UUID]postgres.User{userID: {ID: userID, Email: "jane@example.test"}},
	}, signalModel.DefaultRegistry, dispatch)

	require.NoError(t, planner.Handle(context.Background(), signalModel.Signal{Type: signalModel.TypeParticipantEnrolled, Payload: raw}))
	require.Empty(t, dispatch.all)
}

func TestNotificationPlanner_ClassifiesLabFailureAsStandRequest(t *testing.T) {
	eventID, managerID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	raw, err := json.Marshal(signalModel.EventLabFailedPayload{ScopeEventID: eventID, SubjectUserID: managerID, TeamID: teamID})
	require.NoError(t, err)
	dispatch := &plannerDispatch{}
	planner := NewNotificationPlanner(plannerQueries{rows: []postgres.ListEffectiveEnabledSignalNotificationSubscriptionsRow{
		{Channel: "in_app", Audience: []byte(`{"kind":"signal_subject"}`)},
	}}, signalModel.DefaultRegistry, dispatch)

	require.NoError(t, planner.Handle(context.Background(), signalModel.Signal{Type: signalModel.TypeEventLabFailed, Payload: raw}))
	require.Equal(t, []uuid.UUID{managerID}, dispatch.all)
	require.NotNil(t, dispatch.options.Inbox)
	require.Equal(t, inboxModel.CategoryRequests, dispatch.options.Inbox.Category)
	require.True(t, dispatch.options.Inbox.ActionRequired)
	require.Equal(t, inboxModel.StandRef(eventID, teamID), dispatch.options.Inbox.SubjectRef)
}

func TestNotificationPlanner_EventNoticeReachesManagersAsActivity(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	participantID, managerID, both := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	raw, err := json.Marshal(signalModel.EventNoticePayload{ScopeEventID: eventID, EventName: "CTF"})
	require.NoError(t, err)
	dispatch := &plannerDispatch{}
	planner := NewNotificationPlanner(plannerQueries{
		rows: []postgres.ListEffectiveEnabledSignalNotificationSubscriptionsRow{
			{Channel: "email", Audience: []byte(`{"kind":"all_participants"}`)},
			{Channel: "in_app", Audience: []byte(`{"kind":"all_participants"}`)},
		},
		recipients: []postgres.ListEventFormRecipientCandidatesRow{{UserID: participantID}, {UserID: both}},
		managers:   []uuid.UUID{managerID, both},
	}, signalModel.DefaultRegistry, dispatch)

	require.NoError(t, planner.Handle(context.Background(), signalModel.Signal{Type: signalModel.TypeParticipantEventFinished, Payload: raw}))
	require.ElementsMatch(t, []uuid.UUID{participantID, both, managerID}, dispatch.all)
	manager := dispatch.byUser[managerID]
	require.Equal(t, []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelInApp}, manager.OverrideChannels)
	require.Equal(t, inboxModel.CategoryActivity, manager.Inbox.Category)
	require.Equal(t, inboxModel.RoleManager, manager.Inbox.Role)
	require.Len(t, dispatch.byUser[both].OverrideChannels, 2, "a manager who participates keeps the participant copy")
	require.Equal(t, inboxModel.CategoryActivity, dispatch.byUser[participantID].Inbox.Category)
}

func TestNotificationPlanner_EventNoticeWithoutInAppSkipsManagers(t *testing.T) {
	eventID, participantID, managerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	raw, err := json.Marshal(signalModel.EventNoticePayload{ScopeEventID: eventID})
	require.NoError(t, err)
	dispatch := &plannerDispatch{}
	planner := NewNotificationPlanner(plannerQueries{
		rows:       []postgres.ListEffectiveEnabledSignalNotificationSubscriptionsRow{{Channel: "email", Audience: []byte(`{"kind":"all_participants"}`)}},
		recipients: []postgres.ListEventFormRecipientCandidatesRow{{UserID: participantID}},
		managers:   []uuid.UUID{managerID},
	}, signalModel.DefaultRegistry, dispatch)

	require.NoError(t, planner.Handle(context.Background(), signalModel.Signal{Type: signalModel.TypeParticipantEventStartReminder, Payload: raw}))
	require.Equal(t, []uuid.UUID{participantID}, dispatch.all)
}

func TestNotificationPlanner_ApplicantCopyIsPersonal(t *testing.T) {
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	raw, err := json.Marshal(signalModel.ParticipantPayload{ScopeEventID: eventID, SubjectUserID: userID})
	require.NoError(t, err)
	dispatch := &plannerDispatch{}
	planner := NewNotificationPlanner(plannerQueries{rows: []postgres.ListEffectiveEnabledSignalNotificationSubscriptionsRow{
		{Channel: "in_app", Audience: []byte(`{"kind":"signal_subject"}`)},
	}}, signalModel.DefaultRegistry, dispatch)

	require.NoError(t, planner.Handle(context.Background(), signalModel.Signal{Type: signalModel.TypeParticipantApprovalRegistrationSubmitted, Payload: raw}))
	require.Equal(t, inboxModel.CategoryPersonal, dispatch.options.Inbox.Category)
	require.False(t, dispatch.options.Inbox.ActionRequired)
	require.Empty(t, dispatch.options.Inbox.SubjectRef)
}

func TestNotificationPlanner_ResultsPublishedReachesParticipantsAndManagers(t *testing.T) {
	eventID, participantID, managerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	occurred := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	raw, err := json.Marshal(signalModel.EventNoticePayload{ScopeEventID: eventID})
	require.NoError(t, err)
	dispatch := &plannerDispatch{}
	planner := NewNotificationPlanner(plannerQueries{
		rows:       []postgres.ListEffectiveEnabledSignalNotificationSubscriptionsRow{{Channel: "in_app", Audience: []byte(`{"kind":"all_participants"}`)}},
		recipients: []postgres.ListEventFormRecipientCandidatesRow{{UserID: participantID}},
		managers:   []uuid.UUID{managerID},
	}, signalModel.DefaultRegistry, dispatch)

	require.NoError(t, planner.Handle(context.Background(), signalModel.Signal{Type: signalModel.TypeParticipantEventResultsPublished, OccurredAt: occurred, Payload: raw}))
	require.ElementsMatch(t, []uuid.UUID{participantID, managerID}, dispatch.all)
	for _, user := range []uuid.UUID{participantID, managerID} {
		meta := dispatch.byUser[user].Inbox
		require.Equal(t, inboxModel.CategoryActivity, meta.Category)
		require.Equal(t, occurred, *meta.RaisedAt)
	}
}
