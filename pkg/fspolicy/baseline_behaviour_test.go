//go:build goolm && stdjson

// Package fspolicy — the §0 safety-gate baseline capture (spec:
// docs/internal/specs/unified-file-access-and-mounts-spec.md §0).
//
// EVERY test in this file MUST pass against today's code, unchanged. That is
// the entire point: these are not tests of what the unified engine (FR-1..
// FR-9) SHOULD do, they are a photograph of what fspolicy.EffectiveFSPolicy
// and fspolicy.IsCarveOut actually do RIGHT NOW, on this commit, so that once
// the merge lands, any row here that starts failing without a matching line
// in spec §6 is — by definition — an unintended regression, not "an
// improvement nobody documented."
//
// Where today's behaviour is a known, accepted hole (config.json and
// cli.token are NOT in the carve-out set yet — buildCarveOuts, carveout.go),
// it is recorded exactly as it is, with a comment naming the FR that closes
// it later (FR-3.2's merged secret set). Do not "fix" the expectation to
// match the future engine — that defeats the entire gate.
package fspolicy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// IMPORTANT — READ BEFORE TRUSTING THE config.json/cli.token ROWS BELOW.
//
// This gate was written expecting to find config.json and cli.token NOT yet
// in buildCarveOuts (carveout.go) — that is what the task brief and the spec
// dataset (row 4) describe as "today". Investigating a test failure while
// writing this file found that is no longer true: commit 6e7aece0
// ("feat(fspolicy): one secret set, shared by both layers, split by turn
// context") already landed FR-3.1/FR-3.2's merged secret set on THIS branch,
// BEFORE this baseline gate was captured — a direct violation of spec §0/§5
// ("nothing else starts until this is green"). buildCarveOuts now returns
// SecretPaths(omnipusHome), which already includes config.json and
// cli.token. See the qa-lead final report for the full finding; the rows
// below record what IS on disk right now (post-FR-3.1/3.2, pre-FR-4/5/6/7/8),
// not the pre-FR-3 state the task brief anticipated. Do not "fix" these back
// to false — that would misrecord actual current behaviour to match a
// stale expectation, which is exactly what this gate exists to prevent.
//
// baselineHomeTree builds a realistic $OMNIPUS_HOME-shaped tree under
// t.TempDir() with two agent homes, one workspace, and the five
// buildCarveOuts roots all populated with real files — so IsCarveOut and
// EffectiveFSPolicy are exercised against actual paths on disk (through the
// same realpath machinery production uses), not synthetic /omnh-style
// strings the way carveout_test.go's lexical tests do.
//
// All returned paths are ALREADY realpath-resolved (filepath.EvalSymlinks),
// per the task's explicit macOS warning: t.TempDir() lives under /var, a
// symlink to /private/var, so an unresolved comparison against
// EffectiveFSPolicy's (always-resolved) output would fail for reasons having
// nothing to do with the assertion under test.
func baselineHomeTree(t *testing.T) (home, agentSelf, agentOther, workspaceWork, otherWorkspaceWork string) {
	t.Helper()

	raw := t.TempDir()

	mustMkdirAll := func(p string) {
		t.Helper()
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", p, err)
		}
	}
	mustWrite := func(p, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir parent of %q: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %q: %v", p, err)
		}
	}

	agentSelfRaw := filepath.Join(raw, "agents", "self")
	agentOtherRaw := filepath.Join(raw, "agents", "other")
	workspaceWorkRaw := filepath.Join(raw, "workspaces", "w1", "work")
	otherWorkspaceWorkRaw := filepath.Join(raw, "workspaces", "w2", "work")

	mustMkdirAll(agentSelfRaw)
	mustWrite(filepath.Join(agentSelfRaw, "notes.txt"), "self notes")
	mustWrite(filepath.Join(agentOtherRaw, "SOUL.md"), "agent other soul")
	mustMkdirAll(workspaceWorkRaw)
	mustWrite(filepath.Join(otherWorkspaceWorkRaw, "file.txt"), "other workspace file")
	mustWrite(filepath.Join(raw, "master.key"), "key-material")
	mustWrite(filepath.Join(raw, "credentials.json"), `{"v":1}`)
	mustWrite(filepath.Join(raw, "config.json"), `{"sandbox":{"mode":"off"}}`)
	mustWrite(filepath.Join(raw, "cli.token"), "live-bearer-token")
	mustWrite(filepath.Join(raw, "entities", "agents", "self.json"), `{"tools":{}}`)

	resolve := func(p string) string {
		t.Helper()
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatalf("EvalSymlinks(%q): %v", p, err)
		}
		return r
	}

	return resolve(raw), resolve(agentSelfRaw), resolve(agentOtherRaw), resolve(workspaceWorkRaw), resolve(otherWorkspaceWorkRaw)
}

