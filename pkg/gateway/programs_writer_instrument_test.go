package gateway

import (
	"go/ast"
	"go/build"
	"go/parser"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgramCredential_WriterReferenceInstrument(t *testing.T) {
	root := pgWriterRoot(t)
	t.Chdir(root)
	old := build.Default
	t.Cleanup(func() { build.Default = old })
	build.Default = pgWriterBuildContexts()[0]
	source := pgNewWriterSource(root)
	targets := pgWriterTargets(t, source)
	// These are real imported Store/Service types, not stand-in method stubs.
	// The homonymous decoy is intentionally a different concrete type.
	const planted = `package planted
import c "github.com/elicify-ai/omnipus/pkg/credentials"
import p "github.com/elicify-ai/omnipus/pkg/programs"
func arbitrary(s *c.Store) { _, _ = s.CommitProgramCredential("R", "value", nil) }
func value(s *c.Store) { f := s.CommitProgramCredential; _, _ = f("R", "value", nil) }
var expression = (*c.Store).RemoveProgramCredential
func closure(s *c.Store) { f := func() { _ = s.RemoveProgramCredential("R") }; f() }
func structural(w interface { RemoveProgramCredential(string) error }) { _ = w.RemoveProgramCredential("R") }
func service(s *p.Service) { f := s.Publish; _, _ = f("github", "value", nil, nil) }
func bridge(s *c.Store) { _, _ = p.CommitCardToken(s, "github", "value", nil) }
type decoy struct{}
func (decoy) CommitProgramCredential(string, string, *c.CardReceipt) (int64, error) { return 0, nil }
func harmless(d decoy) { _, _ = d.CommitProgramCredential("R", "value", nil) }
func universe(e error) { _ = e.Error() }
`
	file, err := parser.ParseFile(source.fset, filepath.Join(root, "s113-planted.go"), planted, 0)
	require.NoError(t, err)
	pkg, err := source.checkFiles(pgWriterModule+"/s113-planted", []*ast.File{file}, "planted")
	require.NoError(t, err, "instrument must type-check the planted capability references")
	refs := pgWriterReferences(source.fset, pkg, targets)
	counts := map[string]int{}
	for _, ref := range refs {
		counts[ref.key()]++
	}
	prefix := pgWriterModule + "/s113-planted."
	store := pgWriterModule + "/pkg/credentials.Store."
	programs := pgWriterModule + "/pkg/programs."
	expected := map[string]int{
		prefix + "arbitrary -> " + store + "CommitProgramCredential":     1,
		prefix + "value -> " + store + "CommitProgramCredential":         1,
		"planted.<package-init> -> " + store + "RemoveProgramCredential": 1,
		prefix + "closure -> " + store + "RemoveProgramCredential":       1,
		prefix + "structural -> " + store + "RemoveProgramCredential":    1,
		prefix + "service -> " + programs + "Service.Publish":            1,
		prefix + "bridge -> " + programs + "CommitCardToken":             1,
	}
	require.Equal(t, expected, counts, "instrument: calls, values, expression, closure, structural interface and bridge observed; decoy excluded by type")
	require.Len(t, pgWriterInventoryFailures(refs, nil), len(expected), "every unlisted capability edge must fail")
	require.Empty(t, pgWriterInventoryFailures(refs, expected), "instrument: exact permitted table is accepted")
	missing := append([]pgWriterReference(nil), refs[1:]...)
	require.Len(t, pgWriterInventoryFailures(missing, expected), 1, "stale allowlist edge must fail, not pass an empty scan")
	extra := append(append([]pgWriterReference(nil), refs...), refs[0])
	require.Len(t, pgWriterInventoryFailures(extra, expected), 1, "a second reference in an already allowed function must fail")
	bad, err := parser.ParseFile(source.fset, filepath.Join(root, "s113-invalid.go"), "package invalid; func f() { missingCapability() }", 0)
	require.NoError(t, err)
	_, err = source.checkFiles(pgWriterModule+"/s113-invalid", []*ast.File{bad}, "invalid")
	require.ErrorContains(t, err, "undefined: missingCapability", "unresolved source is an error, not an empty successful inventory")
	t.Log("typed inventory instrument: seven exact real capability references found/rejected, homonymous concrete method excluded, stale/duplicate/unresolved controls detected; not a production mutant")
}
