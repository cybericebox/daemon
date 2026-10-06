package event_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestJoinTeam_ReservesCapacityAndAssignsApprovedParticipant(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	teamID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
		MaxTeamSize: 5, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rosterOpenEventRow(eventID, now), nil)
	q.EXPECT().GetEventTeamByJoinCode(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, Name: "Blue Team", JoinCode: "secure-join-code",
		CaptainID: uuid.Must(uuid.NewV7()), MemberCount: 1, CreatedAt: now, UpdatedAt: now,
	}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), CreatedAt: now,
	}, nil)
	q.EXPECT().TryAddEventTeamMember(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.TryAddEventTeamMemberParams) (int64, error) {
			if arg.ID != teamID || arg.EventID != eventID || arg.MaxTeamSize != 5 {
				t.Fatalf("unexpected capacity reservation: %+v", arg)
			}
			return 1, nil
		})
	q.EXPECT().AssignEventParticipantTeam(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.AssignEventParticipantTeamParams) (int64, error) {
			if arg.EventID != eventID || arg.UserID != userID || !arg.TeamID.Valid || arg.TeamID.UUID != teamID ||
				!arg.TeamRole.Valid || arg.TeamRole.Int16 != int16(participantModel.TeamRoleMember) {
				t.Fatalf("unexpected membership assignment: %+v", arg)
			}
			return 1, nil
		})

	if err := uc.JoinTeam(context.Background(), eventID, userID, "secure-join-code"); err != nil {
		t.Fatalf("JoinTeam: %v", err)
	}
}

func TestJoinTeam_RejectsIndividualFormat(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationIndividual), Valid: true},
		CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)

	err := uc.JoinTeam(context.Background(), eventID, userID, "solo-code")
	if !eventTeamModel.ErrEventTeamParticipationInvalid.Err().Is(err) {
		t.Fatalf("expected team format restriction, got %v", err)
	}
}

func TestAssignParticipantToTeam_RejectsIndividualFormat(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, teamID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationIndividual), Valid: true},
		CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)

	err := uc.AssignParticipantToTeam(context.Background(), eventID, teamID, userID)
	if !eventTeamModel.ErrEventTeamParticipationInvalid.Err().Is(err) {
		t.Fatalf("expected team format restriction, got %v", err)
	}
}

func TestCreateManagedTeam_BypassesSelfServiceRosterButKeepsCaptainInvariant(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, nil).AnyTimes()
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	captainID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
		MaxTeamSize: 5, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: captainID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: captainID, Status: int16(participantModel.StatusApproved), CreatedAt: now,
	}, nil)
	q.EXPECT().GetEventTeamFieldConfig(gomock.Any(), eventID).Return(postgres.EventTeamFieldConfig{}, pgx.ErrNoRows)
	q.EXPECT().CreateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateEventTeamParams) (postgres.EventTeam, error) {
		if arg.EventID != eventID || arg.CaptainID != captainID || arg.Name != "Blue Team" || arg.MemberCount != 1 {
			t.Fatalf("unexpected managed team: %+v", arg)
		}
		return postgres.EventTeam{ID: arg.ID, EventID: arg.EventID, Name: arg.Name, JoinCode: arg.JoinCode, CaptainID: arg.CaptainID, MemberCount: arg.MemberCount, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
	})
	q.EXPECT().AssignEventParticipantTeam(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.AssignEventParticipantTeamParams) (int64, error) {
		if arg.EventID != eventID || arg.UserID != captainID || !arg.TeamID.Valid || !arg.TeamRole.Valid || arg.TeamRole.Int16 != int16(participantModel.TeamRoleCaptain) {
			t.Fatalf("unexpected captain assignment: %+v", arg)
		}
		return 1, nil
	})

	q.EXPECT().GetEventTeamExtraFields(gomock.Any(), gomock.Any()).Return([]byte(`{}`), nil)

	got, err := uc.CreateManagedTeam(context.Background(), eventID, event.CreateManagedTeamInput{Name: "Blue Team", CaptainID: captainID})
	if err != nil {
		t.Fatalf("CreateManagedTeam: %v", err)
	}
	if got.CaptainID != captainID || got.Name != "Blue Team" || !unit.saved || !unit.restored {
		t.Fatalf("unexpected managed team result or transaction: %+v %+v", got, unit)
	}
}

func TestRemoveParticipantFromTeam_RejectsCaptain(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID, teamID, captainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventTeamByID(gomock.Any(), postgres.GetEventTeamByIDParams{EventID: eventID, ID: teamID}).Return(postgres.EventTeam{ID: teamID, EventID: eventID, CaptainID: captainID, MemberCount: 2, CreatedAt: now, UpdatedAt: now}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: captainID}).Return(postgres.EventParticipant{EventID: eventID, UserID: captainID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, TeamRole: pgtype.Int2{Int16: int16(participantModel.TeamRoleCaptain), Valid: true}, CreatedAt: now}, nil)

	err := uc.RemoveParticipantFromTeam(context.Background(), eventID, teamID, captainID)
	if !eventTeamModel.ErrEventTeamCaptainMustTransfer.Err().Is(err) {
		t.Fatalf("expected captain transfer error, got %v", err)
	}
}

func TestCreateTeam_LocksEventAndAssignsCreatorAsCaptain(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, Infra: &recordingLabAccessInfra{}})
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID:       eventID,
		Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
		MaxTeamSize:   5,
		MaxTeams:      pgtype.Int4{Int32: 10, Valid: true},
		CreatedAt:     now,
		UpdatedAt:     pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rosterOpenEventRow(eventID, now), nil)
	q.EXPECT().CountEventTeams(gomock.Any(), eventID).Return(int64(3), nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), CreatedAt: now,
	}, nil)
	document, err := json.Marshal(eventContentModel.Document{Blocks: []eventContentModel.Block{{ID: "school", Type: eventContentModel.BlockField, Key: "school", Input: "text", Label: "School", Required: true}}})
	if err != nil {
		t.Fatal(err)
	}
	q.EXPECT().GetEventTeamFieldConfig(gomock.Any(), eventID).Return(postgres.EventTeamFieldConfig{EventID: eventID, Enabled: true, Required: true, Document: document}, nil)
	q.EXPECT().CreateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateEventTeamParams) (postgres.EventTeam, error) {
			if arg.EventID != eventID || arg.CaptainID != userID || arg.Name != "Blue Team" || arg.MemberCount != 1 || arg.ID == uuid.Nil || arg.JoinCode == "" {
				t.Fatalf("unexpected team creation: %+v", arg)
			}
			return postgres.EventTeam{ID: arg.ID, EventID: arg.EventID, Name: arg.Name, JoinCode: arg.JoinCode, CaptainID: arg.CaptainID, MemberCount: arg.MemberCount, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
		})
	q.EXPECT().UpdateEventTeamExtraFields(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventTeamExtraFieldsParams) (int64, error) {
		if arg.EventID != eventID || arg.TeamID == uuid.Nil || string(arg.ExtraFields) != `{"school":"Kyiv"}` {
			t.Fatalf("unexpected team answers: %+v", arg)
		}
		return 1, nil
	})
	q.EXPECT().AssignEventParticipantTeam(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.AssignEventParticipantTeamParams) (int64, error) {
			if arg.EventID != eventID || arg.UserID != userID || !arg.TeamID.Valid || !arg.TeamRole.Valid || arg.TeamRole.Int16 != int16(participantModel.TeamRoleCaptain) {
				t.Fatalf("unexpected captain assignment: %+v", arg)
			}
			return 1, nil
		})
	q.EXPECT().RequestEventLabAccessSync(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.RequestEventLabAccessSyncParams) (postgres.EventLabAccessSync, error) {
			if arg.EventTeamID == uuid.Nil {
				t.Fatal("new team must schedule group creation")
			}
			return postgres.EventLabAccessSync{EventTeamID: arg.EventTeamID}, nil
		})

	if err := uc.CreateTeamWithFields(context.Background(), eventID, userID, "Blue Team", map[string]any{"school": "Kyiv"}); err != nil {
		t.Fatalf("CreateTeamWithFields: %v", err)
	}
	if !unit.saved || !unit.restored {
		t.Fatalf("team creation must commit and close its transaction: %+v", unit)
	}
}

