package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// scrubSearXNGConfig removes the unsupported search provider from an existing
// config.json during an ordinary load. Raw JSON objects retain every unrelated
// field, including fields this version of the typed config does not know.
// A failed write is returned to the loader so the operator sees a failed load
// instead of running with a silently unpersisted repair.
func scrubSearXNGConfig(cfg *Config, path string, data []byte, onSelfHeal SelfHealWriteHook) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("read config for SearXNG removal: %w", err)
	}
	var tools map[string]json.RawMessage
	if len(root["tools"]) == 0 || bytes.Equal(bytes.TrimSpace(root["tools"]), []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(root["tools"], &tools); err != nil {
		return fmt.Errorf("read tools for SearXNG removal: %w", err)
	}
	if len(tools["web"]) == 0 || bytes.Equal(bytes.TrimSpace(tools["web"]), []byte("null")) {
		return nil
	}
	var web map[string]json.RawMessage
	if err := json.Unmarshal(tools["web"], &web); err != nil {
		return fmt.Errorf("read tools.web for SearXNG removal: %w", err)
	}

	_, hadSection := web["searxng"]
	delete(web, "searxng")
	defaultID, fallbackID := "", ""
	if raw, ok := web["default_provider"]; ok {
		if err := json.Unmarshal(raw, &defaultID); err != nil {
			return fmt.Errorf("read tools.web.default_provider for SearXNG removal: %w", err)
		}
	}
	if raw, ok := web["fallback_provider"]; ok {
		if err := json.Unmarshal(raw, &fallbackID); err != nil {
			return fmt.Errorf("read tools.web.fallback_provider for SearXNG removal: %w", err)
		}
	}

	var cleared []string
	converted := false
	if defaultID == "searxng" {
		// The saved file's state, not the defaults overlaid in cfg, decides
		// whether the ordinary post-injection roles pass must still run.
		var savedWeb map[string]any
		if err := json.Unmarshal(tools["web"], &savedWeb); err != nil {
			return fmt.Errorf("read saved web-search roles for SearXNG removal: %w", err)
		}
		converted = webRolesAlreadyDecided(savedWeb)
		web["default_provider"] = json.RawMessage(`""`)
		cfg.Tools.Web.DefaultProvider = ""
		cleared = append(cleared, "default_provider")
	}
	if fallbackID == "searxng" || (converted && defaultID == "searxng") {
		web["fallback_provider"] = json.RawMessage(`"none"`)
		cfg.Tools.Web.FallbackProvider = SearchProviderNone
		if fallbackID != SearchProviderNone {
			cleared = append(cleared, "fallback_provider")
		}
	}
	if !hadSection && len(cleared) == 0 {
		return nil
	}

	var err error
	if tools["web"], err = json.Marshal(web); err != nil {
		return fmt.Errorf("encode tools.web without SearXNG: %w", err)
	}
	if root["tools"], err = json.Marshal(tools); err != nil {
		return fmt.Errorf("encode tools without SearXNG: %w", err)
	}
	written, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config without SearXNG: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path, written, 0o600); err != nil {
		return fmt.Errorf("persist SearXNG removal in config.json: %w", err)
	}
	if onSelfHeal != nil {
		onSelfHeal(written)
	}
	message := "removed unsupported SearXNG search provider from config.json"
	if len(cleared) != 0 {
		message += "; cleared " + strings.Join(cleared, ", ") + " because SearXNG is no longer supported"
	}
	logger.WarnF(message, map[string]any{"path": path})
	return nil
}
