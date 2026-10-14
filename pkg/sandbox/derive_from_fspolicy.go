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
	//
	// Ignored entirely when NetworkAutoDeny is true (D8) — see that field's
	// doc comment for why bash's Auto rendering does not also special-case
	// the dev-server range.
	ConnectPorts []uint16

	// NetworkAutoDeny renders BindPortRules/ConnectPortRules EMPTY instead of
	// DefaultPolicyForModel's unconditional DefaultConnectPorts seed (ADR-092
	// D8/FR-042 — Auto denies bash's outbound network by default). On Linux,
	// ABI v4+ installs handledAccessNet unconditionally at the backend level
	// (sandbox_linux.go::computeRights), independent of whether the ruleset
	// carries any port rules — so an empty ConnectPortRules here is a true
	// kernel-enforced deny-all for this child's connect(2), not "no
	// restriction." macOS renders ConnectPortRules identically
	// (seatbelt_profile.go) — same mechanism, no extra code needed there.
	//
	// Set ONLY by the bash tool's own per-turn rendering under Auto mode
	// (pkg/tools/preflight.go's network evaluator supplies the grant state
	// that decides this). Every other caller — including bash's own
	// rendering under Ask or God Mode — leaves this false and keeps today's
	// DefaultConnectPorts-seeded behavior byte-identical.
	NetworkAutoDeny bool

	// NetworkGranted restores DefaultConnectPorts when NetworkAutoDeny is
	// also true — the D8 network-widening grant (FR-044): once the pre-flight
	// escalation is approved for the session, the bash child's connect-port
	// allow-list reverts to EXACTLY DefaultConnectPorts (port-level, not
	// domain-level — sandbox.go's own documented Landlock NET_CONNECT_TCP
	// limitation). The widened render does NOT also pick up ConnectPorts'
	// dev-server range: a granted bash network escalation is about outbound
	// need (curl, git push, package registries), not binding a local
	// listener. Ignored when NetworkAutoDeny is false.
	NetworkGranted bool

	// WarnFn receives one message per rule the policy computation strips or
	// skips. May be nil.
	WarnFn func(msg string, path string)

	// AllowRoots are the turn's allowances inside $OMNIPUS_HOME that the
	// denial must see BEFORE it is computed (amendment §2 D3/D5, chief
	// ruling 2026-10-13): the published runtime generations (rx) and the
	// document runtime prefix (rx) plus the worker's document cache (rw).
	// turnKernelPolicy resolves rt and doc before calling this function and
	// fills this slice from them; without it the Linux expansion would
	// silently drop the additive read+exec rules (the F01 failure) and macOS
	// would deny a cache the turn owns.
	//
	// Each entry becomes one allowed branch of the deny enumeration and one
	// PathRule with its exact access — never a post-hoc patch on a denial
	// that already shipped.
	AllowRoots []fspolicy.AllowRoot

	// Floor renders the GOD MODE floor profile (amendment §2 D7, founder Q1
	// answer A): filesystem rights open on everything EXCEPT the protected
	// set, network untouched, no /tmp narrowing. God Mode keeps its meaning
	// (no approvals, writes anywhere, open network) and never lifts the
	// internal-file protection. Set only by the God Mode spawn paths.
	Floor bool
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
//   - PathGrants (ADR-092 D7/FR-036) each become one additional PathRule,
//     exactly the access class the pre-flight escalation approved — never
//     a path this turn's own secret set already covers (FR-037; see
//     pathGrantIsDenied).
//   - Scope is NOT carried across as a read restriction. Post-ADR-062 reads are
//     open, and Scope now governs writes only (spec FR-2.5). Rendering it as a
//     read restriction here would put the kernel back out of step with the app
//     layer in the one place this whole change exists to fix.
//   - TurnPolicyInput.NetworkAutoDeny/NetworkGranted (ADR-092 D8/FR-042)
//     render bash's Auto per-turn ConnectPortRules/BindPortRules empty by
//     default and widen to exactly DefaultConnectPorts on a network grant —
//     independent of fspolicy.FSPolicy, which carries no network vocabulary
//     (the package comment: a stdlib-only leaf that must stay filesystem-only).
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

	// A5/A6 allowance rules (runtime generations, document prefix, document
	// cache) are emitted HERE, before the denial is computed, each with its
	// exact access class — never appended after a denial that already
	// shipped. They are recorded on AllowedBranches so the Linux grant
	// expansion grants a branch root with ITS access rather than the parent
	// rule's (a published generation under the RWX home rule must stay
	// read+execute). macOS does not read AllowedBranches: its explicit
	// denies already withhold write from these roots.
	var allowanceRules []PathRule
	branchRoots := make([]string, 0, len(in.AllowRoots))
	for _, root := range in.AllowRoots {
		clean := filepath.Clean(root.Path)
		if clean == "" || clean == "." || !filepath.IsAbs(clean) {
			continue
		}
		access := AccessRead | AccessExecute
		if root.Writable {
			access = AccessRead | AccessWrite
		}
		allowanceRules = append(allowanceRules, PathRule{Path: clean, Access: access})
		branchRoots = append(branchRoots, clean)
	}
	if len(allowanceRules) > 0 {
		policy.FilesystemRules = append(policy.FilesystemRules, allowanceRules...)
		policy.AllowedBranches = append(policy.AllowedBranches, allowanceRules...)
	}

	// boot_epoch* — a minted boot file may not exist when the enumeration
	// runs; the anchored prefix deny (the same mechanism the secret backups
	// use) covers it by construction on both platforms.
	policy.DeniedPathPrefixes = append(policy.DeniedPathPrefixes, fspolicy.HomeGlobPrefixes(in.HomePath)...)

	if in.Floor {
		return deriveFloorPolicy(authored, in, policy, branchRoots)
	}

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
	// where a divergence can exist, without changing gateway startup. The God
	// Mode floor (above) skips the narrowing: God Mode keeps its free writes.
	policy.FilesystemRules = narrowSharedTmpWrite(policy.FilesystemRules)

	if in.NetworkAutoDeny {
		// D8/FR-042: bash's Auto per-turn render either denies outbound
		// network entirely (empty ConnectPortRules — a true kernel deny-all
		// on Linux ABI v4+ and on macOS Seatbelt, per this field's own doc
		// comment) or, once granted, EXACTLY DefaultConnectPorts. This
		// bypasses the extra-connect-ports branch below entirely: bash's
		// Auto network posture does not also inherit in.ConnectPorts' dev-
		// server range, and BindPortRules stays empty regardless of grant
		// state — a network-need grant is about outbound connect (curl, git
		// push, package registries), never about binding a local listener.
		policy.BindPortRules = nil
		if in.NetworkGranted {
			policy.ConnectPortRules = make([]NetPortRule, 0, len(DefaultConnectPorts))
			for _, p := range DefaultConnectPorts {
				policy.ConnectPortRules = append(policy.ConnectPortRules, NetPortRule{Port: p})
			}
		} else {
			policy.ConnectPortRules = nil
		}
	} else if len(in.ConnectPorts) > 0 {
		// Extra connect ports, deduplicated against the DefaultConnectPorts
		// seed. A duplicate rule is harmless to both backends but is noise in
		// a rendered Seatbelt profile that a reader then has to discount.
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

	// The per-turn deny set (amendment §2 D5): one enumeration serving both
	// backends. Children of $OMNIPUS_HOME outside every allowed branch are
	// denied — recursively down to the branch roots — with the named
	// protected inventory always denied on top, and the read-only branch
	// roots (media, read-only allowances) reported separately as
	// write-denied. The branch set is the work dir, the mounts, the
	// operator's allowed paths, the turn's A5/A6 allowances, and media — the
	// same branches the app layer's HomeGuard allows speak for, minus skills,
	// whose whole directory stays child-denied (A7, unchanged).
	branches := kernelBranchSet(authored, in, false)

	denied, writeDenied, enumErr := fspolicy.KernelDenyEnumeration(in.HomePath, authored.WorkDir, branches)
	if enumErr != nil {
		// FAIL CLOSED. Without the listing there is no way to tell the caller's
		// own tree from anyone else's, so fall back to the set with NO own-tree
		// exception at all: everything the old always-set named is denied
		// wholesale.
		//
		// That locks the agent out of its own working directory, which is
		// severe — and it is the correct direction. The alternative,
		// re-admitting roots wholesale, is the WIDER answer: it would hand the
		// child every agent's home precisely when we have just discovered we
		// cannot reason about the layout. A loud, obvious breakage beats a
		// silent widening, and the WarnFn below names the cause.
		if in.WarnFn != nil {
			in.WarnFn(
				"kernel policy: could not enumerate $OMNIPUS_HOME to separate this turn's own tree "+
					"from other agents'/workspaces'; denying the whole always-set wholesale for this "+
					"turn (the agent will be unable to reach its own home): "+enumErr.Error(),
				in.HomePath,
			)
		}
		slog.Error("sandbox: per-turn deny set could not be enumerated; falling back to the strictest set",
			"home", in.HomePath,
			"work_dir", authored.WorkDir,
			"error", enumErr,
			"effect", "the always-set denied wholesale for this turn")
		denied = fspolicy.SecretPaths(in.HomePath)
		writeDenied = nil
	}
	policy.DeniedPaths = denied
	policy.WriteDeniedPaths = writeDenied

	// The directory NODES on the chain down to each allowed branch, plus the
	// home itself (amendment §2 D5: the home is a denied node — reachable,
	// never granted). Separate from the list above because they must stay
	// REACHABLE (the branches live underneath them) while the entries
	// themselves must not be writable.
	//
	// Without this, `mv $OMNIPUS_HOME/agents $OMNIPUS_HOME/agents-old` succeeds
	// and every per-sibling deny computed above is bypassed in one syscall: the
	// denies name paths that no longer exist, and the relocated tree is covered
	// by nothing. Measured against a real child, with the correct deny list in
	// place, before this line existed — a wall that is computed and never
	// connected protects nothing, which is why KernelDeniedNodesFor is
	// consumed HERE rather than left as a helper nobody calls.
	//
	// No error leg: unlike the enumeration this needs no directory listing,
	// so there is nothing that can fail. It is derived from the branch paths
	// alone, which means it is also correct in the fail-closed branch above
	// (where `denied` fell back to the full set) — a node deny on a root that
	// is already denied wholesale is redundant, never wrong.
	policy.DeniedNodes = fspolicy.KernelDeniedNodesFor(in.HomePath, authored.WorkDir, branchRoots...)

	// PathGrants (ADR-092 D7/FR-036): each is rendered as one additional
	// PathRule, additive to the WorkDir/AllowedRoots rendering above — the
	// same DeriveKernelPolicy call, no parallel construction site.
	//
	// fspolicy.FSPolicy.Validate already refuses to construct a policy whose
	// PathGrants names a path IsCarveOut denies (FR-037), so authored.
	// PathGrants should never contain one here. This loop checks again
	// anyway, against policy.DeniedPaths/DeniedNodes (the FINAL, per-turn
	// deny set this function just computed) rather than trusting the
	// upstream Validate call happened, or happened against the same denied
	// set — defense in depth for the one guarantee that must never regress:
	// a PathGrant can never reopen a path this turn's own kernel-only secret
	// set (SecretPaths) already covers, even given a bug in whatever
	// produced the grant. A skipped grant is warned, never silently applied.
	for _, g := range authored.PathGrants {
		grantPath := filepath.Clean(g.Path)
		if g.Access == 0 || grantPath == "" || grantPath == "." {
			continue
		}
		if pathGrantIsDenied(grantPath, policy.DeniedPaths, policy.DeniedNodes) {
			if in.WarnFn != nil {
				in.WarnFn("kernel policy: PathGrant refused — path falls within the secret set (FR-037)", grantPath)
			}
			slog.Warn("sandbox: PathGrant refused, secret-set path",
				"path", grantPath, "home", in.HomePath, "work_dir", authored.WorkDir)
			continue
		}
		policy.FilesystemRules = append(policy.FilesystemRules, PathRule{Path: grantPath, Access: g.Access})
	}

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

// deriveFloorPolicy renders the GOD MODE floor (amendment §2 D7, founder Q1
// answer A): God Mode keeps its documented meaning — no approvals, writes
// anywhere, network open, no resource caps — and never lifts the protected-
// internal-file set. Concretely:
//
//   - filesystem rights are open on everything EXCEPT the same D2/D3-derived
//     deny enumeration every ordinary turn gets (one "/" RWX rule whose Linux
//     expansion grants around the deny set; macOS renders it as blanket
//     allows under explicit denies — the measured precedence rule does the
//     rest);
//   - no write-denied branches: the floor has no read-only allowances of its
//     own, and "write anywhere" is the mode's documented meaning;
//   - network is explicitly UNHANDLED (NetworkUnhandled): no egress filter,
//     no port allow-list — Linux ABI v4+ then declares no net handling at
//     all instead of deny-all-on-empty, and macOS renders blanket network
//     allows instead of (deny network*);
//   - the /tmp write narrowing is skipped (God Mode writes anywhere).
//
// The known Linux limit is measured, not assumed (T-L3): Landlock cannot punch
// a grant out of an allowed parent, so the floor's enumerated ancestor chain
// leaves a child unable to CREATE a new entry directly in an enumerated
// ancestor directory. If a common God Mode flow breaks on that, the fix is to
// document it in docs/security.md — never to weaken the floor.
func deriveFloorPolicy(authored fspolicy.FSPolicy, in TurnPolicyInput, policy SandboxPolicy, allowanceBranchRoots []string) SandboxPolicy {
	// The floor's working branch is the turn's WHOLE work tree, not the
	// exact per-turn WorkDir: a God setup child rooted at an installation
	// prefix inside the workspace must keep reading (and writing) the
	// workspace's own files — God Mode A opens everything except the
	// protected set. Outside a workspace work tree the branch is unchanged.
	floorWork := fspolicy.WorkTreeRootFor(in.HomePath, authored.WorkDir)

	branches := kernelBranchSet(authored, in, true)

	denied, writeDenied, enumErr := fspolicy.KernelDenyEnumeration(in.HomePath, floorWork, branches)
	if enumErr != nil {
		if in.WarnFn != nil {
			in.WarnFn(
				"god floor: could not enumerate $OMNIPUS_HOME; falling back to the strictest always-set "+
					"for this child: "+enumErr.Error(),
				in.HomePath,
			)
		}
		slog.Error("sandbox: god floor deny set could not be enumerated; falling back to the strictest set",
			"home", in.HomePath, "work_dir", authored.WorkDir, "error", enumErr)
		denied = fspolicy.SecretPaths(in.HomePath)
		writeDenied = nil
	}
	policy.DeniedPaths = denied
	// Read-only branches stay read-only under the floor: God Mode A opens
	// everything except the PROTECTED set and the amendment's read-only
	// runtime shapes — a published shared generation is rx for a God child
	// exactly as for an ordinary one (round-1 chief ruling; the workflow test
	// chmods the program owner-writable so only the kernel can deny).
	policy.WriteDeniedPaths = writeDenied
	policy.DeniedNodes = fspolicy.KernelDeniedNodesFor(in.HomePath, floorWork, allowanceBranchRoots...)
	// The read-only branches must also survive the LINUX grant expansion: a
	// branch root is granted with ITS access class instead of the blanket
	// root rule's, so a generation stays read+execute where Seatbelt's
	// explicit write deny does the same job.
	policy.AllowedBranches = policy.AllowedBranches[:0:0]
	for _, root := range in.AllowRoots {
		access := AccessRead | AccessExecute
		if root.Writable {
			access = AccessRead | AccessWrite
		}
		policy.AllowedBranches = append(policy.AllowedBranches, PathRule{
			Path: filepath.Clean(root.Path), Access: access,
		})
	}

	// Free, network-open, uncapped: the floor carries no port rules and the
	// backend must not apply its default net handling.
	policy.NetworkUnhandled = true
	policy.BindPortRules = nil
	policy.ConnectPortRules = nil

	// Filesystem open on everything except the deny set: one root rule.
	// Landlock expands it around the denied entries; Seatbelt renders it as
	// blanket read/write/exec allows that the explicit deny block then beats.
	policy.FilesystemRules = append(policy.FilesystemRules, PathRule{
		Path:   "/",
		Access: AccessRead | AccessWrite | AccessExecute,
	})
	return policy
}

// kernelBranchSet builds the enumeration's branch list: the turn's own work
// dir and mounts, the operator's allowed paths, the §2 A5/A6 runtime
// allowances (marked Runtime — internal runtime input never reopens
// inventory), and the base read-only media branch. floorWiden applies the
// God floor's work-tree widening to the work dir and mounts (a God setup
// child rooted at an install prefix keeps its whole workspace open).
func kernelBranchSet(authored fspolicy.FSPolicy, in TurnPolicyInput, floorWiden bool) []fspolicy.KernelBranch {
	branches := make([]fspolicy.KernelBranch, 0, len(authored.AllowedRoots)+len(in.AllowedPaths)+len(in.AllowRoots)+2)
	widen := func(p string) string {
		if !floorWiden {
			return p
		}
		return fspolicy.WorkTreeRootFor(in.HomePath, p)
	}
	for _, root := range authored.AllowedRoots {
		branches = append(branches, fspolicy.KernelBranch{Path: widen(root), Writable: true})
	}
	for _, p := range in.AllowedPaths {
		branches = append(branches, fspolicy.KernelBranch{Path: p, Writable: true})
	}
	for _, root := range in.AllowRoots {
		branches = append(branches, fspolicy.KernelBranch{
			Path:     root.Path,
			Writable: root.Writable,
			Runtime:  true, // internal runtime input — never reopens inventory
		})
	}
	branches = append(branches, fspolicy.KernelBaseBranches(in.HomePath)...)
	return branches
}

// pathGrantIsDenied reports whether a would-be PathGrant rule at cleanPath
// (already filepath.Clean'd by the caller) falls within this turn's own
// kernel deny set — at or under any deniedPaths entry, or an exact match on
// a deniedNodes entry (a directory that must never carry a right of its own,
// even a single-path one; see SandboxPolicy.DeniedNodes's own doc comment).
//
// ADR-092 FR-037's "never widenable" guarantee is enforced HERE, at the one
// function that renders every PathGrant into the kernel policy — not only
// assumed from fspolicy.FSPolicy.Validate having already refused a
// secret-set grant, and not only from ExpandRulesExcluding's later,
// Linux-specific rewrite (sibling_grants.go) dropping any rule that lands on
// a denied path. Checking again here, against THIS function's own final
// denied/node lists, means the guarantee holds even if one of those other
// two layers is bypassed, absent, or (macOS) uses a different rendering
// mechanism (renderDeniedPaths, an explicit Seatbelt deny) that does not go
// through ExpandRulesExcluding at all.
func pathGrantIsDenied(cleanPath string, deniedPaths, deniedNodes []string) bool {
	for _, d := range deniedPaths {
		if pathIsUnder(cleanPath, filepath.Clean(d)) {
			return true
		}
	}
	for _, n := range deniedNodes {
		if cleanPath == filepath.Clean(n) {
			return true
		}
	}
	return false
}