func TestLeaveTeam_RemovesNonCaptainAndDecrementsTeam(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	teamID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	captainID := uuid.Must(uuid.NewV7())
	now := time.Now()
	role := participantModel.TeamRoleMember

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rosterOpenEventRow(eventID, now), nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, TeamRole: pgtype.Int2{Int16: int16(role), Valid: true}, CreatedAt: now,
	}, nil)
	q.EXPECT().GetEventTeamByID(gomock.Any(), postgres.GetEventTeamByIDParams{EventID: eventID, ID: teamID}).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, Name: "Blue Team", JoinCode: "secure-join-code", CaptainID: captainID, MemberCount: 2, CreatedAt: now, UpdatedAt: now,
	}, nil)
	q.EXPECT().TryRemoveEventTeamMember(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.TryRemoveEventTeamMemberParams) (int64, error) {
			if arg.ID != teamID || arg.EventID != eventID {
				t.Fatalf("unexpected team decrement: %+v", arg)
			}
			return 1, nil
		})
	q.EXPECT().ClearEventParticipantTeam(gomock.Any(), postgres.ClearEventParticipantTeamParams{
		EventID: eventID, UserID: userID, TeamID: uuid.NullUUID{UUID: teamID, Valid: true},
	}).Return(int64(1), nil)

	if err := uc.LeaveTeam(context.Background(), eventID, userID); err != nil {
		t.Fatalf("LeaveTeam: %v", err)
	}
	if !unit.saved || !unit.restored {
		t.Fatalf("leave must commit and close transaction: %+v", unit)
	}
}

// A member of a formed team cannot leave it alone: the roster is closed and nobody could come back. The
// captain's kick stays (see the kick tests).
func TestLeaveTeam_RefusedForFormedTeam(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	teamID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	now := time.Now()
	role := participantModel.TeamRoleMember

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rosterOpenEventRow(eventID, now), nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, TeamRole: pgtype.Int2{Int16: int16(role), Valid: true}, CreatedAt: now,
	}, nil)
	q.EXPECT().GetEventTeamByID(gomock.Any(), postgres.GetEventTeamByIDParams{EventID: eventID, ID: teamID}).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, Name: "Blue Team", JoinCode: "secure-join-code", CaptainID: uuid.Must(uuid.NewV7()), MemberCount: 2,
		FormedAt: pgtype.Timestamptz{Time: now, Valid: true}, CreatedAt: now, UpdatedAt: now,
	}, nil)

	if err := uc.LeaveTeam(context.Background(), eventID, userID); !errors.Is(err, eventTeamModel.ErrEventTeamLeaveLocked.Err()) {
		t.Fatalf("LeaveTeam of a formed team = %v, want ErrEventTeamLeaveLocked", err)
	}
	if unit.saved {
		t.Fatal("a refused leave must not commit")
	}
}

func TestRemoveParticipantFromTeam_RequestsAccessPolicyReplacement(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	infra := &recordingLabAccessInfra{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, Infra: infra})
	eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	userID, captainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved),
		TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, TeamRole: pgtype.Int2{Int16: int16(participantModel.TeamRoleMember), Valid: true}, CreatedAt: now,
	}, nil)
	q.EXPECT().GetEventTeamByID(gomock.Any(), postgres.GetEventTeamByIDParams{EventID: eventID, ID: teamID}).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, CaptainID: captainID, MemberCount: 2, CreatedAt: now, UpdatedAt: now,
	}, nil)
	// Before the start moderators still rearrange rosters.
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rosterOpenEventRow(eventID, now), nil)
	q.EXPECT().TryRemoveEventTeamMember(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().ClearEventParticipantTeam(gomock.Any(), postgres.ClearEventParticipantTeamParams{
		EventID: eventID, UserID: userID, TeamID: uuid.NullUUID{UUID: teamID, Valid: true},
	}).Return(int64(1), nil)
	q.EXPECT().RequestEventLabAccessSync(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.RequestEventLabAccessSyncParams) (postgres.EventLabAccessSync, error) {
			if arg.EventTeamID != teamID {
				t.Fatalf("access sync team = %s, want %s", arg.EventTeamID, teamID)
			}
			return postgres.EventLabAccessSync{EventTeamID: teamID, DesiredRevision: 2}, nil
		},
	)

	if err := uc.RemoveParticipantFromTeam(context.Background(), eventID, teamID, userID); err != nil {
		t.Fatalf("RemoveParticipantFromTeam: %v", err)
	}
	if !unit.saved {
		t.Fatal("leave must persist the revoked ACL in its transaction")
	}
}

func TestRenameTeam_AllowsCaptainAndUsesOptimisticLock(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	teamID := uuid.Must(uuid.NewV7())
	captainID := uuid.Must(uuid.NewV7())
	now := time.Now()

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rosterOpenEventRow(eventID, now), nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: captainID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}, nil).AnyTimes()
	q.EXPECT().GetEventTeamByID(gomock.Any(), postgres.GetEventTeamByIDParams{EventID: eventID, ID: teamID}).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, Name: "Old Team", JoinCode: "secure-join-code", CaptainID: captainID, MemberCount: 2, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}, nil)
	q.EXPECT().UpdateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpdateEventTeamParams) (int64, error) {
			if arg.Name != "New Team" || !arg.ExpectedUpdatedAt.Equal(now) || arg.CaptainID != captainID {
				t.Fatalf("unexpected team update: %+v", arg)
			}
			return 1, nil
		})

	if err := uc.RenameTeam(context.Background(), eventID, teamID, captainID, "New Team"); err != nil {
		t.Fatalf("RenameTeam: %v", err)
	}
	if !unit.saved {
		t.Fatal("rename must commit transaction")
	}
}

