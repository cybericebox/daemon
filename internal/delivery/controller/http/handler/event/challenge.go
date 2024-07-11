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
	AddExercisesToEvent(ctx context.Context, eventID, categoryID uuid.UUID, exerciseIDs []uuid.UUID) error
	DeleteEventChallenge(ctx context.Context, eventID uuid.UUID, challengeID uuid.UUID) error

	UpdateEventChallengesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error

	GetTeamsSolvedChallenge(ctx context.Context, eventID, challengeID uuid.UUID) ([]*model.TeamSolvedChallenge, error)
	SolveChallenge(ctx context.Context, eventID, challengeID uuid.UUID, solution string) (bool, error)
}

func (h *Handler) initChallengeAPIHandler(router *gin.RouterGroup) {
	challengeAPI := router.Group("challenges", protection.RequireProtection())
	{
		challengeAPI.GET("", h.getChallenges)
		challengeAPI.GET("info", h.getChallengesInfo) // get all challenges info
		challengeAPI.POST("", h.addExerciseToEvent)   // add all challenges from exercise to event

		challengeAPI.PATCH("order", h.updateChallengesOrder)

		singleChallengeAPI := challengeAPI.Group(":challengeID")
		{
			singleChallengeAPI.DELETE("", h.deleteChallenge)           // delete challenge
			singleChallengeAPI.POST("solve", h.solveChallenge)         // solve challenge
			singleChallengeAPI.GET("solvedBy", h.getChallengeSolvedBy) // get teams solved challenge
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

	response.AbortWithContent(ctx, challenges)
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

	response.AbortWithContent(ctx, challenges)
}

type addExercisesToEventInput struct {
	ExerciseIDs []uuid.UUID `validate:"required,dive,uuid"`
	CategoryID  uuid.UUID   `validate:"required"`
}

func (h *Handler) addExerciseToEvent(ctx *gin.Context) {
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

	if err = h.useCase.AddExercisesToEvent(ctx, eventID, inp.CategoryID, inp.ExerciseIDs); err != nil {
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

	code := model.SolutionRejected
	if solved {
		code = model.SolutionAccepted
	}

	response.AbortWithCode(ctx, code)
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

	teams, err := h.useCase.GetTeamsSolvedChallenge(ctx, eventID, challengeID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithContent(ctx, teams)
}
