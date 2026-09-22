// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package environmentsetup

import "time"

// Scope selects the installation destination of one environment_setup call.
// Values are the shared vocabulary between the tool schema and this package.
type Scope string

const (
	// ScopeWorkspace installs inside the authorized workspace, under the
	// reserved <workspace>/.omnipus/env subtree. Default scope.
	ScopeWorkspace Scope = "workspace"
	// ScopeShared installs into the application-managed shared store that
	// only the application publish path writes to; agents get read/execute
	// only. Must be explicit in the approved call.
	ScopeShared Scope = "shared"
)

// Environment variables the tool layer sets for the setup child so the
// agent's command can reference the actual destinations (ES-FR-02).
const (
	// EnvVarPrefix is the installation prefix for this operation: the
	// workspace prefix (workspace scope) or the pre-allocated generation
	// directory (shared scope).
	EnvVarPrefix = "OMNIPUS_ENV_PREFIX"
	// EnvVarCache is the operation's writable cache directory.
	EnvVarCache = "OMNIPUS_ENV_CACHE"
	// EnvVarTmp is the operation's writable temp directory.
	EnvVarTmp = "OMNIPUS_ENV_TMP"
)

// Paths is the sandbox-relevant shape of one installation area: which subtree
// is the root, which dirs agents may read/execute, and which dirs are
// writable. It is descriptive — the tool/runtime lanes derive their grant
// rules from it.
type Paths struct {
	Root     string
	ReadExec []string
	Writable []string
}

// SharedGeneration is one published shared installation. Dir is its final,
// stable path: it is allocated before the installation command ran and is
// never renamed or moved afterwards, so running tasks can keep using paths
// they resolved earlier.
type SharedGeneration struct {
	ID          string // directory name under the shared store
	Dir         string // absolute final path (read/execute only for agents)
	Digest      string // sha256 hex of the published tree (marker excluded)
	PublishedAt time.Time
	Platform    string // "goos/goarch" the installation ran on
}

// SharedResult is what Commit returns to the tool layer.
type SharedResult struct {
	Generation string // generation ID (also the directory name)
	Dir        string // published generation directory
}

// RuntimeEnv is the per-turn runtime view of installed environments for one
// workspace. It has no agent or role inputs — the caller's policy layer
// decides visibility; this reports the actual paths.
type RuntimeEnv struct {
	// WorkspacePrefix is the workspace's reserved env subtree, "" when the
	// workspace has none yet.
	WorkspacePrefix string
	// WorkspaceCache and WorkspaceTmp are the per-turn writable dirs inside
	// the workspace prefix, "" alongside an empty WorkspacePrefix.
	WorkspaceCache string
	WorkspaceTmp   string
	// Shared lists published shared generations in PATH precedence order:
	// newest publication first, older ones after. Additive — later
	// publications never hide earlier ones.
	Shared []SharedGeneration
	// ReadExec lists dirs to grant read+execute (existing prefixes, PATH
	// order).
	ReadExec []string
	// BinDirs lists existing conventional executable directories in PATH
	// order (newest prefix first): <prefix>/bin on Unix; on Windows both
	// <prefix>/Scripts (venv convention) and <prefix>/bin (generic), in that
	// order, only real directories — never a symlinked bin.
	BinDirs []string
	// Writable lists the per-turn writable dirs (workspace cache/tmp; empty
	// without a workspace env). The shared store is never here.
	Writable []string
	// Broken lists published generation IDs whose marker is corrupt or
	// mismatched. They are excluded from ReadExec/BinDirs but reported
	// truthfully instead of silently ignored.
	Broken []string
}
