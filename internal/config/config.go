package config

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/rs/zerolog/log"

	retentionModel "github.com/cybericebox/daemon/internal/model/retention"
)

type (
	Config struct {
		// Environment (ENV): development (default) | stage | production. It also
		// sets gin and log output (SetupLogger, middleware.ForMode): development
		// runs gin debug mode with the [GIN-debug] route dump, gin's coloured
		// request lines and console logs; any other value runs gin release mode
		// and logs everything, requests included, as zerolog JSON (debug level
		// on stage, info on production).
		Environment    string               `env:"ENV" envDefault:"development"`
		HTTPController HTTPControllerConfig `                                   envPrefix:""`
		Infrastructure InfrastructureConfig `                                   envPrefix:""`
		Auth           AuthConfig           `                                   envPrefix:""`
		Media          MediaConfig          `                                   envPrefix:"MEDIA_"`
		Exercise       ExerciseConfig       `                                   envPrefix:"EXERCISE_"`
		FlagRateLimit  FlagRateLimitConfig  `                                   envPrefix:"FLAG_RATE_LIMIT_"`
		Retention      RetentionConfig      `                                   envPrefix:"RETENTION_"`
		LabAccess      LabAccessConfig      `                                   envPrefix:"LAB_ACCESS_"`
		VPN            VPNConfig            `                                   envPrefix:"VPN_"`
		Platform       PlatformConfig       `                                   envPrefix:"PLATFORM_"`
	}

	// LabAccessConfig sets how long the lab access tokens (the /_auth links) of the laboratory L7
	// proxy can be opened. They are signed with the access key of the agent that holds the lab group
	// (every agent is a tenant with its own key), never with a platform-wide key.
	LabAccessConfig struct {
		// TokenTTL is how long an access token can be opened: it is exchanged for
		// the proxy's own cookie at once. Up to 5m.
		TokenTTL time.Duration `env:"TOKEN_TTL" envDefault:"1m"`
	}

	InfrastructureConfig struct {
		Postgres PostgresConfig `envPrefix:"POSTGRES_"`
		SMTP     SMTPConfig     `envPrefix:"SMTP_"`
		Storage  StorageConfig  `envPrefix:"STORAGE_"`
		Agent    AgentConfig    `envPrefix:"AGENT_"`
	}

	// AgentConfig bootstraps the first infrastructure agent from the deployment config. Agents live in
	// the database: they are enrolled in the admin, or, when Endpoint and EnrollmentToken are set here and
	// no live agent with that endpoint exists yet, enrolled once at startup with the one-time token. The
	// token is ignored afterwards (a still-set token is logged once). Without any agent the daemon runs
	// fine (catalog, event setup, exercise authoring), but whatever needs to deploy a lab fails with
	// ErrInfrastructureUnavailable.
	AgentConfig struct {
		// Endpoint is host:443 of the agent. Dial it by its certificate hostname.
		Endpoint string `env:"ENDPOINT"`
		// EnrollmentToken is the one-time enrollment token of the agent's tenant (from the cluster
		// administrator's kit).
		EnrollmentToken string `env:"ENROLLMENT_TOKEN"`
		// Name labels the bootstrapped agent in the admin.
		Name string `env:"NAME" envDefault:"default"`
		// CAFile verifies the agent's server certificate; empty means the system roots (a publicly
		// trusted certificate). Needed for self-signed development stands only.
		CAFile string `env:"CA_FILE"`
		// InstanceID is the immutable platform-instance label put on every object this daemon
		// creates in the infrastructure (cybericebox.io/instance); its Monitoring subscription
		// selects by it, so two platform instances can share one cluster. Never change it for a
		// running deployment.
		InstanceID string `env:"INSTANCE_ID" envDefault:"cybericebox"`
	}

	// StorageConfig holds the S3/MinIO object store used for user avatars.
	// Empty Endpoint disables storage (avatar upload/serve becomes unavailable).
	StorageConfig struct {
		Endpoint  string `env:"ENDPOINT"`
		AccessKey string `env:"ACCESS_KEY"`
		SecretKey string `env:"SECRET_KEY"`
		Bucket    string `env:"BUCKET"     envDefault:"cybericebox"`
		Region    string `env:"REGION"     envDefault:"us-east-1"`
		UseSSL    bool   `env:"USE_SSL"    envDefault:"false"`
	}

	SMTPConfig struct {
		Host         string `env:"HOST"`
		Port         int    `env:"PORT"           envDefault:"587"`
		Username     string `env:"USERNAME"`
		Password     string `env:"PASSWORD"`
		SenderName   string `env:"SENDER_NAME"`
		SenderEmail  string `env:"SENDER_EMAIL"`
		ReplyToName  string `env:"REPLY_TO_NAME"`
		ReplyToEmail string `env:"REPLY_TO_EMAIL"`
		// MaxPerSecond and DailyQuota are the provider send limits of the env
		// transport (Amazon SES: Sending quota); 0 = no limit.
		MaxPerSecond float64 `env:"MAX_PER_SECOND"`
		DailyQuota   int     `env:"DAILY_QUOTA"`
	}

	PostgresConfig struct {
		Host           string `env:"HOST"     envDefault:"localhost"`
		Port           string `env:"PORT"     envDefault:"5432"`
		User           string `env:"USER"     envDefault:"postgres"`
		Password       string `env:"PASSWORD" envDefault:"postgres"`
		Database       string `env:"DB"       envDefault:"cybericebox_dev"`
		SSLMode        string `env:"SSL_MODE" envDefault:"disable"`
		MigrationsPath string // derived in populateForAllConfig
	}

	AuthConfig struct {
		Domain          string          `env:"DOMAIN"`
		TokenSignature  string          `env:"JWT_TOKEN_SIGNATURE"`
		SessionIdleTTL  time.Duration   `env:"SESSION_IDLE_TTL"    envDefault:"720h"`
		TemporalCodeTTL time.Duration   `env:"TEMPORAL_CODE_TTL"   envDefault:"1h"`
		SuperAdminEmail string          `env:"SUPER_ADMIN_EMAIL"`
		OAuth           OAuthConfig     `                                            envPrefix:""`
		Recaptcha       RecaptchaConfig `                                            envPrefix:"RECAPTCHA_"`
		Password        PasswordConfig  `                                            envPrefix:"PASSWORD_"`
	}

	// PasswordConfig is the password complexity policy enforced on
	// registration / password change and published at GET /api/auth/password/policy.
	// MaxLength defaults to 72 — bcrypt ignores bytes beyond that.
	PasswordConfig struct {
		MinLength            int `env:"MIN_LENGTH"             envDefault:"8"`
		MaxLength            int `env:"MAX_LENGTH"             envDefault:"72"`
		MinCapitalLetters    int `env:"MIN_CAPITAL_LETTERS"    envDefault:"1"`
		MinSmallLetters      int `env:"MIN_SMALL_LETTERS"      envDefault:"1"`
		MinDigits            int `env:"MIN_DIGITS"             envDefault:"1"`
		MinSpecialCharacters int `env:"MIN_SPECIAL_CHARACTERS" envDefault:"0"`
	}

	// OAuthConfig keeps the historical flat env names (GOOGLE_CLIENT_ID,
	// OAUTH_STATE_SIGNATURE) — the Google prefix is provider-level, not nested
	// under OAUTH_, so deployments keep working.
	OAuthConfig struct {
		Google              OAuthProviderConfig `envPrefix:"GOOGLE_"`
		RedirectURLTemplate string              // derived from Domain in MustGetConfig
		StateSignature      string              `                    env:"OAUTH_STATE_SIGNATURE"`
		StateTTL            time.Duration       `                    env:"OAUTH_STATE_TTL"       envDefault:"15m"`
	}

	OAuthProviderConfig struct {
		ClientID     string `env:"CLIENT_ID"`
		ClientSecret string `env:"SECRET"`
	}

	RecaptchaConfig struct {
		SecretKey string  `env:"SECRET"`
		SiteKey   string  `env:"SITE_KEY"`
		ProjectID string  `env:"PROJECT"`
		APIKey    string  `env:"API_KEY"`
		Score     float32 `env:"SCORE"    envDefault:"0.5"`
	}

	HTTPControllerConfig struct {
		Server            HTTPServerConfig `envPrefix:"HTTP_SERVER_"`
		EnableSwaggerDocs bool
	}

	HTTPServerConfig struct {
		Host               string        `env:"HOST"          envDefault:"0.0.0.0"`
		Port               string        `env:"PORT"          envDefault:"80"`
		ReadTimeout        time.Duration `env:"READ_TIMEOUT"  envDefault:"10s"`
		WriteTimeout       time.Duration `env:"WRITE_TIMEOUT" envDefault:"10s"`
		MaxHeaderMegabytes int           `env:"MAX_HEADER_MB" envDefault:"1"`
		TLS                TLSConfig     `                                         envPrefix:"TLS_"`
	}

	// TLSConfig is shared by the inbound HTTP server (presents CertFile/KeyFile;
	// CAFile unused) and outbound mTLS clients such as the agent (CertFile/KeyFile
	// is the CLIENT certificate we present, CAFile verifies the peer's server
	// certificate; an empty CAFile means the system roots, for a publicly trusted
	// certificate). Cert/key have no tag default so each user sets its own; the
	// HTTP server's historical /certificates defaults are applied in
	// populateForAllConfig.
	TLSConfig struct {
		Enabled  bool   `env:"ENABLED"   envDefault:"false"`
		CertFile string `env:"CERT_FILE"`
		KeyFile  string `env:"KEY_FILE"`
		CAFile   string `env:"CA_FILE"`
	}

	// MediaConfig bounds uploads and schedules unreferenced-file GC for the
	// media subsystem (exercise attachments).
	MediaConfig struct {
		MaxUploadBytes int64         `env:"MAX_UPLOAD_BYTES" envDefault:"52428800"` // 50 MiB
		GCGrace        time.Duration `env:"GC_GRACE"         envDefault:"24h"`
	}

	// VPNConfig seals the stored VPN client configs (participants' and test deploys'
	// tester keys). Empty key disables VPN config storage; an invalid one is fatal.
	VPNConfig struct {
		SecretsKey string `env:"SECRETS_KEY"` // 64 hex chars → AES-256
	}

	// PlatformConfig seals platform-level settings secrets (SMTP/mail passwords and
	// the like). Empty key disables them; an invalid one is fatal.
	PlatformConfig struct {
		SecretsKey string `env:"SECRETS_KEY"` // 64 hex chars → AES-256
	}

	// ExerciseConfig holds catalog secret handling and flag generation policy.
	// Empty key disables exercise secret env vars (saving one yields a 409).
	ExerciseConfig struct {
		SecretsKey      string `env:"SECRETS_KEY"` // 64 hex chars → AES-256
		FlagRandomBytes int    `env:"FLAG_RANDOM_BYTES" envDefault:"20"`
		FlagWarningBits int    `env:"FLAG_WARNING_BITS" envDefault:"20"`
		// MaxActiveTestDeploys is how many test labs one user may run at the same time.
		MaxActiveTestDeploys int `env:"MAX_ACTIVE_TEST_DEPLOYS" envDefault:"3"`
		// StandDeployBudget caps the Lab deploy calls the stand engine sends to the agent per event and
		// pass (a pass runs every 10 seconds). It only protects the agent API from a burst: the launch
		// pacing (order, waves, image preparation) is done by the Laboratory operator, which queues the
		// Labs, so the backend hands them over quickly.
		StandDeployBudget int `env:"STAND_DEPLOY_BUDGET" envDefault:"200"`
		// DevicePersistence says the cluster lets devices keep their state (the chart's
		// devices.statePersistence and the tenant policy). The exercise editor offers the option only
		// when it is true; the agent refuses it otherwise.
		DevicePersistence bool `env:"DEVICE_PERSISTENCE" envDefault:"true"`
		// StandPrewarmLead is how long before an event's stand deploy time its images are fetched
		// into the platform image cache (the agent's PrewarmImages); 0 turns it off. With the
		// default 30 minutes deploy lead the images are warmed an hour before the start.
		StandPrewarmLead time.Duration `env:"STAND_PREWARM_LEAD" envDefault:"30m"`
	}
)

