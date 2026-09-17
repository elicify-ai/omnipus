#!/usr/bin/env bash
# check-gocyclo-budget.sh
#
# Cyclomatic-complexity budget gate (founder ruling, decision #3 of the
# 2026-09-16 conventions interview, loop-split-bench
# results/conventions-decisions.md):
#
#   gocyclo WARN at 30 (74 names today), implemented in the CUSTOM budget
#   script, NOT .golangci.yaml. cyclop stays off. nestif NOT adopted.
#
# This is that custom budget script. It deliberately does not touch
# .golangci.yaml; the YAML's gocyclo settings stay configured-and-disabled.
#
# Rule:
#   * a function with cyclomatic complexity at or above 30 prints a WARN line
#   * WARN does NOT fail the build — there is NO FAIL threshold for this gate;
#     it is advisory (the founder chose visibility over blocking)
#   * the one thing that DOES fail: a function already listed in
#     scripts/budgets/gocyclo.txt that GREW — the shrink-only ratchet, same
#     contract as scripts/budgets/functions.txt and files.txt. New complexity
#     cannot be added to an already-complex function; existing complexity is
#     not a build-stopper.
#
# Complexity definition implemented (stated per the ruling that named gocyclo):
# the upstream fzipp/gocyclo count — 1 + one for each `if`, `for` (including
# `for ... range`), each individual `case` clause (not the `switch` itself;
# `default:` counts as a case), each `select` comm-clause (`default:` counts),
# and each `&&` and `||`. Counted over the whole body INCLUDING nested
# function literals, so a closure's branches belong to the enclosing function.
# `gocyclo` itself is not on PATH here and is not a dependency; the count is
# computed by the embedded go/ast scanner below (same approach as
# scripts/funlen and cmd/funstats in the split-bench). The scanner parses
# every *.go file regardless of build tags, so tag-gated files (e.g.
# pkg/channels/matrix under goolm) ARE counted — complexity is a property of
# the source text, not of the tag set a particular build happened to use.
#
# Matching is by `file<TAB>qualified name`, never by line number, so a listed
# function that moves within its file keeps its entry; a package-dir+name
# fallback key keeps it across a file split within the same package (mirrors
# check-function-budget.sh).
#
# Output contract:
#   WARN <file:line> <name> <n> >= 30
#   FAIL <file:line> <name> <n> > listed <m>
#   listed: <n> functions; at or over 30: <m>   (always last)
#
# Exit: 0 clean or warn-only, 1 any FAIL (a listed function grew), 2 the gate
# itself could not run (no go toolchain, scanner crashed, bad arguments) — a
# false green here would be worse than a false red
# (docs/internal/false-green-patterns.md).
#
# Usage: bash scripts/check-gocyclo-budget.sh [--root <dir>] [--budget <file>]
#                                              [--self-test]
# --root/--budget exist so --self-test can point the real scanner and the
# real matching logic at a throwaway fixture tree instead of this repository.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NAME="check-gocyclo-budget"