// TestBaseline_CarveOuts_AgentHomeRootedTurn captures dataset item 3 (spec
// §0, task item 3) for the FIRST of the two own-tree-exception shapes:
// WorkDir == <home>/agents/<self>. Exercised end to end through
// EffectiveFSPolicy (not a hand-built FSPolicy{}), for BOTH FSScopeConfined
// and FSScopeUnrestricted, because IsCarveOut's own-tree exception is
// deliberately SCOPE-INDEPENDENT (FR-017: carve-outs apply regardless of
// scope) — this table proves that today, not just asserts it in prose.
func TestBaseline_CarveOuts_AgentHomeRootedTurn(t *testing.T) {
	home, agentSelf, agentOther, _, otherWorkspaceWork := baselineHomeTree(t)

	rows := []struct {
		name string
		path string
		want bool
		why  string
	}{
		{
			"own home file", filepath.Join(agentSelf, "notes.txt"), false,
			"own-tree exception: WorkDir==agents/self is a proper descendant of the agents/ carve-out root, so the agent's own home is exempt (carveout.go's documented exception)",
		},
		{
			"own home root itself", agentSelf, false,
			"same own-tree exception applied to WorkDir itself, not just a file under it",
		},
		{
			"other agent's home file", filepath.Join(agentOther, "SOUL.md"), true,
			"own-tree exception is per-agent — it never extends to a SIBLING home under the same agents/ root",
		},
		{
			"master.key", filepath.Join(home, "master.key"), true,
			"FR-017 carve-out, unconditional",
		},
		{
			"credentials.json", filepath.Join(home, "credentials.json"), true,
			"FR-017 carve-out, unconditional",
		},
		{
			"config.json", filepath.Join(home, "config.json"), true,
			"ALREADY CLOSED as of commit 6e7aece0 (FR-3.1/3.2's merged secret set landed on this branch before the §0 baseline gate — see the file-level note above). " +
				"config.json is now IN buildCarveOuts (via SecretPaths' SecretEntriesAlways). Not a hole to record faithfully anymore; this is what is actually on disk right now.",
		},
		{
			"cli.token", filepath.Join(home, "cli.token"), true,
			"ALREADY CLOSED — same FR-3.1/3.2 commit as config.json above; see the file-level note.",
		},
		{
			"entities/ record, including own agent's own record", filepath.Join(home, "entities", "agents", "self.json"), true,
			"entities/ is its own carve-out root (ADR-054), and — unlike agents/ — has no own-tree exception carved into buildCarveOuts at all: WorkDir==agents/self is not a descendant of the entities/ root, so the exception's per-root scoping (BLOCK #2) never even reaches it",
		},
		{
			"another workspace's work dir", filepath.Join(otherWorkspaceWork, "file.txt"), true,
			"workspaces/ carve-out root; WorkDir here is agents/self, not a descendant of workspaces/, so no own-tree exception applies",
		},
	}

	for _, scope := range []FSScope{FSScopeConfined, FSScopeUnrestricted} {
		t.Run(string(scope), func(t *testing.T) {
			restrict := scope == FSScopeConfined
			policy, err := EffectiveFSPolicy(context.Background(), agentSelf, "", restrict, home, "self", "")
			if err != nil {
				t.Fatalf("EffectiveFSPolicy: %v", err)
			}
			if policy.Scope != scope {
				t.Fatalf("policy.Scope = %q, want %q", policy.Scope, scope)
			}

			for _, r := range rows {
				t.Run(r.name, func(t *testing.T) {
					got := IsCarveOut(r.path, policy)
					if got != r.want {
						t.Errorf("IsCarveOut(%q) = %v, want %v — %s", r.path, got, r.want, r.why)
					}
				})
			}
		})
	}
}

