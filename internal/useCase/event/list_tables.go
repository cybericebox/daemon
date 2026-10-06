package event

import (
	"context"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/model"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	"github.com/cybericebox/daemon/pkg/pagination"
)

// Columns of the manage tables that filters and sorting may use; any other
// key must be a form field key (no "@" prefix). Sorting "@team" orders by
// the team name.
var (
	participantTableColumns = map[string]bool{"@name": true, "@email": true, "@pseudonym": true, "@status": true, "@invitation": true, "@team": true, "@date": true, "@missing": true, "@lastSeen": true, "@lastLab": true}
	teamTableColumns        = map[string]bool{"@name": true, "@captain": true, "@captainPending": true, "@members": true, "@status": true, "@created": true, "@pending": true, "@missing": true}
	participantSortAliases  = map[string]string{"@team": "@teamName"}
)

// TableSort orders a manage table by one column or form field.
type TableSort struct {
	Key  string
	Desc bool
}

// ParticipantsTableQuery is one page of the manage participants table.
type ParticipantsTableQuery struct {
	EventID  uuid.UUID
	Status   *participantModel.Status
	Kind     participantRepo.Kind
	Search   string
	Filters  []AnswerFilter
	Sort     TableSort
	Page     int
	PageSize int
}

// ParticipantsTableResult is an offset page plus the moderation tab counts.
type ParticipantsTableResult struct {
	Participants []ParticipantView
	Total        int64
	Page         int
	PageSize     int
	Counts       ParticipantCountsView
}

// TeamsTableQuery is one page of the manage teams table.
type TeamsTableQuery struct {
	EventID  uuid.UUID
	Search   string
	Filters  []AnswerFilter
	Sort     TableSort
	Page     int
	PageSize int
}

// TeamsTableResult is an offset page of team views.
type TeamsTableResult struct {
	Teams    []TeamView
	Total    int64
	Page     int
	PageSize int
}

// tableKeys validates filter and sort keys against the table's columns and
// reports whether any of them is a form field (answers must be loaded).
func tableKeys(columns map[string]bool, filters []AnswerFilter, sort TableSort) (bool, error) {
	withAnswers := false
	check := func(key string) error {
		if strings.HasPrefix(key, "@") {
			if !columns[key] {
				return ErrListQueryInvalid
			}
			return nil
		}
		if key == "" || len(key) > maxAnswerFilterKey {
			return ErrListQueryInvalid
		}
		withAnswers = true
		return nil
	}
	for _, filter := range filters {
		if err := check(filter.Key); err != nil {
			return false, err
		}
	}
	if err := check(sort.Key); err != nil {
		return false, err
	}
	return withAnswers, nil
}

func tablePage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > pagination.MaxPageSize {
		pageSize = pagination.DefaultPageSize
	}
	return page, pageSize
}

// ListParticipantsTable returns an offset page of the manage participants
// table: every column filters and sorts (newest first by default).
func (u *EventUseCase) ListParticipantsTable(ctx context.Context, q ParticipantsTableQuery) (ParticipantsTableResult, error) {
	if !q.Kind.Valid() {
		return ParticipantsTableResult{}, participantModel.ErrParticipantKindInvalid.Err()
	}
	if q.Sort.Key == "" {
		q.Sort = TableSort{Key: "@date", Desc: true}
	}
	withAnswers, err := tableKeys(participantTableColumns, q.Filters, q.Sort)
	if err != nil {
		return ParticipantsTableResult{}, err
	}
	sortKey := q.Sort.Key
	if alias, ok := participantSortAliases[sortKey]; ok {
		sortKey = alias
	}
	page, pageSize := tablePage(q.Page, q.PageSize)
	statusFilter := int32(-1)
	if q.Status != nil {
		statusFilter = int32(*q.Status)
	}
	query := participantRepo.TableQuery{
		EventID: q.EventID, StatusFilter: statusFilter, Kind: q.Kind, Search: q.Search,
		Filters: answerFiltersJSON(q.Filters), WithAnswers: withAnswers, SortKey: sortKey, SortDesc: q.Sort.Desc,
		Limit: int32(pageSize), Offset: int32((page - 1) * pageSize),
	}
	rows, err := u.participants.ListTable(ctx, query)
	if err != nil {
		return ParticipantsTableResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list participants").Err()
	}
	items, err := u.participantViews(ctx, q.EventID, rows)
	if err != nil {
		return ParticipantsTableResult{}, err
	}
	total, err := u.participants.CountTable(ctx, query)
	if err != nil {
		return ParticipantsTableResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count participants").Err()
	}
	counts, err := u.participants.CountKinds(ctx, q.EventID)
	if err != nil {
		return ParticipantsTableResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count participant tabs").Err()
	}
	return ParticipantsTableResult{
		Participants: items, Total: total, Page: page, PageSize: pageSize,
		Counts: ParticipantCountsView{Participants: counts.Participants, Applications: counts.Applications, Invitations: counts.Invitations},
	}, nil
}

// ListTeamsTable returns an offset page of the manage teams table: every
// column filters and sorts (newest first by default).
func (u *EventUseCase) ListTeamsTable(ctx context.Context, q TeamsTableQuery) (TeamsTableResult, error) {
	if q.Sort.Key == "" {
		q.Sort = TableSort{Key: "@created", Desc: true}
	}
	if _, err := tableKeys(teamTableColumns, q.Filters, q.Sort); err != nil {
		return TeamsTableResult{}, err
	}
	page, pageSize := tablePage(q.Page, q.PageSize)
	query := eventTeamRepo.TableQuery{
		EventID: q.EventID, Search: q.Search, Filters: answerFiltersJSON(q.Filters),
		SortKey: q.Sort.Key, SortDesc: q.Sort.Desc, Limit: int32(pageSize), Offset: int32((page - 1) * pageSize),
	}
	rows, err := u.teams.ListTable(ctx, query)
	if err != nil {
		return TeamsTableResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event teams").Err()
	}
	minTeamSize, err := u.teams.MinTeamSize(ctx, q.EventID)
	if err != nil {
		return TeamsTableResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get minimum team size").Err()
	}
	autoFormed, err := u.autoFormedAt(ctx, q.EventID)
	if err != nil {
		return TeamsTableResult{}, err
	}
	items, err := buildTeamViews(ctx, u.participants, q.EventID, rows, minTeamSize, autoFormed)
	if err != nil {
		return TeamsTableResult{}, err
	}
	total, err := u.teams.CountTable(ctx, query)
	if err != nil {
		return TeamsTableResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count event teams").Err()
	}
	return TeamsTableResult{Teams: items, Total: total, Page: page, PageSize: pageSize}, nil
}
