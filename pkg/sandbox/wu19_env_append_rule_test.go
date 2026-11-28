package sandbox

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// Ruling WU19-win 1b fixtures for envAppendKeepsRoot. A helper receives the
// command after its single rooted Env assignment and before the launch. Only
// a builtin append onto the same command's own Env, adding no environment
// data and with no other Env write in the helper, keeps the root; every
// look-alike below must stay a finding.
func TestWU19_EnvAppendRule(t *testing.T) {
	const helperTypes = `type RP struct { Env []string }; func lit() []string { return []string{"GH_TOKEN=x"} }`
	roots := map[string]struct{ pkg, imports, owner, rootExpr string }{
		"generic": {"github.com/elicify-ai/omnipus/pkg/sandbox", `import ("os"; "os/exec")`, "func probe(set RP, other *exec.Cmd)", "ScrubGatewayEnv()"},
		"runner":  {"github.com/elicify-ai/omnipus/pkg/sandbox", `import ("os"; "os/exec")`, "func probe(set RP, other *exec.Cmd)", "ScrubGatewayEnvForRunner()"},
		"god":     {"github.com/elicify-ai/omnipus/pkg/tools", `import ("os"; "os/exec"; "github.com/elicify-ai/omnipus/pkg/childenv")`, "type ExecTool struct{}; func (t *ExecTool) runUnconstrained(set RP, other *exec.Cmd)", "scrubbedEnv(os.Environ())"},
	}
	cases := []struct {
		name, helper, extra string
		roots               []string
		unsafe              bool
	}{
		// Safe: append-only helper of literal-built values, after each root.
		{name: "append_literal_values", helper: `func attach(c *exec.Cmd, s RP) { c.Env = append(c.Env, "A=1", "B=2") }`, roots: []string{"generic", "runner", "god"}},
		{name: "append_literal_helper_values", helper: `func attach(c *exec.Cmd, s RP) { c.Env = append(c.Env, lit()...) }`, roots: []string{"generic", "runner", "god"}},
		{name: "append_program_values", helper: `func attach(c *exec.Cmd, s RP) error { if len(s.Env) > 0 { if c.Env == nil { return nil }; c.Env = append(c.Env, s.Env...) }; return nil }`, roots: []string{"generic", "runner", "god"}},
		// Unsafe look-alikes.
		{name: "append_onto_raw_environ", helper: `func attach(c *exec.Cmd, s RP) { c.Env = append(os.Environ(), s.Env...) }`, roots: []string{"generic", "god"}, unsafe: true},
		{name: "append_raw_environ_values", helper: `func attach(c *exec.Cmd, s RP) { c.Env = append(c.Env, os.Environ()...) }`, roots: []string{"generic", "god"}, unsafe: true},
		{name: "append_scrubbed_values", helper: `func attach(c *exec.Cmd, s RP) { c.Env = append(c.Env, childenv.Filter()...) }`, roots: []string{"god"}, unsafe: true},
		{name: "append_other_command_env", helper: `func attach(c *exec.Cmd, o *exec.Cmd, s RP) { c.Env = append(o.Env, s.Env...) }`, roots: []string{"generic", "god"}, unsafe: true, extra: "other"},
		{name: "append_resliced_env", helper: `func attach(c *exec.Cmd, s RP) { c.Env = append(c.Env[:0], s.Env...) }`, roots: []string{"god"}, unsafe: true},
		{name: "package_local_append", helper: `func append(a []string, b ...string) []string { return os.Environ() }; func attach(c *exec.Cmd, s RP) { c.Env = append(c.Env, "A=1") }`, roots: []string{"god"}, unsafe: true},
		{name: "shadowed_local_append", helper: `func attach(c *exec.Cmd, s RP) { append := func(a []string, b ...string) []string { return os.Environ() }; c.Env = append(c.Env, "A=1") }`, roots: []string{"god"}, unsafe: true},
		{name: "append_with_nil_fallback", helper: `func attach(c *exec.Cmd, s RP) { if c.Env == nil { c.Env = os.Environ() }; c.Env = append(c.Env, s.Env...) }`, roots: []string{"god"}, unsafe: true},
		{name: "append_with_nil_value", helper: `func attach(c *exec.Cmd, s RP) { c.Env = append(c.Env, nil...) }`, roots: []string{"god"}, unsafe: true},
	}
	for _, tc := range cases {
		for _, rootName := range tc.roots {
			root := roots[rootName]
			t.Run(tc.name+"/"+rootName, func(t *testing.T) {
				p := wu19NewInventory()
				call := "attach(cmd, set)"
				if tc.extra == "other" {
					call = "attach(cmd, other, set)"
				}
				pkgName := root.pkg[strings.LastIndex(root.pkg, "/")+1:]
				main := "package " + pkgName + "; " + root.imports + "; " + root.owner + " { cmd := exec.Command(\"x\"); cmd.Env = " + root.rootExpr + "; " + call + "; cmd.Run() }"
				fset := token.NewFileSet()
				f, err := parser.ParseFile(fset, "probe.go", main, parser.SkipObjectResolution)
				if err != nil {
					t.Fatal(err)
				}
				p.addFile("probe.go", root.pkg, f, fset)
				helperSet := token.NewFileSet()
				uses := "var _ = os.Environ; "
				if strings.Contains(root.imports, "childenv") {
					uses += "var _ = childenv.Filter; "
				}
				helper, err := parser.ParseFile(helperSet, "helper.go", "package "+pkgName+"; "+root.imports+"; "+uses+helperTypes+"; "+tc.helper, parser.SkipObjectResolution)
				if err != nil {
					t.Fatal(err)
				}
				p.addFile("helper.go", root.pkg, helper, helperSet)
				sites, findings := p.scan()
				if sites < 1 || (len(findings) > 0) != tc.unsafe {
					t.Errorf("%s after %s root: constructors=%d unsafe=%t want unsafe=%t; findings=%v unresolved=%v", tc.name, rootName, sites, len(findings) > 0, tc.unsafe, findings, p.unresolved)
				}
			})
		}
	}
}
