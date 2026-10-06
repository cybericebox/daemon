// Package eventTeamRepo maps the EventTeam aggregate to generated PostgreSQL
// queries and keeps pg/sqlc details out of use cases.
package eventTeamRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
)

type Queries interface {
	fieldPolicyQueries
	ListEventTeamsTable(ctx context.Context, arg postgres.ListEventTeamsTableParams) ([]postgres.ListEventTeamsTableRow, error)
	CountEventTeamsTable(ctx context.Context, arg postgres.CountEventTeamsTableParams) (int64, error)
	CreateEventTeam(ctx context.Context, arg postgres.CreateEventTeamParams) (postgres.EventTeam, error)
	GetEventTeamByID(ctx context.Context, arg postgres.GetEventTeamByIDParams) (postgres.EventTeam, error)
	GetEventTeamByJoinCode(ctx context.Context, arg postgres.GetEventTeamByJoinCodeParams) (postgres.EventTeam, error)
	GetEventTeamForParticipant(ctx context.Context, arg postgres.GetEventTeamForParticipantParams) (postgres.EventTeam, error)
	CountEventTeams(ctx context.Context, eventID uuid.UUID) (int64, error)
	CountEventTeamsFiltered(ctx context.Context, arg postgres.CountEventTeamsFilteredParams) (int64, error)
	CountApprovedEventTeams(ctx context.Context, eventID uuid.UUID) (int64, error)
	ListEventTeams(ctx context.Context, arg postgres.ListEventTeamsParams) ([]postgres.ListEventTeamsRow, error)
	GetEventTeamAdmitted(ctx context.Context, arg postgres.GetEventTeamAdmittedParams) (bool, error)
	GetEventTeamFormed(ctx context.Context, arg postgres.GetEventTeamFormedParams) (bool, error)
	FormEventTeam(ctx context.Context, arg postgres.FormEventTeamParams) (int64, error)
	GetEventTeamVisible(ctx context.Context, arg postgres.GetEventTeamVisibleParams) (bool, error)
	SetEventTeamHidden(ctx context.Context, arg postgres.SetEventTeamHiddenParams) (int64, error)
	GetEventMinTeamSize(ctx context.Context, eventID uuid.UUID) (int32, error)
	DeleteEventTeam(ctx context.Context, arg postgres.DeleteEventTeamParams) (int64, error)
	UpdateEventTeam(ctx context.Context, arg postgres.UpdateEventTeamParams) (int64, error)
	TryAddEventTeamMember(ctx context.Context, arg postgres.TryAddEventTeamMemberParams) (int64, error)
	TryRemoveEventTeamMember(ctx context.Context, arg postgres.TryRemoveEventTeamMemberParams) (int64, error)
	GetEventTeamFieldConfig(ctx context.Context, eventID uuid.UUID) (postgres.EventTeamFieldConfig, error)
	UpsertEventTeamFieldConfig(ctx context.Context, arg postgres.UpsertEventTeamFieldConfigParams) (postgres.EventTeamFieldConfig, error)
	GetEventTeamExtraFields(ctx context.Context, arg postgres.GetEventTeamExtraFieldsParams) ([]byte, error)
	UpdateEventTeamExtraFields(ctx context.Context, arg postgres.UpdateEventTeamExtraFieldsParams) (int64, error)
	GetEventTeamByName(ctx context.Context, arg postgres.GetEventTeamByNameParams) (postgres.EventTeam, error)
}

func (r *Repository) GetFieldConfig(ctx context.Context, eventID uuid.UUID) (eventFormModel.Form, error) {
	row, err := r.q.GetEventTeamFieldConfig(ctx, eventID)
	if err != nil {
		return eventFormModel.Form{}, err
	}
	var form eventFormModel.Form
	if err = json.Unmarshal(row.Document, &form.Document); err != nil {
		return eventFormModel.Form{}, err
	}
	form.Version, form.Enabled, form.Required = row.Version, row.Enabled, row.Required
	form.RequireExisting, form.BlockSubmissions = row.RequireExisting, row.BlockSubmissions
	return form, nil
}

