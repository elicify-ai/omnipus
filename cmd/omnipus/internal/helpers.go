package internal

import (
	"os"
	"path/filepath"

	"github.com/dapicom-ai/omnipus/pkg"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

const Logo = pkg.Logo

// GetOmnipusHome returns the omnipus home directory.
// Priority: $OMNIPUS_HOME > ~/.omnipus
func GetOmnipusHome() string {
	if home := os.Getenv(config.EnvHome); home != "" {
		return home
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, pkg.DefaultOmnipusHome)
}

func GetConfigPath() string {
	if configPath := os.Getenv(config.EnvConfig); configPath != "" {
		return configPath
	}
	return filepath.Join(GetOmnipusHome(), "config.json")
}

func LoadConfig() (*config.Config, error) {
	cfg, err := config.LoadConfig(GetConfigPath())
	if err != nil {
		return nil, err
	}
	logger.SetLevelFromString(cfg.Gateway.LogLevel)
	return cfg, nil
}
