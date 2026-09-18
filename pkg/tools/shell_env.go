// shell_env.go — ADR-090 environment-setup: per-turn execution environment
// composition and kernel-policy augmentation for the bash tool.
//
// ES-FR-04: "The existing execution tool makes workspace packages and
// approved shared runtimes available to every permitted native agent. Supply
// executable/package paths and writable caches/temp directories within the
// sandbox. Replace Mia/worker/Admin ID checks with context-based runtime
// availability."
//
// Three independent layers compose into the per-turn child environment, in
// PATH precedence order:
//
//  1. Generic prefixes (pkg/environmentsetup.RuntimeEnvPaths — storage's
//     single path authority, no path discovery duplicated here): the
//     workspace env subtree (<turn workspace root>/.omnipus/env, conventional
//     bin/ layout) and the app-managed shared store's published generations,
//     in storage's deterministic order — workspace first, so an agent's own
//     install wins over a shared one. Existence-tolerant: a workspace or
//     generation that does not exist simply contributes nothing (the PATH
//     prepends only existing directories).
//  2. The optional managed document runtime (read-only shared prefix,
//     per-turn writable cache): injected only when the runtime passes
//     readiness (converter registry validation via documentruntime.ConverterServiceOverride).
//     A BROKEN runtime is never injected — commands keep running on the host
//     environment and the result carries a truthful notice, so the fallback
//     is loud rather than silent, and an optional Office installation never
//     blocks unrelated commands (coordinator ruling, 2026-09-18). Its fixed
//     prefix/manifest is NOT a prerequisite for generic installation success:
//     a generically installed tool precedes it on PATH and wins.
//  3. The inherited host environment: always the PATH tail, so anything the
//     composition does not provide falls through to the operator's real
//     environment (god mode) or the sandbox baseline (non-god mode), exactly
//     as before this layer existed.
//
// The composition is resolved PER TURN from the turn's authorized root —
// never from a construction-time agent ID or a remembered workspace — so an
// agent serving workspaces A and B in alternating turns observes A's and B's
// prefixes/caches respectively and can never leak one into the other (GS-05).

package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/documentruntime"
	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// documentCacheDirName is the per-turn writable cache directory the managed
// document runtime points HOME/TMPDIR/XDG_CACHE_HOME at. It lives INSIDE the
// turn's authorized root (workspace dir, or the agent's own home for
// standalone turns) so caches never cross workspaces and the turn's existing
// write authority covers them.
const documentCacheDirName = ".omnipus-document-cache"

// runtimeEnvTurn resolves the generic per-turn runtime paths via storage's
// RuntimeEnvPaths (workspace env subtree + shared store generations).
// Existence-tolerant by contract — missing state yields an empty view, not an
// error — but a real failure (unresolvable workspace root, unreadable
// published set) IS returned: the caller composes nothing and attaches a
// truthful notice, because silently falling through to the host PATH could
// run a different program of the same name. The command itself still runs.
func runtimeEnvTurn(workspaceRoot string) (environmentsetup.RuntimeEnv, error) {
	return environmentsetup.RuntimeEnvPaths(config.OmnipusHomeDir(), workspaceRoot)
}

// runtimeEnvNotice renders the per-turn notice for absent generic runtime
// paths: err means the view could not be resolved at all; rt.Broken lists
// published generations whose marker is corrupt or missing, which storage
// deliberately keeps out of PATH — invisible without a notice, they would be
// exactly the "host program of the same name" trap.
func runtimeEnvNotice(rt environmentsetup.RuntimeEnv, err error) string {
	switch {
	case err != nil:
		return "installed runtime paths could not be resolved (" + err.Error() +
			"); this command ran WITHOUT them, so a host program of the same name may have run instead. " +
			"Repair installed runtimes with environment_setup before relying on them."
	case len(rt.Broken) > 0:
		return "installed shared runtime generation(s) " + strings.Join(rt.Broken, ", ") +
			" are unreadable or corrupt and were not made available to this command. " +
			"Republish them with environment_setup (scope \"shared\")."
	default:
		return ""
	}
}

// turnDocumentLayout derives the per-turn view of the wired document runtime:
// the shared, read-only prefix (Bin/Lib/Skills/Manifest) is static; the
// writable cache follows the turn's authorized root. An empty baseDir keeps
// the construction-time cache (degenerate; t.workingDir is always set in
// practice).
func (t *ExecTool) turnDocumentLayout(baseDir string) documentruntime.Layout {
	layout := *t.documentRuntime
	if baseDir != "" {
		layout.Cache = filepath.Join(baseDir, documentCacheDirName)
	}
	return layout
}

