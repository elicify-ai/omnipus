// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — the §0 safety-gate baseline capture (spec:
// docs/internal/specs/unified-file-access-and-mounts-spec.md §0), app-layer
// half (ResolvePath) plus the app-vs-kernel divergence table.
//
// EVERY test in this file MUST pass against today's code, unchanged. This is
// a photograph, not a spec compliance test: once the unified engine (FR-1..
// FR-9) lands for real, any row here that starts failing WITHOUT a matching
// line in spec §6 is an unintended regression.
//
// ============================================================================
// CRITICAL — READ BEFORE TRUSTING "TODAY" AS THIS FILE DESCRIBES IT
// ============================================================================
//
// This gate was commissioned as spec §0's mandatory FIRST step: "before the
// unified engine becomes the decision path, the CURRENT behaviour of both
// layers must be captured as tests that pass against today's code" — and
// spec §5 step 1 says "Nothing else starts until this is green."
//
// While writing pkg/fspolicy/baseline_behaviour_test.go (the sibling half of
// this gate), a test failure surfaced that this branch's HEAD (commit
// 6e7aece0, "feat(fspolicy): one secret set, shared by both layers, split by
// turn context") had ALREADY landed FR-3.1/FR-3.2 (the merged app+kernel
// secret set) — BEFORE this baseline gate was ever captured. A second,
// unwired function (pkg/sandbox/derive_from_fspolicy.go, DeriveKernelPolicy)
// already implements FR-1.3's policy derivation and FR-3's per-turn
// DeniedPathsFor narrowing too.
//
// Net effect on what "today" actually means, verified by reading the diff
// against the previous commit and by direct experiment below:
//
//   - App layer (fspolicy.IsCarveOut / this file's ResolvePath rows):
//     config.json and cli.token are ALREADY carve-outs. Spec dataset row 4
//     ("read_file on cli.token | denied | app layer gains this") describes a
//     gain that has already happened, pre-gate.
//   - Kernel layer (sandbox.DefaultPolicyForModel — VERIFIED still the
//     production call site; DeriveKernelPolicy has no caller outside its own
//     test, grep confirms): UNCHANGED by that commit. Still grants RWX on the
//     whole of $OMNIPUS_HOME (including every agent's home and every
//     workspace) and denies only the boot-time 5-entry set (master.key,
//     credentials.json, config.json, cli.token, entities). Spec dataset row 5
//     ("bash cat on another agent's dir | denied | kernel gains this") is
//     STILL an open gap today — that part of FR-3 has not reached the
//     production kernel-policy call site.
//
// So today's REAL divergence is narrower than the spec's own dataset table:
// config.json/cli.token are no longer a divergence (both layers already deny
// them) — cross-agent reachability via agents/, workspaces/ is the one that
// remains. Every row below reflects what is ACTUALLY on disk right now, not
// what the task brief anticipated or what the spec's dataset table says —
// per the hard requirement, a failing test means a mis-recorded expectation,
// never a production-code "fix". This is reported in full in the qa-lead
// final report; treat this file as the receipts.
// ============================================================================
package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// baselineTree is the shared fixture for this file: a realistic
// $OMNIPUS_HOME with two agent homes, a workspace, and every secret entry
// populated with distinguishing real content — so every "allowed" assertion
// below can check actual bytes, not just "no error" (never enough on its
// own: a stub returning empty bytes would still pass an err==nil check).
type baselineTree struct {
	home               string // realpath'd $OMNIPUS_HOME
	agentSelf          string // home/agents/self
	agentOther         string // home/agents/other
	workspaceWork      string // home/workspaces/w1/work
	otherWorkspaceWork string // home/workspaces/w2/work
	outside            string // unrelated dir, no relation to home at all
	masterKey          string
	credentialsJSON    string
	configJSON         string
	cliToken           string
	entitiesSelfRecord string
	agentSelfNotesFile string
	agentOtherSoulFile string
	workspaceOwnFile   string
	otherWorkspaceFile string
	outsideFile        string
}