# ── self-test ───────────────────────────────────────────────────────────────
# Proves the gate can actually FAIL (a listed function that grew) AND that it
# stays advisory where it must (an unlisted function at 31 warns but exits 0;
# a function at 29 is silent). A gate with no negative test is not a gate.
# Runs the REAL gate (this script, re-invoked) against generated fixtures —
# the same real-scanner-plus-real-AWK path CI takes, never a reimplementation.
if [ "${1:-}" = "--self-test" ]; then
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/gocyclo-budget-selftest.XXXXXX")" \
    || { echo "$NAME: mktemp failed" >&2; exit 2; }
  trap 'rm -rf "$tmp"' EXIT

  # One fixture file. Complexity is 1 + (number of ifs): ThirtyOne and
  # ListedGrew and ListedExact each have 30 ifs (complexity 31); TwentyNine
  # has 28 (complexity 29, below the gate's report limit).
  gen_ifs() { # $1 = how many ifs to emit
    local i
    for ((i = 0; i < $1; i++)); do
      printf '\tif x > %d {\n\t\ty++\n\t}\n' "$i"
    done
  }
  mkdir -p "$tmp/repo/pkg/fixture"
  {
    echo "package fixture"
    echo
    echo "func ThirtyOne(x int) int {"
    printf '\ty := 0\n'
    gen_ifs 30
    printf '\treturn y\n'
    echo "}"
    echo
    echo "func TwentyNine(x int) int {"
    printf '\ty := 0\n'
    gen_ifs 28
    printf '\treturn y\n'
    echo "}"
    echo
    echo "func ListedGrew(x int) int {"
    printf '\ty := 0\n'
    gen_ifs 30
    printf '\treturn y\n'
    echo "}"
    echo
    echo "func ListedExact(x int) int {"
    printf '\ty := 0\n'
    gen_ifs 30
    printf '\treturn y\n'
    echo "}"
  } > "$tmp/repo/pkg/fixture/complex.go"

  # (1) MUST FAIL: ListedGrew is listed at 30 but measures 31 (grew).
  printf 'pkg/fixture/complex.go\tListedGrew\t30\npkg/fixture/complex.go\tListedExact\t31\n' \
    > "$tmp/budget-grew.txt"
  rc=0
  bash "$0" --root "$tmp/repo" --budget "$tmp/budget-grew.txt" \
    > "$tmp/run1.out" 2>&1 || rc=$?
  if [ "$rc" -ne 1 ]; then
    echo "$NAME: SELF-TEST FAILED — a listed function that grew must exit 1, got $rc" >&2
    sed 's/^/    /' "$tmp/run1.out" >&2
    exit 2
  fi
  grep -q 'FAIL pkg/fixture/complex.go:[0-9]* ListedGrew 31 > listed 30' "$tmp/run1.out" \
    || { echo "$NAME: SELF-TEST FAILED — no '31 > listed 30' FAIL line for ListedGrew" >&2
         sed 's/^/    /' "$tmp/run1.out" >&2; exit 2; }
  echo "  ok  self-test: a listed function that grew fails (exit 1, '31 > listed 30')"

  # (1b) the same run must stay ADVISORY everywhere else: unlisted ThirtyOne
  # (31) and ListedExact (at its listed 31) warn but do not fail, and
  # TwentyNine (29) is not reported at all.
  grep -q 'WARN pkg/fixture/complex.go:[0-9]* ThirtyOne 31 >= 30' "$tmp/run1.out" \
    || { echo "$NAME: SELF-TEST FAILED — no WARN line for unlisted ThirtyOne" >&2
         sed 's/^/    /' "$tmp/run1.out" >&2; exit 2; }
  grep -q 'WARN pkg/fixture/complex.go:[0-9]* ListedExact 31 >= 30' "$tmp/run1.out" \
    || { echo "$NAME: SELF-TEST FAILED — no WARN line for ListedExact at its pinned value" >&2
         sed 's/^/    /' "$tmp/run1.out" >&2; exit 2; }
  if grep -q 'TwentyNine' "$tmp/run1.out"; then
    echo "$NAME: SELF-TEST FAILED — complexity-29 function must be silent" >&2
    sed 's/^/    /' "$tmp/run1.out" >&2
    exit 2
  fi
  echo "  ok  self-test: unlisted 31 and pinned-at-31 WARN only; 29 is silent"

  # (2) MUST PASS: same fixture, budget re-pinned at the measured values —
  # over-30 functions everywhere, zero FAILs, exit 0. This is the advisory
  # half of the ruling: existing complexity is not a build-stopper.
  printf 'pkg/fixture/complex.go\tListedGrew\t31\npkg/fixture/complex.go\tListedExact\t31\n' \
    > "$tmp/budget-ok.txt"
  rc=0
  bash "$0" --root "$tmp/repo" --budget "$tmp/budget-ok.txt" \
    > "$tmp/run2.out" 2>&1 || rc=$?
  if [ "$rc" -ne 0 ]; then
    echo "$NAME: SELF-TEST FAILED — correctly-pinned over-30 functions must exit 0, got $rc" >&2
    sed 's/^/    /' "$tmp/run1.out" >&2
    sed 's/^/    /' "$tmp/run2.out" >&2
    exit 2
  fi
  if grep -q '^FAIL' "$tmp/run2.out"; then
    echo "$NAME: SELF-TEST FAILED — no FAIL line may appear when nothing grew" >&2
    sed 's/^/    /' "$tmp/run2.out" >&2
    exit 2
  fi
  grep -q '^listed: 2 functions; at or over 30: 3' "$tmp/run2.out" \
    || { echo "$NAME: SELF-TEST FAILED — summary line wrong" >&2
         sed 's/^/    /' "$tmp/run2.out" >&2; exit 2; }
  echo "  ok  self-test: over-30 with nothing grown warns only (exit 0)"
  echo "$NAME: self-test passed"
  exit 0
fi

ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BUDGET="$SCRIPT_DIR/budgets/gocyclo.txt"

while [ $# -gt 0 ]; do
  case "$1" in
    --root) ROOT="$2"; shift 2 ;;
    --budget) BUDGET="$2"; shift 2 ;;
    *) echo "$NAME: unknown argument: $1" >&2; exit 2 ;;
  esac
done

