package platformAnalytics

import (
	"time"

	"github.com/gofrs/uuid"
)

// Sign-in method groups of the users report: exclusive, so they sum to the
// accounts.
const (
	// UsersMethodPassword: a password, no linked provider.
	UsersMethodPassword = "password"
	// UsersMethodGoogle: a linked Google account, no password.
	UsersMethodGoogle = "google"
	// UsersMethodBoth: a password and a linked Google account.
	UsersMethodBoth = "both"
	// UsersMethodNone: neither (an account that has not finished registration).
	UsersMethodNone = "none"
)

type (
	UsersView struct {
		Period OverviewPeriodView
		// Total and Blocked are the accounts now; ByRole splits Total by role.
		Total   int64
		Blocked int64
		ByRole  []UsersRoleView
		// New registered in the period; Active had recorded activity in it.
		New    OverviewMetricView
		Active OverviewMetricView
		// AvgDailyActive is the mean daily active accounts over the period.
		AvgDailyActive float64
		Registrations  []OverviewDayNewView
		// ActiveByDay holds daily (DAU) and weekly (WAU, trailing 7 days) active accounts per day.
		ActiveByDay []UsersActiveDayView
		Methods     []UsersMethodView
		Retention   UsersRetentionView
	}

	UsersRoleView struct {
		Role  string
		Count int64
	}

	UsersActiveDayView struct {
		Day      time.Time
		DAU, WAU int64
	}

	// UsersMethodView: Total are all current accounts of the group, New those
	// registered in the period.
	UsersMethodView struct {
		Method     string
		Total, New int64
	}

	// UsersRetentionView: accounts that joined 1, 2, or 3+ events (approved)
	// by the end of the period, and accounts that joined none.
	UsersRetentionView struct{ One, Two, ThreePlus, Never int64 }

	// UsersPersonView is one row of the most active accounts (personal data).
	UsersPersonView struct {
		ID           uuid.UUID
		Name         string
		Email        string
		Role         string
		EventsJoined int64
		Solves       int64
		// LastSeenAt is the account's last authenticated request on the platform.
		LastSeenAt time.Time
	}
)