func newBaselineTree(t *testing.T) baselineTree {
	t.Helper()
	raw := t.TempDir()
	outsideRaw := t.TempDir()

	write := func(p, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir parent of %q: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %q: %v", p, err)
		}
	}

	tr := baselineTree{
		agentSelf:          filepath.Join(raw, "agents", "self"),
		agentOther:         filepath.Join(raw, "agents", "other"),
		workspaceWork:      filepath.Join(raw, "workspaces", "w1", "work"),
		otherWorkspaceWork: filepath.Join(raw, "workspaces", "w2", "work"),
		outside:            outsideRaw,
		masterKey:          filepath.Join(raw, "master.key"),
		credentialsJSON:    filepath.Join(raw, "credentials.json"),
		configJSON:         filepath.Join(raw, "config.json"),
		cliToken:           filepath.Join(raw, "cli.token"),
		entitiesSelfRecord: filepath.Join(raw, "entities", "agents", "self.json"),
	}
	tr.agentSelfNotesFile = filepath.Join(tr.agentSelf, "notes.txt")
	tr.agentOtherSoulFile = filepath.Join(tr.agentOther, "SOUL.md")
	tr.workspaceOwnFile = filepath.Join(tr.workspaceWork, "own.txt")
	tr.otherWorkspaceFile = filepath.Join(tr.otherWorkspaceWork, "other.txt")
	tr.outsideFile = filepath.Join(outsideRaw, "outside.txt")

	write(tr.agentSelfNotesFile, "self notes content")
	write(tr.agentOtherSoulFile, "agent other soul content")
	write(tr.workspaceOwnFile, "workspace own content")
	write(tr.otherWorkspaceFile, "other workspace content")
	write(tr.outsideFile, "outside content")
	write(tr.masterKey, "key-material")
	write(tr.credentialsJSON, `{"v":1}`)
	write(tr.configJSON, `{"sandbox":{"mode":"off"}}`)
	write(tr.cliToken, "live-bearer-token")
	write(tr.entitiesSelfRecord, `{"tools":{}}`)

	resolve := func(p string) string {
		t.Helper()
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatalf("EvalSymlinks(%q): %v", p, err)
		}
		return r
	}
	// Re-resolve every field through the real path (macOS: /var -> /private/var)
	// so later comparisons against ResolvePath's always-resolved output land
	// on the same spelling. This must happen AFTER every file above is
	// written, since EvalSymlinks requires the target to exist.
	tr.home = resolve(raw)
	tr.agentSelf = resolve(tr.agentSelf)
	tr.agentOther = resolve(tr.agentOther)
	tr.workspaceWork = resolve(tr.workspaceWork)
	tr.otherWorkspaceWork = resolve(tr.otherWorkspaceWork)
	tr.outside = resolve(tr.outside)
	tr.masterKey = resolve(tr.masterKey)
	tr.credentialsJSON = resolve(tr.credentialsJSON)
	tr.configJSON = resolve(tr.configJSON)
	tr.cliToken = resolve(tr.cliToken)
	tr.entitiesSelfRecord = resolve(tr.entitiesSelfRecord)
	tr.agentSelfNotesFile = resolve(tr.agentSelfNotesFile)
	tr.agentOtherSoulFile = resolve(tr.agentOtherSoulFile)
	tr.workspaceOwnFile = resolve(tr.workspaceOwnFile)
	tr.otherWorkspaceFile = resolve(tr.otherWorkspaceFile)
	tr.outsideFile = resolve(tr.outsideFile)

	return tr
}

// policyFor builds the real fspolicy.FSPolicy for workDir via
// fspolicy.EffectiveFSPolicy — never a hand-built FSPolicy{} literal — so
// this capture exercises the exact function production calls.
func policyFor(t *testing.T, tr baselineTree, agentHome string, restrict bool) fspolicy.FSPolicy {
	t.Helper()
	p, err := fspolicy.EffectiveFSPolicy(context.Background(), agentHome, "", restrict, tr.home, "self", "")
	if err != nil {
		t.Fatalf("EffectiveFSPolicy: %v", err)
	}
	return p
}

// ============================================================================
// Section A — FSScopeConfined matrix (task item 1)
// ============================================================================