[ -d "$ROOT" ] || { echo "$NAME: root not found: $ROOT" >&2; exit 2; }
[ -f "$BUDGET" ] || { echo "$NAME: budget file not found: $BUDGET" >&2; exit 2; }
command -v go >/dev/null 2>&1 || { echo "$NAME: go toolchain not found on PATH" >&2; exit 2; }

TMP="$(mktemp -d "${TMPDIR:-/tmp}/gocyclo-budget.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

# The scanner is embedded rather than living in scripts/<tool>/ (as funlen
# does) so this gate is one self-contained file. It imports only the standard
# library, so `go run` works on a bare file outside any module; GOWORK/GOFLAGS
# are neutralised so the repo's tag/module environment cannot change what the
# scanner sees.
cat > "$TMP/gocyclo_scan.go" <<'GOSCAN'
// gocyclo_scan: lists every Go function/method whose cyclomatic complexity is
// at or above -limit, for scripts/check-gocyclo-budget.sh. Pure go/ast — no
// type checking, no build tags applied, so every parseable .go file in the
// tree is measured (a tag-gated file still has complexity). Mirrors
// scripts/funlen's walk/skip/naming so the two gates agree on which functions
// exist.
//
// Complexity (upstream fzipp/gocyclo definition): 1 + one for each ast.IfStmt,
// ast.ForStmt (a `for ... range` is an ast.RangeStmt and counts equally — the
// ruling's "for" covers both loop forms), ast.CaseClause (each individual
// case, not the switch; `default:` is a CaseClause and counts), ast.CommClause
// (each select comm-clause; `default:` counts), and each `&&` / `||` binary
// operator. Counted over the whole body including nested function literals, so
// a closure's branches belong to the enclosing function.
//
// Output rows: complexity<TAB>file:line<TAB>qualified name<TAB>kind, sorted by
// complexity descending then path. kind is "function" or "test" (a _test.go
// file) — informational; the gate's AWK does not branch on it. Lines starting
// with "#" are commentary, not data.
package main

import (
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type row struct {
	file  string
	name  string
	start int
	comp  int
	test  bool
}

func main() {
	root := flag.String("root", ".", "repo root to scan")
	limit := flag.Int("limit", 30, "report functions with complexity at or above this")
	skip := flag.String("skip", "pkg/api/generated,pkg/gateway/spa,node_modules,.gitnexus,dist,vendor,.git", "comma-separated path fragments to skip entirely")
	flag.Parse()

	skips := splitNonEmpty(*skip)
	fset := token.NewFileSet()
	var rows []row
	totalProd, totalTest := 0, 0

	walkErr := filepath.WalkDir(*root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A path that vanished mid-walk, or one we may not read, is not
			// this scanner's business; any other error must stop the scan —
			// silently skipping it would under-report complexity and let the
			// budget gate pass on an incomplete measurement.
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return err
		}
		rel, relErr := filepath.Rel(*root, p)
		if relErr != nil {
			rel = p
		}
		if shouldSkip(rel, skips) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			// A parse failure is reported and skips the file; the gate still
			// runs on what parsed, but the error is visible, never silent.
			fmt.Fprintln(os.Stderr, "gocyclo_scan: parse error:", p, perr)
			return nil
		}
		isTest := strings.HasSuffix(p, "_test.go")
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			if isTest {
				totalTest++
			} else {
				totalProd++
			}
			if c := cyclomatic(fd); c >= *limit {
				rows = append(rows, row{
					file:  rel,
					name:  qualifiedName(fd),
					start: fset.Position(fd.Pos()).Line,
					comp:  c,
					test:  isTest,
				})
			}
		}
		return nil
	})
	if walkErr != nil {
		fmt.Fprintln(os.Stderr, "gocyclo_scan: walk error:", walkErr)
		os.Exit(1)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].comp != rows[j].comp {
			return rows[i].comp > rows[j].comp
		}
		return rows[i].file < rows[j].file
	})

	fmt.Printf("# functions with cyclomatic complexity >= %d\n", *limit)
	fmt.Printf("# scanned: %d production, %d test\n", totalProd, totalTest)
	fmt.Printf("# at or over limit: %d\n", len(rows))
	for _, r := range rows {
		kind := "function"
		if r.test {
			kind = "test"
		}
		fmt.Printf("%d\t%s:%d\t%s\t%s\n", r.comp, r.file, r.start, r.name, kind)
	}
}

// cyclomatic is the fzipp/gocyclo count over fd's whole body, closures
// included. See the file comment for the exact node/op list.
func cyclomatic(fd *ast.FuncDecl) int {
	c := 1
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.CaseClause, *ast.CommClause:
			c++
		case *ast.BinaryExpr:
			if x.Op == token.LAND || x.Op == token.LOR {
				c++
			}
		}
		return true
	})
	return c
}

