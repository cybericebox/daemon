package event

import (
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type batchTeamMemberRequest struct {
	Email     string `json:"Email"`
	FirstName string `json:"FirstName"`
	LastName  string `json:"LastName"`
	// Fields prefill the participant form answers (CSV import).
	Fields map[string]any `json:"Fields"`
}

type batchTeamRequest struct {
	Name         string                   `json:"Name"`
	CaptainEmail string                   `json:"CaptainEmail"`
	Members      []batchTeamMemberRequest `json:"Members"`
	Fields       map[string]any           `json:"Fields"`
}

type createManagedTeamsRequest struct {
	Teams []batchTeamRequest `json:"Teams" binding:"required"`
	// DryRun validates and counts without writing or sending anything.
	DryRun bool `json:"DryRun"`
	// Import marks a CSV import: team and member fields are checked by type
	// only, required ones may stay empty.
	Import bool `json:"Import"`
}

type batchTeamIssueResponse struct {
	// Team is the index in the request; -1 is about the whole batch.
	Team  int    `json:"Team"`
	Email string `json:"Email,omitempty"`
	Code  string `json:"Code"`
}

type batchTeamOutcomeResponse struct {
	ID      uuid.UUID `json:"ID"`
	Name    string    `json:"Name"`
	Created bool      `json:"Created"`
}

type createManagedTeamsResponse struct {
	Issues    []batchTeamIssueResponse   `json:"Issues"`
	Teams     []batchTeamOutcomeResponse `json:"Teams"`
	Assigned  int                        `json:"Assigned"`
	Invited   int                        `json:"Invited"`
	Unchanged int                        `json:"Unchanged"`
	NotSent   []string                   `json:"NotSent"`
}

// @Summary Create event teams with members and invitations (moderator)
// @Description One transaction: with any issue nothing is written and Issues lists them. Approved participants join at once; other addresses get a pending team invitation (a pending account is created for unknown ones). Idempotent per address.
// @Tags events
// @Accept json
// @Param id path string true "event ID"
// @Param body body createManagedTeamsRequest true "teams"
// @Success 200 {object} response.Response{data=createManagedTeamsResponse}
// @Router /events/{id}/manage/teams/batch [post]
func (h *Handler) createManagedTeams(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req createManagedTeamsRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	in := make([]eventUseCase.BatchTeamInput, 0, len(req.Teams))
	for _, team := range req.Teams {
		members := make([]eventUseCase.BatchTeamMemberInput, 0, len(team.Members))
		for _, member := range team.Members {
			members = append(members, eventUseCase.BatchTeamMemberInput{Email: member.Email, FirstName: member.FirstName, LastName: member.LastName, Fields: member.Fields})
		}
		in = append(in, eventUseCase.BatchTeamInput{Name: team.Name, CaptainEmail: team.CaptainEmail, Members: members, Fields: team.Fields, Partial: req.Import})
	}
	result, err := h.useCase.CreateManagedTeams(ctx, eventID, claims.UserID, in, req.DryRun)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := createManagedTeamsResponse{
		Issues: make([]batchTeamIssueResponse, 0, len(result.Issues)), Teams: make([]batchTeamOutcomeResponse, 0, len(result.Teams)),
		Assigned: result.Assigned, Invited: result.Invited, Unchanged: result.Unchanged, NotSent: result.NotSent,
	}
	if out.NotSent == nil {
		out.NotSent = []string{}
	}
	for _, issue := range result.Issues {
		out.Issues = append(out.Issues, batchTeamIssueResponse{Team: issue.Team, Email: issue.Email, Code: issue.Code})
	}
	for _, team := range result.Teams {
		out.Teams = append(out.Teams, batchTeamOutcomeResponse{ID: team.ID, Name: team.Name, Created: team.Created})
	}
	response.AbortWithData(ctx, out)
}
