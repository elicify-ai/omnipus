package sandbox

import (
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FR-239 / CHECK F01: imports are resolved by identity, helper-return provenance
// is followed across production files, and assignment order/count is checked at
// each command boundary. Unknown provenance is a finding, never an approval.
func TestWU19_SpawnSiteInventory(t *testing.T) {
	t.Run("instrument", wu19InventoryInstrument)
	p := wu19LoadInventory(t, filepath.Join("..", ".."))
	sites, findings := p.scan()
	for _, f := range findings {
		t.Errorf("FR-239: %s::%s: %s", f.path, f.symbol, f.reason)
	}
	for symbol := range p.unresolved {
		t.Logf("unresolved environment dependency: %s", symbol)
	}
	if sites == 0 {
		t.Fatal("inventory instrument failed: zero production exec constructors found")
	}
	t.Logf("inventoried %d production exec constructors across all platform files", sites)
}

func wu19LoadInventory(t *testing.T, root string) *wu19Inventory {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("inventory root instrument: %v", err)
	}
	p := wu19NewInventory()
	for _, tree := range []string{"pkg", "cmd", "internal"} {
		base := filepath.Join(root, tree)
		if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
			continue // optional tree; root/go.mod and production discovery are checked.
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "testutil" || d.Name() == "runnertest" {
					return filepath.SkipDir // fixture executables, not production.
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			p.addFile(path, "github.com/elicify-ai/omnipus/"+filepath.ToSlash(rel), file, fset)
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", tree, err)
		}
	}
	return p
}