func TestTransferTeamCaptaincy_UpdatesTeamAndBothMemberRoles(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	teamID := uuid.Must(uuid.NewV7())
	captainID := uuid.Must(uuid.NewV7())
	newCaptainID := uuid.Must(uuid.NewV7())
	now := time.Now()

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rosterOpenEventRow(eventID, now), nil)
	q.EXPECT().GetEventTeamByID(gomock.Any(), postgres.GetEventTeamByIDParams{EventID: eventID, ID: teamID}).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, Name: "Blue Team", JoinCode: "secure-join-code", CaptainID: captainID, MemberCount: 2, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: newCaptainID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: newCaptainID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, TeamRole: pgtype.Int2{Int16: int16(participantModel.TeamRoleMember), Valid: true}, CreatedAt: now,
	}, nil)
	q.EXPECT().UpdateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpdateEventTeamParams) (int64, error) {
			if arg.CaptainID != newCaptainID || !arg.ExpectedUpdatedAt.Equal(now) {
				t.Fatalf("unexpected captain update: %+v", arg)
			}
			return 1, nil
		})
	q.EXPECT().SetEventParticipantTeamRole(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.SetEventParticipantTeamRoleParams) (int64, error) {
			if arg.EventID != eventID || !arg.TeamID.Valid || arg.TeamID.UUID != teamID {
				t.Fatalf("unexpected role change: %+v", arg)
			}
			return 1, nil
		}).Times(2)

	if err := uc.TransferTeamCaptaincy(context.Background(), eventID, teamID, captainID, newCaptainID); err != nil {
		t.Fatalf("TransferTeamCaptaincy: %v", err)
	}
	if !unit.saved {
		t.Fatal("captain transfer must commit transaction")
	}
}

func TestDisbandTeam_CaptainDeletesTeam(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	teamID := uuid.Must(uuid.NewV7())
	captainID := uuid.Must(uuid.NewV7())
	now := time.Now()

	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rosterOpenEventRow(eventID, now), nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: captainID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}, nil).AnyTimes()
	q.EXPECT().GetEventTeamByID(gomock.Any(), postgres.GetEventTeamByIDParams{EventID: eventID, ID: teamID}).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, Name: "Blue Team", JoinCode: "secure-join-code", CaptainID: captainID, MemberCount: 2, CreatedAt: now, UpdatedAt: now,
	}, nil)
	q.EXPECT().QueueTeamLabGroupCleanup(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().DeleteEventTeam(gomock.Any(), postgres.DeleteEventTeamParams{ID: teamID, EventID: eventID}).Return(int64(1), nil)

	if err := uc.DisbandTeam(context.Background(), eventID, teamID, captainID); err != nil {
		t.Fatalf("DisbandTeam: %v", err)
	}
	if !unit.saved {
		t.Fatal("disband must commit transaction")
	}
}

func TestDeleteManagedTeam_QueuesLabGroupCleanupBeforeCascadeDelete(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	teamID := uuid.Must(uuid.NewV7())
	now := time.Now()

	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)
	// An empty team may go at any time: nobody is freed to switch.
	q.EXPECT().GetEventTeamByID(gomock.Any(), postgres.GetEventTeamByIDParams{EventID: eventID, ID: teamID}).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, CaptainID: uuid.Must(uuid.NewV7()), MemberCount: 0, CreatedAt: now, UpdatedAt: now,
	}, nil)
	q.EXPECT().QueueTeamLabGroupCleanup(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().DeleteEventTeam(gomock.Any(), postgres.DeleteEventTeamParams{ID: teamID, EventID: eventID}).Return(int64(1), nil)

	if err := uc.DeleteManagedTeam(context.Background(), eventID, teamID); err != nil {
		t.Fatalf("DeleteManagedTeam: %v", err)
	}
	if !unit.saved {
		t.Fatal("managed deletion must commit cleanup request and team deletion together")
	}
}

func TestGetOwnTeam_ReturnsPrivateJoinCodeAndRole(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, nil).AnyTimes()
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	teamID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	captainID := uuid.Must(uuid.NewV7())
	now := time.Now()

	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, TeamRole: pgtype.Int2{Int16: int16(participantModel.TeamRoleMember), Valid: true}, CreatedAt: now,
	}, nil)
	q.EXPECT().GetEventTeamForParticipant(gomock.Any(), postgres.GetEventTeamForParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, Name: "Blue Team", JoinCode: "secure-join-code", CaptainID: captainID, MemberCount: 2, CreatedAt: now, UpdatedAt: now,
	}, nil)
	q.EXPECT().GetEventTeamExtraFields(gomock.Any(), postgres.GetEventTeamExtraFieldsParams{EventID: eventID, TeamID: teamID}).Return([]byte(`{}`), nil)
	q.EXPECT().GetEventTeamFieldConfig(gomock.Any(), eventID).Return(postgres.EventTeamFieldConfig{}, pgx.ErrNoRows)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, MaxTeamSize: 5, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)

	v, err := uc.GetOwnTeam(context.Background(), eventID, userID)
	if err != nil {
		t.Fatalf("GetOwnTeam: %v", err)
	}
	if v.ID != teamID || v.JoinCode != "" || v.JoinCodeExpiresAt != nil || v.Role != participantModel.TeamRoleMember || !v.Admitted || v.MaxTeamSize != 5 {
		t.Fatalf("a regular member must see the team but no join link: %+v", v)
	}
}

func TestGetOwnTeam_CaptainSeesJoinLinkWithExpiry(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, nil).AnyTimes()
	uc := newUC(q)
	eventID, teamID, captainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC().Truncate(time.Second)
	expires := now.Add(24 * time.Hour)

	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: captainID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: captainID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, TeamRole: pgtype.Int2{Int16: int16(participantModel.TeamRoleCaptain), Valid: true}, CreatedAt: now,
	}, nil)
	q.EXPECT().GetEventTeamForParticipant(gomock.Any(), postgres.GetEventTeamForParticipantParams{EventID: eventID, UserID: captainID}).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, Name: "Blue Team", JoinCode: "secure-join-code", JoinCodeExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
		CaptainID: captainID, MemberCount: 2, CreatedAt: now, UpdatedAt: now,
	}, nil)
	q.EXPECT().GetEventTeamExtraFields(gomock.Any(), postgres.GetEventTeamExtraFieldsParams{EventID: eventID, TeamID: teamID}).Return([]byte(`{}`), nil)
	q.EXPECT().GetEventTeamFieldConfig(gomock.Any(), eventID).Return(postgres.EventTeamFieldConfig{}, pgx.ErrNoRows)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, MaxTeamSize: 5, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)

	v, err := uc.GetOwnTeam(context.Background(), eventID, captainID)
	if err != nil {
		t.Fatalf("GetOwnTeam: %v", err)
	}
	if v.JoinCode != "secure-join-code" || v.JoinCodeExpiresAt == nil || !v.JoinCodeExpiresAt.Equal(expires) {
		t.Fatalf("the captain must see the join link and its expiry: %+v", v)
	}
}

