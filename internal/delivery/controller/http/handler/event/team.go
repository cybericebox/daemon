package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

type ITeamUseCase interface {
	GetEventTeams(ctx context.Context, eventID uuid.UUID) ([]*model.Team, error)
	GetEventTeamsInfo(ctx context.Context, eventID uuid.UUID) ([]*model.TeamInfo, error)
	CreateTeam(ctx context.Context, eventID uuid.UUID, name string) error
	JoinTeam(ctx context.Context, eventID uuid.UUID, name, joinCode string) error
	GetVPNConfig(ctx context.Context, eventID uuid.UUID) (string, error)
	GetSelfTeam(ctx context.Context, eventID uuid.UUID) (*model.Team, error)
}

func (h *Handler) initTeamAPIHandler(router *gin.RouterGroup) {
	teamAPI := router.Group("teams")
	{
		teamAPI.GET("", protection.RequireProtection, h.getTeams) // get teams
		teamAPI.GET("info", h.getTeamsInfo)                       // get teams info only

		teamAPI.POST("", protection.RequireProtection, h.createTeam)   // create team
		teamAPI.POST("join", protection.RequireProtection, h.joinTeam) // join team

		// self team
		selfTeamAPI := teamAPI.Group("self", protection.RequireProtection)
		{
			selfTeamAPI.GET("", h.getSelfTeam)            // get team
			selfTeamAPI.GET("vpn-config", h.getVPNConfig) // get vpn config
		}

	}
}

func (h *Handler) getTeams(ctx *gin.Context) {
	eventID := uuid.FromStringOrNil(ctx.GetString(tools.EventIDCtxKey))
	teams, err := h.useCase.GetEventTeams(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithContent(ctx, teams)
}

func (h *Handler) getTeamsInfo(ctx *gin.Context) {
	eventID := uuid.FromStringOrNil(ctx.GetString(tools.EventIDCtxKey))
	teams, err := h.useCase.GetEventTeamsInfo(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithContent(ctx, teams)
}

type createTeamInput struct {
	Name string `json:"name"`
}

func (h *Handler) createTeam(ctx *gin.Context) {
	eventID := uuid.FromStringOrNil(ctx.GetString(tools.EventIDCtxKey))
	var inp createTeamInput
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.CreateTeam(ctx, eventID, inp.Name); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithOK(ctx, "Team created successfully")
}

type joinTeamInput struct {
	Name     string
	JoinCode string
}

func (h *Handler) joinTeam(ctx *gin.Context) {
	eventID := uuid.FromStringOrNil(ctx.GetString(tools.EventIDCtxKey))
	var inp joinTeamInput
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.JoinTeam(ctx, eventID, inp.Name, inp.JoinCode); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithOK(ctx, "Team joined successfully")
}

func (h *Handler) getSelfTeam(ctx *gin.Context) {
	eventID := uuid.FromStringOrNil(ctx.GetString(tools.EventIDCtxKey))
	team, err := h.useCase.GetSelfTeam(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithContent(ctx, team)
}

func (h *Handler) getVPNConfig(ctx *gin.Context) {
	eventID := uuid.FromStringOrNil(ctx.GetString(tools.EventIDCtxKey))
	cfg, err := h.useCase.GetVPNConfig(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithContent(ctx, cfg)
}
