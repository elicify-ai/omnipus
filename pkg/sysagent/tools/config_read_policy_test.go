// Omnipus — System Agent Tool Tests: get_config disclosure policy
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// readConfig drives the real get_config tool and returns the decoded result.
func readConfig(t *testing.T, deps *systools.Deps, key string) (map[string]any, bool) {
	t.Helper()
	result := systools.NewConfigGetTool(deps).Execute(context.Background(), map[string]any{"key": key})
	var m map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &m); err != nil {
		t.Fatalf("get_config(%q) result is not JSON: %v\nbody: %s", key, err, result.ForLLM)
	}
	return m, result.IsError
}

// seedSecrets fills the config with the material an attacker was after, so a
// leak is visible as real content rather than an absent field. Every value here
// is fake.
func seedSecrets(cfg *config.Config) {
	cfg.Gateway.DevModeBypass = true
	cfg.Gateway.TrustXFF = true
	cfg.Gateway.Token = "raw-bearer-token-fake"
	cfg.Gateway.Users = []config.UserConfig{{
		Username:         "admin",
		PasswordHash:     "$2a$10$fakefakefakefakefakefakefakefakefakefakefakefakefakefa",
		SessionTokenHash: config.BcryptHash("$2a$10$fakesessionhashfakesessionhashfakesessionhashfakeses"),
	}}
}

// TestConfigGet_UndisclosableKeysRefused pins the read boundary, which is
// CREDENTIALS ONLY.
//
// OPERATOR DECISION, 2026-08-12. An earlier version of this test asserted that
// the whole enforcement configuration was unreadable — sandbox, tool_policies,
// the exec allow-list, the path fences. That was the wrong boundary and it
// fought the architecture: config.json is blocked at the FILESYSTEM layer
// precisely so that every read and write goes through this tool, which
// validates. Granting an agent the config tool and then refusing to let it read
// configuration makes the tool half-useless, and it removes the one way an agent
// can explain WHY it cannot do something instead of failing opaquely.
//
// What stays refused is the material that is a credential rather than a
// setting: account password and session hashes, the gateway bearer token, and
// the CLI token. Those live in config.json by accident of storage, not because
// they are configuration — the same reason credentials.json is blocked outright.
//
// Writes are unaffected: every key here and every enforcement key remains
// write-blocked. Reading the cage is diagnosis; opening it is escape.
func TestConfigGet_UndisclosableKeysRefused(t *testing.T) {
	for _, key := range []string{
		"gateway.users",
		"GATEWAY.USERS", // the same case bypass, on the read side
		"gateway.token",
		"gateway.cli_token",
		"Gateway.CLI_Token", // case variant of a credential
	} {
		t.Run(key, func(t *testing.T) {
			deps, cfg := newTestDeps()
			seedSecrets(cfg)

			m, isErr := readConfig(t, deps, key)
			if !isErr {
				t.Fatalf("get_config(%q) succeeded — it disclosed %v", key, m["value"])
			}
			errBlock, _ := m["error"].(map[string]any)
			if code, _ := errBlock["code"].(string); code != "FORBIDDEN" {
				t.Errorf("get_config(%q): code = %v, want FORBIDDEN", key, errBlock["code"])
			}
		})
	}
}

// TestConfigGet_SectionReadIsRedacted proves the ancestor case. Refusing
// "gateway.users" is worth nothing if "gateway" returns the same array, so a
// section read is served with every undisclosable descendant replaced by
// [REDACTED] — and still returns the ordinary fields, which is what keeps the
// tool useful.
func TestConfigGet_SectionReadIsRedacted(t *testing.T) {
	deps, cfg := newTestDeps()
	seedSecrets(cfg)

	m, isErr := readConfig(t, deps, "gateway")
	if isErr {
		t.Fatalf("get_config(gateway) was refused outright; want a redacted read: %v", m)
	}
	raw, err := json.Marshal(m["value"])
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	body := string(raw)

	for _, leak := range []string{
		"$2a$10$fakefakefake",    // password hash
		"$2a$10$fakesessionhash", // session token hash
		"raw-bearer-token-fake",  // bearer token
		`"admin"`,                // account name, as a JSON string value
	} {
		if strings.Contains(body, leak) {
			t.Errorf("get_config(gateway) leaked %q\nbody: %s", leak, body)
		}
	}

	value, _ := m["value"].(map[string]any)
	if value["users"] != "[REDACTED]" {
		t.Errorf("gateway.users = %v, want \"[REDACTED]\"", value["users"])
	}
	// OPERATOR DECISION 2026-08-12: enforcement configuration is READABLE by an
	// agent the operator granted the config tool — that is what the tool is for,
	// and it is how an agent explains why it cannot do something instead of
	// failing opaquely. Only credentials stay hidden. So dev_mode_bypass now
	// comes back with its real value while users/token/cli_token do not.
	if value["dev_mode_bypass"] == "[REDACTED]" {
		t.Error("gateway.dev_mode_bypass must be readable — it is enforcement configuration, not a credential")
	}
	// Positive half of the same read: the section is still worth reading.
	if value["port"] == nil {
		t.Errorf("gateway.port disappeared from the section read: %s", body)
	}
}

// TestConfigGet_LegitimateKeysStillReadable is the positive control. Without
// it, everything above would pass against a build where get_config refuses
// every key — which would break the agent workflows that depend on it rather
// than secure them.
func TestConfigGet_LegitimateKeysStillReadable(t *testing.T) {
	t.Run("gateway.port", func(t *testing.T) {
		deps, cfg := newTestDeps()
		cfg.Gateway.Port = 5123
		m, isErr := readConfig(t, deps, "gateway.port")
		if isErr {
			t.Fatalf("get_config(gateway.port) refused: %v", m)
		}
		if m["value"] != float64(5123) {
			t.Errorf("value = %v, want 5123 (payload: %v)", m["value"], m)
		}
	})

	t.Run("gateway.public_url is a deliberate read carve-out", func(t *testing.T) {
		deps, cfg := newTestDeps()
		cfg.Gateway.PublicURL = "https://omnipus.example"
		m, isErr := readConfig(t, deps, "gateway.public_url")
		if isErr {
			t.Fatalf("get_config(gateway.public_url) refused — agents build preview links from "+
				"it (ADR-044): %v", m)
		}
		if m["value"] != "https://omnipus.example" {
			t.Errorf("value = %v, want the canonical origin", m["value"])
		}
	})

	t.Run("tools.skills.marketplaces is a deliberate read carve-out", func(t *testing.T) {
		deps, _ := newTestDeps()
		m, isErr := readConfig(t, deps, "tools.skills.marketplaces")
		if isErr {
			t.Fatalf("get_config(tools.skills.marketplaces) refused — install_skill REQUIRES a "+
				"registry name, so an agent that cannot see the list cannot install: %v", m)
		}
		body, _ := json.Marshal(m)
		if !strings.Contains(string(body), "clawhub") {
			t.Errorf("the registry list came back without its names: %s", body)
		}
	})

	t.Run("agents.defaults.default_model.model", func(t *testing.T) {
		deps, cfg := newTestDeps()
		cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "glm-4.7"}
		m, isErr := readConfig(t, deps, "agents.defaults.default_model.model")
		if isErr {
			t.Fatalf("get_config(agents.defaults.default_model.model) refused: %v", m)
		}
		if m["value"] != "glm-4.7" {
			t.Errorf("value = %v, want glm-4.7", m["value"])
		}
	})
}