// TestBaseline_CarveOuts_ReRootedWorkspaceTurn captures the SECOND own-tree
// shape: WorkDir == <home>/workspaces/<id>/work. This is the shape spec
// FR-3.4 calls out as "the one a looser implementation gets wrong" — here the
// agent's own home under agents/self must NOT be exempted, because WorkDir is
// a descendant of the workspaces/ carve-out root, not the agents/ root. An
// implementation that exempts "anything the agent identifies as its own"
// rather than scoping the exception PER-ROOT would make the kernel MORE
// permissive than the app layer on every workspace turn (BLOCK #2 in
// carveout.go's doc comment) — this table is the regression net for that.
func TestBaseline_CarveOuts_ReRootedWorkspaceTurn(t *testing.T) {
	home, agentSelf, agentOther, workspaceWork, otherWorkspaceWork := baselineHomeTree(t)

	rows := []struct {
		name string
		path string
		want bool
		why  string
	}{
		{
			"own workspace work-dir file", filepath.Join(workspaceWork, "x.txt"), false,
			"WorkDir itself, or a descendant of it — never a carve-out of anything",
		},
		{
			"agent's OWN home (agents/self)", filepath.Join(agentSelf, "notes.txt"), true,
			"THE case FR-3.4 exists to pin: under a re-rooted workspace turn, the own-tree exception does NOT apply to agents/self, because WorkDir (workspaces/w1/work) is a descendant of the workspaces/ root, not the agents/ root. Getting this wrong makes the kernel MORE permissive than the app layer on every workspace turn.",
		},
		{
			"another agent's home", filepath.Join(agentOther, "SOUL.md"), true,
			"as unreachable as the agent's own home during a workspace turn — matches today's re-root behavior per carveout.go's doc comment",
		},
		{
			"master.key", filepath.Join(home, "master.key"), true,
			"FR-017 carve-out, unconditional",
		},
		{
			"credentials.json", filepath.Join(home, "credentials.json"), true,
			"FR-017 carve-out, unconditional",
		},
		{
			"config.json", filepath.Join(home, "config.json"), true,
			"ALREADY CLOSED (same as the agent-home-rooted case) — FR-3.1/3.2 landed via commit 6e7aece0 before this gate; see the file-level note at the top of this file.",
		},
		{
			"cli.token", filepath.Join(home, "cli.token"), true,
			"ALREADY CLOSED — same commit as config.json above.",
		},
		{
			"entities/ record", filepath.Join(home, "entities", "agents", "self.json"), true,
			"entities/ carve-out root, unconditional",
		},
		{
			"another workspace's work dir", filepath.Join(otherWorkspaceWork, "file.txt"), true,
			"a DIFFERENT workspace's work dir under the same workspaces/ root — own-tree exception only exempts THIS turn's own WorkDir, never a sibling",
		},
	}

	for _, scope := range []FSScope{FSScopeConfined, FSScopeUnrestricted} {
		t.Run(string(scope), func(t *testing.T) {
			restrict := scope == FSScopeConfined
			// turnWorkDir=workspaceWork models the re-rooted shape: the agent's
			// fixed home (agentSelf) is passed as agentHome (the fallback),
			// exactly as tools.ResolveTurnFSPolicy does, but turnWorkDir wins.
			policy, err := EffectiveFSPolicy(context.Background(), agentSelf, workspaceWork, restrict, home, "self", "w1")
			if err != nil {
				t.Fatalf("EffectiveFSPolicy: %v", err)
			}
			if policy.Scope != scope {
				t.Fatalf("policy.Scope = %q, want %q", policy.Scope, scope)
			}
			if policy.WorkDir != workspaceWork {
				t.Fatalf("policy.WorkDir = %q, want the re-rooted workspace work dir %q", policy.WorkDir, workspaceWork)
			}

			for _, r := range rows {
				t.Run(r.name, func(t *testing.T) {
					got := IsCarveOut(r.path, policy)
					if got != r.want {
						t.Errorf("IsCarveOut(%q) = %v, want %v — %s", r.path, got, r.want, r.why)
					}
				})
			}
		})
	}
}
