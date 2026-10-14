package tools

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

// F3 / D3 A2 and D5: an operator pattern opens exactly the permitted
// non-inventory home path, but never inventory or a read-confined outsider.
func TestWU23R1_OperatorReadPatternsRespectHomeInventory(t *testing.T) {
	f := fixture.New(t)
	permitted := filepath.Join(f.Home, "operator-reference", "permitted.txt")
	unmatched := filepath.Join(f.Home, "operator-reference", "unmatched.txt")
	fixture.Write(t, permitted, "OPERATOR_PERMITTED_EXACT\n")
	fixture.Write(t, unmatched, "UNMATCHED_PRIVATE\n")
	protected := filepath.Join(f.Home, "sessions", "private.txt")
	patterns := []*regexp.Regexp{regexp.MustCompile("^" + regexp.QuoteMeta(permitted) + "$"), regexp.MustCompile("^" + regexp.QuoteMeta(protected) + "$")}
	reader := NewReadFileTool(f.AgentHome, true, MaxReadFileSize, patterns)
	ctx := wu23Context(f)
	policy, err := ResolveTurnFSPolicy(ctx, f.AgentHome, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("workspace_positive", func(t *testing.T) {
		res := reader.Execute(ctx, map[string]any{"path": filepath.Join(f.Work, "allowed.txt")})
		if res.IsError || !strings.Contains(res.ForLLM, "WU23_ALLOWED_CONTROL") {
			t.Fatalf("real workspace read control: %s", res.ForLLM)
		}
	})
	t.Run("inventory_still_refused", func(t *testing.T) {
		res := reader.Execute(ctx, map[string]any{"path": protected})
		if !res.IsError || !errors.Is(res.Err, ErrCarveOut) || strings.Contains(res.ForLLM, fixture.Sentinel) {
			t.Fatalf("pattern must not open inventory: error=%v output=%s", res.Err, res.ForLLM)
		}
	})
	t.Run("unmatched_neighbor_still_refused", func(t *testing.T) {
		res := reader.Execute(ctx, map[string]any{"path": unmatched})
		if !res.IsError || !errors.Is(res.Err, ErrCarveOut) || strings.Contains(res.ForLLM, "UNMATCHED_PRIVATE") {
			t.Fatalf("exact operator pattern widened to its neighbor: error=%v output=%s", res.Err, res.ForLLM)
		}
	})
	t.Run("matched_home_reference_is_readable", func(t *testing.T) {
		res := reader.Execute(ctx, map[string]any{"path": permitted})
		if res.IsError || !strings.Contains(res.ForLLM, "OPERATOR_PERMITTED_EXACT") {
			t.Errorf("explicit tools.allow_read_paths must admit non-inventory home file: %s", res.ForLLM)
		}
		handle, resolveErr := ResolvePathAllowingPatterns(ctx, policy, "read_file", "r1-pattern", FSOpRead, permitted, patterns)
		if resolveErr != nil {
			t.Fatalf("matched non-inventory path must resolve: %v", resolveErr)
		}
		defer handle.Close()
		data, readErr := handle.ReadFile()
		if readErr != nil || string(data) != "OPERATOR_PERMITTED_EXACT\n" {
			t.Fatalf("exact permitted read bytes=%q err=%v", data, readErr)
		}
	})
	t.Run("read_confined_pattern_does_not_open_external_reference", func(t *testing.T) {
		outside := filepath.Join(f.Outside, "read-confined.txt")
		fixture.Write(t, outside, "EXTERNAL_REFERENCE\n")
		outsidePattern := []*regexp.Regexp{regexp.MustCompile("^" + regexp.QuoteMeta(outside) + "$")}
		confinedCtx := WithReadConfined(ctx, true)
		confined, policyErr := ResolveTurnFSPolicy(confinedCtx, f.AgentHome, true)
		if policyErr != nil {
			t.Fatal(policyErr)
		}
		handle, resolveErr := ResolvePathAllowingPatterns(confinedCtx, confined, "read_file", "r1-confined", FSOpRead, outside, outsidePattern)
		if handle != nil {
			handle.Close()
		}
		if !errors.Is(resolveErr, ErrOutsideScope) {
			t.Fatalf("read-confined policy must still refuse external pattern with ErrOutsideScope: %v", resolveErr)
		}
	})
}

func wu23R1AssertProtectedCode(t *testing.T, res *ToolResult, tool string) {
	t.Helper()
	if res == nil || !res.IsError {
		t.Fatalf("%s protected request did not fail: %+v", tool, res)
	}
	// Chief Q1 A: use the contract-generated type. No handwritten new payload;
	// this RED still compiles before the additive contract has been generated.
	var payload generated.PermissionDenied
	field := reflect.ValueOf(&payload).Elem().FieldByName("Code")
	if !field.IsValid() {
		t.Fatalf("BLOCKED: generated.PermissionDenied.Code not implemented — required by WU23 round 1 chief Q1 A; %s actual result=%q", tool, res.ForLLM)
	}
	dec := json.NewDecoder(strings.NewReader(res.ForLLM))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		t.Fatalf("%s must emit generated PermissionDenied, not unstructured prose: %v output=%q", tool, err, res.ForLLM)
	}
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			t.Fatal("protected_internal_file code omitted from protected refusal")
		}
		field = field.Elem()
	}
	if field.Kind() != reflect.String || field.String() != "protected_internal_file" {
		t.Fatalf("%s code=%v want protected_internal_file", tool, field)
	}
	if payload.Error != "permission_denied" || payload.Tool != tool || !payload.Permanent || payload.Message == "" || payload.Reason == "" {
		t.Fatalf("new additive code must preserve PermissionDenied fields: %+v", payload)
	}
}

// F5: both real refusal producers must expose the same additive code. This
// test checks actual results, not that a source file happens to name a code.
func TestWU23R1_ProtectedRefusalsHaveStructuredCode(t *testing.T) {
	f := fixture.New(t)
	ctx := wu23Context(f)
	reader := NewReadFileTool(f.AgentHome, false, MaxReadFileSize)
	t.Run("workspace_positive", func(t *testing.T) {
		res := reader.Execute(ctx, map[string]any{"path": filepath.Join(f.Work, "allowed.txt")})
		if res.IsError || !strings.Contains(res.ForLLM, "WU23_ALLOWED_CONTROL") {
			t.Fatalf("ordinary allowed read is not an internal-file refusal: %s", res.ForLLM)
		}
	})
	t.Run("ErrCarveOut_file_tool", func(t *testing.T) {
		res := reader.Execute(ctx, map[string]any{"path": filepath.Join(f.Home, "sessions", "private.txt")})
		if !errors.Is(res.Err, ErrCarveOut) {
			t.Fatalf("file refusal must retain ErrCarveOut: %v", res.Err)
		}
		wu23R1AssertProtectedCode(t, res, "read_file")
	})
	for _, god := range []bool{false, true} {
		name := "shell_path_guard_bash_normal"
		if god {
			name = "shell_path_guard_bash_god"
		}
		t.Run(name, func(t *testing.T) {
			bash, err := NewExecToolWithDeps(f.AgentHome, false, nil, ExecToolDeps{GodMode: god})
			if err != nil {
				t.Fatal(err)
			}
			if allowed := bash.guardCommand(ctx, "cat "+filepath.Join(f.Work, "allowed.txt"), f.Work); allowed != "" {
				t.Fatalf("command guard refused an allowed operand: %s", allowed)
			}
			res := bash.Execute(ctx, map[string]any{"command": "cat " + filepath.Join(f.Home, "sessions", "private.txt")})
			wu23R1AssertProtectedCode(t, res, "bash")
		})
	}
}
