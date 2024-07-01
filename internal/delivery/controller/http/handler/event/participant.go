package event

import "github.com/gin-gonic/gin"

type IParticipantUseCase interface {
}

func (h *Handler) initParticipantAPIHandler(router *gin.RouterGroup) {
	participantsAPI := router.Group("participants")
	{
		participantsAPI.GET("")
	}
}
