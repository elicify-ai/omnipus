// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build darwin || linux

package preview

import "github.com/elicify-ai/omnipus/pkg/sandbox"

// policy renders the confinement as a kernel policy (macOS Seatbelt and
// Linux Landlock; Windows uses the Job Object only, AD-7).
func (c confinement) policy() sandbox.SandboxPolicy {
	var p sandbox.SandboxPolicy
	if c.readModel != nil {
		p.ReadsOpen = c.readModel.ReadsOpen
		p.ExecOpen = c.readModel.ExecOpen
		p.DeniedPaths = append(p.DeniedPaths, c.readModel.DeniedPaths...)
		p.DeniedNodes = append(p.DeniedNodes, c.readModel.DeniedNodes...)
		p.DeniedPathPrefixes = append(p.DeniedPathPrefixes, c.readModel.DeniedPathPrefixes...)
		for _, r := range c.readModel.FilesystemRules {
			if access := r.Access &^ sandbox.AccessWrite; access != 0 {
				p.FilesystemRules = append(p.FilesystemRules, sandbox.PathRule{Path: r.Path, Access: access})
			}
		}
	}
	for _, d := range c.readExec {
		p.FilesystemRules = append(p.FilesystemRules, sandbox.PathRule{Path: d, Access: sandbox.AccessRead | sandbox.AccessExecute})
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