// TestBaseline_Confined_ReadWriteMatrix captures every confined-scope
// decision in the task's dataset item 1: inside WorkDir, outside WorkDir, a
// ".." escape, a symlink pointing out, and each carve-out. Both read AND
// write are exercised per row (not just read) because item 4 (op is
// currently ignored) depends on this file independently proving both ops
// land the same place.
func TestBaseline_Confined_ReadWriteMatrix(t *testing.T) {
	tr := newBaselineTree(t)
	policy := policyFor(t, tr, tr.agentSelf, true)
	if policy.Scope != fspolicy.FSScopeConfined {
		t.Fatalf("policy.Scope = %q, want confined", policy.Scope)
	}

	t.Run("inside WorkDir: read succeeds with real content", func(t *testing.T) {
		h, err := ResolvePath(context.Background(), policy, "baseline", "", FSOpRead, "notes.txt")
		if err != nil {
			t.Fatalf("ResolvePath: %v", err)
		}
		defer h.Close()
		data, err := h.ReadFile()
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(data) != "self notes content" {
			t.Errorf("content = %q, want %q", data, "self notes content")
		}
	})

	t.Run("inside WorkDir: write succeeds and persists", func(t *testing.T) {
		h, err := ResolvePath(context.Background(), policy, "baseline", "", FSOpWrite, "written.txt")
		if err != nil {
			t.Fatalf("ResolvePath: %v", err)
		}
		defer h.Close()
		if err := h.WriteFile([]byte("new content")); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(tr.agentSelf, "written.txt"))
		if err != nil || string(data) != "new content" {
			t.Fatalf("write did not persist: data=%q err=%v", data, err)
		}
	})

	t.Run("outside WorkDir (unrelated dir): read now ALLOWED (ADR-061 / spec unified-file-access-and-mounts FR-2.2 — reads are open outside WorkDir, regardless of Scope)", func(t *testing.T) {
		h, err := ResolvePath(context.Background(), policy, "baseline", "", FSOpRead, tr.outsideFile)
		if err != nil {
			t.Fatalf("FR-2.2: expected a Confined read outside WorkDir to succeed, got: %v", err)
		}
		defer h.Close()
		data, err := h.ReadFile()
		if err != nil || string(data) != "outside content" {
			t.Fatalf("ReadFile: data=%q err=%v", data, err)
		}
	})

	t.Run("outside WorkDir (unrelated dir): write denied ErrOutsideScope, file untouched", func(t *testing.T) {
		_, err := ResolvePath(context.Background(), policy, "baseline", "", FSOpWrite, tr.outsideFile)
		if !errors.Is(err, ErrOutsideScope) {
			t.Errorf("err = %v, want ErrOutsideScope", err)
		}
		data, rerr := os.ReadFile(tr.outsideFile)
		if rerr != nil || string(data) != "outside content" {
			t.Fatalf("outside file must be untouched: data=%q err=%v", data, rerr)
		}
	})

	// relEscapeToOutside is a genuine ".." escape (not just lexically
	// suggestive) that lands on tr.outsideFile — a real location entirely
	// unrelated to $OMNIPUS_HOME, so ONLY the "escapes WorkDir" class fires,
	// never the carve-out class. filepath.Join("..", "other", ...) was tried
	// here first and rejected: relative to agents/self, ".." lands back
	// inside agents/, which is itself a carve-out root — so that path hit
	// ErrCarveOut (checked before ErrOutsideScope, resolvepath.go step 2
	// precedes step 3) instead of ErrOutsideScope, misrecording which class
	// of denial a plain ".." escape actually produces.
	relEscapeToOutside, err := filepath.Rel(tr.agentSelf, tr.outsideFile)
	if err != nil {
		t.Fatalf("filepath.Rel: %v", err)
	}

	t.Run("leading .. escape (to a genuinely unrelated dir, not a carve-out): read now ALLOWED (FR-2.2)", func(t *testing.T) {
		h, err := ResolvePath(context.Background(), policy, "baseline", "", FSOpRead, relEscapeToOutside)
		if err != nil {
			t.Fatalf("FR-2.2: expected a Confined '..' escape read to succeed, got: %v", err)
		}
		h.Close()
	})

	t.Run("leading .. escape (to a genuinely unrelated dir, not a carve-out): write denied ErrOutsideScope", func(t *testing.T) {
		_, err := ResolvePath(context.Background(), policy, "baseline", "", FSOpWrite, relEscapeToOutside)
		if !errors.Is(err, ErrOutsideScope) {
			t.Errorf("err = %v, want ErrOutsideScope", err)
		}
	})

	t.Run("symlink inside WorkDir pointing outside: read now ALLOWED (FR-2.2)", func(t *testing.T) {
		link := filepath.Join(tr.agentSelf, "escape_link")
		if err := os.Symlink(tr.outsideFile, link); err != nil {
			t.Skipf("symlinks not supported: %v", err)
		}
		h, err := ResolvePath(context.Background(), policy, "baseline", "", FSOpRead, "escape_link")
		if err != nil {
			t.Fatalf("FR-2.2: expected a Confined symlink-escape read to succeed, got: %v", err)
		}
		h.Close()
	})

	t.Run("symlink inside WorkDir pointing outside: write denied ErrOutsideScope", func(t *testing.T) {
		link := filepath.Join(tr.agentSelf, "escape_link_w")
		if err := os.Symlink(tr.outsideFile, link); err != nil {
			t.Skipf("symlinks not supported: %v", err)
		}
		_, err := ResolvePath(context.Background(), policy, "baseline", "", FSOpWrite, "escape_link_w")
		if !errors.Is(err, ErrOutsideScope) {
			t.Errorf("err = %v, want ErrOutsideScope", err)
		}
	})

	carveOutRows := []struct {
		name string
		path string
	}{
		{"master.key", tr.masterKey},
		{"credentials.json", tr.credentialsJSON},
		{"config.json", tr.configJSON},
		{"cli.token", tr.cliToken},
		{"entities/ record", tr.entitiesSelfRecord},
		{"another agent's home", tr.agentOtherSoulFile},
		{"another workspace", tr.otherWorkspaceFile},
	}
	for _, r := range carveOutRows {
		t.Run("carve-out ("+r.name+"): read denied ErrCarveOut even though outside WorkDir would also deny", func(t *testing.T) {
			_, err := ResolvePath(context.Background(), policy, "baseline", "", FSOpRead, r.path)
			// IsCarveOut runs BEFORE the within-workspace check (resolvepath.go
			// step 2 precedes step 3), so the carve-out class wins even under
			// Confined scope where ErrOutsideScope would otherwise also apply.
			if !errors.Is(err, ErrCarveOut) {
				t.Errorf("err = %v, want ErrCarveOut", err)
			}
		})
	}
}

