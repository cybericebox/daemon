package inboxUseCase_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
	"github.com/cybericebox/daemon/pkg/tools"
)

type sent struct {
	userID  uuid.UUID
	typ     notificationTypes.NotificationType
	vars    map[string]any
	options dispatchModel.NotifyOptions
}

type fakeNotifier struct{ sent []sent }

func (f *fakeNotifier) Notify(_ context.Context, userID uuid.UUID, p notificationTypes.NotificationPayload, opts ...dispatchModel.NotifyOption) error {
	raw, err := p.Marshal()
	if err != nil {
		return err
	}
	vars := map[string]any{}
	if err = json.Unmarshal(raw, &vars); err != nil {
		return err
	}
	f.sent = append(f.sent, sent{userID: userID, typ: p.NotificationType(), vars: vars, options: dispatchModel.ApplyNotifyOptions(opts)})
	return nil
}

func participantSignal(t *testing.T, typ signalModel.Type, p signalModel.ParticipantPayload) signalModel.Signal {
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	return signalModel.Signal{ID: tools.NewUUIDv7(), Type: typ, Payload: raw}
}

func TestRequestRouter_ApplicationGoesToWriteManagersButTheApplicant(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	notifier := &fakeNotifier{}
	router := inboxUseCase.NewRequestRouter(repo, notifier)
	eventID, applicant, owner, moderator := tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7()
	occurred := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	// Viewers are filtered by the query (integration-tested); an applicant who
	// also manages the Event is skipped here.
	repo.EXPECT().ListEventWriteManagerUserIDs(gomock.Any(), eventID).Return([]uuid.UUID{owner, applicant, moderator}, nil)
	repo.EXPECT().GetUserByID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID) (postgres.User, error) {
		if id == applicant {
			return postgres.User{ID: id, FirstName: "Олена", LastName: "К.", Email: "o@example.org"}, nil
		}
		return postgres.User{ID: id, Email: id.String() + "@example.org"}, nil
	}).AnyTimes()

	signal := participantSignal(t, signalModel.TypeParticipantApprovalRegistrationSubmitted,
		signalModel.ParticipantPayload{ScopeEventID: eventID, SubjectUserID: applicant, EventName: "CTF"})
	signal.OccurredAt = occurred
	err := router.Handle(context.Background(), signal)
	require.NoError(t, err)

	require.Len(t, notifier.sent, 2)
	ref := inboxModel.ApplicationRef(eventID, applicant)
	for i, want := range []uuid.UUID{owner, moderator} {
		got := notifier.sent[i]
		assert.Equal(t, want, got.userID)
		assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeApplicationSubmitted), got.typ)
		assert.Equal(t, "Олена К.", got.vars["applicant_name"])
		assert.Equal(t, want.String(), got.vars["user_id"])
		require.NotNil(t, got.options.Inbox)
		assert.Equal(t, inboxModel.CategoryRequests, got.options.Inbox.Category)
		assert.True(t, got.options.Inbox.ActionRequired)
		assert.Equal(t, ref, got.options.Inbox.SubjectRef)
		require.NotNil(t, got.options.Inbox.RaisedAt)
		assert.Equal(t, occurred, *got.options.Inbox.RaisedAt)
		require.NotNil(t, got.options.ScopeEventID)
		assert.Equal(t, eventID, *got.options.ScopeEventID)
		assert.Equal(t, []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelInApp}, got.options.OverrideChannels)
	}
}

func TestRequestRouter_DecisionResolvesApplicationForEveryone(t *testing.T) {
	eventID, applicant, moderator := tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7()
	for typ, resolution := range map[signalModel.Type]inboxModel.Resolution{
		signalModel.TypeParticipantApprovalRegistrationApproved: inboxModel.ResolutionApproved,
		signalModel.TypeParticipantApprovalRegistrationRejected: inboxModel.ResolutionRejected,
	} {
		ctrl := gomock.NewController(t)
		repo := postgresMocks.NewMockQuerier(ctrl)
		router := inboxUseCase.NewRequestRouter(repo, &fakeNotifier{})
		repo.EXPECT().ResolveInboxBySubjectRef(gomock.Any(), postgres.ResolveInboxBySubjectRefParams{
			SubjectRef: inboxModel.ApplicationRef(eventID, applicant), Resolution: string(resolution),
			ResolvedBy: uuid.NullUUID{UUID: moderator, Valid: true},
		}).Return(int64(3), nil)

		require.NoError(t, router.Handle(context.Background(), participantSignal(t, typ,
			signalModel.ParticipantPayload{ScopeEventID: eventID, SubjectUserID: applicant, ActorUserID: moderator})))
	}
}

