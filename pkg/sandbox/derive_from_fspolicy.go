// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"log/slog"
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

	// Model selects the ADR-062 read/exec posture.
	Model FilesystemModel

	// AllowedPaths and AllowedExecPaths are the operator's config lists, passed
	// through to DefaultPolicyForModel unchanged.
	AllowedPaths     []string
	AllowedExecPaths []string

	// BindPorts is the dev-server range expanded by the caller.
	BindPorts []uint16

	// ConnectPorts are outbound ports to allow ON TOP of DefaultConnectPorts
	// (53/80/443), which DefaultPolicyForModel already seeds. The gateway puts
	// the dev-server range here so a child can dial a gateway-owned dev server
	// and the egress proxy.
	//
	// It exists because the boot policy does not stop at DefaultPolicyForModel
	// — pkg/gateway/sandbox_apply.go appends the same range to the returned
	// policy afterwards. Without carrying it here, a per-turn policy would be
	// silently NARROWER than the boot profile on exactly one axis, and the
	// symptom would be a dev server that becomes unreachable the moment
	// per-turn confinement is switched on. Carrying it keeps the two policies
	// differing only where they are MEANT to differ: the filesystem.
	ConnectPorts []uint16

	// WarnFn receives one message per rule the policy computation strips or
	// skips. May be nil.
	WarnFn func(msg string, path string)
}

// DeriveKernelPolicy is THE single function that turns an authored per-turn
// policy into a kernel policy. ADR-063 D1 / spec FR-1.3.
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
//     open model, so a mount is a write grant and nothing else — see ADR-063 D4.
//   - Scope is NOT carried across as a read restriction. Post-ADR-062 reads are
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

	// Narrow the blanket /tmp WRITE grant for this turn. Reads and exec stay.
	//
	// DefaultPolicyForModel grants /tmp read+write+execute as a scratch space.
	// That is defensible for the BOOT profile, which has no turn to be measured
	// against — but for a per-turn policy it is the exact two-layer divergence
	// this ADR exists to remove. The authored policy confines writes to the work
	// dir and mounts; the kernel granting all of /tmp on top means `bash` can
	// write where `write_file` refuses, from the same turn, with the same
	// intent. Found in UAT, not by a test: write_file("/tmp/x") was denied while
	// `echo > /tmp/x` succeeded.
	//
	// Narrowed rather than removed, and only here. /tmp stays READABLE (things
	// legitimately read from it) and EXECUTABLE, and the per-user $TMPDIR keeps
	// its own write grant — that is the directory os.TempDir() returns and
	// hardened_exec forwards to every child, so mktemp/npm/pip/git/go build are
	// unaffected. What breaks is a tool writing to the SHARED /tmp specifically,
	// which is precisely the write the authored policy says a turn may not make.
	//
	// The boot profile is deliberately left alone: this fixes the divergence
	// where a divergence can exist, without changing gateway startup.
	policy.FilesystemRules = narrowSharedTmpWrite(policy.FilesystemRules)

	// Extra connect ports, deduplicated against the DefaultConnectPorts seed.
	// A duplicate rule is harmless to both backends but is noise in a rendered
	// Seatbelt profile that a reader then has to discount.
	if len(in.ConnectPorts) > 0 {
		seenPort := make(map[uint16]struct{}, len(policy.ConnectPortRules)+len(in.ConnectPorts))
		for _, r := range policy.ConnectPortRules {
			seenPort[r.Port] = struct{}{}
		}
		for _, p := range in.ConnectPorts {
			if _, dup := seenPort[p]; dup {
				continue
			}
			seenPort[p] = struct{}{}
			policy.ConnectPortRules = append(policy.ConnectPortRules, NetPortRule{Port: p})
		}
	}

	// The per-turn secret set, which is where the own-tree exception lands.
	// DefaultPolicyForModel populates the BOOT set; replacing it here is what
	// makes agents/ and workspaces/ enforceable at the kernel layer at all,
	// since they can only be excluded once a work dir exists to compare
	// against.
	//
	// KernelDeniedPathsFor, not DeniedPathsFor: the latter re-admits a whole
	// per-turn root once the work dir is inside it, which is exact for a
	// workspace-rooted turn and WIDE OPEN for an agent-home-rooted one. It
	// re-admits `agents` entirely, so every other agent's home became reachable
	// at the kernel layer while the app layer denied it by path. That gap was
	// executed against real children, not reasoned about: `cat` and `echo >` on
	// another agent's SOUL.md both succeeded from `bash`. KernelDeniedPathsFor
	// enumerates the siblings so the kernel list reproduces IsCarveOut's
	// per-path answer (FR-3.3, "carried across EXACTLY").
	denied, err := fspolicy.KernelDeniedPathsFor(in.HomePath, authored.WorkDir)
	if err != nil {
		// FAIL CLOSED. Without the listing there is no way to tell the caller's
		// own tree from anyone else's, so fall back to the set with NO own-tree
		// exception at all: agents/ and workspaces/ denied wholesale.
		//
		// That locks the agent out of its own working directory, which is
		// severe — and it is the correct direction. The alternative,
		// DeniedPathsFor's re-admission, is the WIDER answer: it would hand the
		// child every agent's home precisely when we have just discovered we
		// cannot reason about the layout. A loud, obvious breakage beats a
		// silent widening, and the WarnFn below names the cause.
		if in.WarnFn != nil {
			in.WarnFn(
				"kernel policy: could not enumerate $OMNIPUS_HOME to separate this turn's own tree "+
					"from other agents'/workspaces'; denying agents/ and workspaces/ wholesale for this "+
					"turn (the agent will be unable to reach its own home): "+err.Error(),
				in.HomePath,
			)
		}
		slog.Error("sandbox: per-turn deny set could not be enumerated; falling back to the strictest set",
			"home", in.HomePath,
			"work_dir", authored.WorkDir,
			"error", err,
			"effect", "agents/ and workspaces/ denied wholesale for this turn")
		denied = fspolicy.SecretPaths(in.HomePath)
	}
	policy.DeniedPaths = denied

	// The directory NODES on the chain down to the work dir. Separate from the
	// list above because they must stay REACHABLE (the work dir is underneath
	// them) while the entry itself must not be writable.
	//
	// Without this, `mv $OMNIPUS_HOME/agents $OMNIPUS_HOME/agents-old` succeeds
	// and every per-sibling deny computed above is bypassed in one syscall: the
	// denies name paths that no longer exist, and the relocated tree is covered
	// by nothing. Measured against a real child, with the correct deny list in
	// place, before this line existed — a wall that is computed and never
	// connected protects nothing, which is why KernelDeniedNodesFor is
	// consumed HERE rather than left as a helper nobody calls.
	//
	// No error leg: unlike KernelDeniedPathsFor this needs no directory
	// listing, so there is nothing that can fail. It is derived from the
	// work-dir path alone, which means it is also correct in the fail-closed
	// branch above (where `denied` fell back to the full set) — a node deny on
	// a root that is already denied wholesale is redundant, never wrong.
	policy.DeniedNodes = fspolicy.KernelDeniedNodesFor(in.HomePath, authored.WorkDir)

	// DeniedPathPrefixes is NOT set here. It is turn-independent — a backup
	// copy of a secret is a secret in every turn shape — so DefaultPolicyForModel
	// above populates it once, for the boot profile and every per-turn policy
	// alike. Naming it again here would be a second source of truth for a value
	// that has only one.

	return policy
}

