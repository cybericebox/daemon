package event_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
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

func pseudonymConfig(eventID uuid.UUID, allowed bool) postgres.EventConfig {
	now := time.Now()
	return postgres.EventConfig{EventID: eventID, AllowPseudonyms: allowed, MaxTeamSize: 5, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}
}

func TestSetOwnPseudonym(t *testing.T) {
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	name := "Frost"
	for _, tc := range []struct {
		name    string
		allowed bool
		started bool
		saveErr error
		want    error
	}{
		{name: "saved", allowed: true},
		{name: "disabled", want: participantModel.ErrPseudonymsDisabled.Err()},
		{name: "after start", allowed: true, started: true, want: participantModel.ErrPseudonymLocked.Err()},
		{name: "taken", allowed: true, saveErr: &pgconn.PgError{Code: pgerrcode.UniqueViolation}, want: participantModel.ErrPseudonymTaken.Err()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := newFormGateMock(gomock.NewController(t))
			uc := newUC(q)
			q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved)}, nil)
			q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(pseudonymConfig(eventID, tc.allowed), nil)
			row := rosterOpenEventRow(eventID, time.Now())
			if tc.started {
				row = startedEvent(eventID, time.Now())
			}
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(row, nil)
			if tc.want == nil || tc.saveErr != nil {
				q.EXPECT().SetEventParticipantPseudonym(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.SetEventParticipantPseudonymParams) (int64, error) {
					if !arg.Pseudonym.Valid || arg.Pseudonym.String != name {
						t.Fatalf("pseudonym write = %+v", arg)
					}
					return 1, tc.saveErr
				})
			}
			view, err := uc.SetOwnPseudonym(context.Background(), eventID, userID, &name)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
				return
			}
			if err != nil || view.DisplayName == "" {
				t.Fatalf("SetOwnPseudonym = %+v, %v", view, err)
			}
		})
	}
}

func TestListOwnChallengesRejectsTeamBelowMinimum(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := newUC(q)
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true},
	}, nil)
	q.EXPECT().GetEventTeamAdmitted(gomock.Any(), postgres.GetEventTeamAdmittedParams{ID: teamID, EventID: eventID}).Return(false, nil)
	if _, err := uc.ListOwnChallenges(context.Background(), eventID, userID); !errors.Is(err, eventTeamModel.ErrEventTeamNotAdmitted.Err()) {
		t.Fatalf("non-admitted team err = %v", err)
	}
}

func TestSetTeamAdmissionMarksManualAdmission(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, nil).AnyTimes()
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, Infra: &recordingLabAccessInfra{}})
	eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Name: "Blue", CaptainID: uuid.Must(uuid.NewV7()), MemberCount: 1, CreatedAt: now, UpdatedAt: now}, nil)
	q.EXPECT().UpdateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventTeamParams) (int64, error) {
		if !arg.AdmittedManually || arg.ExpectedUpdatedAt != now {
			t.Fatalf("manual admission write = %+v", arg)
		}
		return 1, nil
	})
	q.EXPECT().GetEventTeamExtraFields(gomock.Any(), gomock.Any()).Return([]byte(`{}`), nil)
	q.EXPECT().RequestEventLabAccessSync(gomock.Any(), gomock.Any()).Return(postgres.EventLabAccessSync{EventTeamID: teamID}, nil)
	q.EXPECT().AdvanceEventResultRevision(gomock.Any(), gomock.Any()).Return(postgres.AdvanceEventResultRevisionRow{Revision: 1, UpdatedAt: now}, nil)
	q.EXPECT().CreateEventResultChange(gomock.Any(), gomock.Any()).Return(postgres.EventResultChange{EventID: eventID, Revision: 1, Kind: "scoreboard_recalculated", CreatedAt: now}, nil)
	view, err := uc.SetTeamAdmission(context.Background(), eventID, teamID, true)
	if err != nil || !view.AdmittedManually || !unit.saved {
		t.Fatalf("SetTeamAdmission = %+v, %v", view, err)
	}
}

