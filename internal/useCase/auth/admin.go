package auth

import (
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
		case "name", "role", "status", "created", "lastSeen":
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
		rows, err := u.users.ListPage(ctx, userRepo.PageParams{
			Search: f.Search, Roles: roles, Status: string(f.Status), SortBy: sortBy,
			SortDir: sortDir, Limit: int32(limit), Offset: int32(offset),
		})
		if err != nil {
			return UsersListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list users").Err()
		}
		users := make([]UserInfo, 0, len(rows))
		for _, r := range rows {
			users = append(users, UserInfo{ID: r.ID, FirstName: r.FirstName, LastName: r.LastName,
				Picture: r.Picture, Email: r.Email, Role: r.Role, Status: r.Status,
				LastSeen: r.LastSeen, CreatedAt: r.CreatedAt})
		}
		total, err := u.users.Count(ctx, f.Search, roles, string(f.Status))
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
			Email: r.Email, Role: r.Role, Status: r.Status,
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
		if _, err := u.sessions.DeleteAllForUser(ctx, userID); err != nil {
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

func requireManageableTarget(caller, target rbac.Role) error {
	if !rbac.CanAssignRole(caller, target) {
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
