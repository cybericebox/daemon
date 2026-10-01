package config

import (
	"os"
	"testing"
	"time"

	retentionModel "github.com/cybericebox/daemon/internal/model/retention"
)

// testHosts is the host set every test config starts with: the hosts are required, so MustGetConfig
// stops without them.
var testHosts = map[string]string{
	"SUPPORT_EMAIL": "support@example.test", "MAIN_HOST": "example.test", "API_HOST": "api.example.test", "ID_HOST": "id.example.test",
	"ADMIN_HOST": "admin.example.test", "EXERCISES_HOST": "exercises.example.test", "EVENT_DOMAIN": "example.test",
}

func TestMain(m *testing.M) {
	for k, v := range testHosts {
		_ = os.Setenv(k, v)
	}
	os.Exit(m.Run())
}

func setTestHosts(t *testing.T) {
	t.Helper()
	for k, v := range testHosts {
		t.Setenv(k, v)
	}
}

func TestAuthConfig_ParsedFromEnv(t *testing.T) {
	setTestHosts(t)
	t.Setenv("JWT_TOKEN_SIGNATURE", "sig")
	t.Setenv("OAUTH_STATE_SIGNATURE", "oauth-sig")
	t.Setenv("GOOGLE_CLIENT_ID", "gcid")
	t.Setenv("RECAPTCHA_SECRET", "rsecret")

	cfg := MustGetConfig()
	if cfg.Auth.Hosts.API != "api.example.test" || cfg.Auth.Hosts.EventDomain != "example.test" {
		t.Fatalf("Hosts: got %+v", cfg.Auth.Hosts)
	}
	if cfg.Auth.TokenSignature != "sig" {
		t.Fatalf("TokenSignature: got %q", cfg.Auth.TokenSignature)
	}
	if cfg.Auth.SessionIdleTTL != 720*time.Hour {
		t.Fatalf("SessionIdleTTL default: got %v want 720h", cfg.Auth.SessionIdleTTL)
	}
	if cfg.Auth.TemporalCodeTTL != time.Hour {
		t.Fatalf("TemporalCodeTTL default: got %v want 1h", cfg.Auth.TemporalCodeTTL)
	}
	if cfg.Auth.OAuth.StateSignature != "oauth-sig" {
		t.Fatalf("OAuth.StateSignature: got %q", cfg.Auth.OAuth.StateSignature)
	}
	if cfg.Auth.OAuth.StateTTL != 15*time.Minute {
		t.Fatalf("OAuth.StateTTL default: got %v want 15m", cfg.Auth.OAuth.StateTTL)
	}
	if cfg.Auth.OAuth.Google.ClientID != "gcid" {
		t.Fatalf("OAuth.Google.ClientID: got %q", cfg.Auth.OAuth.Google.ClientID)
	}
	if cfg.Auth.Recaptcha.SecretKey != "rsecret" {
		t.Fatalf("Recaptcha.SecretKey: got %q", cfg.Auth.Recaptcha.SecretKey)
	}
	if cfg.Auth.Recaptcha.Score != 0.5 {
		t.Fatalf("Recaptcha.Score default: got %v want 0.5", cfg.Auth.Recaptcha.Score)
	}
	if cfg.Auth.OAuth.RedirectURLTemplate != "https://api.example.test/api/auth/%s/callback" {
		t.Fatalf("RedirectURLTemplate: got %q", cfg.Auth.OAuth.RedirectURLTemplate)
	}
}

func TestPasswordConfig_Defaults(t *testing.T) {
	t.Setenv("RECAPTCHA_SECRET", "rsecret")

	got := MustGetConfig().Auth.Password
	want := PasswordConfig{
		MinLength:            8,
		MaxLength:            72,
		MinCapitalLetters:    1,
		MinSmallLetters:      1,
		MinDigits:            1,
		MinSpecialCharacters: 0,
	}
	if got != want {
		t.Fatalf("Password defaults: got %+v want %+v", got, want)
	}
}

func TestPasswordConfig_ParsedFromEnv(t *testing.T) {
	t.Setenv("RECAPTCHA_SECRET", "rsecret")
	t.Setenv("PASSWORD_MIN_LENGTH", "12")
	t.Setenv("PASSWORD_MAX_LENGTH", "64")
	t.Setenv("PASSWORD_MIN_CAPITAL_LETTERS", "2")
	t.Setenv("PASSWORD_MIN_SMALL_LETTERS", "3")
	t.Setenv("PASSWORD_MIN_DIGITS", "4")
	t.Setenv("PASSWORD_MIN_SPECIAL_CHARACTERS", "5")

	got := MustGetConfig().Auth.Password
	want := PasswordConfig{
		MinLength:            12,
		MaxLength:            64,
		MinCapitalLetters:    2,
		MinSmallLetters:      3,
		MinDigits:            4,
		MinSpecialCharacters: 5,
	}
	if got != want {
		t.Fatalf("Password from env: got %+v want %+v", got, want)
	}
}