func splitNonEmpty(csv string) []string {
	var out []string
	for _, s := range strings.Split(csv, ",") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// shouldSkip matches a path component boundary, not a bare substring, so
// "dist" does not also skip "distillery/".
func shouldSkip(rel string, skips []string) bool {
	sep := string(filepath.Separator)
	for _, s := range skips {
		if rel == s || strings.HasPrefix(rel, s+sep) || strings.Contains(rel, sep+s+sep) {
			return true
		}
	}
	return false
}

// qualifiedName: "Receiver.Method" for methods, the bare name otherwise —
// same convention as scripts/funlen and the functions.txt budget.
func qualifiedName(fd *ast.FuncDecl) string {
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		return recvName(fd.Recv.List[0].Type) + "." + fd.Name.Name
	}
	return fd.Name.Name
}

func recvName(t ast.Expr) string {
	switch x := t.(type) {
	case *ast.StarExpr:
		return recvName(x.X)
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return recvName(x.X)
	case *ast.IndexListExpr:
		return recvName(x.X)
	}
	return "?"
}
GOSCAN

# Capture the scanner's exit code directly (no pipe) — a pipeline's status is
# its last command's, so `scanner | something` would silently hide a scanner
# crash (docs/internal/false-green-patterns.md).
SCAN_RC=0
env GOWORK=off GO111MODULE=on GOFLAGS= go run "$TMP/gocyclo_scan.go" \
  -root "$ROOT" -limit 30 >"$TMP/scan.out" 2>"$TMP/scan.err" || SCAN_RC=$?
if [ "$SCAN_RC" -ne 0 ]; then
  echo "$NAME: scanner failed (exit $SCAN_RC)" >&2
  cat "$TMP/scan.err" >&2
  exit 2
fi
if [ -s "$TMP/scan.err" ]; then
  echo "$NAME: scanner reported parse errors (scan incomplete):" >&2
  cat "$TMP/scan.err" >&2
  exit 2
fi

# The matching/reporting logic lives in AWK (portable across the BSD awk on
# macOS and gawk on Linux CI, and immune to bash 3.2's lack of associative
# arrays) — same shape as check-function-budget.sh.
#   * WARN at >= 30 — advisory, never fatal by itself.
#   * FAIL only when a listed function's measured value exceeds its listed
#     value (the shrink-only ratchet). An unlisted function at any value
#     cannot FAIL — there is no FAIL threshold for this gate.
#   * A listed entry matches by exact file+name, or by package dir+name so a
#     function moved to a sibling file in the same package (a file split)
#     keeps its entry. If two listed functions in one dir share a name, the
#     dir key keeps the larger number (the exact key still wins).
awk -v budget="$BUDGET" '
  BEGIN {
    FS = "\t";
    while ((getline line < budget) > 0) {
      if (line ~ /^[[:space:]]*(#|$)/) continue;
      n = split(line, f, "\t");
      if (n < 3) continue;
      key = f[1] SUBSEP f[2];
      budget_c[key] = f[3] + 0;
      dir = f[1]; sub(/\/[^\/]*$/, "", dir);
      dkey = dir SUBSEP f[2];
      if (!(dkey in budget_dir) || f[3] + 0 > budget_dir[dkey]) budget_dir[dkey] = f[3] + 0;
      budget_count++;
    }
    close(budget);
    fail_count = 0;
    warn_count = 0;
  }
  {
    if ($0 !~ /^[0-9]+\t/) next;
    n = split($0, f, "\t");
    if (n < 4) next;
    comp = f[1] + 0;
    filecolon = f[2];
    name = f[3];

    pos = match(filecolon, /:[0-9]+$/);
    file = (pos > 0) ? substr(filecolon, 1, pos - 1) : filecolon;

    if (comp < 30) next;

    printf "WARN %s %s %d >= 30\n", filecolon, name, comp;
    warn_count++;

    key = file SUBSEP name;
    dir = file; sub(/\/[^\/]*$/, "", dir);
    dkey = dir SUBSEP name;
    if (key in budget_c || dkey in budget_dir) {
      pinned = (key in budget_c) ? budget_c[key] : budget_dir[dkey];
      if (comp > pinned) {
        printf "FAIL %s %s %d > listed %d\n", filecolon, name, comp, pinned;
        fail_count++;
      }
    }
  }
  END {
    printf "listed: %d functions; at or over 30: %d\n", budget_count, warn_count;
    exit (fail_count > 0) ? 1 : 0;
  }
' "$TMP/scan.out"
