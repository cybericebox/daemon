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
	}

	// ServerConfig is the configuration for the HTTP server
	ServerConfig struct {
		Host               string        `yaml:"host" env:"http-host" env-default:"0.0.0.0" env-description:"HTTP host"`
		Port               string        `yaml:"port" env:"http-port" env-default:"80" env-description:"HTTP port"`
		ReadTimeout        time.Duration `yaml:"readTimeout" env:"http-readTimeout" env-default:"10s" env-description:"HTTP readTimeout"`
		WriteTimeout       time.Duration `yaml:"writeTimeout" env:"http-writeTimeout" env-default:"10s" env-description:"HTTP writeTimeout"`
		MaxHeaderMegabytes int           `yaml:"maxHeaderBytes" env:"http-maxHeaderBytes" env-default:"1" env-description:"HTTP maxHeaderBytes"`
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

	return instance, true
}

func isValidConfig(cfg *Config) Valid {
	// TODO: Implement validation logic
	return true
}
