package sandbox

import (
	"fmt"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// FR-239 / re-CHECK R2-F01: command identity, not local/field names, determines
// which Env writes and nested handoffs must preserve the strict scrub root.
// Expectations come from the round-2 brief; P01 forbids banning all aliases.
func TestWU19_CommandAliasInstrument(t *testing.T) {
	cases := []struct {
		name, helper string
		unsafe       bool
	}{
		{"local_raw", `func mutate(c *exec.Cmd) { copied := c; copied.Env = os.Environ() }`, true},
		{"local_legacy_god", `func mutate(c *exec.Cmd) { copied := c; copied.Env = append(copied.Env, scrubbedEnv(os.Environ())...) }`, true},
		{"transitive_raw", `func mutate(c *exec.Cmd) { first := c; second := first; second.Env = append(second.Env, os.Environ()...) }`, true},
		{"field_to_local_raw", `func mutate(c *exec.Cmd) { box := struct{ process *exec.Cmd }{process: c}; copied := box.process; copied.Env = os.Environ() }`, true},
		{"field_write_raw", `func mutate(c *exec.Cmd) { box := struct{ process *exec.Cmd }{process: c}; box.process.Env = os.Environ() }`, true},
		{"nested_alias_raw", `func mutate(c *exec.Cmd) { copied := c; nested(copied) }; func nested(c *exec.Cmd) { c.Env = os.Environ() }`, true},
		{"nested_field_raw", `func mutate(c *exec.Cmd) { box := struct{ process *exec.Cmd }{process: c}; nested(box.process) }; func nested(c *exec.Cmd) { c.Env = os.Environ() }`, true},
		{"nested_transitive_raw", `func mutate(c *exec.Cmd) { first := c; second := first; nested(second) }; func nested(c *exec.Cmd) { copied := c; copied.Env = os.Environ() }`, true},
		{"alias_nil_env", `func mutate(c *exec.Cmd) { copied := c; copied.Env = nil }`, true},
		{"alias_unknown_env", `func mutate(c *exec.Cmd) { copied := c; copied.Env = unknownEnv() }`, true},
		{"unknown_alias_handoff", `func mutate(c *exec.Cmd) { copied := c; opaque(copied) }`, true},
		{"opaque_alias_source", `func mutate(c *exec.Cmd) { copied := opaque(c); copied.Env = append(copied.Env, "SITE=explicit") }`, true},
		{"reassigned_alias", `func mutate(c *exec.Cmd) { copied := c; copied = opaque(); copied.Env = append(copied.Env, "SITE=explicit") }`, true},
		{"branch_alias_merge", `func mutate(c *exec.Cmd) { copied := c; if flag { copied = opaque() }; copied.Env = append(copied.Env, "SITE=explicit") }`, true},
		{"reassigned_field", `func mutate(c *exec.Cmd) { box := struct{ process *exec.Cmd }{process: c}; box.process = opaque(); copied := box.process; copied.Env = append(copied.Env, "SITE=explicit") }`, true},
		{"field_branch_merge", `func mutate(c *exec.Cmd) { box := struct{ process *exec.Cmd }{process: c}; if flag { box.process = nil }; copied := box.process; copied.Env = append(copied.Env, "SITE=explicit") }`, true},
		{"nil_guard_else_raw", `func mutate(c *exec.Cmd) { copied := c; if copied.Env == nil { copied.Env = ScrubGatewayEnv() } else { copied.Env = os.Environ() } }`, true},
		{"alias_index_raw", `func mutate(c *exec.Cmd) { copied := c; copied.Env[0] = os.Environ()[0] }`, true},
		{"alias_copy_raw", `func mutate(c *exec.Cmd) { copied := c; copy(copied.Env, os.Environ()) }`, true},
		{"parameter_reassignment", `func mutate(c *exec.Cmd) { c = opaque(); c.Env = append(c.Env, "SITE=explicit") }`, true},
		{"unproved_nested_argument", `func mutate(c *exec.Cmd) { copied := opaque(); nested(copied) }; func nested(c *exec.Cmd) { c.Env = append(c.Env, "SITE=explicit") }`, true},
		{"unproved_field_source", `func mutate(c *exec.Cmd) { holder := wrap(c); copied := holder.process; copied.Env = os.Environ() }; func wrap(c *exec.Cmd) struct{ process *exec.Cmd } { return struct{ process *exec.Cmd }{process: c} }`, true},
		{"assigned_field_raw", `func mutate(c *exec.Cmd) { var box struct{ process *exec.Cmd }; box.process = c; copied := box.process; copied.Env = os.Environ() }`, true},
		{"positional_field_raw", `type box struct { process *exec.Cmd }; func mutate(c *exec.Cmd) { holder := box{c}; copied := holder.process; copied.Env = os.Environ() }`, true},
		{"nested_container_field_raw", `func mutate(c *exec.Cmd) { holder := struct{ inner struct{ process *exec.Cmd } }{inner: struct{ process *exec.Cmd }{process: c}}; copied := holder.inner.process; copied.Env = os.Environ() }`, true},
		{"nested_env_parameter_raw_reassignment", `func mutate(c *exec.Cmd) { nested(c, []string{"SITE=explicit"}) }; func nested(c *exec.Cmd, values []string) { values = os.Environ(); copied := c; copied.Env = append(copied.Env, values...) }`, true},
		{"safe_nested_env_parameter_reassignment", `func mutate(c *exec.Cmd) { nested(c, []string{"SITE=explicit"}) }; func nested(c *exec.Cmd, values []string) { values = []string{"SITE=changed-explicit"}; copied := c; copied.Env = append(copied.Env, values...) }`, false},
		{"safe_alias_index", `func mutate(c *exec.Cmd) { copied := c; copied.Env[0] = "SITE=explicit" }`, false},
		{"safe_alias_copy", `func mutate(c *exec.Cmd) { copied := c; copy(copied.Env, []string{"SITE=explicit"}) }`, false},
		{"safe_local_addition", `func mutate(c *exec.Cmd) { copied := c; copied.Env = append(copied.Env, "SITE=explicit") }`, false},
		{"safe_field_transitive_addition", `func mutate(c *exec.Cmd) { box := struct{ process *exec.Cmd }{process: c}; first := box.process; second := first; second.Env = append(second.Env, "SITE=explicit") }`, false},
		{"safe_field_write", `func mutate(c *exec.Cmd) { box := struct{ process *exec.Cmd }{process: c}; box.process.Env = append(box.process.Env, "SITE=explicit") }`, false},
		{"safe_nested_addition", `func mutate(c *exec.Cmd) { copied := c; nested(copied, "SITE=explicit") }; func nested(c *exec.Cmd, addition string) { first := c; second := first; second.Env = append(second.Env, addition) }`, false},
		{"safe_nested_field_addition", `func mutate(c *exec.Cmd) { box := struct{ process *exec.Cmd }{process: c}; nested(box.process) }; func nested(c *exec.Cmd) { copied := c; copied.Env = append(copied.Env, "SITE=explicit") }`, false},
		{"safe_declared_alias", `func mutate(c *exec.Cmd) { var copied *exec.Cmd = c; copied.Env = append(copied.Env, "SITE=explicit") }`, false},
		{"safe_alias_nil_fallback", `func mutate(c *exec.Cmd) { copied := c; if copied.Env == nil { copied.Env = os.Environ() }; copied.Env = append(copied.Env, "SITE=explicit") }`, false},
	}
	// Sixteen hops are a representative long chain, not a supported-depth cap.
	// The same resolver must handle every hop; there is no source-name exception.
	var chain strings.Builder
	chain.WriteString("func mutate(c *exec.Cmd) { node0 := c; ")
	for i := 1; i <= 16; i++ {
		fmt.Fprintf(&chain, "node%d := node%d; ", i, i-1)
	}
	chain.WriteString("node16.Env = os.Environ() }")
	cases = append(cases, struct {
		name, helper string
		unsafe       bool
	}{"long_transitive_chain", chain.String(), true})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wu19AssertCommandAliasClassification(t, tc.helper, tc.unsafe)
		})
	}
}

