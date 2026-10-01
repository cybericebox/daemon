package eventTeamRepo

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
)

type fakeQueries struct {
	fieldPolicyQueries // not exercised here
	created            postgres.CreateEventTeamParams
	listed             postgres.ListEventTeamsParams
	counted            postgres.CountEventTeamsFilteredParams
	table              postgres.ListEventTeamsTableParams
}

func (q *fakeQueries) ListEventTeamsTable(_ context.Context, arg postgres.ListEventTeamsTableParams) ([]postgres.ListEventTeamsTableRow, error) {
	q.table = arg
	return []postgres.ListEventTeamsTableRow{{EventTeam: postgres.EventTeam{ID: arg.EventID, EventID: arg.EventID, Name: "Blue", ExtraFields: []byte(`{"city":"Київ"}`)}, Admitted: true}}, nil
}
func (q *fakeQueries) CountEventTeamsTable(context.Context, postgres.CountEventTeamsTableParams) (int64, error) {
	return 1, nil
}

func (q *fakeQueries) CreateEventTeam(_ context.Context, arg postgres.CreateEventTeamParams) (postgres.EventTeam, error) {
	q.created = arg
	return postgres.EventTeam{ID: arg.ID, EventID: arg.EventID, Name: arg.Name, JoinCode: arg.JoinCode,
		CaptainID: arg.CaptainID, Hidden: arg.Hidden, MemberCount: arg.MemberCount,
		CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt, Individual: arg.Individual}, nil
}