// FlagRateLimitConfig bounds flag submissions in sliding windows: per team
// and challenge (also the moderators board check) and per team overall.
type FlagRateLimitConfig struct {
	ChallengeAttempts int32         `env:"CHALLENGE_ATTEMPTS" envDefault:"5"`
	ChallengeWindow   time.Duration `env:"CHALLENGE_WINDOW"   envDefault:"30s"`
	TeamAttempts      int32         `env:"TEAM_ATTEMPTS"      envDefault:"20"`
	TeamWindow        time.Duration `env:"TEAM_WINDOW"        envDefault:"1m"`
}

// RetentionConfig holds the data retention periods of the Privacy Policy.
// The defaults are the published periods; change them only together with the
// policy text.
type RetentionConfig struct {
	SessionAfterExpiry       time.Duration `env:"SESSION_AFTER_EXPIRY"         envDefault:"2160h"`  // 90 days
	LabTelemetry             time.Duration `env:"LAB_TELEMETRY"                envDefault:"2160h"`  // 90 days
	DeliveryLog              time.Duration `env:"DELIVERY_LOG"                 envDefault:"4320h"`  // 180 days
	FormAnswersAfterEventEnd time.Duration `env:"FORM_ANSWERS_AFTER_EVENT_END" envDefault:"8760h"`  // 365 days
	InactiveAccount          time.Duration `env:"INACTIVE_ACCOUNT"             envDefault:"26280h"` // 3 x 365 days
	InactivityGrace          time.Duration `env:"INACTIVITY_GRACE"             envDefault:"720h"`   // 30 days
	ExpiredInvitation        time.Duration `env:"EXPIRED_INVITATION"           envDefault:"168h"`   // 7 days
	PendingAccount           time.Duration `env:"PENDING_ACCOUNT"              envDefault:"720h"`   // 30 days
	UnusedAnswerFile         time.Duration `env:"UNUSED_ANSWER_FILE"           envDefault:"24h"`    // 1 day
	SignalHistory            time.Duration `env:"SIGNAL_HISTORY"               envDefault:"8760h"`  // 365 days
	EventAnalyticsAfterEnd   time.Duration `env:"EVENT_ANALYTICS_AFTER_END"    envDefault:"8760h"`  // 365 days
	BatchSize                int32         `env:"BATCH_SIZE"                   envDefault:"1000"`
}

