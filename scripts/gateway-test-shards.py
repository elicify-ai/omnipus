#!/usr/bin/env python3
"""Partition gateway tests for parallel binary execution.

Usage:
  python3 scripts/gateway-test-shards.py list N
      Emit N lines of regex patterns, one per shard.
  python3 scripts/gateway-test-shards.py check
      Verify the partition is complete and non-overlapping.

Partitioning: deterministic hash of test name modulo N.
This ensures the same test always goes to the same shard across runs.
"""

import argparse
import hashlib
import re
import subprocess
import sys
from typing import List, Tuple


def get_test_names(binary_path: str = None) -> List[str]:
    """Get all test names from a compiled test binary or via go test."""
    if binary_path:
        result = subprocess.run(
            [binary_path, "-test.list", ".*"],
            capture_output=True,
            text=True,
            check=True,
        )
        names = [line for line in result.stdout.splitlines() if line.startswith("Test")]
    else:
        # Fallback: use go test -list (requires build tags handled by caller)
        result = subprocess.run(
            ["go", "test", "-list", ".*", "./pkg/gateway"],
            capture_output=True,
            text=True,
            check=True,
        )
        names = [line for line in result.stdout.splitlines() if line.startswith("Test")]
    return names


def partition_tests(names: List[str], n: int) -> List[List[str]]:
    """Partition test names into n shards using hash modulo."""
    shards: List[List[str]] = [[] for _ in range(n)]
    for name in names:
        h = int(hashlib.sha256(name.encode()).hexdigest(), 16)
        shard = h % n
        shards[shard].append(name)
    return shards


def names_to_regex(names: List[str]) -> str:
    """Convert a list of test names to a -run regex.

    Uses alternation with word boundaries to avoid partial matches.
    For large lists, we use a more efficient form.
    """
    if not names:
        return "^$"  # Match nothing
    if len(names) == 1:
        return f"^{re.escape(names[0])}$"
    # Build alternation: ^(TestA|TestB|...)$
    escaped = "|".join(re.escape(n) for n in names)
    return f"^({escaped})$"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("cmd", choices=("list", "check", "count"))
    parser.add_argument("n", type=int, nargs="?", default=4, help="Number of shards (for list)")
    parser.add_argument("--binary", help="Path to pre-compiled test binary")
    args = parser.parse_args()

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

    if args.cmd == "list":
        for shard in shards:
            regex = names_to_regex(shard)
            print(regex)


if __name__ == "__main__":
    main()
