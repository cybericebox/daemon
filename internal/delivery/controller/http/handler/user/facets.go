package user

import (
	"errors"
	"slices"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
)

type userFacetResponse struct {
	// Counts maps a value of the field to the number of users with it under the other filters; values with no
	// users are absent. signIn counts a user once per method (email, google).
	Counts map[string]int64 `json:"Counts"`
}

// facets godoc
// @Summary  Facet counts of the users table
// @Description How many users have each value of field (role, status, emailConfirmed or signIn) under the same filters as GET /users (search, role, status, emailConfirmed, signIn, inactiveDays) except the field's own.
// @Tags     users
// @Produce  json
// @Param    field  query  string  true  "role | status | emailConfirmed | signIn"
// @Success  200  {object}  response.Response{data=userFacetResponse}
// @Router   /users/facets [get]
func (h *Handler) facets(ctx *gin.Context) {
	field := ctx.Query("field")
	if !slices.Contains(authUseCase.UserFacetFields(), field) {
		response.AbortWithBadRequest(
			ctx,
			errors.New("field must be role, status, emailConfirmed or signIn"),
		)
		return
	}
	emailConfirmed, signIn, inactiveDays, err := parseUserFilters(ctx)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	counts, err := h.useCase.UserFacets(ctx.Request.Context(), field, authUseCase.UsersFilter{
		Search: ctx.Query(
			"search",
		),
		Roles:          rbac.RolesFromStrings(ctx.QueryArray("role")),
		Status:         userModel.UserStatus(ctx.Query("status")),
		EmailConfirmed: emailConfirmed,
		SignIn:         signIn,
		InactiveDays:   inactiveDays,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	response.AbortWithData(ctx, userFacetResponse{Counts: counts})
}
