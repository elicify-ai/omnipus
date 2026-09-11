// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — ADR-084 JUDGE-FR-060 / FR-060b: read confinement, and the
// engine-set turn fact that switches it on.
//
// What these tests are for. Before ADR-084 a verifier turn could read every
// transcript in the install: ADR-063 FR-2.2 made FSOpRead, FSOpList and
// FSOpSend open outside the secret set, and $OMNIPUS_HOME/sessions/, tasks/,
// plans/ and memory/ are in NEITHER secret set. That made the verifier's own
// target-session lock (VerifierSessionScopeAllows) bypassable by opening the
// transcript file instead of calling inspect_session.
//
// The oracle for every assertion below is the specification, not the code:
// JUDGE-FR-060 ("a mechanism that actually confines — and it confines three
// operations, not one"), JUDGE-FR-060b's polarity rule ("unset means NOT
// confined"), and the FR's own "what does NOT change" clause, which requires
// the unconfined posture to be asserted as explicitly as the confined one.
//
// Each test exercises BOTH postures on the same fixture. A confinement test
// that only checks the refusal cannot distinguish "confines correctly" from
// "refuses everything", and a mechanism that refused every read would pass it.
package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// readConfinedTree is a minimal $OMNIPUS_HOME with one agent home (the work
// dir), one state directory outside it that the Judge must not reach
// (sessions/, chosen because it is the concrete hole JUDGE-FR-060 cites and
// is in neither secret set), and one unrelated directory off the home
// entirely.
type readConfinedTree struct {
	home         string // realpath'd $OMNIPUS_HOME
	workDir      string // home/agents/judge — the turn's effective work dir
	insideFile   string // workDir/artifact.txt — reachable in both postures
	sessionsFile string // home/sessions/other/transcript.jsonl — the target
	outsideFile  string // a file with no relation to home at all
}

func newReadConfinedTree(t *testing.T) readConfinedTree {
	t.Helper()

	raw := t.TempDir()
	outsideRaw := t.TempDir()

	write := func(p, content string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir parent of %q: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %q: %v", p, err)
		}
		return p
	}

	workDir := filepath.Join(raw, "agents", "judge")
	inside := write(filepath.Join(workDir, "artifact.txt"), "the work under review")
	sessions := write(filepath.Join(raw, "sessions", "other", "transcript.jsonl"), `{"role":"user"}`)
	outside := write(filepath.Join(outsideRaw, "unrelated.txt"), "unrelated content")

	resolve := func(p string) string {
		t.Helper()
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatalf("EvalSymlinks(%q): %v", p, err)
		}
		return r
	}

	return readConfinedTree{
		home:         resolve(raw),
		workDir:      resolve(workDir),
		insideFile:   resolve(inside),
		sessionsFile: resolve(sessions),
		outsideFile:  resolve(outside),
	}
}

// readConfinedPolicy builds the policy through the real production
// constructor — never a hand-built FSPolicy{} literal — so these tests
// exercise the same function ResolveTurnFSPolicy calls.
func readConfinedPolicy(t *testing.T, tr readConfinedTree, confined bool) fspolicy.FSPolicy {
	t.Helper()
	p, err := fspolicy.EffectiveFSPolicyWithReadConfined(
		context.Background(), tr.workDir, "", true, tr.home, "judge", "", confined,
	)
	if err != nil {
		t.Fatalf("EffectiveFSPolicyWithReadConfined(confined=%v): %v", confined, err)
	}
	if p.ReadConfined != confined {
		t.Fatalf("policy.ReadConfined = %v, want %v — the explicit parameter was not honoured", p.ReadConfined, confined)
	}
	return p
}

