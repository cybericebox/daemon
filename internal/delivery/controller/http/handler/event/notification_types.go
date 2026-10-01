package event

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

type notificationVariableResponse struct {
	Name        string `json:"Name"`
	Description string `json:"Description"`
	Default     string `json:"Default"`
}

type notificationTypeResponse struct {
	Type      string                         `json:"Type"`
	Channels  []string                       `json:"Channels"`
	Variables []notificationVariableResponse `json:"Variables"`
}

// listNotificationTypes godoc
// @Summary List the notification types an Event can configure
// @Description Event-scoped types with their channels and template variables (name, description, sample value). Drives the variable hints and the in-app preview of the template editors.
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=[]notificationTypeResponse}
// @Router /events/{id}/manage/notification-types [get]
func (h *Handler) listNotificationTypes(ctx *gin.Context) {
	if _, ok := parseEventID(ctx); !ok {
		return
	}
	out := make([]notificationTypeResponse, 0)
	for _, info := range notificationTypes.Types() {
		if !notificationTypes.IsEventScoped(info.Type) {
			continue
		}
		item := notificationTypeResponse{Type: string(info.Type), Channels: make([]string, 0, len(info.Channels)), Variables: make([]notificationVariableResponse, 0)}
		for _, channel := range info.Channels {
			item.Channels = append(item.Channels, string(channel))
		}
		for _, d := range notificationTypes.Descriptors(info.Type) {
			item.Variables = append(item.Variables, notificationVariableResponse{Name: d.Name, Description: d.Description, Default: d.Default})
		}
		out = append(out, item)
	}
	response.AbortWithData(ctx, out)
}
