package gateway

import (
	"errors"
	"go/build"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func pgWriterRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		data, readErr := os.ReadFile(filepath.Join(dir, "go.mod"))
		if readErr == nil {
			require.True(t, strings.HasPrefix(string(data), "module "+pgWriterModule+"\n"), "source inventory must inspect the actual module")
			return dir
		}
		require.True(t, errors.Is(readErr, os.ErrNotExist), "module root read failed: %v", readErr)
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "BLOCKED: module source root unavailable — S113")
		dir = parent
	}
}

func pgWriterBuildContexts() []build.Context {
	var out []build.Context
	seen := map[string]bool{}
	for _, target := range [][2]string{{runtime.GOOS, runtime.GOARCH}, {"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"}, {"windows", "arm64"}} {
		key := target[0] + "/" + target[1]
		if seen[key] {
			continue
		}
		seen[key] = true
		ctx := build.Default
		ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled = target[0], target[1], false
		ctx.BuildTags = []string{"goolm", "stdjson", "s1seams"}
		out = append(out, ctx)
	}
	return out
}

func pgWriterTargets(t *testing.T, source *pgWriterSource) map[string]*types.Func {
	t.Helper()
	roots, err := pgWriterRootObjects(source)
	require.NoError(t, err, "BLOCKED: writer root source import — S113")
	programs, err := source.check(filepath.Join(source.root, "pkg", "programs"))
	require.NoError(t, err, "BLOCKED: Programs source type-check — S113")
	targets := pgWriterProgramTargets(programs, roots)
	// Use imported receiver identities for interface matching, since separately
	// checking Programs bodies creates a distinct types.Package for that body.
	imported, err := source.importer.Import(pgWriterModule + "/pkg/programs")
	require.NoError(t, err)
	for key, fn := range targets {
		if fn.Pkg().Path() != imported.Path() {
			continue
		}
		sig, ok := fn.Type().(*types.Signature)
		require.True(t, ok)
		if sig.Recv() == nil {
			resolved, found := imported.Scope().Lookup(fn.Name()).(*types.Func)
			require.True(t, found)
			targets[key] = resolved
			continue
		}
		obj := imported.Scope().Lookup("Service")
		require.NotNil(t, obj)
		selection := types.NewMethodSet(types.NewPointer(obj.Type())).Lookup(imported, fn.Name())
		require.NotNil(t, selection)
		resolved, ok := selection.Obj().(*types.Func)
		require.True(t, ok)
		targets[key] = resolved
	}
	return targets
}

func TestProgramCredential_WriterReferences(t *testing.T) {
	root := pgWriterRoot(t)
	t.Chdir(root)
	old := build.Default
	t.Cleanup(func() { build.Default = old })
	contexts := pgWriterBuildContexts()
	// Discover wrapper names from every distinct Programs source variant,
	// including a future platform-specific wrapper, before selecting callers.
	names := map[string]bool{"CommitProgramCredential": true, "RemoveProgramCredential": true}
	variants := map[string]bool{}
	for _, ctx := range contexts {
		bp, err := ctx.ImportDir(filepath.Join(root, "pkg", "programs"), 0)
		require.NoError(t, err)
		variant := strings.Join(bp.GoFiles, "\n")
		if variants[variant] {
			continue
		}
		variants[variant] = true
		build.Default = ctx
		for _, fn := range pgWriterTargets(t, pgNewWriterSource(root)) {
			names[fn.Name()] = true
		}
	}
	files, err := pgWriterCandidateFiles(root, names)
	require.NoError(t, err, "BLOCKED: complete production source selection — S113")
	require.NotEmpty(t, files)
	groups := make([]map[string]bool, len(contexts))
	for i := range groups {
		groups[i] = map[string]bool{}
	}
	for _, file := range files {
		selected := false
		for i, ctx := range contexts {
			match, matchErr := ctx.MatchFile(filepath.Dir(file), filepath.Base(file))
			require.NoError(t, matchErr)
			if match {
				groups[i][filepath.Dir(file)] = true
				selected = true
				break
			}
		}
		require.True(t, selected, "BLOCKED: candidate writer reference in unaccounted build-constrained production source %s — S113", file)
	}
	unique := map[string]pgWriterReference{}
	for i, group := range groups {
		if len(group) == 0 {
			continue
		}
		build.Default = contexts[i]
		source := pgNewWriterSource(root)
		targets := pgWriterTargets(t, source)
		var dirs []string
		for dir := range group {
			dirs = append(dirs, dir)
		}
		sort.Strings(dirs)
		for _, dir := range dirs {
			pkg, checkErr := source.check(dir)
			require.NoError(t, checkErr, "BLOCKED: complete writer inventory type-check — S113")
			for _, ref := range pgWriterReferences(source.fset, pkg, targets) {
				unique[ref.identity()] = ref
			}
		}
	}
	refs := make([]pgWriterReference, 0, len(unique))
	for _, ref := range unique {
		refs = append(refs, ref)
	}
	require.Empty(t, pgWriterInventoryFailures(refs, pgWriterGatedAllowlist()), "S113: every typed writer/wrapper reference must be a permitted gated caller, with exact reference count")
	t.Logf("source-typed writer inventory: %d production candidate files; %d unique writer/bridge references; exact gated caller/count table matched", len(files), len(refs))
}