func TestJoinTeam_RejectsExpiredJoinLink(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID, teamID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
		MaxTeamSize: 5, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rosterOpenEventRow(eventID, now), nil)
	q.EXPECT().GetEventTeamByJoinCode(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, Name: "Blue Team", JoinCode: "secure-join-code",
		JoinCodeExpiresAt: pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true},
		CaptainID:         uuid.Must(uuid.NewV7()), MemberCount: 1, CreatedAt: now, UpdatedAt: now,
	}, nil)

	err := uc.JoinTeam(context.Background(), eventID, userID, "secure-join-code")
	if !eventTeamModel.ErrEventTeamJoinCodeExpired.Err().Is(err) {
		t.Fatalf("an expired join link must be rejected, got %v", err)
	}
}

func TestRegenerateTeamJoinCode_CaptainSetsNewCodeAndExpiry(t *testing.T) {
	cases := map[eventTeamModel.JoinCodeExpiry]time.Duration{eventTeamModel.JoinCodeExpiryNone: 0, eventTeamModel.JoinCodeExpiryDay: 24 * time.Hour, eventTeamModel.JoinCodeExpiryWeek: 7 * 24 * time.Hour}
	for expiry, ttl := range cases {
		t.Run(string(expiry), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			unit := &testUoW{}
			uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
			eventID, teamID, captainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			now := time.Now()
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rosterOpenEventRow(eventID, now), nil)
			q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: captainID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}, nil).AnyTimes()
			q.EXPECT().GetEventTeamByID(gomock.Any(), postgres.GetEventTeamByIDParams{EventID: eventID, ID: teamID}).Return(postgres.EventTeam{
				ID: teamID, EventID: eventID, Name: "Blue Team", JoinCode: "old-secure-join-code",
				JoinCodeExpiresAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true},
				CaptainID:         captainID, MemberCount: 2, CreatedAt: now, UpdatedAt: now,
			}, nil)
			q.EXPECT().UpdateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, arg postgres.UpdateEventTeamParams) (int64, error) {
					if arg.JoinCode == "old-secure-join-code" || len(arg.JoinCode) < 12 {
						t.Fatalf("the old link must be replaced: %q", arg.JoinCode)
					}
					if ttl == 0 && arg.JoinCodeExpiresAt.Valid {
						t.Fatalf("no expiry must clear the old one: %+v", arg.JoinCodeExpiresAt)
					}
					if ttl > 0 {
						if got := arg.JoinCodeExpiresAt.Time.Sub(now); !arg.JoinCodeExpiresAt.Valid || got < ttl-time.Minute || got > ttl+time.Minute {
							t.Fatalf("unexpected expiry %v for %v", arg.JoinCodeExpiresAt, ttl)
						}
					}
					return 1, nil
				})
			if err := uc.RegenerateTeamJoinCode(context.Background(), eventID, teamID, captainID, expiry); err != nil {
				t.Fatalf("RegenerateTeamJoinCode: %v", err)
			}
			if !unit.saved {
				t.Fatal("regeneration must commit")
			}
		})
	}
}

func TestRegenerateTeamJoinCode_UntilStartIsBoundToTheEventStart(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, teamID, captainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	row := rosterOpenEventRow(eventID, now)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(row, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: captainID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}, nil).AnyTimes()
	q.EXPECT().GetEventTeamByID(gomock.Any(), postgres.GetEventTeamByIDParams{EventID: eventID, ID: teamID}).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, Name: "Blue Team", JoinCode: "old-secure-join-code", CaptainID: captainID, MemberCount: 2, CreatedAt: now, UpdatedAt: now,
	}, nil)
	q.EXPECT().UpdateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpdateEventTeamParams) (int64, error) {
			if !arg.JoinCodeExpiresAt.Valid || !arg.JoinCodeExpiresAt.Time.Equal(row.StartAt) {
				t.Fatalf("the link must end at the event start %v, got %+v", row.StartAt, arg.JoinCodeExpiresAt)
			}
			return 1, nil
		})
	if err := uc.RegenerateTeamJoinCode(context.Background(), eventID, teamID, captainID, eventTeamModel.JoinCodeExpiryStart); err != nil {
		t.Fatalf("RegenerateTeamJoinCode: %v", err)
	}
}

func TestRegenerateTeamJoinCode_RejectsNonCaptainAndUnknownExpiry(t *testing.T) {
	cases := []struct {
		name   string
		caller func(captainID uuid.UUID) uuid.UUID
		expiry eventTeamModel.JoinCodeExpiry
		want   error
	}{
		{"member", func(uuid.UUID) uuid.UUID { return uuid.Must(uuid.NewV7()) }, eventTeamModel.JoinCodeExpiryNone, eventTeamModel.ErrEventTeamCaptainRequired.Err()},
		{"unknown expiry", func(c uuid.UUID) uuid.UUID { return c }, "month", eventTeamModel.ErrEventTeamJoinCodeExpiryInvalid.Err()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			unit := &testUoW{}
			uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
			eventID, teamID, captainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			now := time.Now()
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rosterOpenEventRow(eventID, now), nil)
			q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: captainID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}, nil).AnyTimes()
			q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{
				ID: teamID, EventID: eventID, Name: "Blue Team", JoinCode: "old-secure-join-code", CaptainID: captainID, MemberCount: 2, CreatedAt: now, UpdatedAt: now,
			}, nil)
			err := uc.RegenerateTeamJoinCode(context.Background(), eventID, teamID, tc.caller(captainID), tc.expiry)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if unit.saved {
				t.Fatal("a rejected change must not commit")
			}
		})
	}
}

