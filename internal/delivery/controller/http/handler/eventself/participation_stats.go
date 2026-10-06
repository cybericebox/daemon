package eventself

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type participationSolveResponse struct {
	EventChallengeID uuid.UUID `json:"EventChallengeID"`
	ChallengeName    string    `json:"ChallengeName"`
	Category         string    `json:"Category"`
	Points           int32     `json:"Points"`
	SolvedAt         time.Time `json:"SolvedAt"`
	// SolvedByUserID is null when the solving attempt is unknown.
	SolvedByUserID *uuid.UUID `json:"SolvedByUserID"`
	SolvedByName   string     `json:"SolvedByName"`
	FirstBlood     bool       `json:"FirstBlood"`
}

type participationMemberResponse struct {
	UserID          uuid.UUID `json:"UserID"`
	Name            string    `json:"Name"`
	Role            int16     `json:"Role"`
	JoinedAt        time.Time `json:"JoinedAt"`
	Points          int64     `json:"Points"`
	Solves          int32     `json:"Solves"`
	FirstBloods     int32     `json:"FirstBloods"`
	Attempts        int64     `json:"Attempts"`
	CorrectAttempts int64     `json:"CorrectAttempts"`
	Hints           int64     `json:"Hints"`
}

type participationTeamResponse struct {
	TeamID          uuid.UUID                     `json:"TeamID"`
	TeamName        string                        `json:"TeamName"`
	Attempts        int64                         `json:"Attempts"`
	CorrectAttempts int64                         `json:"CorrectAttempts"`
	Hints           int64                         `json:"Hints"`
	FirstBloods     int32                         `json:"FirstBloods"`
	Solves          []participationSolveResponse  `json:"Solves"`
	Members         []participationMemberResponse `json:"Members"`
}

type participationStatsResponse struct {
	// Rank is 0 when the results are not shown to the caller or the team is not ranked.
	Rank     int32                           `json:"Rank"`
	Points   int64                           `json:"Points"`
	Solved   int64                           `json:"Solved"`
	Frozen   bool                            `json:"Frozen"`
	Team     participationTeamResponse       `json:"Team"`
	Me       participationMemberResponse     `json:"Me"`
	Timeline []ownScoreTimelineEntryResponse `json:"Timeline"`
}

func toParticipationMemberResponse(v eventUseCase.ParticipationMemberView) participationMemberResponse {
	return participationMemberResponse{UserID: v.UserID, Name: v.Name, Role: v.Role, JoinedAt: v.JoinedAt, Points: v.Points, Solves: v.Solves, FirstBloods: v.FirstBloods,
		Attempts: v.Attempts, CorrectAttempts: v.CorrectAttempts, Hints: v.Hints}
}

func toParticipationStatsResponse(v eventUseCase.ParticipationStatsView) participationStatsResponse {
	team := participationTeamResponse{TeamID: v.Team.TeamID, TeamName: v.Team.TeamName, Attempts: v.Team.Attempts, CorrectAttempts: v.Team.CorrectAttempts, Hints: v.Team.Hints,
		FirstBloods: v.Team.FirstBloods, Solves: make([]participationSolveResponse, 0, len(v.Team.Solves)), Members: make([]participationMemberResponse, 0, len(v.Team.Members))}
	for _, s := range v.Team.Solves {
		item := participationSolveResponse{EventChallengeID: s.EventChallengeID, ChallengeName: s.ChallengeName, Category: s.Category, Points: s.Points, SolvedAt: s.SolvedAt, SolvedByName: s.SolvedByName, FirstBlood: s.FirstBlood}
		if s.SolvedByUserID != uuid.Nil {
			by := s.SolvedByUserID
			item.SolvedByUserID = &by
		}
		team.Solves = append(team.Solves, item)
	}
	for _, m := range v.Team.Members {
		team.Members = append(team.Members, toParticipationMemberResponse(m))
	}
	timeline := make([]ownScoreTimelineEntryResponse, 0, len(v.Timeline))
	for _, item := range v.Timeline {
		timeline = append(timeline, toOwnScoreTimelineEntryResponse(item))
	}
	return participationStatsResponse{Rank: v.Rank, Points: v.Points, Solved: v.Solved, Frozen: v.Freeze, Team: team, Me: toParticipationMemberResponse(v.Me), Timeline: timeline}
}

// participationStats godoc
// @Summary Get the caller's participation statistics: own team results, members' contributions and solves
// @Description Scoped to the caller's own team only. Carries counters and solved tasks, never submitted answers; the rank follows the results visibility and freeze rules. The ETag lets a poll revalidate cheaply.
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Param as query string false "moderators: an event manager reads the results of the hidden moderators team (organizers' preview)"
// @Success 200 {object} response.Response{data=participationStatsResponse}
// @Router /events/{id}/results/participation [get]
func (h *Handler) participationStats(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	read := h.useCase.GetParticipationStats
	if ctx.Query("as") == "moderators" {
		read = h.useCase.GetModeratorsParticipationStats
	}
	view, err := read(ctx, eventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	abortWithETagData(ctx, toParticipationStatsResponse(view))
}
