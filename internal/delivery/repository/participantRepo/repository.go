// Package participantRepo is the repository for the per-(event,user)
// Participant aggregate: it accepts/returns whole domain entities and keeps
// all pgtype/sqlc mapping (nullable decided_at, status filter sentinel) out
// of the business layer.
package participantRepo

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	ListEventParticipantsTable(ctx context.Context, arg postgres.ListEventParticipantsTableParams) ([]postgres.ListEventParticipantsTableRow, error)
	CountEventParticipantsTable(ctx context.Context, arg postgres.CountEventParticipantsTableParams) (int64, error)
	UpsertEventParticipant(ctx context.Context, arg postgres.UpsertEventParticipantParams) (postgres.EventParticipant, error)
	GetEventParticipant(ctx context.Context, arg postgres.GetEventParticipantParams) (postgres.EventParticipant, error)
	InviteEventParticipant(ctx context.Context, arg postgres.InviteEventParticipantParams) (postgres.EventParticipant, error)
	RemoveEventParticipantFromEvent(ctx context.Context, arg postgres.RemoveEventParticipantFromEventParams) (int64, error)
	UpdateEventParticipant(ctx context.Context, arg postgres.UpdateEventParticipantParams) (int64, error)
	AssignEventParticipantTeam(ctx context.Context, arg postgres.AssignEventParticipantTeamParams) (int64, error)
	ClearEventParticipantTeam(ctx context.Context, arg postgres.ClearEventParticipantTeamParams) (int64, error)
	SetEventParticipantTeamRole(ctx context.Context, arg postgres.SetEventParticipantTeamRoleParams) (int64, error)
	ListEventParticipantsDetailed(ctx context.Context, arg postgres.ListEventParticipantsDetailedParams) ([]postgres.ListEventParticipantsDetailedRow, error)
	CountEventParticipants(ctx context.Context, arg postgres.CountEventParticipantsParams) (int64, error)
	CountEventParticipantKinds(ctx context.Context, eventID uuid.UUID) (postgres.CountEventParticipantKindsRow, error)
	SetEventParticipantPseudonym(ctx context.Context, arg postgres.SetEventParticipantPseudonymParams) (int64, error)
	DeleteEventParticipantInvitation(ctx context.Context, arg postgres.DeleteEventParticipantInvitationParams) (int64, error)
	MarkEventParticipantInvitationSent(ctx context.Context, arg postgres.MarkEventParticipantInvitationSentParams) (int64, error)
	CountPendingTeamInvitations(ctx context.Context, arg postgres.CountPendingTeamInvitationsParams) (int64, error)
	ListEventTeamMembers(ctx context.Context, arg postgres.ListEventTeamMembersParams) ([]postgres.ListEventTeamMembersRow, error)
	ListPendingTeamInvitations(ctx context.Context, arg postgres.ListPendingTeamInvitationsParams) ([]postgres.ListPendingTeamInvitationsRow, error)
	GetEventParticipantProfile(ctx context.Context, arg postgres.GetEventParticipantProfileParams) (postgres.GetEventParticipantProfileRow, error)
	GetEventParticipantDetail(ctx context.Context, arg postgres.GetEventParticipantDetailParams) (postgres.GetEventParticipantDetailRow, error)
	ListOwnTeamMembers(ctx context.Context, arg postgres.ListOwnTeamMembersParams) ([]postgres.ListOwnTeamMembersRow, error)
	ListOwnTeamPendingInvitees(ctx context.Context, arg postgres.ListOwnTeamPendingInviteesParams) ([]postgres.ListOwnTeamPendingInviteesRow, error)
	SetEventParticipantInvitedTeam(ctx context.Context, arg postgres.SetEventParticipantInvitedTeamParams) (int64, error)
	TouchEventParticipantPresence(ctx context.Context, arg postgres.TouchEventParticipantPresenceParams) error
	ListEventUsersLastActivity(ctx context.Context, arg postgres.ListEventUsersLastActivityParams) ([]postgres.ListEventUsersLastActivityRow, error)
}