func (c FlagRateLimitConfig) Validate() error {
	if c.ChallengeAttempts < 1 || c.TeamAttempts < 1 {
		return errors.New("flag rate limit: FLAG_RATE_LIMIT_*_ATTEMPTS must be at least 1")
	}
	if c.ChallengeWindow < time.Second || c.TeamWindow < time.Second {
		return errors.New("flag rate limit: FLAG_RATE_LIMIT_*_WINDOW must be at least 1s")
	}
	return nil
}

var instanceIDPattern = regexp.MustCompile(`^[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?$`)

// Validate checks the instance id is a valid Kubernetes label value: it is used as one.
func (c AgentConfig) Validate() error {
	if !instanceIDPattern.MatchString(c.InstanceID) {
		return errors.New("infrastructure agent: AGENT_INSTANCE_ID must be a label value (1-63 characters of letters, digits, '-', '_' or '.', starting and ending with a letter or digit)")
	}
	return nil
}

func (c ExerciseConfig) Validate() error {
	if c.FlagRandomBytes < 1 || c.FlagRandomBytes > 1024 {
		return errors.New("exercise: EXERCISE_FLAG_RANDOM_BYTES must be between 1 and 1024")
	}
	if c.FlagWarningBits < 1 || c.FlagWarningBits > 1024 {
		return errors.New("exercise: EXERCISE_FLAG_WARNING_BITS must be between 1 and 1024")
	}
	if c.MaxActiveTestDeploys < 1 || c.MaxActiveTestDeploys > 20 {
		return errors.New("exercise: EXERCISE_MAX_ACTIVE_TEST_DEPLOYS must be between 1 and 20")
	}
	if c.StandPrewarmLead < 0 || c.StandPrewarmLead > 24*time.Hour {
		return errors.New("exercise: EXERCISE_STAND_PREWARM_LEAD must be between 0 and 24h")
	}
	if c.StandDeployBudget < 1 || c.StandDeployBudget > 5000 {
		return errors.New("exercise: EXERCISE_STAND_DEPLOY_BUDGET must be between 1 and 5000")
	}
	return nil
}

