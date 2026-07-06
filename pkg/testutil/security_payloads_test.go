package testutil_test

// Tests for pkg/testutil/security_payloads.go
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F12 (testutil half) + F20
//
// BDD scenarios:
//   Given: the security payload functions (XSSPayloads, SQLInjectionPayloads, etc.)
//   When: the caller mutates the returned slice
//   Then: a subsequent call returns the original, unmodified payloads
//
// Immutability guarantee: each function returns a fresh copy of the backing
// slice. Mutations to the returned slice must not corrupt future calls.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/testutil"
)

// TestXSSPayloads_Immutability verifies that mutating the slice returned by
// XSSPayloads() does not affect a subsequent call.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F20 — immutability guarantee for XSSPayloads
func TestXSSPayloads_Immutability(t *testing.T) {
	first := testutil.XSSPayloads()
	require.NotEmpty(t, first, "XSSPayloads must return a non-empty slice")

	// Capture the original first element for later comparison.
	originalFirst := first[0]

	// Mutate the returned slice.
	first[0] = "mutated-xss"

	// A second call must return the original, unmodified first element.
	second := testutil.XSSPayloads()
	require.NotEmpty(t, second)
	assert.Equal(t, originalFirst, second[0],
		"XSSPayloads: mutation of returned slice must not affect subsequent calls; "+
			"got %q, want %q", second[0], originalFirst)
}

// TestXSSPayloads_ContentAndCount verifies the slice has the expected length
// and that two calls return distinct slice headers (different pointers).
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F20
func TestXSSPayloads_ContentAndCount(t *testing.T) {
	a := testutil.XSSPayloads()
	b := testutil.XSSPayloads()

	require.Equal(t, len(a), len(b), "Both calls must return the same length")
	require.Equal(t, 10, len(a), "XSSPayloads must contain exactly 10 entries")
	assert.Equal(t, a, b, "Both calls must return equal content")

	// Verify they are distinct slices (different backing arrays).
	a[0] = "differentiated"
	assert.NotEqual(t, a[0], b[0],
		"XSSPayloads must return independent slices; modifying one must not affect the other")
}

// TestSQLInjectionPayloads_Immutability verifies mutation safety.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F20 — SQLInjectionPayloads
func TestSQLInjectionPayloads_Immutability(t *testing.T) {
	first := testutil.SQLInjectionPayloads()
	require.NotEmpty(t, first)

	originalFirst := first[0]
	first[0] = "mutated-sql"

	second := testutil.SQLInjectionPayloads()
	require.NotEmpty(t, second)
	assert.Equal(t, originalFirst, second[0],
		"SQLInjectionPayloads: mutation of returned slice must not affect subsequent calls")
}

// TestSQLInjectionPayloads_Count verifies the expected element count.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F20
func TestSQLInjectionPayloads_Count(t *testing.T) {
	assert.Equal(t, 6, len(testutil.SQLInjectionPayloads()),
		"SQLInjectionPayloads must contain exactly 6 entries")
}

// TestPathTraversalPayloads_Immutability verifies mutation safety.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F20 — PathTraversalPayloads
func TestPathTraversalPayloads_Immutability(t *testing.T) {
	first := testutil.PathTraversalPayloads()
	require.NotEmpty(t, first)

	originalFirst := first[0]
	first[0] = "mutated-path"

	second := testutil.PathTraversalPayloads()
	require.NotEmpty(t, second)
	assert.Equal(t, originalFirst, second[0],
		"PathTraversalPayloads: mutation of returned slice must not affect subsequent calls")
}

// TestPathTraversalPayloads_Count verifies the expected element count.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F20
func TestPathTraversalPayloads_Count(t *testing.T) {
	assert.Equal(t, 10, len(testutil.PathTraversalPayloads()),
		"PathTraversalPayloads must contain exactly 10 entries")
}

// TestCommandInjectionPayloads_Immutability verifies mutation safety.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F20 — CommandInjectionPayloads
func TestCommandInjectionPayloads_Immutability(t *testing.T) {
	first := testutil.CommandInjectionPayloads()
	require.NotEmpty(t, first)

	originalFirst := first[0]
	first[0] = "mutated-cmd"

	second := testutil.CommandInjectionPayloads()
	require.NotEmpty(t, second)
	assert.Equal(t, originalFirst, second[0],
		"CommandInjectionPayloads: mutation of returned slice must not affect subsequent calls")
}

// TestCommandInjectionPayloads_Count verifies the expected element count.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F20
func TestCommandInjectionPayloads_Count(t *testing.T) {
	assert.Equal(t, 10, len(testutil.CommandInjectionPayloads()),
		"CommandInjectionPayloads must contain exactly 10 entries")
}

// TestPromptInjectionPayloads_Immutability verifies mutation safety.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F20 — PromptInjectionPayloads
func TestPromptInjectionPayloads_Immutability(t *testing.T) {
	first := testutil.PromptInjectionPayloads()
	require.NotEmpty(t, first)

	originalFirst := first[0]
	first[0] = "mutated-prompt"

	second := testutil.PromptInjectionPayloads()
	require.NotEmpty(t, second)
	assert.Equal(t, originalFirst, second[0],
		"PromptInjectionPayloads: mutation of returned slice must not affect subsequent calls")
}

// TestPromptInjectionPayloads_Count verifies the expected element count.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F20
func TestPromptInjectionPayloads_Count(t *testing.T) {
	assert.Equal(t, 10, len(testutil.PromptInjectionPayloads()),
		"PromptInjectionPayloads must contain exactly 10 entries")
}

// TestKnownSecretPrefixes_Immutability verifies mutation safety.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F20 — KnownSecretPrefixes
func TestKnownSecretPrefixes_Immutability(t *testing.T) {
	first := testutil.KnownSecretPrefixes()
	require.NotEmpty(t, first)

	originalFirst := first[0]
	first[0] = "mutated-secret"

	second := testutil.KnownSecretPrefixes()
	require.NotEmpty(t, second)
	assert.Equal(t, originalFirst, second[0],
		"KnownSecretPrefixes: mutation of returned slice must not affect subsequent calls")
}

// TestKnownSecretPrefixes_Count verifies the expected element count.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F20
func TestKnownSecretPrefixes_Count(t *testing.T) {
	assert.Equal(t, 8, len(testutil.KnownSecretPrefixes()),
		"KnownSecretPrefixes must contain exactly 8 entries")
}

// TestAllPayloads_DifferentInputsDifferentOutputs verifies that different
// payload categories return genuinely different content — a differentiation
// test to catch a hypothetical implementation that returns the same slice
// for every category.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F12 — differentiation test
func TestAllPayloads_DifferentInputsDifferentOutputs(t *testing.T) {
	xss := testutil.XSSPayloads()
	sql := testutil.SQLInjectionPayloads()
	path := testutil.PathTraversalPayloads()
	cmd := testutil.CommandInjectionPayloads()
	prompt := testutil.PromptInjectionPayloads()
	secrets := testutil.KnownSecretPrefixes()

	// The first element of each category must be unique across all categories.
	firsts := []string{xss[0], sql[0], path[0], cmd[0], prompt[0], secrets[0]}
	seen := make(map[string]bool)
	for _, v := range firsts {
		assert.False(t, seen[v],
			"payload categories must not share the same first element; duplicate: %q", v)
		seen[v] = true
	}
}
