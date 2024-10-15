package config

const (
// MigrationPath = "internal/delivery/repository/postgres/migrations"
// MigrationPath = "migrations"
)

// Environments
const (
	Local      = "local"
	Stage      = "stage"
	Production = "production"
)

// exercise flag
const (
	FlagFormat       = "ICE{%s}"
	RandomFlagLength = 20
)

// subdomains and paths
const (
	MainSubdomain  = ""
	AdminSubdomain = "admin"

	SignInPage        = "/sign-in"
	EventNotFoundPage = "/event-not-found"
)

const (
	SchemeHTTPS = "https"
)

// from url field
const (
	FromURLField   = "fromURL"
	DefaultFromURL = "/"
)
