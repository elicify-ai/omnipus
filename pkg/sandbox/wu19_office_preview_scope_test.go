package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// W3 / M10a / M10b: unrelated declarations must not invalidate a shared gate.
// Only parsed in-memory fixtures change; no production source is edited.
func TestWU19_OfficePreviewPinScope(t *testing.T) {
	base := wu19LoadInventory(t, filepath.Join("..", ".."))
	files := []string{
		"cmd/omnipus/main.go", "pkg/app/app.go", "pkg/preview/programs.go",
		"pkg/preview/confine.go", "pkg/preview/confine_unix.go",
		"pkg/preview/confine_darwin.go", "pkg/preview/confine_linux.go", "pkg/preview/confine_windows.go",
		"pkg/sandbox/backend_darwin_seatbelt.go", "pkg/sandbox/hardened_exec_held.go",
	}
	for _, name := range files {
		t.Run("unrelated_function/"+name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(name)))
			require.NoError(t, err)
			p := wu19OfficeFixture(t, base, name, string(raw), string(raw)+"\nfunc w3UnrelatedFunction() string { return \"unrelated\" }\n")
			require.Empty(t, p.officePreviewClosedEnvironment(), "an unrelated declaration must not invalidate the Office boundary")
			if name == "pkg/app/app.go" {
				count, findings := p.scan()
				require.Equal(t, 29, count, "the unrelated-function control retains the inventoried spawn sites")
				require.Empty(t, findings, "M10b control: the shared spawn inventory must stay green")
			}
		})
	}
	t.Run("comment_only_dispatch", func(t *testing.T) {
		p := wu19OfficeFixture(t, base, "pkg/app/app.go", "func Main() {", "func Main() { // unrelated comment\n")
		require.Empty(t, p.officePreviewClosedEnvironment(), "M10a: comments are not environment-boundary changes")
	})
	for _, tc := range []struct{ name, file, old, replacement, declaration string }{
		{"hidden_dispatch", "pkg/app/app.go", "return launcher.Main(args[2:]), true", `return launcher.Main(append(args[2:], "changed")), true`, "runHiddenMode"},
		{"main_entry", "cmd/omnipus/main.go", "app.Main()", "app.Main(); app.Main()", "main"},
		{"seatbelt_wrapper", "pkg/sandbox/backend_darwin_seatbelt.go", "cmd.Path = seatbeltExecPath", `cmd.Path = seatbeltExecPath; cmd.Env = append(cmd.Env, "W3_EXTRA=x")`, "SeatbeltBackend.wrapUnderSandboxExec"},
	} {
		t.Run("pinned_declaration/"+tc.name, func(t *testing.T) {
			p := wu19OfficeFixture(t, base, tc.file, tc.old, tc.replacement)
			location, err := filepath.Abs(filepath.Join("..", "..", filepath.FromSlash(tc.file)))
			require.NoError(t, err)
			want := "reviewed declaration " + location + "::" + tc.declaration + " changed"
			require.Contains(t, p.officePreviewClosedEnvironment(), want, "a pinned declaration must still invalidate the exception by name")
		})
	}
}

// The fixed vector was derived from an explicit token list and a separate
// Python SHA-256/uint32-big-endian encoder, not observed Go formatter output.
func TestWU19_OfficePreviewStableDigest(t *testing.T) {
	const source = `func pinned() { sink("fixed") }`
	const golden = "afea129d78093cc0d666f6a72f4f6ca32a10ea061d3b0b11ba0d9deec5da4a89"
	for _, tc := range []struct{ name, source string }{
		{"fixed_vector", source},
		{"spacing", `func   pinned ( ) { sink ( "fixed" ) }`},
		{"newlines", "func pinned() {\n\tsink(\"fixed\")\n}\n"},
		{"line_comment", "// comment\nfunc pinned() { sink(\"fixed\") } // other comment\n"},
		{"block_comment", `func /* comment */ pinned() { sink("fixed") /* unchanged */ }`},
		{"optional_closing_semicolon", `func pinned() { sink("fixed"); };`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			digest, err := wu19OfficeTokenDigest([]byte(tc.source), nil)
			require.NoError(t, err)
			require.Equal(t, golden, digest, "versioned lexical digest must not depend on formatting or comments")
		})
	}
	for _, tc := range []struct {
		name, source string
		bindings     map[string]string
	}{
		{"literal_change", strings.Replace(source, "fixed", "changed", 1), nil},
		{"name_change", strings.Replace(source, "sink", "other", 1), nil},
		{"import_binding", source, map[string]string{"sink": "example.test/different"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			digest, err := wu19OfficeTokenDigest([]byte(tc.source), tc.bindings)
			require.NoError(t, err)
			require.NotEqual(t, golden, digest, "meaningful tokens/import identities must remain pinned")
		})
	}
	t.Run("malformed_tokens", func(t *testing.T) {
		_, err := wu19OfficeTokenDigest([]byte("func pinned() {\x00}"), nil)
		require.ErrorContains(t, err, "invalid Go token stream")
	})
}