// ============================================================================
// Section B — FSScopeUnrestricted matrix (task item 2)
// ============================================================================

// TestBaseline_Unrestricted_ReadWriteMatrix is the unrestricted-scope
// counterpart. UPDATED for ADR-061 / spec unified-file-access-and-mounts
// FR-2.2/FR-2.5, which this file's own original comment named as the
// expected future change: a write OUTSIDE WorkDir under Unrestricted no
// longer succeeds like a read does — ResolvePath now dispatches on op, and
// FSOpWrite is confined to WorkDir/a mount regardless of Scope. This test
// is the regression net that PROVES that change landed, not (as originally
// written) the baseline photograph of the pre-change behaviour it still
// documents in its sub-test names.
func TestBaseline_Unrestricted_ReadWriteMatrix(t *testing.T) {
	tr := newBaselineTree(t)
	policy := policyFor(t, tr, tr.agentSelf, false)
	if policy.Scope != fspolicy.FSScopeUnrestricted {
		t.Fatalf("policy.Scope = %q, want unrestricted", policy.Scope)
	}

	t.Run("outside WorkDir, non-carve-out: read succeeds with real content", func(t *testing.T) {
		h, err := ResolvePath(context.Background(), policy, "baseline", "", FSOpRead, tr.outsideFile)
		if err != nil {
			t.Fatalf("ResolvePath: %v", err)
		}
		defer h.Close()
		data, err := h.ReadFile()
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(data) != "outside content" {
			t.Errorf("content = %q, want %q", data, "outside content")
		}
	})

	t.Run("outside WorkDir, non-carve-out: write now DENIED under Unrestricted too (ADR-061 / spec unified-file-access-and-mounts FR-2.2/FR-2.5 — this test's own doc comment named this as the expected future change)", func(t *testing.T) {
		target := filepath.Join(tr.outside, "written_by_agent.txt")
		_, err := ResolvePath(context.Background(), policy, "baseline", "", FSOpWrite, target)
		if !errors.Is(err, ErrOutsideScope) {
			t.Errorf("err = %v, want ErrOutsideScope — FR-2.2: writes are confined to WorkDir/a mount regardless of Scope", err)
		}
		if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
			t.Fatalf("target must not have been created: stat err=%v", statErr)
		}
	})

	carveOutRows := []struct {
		name string
		path string
	}{
		{"master.key", tr.masterKey},
		{"credentials.json", tr.credentialsJSON},
		{"config.json", tr.configJSON},
		{"cli.token", tr.cliToken},
		{"entities/ record", tr.entitiesSelfRecord},
		{"another agent's home", tr.agentOtherSoulFile},
		{"another workspace", tr.otherWorkspaceFile},
	}
	for _, r := range carveOutRows {
		t.Run("carve-out ("+r.name+"): read denied ErrCarveOut even under Unrestricted (FR-017)", func(t *testing.T) {
			_, err := ResolvePath(context.Background(), policy, "baseline", "", FSOpRead, r.path)
			if !errors.Is(err, ErrCarveOut) {
				t.Errorf("err = %v, want ErrCarveOut", err)
			}
		})
		t.Run("carve-out ("+r.name+"): write ALSO denied ErrCarveOut even under Unrestricted", func(t *testing.T) {
			_, err := ResolvePath(context.Background(), policy, "baseline", "", FSOpWrite, r.path)
			if !errors.Is(err, ErrCarveOut) {
				t.Errorf("err = %v, want ErrCarveOut", err)
			}
		})
	}
}

