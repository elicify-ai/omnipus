// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package preview

import (
	"os/exec"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// confinement is the AD-7 file and network shape of one program start.
type confinement struct {
	// readModel, when set, is the calling turn's kernel policy: its read
	// model (and its secret denies) is kept, every write right and network
	// allowance is dropped (LibreOffice gets none of the caller's write
	// roots). The renderer and extract-text get no read model at all — only
	// their own folders (MV-6).
	readModel *sandbox.SandboxPolicy
	// readExec are folders the program may read and execute.
	readExec []string
	// readWrite are folders the program may write (LibreOffice: scratch).
	readWrite []string
	// socketDir is the macOS LibreOffice socket folder (bind + connect).
	socketDir string
}

// spawnHooks are the launcher Prepare/Start hooks for this platform and the
// unconfined reason ("" when the kernel sandbox applies).
type spawnHooks struct {
	prepare    func(*exec.Cmd) error
	start      func(*exec.Cmd) (func(), error)
	unconfined string
}
