package sandbox

import (
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// Every negative is an in-memory source fixture. No production file is mutated.
// Oracles: Q62 A, security F1 and the seven environment-exception requirement rows.
func TestWU19_OfficePreviewClosedEnvironment(t *testing.T) {
	base := wu19LoadInventory(t, filepath.Join("..", ".."))
	wu19OfficeMaybeUpdatePins(t, base)
	const program = "pkg/preview/programs.go"
	const command = "pkg/preview/launcher/launcher.go"
	cases := []struct{ name, file, old, replacement, declaration, want string }{
		{"reviewed_closed_lists", "", "", "", "", ""},
		{"added_converter_key", program, `"LANG=en_US.UTF-8",`, `"LANG=en_US.UTF-8", "Q62_EXTRA=x",`, "officeConversion.run", ""},
		{"raw_renderer_inheritance", program, `Env:     []string{},`, `Env: os.Environ(),`, "protocolProgram.run", ""},
		{"generic_renderer_inheritance", program, `Env:     []string{},`, `Env: sandbox.ScrubGatewayEnv(),`, "protocolProgram.run", ""},
		{"runner_renderer_inheritance", program, `Env:     []string{},`, `Env: sandbox.ScrubGatewayEnvForRunner(),`, "protocolProgram.run", ""},
		{"nil_inheriting_assignment", command, `cmd.Env = append([]string{}, spec.Env...)`, `cmd.Env = spec.Env`, "Command", ""},
		{"duplicate_assignment", command, `cmd.Env = append([]string{}, spec.Env...)`, `cmd.Env = append([]string{}, spec.Env...); cmd.Env = os.Environ()`, "Command", ""},
		{"widening_prepare_hook", "pkg/preview/confine_darwin.go", `return backend.ApplyToCmd(cmd, policy)`, `cmd.Env = append(cmd.Env, "Q62_EXTRA=x"); return backend.ApplyToCmd(cmd, policy)`, "hooksFor", ""},
		{"duplicate_windows_override", program, `"SystemRoot="+os.Getenv("SystemRoot")`, `"SystemRoot="+os.Getenv("SystemRoot"), "SYSTEMROOT=override"`, "officeConversion.run", ""},
		{"unix_forwarding_enrichment", "pkg/preview/launcher/child_unix.go", `unix.Exec(target[0], target, os.Environ())`, `unix.Exec(target[0], target, append(os.Environ(), "Q62_EXTRA=x"))`, "Main", ""},
		{"windows_forwarding_enrichment", "pkg/preview/launcher/child_windows.go", `cmd.Env = os.Environ()`, `cmd.Env = append(os.Environ(), "Q62_EXTRA=x")`, "Main", ""},
		{"before_hidden_dispatch", "pkg/app/app.go", `if code, handled := runHiddenMode(os.Args); handled {`, `os.Setenv("Q62_EXTRA", "x"); if code, handled := runHiddenMode(os.Args); handled {`, "Main", ""},
		{"unreviewed_start_caller", "pkg/q62probe/extra.go", "", `package q62probe; import "github.com/elicify-ai/omnipus/pkg/preview/launcher"; func extra(s launcher.Spec) { launcher.Start(s) }`, "", "unreviewed boundary reference " + wu19PreviewLauncher + ".Start from github.com/elicify-ai/omnipus/pkg/q62probe.extra"},
		{"unreviewed_command_caller", "pkg/q62probe/extra.go", "", `package q62probe; import "github.com/elicify-ai/omnipus/pkg/preview/launcher"; func extra(s launcher.Spec) { launcher.Command(s) }`, "", "unreviewed boundary reference " + wu19PreviewLauncher + ".Command from github.com/elicify-ai/omnipus/pkg/q62probe.extra"},
		{"new_launcher_initialization", "pkg/preview/launcher/q62_extra.go", "", `package launcher; import "os"; func init() { os.Setenv("Q62_EXTRA", "x") }`, "", "unreviewed launcher file"},
		{"other_package_initialization", "pkg/q62probe/extra.go", "", `package q62probe; import "os"; func init() { enrich() }; func enrich() { os.Setenv("Q62_EXTRA", "x") }`, "", "initialization enriches environment in"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			if tc.file != "" {
				p = wu19OfficeFixture(t, base, tc.file, tc.old, tc.replacement)
			}
			reason := p.officePreviewClosedEnvironment()
			if tc.file == "" {
				if reason != "" {
					t.Fatalf("reviewed closed lists must qualify: %s", reason)
				}
				return
			}
			location, err := filepath.Abs(filepath.Join("..", "..", filepath.FromSlash(tc.file)))
			if err != nil {
				t.Fatal(err)
			}
			want := tc.want
			if tc.declaration != "" {
				want = "reviewed declaration " + location + "::" + tc.declaration + " changed"
			} else if tc.name == "new_launcher_initialization" || tc.name == "other_package_initialization" {
				want += " " + location
			}
			if !strings.Contains(reason, want) {
				t.Fatalf("unsafe fixture %s must report specific rule %q, got %q", tc.name, want, reason)
			}
			t.Logf("rejected for specific rule: %s", want)
			if tc.name == "added_converter_key" {
				t.Logf("actionable diagnostic: %s", reason)
			}
			for _, instruction := range []string{"Office preview closed-environment boundary", "security-lead review", "OMNIPUS_OFFICE_UPDATE_ENV_PINS=1", "closed-env-source-pins.json", "CLOSED-ENV-PINS.md"} {
				if !strings.Contains(reason, instruction) {
					t.Fatalf("unsafe fixture %s lacks actionable instruction %q: %s", tc.name, instruction, reason)
				}
			}
			_, findings := p.scan()
			found := false
			for _, finding := range findings {
				found = found || strings.Contains(finding.reason, want)
			}
			if !found {
				t.Fatalf("unsafe fixture %s must fail actual inventory for specific rule %q, findings=%v", tc.name, want, findings)
			}
		})
	}
}

func wu19OfficeMap[K comparable, V any](source map[K]V) map[K]V {
	cloned := make(map[K]V, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func wu19OfficeFixture(t *testing.T, base *wu19Inventory, name, old, replacement string) *wu19Inventory {
	t.Helper()
	p := *base
	p.files = append([]*wu19File(nil), base.files...)
	p.funcs = wu19OfficeMap(base.funcs)
	p.types = wu19OfficeMap(base.types)
	p.typeOwners = wu19OfficeMap(base.typeOwners)
	p.globals = wu19OfficeMap(base.globals)
	p.globalObjects = wu19OfficeMap(base.globalObjects)
	p.unresolved = map[string]bool{}
	source := replacement
	if old != "" {
		raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(raw), old) != 1 {
			t.Fatalf("fixture instrument: %q must occur exactly once in %s", old, name)
		}
		source = strings.Replace(string(raw), old, replacement, 1)
	}
	for key, list := range p.funcs {
		kept := make([]*wu19Function, 0, len(list))
		for _, fn := range list {
			if wu19OfficeFileName(fn.file) != name {
				kept = append(kept, fn)
			}
		}
		p.funcs[key] = kept
	}
	p.files = nil
	for _, file := range base.files {
		if wu19OfficeFileName(file) != name {
			p.files = append(p.files, file)
		}
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("fixture must parse: %v", err)
	}
	p.addFile(name, "github.com/elicify-ai/omnipus/"+path.Dir(name), file, fset, []byte(source))
	return &p
}

func wu19OfficeFileName(file *wu19File) string {
	return strings.TrimPrefix(file.pkg, "github.com/elicify-ai/omnipus/") + "/" + filepath.Base(file.path)
}
