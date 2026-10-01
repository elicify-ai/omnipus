#!/usr/bin/env python3
"""Partition the existing PR Go package sets without narrowing coverage.

Usage: python3 scripts/go-test-shards.py test|race matrix|check|<shard>
`matrix` emits the shard names used by BOTH workflow matrices (no Go required).
`check` prints the complete partition and rejects missing/duplicate packages.
Other commands print only the selected shard's package arguments, one per line.

Weights are package seconds from successful Tests/Race Tests jobs in PR #1115,
run 36813561528 (2026-10-01), checked against the last successful release run,
34449721906. They balance work, NOT select coverage: every newly discovered
package is assigned too. Package seconds are not job wall-clock estimates.
"""

import argparse
from collections import Counter
import json
from pathlib import Path
import subprocess
import sys


MODULE = "github.com/elicify-ai/omnipus/"
SHARDS = ("gateway", "agent", "group-1", "group-2")
# Small/unmeasured packages get weight 1. Gateway and agent are dedicated.
SECONDS = {
    "test": {
        "pkg/sandbox": 104.937,
        "pkg/tools/browser/webrtc": 88.516,
        "pkg/tools/browser": 83.461,
        "pkg/providers": 77.204,
        "pkg/knowledge": 70.410,
        "pkg/records/propindex": 55.958,
        "tests/adr091": 47.932,
        "tests/integration": 41.838,
        "pkg/tools": 36.586,
        "pkg/channels": 17.475,
        "pkg/sysagent/tools": 16.161,
        "pkg/skills": 13.947,
        "pkg/media/resize": 13.495,
        "pkg/cron": 12.785,
        "pkg/credentials": 8.166,
        "tests/security": 6.962,
        "tests/e2e": 5.519,
        "pkg/entity": 5.479,
        "pkg/session": 5.096,
    },
    "race": {
        "pkg/tools/browser": 641.723,
        "pkg/tools/browser/webrtc": 95.114,
        "pkg/tools": 90.215,
        "pkg/providers": 81.741,
        "tests/integration": 68.225,
        "pkg/channels": 18.935,
        "pkg/credentials": 15.751,
        "pkg/sysagent/tools": 15.050,
        "pkg/config": 13.535,
        "pkg/entity": 7.387,
        "pkg/providers/catalog": 6.194,
        "pkg/task": 5.672,
        "pkg/tools/browser/cdppipe": 5.296,
    },
}


def package_set(mode):
    if mode == "test":
        # Match the original job's `go list ./...` exactly. Adding tags here
        # changes discovery; the actual test commands still use goolm,stdjson.
        command = ["go", "list", "./..."]
    else:
        # Keep the race list canonical, including recursive subpackages.
        script = Path(__file__).with_name("race-packages.sh")
        patterns = subprocess.check_output(["bash", str(script)], text=True).split()
        if not patterns:
            raise ValueError("race-packages.sh produced no package patterns")
        command = ["go", "list", "-tags", "goolm,stdjson", *patterns]
    packages = set(subprocess.check_output(command, text=True).splitlines())
    if not packages:
        raise ValueError("go list produced no packages")
    return packages


def partition(packages, mode):
    shards = {name: [] for name in SHARDS}
    remaining = []
    for package in sorted(packages):
        path = package.removeprefix(MODULE)
        if path == "pkg/gateway" or (mode == "race" and path.startswith("pkg/gateway/")):
            shards["gateway"].append(package)
        elif path == "pkg/agent" or path.startswith("pkg/agent/"):
            shards["agent"].append(package)
        else:
            remaining.append(package)
    # Longest packages first; assign to the lighter of the two general shards.
    weights = SECONDS[mode]
    loads = {"group-1": 0.0, "group-2": 0.0}
    for package in sorted(remaining, key=lambda p: (-weights.get(p.removeprefix(MODULE), 1), p)):
        shard = min(loads, key=lambda name: (loads[name], name))
        shards[shard].append(package)
        loads[shard] += weights.get(package.removeprefix(MODULE), 1)
    return shards


def check(packages, shards):
    assigned = Counter(package for group in shards.values() for package in group)
    missing = sorted(packages - assigned.keys())
    extra = sorted(assigned.keys() - packages)
    duplicates = sorted(package for package, count in assigned.items() if count != 1)
    empty = sorted(name for name, group in shards.items() if not group)
    if missing or extra or duplicates or empty:
        raise ValueError(f"invalid partition: missing={missing}, extra={extra}, duplicates={duplicates}, empty={empty}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=SECONDS)
    parser.add_argument("shard", choices=("matrix", "check", *SHARDS))
    args = parser.parse_args()
    if args.shard == "matrix":
        print(json.dumps(SHARDS))
        return
    packages = package_set(args.mode)
    shards = partition(packages, args.mode)
    check(packages, shards)
    if args.shard == "check":
        for name, group in shards.items():
            print(f"{name}: {len(group)} packages")
            for package in sorted(group):
                print(f"  {package}")
        print(f"OK: {len(packages)} packages assigned exactly once across {len(shards)} shards")
    else:
        print("\n".join(sorted(shards[args.shard])))


if __name__ == "__main__":
    try:
        main()
    except (ValueError, subprocess.CalledProcessError) as error:
        print(f"ERROR: {error}", file=sys.stderr)
        sys.exit(1)