func TestRequestRouter_ProposalSubmittedGoesToAdmins(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	notifier := &fakeNotifier{}
	router := inboxUseCase.NewRequestRouter(repo, notifier)
	proposer, admin, proposalID := tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7()

	repo.EXPECT().ListPlatformAdminUserIDs(gomock.Any()).Return([]uuid.UUID{admin, proposer}, nil)
	repo.EXPECT().GetUserByID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID) (postgres.User, error) {
		return postgres.User{ID: id, FirstName: "Name", Email: "x@example.org"}, nil
	}).AnyTimes()

	require.NoError(t, router.ProposalSubmitted(context.Background(), inboxUseCase.Proposal{
		ID: proposalID, ExerciseID: tools.NewUUIDv7(), ExerciseName: "Web", ProposedBy: proposer,
	}))
	require.Len(t, notifier.sent, 1, "the proposer never reviews their own proposal")
	got := notifier.sent[0]
	assert.Equal(t, admin, got.userID)
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeProposalSubmitted), got.typ)
	assert.Equal(t, "Web", got.vars["exercise_name"])
	assert.Equal(t, inboxModel.CategoryRequests, got.options.Inbox.Category)
	assert.Equal(t, inboxModel.ProposalRef(proposalID), got.options.Inbox.SubjectRef)
	assert.Nil(t, got.options.ScopeEventID, "catalog requests are platform items")
}

func TestRequestRouter_ProposalDecisionResolvesAndTellsProposer(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	notifier := &fakeNotifier{}
	router := inboxUseCase.NewRequestRouter(repo, notifier)
	proposer, admin, proposalID := tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7()

	repo.EXPECT().ResolveInboxBySubjectRef(gomock.Any(), postgres.ResolveInboxBySubjectRefParams{
		SubjectRef: inboxModel.ProposalRef(proposalID), Resolution: "rejected", ResolvedBy: uuid.NullUUID{UUID: admin, Valid: true},
	}).Return(int64(2), nil)
	repo.EXPECT().GetUserByID(gomock.Any(), proposer).Return(postgres.User{ID: proposer, Email: "p@example.org"}, nil).Times(2)

	require.NoError(t, router.ProposalDecided(context.Background(), inboxUseCase.Proposal{
		ID: proposalID, ExerciseName: "Web", ProposedBy: proposer, DecisionNote: "not yet",
	}, false, admin))
	require.Len(t, notifier.sent, 1)
	got := notifier.sent[0]
	assert.Equal(t, proposer, got.userID)
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeProposalRejected), got.typ)
	assert.Equal(t, "not yet", got.vars["decision_note"])
	assert.Equal(t, inboxModel.CategoryPersonal, got.options.Inbox.Category)
	assert.False(t, got.options.Inbox.ActionRequired)
}

