package browser

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// lease_structural_test.go — FR-030 and SC-029.
//
// Both guard shapes rather than behaviour, because both failures are invisible
// at runtime: a second lease primitive serialises perfectly against itself while
// losing mutual exclusion against the first, and a flock-backed lease reports
// success on Windows while guaranteeing nothing.

// TestLease_IsInProcessOnly_NoFlock is FR-030.
//
// fileutil.WithFlock is a DOCUMENTED no-op on Windows
// (pkg/fileutil/flock_windows.go). A lease built on it would therefore
// advertise a cross-process guarantee it does not have on one supported
// platform — and it would do so silently, because a no-op lock always succeeds.
// Two gateways on one $OMNIPUS_HOME are out of scope (§12 A11); pretending
// otherwise is worse than saying so.
func TestLease_IsInProcessOnly_NoFlock(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "lease.go", nil, parser.ImportsOnly)
	require.NoError(t, err)

	forbidden := []string{"fileutil", "golang.org/x/sys/unix", "syscall"}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		for _, bad := range forbidden {
			require.NotContains(t, path, bad,
				"lease.go imports %q. The write lease is IN-PROCESS ONLY (FR-030): a file lock would "+
					"claim a cross-process guarantee that does not exist on Windows, where "+
					"fileutil.WithFlock is a documented no-op that always succeeds.", path)
		}
	}
}

// TestLease_ExactlyOneAcquireSymbol is SC-029, and it is matched by SHAPE
// rather than by a grep for the literal name "acquireWrite".
//
// The failure it prevents is a second acquisition primitive added beside the
// first — most plausibly as an "owner-aware variant" during a future fix. Two
// unrelated mutexes over the same call sites means mutual exclusion is lost for
// whichever tool takes only one of them, and the result is nondeterministic
// interleaving: the most expensive failure class in this design, and one that
// reproduces roughly never.
//
// The shape: any method or function in pkg/tools/browser (INCLUDING _test.go)
// whose results are (a func, a bool, ...) or (a func, a bool) — a release
// closure plus an acquired flag. That is what a lease acquisition looks like
// whatever it is called.
func TestLease_ExactlyOneAcquireSymbol(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	var found []string

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		file, perr := parser.ParseFile(fset, filepath.Join(".", e.Name()), nil, 0)
		require.NoError(t, perr)

		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Type.Results == nil {
				return true
			}
			results := fn.Type.Results.List
			// Flatten the result list: `(release func(), ok bool, holder string)`
			// is three fields, but `(func(), bool)` is two.
			var kinds []ast.Expr
			for _, f := range results {
				n := len(f.Names)
				if n == 0 {
					n = 1
				}
				for i := 0; i < n; i++ {
					kinds = append(kinds, f.Type)
				}
			}
			if len(kinds) < 2 {
				return true
			}
			_, firstIsFunc := kinds[0].(*ast.FuncType)
			secondIdent, secondIsIdent := kinds[1].(*ast.Ident)
			if firstIsFunc && secondIsIdent && secondIdent.Name == "bool" {
				found = append(found, e.Name()+": "+fn.Name.Name)
			}
			return true
		})
	}

	require.Len(t, found, 1,
		"pkg/tools/browser must contain EXACTLY ONE lease-acquisition symbol (SC-029). Found %d: %s.\n"+
			"Two acquisition primitives over the same call sites means mutual exclusion is lost for "+
			"whichever tool takes only one of them — and that failure is nondeterministic, so it will "+
			"not show up in a test run.",
		len(found), strings.Join(found, ", "))
	require.Contains(t, found[0], "acquireWrite",
		"the one acquisition symbol should still be acquireWrite; if it was renamed, say so here")
}