func TestListTeams_ReturnsCursorPageWithoutJoinCodes(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, nil).AnyTimes()
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	firstID := uuid.Must(uuid.NewV7())
	secondID := uuid.Must(uuid.NewV7())
	now := time.Now()

	q.EXPECT().ListEventTeams(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ListEventTeamsParams) ([]postgres.ListEventTeamsRow, error) {
			if arg.EventID != eventID || arg.LimitVal != 2 || arg.Search != "" || arg.AdmissionFilter != -1 {
				t.Fatalf("unexpected list params: %+v", arg)
			}
			return []postgres.ListEventTeamsRow{
				{EventTeam: postgres.EventTeam{ID: firstID, EventID: eventID, Name: "First", JoinCode: "private-code-1", CaptainID: uuid.Must(uuid.NewV7()), MemberCount: 2, CreatedAt: now, UpdatedAt: now, ExtraFields: []byte(`{"city":"Київ"}`)}, Admitted: true},
				{EventTeam: postgres.EventTeam{ID: secondID, EventID: eventID, Name: "Second", JoinCode: "private-code-2", CaptainID: uuid.Must(uuid.NewV7()), MemberCount: 1, CreatedAt: now.Add(-time.Minute), UpdatedAt: now}},
			}, nil
		})
	q.EXPECT().CountEventTeamsFiltered(gomock.Any(), postgres.CountEventTeamsFilteredParams{EventID: eventID, AdmissionFilter: -1, FieldFilters: []byte("[]")}).Return(int64(2), nil)

	v, err := uc.ListTeams(context.Background(), event.ListTeamsFilter{EventID: eventID, PageSize: 1})
	if err != nil {
		t.Fatalf("ListTeams: %v", err)
	}
	if !v.HasMore || v.NextCursor != firstID || len(v.Teams) != 1 || v.Teams[0].ID != firstID || v.Total != 2 ||
		!v.Teams[0].Admitted || v.Teams[0].ExtraFields["city"] != "Київ" || v.Teams[0].Members == nil {
		t.Fatalf("unexpected teams page: %+v", v)
	}
}

func TestListTeams_PassesSearchAndAdmissionFilters(t *testing.T) {
	for _, tc := range []struct {
		name     string
		admitted *bool
		want     int32
	}{
		{name: "admitted", admitted: func() *bool { v := true; return &v }(), want: 1},
		{name: "not admitted", admitted: new(bool), want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, nil).AnyTimes()
			uc := newUC(q)
			eventID := uuid.Must(uuid.NewV7())
			q.EXPECT().ListEventTeams(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, arg postgres.ListEventTeamsParams) ([]postgres.ListEventTeamsRow, error) {
					if arg.Search != "blue" || arg.AdmissionFilter != tc.want {
						t.Fatalf("unexpected list params: %+v", arg)
					}
					return nil, nil
				})
			q.EXPECT().CountEventTeamsFiltered(gomock.Any(), postgres.CountEventTeamsFilteredParams{EventID: eventID, Search: "blue", AdmissionFilter: tc.want, FieldFilters: []byte(`[{"key":"size","op":"any","values":["S"]}]`)}).Return(int64(0), nil)
			if _, err := uc.ListTeams(context.Background(), event.ListTeamsFilter{EventID: eventID, Search: "blue", Admitted: tc.admitted, Fields: []event.AnswerFilter{{Key: "size", Op: event.AnswerFilterAny, Values: []string{"S"}}}}); err != nil {
				t.Fatalf("ListTeams: %v", err)
			}
		})
	}
}

// rosterOpenEventRow is a published event that has not started: the only
// window in which participants may change team rosters.
func rosterOpenEventRow(eventID uuid.UUID, now time.Time) postgres.Event {
	row := rollingEventRow(eventID, now)
	row.StartAt = time.Now().Add(24 * time.Hour)
	row.ArchiveAt = pgtype.Timestamptz{Time: time.Now().Add(72 * time.Hour), Valid: true}
	return row
}

func rollingEventRow(eventID uuid.UUID, now time.Time) postgres.Event {
	return postgres.Event{LifecycleConfigured: true,
		ID: eventID, Tag: "event", Name: "Event", CreatedAt: now.Add(-time.Hour),
		AvailableFrom: now.Add(-time.Hour), ArchiveAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true},
		JoinPolicy: int16(1),
		PublishAt:  now.Add(-time.Hour), StartAt: now.Add(-time.Hour),
		UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}
}

// lockedStartedEventRow is a started event whose joining closed at the start:
// its roster is frozen for participants.
func lockedStartedEventRow(eventID uuid.UUID, now time.Time) postgres.Event {
	row := rollingEventRow(eventID, now)
	row.JoinPolicy = int16(0)
	return row
}

func TestTeamRoster_FrozenAtStartWhenJoiningClosesAtStart(t *testing.T) {
	captainID, memberID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	actions := map[string]func(uc *event.EventUseCase, eventID, teamID uuid.UUID) error{
		"rename": func(uc *event.EventUseCase, eventID, teamID uuid.UUID) error {
			return uc.RenameTeam(context.Background(), eventID, teamID, captainID, "New Team")
		},
		"regenerate": func(uc *event.EventUseCase, eventID, teamID uuid.UUID) error {
			return uc.RegenerateTeamJoinCode(context.Background(), eventID, teamID, captainID, eventTeamModel.JoinCodeExpiryNone)
		},
		"transfer": func(uc *event.EventUseCase, eventID, teamID uuid.UUID) error {
			return uc.TransferTeamCaptaincy(context.Background(), eventID, teamID, captainID, memberID)
		},
		"kick": func(uc *event.EventUseCase, eventID, _ uuid.UUID) error {
			return uc.KickTeamMember(context.Background(), eventID, captainID, memberID)
		},
		"leave": func(uc *event.EventUseCase, eventID, _ uuid.UUID) error {
			return uc.LeaveTeam(context.Background(), eventID, memberID)
		},
	}
	for name, act := range actions {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			unit := &testUoW{}
			uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
			eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(lockedStartedEventRow(eventID, time.Now()), nil)
			if err := act(uc, eventID, teamID); !eventTeamModel.ErrEventTeamRosterLocked.Err().Is(err) {
				t.Fatalf("a started roster with closed joining must be frozen, got %v", err)
			}
			if unit.saved {
				t.Fatal("a frozen roster must not commit")
			}
		})
	}
}

func TestTeamRoster_StaysOpenAfterStartWhenJoiningIsRolling(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, teamID, captainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: captainID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}, nil).AnyTimes()
	q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, Name: "Old Team", JoinCode: "secure-join-code", CaptainID: captainID, MemberCount: 2, CreatedAt: now, UpdatedAt: now,
	}, nil)
	q.EXPECT().UpdateEventTeam(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	if err := uc.RenameTeam(context.Background(), eventID, teamID, captainID, "New Team"); err != nil {
		t.Fatalf("a rolling event must keep the roster open after the start: %v", err)
	}
	if !unit.saved {
		t.Fatal("the change must commit")
	}
}

