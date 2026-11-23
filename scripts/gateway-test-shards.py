#!/usr/bin/env python3
"""Partition Go package tests for parallel binary execution.

Usage:
  python3 scripts/gateway-test-shards.py list N --binary B
      Emit N bounded regex patterns, one per shard; reject oversized shards.
  python3 scripts/gateway-test-shards.py chunks N --binary B --out-dir D
      Emit N absolute file paths, one per shard. Each file contains bounded
      regex patterns (one per line) to run sequentially within that shard.
  python3 scripts/gateway-test-shards.py check
      Verify the partition is complete and non-overlapping.
  python3 scripts/gateway-test-shards.py count --binary B
      Print the number of runnable tests.
  python3 scripts/gateway-test-shards.py verify --binary B --log L
      Completeness check for a sharded run (run-test review 2026-10-30,
      HIGH 3: nothing used to prove the shards together ran the package).
      Every runnable test listed by the binary must have at least one
      top-level `--- PASS|FAIL|SKIP` result line in the combined shard
      log; exits 1 printing the missing names otherwise. Both this and
      `list`/`chunks` derive the expected set from the SAME binary with the SAME
      runnable-name rule, so the shards and the check cannot disagree
      about what should have run.

Partitioning: deterministic hash of test name modulo N.
This ensures the same test always goes to the same shard across runs.

Which `-test.list` names are partitioned — fixture-verified 2026-10-30
against a compiled `go test -c` binary of a package holding all four kinds
(Test, Benchmark, Fuzz, Example with and without an Output comment):

  Prefix      Listed by -test.list        Runs under a plain binary run?
  ----------  --------------------------  ------------------------------------
  Test*       yes                         yes; reports `--- PASS/FAIL/SKIP:`
  Fuzz*       yes                         yes (seed corpus); reports like a test
  Example*    only those WITH a           yes; reports like a test
              `// Output:` comment —
              output-less examples are
              not listed at all
  Benchmark*  yes                         NO — needs -test.bench; never reports

So the runnable set is Test*/Fuzz*/Example*: exactly what -test.list emits
minus Benchmark*. `list`/`chunks` partition it and `verify` expects it, so a listed
test can no longer silently miss every shard regex (the old filter kept
only `Test`-prefixed names, dropping Fuzz*/Example* — none existed in
pkg/agent or pkg/gateway at fix time, i.e. a latent gap).
"""

import argparse
import hashlib
import re
import subprocess
import sys
from pathlib import Path
from typing import List, Set

# Linux limits each argv string to MAX_ARG_STRLEN (131072 bytes on CI's
# 4 KiB pages), regardless of the larger total ARG_MAX budget. Leave room
# for the terminating NUL and count UTF-8 bytes, including regex escaping.
MAX_RUN_REGEX_BYTES = 100000

# Names matching these prefixes are executed by a plain run of the compiled
# test binary and report a top-level result line. See the module docstring
# for the fixture evidence; Benchmark* is deliberately absent (listed by
# -test.list but never executed without -test.bench, so it can never have a
# result line and would make the completeness check fail forever).
RUNNABLE_PREFIXES = ("Test", "Fuzz", "Example")


def is_runnable(name: str) -> bool:
    """Whether a -test.list name runs and reports under a plain binary run."""
    return name.startswith(RUNNABLE_PREFIXES)


def get_test_names(binary_path: str = None) -> List[str]:
    """Get all runnable test names from a compiled test binary or via go test."""
    if binary_path:
        result = subprocess.run(
            [binary_path, "-test.list", ".*"],
            capture_output=True,
            text=True,
            check=True,
        )
        listed = result.stdout.splitlines()
    else:
        # Fallback: use go test -list (requires build tags handled by caller)
        result = subprocess.run(
            ["go", "test", "-list", ".*", "./pkg/gateway"],
            capture_output=True,
            text=True,
            check=True,
        )
        listed = result.stdout.splitlines()
    return [name for name in listed if is_runnable(name)]


def partition_tests(names: List[str], n: int) -> List[List[str]]:
    """Partition test names into n shards using hash modulo."""
    shards: List[List[str]] = [[] for _ in range(n)]
    for name in names:
        h = int(hashlib.sha256(name.encode()).hexdigest(), 16)
        shard = h % n
        shards[shard].append(name)
    return shards


def names_to_regex(names: List[str]) -> str:
    """Build an exact-name -test.run regex, refusing unsafe argv lengths."""
    if not names:
        regex = "^$"  # Match nothing
    elif len(names) == 1:
        regex = f"^{re.escape(names[0])}$"
    else:
        escaped = "|".join(re.escape(n) for n in names)
        regex = f"^({escaped})$"
    size = len(regex.encode("utf-8"))
    if size > MAX_RUN_REGEX_BYTES:
        raise ValueError(
            f"-test.run regex is {size} UTF-8 bytes; limit is {MAX_RUN_REGEX_BYTES} bytes"
        )
    return regex