func TestExerciseFlagPolicy_DefaultsAndRejectsInvalidValues(t *testing.T) {
	t.Setenv("RECAPTCHA_SECRET", "rsecret")
	got := MustGetConfig().Exercise
	if got.FlagRandomBytes != 20 || got.FlagWarningBits != 20 || got.Validate() != nil {
		t.Fatalf("invalid default policy: %+v", got)
	}
	for _, invalid := range []ExerciseConfig{
		{FlagRandomBytes: 0, FlagWarningBits: 20},
		{FlagRandomBytes: 20, FlagWarningBits: 0},
		{FlagRandomBytes: 1025, FlagWarningBits: 20},
	} {
		if invalid.Validate() == nil {
			t.Fatalf("accepted invalid policy: %+v", invalid)
		}
	}
}

func TestFlagRateLimit_DefaultsEnvAndRejectsInvalidValues(t *testing.T) {
	t.Setenv("RECAPTCHA_SECRET", "rsecret")
	got := MustGetConfig().FlagRateLimit
	want := FlagRateLimitConfig{ChallengeAttempts: 5, ChallengeWindow: 30 * time.Second, TeamAttempts: 20, TeamWindow: time.Minute}
	if got != want || got.Validate() != nil {
		t.Fatalf("default flag rate limit: got %+v want %+v", got, want)
	}

	t.Setenv("FLAG_RATE_LIMIT_CHALLENGE_ATTEMPTS", "3")
	t.Setenv("FLAG_RATE_LIMIT_TEAM_WINDOW", "2m")
	got = MustGetConfig().FlagRateLimit
	if got.ChallengeAttempts != 3 || got.TeamWindow != 2*time.Minute {
		t.Fatalf("flag rate limit from env: %+v", got)
	}

	for _, invalid := range []FlagRateLimitConfig{
		{ChallengeAttempts: 0, ChallengeWindow: time.Second, TeamAttempts: 1, TeamWindow: time.Second},
		{ChallengeAttempts: 1, ChallengeWindow: time.Second, TeamAttempts: 1, TeamWindow: 0},
	} {
		if invalid.Validate() == nil {
			t.Fatalf("accepted invalid flag rate limit: %+v", invalid)
		}
	}
}

func TestRetentionConfig_DefaultsMatchPrivacyPolicy(t *testing.T) {
	t.Setenv("RECAPTCHA_SECRET", "rsecret")

	if got, want := MustGetConfig().Retention.Policy(), retentionModel.DefaultPolicy(); got != want {
		t.Fatalf("retention defaults: got %+v, want %+v", got, want)
	}
}

func TestRetentionConfig_ParsedFromEnv(t *testing.T) {
	t.Setenv("RECAPTCHA_SECRET", "rsecret")
	t.Setenv("RETENTION_DELIVERY_LOG", "720h")
	t.Setenv("RETENTION_BATCH_SIZE", "50")

	got := MustGetConfig().Retention
	if got.DeliveryLog != 720*time.Hour || got.BatchSize != 50 {
		t.Fatalf("retention env overrides: got %+v", got)
	}
}

func TestSecretKeys_AreThreeSeparateEnvVars(t *testing.T) {
	t.Setenv("RECAPTCHA_SECRET", "rsecret")
	t.Setenv("EXERCISE_SECRETS_KEY", "aa")
	t.Setenv("VPN_SECRETS_KEY", "bb")
	t.Setenv("PLATFORM_SECRETS_KEY", "cc")
	cfg := MustGetConfig()
	if cfg.Exercise.SecretsKey != "aa" || cfg.VPN.SecretsKey != "bb" || cfg.Platform.SecretsKey != "cc" {
		t.Fatalf("each family reads its own variable: %q %q %q", cfg.Exercise.SecretsKey, cfg.VPN.SecretsKey, cfg.Platform.SecretsKey)
	}
}

func TestExerciseMaxActiveTestDeploys_DefaultsToThreeAndIsValidated(t *testing.T) {
	t.Setenv("RECAPTCHA_SECRET", "rsecret")
	got := MustGetConfig().Exercise
	if got.MaxActiveTestDeploys != 3 || got.Validate() != nil {
		t.Fatalf("default limit: %+v", got)
	}
	t.Setenv("EXERCISE_MAX_ACTIVE_TEST_DEPLOYS", "5")
	if MustGetConfig().Exercise.MaxActiveTestDeploys != 5 {
		t.Fatal("the limit is read from the environment")
	}
	for _, bad := range []int{0, -1, 21} {
		c := got
		c.MaxActiveTestDeploys = bad
		if c.Validate() == nil {
			t.Fatalf("accepted limit %d", bad)
		}
	}
}

func TestAgentBootstrapConfig(t *testing.T) {
	t.Setenv("RECAPTCHA_SECRET", "rsecret")
	t.Setenv("AGENT_ENDPOINT", "ctl.example.test:443")
	t.Setenv("AGENT_ENROLLMENT_TOKEN", "one-time")
	t.Setenv("AGENT_CA_FILE", "/ca/ca.crt")
	agent := MustGetConfig().Infrastructure.Agent
	if agent.Endpoint != "ctl.example.test:443" || agent.EnrollmentToken != "one-time" || agent.CAFile != "/ca/ca.crt" || agent.Name != "default" || agent.InstanceID != "cybericebox" {
		t.Fatalf("agent bootstrap config = %+v", agent)
	}
	t.Setenv("AGENT_NAME", "eu-1")
	if got := MustGetConfig().Infrastructure.Agent.Name; got != "eu-1" {
		t.Fatalf("name = %q", got)
	}
}