// narrowSharedTmpWrite strips AccessWrite from a rule covering the SHARED /tmp,
// leaving read and execute intact and every other rule untouched.
//
// It matches "/tmp" and its symlink-resolved form ("/private/tmp" on macOS,
// where /tmp is a firmlink) because the policy is authored in declared paths
// while the kernel matches resolved ones — a check against only one spelling
// would silently do nothing on the platform that needs it.
//
// It deliberately does NOT match a path merely UNDER /tmp. A work dir or a
// mount target can legitimately live there (an operator running from a temp
// location, or a test), and those grants come from the authored policy. Only
// the blanket grant on the directory itself is narrowed.
func narrowSharedTmpWrite(rules []PathRule) []PathRule {
	if len(rules) == 0 {
		return rules
	}
	out := make([]PathRule, 0, len(rules))
	for _, r := range rules {
		if isSharedTmpPath(r.Path) {
			r.Access &^= AccessWrite
			// A rule with no access left would be noise in the rendered
			// profile; drop it rather than emit an empty allow.
			if r.Access == 0 {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// isSharedTmpPath reports whether p IS the shared temp directory itself (not
// something inside it), in either its declared or symlink-resolved spelling.
func isSharedTmpPath(p string) bool {
	switch filepath.Clean(p) {
	case "/tmp", "/private/tmp":
		return true
	}
	return false
}