func TestTeamRoster_OnlyCaptainMayKickTransferOrDisband(t *testing.T) {
	member, target := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	actions := map[string]func(uc *event.EventUseCase, eventID, teamID uuid.UUID) error{
		"transfer": func(uc *event.EventUseCase, eventID, teamID uuid.UUID) error {
			return uc.TransferTeamCaptaincy(context.Background(), eventID, teamID, member, target)
		},
		"disband": func(uc *event.EventUseCase, eventID, teamID uuid.UUID) error {
			return uc.DisbandTeam(context.Background(), eventID, teamID, member)
		},
	}
	for name, act := range actions {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			unit := &testUoW{}
			uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
			eventID, teamID, captainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			now := time.Now()
			q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil).AnyTimes()
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rosterOpenEventRow(eventID, now), nil)
			q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{
				ID: teamID, EventID: eventID, Name: "Blue Team", JoinCode: "secure-join-code", CaptainID: captainID, MemberCount: 3, CreatedAt: now, UpdatedAt: now,
			}, nil).AnyTimes()
			q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{
				EventID: eventID, UserID: target, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true},
				TeamRole: pgtype.Int2{Int16: int16(participantModel.TeamRoleMember), Valid: true}, CreatedAt: now,
			}, nil).AnyTimes()
			if err := act(uc, eventID, teamID); !eventTeamModel.ErrEventTeamCaptainRequired.Err().Is(err) {
				t.Fatalf("only the captain may do this, got %v", err)
			}
			if unit.saved {
				t.Fatal("a rejected change must not commit")
			}
		})
	}
}

func TestKickTeamMember_RejectsNonCaptainCaller(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, teamID, captainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	caller, target := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rosterOpenEventRow(eventID, now), nil)
	q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, Name: "Blue Team", JoinCode: "secure-join-code", CaptainID: captainID, MemberCount: 3, CreatedAt: now, UpdatedAt: now,
	}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.GetEventParticipantParams) (postgres.EventParticipant, error) {
			return postgres.EventParticipant{
				EventID: eventID, UserID: arg.UserID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true},
				TeamRole: pgtype.Int2{Int16: int16(participantModel.TeamRoleMember), Valid: true}, CreatedAt: now,
			}, nil
		}).Times(2)
	if err := uc.KickTeamMember(context.Background(), eventID, caller, target); !eventTeamModel.ErrEventTeamCaptainRequired.Err().Is(err) {
		t.Fatalf("a regular member must not kick, got %v", err)
	}
	if unit.saved {
		t.Fatal("a rejected kick must not commit")
	}
}

func TestListOwnTeamMembers_ListsMembersThenPendingInvitees(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID, teamID, userID, inviteeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true},
		TeamRole: pgtype.Int2{Int16: int16(participantModel.TeamRoleCaptain), Valid: true}, CreatedAt: now,
	}, nil)
	q.EXPECT().ListOwnTeamMembers(gomock.Any(), postgres.ListOwnTeamMembersParams{EventID: eventID, TeamID: teamID}).Return([]postgres.ListOwnTeamMembersRow{
		{UserID: userID, TeamRole: pgtype.Int2{Int16: int16(participantModel.TeamRoleCaptain), Valid: true}, DisplayName: "Captain"},
	}, nil)
	q.EXPECT().ListOwnTeamPendingInvitees(gomock.Any(), postgres.ListOwnTeamPendingInviteesParams{EventID: eventID, TeamID: teamID}).Return([]postgres.ListOwnTeamPendingInviteesRow{
		{UserID: inviteeID, DisplayName: "Invitee"},
	}, nil)

	items, err := uc.ListOwnTeamMembers(context.Background(), eventID, userID)
	if err != nil {
		t.Fatalf("ListOwnTeamMembers: %v", err)
	}
	if len(items) != 2 || items[0].Pending || !items[0].Own || items[0].Role != participantModel.TeamRoleCaptain ||
		!items[1].Pending || items[1].UserID != inviteeID || items[1].Own {
		t.Fatalf("unexpected roster: %+v", items)
	}
}

func TestGetTeamProfile_ReturnsRosterStatusAnswersAndResults(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, teamID, captainID, inviteeID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventTeamByID(gomock.Any(), postgres.GetEventTeamByIDParams{EventID: eventID, ID: teamID}).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, Name: "Blue Team", JoinCode: "secure-join-code", CaptainID: captainID, MemberCount: 1, CreatedAt: now, UpdatedAt: now,
	}, nil)
	q.EXPECT().GetEventTeamExtraFields(gomock.Any(), postgres.GetEventTeamExtraFieldsParams{EventID: eventID, TeamID: teamID}).Return([]byte(`{"city":"Kyiv"}`), nil)
	q.EXPECT().GetEventTeamAdmitted(gomock.Any(), postgres.GetEventTeamAdmittedParams{ID: teamID, EventID: eventID}).Return(true, nil)
	q.EXPECT().GetEventMinTeamSize(gomock.Any(), eventID).Return(int32(1), nil)
	q.EXPECT().ListEventTeamMembers(gomock.Any(), gomock.Any()).Return([]postgres.ListEventTeamMembersRow{
		{TeamID: teamID, UserID: captainID, TeamRole: pgtype.Int2{Int16: int16(participantModel.TeamRoleCaptain), Valid: true}, FirstName: "Olena", LastName: "Koval", Email: "o@example.com"},
	}, nil)
	q.EXPECT().ListPendingTeamInvitations(gomock.Any(), gomock.Any()).Return([]postgres.ListPendingTeamInvitationsRow{
		{TeamID: teamID, UserID: inviteeID, FirstName: "Ivan", LastName: "Petrenko", Email: "i@example.com", CreatedAt: now},
	}, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(frozenConfig(eventID, now, 10), nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil).Times(2)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 2, UpdatedAt: now}, nil)
	q.EXPECT().ListManageScoreboard(gomock.Any(), eventID).Return([]postgres.ListManageScoreboardRow{
		{TeamID: uuid.Must(uuid.NewV7()), Admitted: true, PublicName: "Other", Points: 900},
		{TeamID: teamID, Admitted: true, PublicName: "Blue Team", Points: 300},
	}, nil)
	q.EXPECT().ListManageScoreSolves(gomock.Any(), eventID).Return([]postgres.ListManageScoreSolvesRow{
		{EventTeamID: teamID, EventChallengeID: challengeID, ChallengeName: "Web 1", Points: 300, SolvedAt: now.Add(-time.Hour)},
	}, nil)
	q.EXPECT().ListManageHintTotals(gomock.Any(), eventID).Return(nil, nil)

	got, err := uc.GetTeamProfile(context.Background(), eventID, teamID)
	if err != nil {
		t.Fatalf("GetTeamProfile: %v", err)
	}
	if got.Team.CaptainID != captainID || len(got.Team.Members) != 1 || len(got.Team.PendingInvitations) != 1 || !got.Team.Admitted || got.Team.ExtraFields["city"] != "Kyiv" {
		t.Fatalf("unexpected team: %+v", got.Team)
	}
	if got.Results == nil || got.Results.Points != 300 || got.Results.Rank == nil || *got.Results.Rank != 2 || len(got.Results.Solves) != 1 {
		t.Fatalf("unexpected results: %+v", got.Results)
	}
}

