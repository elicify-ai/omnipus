// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// seedCarveOutFixture builds the one installation shape in which a mount can
// legitimately place $OMNIPUS_HOME inside a searchable root: a NON-dotted
// $OMNIPUS_HOME (a first-class supported spelling — OMNIPUS_HOME is an env
// var, and /srv/omnipus is as valid as ~/.omnipus) with a workspace mount on
// its PARENT directory. workspace.CheckMountTarget warns and allows exactly
// that shape — it hard-refuses only a target that IS or lies INSIDE
// $OMNIPUS_HOME — and its warning text promises "the installation's own
// secrets remain protected independently of this mount". This fixture is what
// holds that promise to grep.
//
// Returns the agent's work dir, the mount target (the parent), and the home.
func seedCarveOutFixture(t *testing.T) (work, parent, home string) {
	t.Helper()

	parent = t.TempDir()
	home = filepath.Join(parent, "omnipus")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("seed home: %v", err)
	}
	t.Setenv(config.EnvHome, home)

	const wsID, agentID = "ws-carve", "agent-carve"
	work = seedGrepWorkspace(t, home, wsID, agentID)

	// The secret set, seeded with content AND names a search can match.
	// Every one of these is refused to read_file by fspolicy.IsCarveOut.
	for path, body := range map[string]string{
		filepath.Join(home, "credentials.json"):        "{\"api_key\":\"sk-CARVEOUT-CREDENTIAL\"}\n",
		filepath.Join(home, "master.key"):              "deadbeefCARVEOUTMASTERKEY\n",
		filepath.Join(home, "config.json"):             "{\"api_key\":\"sk-CARVEOUT-CONFIG\"}\n",
		filepath.Join(home, "cli.token"):               "sk-CARVEOUT-CLITOKEN\n",
		filepath.Join(home, "config.json.bak-1700000"): "{\"api_key\":\"sk-CARVEOUT-BACKUP\"}\n",
		filepath.Join(home, "auth.json"):               "{\"api_key\":\"sk-CARVEOUT-AUTHJSON\"}\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}
	for dir, name := range map[string]string{
		filepath.Join(home, "system"):          "audit.jsonl",
		filepath.Join(home, "entities/agents"): "mia.json",
		filepath.Join(home, "backups"):         "backup.json",
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("seed dir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name),
			[]byte("{\"api_key\":\"sk-CARVEOUT-"+strings.ToUpper(filepath.Base(dir))+"\"}\n"), 0o600); err != nil {
			t.Fatalf("seed %s/%s: %v", dir, name, err)
		}
	}

	// A sibling that is NOT a secret: the control proving the fix suppresses
	// the secret set specifically, not the mount.
	project := filepath.Join(parent, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatalf("seed project dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "notes.txt"),
		[]byte("{\"api_key\":\"sk-ORDINARY-PROJECT-FILE\"}\n"), 0o600); err != nil {
		t.Fatalf("seed project notes: %v", err)
	}

	if _, warn, err := workspace.CreateMount(home, wsID, "host", parent); err != nil {
		t.Fatalf("create mount on $OMNIPUS_HOME's parent: %v", err)
	} else if warn == "" {
		t.Fatalf("mounting $OMNIPUS_HOME's parent must warn (FR-7.6); got no warning")
	}

	return work, parent, home
}

func newCarveOutGrepTool(t *testing.T, work string) (*GrepTool, context.Context) {
	t.Helper()
	return NewGrepTool(work, true), WithTurnWorkspaceDir(WithAgentID(context.Background(), "agent-carve"), work)
}

// TestGrepTool_SecretCarveOutIsHonoured is the security contract: grep must
// refuse the SAME secret set every other filesystem-reading tool refuses
// (fspolicy.IsCarveOut, the predicate pkg/tools/resolvepath.go's ResolvePath
// consults before it looks at a mount at all). Both halves of a hit are
// covered: an excerpt discloses the secret's CONTENT, and a name hit
// discloses its EXISTENCE and exact path.
func TestGrepTool_SecretCarveOutIsHonoured(t *testing.T) {
	work, parent, home := seedCarveOutFixture(t)
	tool, ctx := newCarveOutGrepTool(t, work)

	t.Run("content of every carved-out file stays unreachable", func(t *testing.T) {
		res := tool.Execute(ctx, map[string]any{"pattern": "api_key"})
		if res.IsError {
			t.Fatalf("grep must still run against the mount, got error: %s", res.ForLLM)
		}
		for _, leak := range []string{
			"sk-CARVEOUT-CREDENTIAL", "sk-CARVEOUT-CONFIG", "sk-CARVEOUT-CLITOKEN",
			"sk-CARVEOUT-BACKUP", "sk-CARVEOUT-AUTHJSON",
			"sk-CARVEOUT-SYSTEM", "sk-CARVEOUT-AGENTS", "sk-CARVEOUT-BACKUPS",
		} {
			if strings.Contains(res.ForLLM, leak) {
				t.Errorf("grep disclosed carved-out secret content %q:\n%s", leak, res.ForLLM)
			}
		}
		for _, p := range []string{
			"omnipus/credentials.json", "omnipus/config.json", "omnipus/cli.token",
			"omnipus/auth.json", "omnipus/system/", "omnipus/entities/", "omnipus/backups/",
		} {
			if strings.Contains(res.ForLLM, p) {
				t.Errorf("grep disclosed a carved-out path %q:\n%s", p, res.ForLLM)
			}
		}
	})

	t.Run("a NAME match on a carved-out file is suppressed too", func(t *testing.T) {
		res := tool.Execute(ctx, map[string]any{"pattern": "master.key"})
		if res.IsError {
			t.Fatalf("grep must still run, got error: %s", res.ForLLM)
		}
		if strings.Contains(res.ForLLM, "omnipus/master.key") {
			t.Errorf("grep disclosed the existence of the master key file:\n%s", res.ForLLM)
		}
	})

	t.Run("an ordinary file under the same mount is still searchable", func(t *testing.T) {
		res := tool.Execute(ctx, map[string]any{"pattern": "sk-ORDINARY-PROJECT-FILE"})
		if res.IsError {
			t.Fatalf("grep must still search the non-secret part of the mount: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "host/project/notes.txt:1:") {
			t.Fatalf("the carve-out must suppress the secret set, not the mount:\n%s", res.ForLLM)
		}
	})

	t.Run("scoping directly at a carved-out directory finds nothing", func(t *testing.T) {
		res := tool.Execute(ctx, map[string]any{"pattern": "api_key", "path": "host/omnipus"})
		if res.IsError {
			t.Logf("refused with an error (acceptable): %s", res.ForLLM)
			return
		}
		for _, leak := range []string{"sk-CARVEOUT-CREDENTIAL", "sk-CARVEOUT-CONFIG", "sk-CARVEOUT-SYSTEM"} {
			if strings.Contains(res.ForLLM, leak) {
				t.Errorf("a `path` aimed at $OMNIPUS_HOME disclosed %q:\n%s", leak, res.ForLLM)
			}
		}
	})

	t.Run("a hard link aliasing a carved-out directory discloses no content", func(t *testing.T) {
		// The alias leg of fspolicy.IsCarveOut, which is path-independent: the
		// link lives in the agent's OWN tree at a name the agent chose, so no
		// path rule can refuse it — only its inode identifies it. read_file
		// refuses it; grep must not serve its content either.
		alias := filepath.Join(work, "harmless-notes.txt")
		if err := os.Link(filepath.Join(home, "entities", "agents", "mia.json"), alias); err != nil {
			t.Skipf("cannot hard link on this filesystem: %v", err)
		}
		policy, err := ResolveTurnFSPolicy(ctx, work, true)
		if err != nil {
			t.Fatalf("resolve turn policy: %v", err)
		}
		if _, rErr := ResolvePath(ctx, policy, "read_file", "", FSOpRead, alias); !errors.Is(rErr, ErrCarveOut) {
			t.Fatalf("fixture drift: read_file must refuse the alias with ErrCarveOut, got %v", rErr)
		}
		res := tool.Execute(ctx, map[string]any{"pattern": "sk-CARVEOUT-AGENTS"})
		if res.IsError {
			t.Fatalf("grep must still run: %s", res.ForLLM)
		}
		// Asserted on the HIT line, not on the pattern echo at the top of
		// every rendering — which repeats the query verbatim and would make a
		// bare substring check pass for the wrong reason.
		if strings.Contains(res.ForLLM, "harmless-notes.txt:") {
			t.Fatalf("grep served the content of a hard-linked carve-out:\n%s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "0 match(es)") {
			t.Fatalf("expected no hits at all for the aliased content, got:\n%s", res.ForLLM)
		}
	})

	t.Run("grep and the canonical resolver agree on every seeded path", func(t *testing.T) {
		// The claim under test is PARITY, so it is asserted against the
		// resolver itself rather than against a second hand-written list:
		// every path grep refuses above is a path ResolvePath refuses with
		// ErrCarveOut on the very same turn, and the control file is one it
		// resolves cleanly.
		policy, err := ResolveTurnFSPolicy(ctx, work, true)
		if err != nil {
			t.Fatalf("resolve turn policy: %v", err)
		}
		for _, rel := range []string{
			"credentials.json", "master.key", "config.json", "cli.token",
			"auth.json", "config.json.bak-1700000",
			"system/audit.jsonl", "entities/agents/mia.json", "backups/backup.json",
		} {
			abs := filepath.Join(home, rel)
			if _, rErr := ResolvePath(ctx, policy, "read_file", "", FSOpRead, abs); !errors.Is(rErr, ErrCarveOut) {
				t.Errorf("fixture drift: read_file does not refuse %s with ErrCarveOut (got %v) — "+
					"grep's suppression of it would no longer be parity", rel, rErr)
			}
		}
		if _, rErr := ResolvePath(ctx, policy, "read_file", "",
			FSOpRead, filepath.Join(parent, "project", "notes.txt")); rErr != nil {
			t.Errorf("the control file must be readable by read_file, got: %v", rErr)
		}
	})

	t.Run("the agent's own work tree is not carved out by the agents/workspaces roots", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(work, "own.txt"), []byte("sk-OWN-WORKTREE-FILE\n"), 0o600); err != nil {
			t.Fatalf("seed own file: %v", err)
		}
		res := tool.Execute(ctx, map[string]any{"pattern": "sk-OWN-WORKTREE-FILE"})
		if res.IsError {
			t.Fatalf("grep must search the agent's own work tree: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "own.txt:1:") {
			t.Fatalf("the own-tree exception must keep the agent's own files searchable:\n%s", res.ForLLM)
		}
	})
}

// TestGrepCarveOutFastPathPremise pins the structural fact carveOutFS's
// ReadDir fast path rests on: every carve-out root is a DIRECT CHILD of
// $OMNIPUS_HOME. That is what makes "only $OMNIPUS_HOME's own listing needs
// the exhaustive per-entry check" sound — a carve-out root nested deeper
// would become reachable by name through a directory the fast path waves
// through.
//
// If fspolicy ever grows a nested carve-out root, this fails here rather
// than silently widening what grep will name.
func TestGrepCarveOutFastPathPremise(t *testing.T) {
	work, _, home := seedCarveOutFixture(t)
	_, ctx := newCarveOutGrepTool(t, work)

	policy, err := ResolveTurnFSPolicy(ctx, work, true)
	if err != nil {
		t.Fatalf("resolve turn policy: %v", err)
	}
	if len(policy.CarveOuts) == 0 {
		t.Fatal("expected a non-empty carve-out set for a turn under $OMNIPUS_HOME")
	}
	// Compared against the RESOLVED home: fspolicy builds its carve-out roots
	// from the realpath'd $OMNIPUS_HOME, and on macOS a temp dir under /var is
	// really /private/var — the same reason carveOutFS compares directories by
	// os.SameFile rather than by bytes.
	cleanHome, err := resolveRealpathUnderWorkDir(home, "")
	if err != nil {
		t.Fatalf("resolve $OMNIPUS_HOME: %v", err)
	}
	for _, root := range policy.CarveOuts {
		if parent := filepath.Dir(filepath.Clean(root)); parent != cleanHome {
			t.Errorf("carve-out root %q is not a direct child of $OMNIPUS_HOME (%q) — "+
				"carveOutFS's ReadDir fast path only runs the exhaustive check on $OMNIPUS_HOME "+
				"itself and would no longer suppress this root by name", root, cleanHome)
		}
	}
}