// documentEnvTurn resolves the document-runtime layer for one run call:
// readiness via the engine-owned converter-registry validation (no second
// parser here), the per-turn cache materialized, and — on any failure — a
// truthful notice instead of an injected broken environment.
func (t *ExecTool) documentEnvTurn(baseDir string) documentEnvLayer {
	if t.documentRuntime == nil {
		return documentEnvLayer{}
	}
	layout := t.turnDocumentLayout(baseDir)
	if _, err := documentruntime.ConverterServiceOverride(layout); err != nil {
		return documentEnvLayer{notice: documentRuntimeBrokenNotice(err.Error())}
	}
	if err := ensureTurnCache(baseDir); err != nil {
		return documentEnvLayer{notice: documentRuntimeBrokenNotice("writable cache unavailable: " + err.Error())}
	}
	return documentEnvLayer{layout: layout, ok: true}
}

// ensureTurnCache creates the per-turn document cache directory under the
// authorized turn root WITHOUT following a pre-existing symlink. The kernel
// policy grants read+write on this exact directory, and a symlink could aim
// that grant anywhere — so a symlink (or non-directory) at the path is
// refused, creation uses os.Mkdir (fails on an existing entry, never
// follows one), and the final location is resolved and confirmed to have
// stayed inside the turn root. Same parent-side confinement rule storage
// applies when it resolves the shared store's real location before deriving
// grants (pkg/environmentsetup install.go::beginSharedInstall).
func ensureTurnCache(baseDir string) error {
	cache := filepath.Join(baseDir, documentCacheDirName)
	if info, err := os.Lstat(cache); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", documentCacheDirName)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", documentCacheDirName)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if mkErr := os.Mkdir(cache, 0o755); mkErr != nil && !errors.Is(mkErr, os.ErrExist) {
			return mkErr
		}
	} else {
		return err
	}
	// Resolve through the sanctioned realpath layer (resolvepath.go), not a
	// direct filepath.EvalSymlinks — FR-034's chokepoint rule; the lint
	// allowlist is not expanded. The inputs are internally owned paths this
	// function has just verified or created (never caller-supplied tool
	// arguments), so for them the helper resolves exactly like EvalSymlinks.
	// Its extra walk-up fallback fires only for a leaf that vanished between
	// the Lstat/Mkdir above and this call (a concurrent delete); there the
	// containment check below — not the resolver — is the guard. The
	// must-exist re-check keeps the original EvalSymlinks contract: a cache
	// that vanished after creation still fails this function (the helper's
	// not-yet-existing-leaf fallback must not turn that into success).
	if _, err := os.Lstat(cache); err != nil {
		return err
	}
	realCache, err := resolveRealpathUnderWorkDir(cache, baseDir)
	if err != nil {
		return err
	}
	realBase, err := resolveRealpathUnderWorkDir(baseDir, baseDir)
	if err != nil {
		return err
	}
	if !pathContains(realBase, realCache) {
		return fmt.Errorf("cache path escaped the turn root")
	}
	return nil
}

// documentEnvLayer carries the per-turn document-runtime decision for one
// run call: ok means ChildEnvironment may be applied with layout; notice is
// the honest report attached to the result when the wired runtime is broken
// (commands still run — on the host environment).
type documentEnvLayer struct {
	layout documentruntime.Layout
	ok     bool
	notice string
}

func documentRuntimeBrokenNotice(reason string) string {
	return "document runtime is not ready: " + reason +
		". This command ran without the managed document toolchain; " +
		"repair it with environment_setup (scope \"shared\") before document work."
}