// ============================================================================
// Section C — the own-tree exception, both shapes, through the REAL
// production call path (task item 3 / BDD S-6)
// ============================================================================

// TestBaseline_OwnTreeException_BothShapes_ThroughRealCallPath uses
// tools.ResolveTurnFSPolicy + tools.WithTurnWorkspaceDir — the exact shape
// every real generic file tool assembles its policy through (resolvepath.go
// ResolveTurnFSPolicy's own doc comment) — rather than a hand-built
// fspolicy.FSPolicy{}, matching BDD scenario S-6 precisely.
func TestBaseline_OwnTreeException_BothShapes_ThroughRealCallPath(t *testing.T) {
	tr := newBaselineTree(t)
	t.Setenv(config.EnvHome, tr.home)

	t.Run("agent-home-rooted turn: agent reaches its own home", func(t *testing.T) {
		ctx := context.Background() // no TurnWorkspaceDir set -> falls back to agentHome
		policy, err := ResolveTurnFSPolicy(ctx, tr.agentSelf, true)
		if err != nil {
			t.Fatalf("ResolveTurnFSPolicy: %v", err)
		}
		if policy.WorkDir != tr.agentSelf {
			t.Fatalf("WorkDir = %q, want %q (agent-home-rooted)", policy.WorkDir, tr.agentSelf)
		}
		h, err := ResolvePath(ctx, policy, "baseline", "", FSOpRead, tr.agentSelfNotesFile)
		if err != nil {
			t.Fatalf("expected the agent to reach its own home, got: %v", err)
		}
		defer h.Close()
		data, err := h.ReadFile()
		if err != nil || string(data) != "self notes content" {
			t.Fatalf("ReadFile: data=%q err=%v", data, err)
		}
	})

	t.Run("re-rooted workspace turn: agent's OWN home is DENIED — the shape a blanket own-tree exception gets wrong (FR-3.4)", func(t *testing.T) {
		ctx := WithTurnWorkspaceDir(context.Background(), tr.workspaceWork)
		policy, err := ResolveTurnFSPolicy(ctx, tr.agentSelf, true)
		if err != nil {
			t.Fatalf("ResolveTurnFSPolicy: %v", err)
		}
		if policy.WorkDir != tr.workspaceWork {
			t.Fatalf("WorkDir = %q, want the re-rooted workspace dir %q", policy.WorkDir, tr.workspaceWork)
		}
		// The turn's own work dir is still reachable.
		h, err := ResolvePath(ctx, policy, "baseline", "", FSOpRead, tr.workspaceOwnFile)
		if err != nil {
			t.Fatalf("expected the turn's own work dir to be reachable, got: %v", err)
		}
		h.Close()

		// The agent's own home — normally exempt under the agent-home-rooted
		// shape above — is NOT exempt here: WorkDir is a descendant of
		// workspaces/, not agents/, so the per-root own-tree exception never
		// fires for the agents/ root in this turn.
		_, err = ResolvePath(ctx, policy, "baseline", "", FSOpRead, tr.agentSelfNotesFile)
		if !errors.Is(err, ErrCarveOut) {
			t.Errorf("err = %v, want ErrCarveOut (own home must be as unreachable as any other agent's during a workspace turn)", err)
		}
	})
}

// ============================================================================
// Section D — op is (still) ignored (task item 4)
// ============================================================================

