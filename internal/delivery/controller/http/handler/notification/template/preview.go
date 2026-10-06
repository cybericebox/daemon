package template

import (
	"encoding/json"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

// emailPreviewRequest is a template draft to render; it need not be saved.
type emailPreviewRequest struct {
	NotificationType string          `json:"NotificationType"`
	Subject          string          `json:"Subject"`
	Preheader        string          `json:"Preheader"`
	Body             json.RawMessage `json:"Body"             swaggertype:"object"`
	Styling          json.RawMessage `json:"Styling"          swaggertype:"object"`
	// Values are sample variable values; missing variables use the type's
	// defaults.
	Values map[string]string `json:"Values"`
}

// emailPreviewResponse is the rendered draft. HTML is the dispatched HTML
// (hidden preheader span included) with inline images embedded as data: URIs
// instead of cid: parts, so it displays without any image request.
type emailPreviewResponse struct {
	Subject   string `json:"Subject"`
	Preheader string `json:"Preheader"`
	HTML      string `json:"HTML"`
}

func (r emailPreviewRequest) toInput() emailUseCase.PreviewInput {
	return emailUseCase.PreviewInput{
		NotificationType: r.NotificationType,
		Subject:          r.Subject,
		Preheader:        r.Preheader,
		Body:             r.Body,
		Styling:          r.Styling,
		Values:           r.Values,
	}
}

// previewEmail godoc
// @Summary      Preview an email template draft
// @Description  Renders subject, preheader and body with the dispatch renderer and sample variables (type defaults overlaid by Values). Images are embedded as data: URIs instead of cid: parts. A missing/non-image file or inline images over the per-email cap are 400. Nothing is persisted.
// @Tags         notification-templates
// @Accept       json
// @Produce      json
// @Param        body  body      emailPreviewRequest  true  "template draft"
// @Success      200   {object}  response.Response{data=emailPreviewResponse}
// @Failure      400   {object}  response.Response
// @Router       /notifications/templates/email/preview [post]
func (h *Handler) previewEmail(ctx *gin.Context) {
	var req emailPreviewRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	out, err := h.useCase.PreviewEmail(ctx, req.toInput())
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, emailPreviewResponse{Subject: out.Subject, Preheader: out.Preheader, HTML: out.HTML})
}
