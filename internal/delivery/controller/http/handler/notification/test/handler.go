package test

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
	}

	// IProtection is the subset of the protection middleware this handler needs.
	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}

	IUseCase interface {
		Notify(
			ctx context.Context,
			userID uuid.UUID,
			n notificationTypes.NotificationPayload,
			opts ...dispatchModel.NotifyOption,
		) error
	}
)

// testSendRequest is the POST /test body. Variables is an arbitrary object filled
// by the admin; when empty the handler fills it from the type's descriptor defaults.
// TemplateID is optional; when set the test render uses that specific template (e.g. a
// draft) instead of the published one for the notification type.
type testSendRequest struct {
	Type       string         `json:"Type"`
	Channels   []string       `json:"Channels"`
	Variables  map[string]any `json:"Variables"`
	TemplateID *uuid.UUID     `json:"TemplateID,omitempty"`
}

func NewTestAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	router.POST("test", h.prot.RequirePermission(rbac.PermNotificationsTest), h.testSend)
}

// testSend godoc
// @Summary  Send a test notification to the current user
// @Tags     notification-test
// @Accept   json
// @Produce  json
// @Param    body  body      testSendRequest  true  "notification type, channels, and variables"
// @Success  200   {object}  response.Response
// @Failure  400   {object}  response.Response
// @Router   /notifications/test [post]
func (h *Handler) testSend(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}

	var req testSendRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	notifType := notificationTypes.NotificationType(req.Type)

	vars := req.Variables
	if len(vars) == 0 {
		vars = map[string]any{}
		for _, d := range notificationTypes.Descriptors(notifType) {
			vars[d.Name] = d.Default
		}
	}

	channels := make([]notificationTypes.NotificationChannel, 0, len(req.Channels))
	for _, c := range req.Channels {
		channels = append(channels, notificationTypes.NotificationChannel(c))
	}

	payload := notificationPayloads.DefaultPayload{
		Type:      notifType,
		Variables: vars,
		Channels:  channels,
	}

	opts := []dispatchModel.NotifyOption{dispatchModel.WithOverrideChannels(channels...)}
	if req.TemplateID != nil {
		opts = append(opts, dispatchModel.WithTemplateID(*req.TemplateID))
	}

	if err := h.useCase.Notify(ctx, userID, payload, opts...); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