func TestGetTeamProfile_UnknownTeamIsNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{}, pgx.ErrNoRows)
	if _, err := uc.GetTeamProfile(context.Background(), eventID, teamID); !eventTeamModel.ErrEventTeamNotFound.Err().Is(err) {
		t.Fatalf("want not found, got %v", err)
	}
}

// finishedRollingEventRow is a rolling event that already finished: joining
// stays open while it runs, and closes with the finish.
func finishedRollingEventRow(eventID uuid.UUID, now time.Time) postgres.Event {
	row := rollingEventRow(eventID, now)
	row.StartAt = now.Add(-3 * time.Hour)
	row.PublishAt = now.Add(-4 * time.Hour)
	row.FinishAt = pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true}
	row.WithdrawAt = pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}
	return row
}

// The roster follows the same effective close as registration: every self
// service roster action is refused once it passed, whether joining locked at
// the start or a rolling event finished. Team creation, joining by link and
// disbanding go through the same gate as the rename/kick/transfer/leave set.
func TestTeamRoster_EveryActionIsRefusedOnceJoiningClosed(t *testing.T) {
	captainID, memberID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	rows := map[string]func(uuid.UUID, time.Time) postgres.Event{
		"late join off, started": lockedStartedEventRow,
		"late join on, finished": finishedRollingEventRow,
	}
	actions := map[string]func(uc *event.EventUseCase, eventID, teamID uuid.UUID) error{
		"create": func(uc *event.EventUseCase, eventID, _ uuid.UUID) error {
			return uc.CreateTeam(context.Background(), eventID, captainID, "Team Name")
		},
		"join by link": func(uc *event.EventUseCase, eventID, _ uuid.UUID) error {
			return uc.JoinTeam(context.Background(), eventID, memberID, "secure-join-code")
		},
		"disband": func(uc *event.EventUseCase, eventID, teamID uuid.UUID) error {
			return uc.DisbandTeam(context.Background(), eventID, teamID, captainID)
		},
		"rename": func(uc *event.EventUseCase, eventID, teamID uuid.UUID) error {
			return uc.RenameTeam(context.Background(), eventID, teamID, captainID, "New Team")
		},
		"kick": func(uc *event.EventUseCase, eventID, _ uuid.UUID) error {
			return uc.KickTeamMember(context.Background(), eventID, captainID, memberID)
		},
		"transfer": func(uc *event.EventUseCase, eventID, teamID uuid.UUID) error {
			return uc.TransferTeamCaptaincy(context.Background(), eventID, teamID, captainID, memberID)
		},
		"leave": func(uc *event.EventUseCase, eventID, _ uuid.UUID) error {
			return uc.LeaveTeam(context.Background(), eventID, memberID)
		},
	}
	for rowName, row := range rows {
		for name, act := range actions {
			t.Run(rowName+"/"+name, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				q := newFormGateMock(ctrl)
				unit := &testUoW{}
				uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
				eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
				now := time.Now()
				q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(row(eventID, now), nil)
				q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
					EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
					MaxTeamSize: 5, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
				}, nil).AnyTimes()
				q.EXPECT().LockEventForTeamChange(gomock.Any(), eventID).Return(eventID, nil).AnyTimes()
				if err := act(uc, eventID, teamID); !eventTeamModel.ErrEventTeamRosterLocked.Err().Is(err) {
					t.Fatalf("a closed join period must freeze the roster, got %v", err)
				}
				if unit.saved {
					t.Fatal("a frozen roster must not commit")
				}
			})
		}
	}
}

func formedTeamRow(teamID, eventID, captainID uuid.UUID, now time.Time, formed bool) postgres.EventTeam {
	row := postgres.EventTeam{ID: teamID, EventID: eventID, CaptainID: captainID, MemberCount: 2, CreatedAt: now, UpdatedAt: now}
	if formed {
		row.FormedAt = pgtype.Timestamptz{Time: now, Valid: true}
	}
	return row
}

// A formed team keeps its people: taking a member out (leave, kick, a
// moderator) is leaving the event, so nobody lands in another team; disbanding
// and deleting a team with members would free them and is refused.
func TestFormedTeamRemovalLeavesTheEventAndNeverSwitches(t *testing.T) {
	captainID, memberID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	actions := map[string]func(uc *event.EventUseCase, eventID, teamID uuid.UUID) error{
		"leave": func(uc *event.EventUseCase, eventID, _ uuid.UUID) error {
			return uc.LeaveTeam(context.Background(), eventID, memberID)
		},
		"kick": func(uc *event.EventUseCase, eventID, _ uuid.UUID) error {
			return uc.KickTeamMember(context.Background(), eventID, captainID, memberID)
		},
		"moderator removes": func(uc *event.EventUseCase, eventID, teamID uuid.UUID) error {
			return uc.RemoveParticipantFromTeam(context.Background(), eventID, teamID, memberID)
		},
	}
	for _, formed := range []bool{true, false} {
		for name, act := range actions {
			t.Run(fmt.Sprintf("formed=%v/%s", formed, name), func(t *testing.T) {
				ctrl := gomock.NewController(t)
				q := newFormGateMock(ctrl)
				unit := &testUoW{}
				uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
				eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
				now := time.Now()
				q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil).AnyTimes()
				q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
					EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
					MaxTeamSize: 5, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
				}, nil).AnyTimes()
				q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{
					EventID: eventID, UserID: memberID, Status: int16(participantModel.StatusApproved),
					TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, TeamRole: pgtype.Int2{Int16: int16(participantModel.TeamRoleMember), Valid: true}, CreatedAt: now,
				}, nil).AnyTimes()
				q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(formedTeamRow(teamID, eventID, captainID, now, formed), nil).AnyTimes()
				if formed && name == "leave" {
					// A member of a formed team cannot walk out alone (its roster is closed).
					if err := act(uc, eventID, teamID); !errors.Is(err, eventTeamModel.ErrEventTeamLeaveLocked.Err()) {
						t.Fatalf("leaving a formed team = %v, want ErrEventTeamLeaveLocked", err)
					}
					return
				}
				q.EXPECT().TryRemoveEventTeamMember(gomock.Any(), gomock.Any()).Return(int64(1), nil)
				q.EXPECT().ClearEventParticipantTeam(gomock.Any(), gomock.Any()).Return(int64(1), nil)
				if formed {
					q.EXPECT().RemoveEventParticipantFromEvent(gomock.Any(), gomock.Any()).Return(int64(1), nil)
				}
				if err := act(uc, eventID, teamID); err != nil {
					t.Fatalf("removal must succeed, got %v", err)
				}
				if !unit.saved {
					t.Fatal("removal must commit")
				}
			})
		}
	}
}