// composeExecutionEnv layers the per-turn composition over base and returns
// the final child environment.
//
// base is the spawn-path's own baseline environment (god mode: the scrubbed
// host environ; background sandbox: sandboxLimitsEnv; foreground sandbox:
// nil, which historically means "inherit" and must STAY nil when nothing has
// to be composed). When doc.ok, documentruntime.ChildEnvironment applies the
// managed document toolchain over base first. Then the generic runtime paths'
// bin directories are prepended (storage's deterministic order — workspace
// before shared), the document prefix sits behind them, and the inherited
// host PATH survives as the tail.
//
// A nil base with nothing to compose returns nil unchanged — byte-identical
// to the pre-ADR-090 behavior for a tool with no runtime and no workspace
// environment. When a nil base DOES compose, the materialized baseline is
// sandbox.ScrubGatewayEnv() — the v0.2 #155 child-env allowlist — never the
// raw host environ: sandbox.Run's mergeEnv appends the caller env AFTER its
// scrubbed gateway env and later duplicate entries win on exec, so raw host
// entries would land in the child and override the scrub (runtime-final-review
// R1). The allowlist keeps PATH/HOME/LANG and the rest of the legitimate
// baseline, so the composed PATH tail and working basics are unchanged.
func composeExecutionEnv(base []string, doc documentEnvLayer, rt environmentsetup.RuntimeEnv) []string {
	// The baseline PATH tail survives the whole composition: when base is nil
	// (inherit), the tail comes from the SCRUBBED environ — the environment
	// the child would legitimately inherit (R1: never the raw host environ).
	lookup := base
	if lookup == nil {
		lookup = sandbox.ScrubGatewayEnv()
	}
	hostPATH, _ := envValue(lookup, "PATH")

	if doc.ok {
		base = documentruntime.ChildEnvironment(base, doc.layout)
	}

	segments := append([]string(nil), rt.BinDirs...)
	if doc.ok {
		segments = append(segments, doc.layout.Bin)
	}
	if len(segments) == 0 {
		return base
	}
	if hostPATH != "" {
		segments = append(segments, hostPATH)
	}
	if base == nil {
		// Materialize the "inherit" baseline from the same scrubbed allowlist
		// the lookup used, so the composed PATH tail and the materialized
		// baseline cannot disagree (R1).
		base = lookup
	}
	return setEnvValue(base, "PATH", strings.Join(segments, string(os.PathListSeparator)))
}

// augmentKernelPolicy extends the turn's kernel policy with every runtime
// layer this turn exposes to the child:
//
//   - generic prefixes (the caller-resolved RuntimeEnv): read+execute on
//     ReadExec roots with overlapping base-policy writes stripped (the shared
//     store must never be agent-writable), read+write on the per-turn
//     Writable dirs (workspace cache/tmp);
//   - the optional managed document runtime: read+execute on the prefix
//     (writes over it stripped), read+write on the per-turn cache;
//   - a Unix socket bind/connect rule on the resolved turn cwd, exactly as
//     the retired ApplySandboxAccess did — document IPC stays reachable.
//
// rt is the SAME resolved generic-runtime layer the environment composition
// used this turn: executeRun resolves it ONCE per run call, and on resolution
// failure passes the zero RuntimeEnv with the truthful notice reported at the
// run-call level. The policy rides that snapshot — env and kernel grants are
// derived from one read, never a second, later one (runtime-final-review R2).
//
// No agent-ID gates and no admin write grant: availability follows the
// turn's authorized root alone (ES-FR-04). An error means a rule COULD NOT
// be derived for an existing layer — the caller fails the call rather than
// silently spawning with the wider boot profile.
func (t *ExecTool) augmentKernelPolicy(policy sandbox.SandboxPolicy, cwd string, rt environmentsetup.RuntimeEnv, doc documentEnvLayer) (sandbox.SandboxPolicy, error) {
	out := policy
	out.FilesystemRules = append([]sandbox.PathRule(nil), policy.FilesystemRules...)
	out.UnixSocketRules = append(append([]sandbox.UnixSocketRule(nil), policy.UnixSocketRules...), sandbox.UnixSocketRule{Path: filepath.Clean(cwd), Bind: true, Connect: true})

	// stripWrite removes AccessWrite from any base rule overlapping root.
	// It is applied ONLY to shared-store generations and the document prefix:
	// both sit outside every legitimate base write root, so this is defensive
	// (a misconfigured turn policy must not hand setup code write access to
	// app-managed generations). The workspace prefix is deliberately NOT
	// stripped: it is INSIDE the turn's authorized workspace, and Landlock
	// grants by union of matching rules — stripping the covering workspace
	// rule would silently revoke the agent's write access to the entire
	// workspace, not just the env subtree.
	//
	// R3 limitation, deliberate: stripWrite clears the WHOLE write bit of a
	// covering rule, including an ANCESTOR rule (e.g. an operator-granted
	// write over $OMNIPUS_HOME that covers the shared store). Splitting such
	// a rule to carve out only the store subtree is not attempted — a split
	// that is wrong under lexical-vs-resolved path mismatches would silently
	// leave write on a path that resolves INTO the store, which is far worse
	// than the availability-only cost of the broad strip (no privilege gain;
	// requires operator misconfiguration to reach). Pinned by
	// TestAugmentKernelPolicyStripsWriteOnSharedStoreAncestorRule.
	stripWrite := func(root string) {
		for i := range out.FilesystemRules {
			if pathOverlaps(out.FilesystemRules[i].Path, root) {
				out.FilesystemRules[i].Access &^= sandbox.AccessWrite
			}
		}
	}

	// rt rides the caller's single snapshot: no re-read here. On a resolution
	// failure the caller passed the zero RuntimeEnv, so no generic rules are
	// composed and the baseline turn policy still governs the child — with
	// the failure reported truthfully at the run-call level (runtimeEnvNotice).
	// Shared generations: read+execute, never write (additive — every
	// published generation stays reachable).
	for _, gen := range rt.Shared {
		stripWrite(gen.Dir)
		out.FilesystemRules = append(out.FilesystemRules, sandbox.PathRule{Path: filepath.Clean(gen.Dir), Access: sandbox.AccessRead | sandbox.AccessExecute})
	}
	// The turn root's own env subtree: read+execute on top of the turn's
	// normal workspace write authority (no strip — see above).
	if rt.WorkspacePrefix != "" {
		out.FilesystemRules = append(out.FilesystemRules, sandbox.PathRule{Path: filepath.Clean(rt.WorkspacePrefix), Access: sandbox.AccessRead | sandbox.AccessExecute})
	}
	// Per-turn writable dirs (workspace cache/tmp): read+write.
	for _, path := range rt.Writable {
		out.FilesystemRules = append(out.FilesystemRules, sandbox.PathRule{Path: filepath.Clean(path), Access: sandbox.AccessRead | sandbox.AccessWrite})
	}

	if t.documentRuntime != nil {
		// The managed prefix is the construction-time, read-only identity —
		// identical every turn — so the read+exec grant rides it directly. The
		// per-turn part (the cache) rides the validated doc layer snapshot
		// below, the same single-snapshot rule as the generic layer (R2).
		stripWrite(t.documentRuntime.Prefix)
		out.FilesystemRules = append(out.FilesystemRules, sandbox.PathRule{Path: filepath.Clean(t.documentRuntime.Prefix), Access: sandbox.AccessRead | sandbox.AccessExecute})
		// The cache rule rides ONLY the validated layer: documentEnvTurn
		// refuses a symlinked or escaped cache path, and the write grant must
		// never land on a location the turn root does not own.
		if doc.ok {
			out.FilesystemRules = append(out.FilesystemRules, sandbox.PathRule{Path: filepath.Clean(doc.layout.Cache), Access: sandbox.AccessRead | sandbox.AccessWrite})
		}
	}

	// The cwd hosts document IPC sockets; it must be writable through the
	// final rule set, or the derivation is wrong — fail loudly.
	if !cwdWritable(out.FilesystemRules, cwd) {
		return sandbox.SandboxPolicy{}, os.ErrPermission
	}
	return out, nil
}

