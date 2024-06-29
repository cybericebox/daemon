package config

const (
	//MigrationPath     = "internal/delivery/repository/postgres/migrations"
	MigrationPath = "migrations"
)

// exercise flag
const (
	FlagFormat       = "ICE{%s}"
	RandomFlagLength = 20
)

// subdomains and paths
const (
	MainSubdomain    = ""
	AdminSubdomain   = "admin"
	StorageSubdomain = "storage"

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