// TestResolvePath_ReadConfinedRefusesOutsideWorkdir is JUDGE-FR-060's
// headline oracle: FSOpRead outside the work dir with ReadConfined=true
// returns ErrOutsideScope; with ReadConfined=false it returns a handle,
// unchanged from ADR-063 FR-2.2.
//
// The in-work-dir rows are not padding. They are what separates "confines"
// from "refuses everything" — delete the ReadConfined branch in ResolvePath
// and the refusal rows go red; replace it with an unconditional refusal and
// the in-work-dir rows go red instead.
func TestResolvePath_ReadConfinedRefusesOutsideWorkdir(t *testing.T) {
	tr := newReadConfinedTree(t)
	ctx := context.Background()

	cases := []struct {
		name       string
		confined   bool
		path       string
		wantRefuse bool
	}{
		{
			name:       "confined turn cannot read another session's transcript",
			confined:   true,
			path:       tr.sessionsFile,
			wantRefuse: true,
		},
		{
			name:       "confined turn cannot read a file unrelated to the install",
			confined:   true,
			path:       tr.outsideFile,
			wantRefuse: true,
		},
		{
			name:       "confined turn CAN still read inside its own work dir",
			confined:   true,
			path:       tr.insideFile,
			wantRefuse: false,
		},
		{
			name:       "unconfined turn reads another session's transcript exactly as ADR-063 FR-2.2 allows",
			confined:   false,
			path:       tr.sessionsFile,
			wantRefuse: false,
		},
		{
			name:       "unconfined turn reads an unrelated file exactly as ADR-063 FR-2.2 allows",
			confined:   false,
			path:       tr.outsideFile,
			wantRefuse: false,
		},
		{
			name:       "unconfined turn reads inside its own work dir",
			confined:   false,
			path:       tr.insideFile,
			wantRefuse: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := readConfinedPolicy(t, tr, tc.confined)

			handle, err := ResolvePath(ctx, policy, "read_file", "", FSOpRead, tc.path)
			if tc.wantRefuse {
				if err == nil {
					handle.Close()
					t.Fatalf("ResolvePath(%q) succeeded; JUDGE-FR-060 requires a refusal for a read-confined turn", tc.path)
				}
				if !errors.Is(err, ErrOutsideScope) {
					t.Fatalf("ResolvePath(%q) error = %v; JUDGE-FR-060 requires ErrOutsideScope", tc.path, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolvePath(%q) refused with %v; this posture must reach the path", tc.path, err)
			}
			defer handle.Close()

			// Effective reach, not a field value: prove the handle actually
			// reads the real bytes. A handle that resolved but could not
			// read would otherwise pass as "allowed".
			data, readErr := handle.ReadFile()
			if readErr != nil {
				t.Fatalf("handle.ReadFile() for %q: %v", tc.path, readErr)
			}
			if len(data) == 0 {
				t.Fatalf("handle.ReadFile() for %q returned no bytes", tc.path)
			}
		})
	}
}

// TestResolvePath_ReadConfinedRefusesListAndSendToo covers the other two
// operations in ResolvePath's escape branch (JUDGE-FR-060, "all three ops in
// that branch, deliberately").
//
// Why it matters concretely: the Judge is granted list_directory. A
// read-only flag would leave `list_directory $OMNIPUS_HOME/sessions/` wide
// open and close only half the hole the FR exists to close. FSOpSend is the
// third because send_file resolves through the identical branch.
func TestResolvePath_ReadConfinedRefusesListAndSendToo(t *testing.T) {
	tr := newReadConfinedTree(t)
	ctx := context.Background()
	sessionsDir := filepath.Dir(tr.sessionsFile)

	ops := []struct {
		name string
		op   FSOp
		path string
		tool string
	}{
		{name: "list_directory on the install's sessions dir", op: FSOpList, path: sessionsDir, tool: "list_directory"},
		{name: "send_file on another session's transcript", op: FSOpSend, path: tr.sessionsFile, tool: "send_file"},
	}

	for _, o := range ops {
		t.Run(o.name+" is refused when confined", func(t *testing.T) {
			policy := readConfinedPolicy(t, tr, true)
			handle, err := ResolvePath(ctx, policy, o.tool, "", o.op, o.path)
			if err == nil {
				handle.Close()
				t.Fatalf("ResolvePath(op=%s, %q) succeeded; JUDGE-FR-060 confines all three of read/list/send", o.op, o.path)
			}
			if !errors.Is(err, ErrOutsideScope) {
				t.Fatalf("ResolvePath(op=%s, %q) error = %v; want ErrOutsideScope", o.op, o.path, err)
			}
		})

		t.Run(o.name+" is allowed when unconfined", func(t *testing.T) {
			policy := readConfinedPolicy(t, tr, false)
			handle, err := ResolvePath(ctx, policy, o.tool, "", o.op, o.path)
			if err != nil {
				t.Fatalf("ResolvePath(op=%s, %q) refused with %v; an unconfined turn keeps ADR-063 FR-2.2's open reach", o.op, o.path, err)
			}
			handle.Close()
		})
	}

	// The confined posture must not break the ops inside the work dir —
	// otherwise "confines list and send" would be indistinguishable from
	// "breaks list and send".
	t.Run("confined turn can still list its own work dir", func(t *testing.T) {
		policy := readConfinedPolicy(t, tr, true)
		handle, err := ResolvePath(ctx, policy, "list_directory", "", FSOpList, tr.workDir)
		if err != nil {
			t.Fatalf("ResolvePath(FSOpList, own work dir) refused with %v", err)
		}
		defer handle.Close()
		entries, readErr := handle.ReadDir()
		if readErr != nil {
			t.Fatalf("handle.ReadDir(): %v", readErr)
		}
		if len(entries) == 0 {
			t.Fatal("listing the own work dir returned no entries; the fixture writes one file into it")
		}
	})
}

// TestWithReadConfined_CtxSeamSetByDispatchNotByTool pins JUDGE-FR-060b's
// mechanism: the posture is an engine-set turn fact that ResolveTurnFSPolicy
// reads and hands to fspolicy as an EXPLICIT argument.
//
// The three properties it asserts, and why each is load-bearing:
//
//   - Polarity. An unset context yields ReadConfined=false. The opposite
//     polarity would silently confine every agent in the product the first
//     time a context was built without the flag.
//   - The seam is honoured. WithReadConfined(ctx, true) reaches the policy
//     ResolveTurnFSPolicy returns. Without this, FR-060's mechanism has no
//     input at all and the flag is inert — which is exactly what revision 2
//     of the FR shipped.
//   - It is NOT smuggled through ctx. fspolicy.EffectiveFSPolicy discards
//     the ctx it is handed (`_ = ctx`), so a confined context must NOT
//     produce a confined policy through that door. This is what makes the
//     explicit parameter the only way in, and it is the assertion that
//     would catch someone "simplifying" the plumbing by reading the ctx
//     inside fspolicy.
func TestWithReadConfined_CtxSeamSetByDispatchNotByTool(t *testing.T) {
	tr := newReadConfinedTree(t)
	t.Setenv("OMNIPUS_HOME", tr.home)

	t.Run("unset context is NOT confined", func(t *testing.T) {
		if ReadConfined(context.Background()) {
			t.Fatal("ReadConfined(background) = true; JUDGE-FR-060b requires unset to mean NOT confined")
		}
		policy, err := ResolveTurnFSPolicy(context.Background(), tr.workDir, true)
		if err != nil {
			t.Fatalf("ResolveTurnFSPolicy: %v", err)
		}
		if policy.ReadConfined {
			t.Fatal("ResolveTurnFSPolicy on an unset context produced ReadConfined=true")
		}
	})

	t.Run("explicit false is NOT confined", func(t *testing.T) {
		ctx := WithReadConfined(context.Background(), false)
		if ReadConfined(ctx) {
			t.Fatal("ReadConfined(WithReadConfined(ctx,false)) = true")
		}
		policy, err := ResolveTurnFSPolicy(ctx, tr.workDir, true)
		if err != nil {
			t.Fatalf("ResolveTurnFSPolicy: %v", err)
		}
		if policy.ReadConfined {
			t.Fatal("an explicit false produced ReadConfined=true")
		}
	})

	t.Run("engine-set true reaches the policy", func(t *testing.T) {
		ctx := WithReadConfined(context.Background(), true)
		if !ReadConfined(ctx) {
			t.Fatal("ReadConfined(WithReadConfined(ctx,true)) = false")
		}
		policy, err := ResolveTurnFSPolicy(ctx, tr.workDir, true)
		if err != nil {
			t.Fatalf("ResolveTurnFSPolicy: %v", err)
		}
		if !policy.ReadConfined {
			t.Fatal("ResolveTurnFSPolicy did not carry the engine-set posture onto the policy — FR-060's mechanism has no input")
		}

		// Effective reach, not a field value (JUDGE-FR-061's standard): the
		// policy this seam produced must actually refuse the read.
		if _, resolveErr := ResolvePath(ctx, policy, "read_file", "", FSOpRead, tr.sessionsFile); !errors.Is(resolveErr, ErrOutsideScope) {
			t.Fatalf("a policy built through the ctx seam did not refuse an out-of-workdir read: %v", resolveErr)
		}
	})

	t.Run("the posture does not travel through the ctx fspolicy discards", func(t *testing.T) {
		ctx := WithReadConfined(context.Background(), true)
		policy, err := fspolicy.EffectiveFSPolicy(ctx, tr.workDir, "", true, tr.home, "judge", "")
		if err != nil {
			t.Fatalf("EffectiveFSPolicy: %v", err)
		}
		if policy.ReadConfined {
			t.Fatal("fspolicy.EffectiveFSPolicy read the posture off ctx; JUDGE-FR-060b requires an explicit parameter so every call site is forced to consider it")
		}
	})
}

// TestResolvePath_ReadConfinedDoesNotReopenCarveOuts pins the interaction
// nobody should have to rediscover: read confinement is strictly additive to
// FR-017's carve-out check, which runs unconditionally and earlier. A
// confined policy can never make a previously-denied path reachable, and an
// unconfined one is still refused the secret set.
func TestResolvePath_ReadConfinedDoesNotReopenCarveOuts(t *testing.T) {
	tr := newReadConfinedTree(t)
	masterKey := filepath.Join(tr.home, "master.key")
	if err := os.WriteFile(masterKey, []byte("key-material"), 0o600); err != nil {
		t.Fatalf("write master.key: %v", err)
	}

	for _, confined := range []bool{false, true} {
		policy := readConfinedPolicy(t, tr, confined)
		_, err := ResolvePath(context.Background(), policy, "read_file", "", FSOpRead, masterKey)
		if err == nil {
			t.Fatalf("confined=%v: master.key was reachable; FR-017 denies it in every posture", confined)
		}
		if !confined && !errors.Is(err, ErrCarveOut) {
			t.Fatalf("confined=false: master.key refused with %v, want ErrCarveOut (the carve-out check, not the confinement branch)", err)
		}
	}
}
