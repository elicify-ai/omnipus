// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build darwin || linux

package preview

import "github.com/elicify-ai/omnipus/pkg/sandbox"

// policy renders the confinement as a kernel policy (macOS Seatbelt and
// Linux Landlock; Windows uses the Job Object only, AD-7).
func (c confinement) policy() sandbox.SandboxPolicy {
	// ReadsOpen and ExecOpen stay false and no deny list is set: the rules
	// below are everything the program may touch (spec §3.4 D8c).
	var p sandbox.SandboxPolicy
	for _, d := range c.readExec {
		p.FilesystemRules = append(p.FilesystemRules, sandbox.PathRule{Path: d, Access: sandbox.AccessRead | sandbox.AccessExecute})
	}
	for _, d := range c.readOnly {
		p.FilesystemRules = append(p.FilesystemRules, sandbox.PathRule{Path: d, Access: sandbox.AccessRead})
	}
	for _, d := range c.readWrite {
		p.FilesystemRules = append(p.FilesystemRules, sandbox.PathRule{Path: d, Access: sandbox.AccessRead | sandbox.AccessWrite})
	}
	if c.socketDir != "" {
		p.FilesystemRules = append(p.FilesystemRules, sandbox.PathRule{Path: c.socketDir, Access: sandbox.AccessRead | sandbox.AccessWrite})
		p.UnixSocketRules = append(p.UnixSocketRules, sandbox.UnixSocketRule{Path: c.socketDir, Bind: true, Connect: true})
	}
	// Network: Seatbelt is default-deny (only the system preamble's resolver
	// socket); Linux adds its TCP deny in hooksFor.
	p.InheritToChildren = true
	return p
}