// Kind narrows the moderation list to one tab. The empty kind lists all rows.
type Kind string

const (
	KindAll          Kind = ""
	KindParticipants Kind = "participants"
	KindApplications Kind = "applications"
	KindInvitations  Kind = "invitations"
)

func (k Kind) Valid() bool {
	return k == KindAll || k == KindParticipants || k == KindApplications || k == KindInvitations
}

// Listed is the moderation read shape: the aggregate plus profile and
// resolved names, produced by one query per page.
type Listed struct {
	Participant     participantModel.Participant
	FirstName       string
	LastName        string
	Email           string
	DisplayName     string
	TeamName        string
	TeamHidden      bool
	InvitedTeamName string
	// FieldsMissing is how many required registration fields are unfilled.
	FieldsMissing int32
	// LastSeenAt is the last request on the event (nil: never online);
	// LastLabAt the last laboratory access over the VPN or the proxy (nil: never).
	LastSeenAt *time.Time
	LastLabAt  *time.Time
}

type KindCounts struct {
	Participants, Applications, Invitations int64
}

type TeamMember struct {
	TeamID              uuid.UUID
	UserID              uuid.UUID
	Role                participantModel.TeamRole
	Pseudonym           *string
	FirstName, LastName string
	Email               string
	LastSeenAt          *time.Time
	LastLabAt           *time.Time
}

type PendingTeamInvitation struct {
	TeamID              uuid.UUID
	UserID              uuid.UUID
	CreatedAt           time.Time
	InvitationSentAt    *time.Time
	FirstName, LastName string
	Email               string
}

type Profile struct {
	Pseudonym           *string
	FirstName, LastName string
	DisplayName         string
}

func (r *Repository) ClearTeam(ctx context.Context, eventID, userID, teamID uuid.UUID) (int64, error) {
	return r.q.ClearEventParticipantTeam(ctx, postgres.ClearEventParticipantTeamParams{
		EventID: eventID, UserID: userID, TeamID: uuid.NullUUID{UUID: teamID, Valid: true},
	})
}

