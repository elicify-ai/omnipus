// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// TurnPolicyInput is everything the kernel rendering needs that the authored
// per-turn policy does not already carry.
//
// It exists so DeriveKernelPolicy has ONE parameter that grows, rather than a
// signature that churns every time the kernel needs another fact. The fields
// here are deliberately the ones the app layer has no opinion about — ports and
// the filesystem model are kernel vocabulary, and putting them on
// fspolicy.FSPolicy would push kernel concepts into a package that is meant to
// stay a stdlib-only leaf.
type TurnPolicyInput struct {
	// HomePath is $OMNIPUS_HOME. Required: the secret set is anchored to it.
	HomePath string

	// Model selects the ADR-060 read/exec posture.
	Model FilesystemModel

	// AllowedPaths and AllowedExecPaths are the operator's config lists, passed
	// through to DefaultPolicyForModel unchanged.
	AllowedPaths     []string
	AllowedExecPaths []string

	// BindPorts is the dev-server range expanded by the caller.
	BindPorts []uint16

	// WarnFn receives one message per rule the policy computation strips or
	// skips. May be nil.
	WarnFn func(msg string, path string)
}

// DeriveKernelPolicy is THE single function that turns an authored per-turn
// policy into a kernel policy. ADR-061 D1 / spec FR-1.3.
//
// # Why there is exactly one of these
//
// Before this, the app layer and the kernel layer each computed their own
// answer to "what may this turn touch", from separate inputs, and they had
// drifted in both directions — the app layer denied paths the kernel granted
// and vice versa. A second construction site is how that happens, so there is
// one, and FR-1.4's totality test asserts that every authored field with a
// kernel expression actually reaches the output. A field silently ignored by
// one side is the exact failure mode this replaces.
//
// # What it carries across, and what it deliberately does not
//
//   - WorkDir becomes a write grant, and drives the secret set's own-tree
//     exception through fspolicy.DeniedPathsFor.
//   - AllowedRoots (mounts) become write grants. Reads need no grant under the
//     open model, so a mount is a write grant and nothing else — see ADR-061 D4.
//   - Scope is NOT carried across as a read restriction. Post-ADR-060 reads are
//     open, and Scope now governs writes only (spec FR-2.5). Rendering it as a
//     read restriction here would put the kernel back out of step with the app
//     layer in the one place this whole change exists to fix.
func DeriveKernelPolicy(authored fspolicy.FSPolicy, in TurnPolicyInput) SandboxPolicy {
	allowed := make([]string, 0, len(in.AllowedPaths)+len(authored.AllowedRoots)+1)
	allowed = append(allowed, in.AllowedPaths...)

	// The turn's work dir is a write grant. Under a re-rooted workspace turn
	// this is the workspace's work/ directory rather than the agent home, which
	// is precisely why the kernel policy has to be per-turn rather than
	// computed once at boot.
	if authored.WorkDir != "" {
		allowed = append(allowed, authored.WorkDir)
	}

	// Mounts. Deduplicated against WorkDir and each other so a mount nested
	// inside the work dir does not emit a redundant rule — harmless to
	// Landlock, which unions rights per path, but noise in a rendered Seatbelt
	// profile that a reader has to discount.
	seen := map[string]struct{}{}
	for _, p := range allowed {
		seen[filepath.Clean(p)] = struct{}{}
	}
	for _, root := range authored.AllowedRoots {
		clean := filepath.Clean(root)
		if clean == "" || clean == "." {
			continue
		}
		if _, dup := seen[clean]; dup {
			continue
		}
		seen[clean] = struct{}{}
		allowed = append(allowed, clean)
	}

	policy := DefaultPolicyForModel(
		in.Model, in.HomePath, allowed, in.AllowedExecPaths, in.WarnFn, in.BindPorts)

	// The per-turn secret set, which is where the own-tree exception lands.
	// DefaultPolicyForModel populates the BOOT set; replacing it here is what
	// makes agents/ and workspaces/ enforceable at the kernel layer at all,
	// since they can only be excluded once a work dir exists to compare
	// against.
	policy.DeniedPaths = fspolicy.DeniedPathsFor(in.HomePath, authored.WorkDir)

	return policy
}
