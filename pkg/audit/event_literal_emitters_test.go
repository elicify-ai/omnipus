// License: MIT
// Copyright (c) 2026 Omnipus contributors
package audit

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// bareLiteralEvent matches an Entry literal's Event field assigned a STRING
// LITERAL rather than an Event* constant, e.g. `Event: "memory.auto_recap"`.
var bareLiteralEvent = regexp.MustCompile(`\bEvent:\s*"([a-z][a-z0-9._]*)"`)

// TestEveryBareLiteralEventNameIsRegistered closes a gap the two existing
// event-name tests cannot see.
//
// events_exhaustive_test.go walks the Event* CONSTANTS, and
// event_name_contract_test.go reads IsValidEventName's switch. Neither scans
// EMITTERS, so an emitter that writes a bare string literal with no matching
// constant is invisible to both — it compiles, it passes every test, and the
// only symptom is a warn-once "unknown Event value" line at runtime.
//
// That is not hypothetical. `memory.auto_recap` (pkg/agent/session_end.go's
// auditRecap) shipped unregistered and stayed invisible for as long as audit
// logging defaulted to OFF: with a nil logger the emit path never ran, so the
// warning never fired. It surfaced on the first real CI run after audit became
// ON by default on 2026-09-11.
//
// DIES ON: adding `Event: "something.new"` anywhere under pkg/ without adding
// that name to IsValidEventName.
func TestEveryBareLiteralEventNameIsRegistered(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	pkgDir := filepath.Join(root, "pkg")
	if _, statErr := os.Stat(pkgDir); statErr != nil {
		t.Skipf("pkg/ not present at %s (%v) — nothing to scan", pkgDir, statErr)
	}

	// name -> the files that emit it, so a failure names where to look.
	found := map[string][]string{}

	walkErr := filepath.Walk(pkgDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Generated wire types never emit audit entries.
			if info.Name() == "generated" || info.Name() == "spa" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for _, m := range bareLiteralEvent.FindAllStringSubmatch(string(data), -1) {
			found[m[1]] = append(found[m[1]], rel)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk pkg/: %v", walkErr)
	}

	if len(found) == 0 {
		t.Fatal("scanned pkg/ and found ZERO bare-literal Event assignments — the regex has " +
			"stopped matching, so this test would pass against any unregistered emitter. " +
			"Fix the pattern rather than deleting the test.")
	}

	for name, files := range found {
		if !IsValidEventName(EventName(name)) {
			t.Errorf("audit event %q is emitted as a bare string literal in %s but "+
				"IsValidEventName rejects it, so every one of those entries trips the "+
				"warn-once \"unknown Event value\" path. Add it to IsValidEventName, or "+
				"give it an Event* constant and use that.",
				name, strings.Join(files, ", "))
		}
	}
}