// TestBaseline_FSOp_IsNowConsulted — was TestBaseline_FSOp_CurrentlyIgnored,
// the direct, minimal proof of task item 4. Its own doc comment predicted
// exactly this: "This test WILL legitimately need updating once FR-2
// lands... that is expected, not a regression." ADR-061 / spec
// unified-file-access-and-mounts FR-2.1/FR-2.2 has now landed (the `_ = op`
// line is deleted, resolvepath.go), so this test now asserts the OPPOSITE
// of its original name: op DOES drive ResolvePath's decision for a path
// outside WorkDir. Read and write no longer get identical treatment under
// either Scope.
func TestBaseline_FSOp_IsNowConsulted(t *testing.T) {
	tr := newBaselineTree(t)

	t.Run("confined, outside WorkDir non-carve-out path: read now succeeds, write is still refused — op splits the outcome", func(t *testing.T) {
		policy := policyFor(t, tr, tr.agentSelf, true)
		readHandle, readErr := ResolvePath(context.Background(), policy, "baseline", "", FSOpRead, tr.outsideFile)
		if readErr != nil {
			t.Fatalf("FR-2.2: expected the read to succeed, got: %v", readErr)
		}
		readHandle.Close()

		_, writeErr := ResolvePath(context.Background(), policy, "baseline", "", FSOpWrite, tr.outsideFile)
		if !errors.Is(writeErr, ErrOutsideScope) {
			t.Fatalf("expected the write to still be refused with ErrOutsideScope, got: %v", writeErr)
		}
	})

	t.Run("unrestricted, outside WorkDir non-carve-out path: read succeeds, write is now DENIED — op splits the outcome regardless of Scope", func(t *testing.T) {
		policy := policyFor(t, tr, tr.agentSelf, false)
		readHandle, readErr := ResolvePath(context.Background(), policy, "baseline", "", FSOpRead, tr.outsideFile)
		if readErr != nil {
			t.Fatalf("read: %v", readErr)
		}
		readHandle.Close()

		writeTarget := filepath.Join(tr.outside, "op_ignored_write.txt")
		_, writeErr := ResolvePath(context.Background(), policy, "baseline", "", FSOpWrite, writeTarget)
		if !errors.Is(writeErr, ErrOutsideScope) {
			t.Fatalf("FR-2.2/FR-2.5: expected the write to now be denied even under Unrestricted, got: %v", writeErr)
		}
		if _, statErr := os.Stat(writeTarget); !os.IsNotExist(statErr) {
			t.Fatalf("target must not have been created: stat err=%v", statErr)
		}
	})
}

// ============================================================================
// Section E — app layer vs kernel layer, side by side (task's explicit
// ask: "a table with columns path | app layer today | kernel layer today")
// ============================================================================

// kernelDecision reports whether policy grants the given access bit for path,
// mirroring the longest-matching-prefix-wins semantics the real backends
// implement (Landlock's per-directory grant walk; Seatbelt's literal subpath
// rules), and checking DeniedPaths FIRST — sandbox.go's own doc comment on
// SandboxPolicy.DeniedPaths: "Populated under BOTH models... the exposure
// these close... exists in shipped code today". This is a test-only
// re-implementation of that matching logic for baseline-capture purposes; it
// does not touch production code.
func kernelDecision(policy sandbox.SandboxPolicy, path string, bit uint64) bool {
	clean := filepath.Clean(path)
	within := func(candidate, root string) bool {
		root = filepath.Clean(root)
		if candidate == root {
			return true
		}
		sep := string(filepath.Separator)
		if !strings.HasSuffix(root, sep) {
			root += sep
		}
		return strings.HasPrefix(candidate, root)
	}
	for _, d := range policy.DeniedPaths {
		if within(clean, d) {
			return false
		}
	}
	bestLen := -1
	granted := false
	for _, r := range policy.FilesystemRules {
		rp := filepath.Clean(r.Path)
		if !within(clean, rp) {
			continue
		}
		if len(rp) > bestLen {
			bestLen = len(rp)
			granted = r.Access&bit != 0
		}
	}
	return granted
}