// Validate ensures exactly one reCAPTCHA mode is fully configured. The mode is
// selected by ProjectID: set → Enterprise (needs APIKey + SiteKey); unset →
// classic v3 (needs SecretKey). reCAPTCHA is mandatory in every environment.
func (c RecaptchaConfig) Validate() error {
	if c.ProjectID != "" {
		if c.APIKey == "" || c.SiteKey == "" {
			return errors.New(
				"recaptcha: Enterprise mode (RECAPTCHA_PROJECT set) requires RECAPTCHA_API_KEY and RECAPTCHA_SITE_KEY",
			)
		}
		return nil
	}
	if c.SecretKey == "" {
		return errors.New(
			"recaptcha: configure classic (RECAPTCHA_SECRET) or Enterprise (RECAPTCHA_PROJECT + RECAPTCHA_API_KEY + RECAPTCHA_SITE_KEY) — reCAPTCHA is mandatory",
		)
	}
	return nil
}

// Policy is the configured retention policy.
func (c RetentionConfig) Policy() retentionModel.Policy {
	return retentionModel.Policy{
		SessionAfterExpiry:       c.SessionAfterExpiry,
		LabTelemetry:             c.LabTelemetry,
		DeliveryLog:              c.DeliveryLog,
		FormAnswersAfterEventEnd: c.FormAnswersAfterEventEnd,
		InactiveAccount:          c.InactiveAccount,
		InactivityGrace:          c.InactivityGrace,
		ExpiredInvitation:        c.ExpiredInvitation,
		PendingAccount:           c.PendingAccount,
		UnusedAnswerFile:         c.UnusedAnswerFile,
		SignalHistory:            c.SignalHistory,
		EventAnalyticsAfterEnd:   c.EventAnalyticsAfterEnd,
		BatchSize:                c.BatchSize,
	}
}