func (r *Repository) SetTeamRole(ctx context.Context, eventID, userID, teamID uuid.UUID, role participantModel.TeamRole) (int64, error) {
	return r.q.SetEventParticipantTeamRole(ctx, postgres.SetEventParticipantTeamRoleParams{
		EventID: eventID, UserID: userID, TeamID: uuid.NullUUID{UUID: teamID, Valid: true},
		TeamRole: pgtype.Int2{Int16: int16(role), Valid: true},
	})
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

func decidedAtToDB(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// Upsert inserts the row; the bool reports whether a row was actually
// created (false when an (event,user) row already existed — ON CONFLICT DO
// NOTHING returns no row, surfacing as pgx.ErrNoRows).
func (r *Repository) Upsert(ctx context.Context, p participantModel.Participant) (participantModel.Participant, bool, error) {
	row, err := r.q.UpsertEventParticipant(ctx, postgres.UpsertEventParticipantParams{
		EventID:   p.EventID,
		UserID:    p.UserID,
		Status:    int16(p.Status),
		CreatedAt: p.CreatedAt,
		DecidedAt: decidedAtToDB(p.DecidedAt),
		DecidedBy: p.DecidedBy,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return participantModel.Participant{}, false, nil
		}
		return participantModel.Participant{}, false, err
	}
	return ToDomain(row), true, nil
}

func (r *Repository) Get(ctx context.Context, eventID, userID uuid.UUID) (participantModel.Participant, error) {
	row, err := r.q.GetEventParticipant(ctx, postgres.GetEventParticipantParams{
		EventID: eventID,
		UserID:  userID,
	})
	if err != nil {
		return participantModel.Participant{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) Invite(ctx context.Context, eventID, userID, invitedBy uuid.UUID, targetTeamID uuid.NullUUID, now time.Time) (participantModel.Participant, bool, error) {
	row, err := r.q.InviteEventParticipant(ctx, postgres.InviteEventParticipantParams{
		EventID: eventID, UserID: userID, InvitedBy: uuid.NullUUID{UUID: invitedBy, Valid: true}, InvitedTeamID: targetTeamID, CreatedAt: now,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return participantModel.Participant{}, false, nil
	}
	if err != nil {
		return participantModel.Participant{}, false, err
	}
	return ToDomain(row), true, nil
}

// Update writes the decision columns in one statement (status/decided_at/
// decided_by) — the (event_id, user_id) pair is the primary key, so this is
// a whole-row write for the mutable part of the aggregate.
func (r *Repository) Update(ctx context.Context, p participantModel.Participant) (int64, error) {
	return r.q.UpdateEventParticipant(ctx, postgres.UpdateEventParticipantParams{
		EventID:   p.EventID,
		UserID:    p.UserID,
		Status:    int16(p.Status),
		DecidedAt: decidedAtToDB(p.DecidedAt),
		DecidedBy: p.DecidedBy,
	})
}

func (r *Repository) AssignTeam(ctx context.Context, p participantModel.Participant) (int64, error) {
	if p.TeamID == nil || p.TeamRole == nil {
		return 0, participantModel.ErrParticipantTeamInvalid.Err()
	}
	return r.q.AssignEventParticipantTeam(ctx, postgres.AssignEventParticipantTeamParams{
		EventID: p.EventID, UserID: p.UserID,
		TeamID:   uuid.NullUUID{UUID: *p.TeamID, Valid: true},
		TeamRole: pgtype.Int2{Int16: int16(*p.TeamRole), Valid: true},
	})
}

// List returns a keyset page ordered by (created_at, user_id) DESC.
// statusFilter < 0 means "any status"; kind narrows to one moderation tab;
// a non-empty search matches name, email or pseudonym; fieldFilters is a JSON
// array of registration answer filters.
func (r *Repository) List(ctx context.Context, eventID uuid.UUID, statusFilter int32, kind Kind, search string, fieldFilters []byte, cursorCreatedAt time.Time, cursorID uuid.UUID, limit int32) ([]Listed, error) {
	rows, err := r.q.ListEventParticipantsDetailed(ctx, postgres.ListEventParticipantsDetailedParams{
		EventID:         eventID,
		StatusFilter:    statusFilter,
		Kind:            string(kind),
		Search:          search,
		FieldFilters:    jsonArray(fieldFilters),
		CursorCreatedAt: cursorCreatedAt,
		CursorID:        cursorID,
		LimitVal:        limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Listed, len(rows))
	for i, row := range rows {
		out[i] = toListed(row)
	}
	return out, nil
}

func toListed(row postgres.ListEventParticipantsDetailedRow) Listed {
	return Listed{
		Participant: ToDomain(postgres.EventParticipant{
			EventID: row.EventID, UserID: row.UserID, Status: row.Status, CreatedAt: row.CreatedAt,
			DecidedAt: row.DecidedAt, DecidedBy: row.DecidedBy, TeamID: row.TeamID, TeamRole: row.TeamRole,
			InvitedBy: row.InvitedBy, Invited: row.Invited, InvitedTeamID: row.InvitedTeamID,
			InvitedToTeam: row.InvitedToTeam, Pseudonym: row.Pseudonym, InvitationSentAt: row.InvitationSentAt,
		}),
		FirstName: row.FirstName, LastName: row.LastName, Email: row.Email, DisplayName: row.DisplayName,
		TeamName: row.TeamName, TeamHidden: row.TeamHidden, InvitedTeamName: row.InvitedTeamName,
		FieldsMissing: row.FieldsMissing,
		LastSeenAt:    timePtr(row.LastSeenAt), LastLabAt: labAtPtr(row.LastLabAt),
	}
}

// Count mirrors List's statusFilter and kind semantics.
func (r *Repository) Count(ctx context.Context, eventID uuid.UUID, statusFilter int32, kind Kind) (int64, error) {
	return r.CountMatching(ctx, eventID, statusFilter, kind, "", nil)
}

// jsonArray defaults a missing filter list to an empty JSON array.
func jsonArray(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("[]")
	}
	return raw
}

// CountMatching mirrors List's statusFilter, kind, search and field filters.
func (r *Repository) CountMatching(ctx context.Context, eventID uuid.UUID, statusFilter int32, kind Kind, search string, fieldFilters []byte) (int64, error) {
	return r.q.CountEventParticipants(ctx, postgres.CountEventParticipantsParams{
		EventID:      eventID,
		StatusFilter: statusFilter,
		Kind:         string(kind),
		Search:       search,
		FieldFilters: jsonArray(fieldFilters),
	})
}

func (r *Repository) CountKinds(ctx context.Context, eventID uuid.UUID) (KindCounts, error) {
	row, err := r.q.CountEventParticipantKinds(ctx, eventID)
	if err != nil {
		return KindCounts{}, err
	}
	return KindCounts{Participants: row.Participants, Applications: row.Applications, Invitations: row.Invitations}, nil
}

// SetPseudonym is a narrow column write: the pseudonym is independent of the
// decision and team columns, and its uniqueness is a database index.
func (r *Repository) SetPseudonym(ctx context.Context, eventID, userID uuid.UUID, pseudonym *string) (int64, error) {
	value := pgtype.Text{}
	if pseudonym != nil {
		value = pgtype.Text{String: *pseudonym, Valid: true}
	}
	return r.q.SetEventParticipantPseudonym(ctx, postgres.SetEventParticipantPseudonymParams{EventID: eventID, UserID: userID, Pseudonym: value})
}

// DeleteInvitation removes a still pending invitation (revoke or decline).
func (r *Repository) DeleteInvitation(ctx context.Context, eventID, userID uuid.UUID) (int64, error) {
	return r.q.DeleteEventParticipantInvitation(ctx, postgres.DeleteEventParticipantInvitationParams{EventID: eventID, UserID: userID})
}

func (r *Repository) MarkInvitationSent(ctx context.Context, eventID, userID uuid.UUID, sentAt time.Time) (int64, error) {
	return r.q.MarkEventParticipantInvitationSent(ctx, postgres.MarkEventParticipantInvitationSentParams{EventID: eventID, UserID: userID, SentAt: pgtype.Timestamptz{Time: sentAt, Valid: true}})
}

func (r *Repository) CountPendingTeamInvitations(ctx context.Context, eventID, teamID uuid.UUID) (int64, error) {
	return r.q.CountPendingTeamInvitations(ctx, postgres.CountPendingTeamInvitationsParams{EventID: eventID, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}})
}

func (r *Repository) TeamMembers(ctx context.Context, eventID uuid.UUID, teamIDs []uuid.UUID) ([]TeamMember, error) {
	rows, err := r.q.ListEventTeamMembers(ctx, postgres.ListEventTeamMembersParams{EventID: eventID, TeamIds: teamIDs})
	if err != nil {
		return nil, err
	}
	out := make([]TeamMember, 0, len(rows))
	for _, row := range rows {
		member := TeamMember{TeamID: row.TeamID, UserID: row.UserID, Role: participantModel.TeamRoleMember, Pseudonym: textPtr(row.Pseudonym), FirstName: row.FirstName, LastName: row.LastName, Email: row.Email, LastSeenAt: timePtr(row.LastSeenAt), LastLabAt: labAtPtr(row.LastLabAt)}
		if row.TeamRole.Valid {
			member.Role = participantModel.TeamRole(row.TeamRole.Int16)
		}
		out = append(out, member)
	}
	return out, nil
}

func (r *Repository) PendingTeamInvitations(ctx context.Context, eventID uuid.UUID, teamIDs []uuid.UUID) ([]PendingTeamInvitation, error) {
	rows, err := r.q.ListPendingTeamInvitations(ctx, postgres.ListPendingTeamInvitationsParams{EventID: eventID, TeamIds: teamIDs})
	if err != nil {
		return nil, err
	}
	out := make([]PendingTeamInvitation, 0, len(rows))
	for _, row := range rows {
		out = append(out, PendingTeamInvitation{TeamID: row.TeamID, UserID: row.UserID, CreatedAt: row.CreatedAt, InvitationSentAt: timePtr(row.InvitationSentAt), FirstName: row.FirstName, LastName: row.LastName, Email: row.Email})
	}
	return out, nil
}

func (r *Repository) Profile(ctx context.Context, eventID, userID uuid.UUID) (Profile, error) {
	row, err := r.q.GetEventParticipantProfile(ctx, postgres.GetEventParticipantProfileParams{EventID: eventID, UserID: userID})
	if err != nil {
		return Profile{}, err
	}
	return Profile{Pseudonym: textPtr(row.Pseudonym), FirstName: row.FirstName, LastName: row.LastName, DisplayName: row.DisplayName}, nil
}

func textPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

func timePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}

// ToDomain maps a sqlc row to the domain entity. decided_at/decided_by are
// nullable at the column level (unset until a decision is made) and forgiven
// as nil/invalid.
func ToDomain(row postgres.EventParticipant) participantModel.Participant {
	p := participantModel.Participant{
		EventID:          row.EventID,
		UserID:           row.UserID,
		Status:           participantModel.Status(row.Status),
		CreatedAt:        row.CreatedAt,
		DecidedBy:        row.DecidedBy,
		InvitedBy:        row.InvitedBy,
		Invited:          row.Invited,
		InvitedToTeam:    row.InvitedToTeam,
		Pseudonym:        textPtr(row.Pseudonym),
		InvitationSentAt: timePtr(row.InvitationSentAt),
	}
	if row.InvitedTeamID.Valid {
		teamID := row.InvitedTeamID.UUID
		p.InvitedTeamID = &teamID
	}
	if row.DecidedAt.Valid {
		t := row.DecidedAt.Time
		p.DecidedAt = &t
	}
	if row.TeamID.Valid {
		teamID := row.TeamID.UUID
		p.TeamID = &teamID
	}
	if row.TeamRole.Valid {
		role := participantModel.TeamRole(row.TeamRole.Int16)
		p.TeamRole = &role
	}
	return p
}

// RosterMember is one member of a team roster with the member's public name.
type RosterMember struct {
	UserID      uuid.UUID
	Role        participantModel.TeamRole
	DisplayName string
}

// TeamRoster lists one team's members, captain first, then by public name.
func (r *Repository) TeamRoster(ctx context.Context, eventID, teamID uuid.UUID) ([]RosterMember, error) {
	rows, err := r.q.ListOwnTeamMembers(ctx, postgres.ListOwnTeamMembersParams{EventID: eventID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	out := make([]RosterMember, 0, len(rows))
	for _, row := range rows {
		role := participantModel.TeamRoleMember
		if row.TeamRole.Valid {
			role = participantModel.TeamRole(row.TeamRole.Int16)
		}
		out = append(out, RosterMember{UserID: row.UserID, Role: role, DisplayName: row.DisplayName})
	}
	return out, nil
}

// TeamPendingInvitees lists the invitees of one team who have not accepted
// yet, by public name.
func (r *Repository) TeamPendingInvitees(ctx context.Context, eventID, teamID uuid.UUID) ([]RosterMember, error) {
	rows, err := r.q.ListOwnTeamPendingInvitees(ctx, postgres.ListOwnTeamPendingInviteesParams{EventID: eventID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	out := make([]RosterMember, 0, len(rows))
	for _, row := range rows {
		out = append(out, RosterMember{UserID: row.UserID, Role: participantModel.TeamRoleMember, DisplayName: row.DisplayName})
	}
	return out, nil
}

// SetInvitedTeam moves a still pending invitation to a team; 0 rows means the
// invitation was accepted, declined or revoked meanwhile.
func (r *Repository) SetInvitedTeam(ctx context.Context, eventID, userID, teamID uuid.UUID) (int64, error) {
	return r.q.SetEventParticipantInvitedTeam(ctx, postgres.SetEventParticipantInvitedTeamParams{EventID: eventID, UserID: userID, TeamID: teamID})
}

// TableQuery is one page of the manage participants table (offset mode):
// Filters is a JSON array for event_answers_match_all over the row document
// and SortKey one of its keys; WithAnswers loads registration answers into
// the document (needed only when a filter or the sort uses a form field).
type TableQuery struct {
	EventID      uuid.UUID
	StatusFilter int32
	Kind         Kind
	Search       string
	Filters      []byte
	WithAnswers  bool
	SortKey      string
	SortDesc     bool
	Limit        int32
	Offset       int32
}

func sortDir(desc bool) string {
	if desc {
		return "desc"
	}
	return "asc"
}

func (r *Repository) ListTable(ctx context.Context, q TableQuery) ([]Listed, error) {
	rows, err := r.q.ListEventParticipantsTable(ctx, postgres.ListEventParticipantsTableParams{
		WithAnswers: q.WithAnswers, EventID: q.EventID, StatusFilter: q.StatusFilter, Kind: string(q.Kind),
		Search: q.Search, Filters: jsonArray(q.Filters), SortDir: sortDir(q.SortDesc), SortKey: q.SortKey,
		OffsetVal: q.Offset, LimitVal: q.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Listed, len(rows))
	for i, row := range rows {
		out[i] = toListed(postgres.ListEventParticipantsDetailedRow(row))
	}
	return out, nil
}

// CountTable mirrors ListTable's filters.
func (r *Repository) CountTable(ctx context.Context, q TableQuery) (int64, error) {
	return r.q.CountEventParticipantsTable(ctx, postgres.CountEventParticipantsTableParams{
		WithAnswers: q.WithAnswers, EventID: q.EventID, StatusFilter: q.StatusFilter, Kind: string(q.Kind),
		Search: q.Search, Filters: jsonArray(q.Filters),
	})
}

// RemoveFromEvent stores the participant's exit from the event (see
// Participant.RemoveFromEvent).
func (r *Repository) RemoveFromEvent(ctx context.Context, p participantModel.Participant) (int64, error) {
	return r.q.RemoveEventParticipantFromEvent(ctx, postgres.RemoveEventParticipantFromEventParams{EventID: p.EventID, UserID: p.UserID, DecidedAt: decidedAtToDB(p.DecidedAt)})
}

// labAtPtr maps the epoch the queries use for "never in a laboratory" to nil.
func labAtPtr(value time.Time) *time.Time {
	if value.IsZero() || value.Unix() == 0 {
		return nil
	}
	return &value
}

// Activity is when a user was last online on an event and last in one of its
// laboratories; each is nil when it never happened.
type Activity struct {
	LastSeenAt *time.Time
	LastLabAt  *time.Time
}

// TouchPresence records that the user was online on the event at seenAt; a
// time within a minute of the stored one is ignored.
func (r *Repository) TouchPresence(ctx context.Context, eventID, userID uuid.UUID, seenAt time.Time) error {
	return r.q.TouchEventParticipantPresence(ctx, postgres.TouchEventParticipantPresenceParams{EventID: eventID, UserID: userID, SeenAt: seenAt})
}

// Activities returns the activity of each given user, keyed by user ID.
func (r *Repository) Activities(ctx context.Context, eventID uuid.UUID, userIDs []uuid.UUID) (map[uuid.UUID]Activity, error) {
	out := make(map[uuid.UUID]Activity, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.ListEventUsersLastActivity(ctx, postgres.ListEventUsersLastActivityParams{EventID: eventID, UserIds: userIDs})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.UserID] = Activity{LastSeenAt: timePtr(row.LastSeenAt), LastLabAt: labAtPtr(row.LastLabAt)}
	}
	return out, nil
}