func (q *fakeQueries) GetEventTeamByID(context.Context, postgres.GetEventTeamByIDParams) (postgres.EventTeam, error) {
	return postgres.EventTeam{}, nil
}
func (q *fakeQueries) GetEventTeamByJoinCode(context.Context, postgres.GetEventTeamByJoinCodeParams) (postgres.EventTeam, error) {
	return postgres.EventTeam{}, nil
}
func (q *fakeQueries) GetEventTeamForParticipant(context.Context, postgres.GetEventTeamForParticipantParams) (postgres.EventTeam, error) {
	return postgres.EventTeam{}, nil
}
func (q *fakeQueries) CountEventTeams(context.Context, uuid.UUID) (int64, error) { return 0, nil }
func (q *fakeQueries) CountEventTeamsFiltered(_ context.Context, arg postgres.CountEventTeamsFilteredParams) (int64, error) {
	q.counted = arg
	return 0, nil
}
func (q *fakeQueries) CountApprovedEventTeams(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (q *fakeQueries) ListEventTeams(_ context.Context, arg postgres.ListEventTeamsParams) ([]postgres.ListEventTeamsRow, error) {
	q.listed = arg
	return nil, nil
}
func (q *fakeQueries) GetEventTeamFormed(context.Context, postgres.GetEventTeamFormedParams) (bool, error) {
	return true, nil
}
func (q *fakeQueries) FormEventTeam(context.Context, postgres.FormEventTeamParams) (int64, error) {
	return 1, nil
}
func (q *fakeQueries) GetEventTeamAdmitted(context.Context, postgres.GetEventTeamAdmittedParams) (bool, error) {
	return true, nil
}
func (q *fakeQueries) GetEventTeamVisible(context.Context, postgres.GetEventTeamVisibleParams) (bool, error) {
	return true, nil
}
func (q *fakeQueries) SetEventTeamHidden(context.Context, postgres.SetEventTeamHiddenParams) (int64, error) {
	return 1, nil
}
func (q *fakeQueries) GetEventMinTeamSize(context.Context, uuid.UUID) (int32, error) { return 1, nil }
func (q *fakeQueries) DeleteEventTeam(context.Context, postgres.DeleteEventTeamParams) (int64, error) {
	return 0, nil
}
func (q *fakeQueries) UpdateEventTeam(context.Context, postgres.UpdateEventTeamParams) (int64, error) {
	return 0, nil
}
func (q *fakeQueries) TryAddEventTeamMember(context.Context, postgres.TryAddEventTeamMemberParams) (int64, error) {
	return 0, nil
}
func (q *fakeQueries) TryRemoveEventTeamMember(context.Context, postgres.TryRemoveEventTeamMemberParams) (int64, error) {
	return 0, nil
}
func (q *fakeQueries) GetEventTeamFieldConfig(context.Context, uuid.UUID) (postgres.EventTeamFieldConfig, error) {
	return postgres.EventTeamFieldConfig{}, nil
}
func (q *fakeQueries) UpsertEventTeamFieldConfig(context.Context, postgres.UpsertEventTeamFieldConfigParams) (postgres.EventTeamFieldConfig, error) {
	return postgres.EventTeamFieldConfig{}, nil
}
func (q *fakeQueries) GetEventTeamExtraFields(context.Context, postgres.GetEventTeamExtraFieldsParams) ([]byte, error) {
	return []byte(`{}`), nil
}
func (q *fakeQueries) UpdateEventTeamExtraFields(context.Context, postgres.UpdateEventTeamExtraFieldsParams) (int64, error) {
	return 0, nil
}
func (q *fakeQueries) GetEventTeamByName(context.Context, postgres.GetEventTeamByNameParams) (postgres.EventTeam, error) {
	return postgres.EventTeam{}, nil
}

func TestCreateMapsWholeTeam(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	team, err := eventTeamModel.New(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), "Blue Team", "secure-join-code", now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fake := &fakeQueries{}
	created, err := New(fake).Create(context.Background(), team)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if fake.created.ID != team.ID || fake.created.MemberCount != 1 || created != team {
		t.Fatalf("team mapping mismatch: params=%+v created=%+v", fake.created, created)
	}
}

func TestListAndCountPassSearchAndAdmission(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	cursorID := uuid.Must(uuid.NewV7())
	cursorAt := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	fake := &fakeQueries{}
	repo := New(fake)
	if _, err := repo.List(context.Background(), eventID, "blue", 1, nil, cursorAt, cursorID, 21); err != nil {
		t.Fatalf("List: %v", err)
	}
	want := postgres.ListEventTeamsParams{EventID: eventID, Search: "blue", AdmissionFilter: 1, FieldFilters: []byte("[]"), CursorCreatedAt: cursorAt, CursorID: cursorID, LimitVal: 21}
	if !reflect.DeepEqual(fake.listed, want) {
		t.Fatalf("List params = %+v, want %+v", fake.listed, want)
	}
	if _, err := repo.CountMatching(context.Background(), eventID, "blue", 0, []byte(`[{"key":"a","op":"bool","value":true}]`)); err != nil {
		t.Fatalf("CountMatching: %v", err)
	}
	if want := (postgres.CountEventTeamsFilteredParams{EventID: eventID, Search: "blue", AdmissionFilter: 0, FieldFilters: []byte(`[{"key":"a","op":"bool","value":true}]`)}); !reflect.DeepEqual(fake.counted, want) {
		t.Fatalf("CountMatching params = %+v, want %+v", fake.counted, want)
	}
}

func TestListTableMapsSortAndOffset(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	fake := &fakeQueries{}
	rows, err := New(fake).ListTable(context.Background(), TableQuery{EventID: eventID, SortKey: "@members", SortDesc: true, Limit: 25, Offset: 50})
	if err != nil {
		t.Fatalf("ListTable: %v", err)
	}
	want := postgres.ListEventTeamsTableParams{EventID: eventID, Filters: []byte("[]"), SortDir: "desc", SortKey: "@members", OffsetVal: 50, LimitVal: 25}
	if !reflect.DeepEqual(fake.table, want) {
		t.Fatalf("params = %+v, want %+v", fake.table, want)
	}
	if len(rows) != 1 || rows[0].Team.Name != "Blue" || rows[0].ExtraFields["city"] != "Київ" || !rows[0].Admitted {
		t.Fatalf("rows = %+v", rows)
	}
}
