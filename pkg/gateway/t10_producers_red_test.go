package gateway

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Traces to: schedules-t10-spec-r6.md lines 717–722; TDD 2/5/6, D-4.1–4.
// Structural half of one-write creation: the production JobSpec at EACH creating
// site must carry Kind (a return-only final snapshot cannot see two writes).
// This does not replace the still-required first-save observation/binding gate.
func TestT10_AllProducersCarryKindOnCreatingSpec(t *testing.T) {
	for _, tc := range []struct{ file, fn string }{
		{"heartbeat_schedule.go", "ReconcileHeartbeatSchedules"},
		{"../agent/loop_scheduler.go", "AddInterval"},
		{"../agent/loop_scheduler.go", "AddOneShot"},
		{"../agent/task_trigger.go", "jobSpecFor"},
	} {
		t.Run(tc.fn, func(t *testing.T) {
			f, err := parser.ParseFile(token.NewFileSet(), tc.file, nil, 0)
			require.NoError(t, err)
			functions, specs := 0, 0
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Name.Name != tc.fn {
					continue
				}
				functions++
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					literal, ok := n.(*ast.CompositeLit)
					if !ok {
						return true
					}
					typ, ok := literal.Type.(*ast.SelectorExpr)
					if !ok || typ.Sel.Name != "JobSpec" {
						return true
					}
					specs++
					hasKind := false
					for _, elt := range literal.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok {
							if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Kind" {
								hasKind = true
							}
						}
					}
					assert.True(t, hasKind, "%s/%s creates a job without Kind; a later UpdateJob stamp is forbidden", tc.file, tc.fn)
					return true
				})
			}
			require.Equal(t, 1, functions, "bind this assertion if the production producer is renamed")
			require.Positive(t, specs, "do not pass vacuously when creation is moved to a helper; bind its creating spec")
		})
	}
}