func wu19AssertCommandAliasClassification(t *testing.T, helper string, unsafe bool) {
	t.Helper()
	const imports = `import ("os"; "os/exec"; "github.com/elicify-ai/omnipus/pkg/sandbox")`
	const constructor = `func probe() { cmd := exec.Command("x"); cmd.Env = sandbox.ScrubGatewayEnv(); mutate(cmd); cmd.Run() }`
	p := wu19NewInventory()
	file, err := parser.ParseFile(token.NewFileSet(), "aliases.go", "package tools; "+imports+"; "+constructor+"; "+helper, 0)
	if err != nil {
		t.Fatalf("alias instrument source must parse: %v", err)
	}
	p.addFile("aliases.go", "github.com/elicify-ai/omnipus/pkg/tools", file)
	sites, findings := p.scan()
	if sites != 1 || (len(findings) != 0) != unsafe {
		t.Fatalf("command alias classification: sites=%d findings=%v; want sites=1 unsafe=%t", sites, findings, unsafe)
	}
	if unsafe {
		const reason = "command-pointer helper may reopen raw/nil/unresolved environment after initial scrub"
		if len(findings) != 1 || findings[0].reason != reason {
			t.Fatalf("alias must fail at mutation/handoff boundary: findings=%v; want exactly %q", findings, reason)
		}
	}
}
