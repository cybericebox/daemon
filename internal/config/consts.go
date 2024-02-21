package config

const (
	defaultConfigPath = "config/main.yml"

	// Useful constants
	SubdomainCtxKey = "subdomain"
	UserIdCtxKey    = "userId"

	// tokens
	AccessToken      = "accessToken"
	RefreshToken     = "refreshToken"
	PermissionsToken = "permissionsToken"

	FromURLField   = "fromURL"
	DefaultFromURL = "/"

	// frontend page URLs

	SignInPage = "sign-in"

	// Environments
	EnvProduction  = "production"
	EnvStage       = "stage"
	EnvDevelopment = "development"
)