func TestUpdateOwnTeamFieldsAllowsOnlyEditableFields(t *testing.T) {
	eventID, teamID, captainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	document, err := json.Marshal(eventContentModel.Document{Blocks: []eventContentModel.Block{
		{ID: "school", Type: eventContentModel.BlockField, Key: "school", Input: "text", Label: "School"},
		{ID: "motto", Type: eventContentModel.BlockField, Key: "motto", Input: "text", Label: "Motto", Editable: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	q := newFormGateMock(gomock.NewController(t))
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	now := time.Now()
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)
	q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Name: "Blue", CaptainID: captainID, MemberCount: 2, CreatedAt: now, UpdatedAt: now}, nil)
	q.EXPECT().GetEventTeamFieldConfig(gomock.Any(), eventID).Return(postgres.EventTeamFieldConfig{EventID: eventID, Enabled: true, Document: document}, nil)
	q.EXPECT().GetEventTeamExtraFields(gomock.Any(), gomock.Any()).Return([]byte(`{"school":"KPI"}`), nil)
	_, err = uc.UpdateOwnTeamFields(context.Background(), eventID, teamID, captainID, map[string]any{"school": "Other"})
	if !errors.Is(err, eventTeamModel.ErrEventTeamFieldNotEditable.Err()) || unit.saved {
		t.Fatalf("non-editable field err = %v saved=%v", err, unit.saved)
	}
}

func TestIndividualApprovalIgnoresMaxTeams(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, Infra: &recordingLabAccessInfra{}})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	config := pseudonymConfig(eventID, false)
	config.Participation = pgtype.Int2{Int16: int16(eventConfigModel.ParticipationIndividual), Valid: true}
	config.MaxTeams = pgtype.Int4{Int32: 1, Valid: true}
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(participantModel.StatusPending), CreatedAt: now}, nil)
	q.EXPECT().UpdateEventParticipant(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(config, nil).AnyTimes()
	q.EXPECT().LockEventForTeamChange(gomock.Any(), eventID).Return(eventID, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), CreatedAt: now}, nil)
	q.EXPECT().CreateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateEventTeamParams) (postgres.EventTeam, error) {
		if !arg.Individual {
			t.Fatalf("individual team must be marked: %+v", arg)
		}
		return postgres.EventTeam{ID: arg.ID, EventID: arg.EventID, Name: arg.Name, CaptainID: arg.CaptainID, MemberCount: 1, Individual: true, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
	})
	q.EXPECT().AssignEventParticipantTeam(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().RequestEventLabAccessSync(gomock.Any(), gomock.Any()).Return(postgres.EventLabAccessSync{}, nil)
	if err := uc.ApproveParticipant(context.Background(), eventID, userID, uuid.Must(uuid.NewV7())); err != nil {
		t.Fatalf("individual approval must not be blocked by MaxTeams (no CountEventTeams call): %v", err)
	}
}

func TestSetTeamHiddenAdvancesRevisionOnlyWhenChanged(t *testing.T) {
	for name, changed := range map[string]bool{"toggled": true, "already in that state": false} {
		t.Run(name, func(t *testing.T) {
			q := newFormGateMock(gomock.NewController(t))
			q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, nil).AnyTimes()
			unit := &testUoW{}
			uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
			eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			now := time.Now()
			q.EXPECT().GetModeratorsTeam(gomock.Any(), eventID).Return(postgres.EventTeam{ID: uuid.Must(uuid.NewV7()), Moderators: true, Hidden: true}, nil)
			q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Name: "Blue", CaptainID: uuid.Must(uuid.NewV7()), MemberCount: 1, Hidden: !changed, CreatedAt: now, UpdatedAt: now}, nil)
			affected := int64(0)
			if changed {
				affected = 1
				q.EXPECT().AdvanceEventResultRevision(gomock.Any(), gomock.Any()).Return(postgres.AdvanceEventResultRevisionRow{Revision: 1, UpdatedAt: now}, nil)
				q.EXPECT().CreateEventResultChange(gomock.Any(), gomock.Any()).Return(postgres.EventResultChange{EventID: eventID, Revision: 1, Kind: "scoreboard_recalculated", CreatedAt: now}, nil)
			}
			q.EXPECT().SetEventTeamHidden(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.SetEventTeamHiddenParams) (int64, error) {
				if arg.ID != teamID || !arg.Hidden {
					t.Fatalf("hidden write = %+v", arg)
				}
				return affected, nil
			})
			q.EXPECT().GetEventTeamExtraFields(gomock.Any(), gomock.Any()).Return([]byte(`{}`), nil)
			view, err := uc.SetTeamHidden(context.Background(), eventID, teamID, true)
			if err != nil || !view.Hidden || !unit.saved {
				t.Fatalf("SetTeamHidden = %+v, %v", view, err)
			}
		})
	}
}

// The moderators team is always hidden and cannot be toggled or edited.
func TestSetTeamHiddenRefusesModeratorsTeam(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetModeratorsTeam(gomock.Any(), eventID).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Moderators: true, Hidden: true}, nil)
	if _, err := uc.SetTeamHidden(context.Background(), eventID, teamID, false); !errors.Is(err, eventTeamModel.ErrEventTeamModeratorsLocked.Err()) || unit.saved {
		t.Fatalf("err=%v saved=%v", err, unit.saved)
	}
}
