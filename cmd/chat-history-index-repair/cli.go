package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// repairFailure carries a fixed operator-facing class. Causes remain private;
// command output never copies archive bodies or arbitrary filesystem errors.
type repairFailure struct {
	class string
	exit  int
	cause error
}

func (e *repairFailure) Error() string { return e.class }
func (e *repairFailure) Unwrap() error { return e.cause }
func refusal(class string) error       { return &repairFailure{class: class, exit: 2} }
func integrity(class string, cause error) error {
	return &repairFailure{class: class, exit: 1, cause: cause}
}
func failureClass(err error) (string, int) {
	var f *repairFailure
	if errors.As(err, &f) {
		return f.class, f.exit
	}
	return "io_error", 1
}

type repairArgs struct {
	mode, root, backup, manifest, binding, receipt    string
	output, planOutput, scratch, executable, operator string
	capture, confirm                                  bool
}

func parseRepairArgs(args []string) (repairArgs, error) {
	var a repairArgs
	if len(args) == 0 {
		return a, refusal("arguments")
	}
	a.mode = args[0]
	allowed := map[string]string{
		"dry-run":         " data-root plan-output scratch-root ",
		"manifest":        " data-root backup-root output ",
		"stopped-receipt": " data-root instance-binding gateway-executable operator output capture-instance ",
		"apply":           " data-root backup-root backup-manifest instance-binding stopped-receipt confirm-stopped scratch-root ",
		"verify":          " data-root backup-root backup-manifest ",
	}[a.mode]
	if allowed == "" {
		return a, refusal("arguments")
	}
	values := map[string]string{}
	for i := 1; i < len(args); i++ {
		raw := args[i]
		if !strings.HasPrefix(raw, "--") {
			return a, refusal("arguments")
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(raw, "--"), "=")
		if !strings.Contains(allowed, " "+name+" ") {
			return a, refusal("arguments")
		}
		if _, exists := values[name]; exists {
			return a, refusal("arguments")
		}
		if name == "capture-instance" || name == "confirm-stopped" {
			if hasValue && value != "true" {
				return a, refusal("arguments")
			}
			value = "true"
		} else if !hasValue {
			i++
			if i >= len(args) || strings.HasPrefix(args[i], "--") {
				return a, refusal("arguments")
			}
			value = args[i]
		}
		if value == "" || !utf8.ValidString(value) {
			return a, refusal("arguments")
		}
		values[name] = value
	}
	a.root, a.backup, a.manifest = values["data-root"], values["backup-root"], values["backup-manifest"]
	a.binding, a.receipt, a.output = values["instance-binding"], values["stopped-receipt"], values["output"]
	a.planOutput, a.scratch = values["plan-output"], values["scratch-root"]
	a.executable, a.operator = values["gateway-executable"], values["operator"]
	a.capture, a.confirm = values["capture-instance"] == "true", values["confirm-stopped"] == "true"
	if a.root == "" {
		return a, refusal("arguments")
	}
	switch a.mode {
	case "manifest":
		if a.backup == "" || a.output == "" {
			return a, refusal("arguments")
		}
	case "verify", "apply":
		if a.backup == "" || a.manifest == "" {
			return a, refusal("backup_missing")
		}
		if a.mode == "apply" && !a.confirm {
			return a, refusal("arguments")
		}
	case "stopped-receipt":
		if strings.TrimSpace(a.operator) == "" || a.output == "" {
			return a, refusal("arguments")
		}
		if a.capture {
			if a.executable == "" || a.binding != "" {
				return a, refusal("arguments")
			}
		} else {
			if a.executable != "" {
				return a, refusal("arguments")
			}
			if a.binding == "" {
				return a, refusal("stopped_evidence_missing")
			}
		}
	}
	return a, nil
}

func runRepair(ctx context.Context, args []string, out io.Writer) int {
	a, err := parseRepairArgs(args)
	if err == nil {
		err = normalizeRepairPaths(&a)
	}
	if err != nil {
		class, code := failureClass(err)
		fmt.Fprintf(out, "class=%s\n", class)
		return code
	}
	if a.mode == "dry-run" || a.mode == "apply" || a.mode == "verify" {
		return runRepairPlan(ctx, a, out)
	}
	if a.mode == "manifest" {
		err = produceManifest(ctx, a, out)
	} else {
		err = produceStoppedEvidence(a, out)
	}
	if err != nil {
		class, code := failureClass(err)
		fmt.Fprintf(out, "mode=%s class=%s\n", a.mode, class)
		return code
	}
	return 0
}

func parsePositivePID(raw []byte) (int, error) {
	s := string(raw)
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != s || uint64(int(n)) != n {
		return 0, refusal("stopped_evidence_unknown")
	}
	return int(n), nil
}

func readEvidenceFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, refusal("stopped_evidence_missing")
	}
	if err != nil {
		return nil, integrity("io_error", err)
	}
	return b, nil
}