func TestFormedTeamCannotBeDisbandedOrDeletedWithMembers(t *testing.T) {
	captainID := uuid.Must(uuid.NewV7())
	actions := map[string]func(uc *event.EventUseCase, eventID, teamID uuid.UUID) error{
		"disband": func(uc *event.EventUseCase, eventID, teamID uuid.UUID) error {
			return uc.DisbandTeam(context.Background(), eventID, teamID, captainID)
		},
		"moderator deletes a team with members": func(uc *event.EventUseCase, eventID, teamID uuid.UUID) error {
			return uc.DeleteManagedTeam(context.Background(), eventID, teamID)
		},
	}
	for name, act := range actions {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			unit := &testUoW{}
			uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
			eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			now := time.Now()
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil).AnyTimes()
			q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
				EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
				MaxTeamSize: 5, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
			}, nil).AnyTimes()
			q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: captainID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}, nil).AnyTimes()
			q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(formedTeamRow(teamID, eventID, captainID, now, true), nil).AnyTimes()
			if err := act(uc, eventID, teamID); !eventTeamModel.ErrEventTeamSwitchLocked.Err().Is(err) {
				t.Fatalf("a formed team must not be disbanded, got %v", err)
			}
			if unit.saved {
				t.Fatal("a refused disband must not commit")
			}
		})
	}
}

func TestJoinTeam_FormedTeamIsClosedToLinkJoins(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID, teamID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
		MaxTeamSize: 5, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil).AnyTimes()
	q.EXPECT().GetEventTeamByJoinCode(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, Name: "Blue Team", JoinCode: "secure-join-code", CaptainID: uuid.Must(uuid.NewV7()),
		MemberCount: 1, CreatedAt: now, UpdatedAt: now, FormedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)
	if err := uc.JoinTeam(context.Background(), eventID, userID, "secure-join-code"); !eventTeamModel.ErrEventTeamFormed.Err().Is(err) {
		t.Fatalf("a formed team must refuse link joins, got %v", err)
	}
}

func TestFormTeam_CaptainNeedsMinimumModeratorMayForce(t *testing.T) {
	captainID, moderatorID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	cases := []struct {
		name    string
		members int32
		force   bool
		wantErr func(error) bool
	}{
		{name: "captain reaches the minimum", members: 2},
		{name: "captain below the minimum", members: 0, wantErr: func(err error) bool { return eventTeamModel.ErrEventTeamBelowMinimum.Err().Is(err) }},
		{name: "moderator forces below it", members: 0, force: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			unit := &testUoW{}
			uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
			eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			now := time.Now()
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil).AnyTimes()
			q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
				EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
				MaxTeamSize: 5, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
			}, nil).AnyTimes()
			q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{
				EventID: eventID, UserID: captainID, Status: int16(participantModel.StatusApproved),
				TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, TeamRole: pgtype.Int2{Int16: int16(participantModel.TeamRoleCaptain), Valid: true}, CreatedAt: now,
			}, nil).AnyTimes()
			row := formedTeamRow(teamID, eventID, captainID, now, false)
			row.MemberCount = tc.members
			q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(row, nil).AnyTimes()
			if tc.wantErr == nil {
				q.EXPECT().FormEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.FormEventTeamParams) (int64, error) {
					if !arg.FormedAt.Valid || arg.ID != teamID {
						t.Fatalf("unexpected formation %+v", arg)
					}
					return 1, nil
				})
				q.EXPECT().GetEventStandRollout(gomock.Any(), eventID).Return(postgres.EventStandRollout{}, pgx.ErrNoRows).AnyTimes()
				q.EXPECT().PublishAvailableTeamChallenges(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
			}
			var err error
			if tc.force {
				err = uc.FormManagedTeam(context.Background(), eventID, teamID, moderatorID)
			} else {
				err = uc.FormOwnTeam(context.Background(), eventID, captainID)
			}
			if tc.wantErr != nil {
				if !tc.wantErr(err) {
					t.Fatalf("unexpected error %v", err)
				}
				if unit.saved {
					t.Fatal("a refused formation must not commit")
				}
				return
			}
			if err != nil {
				t.Fatalf("formation must succeed, got %v", err)
			}
			if !unit.saved {
				t.Fatal("formation must commit")
			}
		})
	}
}

// New people who never had access may still join a team after the start when
// late join is on; the captain's other roster actions stay available.
func TestJoinTeam_LateJoinOfNewPersonAfterStart(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID, teamID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
		MaxTeamSize: 5, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().GetEventTeamByJoinCode(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, Name: "Blue Team", JoinCode: "secure-join-code",
		CaptainID: uuid.Must(uuid.NewV7()), MemberCount: 1, CreatedAt: now, UpdatedAt: now,
	}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), CreatedAt: now,
	}, nil)
	q.EXPECT().TryAddEventTeamMember(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().AssignEventParticipantTeam(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	if err := uc.JoinTeam(context.Background(), eventID, userID, "secure-join-code"); err != nil {
		t.Fatalf("a new person may join after the start with late join on: %v", err)
	}
}

// L15: captain_id stays on the team when a moderator rejects or moves the captain; the rights do not.
func TestCaptainActionsNeedAnApprovedCaptainOfThatTeam(t *testing.T) {
	stale := map[string]postgres.EventParticipant{
		"rejected":       {Status: 3},
		"pending":        {Status: 1},
		"another team":   {Status: 2, TeamID: uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}},
		"no team at all": {Status: 2},
	}
	actions := map[string]func(uc *event.EventUseCase, eventID, teamID, captainID uuid.UUID) error{
		"rename": func(uc *event.EventUseCase, eventID, teamID, captainID uuid.UUID) error {
			return uc.RenameTeam(context.Background(), eventID, teamID, captainID, "New Team")
		},
		"join code": func(uc *event.EventUseCase, eventID, teamID, captainID uuid.UUID) error {
			return uc.RegenerateTeamJoinCode(context.Background(), eventID, teamID, captainID, eventTeamModel.JoinCodeExpiryNone)
		},
		"disband": func(uc *event.EventUseCase, eventID, teamID, captainID uuid.UUID) error {
			return uc.DisbandTeam(context.Background(), eventID, teamID, captainID)
		},
	}
	for state, row := range stale {
		for name, act := range actions {
			t.Run(state+"/"+name, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				q := newFormGateMock(ctrl)
				unit := &testUoW{}
				uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
				eventID, teamID, captainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
				now := time.Now()
				row.EventID, row.UserID = eventID, captainID
				q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil).AnyTimes()
				q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true}}, nil).AnyTimes()
				q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{
					ID: teamID, EventID: eventID, Name: "Old", JoinCode: "secure-join-code", CaptainID: captainID, MemberCount: 2, CreatedAt: now, UpdatedAt: now,
				}, nil).AnyTimes()
				q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(row, nil).AnyTimes()
				if err := act(uc, eventID, teamID, captainID); !eventTeamModel.ErrEventTeamCaptainRequired.Err().Is(err) {
					t.Fatalf("a captain who is %s must not act, got %v", state, err)
				}
				if unit.saved {
					t.Fatal("nothing may commit")
				}
			})
		}
	}
}
