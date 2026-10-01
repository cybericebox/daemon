package user

import (
	"context"
	"time"

	utils "github.com/cybericebox/daemon/internal/delivery/controller/http/utils"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/pagination"
)

type (
	IUseCase interface {
		ListUsers(ctx context.Context, f authUseCase.UsersFilter) (authUseCase.UsersListResult, error)
		GetUserStats(ctx context.Context) (authUseCase.UserStats, error)
		GetUser(ctx context.Context, userID uuid.UUID) (*authUseCase.UserDetail, error)
		UpdateUserRole(ctx context.Context, targetID uuid.UUID, role rbac.Role) error
		UpdateUserStatus(ctx context.Context, targetID uuid.UUID, status userModel.UserStatus) error
		DeleteUser(ctx context.Context, targetID uuid.UUID) error
		InviteUser(ctx context.Context, email string, role rbac.Role, firstName, lastName string) error
		InviteUsers(
			ctx context.Context,
			role rbac.Role,
			emails []string,
		) ([]authUseCase.InviteResult, error)
		InviteEntries(ctx context.Context, entries []authUseCase.InviteEntry) ([]authUseCase.InviteEntryResult, error)
	}

	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}

	Handler struct {
		useCase IUseCase
		prot    IProtection
	}

	updateRoleRequest struct {
		Role rbac.Role `json:"Role"`
	}
	updateStatusRequest struct {
		Status userModel.UserStatus `json:"Status"`
	}

	inviteEntryRequest struct {
		Email     string    `json:"Email"`
		FirstName string    `json:"FirstName"`
		LastName  string    `json:"LastName"`
		Role      rbac.Role `json:"Role"`
	}

	inviteRequest struct {
		// Entries: one line per person with its own role (empty = user).
		Entries   []inviteEntryRequest `json:"Entries"`
		Email     string               `json:"Email"`
		Emails    []string             `json:"Emails"`
		Role      rbac.Role            `json:"Role"`
		FirstName string               `json:"FirstName"`
		LastName  string               `json:"LastName"`
	}

	inviteResultResponse struct {
		Email string `json:"Email"`
		Error string `json:"Error,omitempty"`
		// Role and Code are set for Entries: Code is empty on success.
		Role rbac.Role `json:"Role,omitempty"`
		Code string    `json:"Code,omitempty"`
	}

	userResponse struct {
		ID        uuid.UUID            `json:"ID"`
		FirstName string               `json:"FirstName"`
		LastName  string               `json:"LastName"`
		Email     string               `json:"Email"`
		Role      rbac.Role            `json:"Role"`
		Status    userModel.UserStatus `json:"Status"`
		LastSeen  time.Time            `json:"LastSeen"`
		CreatedAt time.Time            `json:"CreatedAt"`
	}

	roleCountResponse struct {
		Role  rbac.Role `json:"Role"`
		Count int       `json:"Count"`
	}
	dayCountResponse struct {
		Day   time.Time `json:"Day"`
		Count int       `json:"Count"`
	}
	userStatsResponse struct {
		Total              int                 `json:"Total"`
		Blocked            int                 `json:"Blocked"`
		NewLast7d          int                 `json:"NewLast7d"`
		ActiveLast7d       int                 `json:"ActiveLast7d"`
		AvgDailyActive7d   float64             `json:"AvgDailyActive7d"`
		ByRole             []roleCountResponse `json:"ByRole"`
		RegistrationsByDay []dayCountResponse  `json:"RegistrationsByDay"`
	}

	userDetailResponse struct {
		ID             uuid.UUID            `json:"ID"`
		FirstName      string               `json:"FirstName"`
		LastName       string               `json:"LastName"`
		Email          string               `json:"Email"`
		Role           rbac.Role            `json:"Role"`
		Status         userModel.UserStatus `json:"Status"`
		EmailConfirmed bool                 `json:"EmailConfirmed"`
		Picture        string               `json:"Picture"`
		SignInMethods  []string             `json:"SignInMethods"`
		LastSeen       time.Time            `json:"LastSeen"`
		CreatedAt      time.Time            `json:"CreatedAt"`
	}
)

func NewUserAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	usersAPI := router.Group("users")
	usersAPI.GET("", h.prot.RequirePermission(rbac.PermUsersRead), h.listUsers)
	usersAPI.GET("stats", h.prot.RequirePermission(rbac.PermUsersRead), h.getUserStats)
	usersAPI.GET(":userID", h.prot.RequirePermission(rbac.PermUsersRead), h.getUser)
	usersAPI.PATCH(":userID/role", h.prot.RequirePermission(rbac.PermUsersRoleWrite), h.updateRole)
	usersAPI.PATCH(
		":userID/status",
		h.prot.RequirePermission(rbac.PermUsersStatusWrite),
		h.updateStatus,
	)
	usersAPI.DELETE(":userID", h.prot.RequirePermission(rbac.PermUsersDelete), h.deleteUser)
	usersAPI.POST("invite", h.prot.RequirePermission(rbac.PermUsersInvite), h.invite)
}

// listUsers godoc
// @Summary  List platform users (cursor pagination)
// @Tags     users
// @Produce  json
// @Param    search  query     string  false  "search term"
// @Param    role    query     string  false  "role filter (repeatable)"
// @Param    cursor  query     string  false  "opaque pagination cursor"
// @Param    pageSize  query     int     false  "page size (default 20)"
// @Success  200  {object}  response.Response{data=pagination.CursorPage[userResponse]}
// @Failure  401  {object}  response.Response
// @Router   /users [get]
func (h *Handler) listUsers(ctx *gin.Context) {
	if ctx.Query("page") != "" {
		page, err := utils.GetOffsetPaginationParams(ctx)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		res, err := h.useCase.ListUsers(ctx, authUseCase.UsersFilter{
			Search: ctx.Query("search"), Roles: rbac.RolesFromStrings(ctx.QueryArray("role")),
			Status: userModel.UserStatus(ctx.Query("status")), Page: page.Page,
			PageSize: page.PageSize, SortBy: ctx.Query("sortBy"), SortDir: ctx.Query("sortDir"),
		})
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		items := make([]userResponse, 0, len(res.Users))
		for _, x := range res.Users {
			items = append(items, userResponse{ID: x.ID, FirstName: x.FirstName, LastName: x.LastName,
				Email: x.Email, Role: x.Role, Status: x.Status, LastSeen: x.LastSeen, CreatedAt: x.CreatedAt})
		}
		response.AbortWithData(ctx, pagination.NewOffsetPage(items, res.Total, page.Page, page.PageSize))
		return
	}
	page, err := utils.GetCursorPaginationParams(ctx)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	res, err := h.useCase.ListUsers(ctx, authUseCase.UsersFilter{
		Search:   ctx.Query("search"),
		Roles:    rbac.RolesFromStrings(ctx.QueryArray("role")),
		Status:   userModel.UserStatus(ctx.Query("status")),
		Cursor:   page.Cursor,
		PageSize: page.PageSize,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	items := make([]userResponse, 0, len(res.Users))
	for _, x := range res.Users {
		items = append(items, userResponse{
			ID:        x.ID,
			FirstName: x.FirstName,
			LastName:  x.LastName,
			Email:     x.Email,
			Role:      x.Role,
			Status:    x.Status,
			LastSeen:  x.LastSeen,
			CreatedAt: x.CreatedAt,
		})
	}
	response.AbortWithData(ctx, pagination.NewCursorPage(items, res.HasMore, res.NextCursor, res.Total))
}

// getUserStats godoc
// @Summary  Aggregate user counts
// @Tags     users
// @Produce  json
// @Success  200  {object}  response.Response{data=userStatsResponse}
// @Router   /users/stats [get]
func (h *Handler) getUserStats(ctx *gin.Context) {
	s, err := h.useCase.GetUserStats(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := userStatsResponse{
		Total: s.Total, Blocked: s.Blocked, NewLast7d: s.NewLast7d,
		ActiveLast7d: s.ActiveLast7d, AvgDailyActive7d: s.AvgDailyActive7d,
		ByRole:             make([]roleCountResponse, 0, len(s.ByRole)),
		RegistrationsByDay: make([]dayCountResponse, 0, len(s.RegistrationsByDay)),
	}
	for _, r := range s.ByRole {
		out.ByRole = append(out.ByRole, roleCountResponse{Role: r.Role, Count: r.Count})
	}
	for _, d := range s.RegistrationsByDay {
		out.RegistrationsByDay = append(
			out.RegistrationsByDay,
			dayCountResponse{Day: d.Day, Count: d.Count},
		)
	}
	response.AbortWithData(ctx, out)
}

// getUser godoc
// @Summary  Get a single user's detail
// @Tags     users
// @Produce  json
// @Param    userID  path      string  true  "target user UUID"
// @Success  200  {object}  response.Response{data=userDetailResponse}
// @Failure  401  {object}  response.Response
// @Failure  403  {object}  response.Response
// @Failure  404  {object}  response.Response
// @Router   /users/{userID} [get]
func (h *Handler) getUser(ctx *gin.Context) {
	targetID, ok := parseUserID(ctx)
	if !ok {
		return
	}
	d, err := h.useCase.GetUser(ctx, targetID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, userDetailResponse{
		ID:             d.ID,
		FirstName:      d.FirstName,
		LastName:       d.LastName,
		Email:          d.Email,
		Role:           d.Role,
		Status:         d.Status,
		EmailConfirmed: d.EmailConfirmed,
		Picture:        d.Picture,
		SignInMethods:  d.SignInMethods,
		LastSeen:       d.LastSeen,
		CreatedAt:      d.CreatedAt,
	})
}

// updateRole godoc
// @Summary  Update a user's role
// @Tags     users
// @Accept   json
// @Produce  json
// @Param    userID  path      string             true  "target user UUID"
// @Param    body    body      updateRoleRequest  true  "new role"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Failure  401  {object}  response.Response
// @Failure  403  {object}  response.Response
// @Router   /users/{userID}/role [patch]
func (h *Handler) updateRole(ctx *gin.Context) {
	targetID, ok := parseUserID(ctx)
	if !ok {
		return
	}
	var req updateRoleRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.UpdateUserRole(ctx.Request.Context(), targetID, req.Role); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// updateStatus godoc
// @Summary  Update a user's status (active / suspended)
// @Tags     users
// @Accept   json
// @Produce  json
// @Param    userID  path      string               true  "target user UUID"
// @Param    body    body      updateStatusRequest  true  "new status"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Failure  401  {object}  response.Response
// @Failure  403  {object}  response.Response
// @Router   /users/{userID}/status [patch]
func (h *Handler) updateStatus(ctx *gin.Context) {
	targetID, ok := parseUserID(ctx)
	if !ok {
		return
	}
	var req updateStatusRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.UpdateUserStatus(ctx.Request.Context(), targetID, req.Status); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// deleteUser godoc
// @Summary  Delete a user account
// @Tags     users
// @Produce  json
// @Param    userID  path      string  true  "target user UUID"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Failure  401  {object}  response.Response
// @Failure  403  {object}  response.Response
// @Router   /users/{userID} [delete]
func (h *Handler) deleteUser(ctx *gin.Context) {
	targetID, ok := parseUserID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.DeleteUser(ctx.Request.Context(), targetID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// invite godoc
// @Summary  Invite one or more users by email
// @Tags     users
// @Accept   json
// @Produce  json
// @Param    body  body      inviteRequest  true  "invite payload (Entries: per-person role and names; single: Email+Role+Name; bulk: Emails+Role)"
// @Success  200   {object}  response.Response{data=[]inviteResultResponse}  "bulk result"
// @Success  200   {object}  response.Response                               "single invite success"
// @Failure  400   {object}  response.Response
// @Failure  401   {object}  response.Response
// @Failure  403   {object}  response.Response
// @Router   /users/invite [post]
func (h *Handler) invite(ctx *gin.Context) {
	var req inviteRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if len(req.Entries) > 0 {
		entries := make([]authUseCase.InviteEntry, 0, len(req.Entries))
		for _, entry := range req.Entries {
			entries = append(entries, authUseCase.InviteEntry{Email: entry.Email, FirstName: entry.FirstName, LastName: entry.LastName, Role: entry.Role})
		}
		results, err := h.useCase.InviteEntries(ctx.Request.Context(), entries)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		out := make([]inviteResultResponse, 0, len(results))
		for _, r := range results {
			out = append(out, inviteResultResponse{Email: r.Email, Role: r.Role, Code: r.Code})
		}
		response.AbortWithData(ctx, out)
		return
	}
	if len(req.Emails) > 0 {
		results, err := h.useCase.InviteUsers(ctx.Request.Context(), req.Role, req.Emails)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		out := make([]inviteResultResponse, 0, len(results))
		for _, r := range results {
			rr := inviteResultResponse{Email: r.Email}
			if r.Err != nil {
				rr.Error = r.Err.Error()
			}
			out = append(out, rr)
		}
		response.AbortWithData(ctx, out)
		return
	}
	if err := h.useCase.InviteUser(
		ctx.Request.Context(),
		req.Email,
		req.Role,
		req.FirstName,
		req.LastName,
	); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// parseUserID extracts and validates the :userID path param.
func parseUserID(ctx *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.FromString(ctx.Param("userID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, false
	}
	return id, true
}
