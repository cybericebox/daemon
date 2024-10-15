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

type IChallengeUseCase interface {
	GetEventChallenges(ctx context.Context, eventID uuid.UUID) ([]*model.Challenge, error)
	GetEventChallengesInfo(ctx context.Context, eventID uuid.UUID) ([]*model.CategoryInfo, error)
	AddEventChallenges(ctx context.Context, eventID, categoryID uuid.UUID, exerciseIDs []uuid.UUID) error
	DeleteEventChallenge(ctx context.Context, eventID uuid.UUID, challengeID uuid.UUID) error

	UpdateEventChallengesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error

	GetTeamsChallengeSolvedBy(ctx context.Context, eventID, challengeID uuid.UUID) ([]*model.TeamChallengeSolvedBy, error)
	SolveChallenge(ctx context.Context, eventID, challengeID uuid.UUID, solution string) (bool, error)

	GetDownloadAttachedFileLink(ctx context.Context, eventID, challengeID, fileID uuid.UUID) (string, error)
}

func (h *Handler) initChallengeAPIHandler(router *gin.RouterGroup) {
	challengeAPI := router.Group("challenges", protection.RequireProtection())
	{
		challengeAPI.GET("", h.getChallenges)
		challengeAPI.GET("info", h.getChallengesInfo) // get all challenges info
		challengeAPI.POST("", h.addChallenges)        // add all challenges from exercise to event

		challengeAPI.PATCH("order", h.updateChallengesOrder)

		singleChallengeAPI := challengeAPI.Group(":challengeID")
		{
			singleChallengeAPI.DELETE("", h.deleteChallenge)           // delete challenge
			singleChallengeAPI.POST("solve", h.solveChallenge)         // solve challenge
			singleChallengeAPI.GET("solvedBy", h.getChallengeSolvedBy) // get teams solved challenge

			singleChallengeAPI.GET("download/:fileID", h.getDownloadFileRedirect) // download attached challenge files
		}

		h.initChallengeCategoryAPIHandler(challengeAPI)
	}
}

func (h *Handler) getChallenges(ctx *gin.Context) {
	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	challenges, err := h.useCase.GetEventChallenges(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithData(ctx, challenges)
}

func (h *Handler) getChallengesInfo(ctx *gin.Context) {
	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	challenges, err := h.useCase.GetEventChallengesInfo(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithData(ctx, challenges)
}

type addExercisesToEventInput struct {
	ExerciseIDs []uuid.UUID `validate:"required,dive,uuid"`
	CategoryID  uuid.UUID   `validate:"required"`
}

func (h *Handler) addChallenges(ctx *gin.Context) {
	var inp addExercisesToEventInput

	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	if err = h.useCase.AddEventChallenges(ctx, eventID, inp.CategoryID, inp.ExerciseIDs); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

func (h *Handler) updateChallengesOrder(ctx *gin.Context) {
	var inp []model.Order

	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	if err = h.useCase.UpdateEventChallengesOrder(ctx, eventID, inp); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

func (h *Handler) deleteChallenge(ctx *gin.Context) {
	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err = h.useCase.DeleteEventChallenge(ctx, eventID, challengeID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

type solveChallengeRequest struct {
	Solution string `validate:"required"`
}

type solveChallengeResponse struct {
	Solved bool
}

func (h *Handler) solveChallenge(ctx *gin.Context) {
	var req solveChallengeRequest

	if err := ctx.BindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	solved, err := h.useCase.SolveChallenge(ctx, eventID, challengeID, req.Solution)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithData(ctx, solveChallengeResponse{Solved: solved})
}

func (h *Handler) getChallengeSolvedBy(ctx *gin.Context) {
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	teams, err := h.useCase.GetTeamsChallengeSolvedBy(ctx, eventID, challengeID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithData(ctx, teams)
}

func (h *Handler) getDownloadFileRedirect(ctx *gin.Context) {
	fileID, err := uuid.FromString(ctx.Param("fileID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	link, err := h.useCase.GetDownloadAttachedFileLink(ctx, eventID, challengeID, fileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.TemporaryRedirect(ctx, link)
}
