// environment_setup_wiring.go — ADR-090 environment_setup live wiring for the
// agent loop: the pkg/environmentsetup storage adapter plus the loop-level
// deps pass. The tool (pkg/tools/environment_setup.go) stays free of
// storage-package knowledge; production wiring supplies this adapter over
// storage's generic lifecycle (BeginInstall → Target → Commit/Abort), tests
// may supply a filesystem-backed fake against the tool's seam instead.
//
// ES-FR-02: Admin's cross-workspace setup authority is a wiring-time identity
// fact derived server-side from the registry's (normalized) agent ID — never
// a caller-supplied argument.

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// environmentSetupStore adapts pkg/environmentsetup's installation-area
// lifecycle to the environment_setup tool's EnvironmentSetupStore seam.
type environmentSetupStore struct{}

// BeginInstall forwards to storage's BeginInstall; the scope string arrives
// validated by the tool's schema enum ("workspace"/"shared") and storage
// rejects anything else, so the typed conversion cannot mask an invalid scope.
func (environmentSetupStore) BeginInstall(appDataRoot, workspaceRoot, scope string) (tools.EnvironmentSetupTarget, error) {
	target, err := environmentsetup.BeginInstall(appDataRoot, workspaceRoot, environmentsetup.Scope(scope))
	if err != nil {
		return nil, err
	}
	return environmentSetupTarget{inner: target}, nil
}

// environmentSetupTarget forwards one storage Target behind the tool's
// interface. Commit returns (generationID, publishedDir, error).
type environmentSetupTarget struct{ inner *environmentsetup.Target }

func (a environmentSetupTarget) Scope() string { return string(a.inner.Scope()) }

// Prefix is stable for the whole command run: shared scope allocates its
// final generation directory up front (no rename after the script saw it),
// so a script embedding absolute paths keeps working after publication.
func (a environmentSetupTarget) Prefix() string { return a.inner.Prefix() }

func (a environmentSetupTarget) Cache() string { return a.inner.Cache() }
func (a environmentSetupTarget) Tmp() string   { return a.inner.Tmp() }

// WritableRoots is the exact write-grant for the setup child: the storage
// contract is {Prefix, Cache, Tmp} — never the enclosing store or unrelated
// workspace files.
func (a environmentSetupTarget) WritableRoots() []string {
	return a.inner.Grant().Writable
}

func (a environmentSetupTarget) Commit() (string, string, error) {
	shared, err := a.inner.Commit()
	if err != nil {
		return "", "", err
	}
	return shared.Generation, shared.Dir, nil
}

func (a environmentSetupTarget) Abort() error { return a.inner.Abort() }

// wireEnvironmentSetupDepsOn replaces each agent's base environment_setup
// registration with the fully-deps'd instance: god mode (the same operator
// opt-out governing that agent's bash children), the kernel-sandbox egress
// proxy, and the production storage adapter. Mirrors wireExecToolDepsOn — it
// runs in the same registry pass, so hot-reload re-applies it on the rebuilt
// registry and WireTier13Deps's re-run carries the proxy.
//
// Registration is unconditional (ES-FR-01): the Ask policy — not registration
// — decides who may call it.
func (al *AgentLoop) wireEnvironmentSetupDepsOn(registry *AgentRegistry, cfg *config.Config) {
	if registry == nil || cfg == nil {
		return
	}
	godMode := GodModeActive(cfg)
	auditFailClosed := resolveBoolWithDefault(cfg.Sandbox.PathGuardAuditFailClosed, cfg.Sandbox.AuditLog)
	store := environmentSetupStore{}
	for _, agentID := range registry.ListAgentIDs() {
		agent, ok := registry.GetAgent(agentID)
		if !ok || agent == nil || agent.Tools == nil {
			continue
		}
		agent.Tools.RegisterReplacing(tools.NewEnvironmentSetupTool(tools.EnvironmentSetupToolDeps{
			Home:            config.OmnipusHomeDir(),
			AgentWorkDir:    agent.Home,
			Admin:           agentID == "admin",
			GodMode:         godMode,
			Proxy:           al.sandboxEgressProxy,
			Store:           store,
			AuditFailClosed: auditFailClosed,
		}))
	}
}
