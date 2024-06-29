package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tool"
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
	challengeAPI := router.Group("challenges", protection.RequireProtection)
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
	eventID := uuid.FromStringOrNil(ctx.GetString(tool.EventIDCtxKey))
	challenges, err := h.useCase.GetEventChallenges(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithContent(ctx, challenges)
}

func (h *Handler) getChallengesInfo(ctx *gin.Context) {
	eventID := uuid.FromStringOrNil(ctx.GetString(tool.EventIDCtxKey))
	challenges, err := h.useCase.GetEventChallengesInfo(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithContent(ctx, challenges)
}

type addExercisesToEventInput struct {
	ExerciseIDs []uuid.UUID
	CategoryID  uuid.UUID
}

func (h *Handler) addExerciseToEvent(ctx *gin.Context) {
	var inp addExercisesToEventInput
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	eventID := uuid.FromStringOrNil(ctx.GetString(tool.EventIDCtxKey))

	if err := h.useCase.AddExercisesToEvent(ctx, eventID, inp.CategoryID, inp.ExerciseIDs); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithOK(ctx, "Exercises added to event successfully")
}

func (h *Handler) updateChallengesOrder(ctx *gin.Context) {
	var inp []model.Order
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	eventID := uuid.FromStringOrNil(ctx.GetString(tool.EventIDCtxKey))

	if err := h.useCase.UpdateEventChallengesOrder(ctx, eventID, inp); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithOK(ctx, "Challenges order updated successfully")
}

func (h *Handler) deleteChallenge(ctx *gin.Context) {
	eventID := uuid.FromStringOrNil(ctx.GetString(tool.EventIDCtxKey))
	challengeID := uuid.FromStringOrNil(ctx.Param("challengeID"))

	if err := h.useCase.DeleteEventChallenge(ctx, eventID, challengeID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithOK(ctx, "Challenge deleted successfully")
}

type solveChallengeRequest struct {
	Solution string
}

func (h *Handler) solveChallenge(ctx *gin.Context) {
	var req solveChallengeRequest
	if err := ctx.BindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	challengeID := uuid.FromStringOrNil(ctx.Param("challengeID"))
	eventID := uuid.FromStringOrNil(ctx.GetString(tool.EventIDCtxKey))

	solved, err := h.useCase.SolveChallenge(ctx, eventID, challengeID, req.Solution)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	if !solved {
		response.AbortWithBadRequest(ctx, model.ErrIncorrectSolution)
		return
	}

	response.AbortWithOK(ctx, "Challenge solved successfully")
}

func (h *Handler) getChallengeSolvedBy(ctx *gin.Context) {
	challengeID := uuid.FromStringOrNil(ctx.Param("challengeID"))
	eventID := uuid.FromStringOrNil(ctx.GetString(tool.EventIDCtxKey))

	teams, err := h.useCase.GetTeamsSolvedChallenge(ctx, eventID, challengeID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithContent(ctx, teams)
}
