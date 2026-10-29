// Package agenthome is the single resolver for an agent's home (working)
// directory and the Omnipus data directory. pkg/agent uses it to create and
// root agents; pkg/clitrust uses it to build the agent-writable roots a
// third-party CLI program must stay out of (T11 spec §3.2, security review
// S3), so both always agree on where an agent can write.
//
// Moved verbatim from pkg/agent/instance.go (resolveAgentHome, omnipusHome,
// expandHome); pkg/agent keeps thin delegating wrappers.
package agenthome

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/routing"
)

// Resolve determines the workspace directory for an agent.
func Resolve(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) string {
	if agentCfg != nil && strings.TrimSpace(agentCfg.Home) != "" {
		return ExpandHome(strings.TrimSpace(agentCfg.Home))
	}
	// Use the configured default workspace (respects OMNIPUS_HOME) for the
	// unidentified agent. The agentCfg.Default flag means "this agent is the
	// routing default for inbound messages" — it does NOT mean "share the
	// workspace". A routing-default agent (e.g. Mia with Default=true) must
	// still get its own per-agent workspace (FUNC-11). Only a caller with no
	// agent identity at all falls back to the shared default workspace; the
	// "main" sentinel that used to land here has been removed.
	if agentCfg == nil || agentCfg.ID == "" {
		return ExpandHome(defaults.Home)
	}
	// Per-agent isolated workspace (FUNC-11). Each custom agent gets its own
	// directory under $OMNIPUS_HOME/agents/{id}/ (or ~/.omnipus/agents/{id}/ when
	// OMNIPUS_HOME is unset), matching the REST API convention.
	// H4: validate that the resolved path is actually under the agents base
	// directory after cleaning, to guard against path traversal via crafted agent IDs.
	safeBase := filepath.Join(OmnipusHome(), "agents")
	// Strip any path separators or ".." from the agent ID.
	sanitizedID := filepath.Base(filepath.Clean(agentCfg.ID))
	if sanitizedID == "." || sanitizedID == ".." || sanitizedID == "" {
		// The agent ID sanitized to an unusable value. Use a UUID-based
		// directory name so this degenerate case still gets a unique,
		// collision-free workspace. (routing.NormalizeAgentID used to return
		// the "main" sentinel for empty/dot inputs, which made a collision
		// with the sentinel's own workspace a real risk here; with the
		// sentinel retired, NormalizeAgentID("") returns empty string and
		// there is no reserved name left to collide with — the UUID fallback
		// remains simply for uniqueness.)
		fallbackID := "agent-" + uuid.New().String()
		logger.WarnCF("agent", "Suspicious agent ID after sanitization; using UUID fallback workspace",
			map[string]any{"original_id": agentCfg.ID, "sanitized": sanitizedID, "fallback_id": fallbackID})
		return filepath.Join(safeBase, fallbackID)
	}
	resolved := filepath.Join(safeBase, sanitizedID)
	if !strings.HasPrefix(filepath.Clean(resolved), safeBase) {
		logger.WarnCF("agent", "Agent workspace path escapes base directory; using fallback",
			map[string]any{"agent_id": agentCfg.ID, "resolved": resolved})
		return filepath.Join(
			ExpandHome(defaults.Home),
			"..",
			"workspace-"+routing.NormalizeAgentID(agentCfg.ID),
		)
	}
	return resolved
}

// OmnipusHome returns the Omnipus data directory.
// Priority: $OMNIPUS_HOME > ~/.omnipus.
// Falls back to /tmp/.omnipus when both os.UserHomeDir and OMNIPUS_HOME are unavailable.
func OmnipusHome() string {
	if h := os.Getenv("OMNIPUS_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/.omnipus"
	}
	return filepath.Join(home, ".omnipus")
}

func ExpandHome(path string) string {
	if path == "" {
		return path
	}
	if path[0] == '~' {
		// When OMNIPUS_HOME is set, expand ~ to the parent of OMNIPUS_HOME so
		// that paths like ~/.omnipus/sessions resolve under OMNIPUS_HOME.
		// When OMNIPUS_HOME is not set, use os.UserHomeDir() for byte-identical
		// behavior to the pre-OMNIPUS_HOME code.
		var home string
		if h := os.Getenv("OMNIPUS_HOME"); h != "" {
			// OMNIPUS_HOME is already the .omnipus directory; its parent is the
			// effective ~ for paths of the form ~/.omnipus/...
			home = filepath.Dir(h)
		} else {
			var err error
			home, err = os.UserHomeDir()
			if err != nil {
				logger.WarnCF("agent", "UserHomeDir failed in expandHome; attempting Getwd fallback",
					map[string]any{"path": path, "error": err.Error()})
				// M20: prefer an absolute path fallback — "." is relative and ambiguous.
				if wd, wdErr := os.Getwd(); wdErr == nil {
					home = wd
				} else {
					// Both UserHomeDir and Getwd failed; use /tmp as a last resort.
					logger.WarnCF("agent", "Getwd also failed in expandHome; using /tmp fallback",
						map[string]any{"path": path, "error": wdErr.Error()})
					home = os.TempDir()
				}
			}
		}
		if len(path) > 1 && (path[1] == '/' || path[1] == filepath.Separator) {
			return home + path[1:]
		}
		return home
	}
	return path
}
