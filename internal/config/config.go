package config

import (
	"flag"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/ilyakaznacheev/cleanenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"time"
)

type (
	Config struct {
		Debug      bool             `yaml:"debug" env:"DAEMON_DEBUG" env-default:"false" env-description:"Debug mode"`
		Domain     string           `yaml:"domain" env:"DOMAIN" env-description:"Domain of the platform"`
		Controller ControllerConfig `yaml:"controller"`
		Service    ServiceConfig    `yaml:"service"`
		Repository RepositoryConfig `yaml:"repository"`
	}

	ControllerConfig struct {
		HTTP HTTPConfig `yaml:"http"`
	}

	HTTPConfig struct {
		Server     ServerConfig     `yaml:"server"`
		Proxy      ProxyConfig      `yaml:"proxy"`
		Protection ProtectionConfig `yaml:"protection"`
	}

	ServerConfig struct {
		Host               string        `yaml:"host" env:"HTTP_HOST" env-default:"0.0.0.0" env-description:"HTTP host"`
		TLS                HTTPTLSConfig `yaml:"tls"`
		Port               string        `yaml:"port" env:"HTTP_PORT" env-default:"80" env-description:"HTTP port"`
		SecurePort         string        `yaml:"securePort" env:"HTTP_SECURE_PORT" env-default:"443" env-description:"HTTP secure port"`
		ReadTimeout        time.Duration `yaml:"readTimeout" env-default:"10s" env-description:"HTTP readTimeout"`
		WriteTimeout       time.Duration `yaml:"writeTimeout" env-default:"10s" env-description:"HTTP writeTimeout"`
		MaxHeaderMegabytes int           `yaml:"maxHeaderMegabytes" env-default:"1" env-description:"HTTP maxHeaderBytes"`
	}

	HTTPTLSConfig struct {
		Enabled  bool   `yaml:"enabled" env:"TLS_ENABLED" env-default:"false" env-description:"TLS enabled"`
		CertFile string `yaml:"certFile" env:"TLS_CERT_FILE" env-description:"Path to TLS cert"`
		KeyFile  string `yaml:"keyFile" env:"TLS_KEY_FILE" env-description:"Path to TLS key"`
	}

	// ProxyConfig is the configuration for the HTTP proxy for another services
	ProxyConfig struct {
		MainFrontend  string `yaml:"mainFrontend" env:"PROXY_MAIN_FRONTEND" env-default:"http://main-frontend-service:3000" env-description:"Main frontend proxy"`
		AdminFrontend string `yaml:"adminFrontend" env:"PROXY_ADMIN_FRONTEND" env-default:"http://admin-frontend-service:3000" env-description:"Admin frontend proxy"`
		EventFrontend string `yaml:"eventFrontend" env:"PROXY_EVENT_FRONTEND" env-default:"http://event-frontend-service:3000" env-description:"Event frontend proxy"`
	}

	// ProtectionConfig is the configuration for the protection layer
	ProtectionConfig struct {
		Recaptcha         RecaptchaConfig `yaml:"recaptcha"`
		JWT               JWTConfig       // link to the jwt in service configs
		TemporalCodeTTL   time.Duration   // link to the temporal code TTL in service configs
		TemporalCookieTTL time.Duration   `yaml:"temporalCookieTTL" env:"TEMPORAL_COOKIE_TTL" env-default:"1h" env-description:"Temporal cookie TTL"`
	}

	RecaptchaConfig struct {
		SecretKey string  `yaml:"secretKey" env:"RECAPTCHA_SECRET" env-description:"Recaptcha secret"`
		SiteKey   string  `yaml:"siteKey" env:"RECAPTCHA_KEY" env-description:"Recaptcha key"`
		ProjectID string  `yaml:"projectID" env:"RECAPTCHA_PROJECT" env-description:"Recaptcha project ID"`
		APIKey    string  `yaml:"apiKey" env:"RECAPTCHA_API_KEY" env-description:"Recaptcha API key"`
		Score     float32 `yaml:"score" env:"RECAPTCHA_SCORE" env-default:"0.5" env-description:"Recaptcha score"`
	}

	ServiceConfig struct {
		JWT          JWTConfig          `yaml:"jwt"`
		OAuth        OAuthConfig        `yaml:"oauth"`
		Password     PasswordConfig     `yaml:"password"`
		Storage      StorageConfig      `yaml:"storage"`
		TemporalCode TemporalCodeConfig `yaml:"temporalCode"`
		MaxWorkers   int                `yaml:"maxWorkers" env:"DAEMON_MAX_WORKERS" env-default:"5" env-description:"Max workers for the worker pool"`
	}

	StorageConfig struct {
		DownloadExpiration time.Duration `yaml:"download_expiration" env:"STORAGE_DOWNLOAD_EXPIRATION" env-default:"1m"`
		UploadExpiration   time.Duration `yaml:"upload_expiration" env:"STORAGE_UPLOAD_EXPIRATION" env-default:"1m"`
		BucketName         string        `yaml:"bucket" env:"STORAGE_BUCKET" env-default:""`
	}

	JWTConfig struct {
		AccessTokenTTL  time.Duration `yaml:"accessTokenTTL" env:"JWT_ACCESS_TOKEN_TTL" env-default:"15m" env-description:"JWT accessToken TTL"`
		RefreshTokenTTL time.Duration `yaml:"refreshTokenTTL" env:"JWT_REFRESH_TOKEN_TTL" env-default:"1h" env-description:"JWT refreshToken TTL"`
		TokenSignature  string        `yaml:"tokenSignature" env:"JWT_TOKEN_SIGNATURE" env-description:"JWT token Signature"`
	}

	PasswordConfig struct {
		HashCost           int                      `yaml:"hashCost" env:"PASSWORD_HASH_COST" env-default:"10" env-description:"Password hash cost"`
		PasswordComplexity PasswordComplexityConfig `yaml:"passwordComplexity"`
	}

	PasswordComplexityConfig struct {
		MinLength            int `yaml:"minLength" env:"PASSWORD_MIN_LENGTH" env-default:"8" env-description:"Password min length"`
		MaxLength            int `yaml:"maxLength" env:"PASSWORD_MAX_LENGTH" env-default:"64" env-description:"Password max length"`
		MinCapitalLetters    int `yaml:"minCapitalLetters" env:"PASSWORD_MIN_CAPITAL_LETTERS" env-default:"1" env-description:"Password min capital letters"`
		MinSmallLetters      int `yaml:"minSmallLetters" env:"PASSWORD_MIN_SMALL_LETTERS" env-default:"1" env-description:"Password min small letters"`
		MinDigits            int `yaml:"minDigits" env:"PASSWORD_MIN_DIGITS" env-default:"1" env-description:"Password min digits"`
		MinSpecialCharacters int `yaml:"minSpecialCharacters" env:"PASSWORD_MIN_SPECIAL_CHARACTERS" env-default:"1" env-description:"Password min special characters"`
	}

	TemporalCodeConfig struct {
		TTL time.Duration `yaml:"ttl" env:"TEMPORAL_CODE_TTL" env-default:"24h" env-description:"Temporal code TTL"`
	}

	OAuthConfig struct {
		Google              GoogleProviderConfig `yaml:"google"`
		RedirectURLTemplate string
	}

	GoogleProviderConfig struct {
		ClientID     string `yaml:"clientID" env:"GOOGLE_CLIENT_ID" env-description:"OAuth client ID"`
		ClientSecret string `yaml:"clientSecret" env:"GOOGLE_SECRET" env-description:"Google OAuth client secret"`
	}

	RepositoryConfig struct {
		Postgres PostgresConfig `yaml:"postgres"`
		//StorageS3 StorageS3Config `yaml:"storageS3"`
		Email EmailConfig     `yaml:"email"`
		VPN   VPNGRPCConfig   `yaml:"vpn"`
		Agent AgentGRPCConfig `yaml:"agent"`
	}

	// PostgresConfig is the configuration for the Postgres database
	PostgresConfig struct {
		Host     string `yaml:"host" env:"POSTGRES_HOSTNAME" env-description:"Host of Postgres"`
		Port     string `yaml:"port" env:"POSTGRES_PORT" env-default:"5432" env-description:"Port of Postgres"`
		Username string `yaml:"username" env:"POSTGRES_USER" env-description:"Username of Postgres"`
		Password string `yaml:"password" env:"POSTGRES_PASSWORD" env-description:"Password of Postgres"`
		Database string `yaml:"database" env:"POSTGRES_DB" env-description:"Database of Postgres"`
		SSLMode  string `yaml:"sslMode" env:"POSTGRES_SSL_MODE" env-default:"require" env-description:"SSL mode of Postgres"`
	}

	// StorageS3Config is the configuration for the S3 storage
	//StorageS3Config struct {
	//	Endpoint  string `yaml:"endpoint" env:"STORAGE_ENDPOINT" env-description:"Storage endpoint"`
	//	Region    string `yaml:"region" env:"STORAGE_REGION" env-description:"Storage region"`
	//	AccessKey string `yaml:"accessKey" env:"STORAGE_ACCESS_KEY" env-description:"Storage access key"`
	//	SecretKey string `yaml:"secretKey" env:"STORAGE_SECRET_KEY" env-description:"Storage secret key"`
	//	UseSSL    bool   `yaml:"useSSL" env:"STORAGE_USE_SSL" env-default:"true" env-description:"Storage use SSL"`
	//}

	EmailConfig struct {
		Host     string `yaml:"host" env:"EMAIL_HOST" env-description:"Host of email"`
		Port     int    `yaml:"port" env:"EMAIL_PORT" env-default:"587" env-description:"Port of email"`
		Username string `yaml:"username" env:"EMAIL_USERNAME" env-description:"Username of email"`
		Password string `yaml:"password" env:"EMAIL_PASSWORD" env-description:"Password of email"`

		SenderName   string `yaml:"senderName" env:"EMAIL_SENDER_NAME" env-description:"Sender name of email"`
		SenderEmail  string `yaml:"senderEmail" env:"EMAIL_SENDER_EMAIL" env-description:"Sender email of email"`
		ReplyToName  string `yaml:"replyToName" env:"EMAIL_REPLY_TO_NAME" env-description:"Reply to name of email"`
		ReplyToEmail string `yaml:"replyToEmail" env:"EMAIL_REPLY_TO_EMAIL" env-description:"Reply to email of email"`
	}

	VPNGRPCConfig struct {
		Endpoint string           `yaml:"endpoint" env:"WG_GRPC_ENDPOINT" env-description:"Endpoint for the VPN gRPC server" env-default:"wireguard-service:5454"`
		AuthKey  string           `yaml:"authKey" env:"WG_GRPC_AUTH_KEY" env-description:"Auth key for the VPN gRPC server"`
		SignKey  string           `yaml:"signKey" env:"WG_GRPC_SIGN_KEY" env-description:"Sign key for the VPN gRPC server"`
		TLS      VPNGRPCTLSConfig `yaml:"tls"`
	}

	VPNGRPCTLSConfig struct {
		Enabled  bool   `yaml:"enabled" env:"WG_GRPC_TLS_ENABLED" env-default:"false" env-description:"VPN gRPC TLS enabled"`
		CertFile string `yaml:"certFile" env:"WG_GRPC_TLS_CERT_FILE" env-description:"Path to VPN gRPC TLS cert"`
		KeyFile  string `yaml:"keyFile" env:"WG_GRPC_TLS_KEY_FILE" env-description:"Path to VPN gRPC TLS key"`
	}

	AgentGRPCConfig struct {
		Endpoint string             `yaml:"endpoint" env:"AGENT_GRPC_ENDPOINT" env-description:"Endpoint for the Agent gRPC server" env-default:"agent-service:5454"`
		AuthKey  string             `yaml:"authKey" env:"AGENT_GRPC_AUTH_KEY" env-description:"Auth key for the Agent gRPC server"`
		SignKey  string             `yaml:"signKey" env:"AGENT_GRPC_SIGN_KEY" env-description:"Sign key for the Agent gRPC server"`
		TLS      AgentGRPCTLSConfig `yaml:"tls"`
	}

	AgentGRPCTLSConfig struct {
		Enabled  bool   `yaml:"enabled" env:"AGENT_GRPC_TLS_ENABLED" env-default:"false" env-description:"Agent gRPC TLS enabled"`
		CertFile string `yaml:"certFile" env:"AGENT_GRPC_TLS_CERT_FILE" env-description:"Path to Agent gRPC TLS cert"`
		KeyFile  string `yaml:"keyFile" env:"AGENT_GRPC_TLS_KEY_FILE" env-description:"Path to Agent gRPC TLS key"`
	}
)

var PlatformDomain string

func MustGetConfig() *Config {
	path := flag.String("config", "", "Path to config file")

	log.Info().Msg("Reading daemon configuration")

	instance := &Config{}
	header := "Config variables:"
	help, _ := cleanenv.GetDescription(instance, &header)

	var err error

	if path != nil && *path != "" {
		err = cleanenv.ReadConfig(*path, instance)
	} else {
		err = cleanenv.ReadEnv(instance)
	}

	if err != nil {
		fmt.Println(help)
		log.Fatal().Err(err).Msg("Failed to read config")
		return nil
	}

	// set log mode
	if !instance.Debug {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
		gin.SetMode(gin.ReleaseMode)
	}

	// Set the domain
	PlatformDomain = instance.Domain

	return instance
}
