package event_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

func TestListParticipantsTable_DefaultsSortAndMapsQuery(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	q.EXPECT().ListEventParticipantsTable(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ListEventParticipantsTableParams) ([]postgres.ListEventParticipantsTableRow, error) {
			if arg.SortKey != "@date" || arg.SortDir != "desc" || arg.OffsetVal != 25 || arg.LimitVal != 25 || arg.WithAnswers || arg.Kind != "participants" {
				t.Fatalf("unexpected params: %+v", arg)
			}
			if string(arg.Filters) != `[{"key":"@status","op":"any","values":["2"]}]` {
				t.Fatalf("filters = %s", arg.Filters)
			}
			return nil, nil
		})
	q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, pgx.ErrNoRows).AnyTimes()
	q.EXPECT().CountEventParticipantsTable(gomock.Any(), gomock.Any()).Return(int64(26), nil)
	q.EXPECT().CountEventParticipantKinds(gomock.Any(), gomock.Any()).Return(postgres.CountEventParticipantKindsRow{Participants: 26}, nil)

	res, err := uc.ListParticipantsTable(context.Background(), event.ParticipantsTableQuery{
		EventID: eventID, Kind: participantRepo.KindParticipants, Page: 2, PageSize: 25,
		Filters: []event.AnswerFilter{{Key: "@status", Op: event.AnswerFilterAny, Values: []string{"2"}}},
	})
	if err != nil {
		t.Fatalf("ListParticipantsTable: %v", err)
	}
	if res.Total != 26 || res.Page != 2 || res.PageSize != 25 || res.Counts.Participants != 26 {
		t.Fatalf("result = %+v", res)
	}
}

func TestListParticipantsTable_AnswerKeysLoadAnswersAndTeamSortsByName(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	q.EXPECT().ListEventParticipantsTable(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ListEventParticipantsTableParams) ([]postgres.ListEventParticipantsTableRow, error) {
			if !arg.WithAnswers || arg.SortKey != "@teamName" || arg.SortDir != "asc" {
				t.Fatalf("unexpected params: %+v", arg)
			}
			return nil, nil
		})
	q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, pgx.ErrNoRows).AnyTimes()
	q.EXPECT().CountEventParticipantsTable(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	q.EXPECT().CountEventParticipantKinds(gomock.Any(), gomock.Any()).Return(postgres.CountEventParticipantKindsRow{}, nil)

	_, err := uc.ListParticipantsTable(context.Background(), event.ParticipantsTableQuery{
		EventID: uuid.Must(uuid.NewV7()), Kind: participantRepo.KindAll, Sort: event.TableSort{Key: "@team"},
		Filters: []event.AnswerFilter{{Key: "city", Op: event.AnswerFilterContains, Value: "Київ"}},
	})
	if err != nil {
		t.Fatalf("ListParticipantsTable: %v", err)
	}
}

func TestListTablesRejectUnknownColumns(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	_, err := uc.ListParticipantsTable(context.Background(), event.ParticipantsTableQuery{EventID: eventID, Kind: participantRepo.KindAll, Sort: event.TableSort{Key: "@members"}})
	if !errors.Is(err, event.ErrListQueryInvalid) {
		t.Fatalf("participants: want ErrListQueryInvalid, got %v", err)
	}
	_, err = uc.ListTeamsTable(context.Background(), event.TeamsTableQuery{EventID: eventID, Filters: []event.AnswerFilter{{Key: "@email", Op: event.AnswerFilterContains, Value: "x"}}})
	if !errors.Is(err, event.ErrListQueryInvalid) {
		t.Fatalf("teams: want ErrListQueryInvalid, got %v", err)
	}
}

func TestListTeamsTable_DefaultsToNewestFirst(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, nil).AnyTimes()
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	q.EXPECT().ListEventTeamsTable(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ListEventTeamsTableParams) ([]postgres.ListEventTeamsTableRow, error) {
			if arg.SortKey != "@created" || arg.SortDir != "desc" || arg.OffsetVal != 0 || arg.LimitVal != 20 {
				t.Fatalf("unexpected params: %+v", arg)
			}
			return nil, nil
		})
	q.EXPECT().CountEventTeamsTable(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	res, err := uc.ListTeamsTable(context.Background(), event.TeamsTableQuery{EventID: eventID})
	if err != nil || res.Page != 1 || res.PageSize != 20 {
		t.Fatalf("ListTeamsTable = %+v, %v", res, err)
	}
}