// TestBaseline_AppVsKernel_DivergenceTable is the side-by-side comparison the
// task asked for. Both layers are exercised against the SAME path set, under
// Unrestricted scope on the app side (where the app layer's own decision
// varies path-by-path — under Confined almost everything outside WorkDir is
// uniformly denied, which would make the table uninteresting) and against
// sandbox.DefaultPolicyForModel — VERIFIED still the production kernel-policy
// call site (pkg/sandbox/sandbox.go:609; DeriveKernelPolicy, the FR-1.3
// successor, has no caller outside its own test as of this commit) — with
// FilesystemModelConfined. The model parameter does not affect this
// particular table: DeniedPaths and the $OMNIPUS_HOME RWX rule are both
// populated identically under Confined and Open (sandbox.go's own doc
// comment on DeniedPaths and on DefaultPolicyForModel's rule set).
func TestBaseline_AppVsKernel_DivergenceTable(t *testing.T) {
	tr := newBaselineTree(t)
	appPolicy := policyFor(t, tr, tr.agentSelf, false) // Unrestricted
	kernelPolicy := sandbox.DefaultPolicyForModel(sandbox.FilesystemModelConfined, tr.home, nil, nil, nil, nil)

	appAllows := func(t *testing.T, path string, op FSOp) bool {
		t.Helper()
		h, err := ResolvePath(context.Background(), appPolicy, "baseline", "", op, path)
		if err != nil {
			return false
		}
		h.Close()
		return true
	}

	rows := []struct {
		name        string
		path        string
		appRead     bool
		appWrite    bool
		kernelRead  bool
		kernelWrite bool
		why         string
	}{
		{
			"master.key", tr.masterKey,
			false, false, false, false,
			"both layers agree: unconditional carve-out / DeniedPaths entry",
		},
		{
			"credentials.json", tr.credentialsJSON,
			false, false, false, false,
			"both layers agree: unconditional carve-out / DeniedPaths entry",
		},
		{
			"config.json", tr.configJSON,
			false, false, false, false,
			"NO LONGER a divergence: both layers already deny this. The app layer only closed its half TODAY (commit 6e7aece0, pre-gate) — the kernel layer already denied it before that commit too (it was always in the boot-time secret set). Spec dataset row 4 describes this as a future gain; it has already happened.",
		},
		{
			"cli.token", tr.cliToken,
			false, false, false, false,
			"same as config.json: already aligned, not a live divergence today",
		},
		{
			"entities/ record", tr.entitiesSelfRecord,
			false, false, false, false,
			"both layers agree: unconditional carve-out / DeniedPaths entry",
		},
		{
			"own agent home file", tr.agentSelfNotesFile,
			true, true, true, true,
			"both layers agree: the agent's own home is reachable both ways today",
		},
		{
			"ANOTHER agent's home file", tr.agentOtherSoulFile,
			false, false, true, true,
			"THE live divergence (spec dataset row 5): app layer denies via the agents/ carve-out (own-tree exception is per-agent), kernel layer grants — sandbox.DefaultPolicyForModel still renders one coarse RWX rule for the whole of $OMNIPUS_HOME with no per-agent scoping. `bash cat` on another agent's home succeeds at the kernel layer today. FR-3.5 closes this, but only once DeriveKernelPolicy (which already computes the correct per-turn answer) is actually wired to a spawn path — it is not yet.",
		},
		{
			"another workspace's file", tr.otherWorkspaceFile,
			false, false, true, true,
			"same divergence as the cross-agent row, via the workspaces/ carve-out this time",
		},
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			gotAppRead := appAllows(t, r.path, FSOpRead)
			gotAppWrite := appAllows(t, r.path, FSOpWrite)
			gotKernelRead := kernelDecision(kernelPolicy, r.path, sandbox.AccessRead)
			gotKernelWrite := kernelDecision(kernelPolicy, r.path, sandbox.AccessWrite)

			if gotAppRead != r.appRead {
				t.Errorf("app read = %v, want %v — %s", gotAppRead, r.appRead, r.why)
			}
			if gotAppWrite != r.appWrite {
				t.Errorf("app write = %v, want %v — %s", gotAppWrite, r.appWrite, r.why)
			}
			if gotKernelRead != r.kernelRead {
				t.Errorf("kernel read = %v, want %v — %s", gotKernelRead, r.kernelRead, r.why)
			}
			if gotKernelWrite != r.kernelWrite {
				t.Errorf("kernel write = %v, want %v — %s", gotKernelWrite, r.kernelWrite, r.why)
			}

			if (r.appRead != r.kernelRead || r.appWrite != r.kernelWrite) && !t.Failed() {
				t.Logf("DIVERGENCE recorded (both layers computed as expected, but they disagree with each other): app(read=%v,write=%v) vs kernel(read=%v,write=%v) — %s",
					r.appRead, r.appWrite, r.kernelRead, r.kernelWrite, r.why)
			}
		})
	}
}