func TestRequestRouter_ElevationRequestGoesToAdminsAndTheDecisionToTheAuthor(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	notifier := &fakeNotifier{}
	router := inboxUseCase.NewRequestRouter(repo, notifier)
	author, admin, elevationID := tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7()
	repo.EXPECT().GetUserByID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID) (postgres.User, error) {
		return postgres.User{ID: id, FirstName: "Name", Email: "x@example.org"}, nil
	}).AnyTimes()
	repo.EXPECT().ListSuperAdminUserIDs(gomock.Any()).Return([]uuid.UUID{admin, author}, nil)

	e := inboxUseCase.Elevation{ID: elevationID, ExerciseID: tools.NewUUIDv7(), ExerciseName: "Web", RequestedBy: author, Devices: "db: 500m / 2Gi", Reason: "big database"}
	require.NoError(t, router.ElevationRequested(context.Background(), e))
	require.Len(t, notifier.sent, 1, "the author never decides their own request")
	got := notifier.sent[0]
	assert.Equal(t, admin, got.userID)
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeElevationRequested), got.typ)
	assert.Equal(t, "db: 500m / 2Gi", got.vars["devices"])
	assert.Equal(t, inboxModel.CategoryRequests, got.options.Inbox.Category)
	assert.Equal(t, inboxModel.ElevationRef(elevationID), got.options.Inbox.SubjectRef)

	notifier.sent = nil
	repo.EXPECT().ResolveInboxBySubjectRef(gomock.Any(), postgres.ResolveInboxBySubjectRefParams{
		SubjectRef: inboxModel.ElevationRef(elevationID), Resolution: "approved", ResolvedBy: uuid.NullUUID{UUID: admin, Valid: true},
	}).Return(int64(2), nil)
	require.NoError(t, router.ElevationDecided(context.Background(), e, true, admin))
	require.Len(t, notifier.sent, 1)
	assert.Equal(t, author, notifier.sent[0].userID)
	assert.Equal(t, notificationTypes.NotificationType(inboxModel.TypeElevationApproved), notifier.sent[0].typ)
	assert.Equal(t, inboxModel.CategoryPersonal, notifier.sent[0].options.Inbox.Category)
}

func TestRequestRouter_StandRecreatedResolvesAsFixed(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	router := inboxUseCase.NewRequestRouter(repo, &fakeNotifier{})
	eventID, teamID, moderator := tools.NewUUIDv7(), tools.NewUUIDv7(), tools.NewUUIDv7()
	repo.EXPECT().ResolveInboxBySubjectRef(gomock.Any(), postgres.ResolveInboxBySubjectRefParams{
		SubjectRef: inboxModel.StandRef(eventID, teamID), Resolution: "fixed", ResolvedBy: uuid.NullUUID{UUID: moderator, Valid: true},
	}).Return(int64(4), nil)

	require.NoError(t, router.StandRecreated(context.Background(), eventID, teamID, moderator))
}

// Every type the router sends must pass the dispatcher's channel gate.
func TestRequestRouter_TypesSupportInApp(t *testing.T) {
	for _, typ := range []string{inboxModel.TypeApplicationSubmitted, inboxModel.TypeProposalSubmitted, inboxModel.TypeProposalApproved, inboxModel.TypeProposalRejected,
		inboxModel.TypeElevationRequested, inboxModel.TypeElevationApproved, inboxModel.TypeElevationRejected} {
		assert.True(t, notificationTypes.Supports(notificationTypes.NotificationType(typ), notificationTypes.NotificationChannelInApp), typ)
	}
}

func TestRequestRouter_EventFinishExpiresItsApplications(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	router := inboxUseCase.NewRequestRouter(repo, &fakeNotifier{})
	eventID := tools.NewUUIDv7()
	raw, err := json.Marshal(signalModel.EventNoticePayload{ScopeEventID: eventID})
	require.NoError(t, err)
	repo.EXPECT().ResolveInboxBySubjectPattern(gomock.Any(), postgres.ResolveInboxBySubjectPatternParams{
		SubjectPattern: "application:" + eventID.String() + ":%", Resolution: "expired",
	}).Return(int64(2), nil)

	require.NoError(t, router.Handle(context.Background(), signalModel.Signal{Type: signalModel.TypeParticipantEventFinished, Payload: raw}))
}

func TestRequestRouter_UserDeletedWithdrawsApplications(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	router := inboxUseCase.NewRequestRouter(repo, &fakeNotifier{})
	userID := tools.NewUUIDv7()
	repo.EXPECT().ResolveInboxBySubjectPattern(gomock.Any(), postgres.ResolveInboxBySubjectPatternParams{
		SubjectPattern: "application:%:" + userID.String(), Resolution: "withdrawn",
	}).Return(int64(1), nil)

	require.NoError(t, router.UserDeleted(context.Background(), userID))
}
