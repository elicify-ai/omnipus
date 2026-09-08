// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/filegrep"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// TestGrepGuard_CarveOutIdentityComesFromTheCarveOutsThemselves proves the
// EXISTENCE half of the carve-out cannot be separated from the DENY half by
// disagreeing about where $OMNIPUS_HOME is.
//
// carveOutFS answers two different questions about a directory, and until this
// test they consulted two different sources:
//
//	denied()             — judges against policy.CarveOuts, whatever root
//	                       those were built from.
//	holdsCarveOutRoots() — decided whether to run the exhaustive per-entry
//	                       ReadDir filter, and judged against a home path
//	                       sourced separately (config.OmnipusHomeDir()).
//
// When the two roots differ, the exhaustive filter never runs where the
// carve-out roots actually live, so master.key / credentials.json / cli.token
// / auth.json become listable and NAME-matchable. Open() still refuses their
// CONTENT, so the failure is silent to any content-leak assertion — which is
// why this test asserts on the NAME.
//
// The divergence is produced here the way production could produce it: through
// grepRoots, with a policy whose CarveOuts were built from the REAL home while
// the process's config.OmnipusHomeDir() reports a different directory (a
// re-rooted policy, a test-constructed one, a future multi-home shape). Only
// the carve-out roots the deny check itself consults may decide this.
func TestGrepGuard_CarveOutIdentityComesFromTheCarveOutsThemselves(t *testing.T) {
	parent := t.TempDir()
	realHome := filepath.Join(parent, "omnipus")
	if err := os.MkdirAll(realHome, 0o700); err != nil {
		t.Fatalf("seed real home: %v", err)
	}
	for name, body := range map[string]string{
		"master.key":       "deadbeefIDENTITYMASTERKEY\n",
		"credentials.json": "{\"identity\":\"credential\"}\n",
		"cli.token":        "sk-IDENTITY-CLITOKEN\n",
		"auth.json":        "{\"identity\":\"auth\"}\n",
	} {
		if err := os.WriteFile(filepath.Join(realHome, name), []byte(body), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	// An ordinary file beside the home directory: the control proving the
	// guard still searches everything that is not a secret.
	if err := os.WriteFile(filepath.Join(parent, "ordinary-master.key.notes.txt"),
		[]byte("a file whose name merely mentions the key\n"), 0o600); err != nil {
		t.Fatalf("seed control file: %v", err)
	}

	// The process believes $OMNIPUS_HOME is somewhere else entirely. This is
	// the ONLY thing that differs from an ordinary run.
	decoyHome := t.TempDir()
	t.Setenv(config.EnvHome, decoyHome)

	// A policy whose CarveOuts are anchored on the REAL home, and whose
	// WorkDir is that home's parent — the search root that contains them.
	// Constructed directly, exactly as the finding describes ("a re-rooted
	// policy, a test-constructed one"): every field is the one the deny check
	// will actually consult.
	policy := fspolicy.FSPolicy{
		WorkDir:   parent,
		Scope:     fspolicy.FSScopeConfined,
		CarveOuts: fspolicy.SecretPaths(realHome),
	}

	tool := NewGrepTool(parent, true)
	roots, closeRoots, err := tool.grepRoots(context.Background(), policy, "")
	if err != nil {
		t.Fatalf("build grep roots: %v", err)
	}
	defer closeRoots()

	res, err := filegrep.Search(context.Background(), roots, filegrep.Options{
		Query:         "master.key",
		IncludeHidden: true,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	paths := make([]string, 0, len(res.Hits))
	for _, h := range res.Hits {
		paths = append(paths, h.Path)
	}
	joined := strings.Join(paths, "\n")
	if strings.Contains(joined, "omnipus/master.key") {
		t.Errorf("the carve-out root's own directory was listed unfiltered — "+
			"master.key's existence and exact path disclosed:\n%s", joined)
	}
	if !strings.Contains(joined, "ordinary-master.key.notes.txt") {
		t.Errorf("the guard must suppress the secret set, not the search: "+
			"the ordinary control file went missing too:\n%s", joined)
	}

	// The other secrets are name-matchable through the same unfiltered
	// listing; assert each one separately so a partial fix cannot pass.
	for _, secret := range []string{"credentials.json", "cli.token", "auth.json"} {
		res, err := filegrep.Search(context.Background(), roots, filegrep.Options{
			Query:         secret,
			IncludeHidden: true,
		})
		if err != nil {
			t.Fatalf("search %s: %v", secret, err)
		}
		for _, h := range res.Hits {
			if strings.Contains(h.Path, "omnipus/"+secret) {
				t.Errorf("carved-out file %q was name-matchable at %q", secret, h.Path)
			}
		}
	}
}
