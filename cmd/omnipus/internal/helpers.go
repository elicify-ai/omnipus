package internal

import (
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

const Logo = pkg.Logo

// GetOmnipusHome returns the omnipus home directory.
// Priority: $OMNIPUS_HOME > ~/.omnipus
//
// Delegates entirely to config.OmnipusHomeDir() — the single canonical
// home-directory resolver. Do not reimplement env/HOME resolution here: a
// prior independent reimplementation silently dropped os.UserHomeDir()
// errors (falling through to a bare relative path with no secure fallback)
// and never resolved a relative $OMNIPUS_HOME to an absolute path against
// CWD, unlike OmnipusHomeDir()'s safety nets.
func GetOmnipusHome() string {
	return config.OmnipusHomeDir()
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
