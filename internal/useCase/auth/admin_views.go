// Application-layer read models for the admin user area and self-profile:
// shapes this use case produces for delivery. Not domain entities.
package auth

import (
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

type (
	// UserInfo is the admin list / self-profile row view.
	UserInfo struct {
		ID             uuid.UUID
		FirstName      string
		LastName       string
		Picture        string
		Email          string
		EmailConfirmed bool
		Role           rbac.Role
		Status         userModel.UserStatus
		LastSeen       time.Time
		CreatedAt      time.Time
		// SignInMethods: email (a password) and the linked providers, as in UserDetail; filled in the offset list.
		SignInMethods []string
	}

	// UserDetail is the admin single-user view.
	UserDetail struct {
		ID             uuid.UUID
		FirstName      string
		LastName       string
		Email          string
		Role           rbac.Role
		Status         userModel.UserStatus
		EmailConfirmed bool
		Picture        string
		SignInMethods  []string
		LastSeen       time.Time
		CreatedAt      time.Time
	}

	UsersFilter struct {
		Search   string
		Roles    []rbac.Role
		Status   userModel.UserStatus
		Cursor   uuid.UUID
		Page     int
		PageSize int
		SortBy   string
		SortDir  string
		// EmailConfirmed is "", "true" or "false"; SignIn "", password, google or passkey; InactiveDays 0 = any.
		EmailConfirmed string
		SignIn         string
		InactiveDays   int
	}

	UsersListResult struct {
		Users      []UserInfo
		NextCursor uuid.UUID
		HasMore    bool
		Total      int64
	}

	RoleCount struct {
		Role  rbac.Role
		Count int
	}

	DayCount struct {
		Day   time.Time
		Count int
	}

	UserStats struct {
		Total              int
		Blocked            int
		NewLast7d          int
		ActiveLast7d       int
		AvgDailyActive7d   float64
		ByRole             []RoleCount
		RegistrationsByDay []DayCount
	}
)

// InviteResult is the per-email outcome of a bulk invitation.
type InviteResult struct {
	Email string
	Err   error
}