// pathOverlaps reports whether child is equal to or nested under root
// (either path may be the deeper one; both must be absolute for a positive).
func pathOverlaps(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		if !filepath.IsAbs(pair[0]) || !filepath.IsAbs(pair[1]) {
			continue
		}
		if rel, err := filepath.Rel(pair[0], pair[1]); err == nil &&
			rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// pathContains reports whether path is root itself or nested under root
// (directional — the mirror direction is NOT containment: a deeper rule
// grants nothing for its ancestors).
func pathContains(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// cwdWritable reports whether some final rule carrying write COVERS path
// (directional containment via pathContains).
func cwdWritable(rules []sandbox.PathRule, path string) bool {
	for _, rule := range rules {
		if rule.Access&sandbox.AccessWrite == 0 {
			continue
		}
		if pathContains(rule.Path, path) {
			return true
		}
	}
	return false
}

// envValue returns the value of key in env (without the "KEY=" prefix).
func envValue(env []string, key string) (string, bool) {
	for _, kv := range env {
		if value, ok := strings.CutPrefix(kv, key+"="); ok {
			return value, true
		}
	}
	return "", false
}

// setEnvValue returns env with key set to value, replacing any prior entry.
// A nil env stays nil-based: it appends onto an EMPTY baseline rather than
// materializing os.Environ(). The old nil materialization was the R1 leak
// primitive (runtime-final-review): a composed env built from the raw host
// environ overrides sandbox.Run's scrubbed gateway env on duplicate keys, so
// host secrets reached the child. composeExecutionEnv supplies the scrubbed
// baseline itself; a future caller passing nil gets a minimal env, never a
// raw environ dump.
func setEnvValue(env []string, key, value string) []string {
	out := env
	if out == nil {
		out = make([]string, 0, 1)
	}
	entry := key + "=" + value
	for i, kv := range out {
		if _, ok := strings.CutPrefix(kv, key+"="); ok {
			out[i] = entry
			return out
		}
	}
	return append(out, entry)
}