// DSN builds a pgx connection string.
func (p PostgresConfig) DSN() string {
	return "postgres://" + p.User + ":" + p.Password + "@" + p.Host + ":" + p.Port + "/" + p.Database + "?sslmode=" + p.SSLMode
}

func MustGetConfig() *Config {
	log.Info().Msg("Reading daemon configuration")

	// ParseAs takes the struct type itself (a pointer type parameter fails at
	// runtime with "expected a pointer to a Struct"). No RequiredIfNoDef:
	// most settings are optional with code-level validation (e.g. Recaptcha).
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		log.Fatal().Err(err).Msg("Config: invalid environment variables")
		return nil
	}
	instance := &cfg

	instance.populateForAllConfig()

	if err = instance.Auth.Recaptcha.Validate(); err != nil {
		log.Fatal().Err(err).Msg("Config: invalid reCAPTCHA configuration")
	}
	if err = instance.Exercise.Validate(); err != nil {
		log.Fatal().Err(err).Msg("Config: invalid exercise configuration")
	}
	if err = instance.FlagRateLimit.Validate(); err != nil {
		log.Fatal().Err(err).Msg("Config: invalid flag rate limit configuration")
	}
	if err = instance.Infrastructure.Agent.Validate(); err != nil {
		log.Fatal().Err(err).Msg("Config: invalid infrastructure agent configuration")
	}
	if err = instance.Retention.Policy().Validate(); err != nil {
		log.Fatal().Err(err).Msg("Config: invalid retention configuration")
	}

	return instance
}

func (c *Config) populateForAllConfig() {
	c.HTTPController.EnableSwaggerDocs = c.Environment != Production

	if c.Environment == Development {
		c.Infrastructure.Postgres.MigrationsPath = "internal/delivery/repository/postgres/migrations"
	} else {
		c.Infrastructure.Postgres.MigrationsPath = "migrations"
	}

	c.Auth.OAuth.RedirectURLTemplate = fmt.Sprintf(
		"https://%s.%s/api/auth/%%s/callback",
		APISubdomain,
		c.Auth.Domain,
	)

	// HTTP server's historical default cert/key paths (moved out of the shared
	// TLSConfig tags so they don't leak onto mTLS clients like the agent).
	if c.HTTPController.Server.TLS.CertFile == "" {
		c.HTTPController.Server.TLS.CertFile = "/certificates/tls.crt"
	}
	if c.HTTPController.Server.TLS.KeyFile == "" {
		c.HTTPController.Server.TLS.KeyFile = "/certificates/tls.key"
	}
}
