package openclaw

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/migrate/internal"
)

// OpenclawHomeEnvVar is the environment variable that overrides the source
// openclaw home directory when migrating from openclaw to omnipus.
// Default: ~/.openclaw
const OpenclawHomeEnvVar = "OPENCLAW_HOME"

var providerMapping = map[string]string{
	"anthropic":  "anthropic",
	"claude":     "anthropic",
	"openai":     "openai",
	"gpt":        "openai",
	"groq":       "groq",
	"ollama":     "ollama",
	"openrouter": "openrouter",
	"deepseek":   "deepseek",
	"together":   "together",
	"mistral":    "mistral",
	"fireworks":  "fireworks",
	"google":     "google",
	"gemini":     "google",
	"xai":        "xai",
	"grok":       "xai",
	"cerebras":   "cerebras",
	"sambanova":  "sambanova",
}

type OpenclawHandler struct {
	opts             Options
	sourceConfigFile string
	sourceWorkspace  string
}

type (
	Options   = internal.Options
	Action    = internal.Action
	Result    = internal.Result
	Operation = internal.Operation
)

func NewOpenclawHandler(opts Options) (Operation, error) {
	home, err := resolveSourceHome(opts.SourceHome)
	if err != nil {
		return nil, err
	}
	opts.SourceHome = home

	configFile, err := findSourceConfig(home)
	if err != nil {
		return nil, err
	}
	return &OpenclawHandler{
		opts:             opts,
		sourceWorkspace:  filepath.Join(opts.SourceHome, "workspace"),
		sourceConfigFile: configFile,
	}, nil
}

func (o *OpenclawHandler) GetSourceName() string {
	return "openclaw"
}

func (o *OpenclawHandler) GetSourceHome() (string, error) {
	return o.opts.SourceHome, nil
}

func (o *OpenclawHandler) GetSourceWorkspace() (string, error) {
	return o.sourceWorkspace, nil
}

func (o *OpenclawHandler) GetSourceConfigFile() (string, error) {
	return o.sourceConfigFile, nil
}

func (o *OpenclawHandler) GetMigrateableFiles() []string {
	return migrateableFiles
}

func (o *OpenclawHandler) GetMigrateableDirs() []string {
	return migrateableDirs
}

func (o *OpenclawHandler) ExecuteConfigMigration(srcConfigPath, dstConfigPath string) error {
	openclawCfg, err := LoadOpenClawConfig(srcConfigPath)
	if err != nil {
		return err
	}

	picoCfg, warnings, err := openclawCfg.ConvertToOmnipus(o.opts.SourceHome)
	if err != nil {
		return err
	}

	for _, w := range warnings {
		fmt.Printf("  Warning: %s\n", w)
	}

	incoming := picoCfg.ToStandardConfig()
	if err := os.MkdirAll(filepath.Dir(dstConfigPath), 0o755); err != nil {
		return err
	}

	// ADR-054: agents are no longer entities inside config.json —
	// AgentsConfig.List carries json:"-" precisely so config.SaveConfig
	// below can never write incoming.Agents.List into config.json. That
	// means it is the ENTITY STORE, not config.json, that must durably
	// carry every migrated agent — do this BEFORE SaveConfig so a
	// persistence failure here aborts the whole migration loudly instead
	// of reporting success while every migrated agent silently
	// evaporates on the very next config load (the bug this fixes: with
	// no entity records written, a legacy agents.list blob previously
	// landed in config.json where stripLegacyAgentsList would drop it on
	// next load, "migrating" every agent straight into the void).
	if err := persistMigratedAgents(dstConfigPath, incoming.Agents.List); err != nil {
		return fmt.Errorf("persist migrated agents to entity store: %w", err)
	}

	return config.SaveConfig(dstConfigPath, incoming)
}

// persistMigratedAgents writes each migrated agent as its own per-entity
// record via pkg/agentstore, rooted at $OMNIPUS_HOME (= filepath.Dir(dstConfigPath)
// — the same derivation the real boot path uses, see
// pkg/gateway.populateAgentsListFromEntityStoreStrict), so the
// migrated roster is readable by the agent registry on first boot exactly
// like any natively-created agent.
//
// Fails fast on the first error: a partially-migrated roster must never be
// reported as a successful migration. entity.Store.Create already
// write-then-verifies durability internally (ADR-054 D6 corollary), so a nil
// error from Store.Create means the record is confirmed on disk.
//
// Re-running the migration (the config-conversion step has no --force gate
// of its own — config.SaveConfig below always unconditionally overwrites
// dstConfigPath) must not fail just because a previous run already created
// the same agent IDs: an ErrAlreadyExists falls back to Update, overwriting
// the existing record's fields with the freshly re-converted ones while
// preserving its original ID/CreatedAt, mirroring the "always clobber on
// re-migrate" semantics config.json itself already has.
func persistMigratedAgents(dstConfigPath string, agents []config.AgentConfig) error {
	if len(agents) == 0 {
		return nil
	}
	store := agentstore.New(filepath.Dir(dstConfigPath))
	for i := range agents {
		ac := agents[i]
		if ac.ID == "" {
			continue
		}
		if err := store.Create(ac.ID, &ac); err != nil {
			if errors.Is(err, entity.ErrAlreadyExists) {
				if _, updateErr := store.Update(ac.ID, func(existing *config.AgentConfig) error {
					createdAt := existing.CreatedAt
					id := existing.ID
					*existing = ac
					existing.ID = id
					existing.CreatedAt = createdAt
					return nil
				}); updateErr != nil {
					return fmt.Errorf("update existing agent entity %q: %w", ac.ID, updateErr)
				}
				continue
			}
			return fmt.Errorf("create agent entity %q: %w", ac.ID, err)
		}
	}
	return nil
}

func resolveSourceHome(override string) (string, error) {
	if override != "" {
		return internal.ExpandHome(override), nil
	}
	if envHome := os.Getenv(OpenclawHomeEnvVar); envHome != "" {
		return internal.ExpandHome(envHome), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".openclaw"), nil
}

func findSourceConfig(sourceHome string) (string, error) {
	candidates := []string{
		filepath.Join(sourceHome, "openclaw.json"),
		filepath.Join(sourceHome, "config.json"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no config file found in %s (tried openclaw.json, config.json)", sourceHome)
}

func rewriteWorkspacePath(path string) string {
	path = strings.Replace(path, ".openclaw", ".omnipus", 1)
	return path
}

func mapProvider(provider string) string {
	if mapped, ok := providerMapping[strings.ToLower(provider)]; ok {
		return mapped
	}
	return strings.ToLower(provider)
}
