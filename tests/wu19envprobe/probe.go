// Package wu19envprobe is test-only process-edge support for WU-19 receipts.
// Arbitrary parent values are never serialized: only key names and explicitly
// seeded fixture/control values are recorded.
package wu19envprobe

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Snapshot is a private test-process receipt, not a gateway/SPA wire format.
// not-wire-format
type Snapshot struct {
	PID    int               `json:"pid"`
	Keys   []string          `json:"keys"`
	Values map[string]string `json:"values"`
}

func ValueKeys() []string {
	return append(DeniedKeys(), []string{
		"HOME", "PATH", "USER", "OMNIPUS_CHILD_WU19_CONTROL", "WU19_SITE_OPTION",
		"OMNIPUS_HOME", "OMNIPUS_MASTER_KEY", "OMNIPUS_KEY_FILE", "OMNIPUS_BEARER_TOKEN",
		"HOST", "PORT", "HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "NO_PROXY", "no_proxy", "npm_config_cache",
		"OMNIPUS_ENV_PREFIX", "OMNIPUS_ENV_CACHE", "OMNIPUS_ENV_TMP", "TMPDIR", "PIP_CACHE_DIR",
	}...)
}

func DeniedKeys() []string {
	return []string{"OPENROUTER_API_KEY", "EMAIL_PASSWORD", "WU19_FUTURE_GATEWAY_VALUE", "OPENAI_API_KEY", "CODEX_API_KEY"}
}

func Capture() Snapshot {
	s := Snapshot{PID: os.Getpid(), Keys: []string{}, Values: map[string]string{}}
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			s.Keys = append(s.Keys, key)
		}
	}
	for _, key := range ValueKeys() {
		if value, ok := os.LookupEnv(key); ok {
			s.Values[key] = value
		}
	}
	return s
}

func Save(path string) error {
	data, err := json.Marshal(Capture())
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func Read(path string) (Snapshot, error) {
	var s Snapshot
	data, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, err
	}
	if s.PID == 0 || s.Values == nil {
		return s, fmt.Errorf("child receipt missing process/control data")
	}
	return s, nil
}

// GenericAllowedKey is an independent oracle from the #155 child contract
// documented in childenv.go and required by FR-239. Do not call the production
// predicate: a widening mutant in it must not widen this oracle with it.
func GenericAllowedKey(key string) bool {
	switch key {
	case "PATH", "HOME", "USER", "LOGNAME", "SHELL", "TZ", "LANG", "TMPDIR", "TERM",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy":
		return true
	}
	return strings.HasPrefix(key, "LC_") || strings.HasPrefix(key, "XDG_") || strings.HasPrefix(key, "OMNIPUS_CHILD_")
}
