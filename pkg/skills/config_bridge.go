package skills

import (
	"net/http"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// MarketplacesFromConfig converts the persisted []config.MarketplaceConfig
// (credential-ref form) into the runtime []MarketplaceConfig (resolved-token
// form) consumed by NewRegistryManagerFromConfig.
//
// resolve maps a credential ref (env-var name) to its resolved plaintext value
// (empty when unset) — wire it to credentials.Bundle.GetString (gateway) or
// os.Getenv (agent loop / CLI).
//
// ssrfClient, when non-nil, is attached to ClawHub entries' HTTPClient so
// outbound registry traffic honors the SSRF policy (SEC-24).
//
// githubWorkspace is injected into GitHub entries that do not carry their own
// workspace (the persisted shape has no workspace field; it is derived from the
// agent/operator workspace at runtime).
func MarketplacesFromConfig(
	cfg *config.Config,
	resolve func(string) string,
	ssrfClient *http.Client,
	githubWorkspace string,
) []MarketplaceConfig {
	if cfg == nil {
		return nil
	}
	src := cfg.Tools.Skills.Marketplaces
	out := make([]MarketplaceConfig, 0, len(src))
	for _, m := range src {
		entry := MarketplaceConfig{
			Name:            m.Name,
			Type:            m.Type,
			Enabled:         m.Enabled,
			BaseURL:         m.BaseURL,
			AuthToken:       resolve(m.AuthTokenRef),
			SearchPath:      m.SearchPath,
			SkillsPath:      m.SkillsPath,
			DownloadPath:    m.DownloadPath,
			Timeout:         m.Timeout,
			MaxZipSize:      m.MaxZipSize,
			MaxResponseSize: m.MaxResponseSize,
			Token:           resolve(m.TokenRef),
			Proxy:           m.Proxy,
		}
		if m.Type == "clawhub" && ssrfClient != nil {
			entry.HTTPClient = ssrfClient
		}
		if m.Type == "github" && entry.Workspace == "" {
			entry.Workspace = githubWorkspace
		}
		out = append(out, entry)
	}
	return out
}

// ClawHubMarketplaceFromConfig returns the runtime MarketplaceConfig for the
// ClawHub entry in cfg (located by Type=="clawhub" or Name=="clawhub"), with
// credential refs resolved and the SSRF client attached. Returns ok=false when
// no ClawHub entry is configured. Used by the gateway's REST search endpoint,
// which builds a single ClawHub registry directly (rather than the full fan-out
// manager).
func ClawHubMarketplaceFromConfig(
	cfg *config.Config,
	resolve func(string) string,
	ssrfClient *http.Client,
) (MarketplaceConfig, bool) {
	if cfg == nil {
		return MarketplaceConfig{}, false
	}
	for _, m := range cfg.Tools.Skills.Marketplaces {
		// Match the ClawHub entry: explicit Type=="clawhub", or an untyped
		// entry named "clawhub" (defensive against hand-edited configs).
		if m.Type != "clawhub" && !(m.Type == "" && m.Name == "clawhub") {
			continue
		}
		entry := MarketplaceConfig{
			Name:            m.Name,
			Type:            "clawhub",
			Enabled:         m.Enabled,
			BaseURL:         m.BaseURL,
			AuthToken:       resolve(m.AuthTokenRef),
			SearchPath:      m.SearchPath,
			SkillsPath:      m.SkillsPath,
			DownloadPath:    m.DownloadPath,
			Timeout:         m.Timeout,
			MaxZipSize:      m.MaxZipSize,
			MaxResponseSize: m.MaxResponseSize,
		}
		if ssrfClient != nil {
			entry.HTTPClient = ssrfClient
		}
		return entry, true
	}
	return MarketplaceConfig{}, false
}

// FirstGitHubMarketplaceCreds returns the resolved token and proxy for the
// first enabled github marketplace entry in cfg, or ("", "") when none is
// configured. Used to seed the shared SkillInstaller (which predates the
// per-marketplace list and serves github installs globally).
func FirstGitHubMarketplaceCreds(
	cfg *config.Config,
	resolve func(string) string,
) (token, proxy string) {
	if cfg == nil {
		return "", ""
	}
	for _, m := range cfg.Tools.Skills.Marketplaces {
		if m.Type != "github" {
			continue
		}
		return resolve(m.TokenRef), m.Proxy
	}
	return "", ""
}
