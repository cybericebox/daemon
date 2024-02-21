package config

import (
	"flag"
	"fmt"
	"github.com/ilyakaznacheev/cleanenv"
	"github.com/rs/zerolog/log"
	"time"
)

type (
	// Config is the configuration for the application

	Config struct {
		Environment string           `yaml:"environment" env:"environment" env-default:"production" env-description:"Runtime environment"`
		Controller  ControllerConfig `yaml:"controller"`
		Service     ServiceConfig    `yaml:"service"`
		Repository  RepositoryConfig `yaml:"repository"`
	}

	// ControllerConfig is the configuration for the controller layer
	ControllerConfig struct {
		HTTP HTTPConfig `yaml:"http"`
	}

	// ServiceConfig is the configuration for the service layer
	ServiceConfig struct {
	}

	// RepositoryConfig is the configuration for the repository layer
	RepositoryConfig struct {
	}

	// HTTPConfig is the configuration for the HTTP server
	HTTPConfig struct {
		Server ServerConfig `yaml:"server"`
		Proxy  ProxyConfig  `yaml:"proxy"`
		Auth   AuthConfig   `yaml:"auth"`
	}

	// ServerConfig is the configuration for the HTTP server
	ServerConfig struct {
		Host               string        `yaml:"host" env:"http-host" env-default:"0.0.0.0" env-description:"HTTP host"`
		Port               string        `yaml:"port" env:"http-port" env-default:"80" env-description:"HTTP port"`
		ReadTimeout        time.Duration `yaml:"readTimeout" env:"http-readTimeout" env-default:"10s" env-description:"HTTP readTimeout"`
		WriteTimeout       time.Duration `yaml:"writeTimeout" env:"http-writeTimeout" env-default:"10s" env-description:"HTTP writeTimeout"`
		MaxHeaderMegabytes int           `yaml:"maxHeaderBytes" env:"http-maxHeaderBytes" env-default:"1" env-description:"HTTP maxHeaderBytes"`
	}

	// ProxyConfig is the configuration for the HTTP proxy
	ProxyConfig struct {
	}

	AuthConfig struct {
		Domain            string `yaml:"domain" env:"auth-domain" env-description:"Root domain"`
		Secure            bool
		JWT               JWTConfig         `yaml:"jwt"`
		RecaptchaV3       RecaptchaV3Config `yaml:"recaptchaV3"`
		OAUTH             OAuthConfig       `yaml:"oauth"`
		TemporalCodeTTL   time.Duration     `yaml:"temporalCodeTTL" env:"auth-temporalCodeTTL" env-default:"24h" env-description:"Temporal code TTL"`
		TemporalCookieTTL time.Duration     `yaml:"temporalCookieTTL" env:"auth-temporalCookieTTL" env-default:"10m" env-description:"Temporal cookie TTL"`
	}

	JWTConfig struct {
		AccessTokenTTL  time.Duration `yaml:"accessTokenTTL" env:"jwt-accessTokenTTL" env-default:"15m" env-description:"JWT accessToken TTL"`
		RefreshTokenTTL time.Duration `yaml:"refreshTokenTTL" env:"jwt-refreshTokenTTL" env-default:"1h" env-description:"JWT refreshToken TTL"`
		TokenSignature  string        `yaml:"tokenSignature" env:"jwt-tokenSignature" env-description:"JWT token Signature"`
	}

	RecaptchaV3Config struct {
		SecretKey string  `yaml:"secretKey" env:"recaptcha-secret" env-description:"Recaptcha secret"`
		SiteKey   string  `yaml:"siteKey" env:"recaptcha-site" env-description:"Recaptcha site"`
		Score     float64 `yaml:"score" env:"recaptcha-score" env-default:"0.5" env-description:"Recaptcha score"`
	}

	OAuthConfig struct {
		Google GoogleOAuthConfig `yaml:"google"`
	}

	GoogleOAuthConfig struct {
		ClientID     string `yaml:"clientID" env:"google-clientID" env-description:"Google clientID"`
		ClientSecret string `yaml:"clientSecret" env:"google-clientSecret" env-description:"Google clientSecret"`
	}

	// Valid is a type that represents the validation status of a configuration
	Valid bool
)

// GetConfig reads the configuration from the environment or a file and validates it
func GetConfig() (*Config, Valid) {
	path := flag.String("config", defaultConfigPath, fmt.Sprintf("Path to config file. Default: %s", defaultConfigPath))

	flag.Parse()

	instance := &Config{}
	header := "Config variables:"
	help, _ := cleanenv.GetDescription(instance, &header)

	var err error

	if nil != path {
		log.Info().Msgf("Reading daemon configuration from file: %s", *path)
		err = cleanenv.ReadConfig(*path, instance)
	} else {
		err = cleanenv.ReadEnv(instance)
	}

	if nil != err {
		log.Error().Err(err).Msg("See the help bellow")
		fmt.Println(help)
		return nil, false
	}

	if !isValidConfig(instance) {
		log.Error().Err(err).Msg("See the help bellow")
		fmt.Println(help)
		return nil, false
	}

	if nil != processConfig(instance) {
		log.Error().Err(err).Msg("See the help bellow")
		fmt.Println(help)
		return nil, false
	}

	return instance, true
}

func processConfig(cfg *Config) error {
	cfg.Controller.HTTP.Auth.Secure = true
	if cfg.Environment == EnvDevelopment {
		cfg.Controller.HTTP.Auth.Secure = false
	}
	return nil
}

func isValidConfig(cfg *Config) Valid {
	// TODO: Implement validation logic
	if nil == cfg {
		return true
	}
	return true
}
