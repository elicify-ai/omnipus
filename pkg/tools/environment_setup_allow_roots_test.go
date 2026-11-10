package tools

import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/stretchr/testify/require"
)

// ADR-090 environment-setup boundary and L1 amendment §2 D3 A5/A6:
// prefix/cache/tmp are the only selected writes, published runtimes stay rx.
// An already-writable prefix covers its nested cache/tmp. Redundant nested
// branches must not turn that writable prefix into a traversal-only node.
func TestSetupAllowRoots_NestedWorkingDirsStayWithinPrefix(t *testing.T) {
	base := t.TempDir()
	prefix := filepath.Join(base, "selected-prefix")
	runtimeRoot := filepath.Join(base, "published-runtime")
	runtimeEnv := environmentsetup.RuntimeEnv{ReadExec: []string{runtimeRoot}}
	for _, row := range []struct {
		name, cache, tmp string
		want             []fspolicy.AllowRoot
	}{
		{
			name:  "nested_cache_and_tmp_are_already_covered",
			cache: filepath.Join(prefix, "cache"), tmp: filepath.Join(prefix, "tmp"),
			want: []fspolicy.AllowRoot{{Path: runtimeRoot}, {Path: prefix, Writable: true}},
		},
		{
			name:  "external_selected_cache_and_tmp_are_preserved",
			cache: filepath.Join(base, "selected-cache"), tmp: filepath.Join(base, "selected-tmp"),
			want: []fspolicy.AllowRoot{{Path: runtimeRoot}, {Path: prefix, Writable: true},
				{Path: filepath.Join(base, "selected-cache"), Writable: true},
				{Path: filepath.Join(base, "selected-tmp"), Writable: true}},
		},
		{
			name:  "one_nested_one_external",
			cache: filepath.Join(prefix, "cache"), tmp: filepath.Join(base, "selected-tmp"),
			want: []fspolicy.AllowRoot{{Path: runtimeRoot}, {Path: prefix, Writable: true},
				{Path: filepath.Join(base, "selected-tmp"), Writable: true}},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			area := &lifecycleTarget{prefix: prefix, cache: row.cache, tmp: row.tmp}
			before := []fspolicy.AllowRoot{{Path: runtimeRoot}, {Path: prefix, Writable: true},
				{Path: row.cache, Writable: true}, {Path: row.tmp, Writable: true}}
			after := setupAllowRoots(area, runtimeEnv)
			t.Logf("before=%+v after=%+v", before, after)
			require.Equal(t, row.want, after,
				"normalization must remove only already-covered writes; runtime read-only and external selections stay exact")
			for _, root := range after {
				require.Contains(t, before, root, "normalization must not introduce or widen a grant")
			}
		})
	}
}