func TestExerciseConfigStandDeployBudgetBounds(t *testing.T) {
	valid := ExerciseConfig{FlagRandomBytes: 20, FlagWarningBits: 20, MaxActiveTestDeploys: 3, StandDeployBudget: 200, TestDeployTTL: 2 * time.Hour, TestDeployTTLMax: 8 * time.Hour}
	if err := valid.Validate(); err != nil {
		t.Fatalf("default budget rejected: %v", err)
	}
	for _, budget := range []int{0, -1, 5001} {
		bad := valid
		bad.StandDeployBudget = budget
		if bad.Validate() == nil {
			t.Errorf("budget %d accepted", budget)
		}
	}
}

func TestExerciseConfigStandPrewarmLeadBounds(t *testing.T) {
	valid := ExerciseConfig{FlagRandomBytes: 20, FlagWarningBits: 20, MaxActiveTestDeploys: 3, StandDeployBudget: 200, TestDeployTTL: 2 * time.Hour, TestDeployTTLMax: 8 * time.Hour}
	for _, lead := range []time.Duration{0, 30 * time.Minute, 24 * time.Hour} {
		ok := valid
		ok.StandPrewarmLead = lead
		if err := ok.Validate(); err != nil {
			t.Errorf("lead %v rejected: %v", lead, err)
		}
	}
	for _, lead := range []time.Duration{-time.Minute, 25 * time.Hour} {
		bad := valid
		bad.StandPrewarmLead = lead
		if bad.Validate() == nil {
			t.Errorf("lead %v accepted", lead)
		}
	}
}

func TestAgentConfigInstanceIDMustBeALabelValue(t *testing.T) {
	for _, id := range []string{"cybericebox", "prod-1", "a", "A.b_c"} {
		if err := (AgentConfig{Endpoint: "agent:443", InstanceID: id}).Validate(); err != nil {
			t.Errorf("%q rejected: %v", id, err)
		}
	}
	for _, id := range []string{"", "-a", "a-", "has space", "x/y", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if (AgentConfig{Endpoint: "agent:443", InstanceID: id}).Validate() == nil {
			t.Errorf("%q accepted", id)
		}
	}
	// The instance id is also the label of agents configured in the admin: it must always be valid.
	if (AgentConfig{}).Validate() == nil {
		t.Error("an empty instance id is invalid even without the environment agent")
	}
}

func TestTunablesDefaultsAndOverrides(t *testing.T) {
	t.Setenv("RECAPTCHA_SECRET", "rsecret")
	cfg := MustGetConfig()
	tn := cfg.Tunables
	if tn.SSEMaxLifetime != 30*time.Minute || tn.EventStandDeployTimeout != 20*time.Minute || tn.LiveScreenLinkMaxTTL != 1440*time.Hour ||
		tn.AgentAccessKeyRetention != 15*time.Minute || tn.AgentCertRenewBefore != 240*time.Hour || tn.EventDefaultMaxTeamSize != 5 ||
		tn.MailMaxRateWait != 20*time.Second || tn.MailQuotaWindow != 24*time.Hour || tn.AvatarMaxBytes != 5<<20 ||
		tn.EmailImageUploadMaxBytes != 10<<20 || tn.EmailImageMaxBytes != 300<<10 || tn.EmailImageMaxWidth != 1200 {
		t.Fatalf("defaults drifted from the env template: %+v", tn)
	}
	if cfg.Auth.SetupTokenTTL != 168*time.Hour || cfg.LabSession.TTL != 24*time.Hour || cfg.LabAccess.TokenMaxTTL != 5*time.Minute ||
		cfg.Exercise.TestDeployTTL != 2*time.Hour || cfg.Exercise.TestDeployTTLMax != 8*time.Hour {
		t.Fatalf("TTL defaults drifted: %v %v %v", cfg.Auth.SetupTokenTTL, cfg.LabSession.TTL, cfg.LabAccess.TokenMaxTTL)
	}
	t.Setenv("SSE_MAX_LIFETIME", "5m")
	t.Setenv("SETUP_TOKEN_TTL", "1h")
	t.Setenv("LAB_SESSION_TTL", "2h")
	cfg = MustGetConfig()
	if cfg.Tunables.SSEMaxLifetime != 5*time.Minute || cfg.Auth.SetupTokenTTL != time.Hour || cfg.LabSession.TTL != 2*time.Hour {
		t.Fatal("env overrides are not read")
	}
	bad := cfg.Tunables
	bad.AvatarMaxBytes = 0
	if bad.Validate() == nil {
		t.Fatal("a zero upload limit must be rejected")
	}
}
