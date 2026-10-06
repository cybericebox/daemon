package event

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	utils "github.com/cybericebox/daemon/internal/delivery/controller/http/utils"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/cybericebox/daemon/pkg/pagination"
)

// maxListSearchLength bounds the free-text filter of management lists.
const maxListSearchLength = 100

var errListSearchTooLong = errors.New("search is too long")

// listSearch reads the trimmed `search` query parameter of a management list.
func listSearch(ctx *gin.Context) (string, error) {
	search := strings.TrimSpace(ctx.Query("search"))
	if utf8.RuneCountInString(search) > maxListSearchLength {
		return "", errListSearchTooLong
	}
	return search, nil
}

var errTableSortInvalid = errors.New("invalid sort")

// tableSort reads `sortBy` (a column or form field key) and `sortDir`.
func tableSort(ctx *gin.Context) (eventUseCase.TableSort, error) {
	key := strings.TrimSpace(ctx.Query("sortBy"))
	switch ctx.Query("sortDir") {
	case "", "asc":
		return eventUseCase.TableSort{Key: key}, nil
	case "desc":
		return eventUseCase.TableSort{Key: key, Desc: true}, nil
	default:
		return eventUseCase.TableSort{}, errTableSortInvalid
	}
}

// tableQueryError answers a bad table query with 400 and anything else as
// the use case error.
func tableQueryError(ctx *gin.Context, err error) {
	if errors.Is(err, eventUseCase.ErrListQueryInvalid) {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	response.AbortWithError(ctx, err)
}

// participantsTablePageResponse is the offset page plus the tab counters.
type participantsTablePageResponse struct {
	pagination.OffsetPage[participantResponse]
	Counts participantCountsResponse
}

// listParticipantsTable is the offset mode of listParticipants (`page` set):
// filters and sorting over every column.
func (h *Handler) listParticipantsTable(ctx *gin.Context, id uuid.UUID, status *participantModel.Status, kind participantRepo.Kind, search string, fields []eventUseCase.AnswerFilter) {
	page, err := utils.GetOffsetPaginationParams(ctx)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	sort, err := tableSort(ctx)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	res, err := h.useCase.ListParticipantsTable(ctx, eventUseCase.ParticipantsTableQuery{
		EventID: id, Status: status, Kind: kind, Search: search, Filters: fields, Sort: sort, Page: page.Page, PageSize: page.PageSize,
	})
	if err != nil {
		tableQueryError(ctx, err)
		return
	}
	items := make([]participantResponse, 0, len(res.Participants))
	for _, p := range res.Participants {
		items = append(items, toParticipantResponse(p))
	}
	response.AbortWithData(ctx, participantsTablePageResponse{
		OffsetPage: pagination.NewOffsetPage(items, res.Total, res.Page, res.PageSize),
		Counts:     participantCountsResponse{Participants: res.Counts.Participants, Applications: res.Counts.Applications, Invitations: res.Counts.Invitations},
	})
}

// listTeamsTable is the offset mode of listTeams (`page` set): filters and
// sorting over every column.
func (h *Handler) listTeamsTable(ctx *gin.Context, id uuid.UUID, search string, fields []eventUseCase.AnswerFilter) {
	page, err := utils.GetOffsetPaginationParams(ctx)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	sort, err := tableSort(ctx)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	res, err := h.useCase.ListTeamsTable(ctx, eventUseCase.TeamsTableQuery{EventID: id, Search: search, Filters: fields, Sort: sort, Page: page.Page, PageSize: page.PageSize})
	if err != nil {
		tableQueryError(ctx, err)
		return
	}
	items := make([]teamResponse, 0, len(res.Teams))
	for _, team := range res.Teams {
		items = append(items, toTeamResponse(team))
	}
	response.AbortWithData(ctx, pagination.NewOffsetPage(items, res.Total, res.Page, res.PageSize))
}