func wu19InventoryInstrument(t *testing.T) {
	cases := []struct {
		name, imports, body, helpers, pkg string
		unsafe                            bool
	}{
		{name: "omitted", body: `cmd := exec.Command("x"); cmd.Run()`, unsafe: true},
		{name: "nil", body: `cmd := exec.Command("x"); cmd.Env = nil; cmd.Run()`, unsafe: true},
		{name: "raw", body: `cmd := exec.Command("x"); cmd.Env = os.Environ(); cmd.Run()`, unsafe: true},
		{name: "indirect", body: `env := os.Environ(); cmd := exec.Command("x"); cmd.Env = append(env, "SITE=x"); cmd.Run()`, unsafe: true},
		{name: "conditional_only", body: `cmd := exec.Command("x"); if flag { cmd.Env = ScrubGatewayEnv() }; cmd.Run()`, unsafe: true},
		{name: "scrubbed", body: `cmd := exec.Command("x"); cmd.Env = append(ScrubGatewayEnv(), "SITE=x"); cmd.Run()`},
		{name: "comments_are_not_proof", body: `cmd := exec.Command("x"); /* cmd.Env = ScrubGatewayEnv() */ cmd.Run()`, unsafe: true},
		{name: "raw_helper", body: `cmd := exec.Command("x"); cmd.Env = parent(); cmd.Run()`, helpers: `func parent() []string { return os.Environ() }`, unsafe: true},
		{name: "aliased_exec", imports: `import (process "os/exec"; host "os")`, body: `cmd := process.Command("x"); cmd.Env = host.Environ(); cmd.Run()`, unsafe: true},
		{name: "dot_exec_import", imports: `import (. "os/exec"; "os")`, body: `cmd := Command("x"); cmd.Env = os.Environ(); cmd.Run()`, unsafe: true},
		{name: "constructor_function_alias", body: `create := exec.Command; cmd := create("x"); cmd.Env = os.Environ(); cmd.Start()`, unsafe: true},
		{name: "unknown_helper_appended_to_scrub", body: `cmd := exec.Command("x"); cmd.Env = append(ScrubGatewayEnv(), unknown()...); cmd.Run()`, unsafe: true},
		{name: "nil_or_scrub_helper", body: `cmd := exec.Command("x"); cmd.Env = sometimes(flag); cmd.Run()`, helpers: `func sometimes(flag bool) []string { if flag { return nil }; return ScrubGatewayEnv() }`, unsafe: true},
		{name: "scrub_after_run", body: `cmd := exec.Command("x"); cmd.Run(); cmd.Env = ScrubGatewayEnv()`, unsafe: true},
		{name: "scrub_after_start", body: `cmd := exec.Command("x"); cmd.Start(); cmd.Env = ScrubGatewayEnv()`, unsafe: true},
		{name: "scrub_after_output", body: `cmd := exec.Command("x"); cmd.Output(); cmd.Env = ScrubGatewayEnv()`, unsafe: true},
		{name: "scrub_after_combined_output", body: `cmd := exec.Command("x"); cmd.CombinedOutput(); cmd.Env = ScrubGatewayEnv()`, unsafe: true},
		{name: "conditional_raw_overwrite", body: `cmd := exec.Command("x"); cmd.Env = ScrubGatewayEnv(); if flag { cmd.Env = os.Environ() }; cmd.Run()`, unsafe: true},
		{name: "unknown_helper", body: `cmd := exec.Command("x"); cmd.Env = unknown(); cmd.Run()`, unsafe: true},
		{name: "parameter_without_root", body: `cmd := exec.Command("x"); cmd.Env = input; cmd.Run()`, unsafe: true},
		{name: "helper_connected_to_scrub", body: `cmd := exec.Command("x"); cmd.Env = safe(); cmd.Run()`, helpers: `func safe() []string { return append(ScrubGatewayEnv(), "SITE=x") }`},
		{name: "helper_scrub_decoy_raw_return", body: `cmd := exec.Command("x"); cmd.Env = decoy(); cmd.Run()`, helpers: `func decoy() []string { _ = ScrubGatewayEnv(); return os.Environ() }`, unsafe: true},
		{name: "mutator_helper_before_scrub", body: `cmd := exec.Command("x"); corrupt(cmd); cmd.Env = ScrubGatewayEnv(); cmd.Run()`, helpers: `func corrupt(c *exec.Cmd) { c.Env = os.Environ() }`, unsafe: true},
		{name: "mutator_helper_after_scrub", body: `cmd := exec.Command("x"); cmd.Env = ScrubGatewayEnv(); corrupt(cmd); cmd.Run()`, helpers: `func corrupt(c *exec.Cmd) { c.Env = os.Environ() }`, unsafe: true},
		{name: "raw_variable_overwrite", body: `env := ScrubGatewayEnv(); if flag { env = os.Environ() }; cmd := exec.Command("x"); cmd.Env = env; cmd.Run()`, unsafe: true},
		{name: "cmd_alias_late_scrub", body: `cmd := exec.Command("x"); alias := cmd; alias.Start(); cmd.Env = ScrubGatewayEnv()`, unsafe: true},
		{name: "separate_file_raw_helper", body: `cmd := exec.Command("x"); cmd.Env = crossFile(); cmd.Run()`, helpers: `func crossFile() []string { return os.Environ() }`, unsafe: true},
		// The named God exception belongs only to its three constructor sites,
		// never to a helper that rewrites a non-God command after its scrub.
		{name: "non_god_helper_legacy_scrub", pkg: "github.com/elicify-ai/omnipus/pkg/tools", imports: `import ("os"; "os/exec"; "github.com/elicify-ai/omnipus/pkg/sandbox")`, body: `cmd := exec.Command("x"); cmd.Env = sandbox.ScrubGatewayEnv(); corrupt(cmd); cmd.Run()`, helpers: `func corrupt(c *exec.Cmd) { c.Env = scrubbedEnv(os.Environ()) }`, unsafe: true},
		{name: "unnamed_god_constructor", pkg: "github.com/elicify-ai/omnipus/pkg/tools", body: `cmd := exec.Command("x"); cmd.Env = scrubbedEnv(os.Environ()); cmd.Run()`, unsafe: true},
		{name: "grouped_assignment_raw_env", body: `junk := []string{}; cmd := exec.Command("x"); junk, cmd.Env = ScrubGatewayEnv(), os.Environ(); cmd.Run(); _ = junk`, unsafe: true},
		{name: "grouped_assignment_scrubbed_env", body: `junk := []string{}; cmd := exec.Command("x"); junk, cmd.Env = []string{"SITE=x"}, ScrubGatewayEnv(); cmd.Run(); _ = junk`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := wu19NewInventory()
			imports := tc.imports
			if imports == "" {
				imports = `import ("os"; "os/exec")`
			}
			main := "package sandbox; " + imports + "; func probe(flag bool, input []string) {" + tc.body + "}"
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "probe.go", main, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			pkg := tc.pkg
			if pkg == "" {
				pkg = "github.com/elicify-ai/omnipus/pkg/sandbox"
			}
			p.addFile("probe.go", pkg, f, fset)
			if tc.helpers != "" {
				helperSet := token.NewFileSet()
				helper, err := parser.ParseFile(helperSet, "helper.go", "package sandbox; "+imports+"; "+tc.helpers, parser.SkipObjectResolution)
				if err != nil {
					t.Fatal(err)
				}
				p.addFile("helper.go", pkg, helper, helperSet)
			}
			sites, findings := p.scan()
			if sites != 1 || (len(findings) > 0) != tc.unsafe {
				t.Errorf("%s: constructors=%d unsafe=%t want constructors=1 unsafe=%t; findings=%v", tc.name, sites, len(findings) > 0, tc.unsafe, findings)
			}
		})
	}
}

// A separate instrument receipt remains meaningful if the production inventory
// is correctly RED for a newly discovered production provenance gap.
func TestWU19_SpawnInventoryInstrument(t *testing.T) { wu19InventoryInstrument(t) }
