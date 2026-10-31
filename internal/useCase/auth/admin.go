package auth

import (
	"slices"
	"context"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/pkg/pagination"
)

// maxUUID sorts after any real UUID; used with cursorSentinelTime as the
// first-page keyset sentinel so one predicate (created_at,id) < (ts,id) works.
var maxUUID = uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff"))

var cursorSentinelTime = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

// ListUsers returns a cursor (keyset) page of users filtered by search and role
// set, newest first. The cursor is the id of the last row of the previous page;
// its (created_at, id) keyset position is looked up server-side. An unknown or
// zero cursor yields the first page (forgiving by design). Requires PermUsersRead.
func (u *AuthUseCase) ListUsers(ctx context.Context, f UsersFilter) (UsersListResult, error) {
	limit := f.PageSize
	if limit <= 0 || limit > pagination.MaxPageSize {
		limit = pagination.DefaultPageSize
	}

	curTs, curID := cursorSentinelTime, maxUUID
	if f.Cursor != uuid.Nil {
		if usr, err := u.users.GetByID(ctx, f.Cursor); err == nil {
			curTs, curID = usr.CreatedAt, usr.ID
		}
	}

	roles := make([]string, 0, len(f.Roles))
	for _, r := range f.Roles {
		roles = append(roles, string(r))
	}
	if f.Page > 0 {
		sortBy := f.SortBy
		switch sortBy {
		case "name", "email", "role", "status", "signIn", "created", "lastSeen":
		default:
			sortBy = "created"
		}
		sortDir := f.SortDir
		if sortDir != "asc" {
			sortDir = "desc"
		}
		offset := (int64(f.Page) - 1) * int64(limit)
		if offset < 0 || offset > 2147483647 {
			return UsersListResult{}, model.ErrPlatform.WithMessage("User page is out of range").Err()
		}
		params := userRepo.PageParams{
			Search: f.Search, Roles: roles, Status: string(f.Status), SortBy: sortBy,
			SortDir: sortDir, Limit: int32(limit), Offset: int32(offset),
			EmailConfirmed: f.EmailConfirmed, SignIn: f.SignIn, InactiveDays: int32(f.InactiveDays),
		}
		rows, err := u.users.ListPage(ctx, params)
		if err != nil {
			return UsersListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list users").Err()
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		providers, err := u.users.ProvidersOf(ctx, ids)
		if err != nil {
			return UsersListResult{}, model.ErrPlatform.WithError(err).
				WithMessage("Failed to list user providers").
				Err()
		}
		users := make([]UserInfo, 0, len(rows))
		for _, r := range rows {
			methods := make([]string, 0, 1+len(providers[r.ID]))
			if r.HasPassword() {
				methods = append(methods, "email")
			}
			methods = append(methods, providers[r.ID]...)
			users = append(users, UserInfo{ID: r.ID, FirstName: r.FirstName, LastName: r.LastName,
				Picture: r.Picture, Email: r.Email, EmailConfirmed: r.EmailConfirmed, Role: r.Role, Status: r.Status,
				LastSeen: r.LastSeen, CreatedAt: r.CreatedAt, SignInMethods: methods})
		}
		total, err := u.users.CountFiltered(ctx, params)
		if err != nil {
			return UsersListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count users").Err()
		}
		return UsersListResult{Users: users, Total: total}, nil
	}

	rows, err := u.users.ListCursor(ctx, userRepo.ListParams{
		Search:          f.Search,
		Roles:           roles,
		Status:          string(f.Status),
		CursorCreatedAt: curTs,
		CursorID:        curID,
		Limit:           int32(limit + 1),
	})
	if err != nil {
		return UsersListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list users").Err()
	}

	hasMore := false
	if len(rows) > limit {
		hasMore = true
		rows = rows[:limit]
	}

	users := make([]UserInfo, 0, len(rows))
	for _, r := range rows {
		users = append(users, UserInfo{
			ID: r.ID, FirstName: r.FirstName, LastName: r.LastName, Picture: r.Picture,
			Email: r.Email, EmailConfirmed: r.EmailConfirmed, Role: r.Role, Status: r.Status,
			LastSeen: r.LastSeen, CreatedAt: r.CreatedAt,
		})
	}

	next := uuid.Nil
	if hasMore && len(rows) > 0 {
		next = rows[len(rows)-1].ID
	}
	total, err := u.users.Count(ctx, f.Search, roles, string(f.Status))
	if err != nil {
		return UsersListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count users").Err()
	}
	return UsersListResult{Users: users, NextCursor: next, HasMore: hasMore, Total: total}, nil
}

// GetUserStats returns aggregate user counts + a 7-day window for the admin
// dashboard. Requires PermUsersRead.
func (u *AuthUseCase) GetUserStats(ctx context.Context) (UserStats, error) {
	since := time.Now().AddDate(0, 0, -7)

	total, err := u.users.Count(ctx, "", nil, "")
	if err != nil {
		return UserStats{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count users").Err()
	}
	byRole, err := u.users.CountByRoleAll(ctx)
	if err != nil {
		return UserStats{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count users by role").Err()
	}
	blocked, err := u.users.CountByStatus(ctx, string(userModel.UserStatusBlocked))
	if err != nil {
		return UserStats{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count blocked users").Err()
	}
	newCount, err := u.users.CountCreatedSince(ctx, since)
	if err != nil {
		return UserStats{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count new users").Err()
	}
	active, err := u.users.CountActiveSince(ctx, since)
	if err != nil {
		return UserStats{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count active users").Err()
	}
	avgDAU, err := u.users.AvgDailyActiveSince(ctx, since)
	if err != nil {
		return UserStats{}, model.ErrPlatform.WithError(err).WithMessage("Failed to average daily active").Err()
	}
	regByDay, err := u.users.RegistrationsByDaySince(ctx, since)
	if err != nil {
		return UserStats{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load registrations by day").Err()
	}

	stats := UserStats{
		Total:              int(total),
		Blocked:            int(blocked),
		NewLast7d:          int(newCount),
		ActiveLast7d:       int(active),
		AvgDailyActive7d:   avgDAU,
		ByRole:             make([]RoleCount, 0, len(byRole)),
		RegistrationsByDay: make([]DayCount, 0, len(regByDay)),
	}
	for _, r := range byRole {
		stats.ByRole = append(stats.ByRole, RoleCount{Role: r.Role, Count: int(r.Count)})
	}
	for _, d := range regByDay {
		stats.RegistrationsByDay = append(stats.RegistrationsByDay, DayCount{Day: d.Day, Count: int(d.Count)})
	}
	return stats, nil
}

// GetUser returns a single user's detail view (profile + status + sign-in
// methods) for the admin UI. Requires PermUsersRead.
func (u *AuthUseCase) GetUser(ctx context.Context, userID uuid.UUID) (*UserDetail, error) {
	usr, err := u.users.GetByID(ctx, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, userModel.ErrUserNotFound.Err()
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get user").Err()
	}

	providers, err := u.users.ListProviders(ctx, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get user providers").Err()
	}

	methods := make([]string, 0, len(providers)+1)
	if usr.HasPassword() {
		methods = append(methods, "email")
	}
	for _, p := range providers {
		methods = append(methods, p)
	}

	return &UserDetail{
		ID:             usr.ID,
		FirstName:      usr.FirstName,
		LastName:       usr.LastName,
		Email:          usr.Email,
		Role:           rbac.Role(usr.Role),
		Status:         userModel.UserStatus(usr.Status),
		EmailConfirmed: usr.EmailConfirmed,
		Picture:        usr.Picture,
		SignInMethods:  methods,
		LastSeen:       usr.LastSeen,
		CreatedAt:      usr.CreatedAt,
	}, nil
}

// UpdateUserRole changes the role of user userID to role. Requires
// PermUsersRoleWrite; the caller must also be able to assign the target role
// (CanAssignRole). When demoting the last super_admin the operation is blocked.
func (u *AuthUseCase) UpdateUserRole(ctx context.Context, userID uuid.UUID, role rbac.Role) error {
	if !rbac.ValidRole(string(role)) {
		return authModel.ErrInvalidRole.Err()
	}
	caller, ok := rbac.CurrentUserSessionFromContext(ctx)
	if !ok {
		return authModel.ErrAuthInvalidSession.Err()
	}
	if !rbac.CanAssignRole(caller.Role, role) {
		return authModel.ErrCannotAssignRole.Err()
	}

	u.superAdminMu.Lock()
	defer u.superAdminMu.Unlock()
	return u.mutateUser(ctx, userID, func(target *userModel.User) error {
		if err := requireManageableTarget(caller.Role, rbac.Role(target.Role)); err != nil {
			return err
		}
		// Below super_admin only the user role is given (rbac.CanSetRole).
		if !rbac.CanSetRole(caller.Role, target.Role, role) {
			return authModel.ErrCannotAssignRole.Err()
		}
		// Guard: demoting the last super_admin is forbidden.
		if target.Role == rbac.RoleSuperAdmin && role != rbac.RoleSuperAdmin {
			if err := u.guardLastSuperAdmin(ctx, target); err != nil {
				return err
			}
		}
		return target.ChangeRole(role, time.Now())
	})
}

// UpdateUserStatus sets the status of user userID. Only "active" and "blocked"
// are valid values. Requires PermUsersStatusWrite.
func (u *AuthUseCase) UpdateUserStatus(ctx context.Context, userID uuid.UUID, status userModel.UserStatus) error {
	// Fail fast on garbage input before loading the aggregate; the entity
	// enforces the transition itself (deleted/incomplete cannot be toggled).
	if status != userModel.UserStatusActive && status != userModel.UserStatusBlocked {
		return authModel.ErrInvalidUserStatus.Err()
	}
	caller, ok := rbac.CurrentUserSessionFromContext(ctx)
	if !ok {
		return authModel.ErrAuthInvalidSession.Err()
	}
	u.superAdminMu.Lock()
	defer u.superAdminMu.Unlock()
	if err := u.mutateUser(ctx, userID, func(target *userModel.User) error {
		if err := requireManageableTarget(caller.Role, rbac.Role(target.Role)); err != nil {
			return err
		}
		if status == userModel.UserStatusBlocked {
			// Blocking the last active super_admin locks the platform just like deleting them.
			if target.Role == rbac.RoleSuperAdmin {
				if err := u.guardLastSuperAdmin(ctx, target); err != nil {
					return err
				}
			}
			return target.Block(time.Now())
		}
		return target.Activate(time.Now())
	}); err != nil {
		return err
	}

	// When blocking a user, immediately revoke all their active sessions so
	// existing cookies stop working at the next validation round-trip.
	if status == userModel.UserStatusBlocked {
		if _, err := u.revokeAll(ctx, userID); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to revoke sessions for blocked user").Err()
		}
	}

	return nil
}

// DeleteUser soft-deletes a user by ID together with their sessions and
// provider links. Requires PermUsersDelete. Deleting the last super_admin is
// blocked to prevent lockout.
func (u *AuthUseCase) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	caller, ok := rbac.CurrentUserSessionFromContext(ctx)
	if !ok {
		return authModel.ErrAuthInvalidSession.Err()
	}
	// Authorize against the loaded target before the cascade mutates it.
	return u.deleteUserCascade(ctx, userID, func(target *userModel.User) error {
		return requireManageableTarget(caller.Role, rbac.Role(target.Role))
	})
}

// requireManageableTarget: only a higher role manages an account (rbac.CanManageAccount); an admin never blocks,
// demotes or deletes another admin, admin_viewer or super_admin.
func requireManageableTarget(caller, target rbac.Role) error {
	if !rbac.CanManageAccount(caller, target) {
		return authModel.ErrInsufficientPermission.Err()
	}
	return nil
}

// guardLastSuperAdmin returns ErrLastSuperAdmin when taking target out of
// service (demote, block, delete) would leave no active super_admin: the
// platform could not be administered, and the only way back would be a database
// edit. A target that is already blocked is not counted on, so removing it
// changes nothing. Callers hold u.superAdminMu: the count and the write that
// follows are one decision, and two concurrent demotions must not both pass.
func (u *AuthUseCase) guardLastSuperAdmin(ctx context.Context, target *userModel.User) error {
	if target.IsBlocked() {
		return nil
	}
	count, err := u.users.Count(ctx, "", []string{string(rbac.RoleSuperAdmin)}, string(userModel.UserStatusActive))
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to count super admins").Err()
	}
	if count <= 1 {
		return authModel.ErrLastSuperAdmin.Err()
	}
	return nil
}

// UserParticipationView is one event the user takes part in or works for (the admin user card).
type UserParticipationView = userRepo.Participation

// ListUserParticipations returns the events of the user: participations with the team and the role in it, and the
// events they manage. Requires PermUsersRead.
func (u *AuthUseCase) ListUserParticipations(
	ctx context.Context,
	userID uuid.UUID,
) ([]UserParticipationView, error) {
	if _, err := u.users.GetByID(ctx, userID); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, userModel.ErrUserNotFound.Err()
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get user").Err()
	}
	items, err := u.users.Participations(ctx, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to list user participations").
			Err()
	}
	return items, nil
}

// EndUserSessions signs the user out everywhere. An administrator never ends their own sessions this way and only
// a higher role manages an account. Requires PermUsersStatusWrite.
func (u *AuthUseCase) EndUserSessions(ctx context.Context, userID uuid.UUID) error {
	caller, ok := rbac.CurrentUserSessionFromContext(ctx)
	if !ok {
		return authModel.ErrAuthInvalidSession.Err()
	}
	if caller.UserID == userID {
		return authModel.ErrInsufficientPermission.Err()
	}
	target, err := u.users.GetByID(ctx, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return userModel.ErrUserNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user").Err()
	}
	if err = requireManageableTarget(caller.Role, target.Role); err != nil {
		return err
	}
	if _, err = u.revokeAll(ctx, userID); err != nil {
		return model.ErrPlatform.WithError(err).
			WithMessage("Failed to end the sessions of the user").
			Err()
	}
	return nil
}

// ResendUserInvitation sends the invitation of an unfinished registration again with a new link (the old one stops
// working). Requires PermUsersInvite and authority over the invited role.
func (u *AuthUseCase) ResendUserInvitation(ctx context.Context, userID uuid.UUID) error {
	target, err := u.users.GetByID(ctx, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return userModel.ErrUserNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user").Err()
	}
	if target.Status != userModel.UserStatusIncomplete {
		return userModel.ErrUserNotIncomplete.Err()
	}
	return u.InviteUser(ctx, target.Email, target.Role, target.FirstName, target.LastName)
}

// Bulk actions on users.
const (
	BulkResendInvitation = "resend-invitation"
	BulkBlock            = "block"
	BulkUnblock          = "unblock"
	BulkEndSessions      = "end-sessions"
	// MaxBulkUsers bounds one bulk request.
	MaxBulkUsers = 200
)

// BulkUserResult is the outcome for one user of a bulk action; Err is nil on success.
type BulkUserResult struct {
	UserID uuid.UUID
	Err    error
}

// BulkUserAction applies one action to every listed user with the checks of the single action (not oneself, only a
// higher role, the last super admin stays). A failure of one user never stops the others; the result keeps the
// request order.
func (u *AuthUseCase) BulkUserAction(
	ctx context.Context,
	action string,
	ids []uuid.UUID,
) ([]BulkUserResult, error) {
	var apply func(context.Context, uuid.UUID) error
	switch action {
	case BulkResendInvitation:
		apply = u.ResendUserInvitation
	case BulkBlock:
		apply = func(c context.Context, id uuid.UUID) error {
			return u.blockUnlessSelf(c, id, userModel.UserStatusBlocked)
		}
	case BulkUnblock:
		apply = func(c context.Context, id uuid.UUID) error {
			return u.blockUnlessSelf(c, id, userModel.UserStatusActive)
		}
	case BulkEndSessions:
		apply = u.EndUserSessions
	default:
		return nil, authModel.ErrInvalidUserStatus.WithMessage("Unknown bulk action").Err()
	}
	if len(ids) == 0 || len(ids) > MaxBulkUsers {
		return nil, authModel.ErrInvalidUserStatus.WithMessage("Provide 1 to 200 users").Err()
	}
	results := make([]BulkUserResult, 0, len(ids))
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		results = append(results, BulkUserResult{UserID: id, Err: apply(ctx, id)})
	}
	return results, nil
}

func (u *AuthUseCase) blockUnlessSelf(
	ctx context.Context,
	id uuid.UUID,
	status userModel.UserStatus,
) error {
	caller, ok := rbac.CurrentUserSessionFromContext(ctx)
	if !ok {
		return authModel.ErrAuthInvalidSession.Err()
	}
	if caller.UserID == id {
		return authModel.ErrInsufficientPermission.Err()
	}
	return u.UpdateUserStatus(ctx, id, status)
}

// UserFacetFields are the fields of the user table that have a facet.
func UserFacetFields() []string { return []string{"role", "status", "emailConfirmed", "signIn"} }

// UserFacets counts the users per value of one field under the other filters of the table (the field's own filter is
// ignored, so the chip shows what choosing each value would give). Requires PermUsersRead.
func (u *AuthUseCase) UserFacets(ctx context.Context, field string, f UsersFilter) (map[string]int64, error) {
	if !slices.Contains(UserFacetFields(), field) {
		return nil, authModel.ErrInvalidUserStatus.WithMessage("Unknown facet field").Err()
	}
	params := userRepo.PageParams{
		Search: f.Search, Status: string(f.Status), EmailConfirmed: f.EmailConfirmed, SignIn: f.SignIn, InactiveDays: int32(f.InactiveDays),
		Roles: []string{},
	}
	for _, r := range f.Roles {
		params.Roles = append(params.Roles, string(r))
	}
	switch field {
	case "role":
		params.Roles = []string{}
	case "status":
		params.Status = ""
	case "emailConfirmed":
		params.EmailConfirmed = ""
	case "signIn":
		params.SignIn = ""
	}
	counts, err := u.users.Facet(ctx, field, params)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to count the users per value").Err()
	}
	return counts, nil
}
