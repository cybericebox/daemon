package config

// Environments
const (
	Development = "development"
	Stage       = "stage"
	Production  = "production"
)

const (
	IDSubdomain    = "id"
	MainSubdomain  = ""
	AdminSubdomain = "admin"
	// ExercisesSubdomain hosts the exercise catalog and editor app (W4).
	ExercisesSubdomain = "exercises"
	// APISubdomain is this service's own host label — api.<domain> is the only
	// Host it answers on (see protection.RequireAPIHost). Frontend routing is
	// nginx's job; this service no longer proxies anything.
	APISubdomain = "api"
)

const (
	DefaultPageSize = 20
)