def names_to_regex_chunks(names: List[str]) -> List[str]:
    """Split one shard into bounded regexes without changing its test order."""
    regexes: List[str] = []
    chunk: List[str] = []
    name_bytes = 0
    for name in names:
        escaped_bytes = len(re.escape(name).encode("utf-8"))
        # Fail clearly if even the singleton cannot fit, before writing a
        # chunk manifest or attempting exec with an oversized argument.
        if escaped_bytes + 2 > MAX_RUN_REGEX_BYTES:
            names_to_regex([name])
        # A singleton has two anchor bytes. Multiple names have four
        # anchor/group bytes plus one separator per existing chunk member.
        candidate_bytes = name_bytes + escaped_bytes + (len(chunk) + 4 if chunk else 2)
        if chunk and candidate_bytes > MAX_RUN_REGEX_BYTES:
            regexes.append(names_to_regex(chunk))
            chunk = []
            name_bytes = 0
        chunk.append(name)
        name_bytes += escaped_bytes
    # Empty shards still get a match-nothing invocation, never an empty
    # -test.run (which would match the entire test binary).
    regexes.append(names_to_regex(chunk))
    return regexes


# Top-level result lines only: `-v` prints subtest results indented and with
# the parent path (`    --- PASS: TestX/sub (0.00s)`), so anchoring at column
# zero and excluding `/` from the name captures exactly one result per
# top-level test (a parent of failed subtests reports its own top-level
# `--- FAIL:` line).
_RESULT_LINE = re.compile(r"^--- (?:PASS|FAIL|SKIP): ([^/]+) \(")


def result_names_from_log(log_path: str) -> Set[str]:
    """Distinct top-level test names with a PASS/FAIL/SKIP result in a log."""
    reported: Set[str] = set()
    with open(log_path, encoding="utf-8", errors="replace") as handle:
        for line in handle:
            match = _RESULT_LINE.match(line)
            if match:
                reported.add(match.group(1))
    return reported


def verify_log(binary_path: str, log_path: str) -> int:
    """Exit 0 iff every runnable listed test has a result in the shard log."""
    expected = get_test_names(binary_path)
    if not expected:
        print("ERROR: no runnable tests listed by the binary", file=sys.stderr)
        return 1
    try:
        reported = result_names_from_log(log_path)
    except OSError as error:
        print(f"ERROR: cannot read shard log {log_path}: {error}", file=sys.stderr)
        return 1
    missing = sorted(set(expected) - reported)
    if missing:
        print(
            f"completeness: FAILED — {len(missing)} of {len(expected)} runnable "
            f"listed tests have no --- PASS/FAIL/SKIP result in {log_path}:",
            file=sys.stderr,
        )
        for name in missing:
            print(f"  MISSING RESULT: {name}", file=sys.stderr)
        return 1
    print(
        f"completeness: OK — {len(expected)}/{len(expected)} runnable listed "
        f"tests have top-level results ({len(reported)} distinct result names)"
    )
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("cmd", choices=("list", "chunks", "check", "count", "verify"))
    parser.add_argument("n", type=int, nargs="?", default=4, help="Number of shards (for list/chunks)")
    parser.add_argument("--binary", help="Path to pre-compiled test binary")
    parser.add_argument("--log", help="Combined shard log (for verify)")
    parser.add_argument("--out-dir", help="Directory for per-shard regex files (for chunks)")
    args = parser.parse_args()
    if args.cmd == "chunks" and not args.out_dir:
        parser.error("chunks requires --out-dir")

    if args.cmd == "verify":
        if not args.binary or not args.log:
            parser.error("verify requires --binary and --log")
        sys.exit(verify_log(args.binary, args.log))

    names = get_test_names(args.binary)
    if not names:
        print("ERROR: no tests found", file=sys.stderr)
        sys.exit(1)

    if args.cmd == "count":
        print(f"{len(names)} tests")
        return

    shards = partition_tests(names, args.n)

    if args.cmd == "check":
        all_assigned = set()
        for i, shard in enumerate(shards):
            for name in shard:
                if name in all_assigned:
                    print(f"ERROR: duplicate assignment: {name}", file=sys.stderr)
                    sys.exit(1)
                all_assigned.add(name)
        missing = set(names) - all_assigned
        if missing:
            print(f"ERROR: unassigned tests: {missing}", file=sys.stderr)
            sys.exit(1)
        print(f"OK: {len(names)} tests partitioned into {args.n} shards")
        for i, shard in enumerate(shards):
            print(f"  shard {i}: {len(shard)} tests")
        return

    try:
        if args.cmd == "list":
            for shard in shards:
                print(names_to_regex(shard))
        elif args.cmd == "chunks":
            directory = Path(args.out_dir).resolve()
            directory.mkdir(parents=True, exist_ok=True)
            for i, shard in enumerate(shards):
                regexes = names_to_regex_chunks(shard)
                path = directory / f"shard-{i}.regexes"
                path.write_text("\n".join(regexes) + "\n", encoding="utf-8")
                print(path)
    except (ValueError, OSError) as error:
        parser.error(str(error))


if __name__ == "__main__":
    main()
