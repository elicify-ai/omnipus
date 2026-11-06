// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package thirdpartycli

import (
	"fmt"
	"strings"
	"testing"
)

// r17 EX-01 and the machine-verifiable limits (CK-13): an env map of 64
// entries is accepted and 65 refused. The numbers are the specification's
// literals, deliberately not maxEnvEntries, so changing the constant cannot
// move the limit unnoticed.
func TestEnvOverrides_SpecLimitIs64Entries(t *testing.T) {
	envOf := func(n int) map[string]string {
		env := make(map[string]string, n)
		for i := 0; i < n; i++ {
			env[fmt.Sprintf("NAME_%d", i)] = "v"
		}
		return env
	}
	if err := ValidateEnvOverrides(envOf(64)); err != nil {
		t.Fatalf("64 entries must be accepted: %v", err)
	}
	const want = "env_overrides has 65 entries; at most 64 are allowed"
	if err := ValidateEnvOverrides(envOf(65)); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("65 entries: err = %v, want %q", err, want)
	}
	merged, _, err := MergeEnvOverrides(nil, envOf(64), nil)
	if err != nil || len(merged) != 64 {
		t.Fatalf("a 64-entry set must merge: %d entries, err = %v", len(merged), err)
	}
	if _, _, err := MergeEnvOverrides(nil, envOf(65), nil); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("a 65-entry set: err = %v, want %q", err, want)
	}
}
