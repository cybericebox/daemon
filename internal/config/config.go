package config

import (
	"errors"
	"fmt"
	"net/mail"
	"net/netip"
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
		LabSession     LabSessionConfig     `                                   envPrefix:"LAB_SESSION_"`
		Tunables       TunablesConfig       `                                   envPrefix:""`
		VPN            VPNConfig            `                                   envPrefix:"VPN_"`
		Platform       PlatformConfig       `                                   envPrefix:"PLATFORM_"`
	}

	// LabAccessConfig sets how long the lab access tokens (the /_auth links) of the laboratory L7
	// proxy can be opened. They are signed with the access key of the agent that holds the lab group
	// (every agent is a tenant with its own key), never with a platform-wide key.
	LabAccessConfig struct {
		// TokenTTL is how long an access token can be opened: it is exchanged for
		// the proxy's own cookie at once. Each agent's proxy states its own cap, which this never exceeds.
		TokenTTL time.Duration `env:"TOKEN_TTL" envDefault:"1m"`
	}

	// LabSessionConfig is how long a lab web session lasts when the caller names no end (LAB_SESSION_TTL).
	LabSessionConfig struct {
		TTL time.Duration `env:"TTL" envDefault:"24h"`
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

	// TunablesConfig holds the operator-set limits and timings that have no other section. The
	// defaults mirror the values of the deployment env template.
	TunablesConfig struct {
		// EventStandDeployTimeout fails a Lab the agent accepted but never reported ready.
		EventStandDeployTimeout time.Duration `env:"EVENT_STAND_DEPLOY_TIMEOUT" envDefault:"20m"`
		// SSEMaxLifetime bounds one event stream; the client reconnects with Last-Event-ID.
		SSEMaxLifetime time.Duration `env:"SSE_MAX_LIFETIME" envDefault:"30m"`
		// LiveScreenLinkMaxTTL caps «until the event ends» of a live screen link.
		LiveScreenLinkMaxTTL time.Duration `env:"LIVE_SCREEN_LINK_MAX_TTL" envDefault:"1440h"`
		// EventDefaultMaxTeamSize is the team size limit of a new event.
		EventDefaultMaxTeamSize int32 `env:"EVENT_DEFAULT_MAX_TEAM_SIZE" envDefault:"5"`

		// SMTPAllowedPorts are the ports an organizer may use for an event SMTP server.
		SMTPAllowedPorts []int `env:"SMTP_ALLOWED_PORTS" envDefault:"25,465,587,2525"`

		// The mail send limiter: the longest a worker waits for its turn, how long a message waits
		// when the daily quota is used, how often the delivered count is re-read, the quota window,
		// and the upper bounds an admin may set for the provider limits.
		MailMaxRateWait       time.Duration `env:"MAIL_MAX_RATE_WAIT"       envDefault:"20s"`
		MailQuotaRetryAfter   time.Duration `env:"MAIL_QUOTA_RETRY_AFTER"   envDefault:"10m"`
		MailQuotaRecheck      time.Duration `env:"MAIL_QUOTA_RECHECK"       envDefault:"30s"`
		MailQuotaWindow       time.Duration `env:"MAIL_QUOTA_WINDOW"        envDefault:"24h"`
		MailMaxPerSecondLimit float64       `env:"MAIL_MAX_PER_SECOND_LIMIT" envDefault:"10000"`
		MailDailyQuotaLimit   int           `env:"MAIL_DAILY_QUOTA_LIMIT"   envDefault:"1000000000"`

		// Upload limits, bytes.
		AvatarMaxBytes              int64 `env:"AVATAR_MAX_BYTES"                envDefault:"5242880"`
		EventLogoMaxBytes           int   `env:"EVENT_LOGO_MAX_BYTES"            envDefault:"2097152"`
		EventPreviewPictureMaxBytes int   `env:"EVENT_PREVIEW_PICTURE_MAX_BYTES" envDefault:"5242880"`
		EventContentImageMaxBytes   int   `env:"EVENT_CONTENT_IMAGE_MAX_BYTES"   envDefault:"5242880"`
		LiveLogoMaxBytes            int   `env:"LIVE_LOGO_MAX_BYTES"             envDefault:"1048576"`
		// Email template images: the raw upload, the processed image, its width and the decoded pixels.
		EmailImageUploadMaxBytes int `env:"EMAIL_IMAGE_UPLOAD_MAX_BYTES" envDefault:"10485760"`
		EmailImageMaxBytes       int `env:"EMAIL_IMAGE_MAX_BYTES"        envDefault:"307200"`
		EmailImageMaxWidth       int `env:"EMAIL_IMAGE_MAX_WIDTH"        envDefault:"1200"`
		EmailImageMaxPixels      int `env:"EMAIL_IMAGE_MAX_PIXELS"       envDefault:"24000000"`
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
		// Insecure lets the env transport go on without TLS when the server does not offer STARTTLS (a
		// development mail catcher). Never set it for a real provider: credentials would travel in the clear.
		Insecure   bool `env:"INSECURE"`
		DailyQuota int  `env:"DAILY_QUOTA"`
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
		Hosts HostsConfig `                                            envPrefix:""`
		// SupportEmail is the default Reply-To of all mail and the contact in the mail footer.
		SupportEmail   string `env:"SUPPORT_EMAIL,required"`
		TokenSignature string `env:"JWT_TOKEN_SIGNATURE"`
		// SetupTokenTTL is the life of a setup link (account setup and invitations).
		SetupTokenTTL time.Duration `env:"SETUP_TOKEN_TTL" envDefault:"168h"`
		// SessionIdleTTL ends a session that was not used for this long (it slides on every use);
		// SessionAbsoluteTTL ends it this long after sign-in however busy it is (the cookie's own
		// lifetime). A stolen cookie therefore cannot be kept alive for ever by using it.
		SessionIdleTTL     time.Duration `env:"SESSION_IDLE_TTL"     envDefault:"336h"`
		SessionAbsoluteTTL time.Duration `env:"SESSION_ABSOLUTE_TTL" envDefault:"720h"`
		// SessionMaxPerUser is how many sessions one account keeps at once; signing in over the cap
		// ends the oldest. 0 means no cap.
		SessionMaxPerUser int `env:"SESSION_MAX_PER_USER" envDefault:"10"`
		// SignupSetupTokenTTL is the life of the setup link mailed to someone who signed up (or came
		// through Google) by themselves; invitations keep SetupTokenTTL.
		SignupSetupTokenTTL time.Duration   `env:"SIGNUP_SETUP_TOKEN_TTL" envDefault:"24h"`
		TemporalCodeTTL     time.Duration   `env:"TEMPORAL_CODE_TTL"   envDefault:"1h"`
		SuperAdminEmail     string          `env:"SUPER_ADMIN_EMAIL"`
		OAuth               OAuthConfig     `                                            envPrefix:""`
		Recaptcha           RecaptchaConfig `                                            envPrefix:"RECAPTCHA_"`
		Password            PasswordConfig  `                                            envPrefix:"PASSWORD_"`
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
		// TrustedProxies are the networks (CIDRs or addresses) of the proxies in front of the daemon (the
		// ingress, the CDN). The client address is taken from X-Forwarded-For only for a request that comes
		// from one of them; with none listed (the default) it is the connection's own address and the header
		// is ignored, so a client cannot choose its address.
		TrustedProxies []string `env:"TRUSTED_PROXIES"`
		// MaxBodyBytes caps every request body; an upload route states its own larger cap.
		MaxBodyBytes int64 `env:"MAX_REQUEST_BODY_BYTES" envDefault:"10485760"`
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
		// StandPrewarmLead is how long before an event's stand deploy time its images are fetched
		// into the platform image cache (the agent's PrewarmImages); 0 turns it off. With the
		// default 30 minutes deploy lead the images are warmed an hour before the start.
		StandPrewarmLead time.Duration `env:"STAND_PREWARM_LEAD" envDefault:"30m"`
		// TestDeployTTL is the lease of a catalog author's test lab; extending it never goes past
		// TestDeployTTLMax counted from the start.
		TestDeployTTL    time.Duration `env:"TEST_DEPLOY_TTL"     envDefault:"2h"`
		TestDeployTTLMax time.Duration `env:"TEST_DEPLOY_TTL_MAX" envDefault:"8h"`
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

// Validate checks the proxy list and the body cap.
func (c HTTPControllerConfig) Validate() error {
	for _, entry := range c.TrustedProxies {
		if _, err := netip.ParsePrefix(entry); err != nil {
			if _, addrErr := netip.ParseAddr(entry); addrErr != nil {
				return fmt.Errorf("TRUSTED_PROXIES: %q is not an address or a CIDR", entry)
			}
		}
	}
	if c.MaxBodyBytes < 1 {
		return errors.New("MAX_REQUEST_BODY_BYTES must be at least 1")
	}
	return nil
}

// Validate rejects values that would turn a limit off by accident.
func (c TunablesConfig) Validate() error {
	durations := map[string]time.Duration{
		"EVENT_STAND_DEPLOY_TIMEOUT": c.EventStandDeployTimeout, "SSE_MAX_LIFETIME": c.SSEMaxLifetime,
		"LIVE_SCREEN_LINK_MAX_TTL": c.LiveScreenLinkMaxTTL, "MAIL_MAX_RATE_WAIT": c.MailMaxRateWait,
		"MAIL_QUOTA_RETRY_AFTER": c.MailQuotaRetryAfter, "MAIL_QUOTA_RECHECK": c.MailQuotaRecheck,
		"MAIL_QUOTA_WINDOW": c.MailQuotaWindow,
	}
	for name, v := range durations {
		if v <= 0 {
			return fmt.Errorf("%s must be positive", name)
		}
	}
	sizes := map[string]int64{
		"EVENT_DEFAULT_MAX_TEAM_SIZE": int64(c.EventDefaultMaxTeamSize), "MAIL_DAILY_QUOTA_LIMIT": int64(c.MailDailyQuotaLimit),
		"AVATAR_MAX_BYTES": c.AvatarMaxBytes, "EVENT_LOGO_MAX_BYTES": int64(c.EventLogoMaxBytes),
		"EVENT_PREVIEW_PICTURE_MAX_BYTES": int64(c.EventPreviewPictureMaxBytes),
		"EVENT_CONTENT_IMAGE_MAX_BYTES":   int64(c.EventContentImageMaxBytes), "LIVE_LOGO_MAX_BYTES": int64(c.LiveLogoMaxBytes),
		"EMAIL_IMAGE_UPLOAD_MAX_BYTES": int64(c.EmailImageUploadMaxBytes), "EMAIL_IMAGE_MAX_BYTES": int64(c.EmailImageMaxBytes),
		"EMAIL_IMAGE_MAX_WIDTH": int64(c.EmailImageMaxWidth), "EMAIL_IMAGE_MAX_PIXELS": int64(c.EmailImageMaxPixels),
	}
	for name, v := range sizes {
		if v < 1 {
			return fmt.Errorf("%s must be at least 1", name)
		}
	}
	for _, port := range c.SMTPAllowedPorts {
		if port < 1 || port > 65535 {
			return errors.New("SMTP_ALLOWED_PORTS must list ports between 1 and 65535")
		}
	}
	if c.MailMaxPerSecondLimit <= 0 {
		return errors.New("MAIL_MAX_PER_SECOND_LIMIT must be positive")
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
	if c.TestDeployTTL <= 0 || c.TestDeployTTLMax < c.TestDeployTTL {
		return errors.New("exercise: EXERCISE_TEST_DEPLOY_TTL must be positive and not above EXERCISE_TEST_DEPLOY_TTL_MAX")
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

// MinSigningSecretBytes is the least a signing secret (JWT_TOKEN_SIGNATURE,
// OAUTH_STATE_SIGNATURE) may be: an HMAC-SHA256 key shorter than its hash is
// brute-forceable offline from one issued token.
const MinSigningSecretBytes = 32

// MinRecaptchaScore is the lowest accepted RECAPTCHA_SCORE: below it (0 accepts
// every bot) the check no longer separates anyone.
const MinRecaptchaScore = 0.3

// Weaknesses lists the settings that make the authentication weaker than it
// should be. In production every one of them stops the start; elsewhere they are
// logged, so a throw-away development key keeps working.
func (c AuthConfig) Weaknesses() []string {
	var out []string
	if len(c.TokenSignature) < MinSigningSecretBytes {
		out = append(out, fmt.Sprintf("JWT_TOKEN_SIGNATURE must be random and at least %d bytes", MinSigningSecretBytes))
	}
	switch {
	case c.OAuth.Google.ClientID != "" && c.OAuth.StateSignature == "":
		out = append(out, "GOOGLE_CLIENT_ID is set but OAUTH_STATE_SIGNATURE is empty: Google sign-in would be silently disabled")
	case c.OAuth.StateSignature != "" && len(c.OAuth.StateSignature) < MinSigningSecretBytes:
		out = append(out, fmt.Sprintf("OAUTH_STATE_SIGNATURE must be random and at least %d bytes", MinSigningSecretBytes))
	}
	if c.OAuth.StateSignature != "" && c.OAuth.StateSignature == c.TokenSignature {
		out = append(out, "OAUTH_STATE_SIGNATURE must differ from JWT_TOKEN_SIGNATURE")
	}
	if c.Recaptcha.Score < MinRecaptchaScore {
		out = append(out, fmt.Sprintf("RECAPTCHA_SCORE must be at least %.1f", MinRecaptchaScore))
	}
	return out
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

	if err = instance.Auth.Hosts.Validate(); err != nil {
		log.Fatal().Err(err).Msg("Config: invalid host configuration")
	}
	if addr, perr := mail.ParseAddress(instance.Auth.SupportEmail); perr != nil || addr.Address != instance.Auth.SupportEmail {
		log.Fatal().Msg("Config: SUPPORT_EMAIL must be a bare email address")
	}

	instance.populateForAllConfig()

	if err = instance.Auth.Recaptcha.Validate(); err != nil {
		log.Fatal().Err(err).Msg("Config: invalid reCAPTCHA configuration")
	}
	for _, weakness := range instance.Auth.Weaknesses() {
		if instance.Environment == Production {
			log.Fatal().Msg("Config: weak authentication setting: " + weakness)
		}
		log.Warn().Msg("Config: weak authentication setting (fatal in production): " + weakness)
	}
	if err = instance.HTTPController.Validate(); err != nil {
		log.Fatal().Err(err).Msg("Config: invalid HTTP controller configuration")
	}
	if err = instance.Tunables.Validate(); err != nil {
		log.Fatal().Err(err).Msg("Config: invalid limits and timings")
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
	// The API docs describe every route, internal ones included: development only (not stage either).
	c.HTTPController.EnableSwaggerDocs = c.Environment == Development

	if c.Environment == Development {
		c.Infrastructure.Postgres.MigrationsPath = "internal/delivery/repository/postgres/migrations"
	} else {
		c.Infrastructure.Postgres.MigrationsPath = "migrations"
	}

	c.Auth.OAuth.RedirectURLTemplate = c.Auth.Hosts.APIURL("/api/auth/%s/callback")

	// HTTP server's historical default cert/key paths (moved out of the shared
	// TLSConfig tags so they don't leak onto mTLS clients like the agent).
	if c.HTTPController.Server.TLS.CertFile == "" {
		c.HTTPController.Server.TLS.CertFile = "/certificates/tls.crt"
	}
	if c.HTTPController.Server.TLS.KeyFile == "" {
		c.HTTPController.Server.TLS.KeyFile = "/certificates/tls.key"
	}
}