func (r *Repository) PutFieldConfig(ctx context.Context, eventID uuid.UUID, form eventFormModel.Form, now time.Time) (eventFormModel.Form, error) {
	document, err := json.Marshal(form.Document)
	if err != nil {
		return eventFormModel.Form{}, err
	}
	row, err := r.q.UpsertEventTeamFieldConfig(ctx, postgres.UpsertEventTeamFieldConfigParams{
		EventID: eventID, Enabled: form.Enabled, Required: form.Required, Document: document, UpdatedAt: now,
		RequireExisting: form.RequireExisting, BlockSubmissions: form.BlockSubmissions,
	})
	if err != nil {
		return eventFormModel.Form{}, err
	}
	form.Version = row.Version
	return form, nil
}

func (r *Repository) GetExtraFields(ctx context.Context, eventID, teamID uuid.UUID) (map[string]any, error) {
	encoded, err := r.q.GetEventTeamExtraFields(ctx, postgres.GetEventTeamExtraFieldsParams{EventID: eventID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	var values map[string]any
	if err = json.Unmarshal(encoded, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func (r *Repository) UpdateExtraFields(ctx context.Context, eventID, teamID uuid.UUID, values map[string]any) (int64, error) {
	encoded, err := json.Marshal(values)
	if err != nil {
		return 0, err
	}
	return r.q.UpdateEventTeamExtraFields(ctx, postgres.UpdateEventTeamExtraFieldsParams{
		EventID: eventID, TeamID: teamID, ExtraFields: encoded,
	})
}

// SaveExtraFields stores the answers together with the number of required
// fields they leave unfilled.
func (r *Repository) SaveExtraFields(ctx context.Context, eventID, teamID uuid.UUID, values map[string]any, missing int32) (int64, error) {
	encoded, err := json.Marshal(values)
	if err != nil {
		return 0, err
	}
	return r.q.UpdateEventTeamExtraFields(ctx, postgres.UpdateEventTeamExtraFieldsParams{
		EventID: eventID, TeamID: teamID, ExtraFields: encoded, FieldsMissing: pgtype.Int4{Int32: missing, Valid: true},
	})
}

func (r *Repository) Delete(ctx context.Context, eventID, teamID uuid.UUID) (int64, error) {
	return r.q.DeleteEventTeam(ctx, postgres.DeleteEventTeamParams{ID: teamID, EventID: eventID})
}

func (r *Repository) TryRemoveMember(ctx context.Context, eventID, teamID uuid.UUID, now time.Time) (int64, error) {
	return r.q.TryRemoveEventTeamMember(ctx, postgres.TryRemoveEventTeamMemberParams{
		ID: teamID, EventID: eventID, UpdatedAt: now,
	})
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func (r *Repository) Create(ctx context.Context, team eventTeamModel.EventTeam) (eventTeamModel.EventTeam, error) {
	row, err := r.q.CreateEventTeam(ctx, toCreateParams(team))
	if err != nil {
		return eventTeamModel.EventTeam{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) GetByID(ctx context.Context, eventID, teamID uuid.UUID) (eventTeamModel.EventTeam, error) {
	row, err := r.q.GetEventTeamByID(ctx, postgres.GetEventTeamByIDParams{ID: teamID, EventID: eventID})
	if err != nil {
		return eventTeamModel.EventTeam{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) GetByJoinCode(ctx context.Context, eventID uuid.UUID, joinCode string) (eventTeamModel.EventTeam, error) {
	row, err := r.q.GetEventTeamByJoinCode(ctx, postgres.GetEventTeamByJoinCodeParams{EventID: eventID, JoinCode: joinCode})
	if err != nil {
		return eventTeamModel.EventTeam{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) GetForParticipant(ctx context.Context, eventID, userID uuid.UUID) (eventTeamModel.EventTeam, error) {
	row, err := r.q.GetEventTeamForParticipant(ctx, postgres.GetEventTeamForParticipantParams{EventID: eventID, UserID: userID})
	if err != nil {
		return eventTeamModel.EventTeam{}, err
	}
	return ToDomain(row), nil
}

// ListedTeam is the management list shape: the aggregate plus its extra
// field answers and the admission computed by the SQL rule.
type ListedTeam struct {
	Team        eventTeamModel.EventTeam
	ExtraFields map[string]any
	Admitted    bool
	// FieldsMissing is how many required team fields are unfilled.
	FieldsMissing int32
}

// List returns a keyset page ordered by (created_at, id) DESC. A non-empty
// search matches the team name; admission < 0 means any, 1 admitted, 0 not;
// fieldFilters is a JSON array of team answer filters.
func (r *Repository) List(ctx context.Context, eventID uuid.UUID, search string, admission int32, fieldFilters []byte, cursorCreatedAt time.Time, cursorID uuid.UUID, limit int32) ([]ListedTeam, error) {
	rows, err := r.q.ListEventTeams(ctx, postgres.ListEventTeamsParams{EventID: eventID, Search: search, AdmissionFilter: admission, FieldFilters: jsonArray(fieldFilters), CursorCreatedAt: cursorCreatedAt, CursorID: cursorID, LimitVal: limit})
	if err != nil {
		return nil, err
	}
	out := make([]ListedTeam, 0, len(rows))
	for _, row := range rows {
		fields := map[string]any{}
		if len(row.EventTeam.ExtraFields) > 0 {
			if err = json.Unmarshal(row.EventTeam.ExtraFields, &fields); err != nil {
				return nil, err
			}
		}
		out = append(out, ListedTeam{Team: ToDomain(row.EventTeam), ExtraFields: fields, Admitted: row.Admitted, FieldsMissing: row.EventTeam.FieldsMissing})
	}
	return out, nil
}

// Admitted evaluates the SQL admission rule (event_team_admitted) for one team.
func (r *Repository) Admitted(ctx context.Context, eventID, teamID uuid.UUID) (bool, error) {
	return r.q.GetEventTeamAdmitted(ctx, postgres.GetEventTeamAdmittedParams{ID: teamID, EventID: eventID})
}

// Formed evaluates the SQL formation rule (event_team_formed) for one team.
func (r *Repository) Formed(ctx context.Context, eventID, teamID uuid.UUID) (bool, error) {
	return r.q.GetEventTeamFormed(ctx, postgres.GetEventTeamFormedParams{ID: teamID, EventID: eventID})
}

// Form stores the formation; false means the team was already formed (or gone).
func (r *Repository) Form(ctx context.Context, eventID, teamID uuid.UUID, by *uuid.UUID, at time.Time) (bool, error) {
	affected, err := r.q.FormEventTeam(ctx, postgres.FormEventTeamParams{ID: teamID, EventID: eventID, FormedAt: pgtype.Timestamptz{Time: at, Valid: true}, FormedBy: toNullUUID(by)})
	return affected > 0, err
}

// Visible evaluates the one results visibility rule (event_team_visible) for a team.
func (r *Repository) Visible(ctx context.Context, eventID, teamID uuid.UUID) (bool, error) {
	return r.q.GetEventTeamVisible(ctx, postgres.GetEventTeamVisibleParams{ID: teamID, EventID: eventID})
}

// SetHidden flips the results visibility of a team; it reports whether the
// stored flag changed (false: already in that state, a moderators team or gone).
func (r *Repository) SetHidden(ctx context.Context, eventID, teamID uuid.UUID, hidden bool, now time.Time) (bool, error) {
	affected, err := r.q.SetEventTeamHidden(ctx, postgres.SetEventTeamHiddenParams{ID: teamID, EventID: eventID, Hidden: hidden, UpdatedAt: now})
	return affected > 0, err
}

func (r *Repository) MinTeamSize(ctx context.Context, eventID uuid.UUID) (int32, error) {
	return r.q.GetEventMinTeamSize(ctx, eventID)
}

func (r *Repository) Count(ctx context.Context, eventID uuid.UUID) (int64, error) {
	return r.q.CountEventTeams(ctx, eventID)
}

// CountMatching mirrors List's search, admission and field filters.
func (r *Repository) CountMatching(ctx context.Context, eventID uuid.UUID, search string, admission int32, fieldFilters []byte) (int64, error) {
	return r.q.CountEventTeamsFiltered(ctx, postgres.CountEventTeamsFilteredParams{EventID: eventID, Search: search, AdmissionFilter: admission, FieldFilters: jsonArray(fieldFilters)})
}

// jsonArray defaults a missing filter list to an empty JSON array.
func jsonArray(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("[]")
	}
	return raw
}

func (r *Repository) CountApproved(ctx context.Context, eventID uuid.UUID) (int64, error) {
	return r.q.CountApprovedEventTeams(ctx, eventID)
}

func (r *Repository) Update(ctx context.Context, team eventTeamModel.EventTeam, expectedUpdatedAt time.Time) (int64, error) {
	return r.q.UpdateEventTeam(ctx, postgres.UpdateEventTeamParams{
		ID: team.ID, EventID: team.EventID, Name: team.Name, JoinCode: team.JoinCode, JoinCodeExpiresAt: toTimestamptz(team.JoinCodeExpiresAt),
		CaptainID: team.CaptainID, Hidden: team.Hidden, MemberCount: team.MemberCount,
		AdmittedManually: team.AdmittedManually, AdmissionLocked: team.AdmissionLocked,
		UpdatedAt: team.UpdatedAt, ExpectedUpdatedAt: expectedUpdatedAt,
	})
}

func (r *Repository) TryAddMember(ctx context.Context, eventID, teamID uuid.UUID, maxTeamSize int32, now time.Time) (int64, error) {
	return r.q.TryAddEventTeamMember(ctx, postgres.TryAddEventTeamMemberParams{
		ID: teamID, EventID: eventID, MaxTeamSize: maxTeamSize, UpdatedAt: now,
	})
}

func toCreateParams(team eventTeamModel.EventTeam) postgres.CreateEventTeamParams {
	return postgres.CreateEventTeamParams{
		ID: team.ID, EventID: team.EventID, Name: team.Name, JoinCode: team.JoinCode,
		CaptainID: team.CaptainID, Hidden: team.Hidden, MemberCount: team.MemberCount,
		CreatedAt: team.CreatedAt, UpdatedAt: team.UpdatedAt,
		Individual: team.Individual, AdmittedManually: team.AdmittedManually, AdmissionLocked: team.AdmissionLocked,
		FormedAt: toTimestamptz(team.FormedAt),
	}
}

func ToDomain(row postgres.EventTeam) eventTeamModel.EventTeam {
	return eventTeamModel.EventTeam{
		ID: row.ID, EventID: row.EventID, Name: row.Name, JoinCode: row.JoinCode, JoinCodeExpiresAt: fromTimestamptz(row.JoinCodeExpiresAt),
		CaptainID: row.CaptainID, Hidden: row.Hidden, MemberCount: row.MemberCount,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		Individual: row.Individual, AdmittedManually: row.AdmittedManually, AdmissionLocked: row.AdmissionLocked,
		Moderators: row.Moderators,
		FormedAt:   fromTimestamptz(row.FormedAt), FormedBy: fromNullUUID(row.FormedBy),
	}
}

// GetByName finds a team by its per-event unique name.
func (r *Repository) GetByName(ctx context.Context, eventID uuid.UUID, name string) (eventTeamModel.EventTeam, error) {
	row, err := r.q.GetEventTeamByName(ctx, postgres.GetEventTeamByNameParams{EventID: eventID, Name: name})
	if err != nil {
		return eventTeamModel.EventTeam{}, err
	}
	return ToDomain(row), nil
}

// TableQuery is one page of the manage teams table (offset mode): Filters is
// a JSON array for event_answers_match_all over the team document and
// SortKey one of its keys.
type TableQuery struct {
	EventID  uuid.UUID
	Search   string
	Filters  []byte
	SortKey  string
	SortDesc bool
	Limit    int32
	Offset   int32
}

func (r *Repository) ListTable(ctx context.Context, q TableQuery) ([]ListedTeam, error) {
	dir := "asc"
	if q.SortDesc {
		dir = "desc"
	}
	rows, err := r.q.ListEventTeamsTable(ctx, postgres.ListEventTeamsTableParams{
		EventID: q.EventID, Search: q.Search, Filters: jsonArray(q.Filters), SortDir: dir, SortKey: q.SortKey,
		OffsetVal: q.Offset, LimitVal: q.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ListedTeam, 0, len(rows))
	for _, row := range rows {
		fields := map[string]any{}
		if len(row.EventTeam.ExtraFields) > 0 {
			if err = json.Unmarshal(row.EventTeam.ExtraFields, &fields); err != nil {
				return nil, err
			}
		}
		out = append(out, ListedTeam{Team: ToDomain(row.EventTeam), ExtraFields: fields, Admitted: row.Admitted, FieldsMissing: row.EventTeam.FieldsMissing})
	}
	return out, nil
}

// CountTable mirrors ListTable's filters.
func (r *Repository) CountTable(ctx context.Context, q TableQuery) (int64, error) {
	return r.q.CountEventTeamsTable(ctx, postgres.CountEventTeamsTableParams{EventID: q.EventID, Search: q.Search, Filters: jsonArray(q.Filters)})
}

func toTimestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func fromTimestamptz(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	at := t.Time
	return &at
}

func toNullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil || *id == uuid.Nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func fromNullUUID(id uuid.NullUUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	v := id.UUID
	return &v
}
