package userModel

// UserStatus is the account lifecycle state.
type UserStatus string

const (
	UserStatusActive     UserStatus = "active"
	UserStatusBlocked    UserStatus = "blocked"
	UserStatusIncomplete UserStatus = "incomplete"
	UserStatusDeleted    UserStatus = "deleted"
)
