#!/usr/bin/env bash
# Validate only the five explicitly built CI candidates, then publish their
# checksum/provenance manifest. Arguments: fresh binary dir, build-start marker,
# and the actual checkout SHA captured immediately before the build.
set -euo pipefail

python3 - "$@" <<'PY'
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys


CANDIDATES = (
    ("omnipus-linux-amd64", "linux/amd64"),
    ("omnipus-linux-arm64", "linux/arm64"),
    ("omnipus-darwin-arm64", "darwin/arm64"),
    ("omnipus-darwin-amd64", "darwin/amd64"),
    ("omnipus-windows-amd64.exe", "windows/amd64"),
)
CONTEXT_KEYS = {
    "repository": "GITHUB_REPOSITORY",
    "workflow": "GITHUB_WORKFLOW",
    "workflow_ref": "GITHUB_WORKFLOW_REF",
    "run_id": "GITHUB_RUN_ID",
    "run_attempt": "GITHUB_RUN_ATTEMPT",
    "event_name": "GITHUB_EVENT_NAME",
    "ref": "GITHUB_REF",
    "event_commit_sha": "GITHUB_SHA",
}


def checkout_sha():
    return subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()


def main():
    if len(sys.argv) != 4:
        raise ValueError("usage: ci-candidate-artifacts.sh BINARY_DIR BUILD_START_MARKER BUILT_COMMIT_SHA")
    binary_dir, marker = Path(sys.argv[1]), Path(sys.argv[2])
    built_sha = sys.argv[3]
    if not re.fullmatch(r"[0-9a-f]{40}", built_sha):
        raise ValueError("invalid built commit SHA")
    if checkout_sha() != built_sha:
        raise ValueError("checkout changed after candidate build started")
    if not stat.S_ISDIR(binary_dir.lstat().st_mode):
        raise ValueError("candidate directory is not a regular directory")
    marker_stat = marker.lstat()
    if not stat.S_ISREG(marker_stat.st_mode):
        raise ValueError("build-start marker is not a regular file")

    inputs = []
    for filename, platform in CANDIDATES:
        path = binary_dir / filename
        try:
            info = path.lstat()
        except FileNotFoundError:
            raise ValueError(f"missing candidate binary: {filename}") from None
        if not stat.S_ISREG(info.st_mode):
            raise ValueError(f"candidate binary is not a regular file: {filename}")
        if info.st_size == 0:
            raise ValueError(f"empty candidate binary: {filename}")
        if info.st_mtime_ns < marker_stat.st_mtime_ns:
            raise ValueError(f"stale candidate binary: {filename}")
        inputs.append((path, platform, info))
    allowed = {filename for filename, _ in CANDIDATES}
    for path in sorted(binary_dir.iterdir()):
        if path.name not in allowed:
            raise ValueError(f"unexpected candidate entry: {path.name}")

    # Deliberate allowlist: never serialize the environment or event payload.
    context = {}
    for key, variable in CONTEXT_KEYS.items():
        value = os.environ.get(variable, "")
        if not value:
            raise ValueError(f"missing creation context: {variable}")
        context[key] = value
    pr_head = os.environ.get("PR_HEAD_SHA") or None
    if pr_head is not None and not re.fullmatch(r"[0-9a-f]{40}", pr_head):
        raise ValueError("invalid pull request head SHA")
    context["pull_request_head_sha"] = pr_head
    context["checkout_differs_from_pull_request_head"] = (built_sha != pr_head) if pr_head else None
    context["artifact_name"] = f"candidate-binaries-{built_sha}-{context['run_id']}-{context['run_attempt']}"

    binaries = []
    for path, platform, info in inputs:
        digest = hashlib.sha256()
        with path.open("rb") as stream:
            while chunk := stream.read(1024 * 1024):
                digest.update(chunk)
        after = path.lstat()
        if (info.st_ino, info.st_size, info.st_mtime_ns, info.st_mode) != (
                after.st_ino, after.st_size, after.st_mtime_ns, after.st_mode):
            raise ValueError(f"candidate binary changed during validation: {path.name}")
        binaries.append({
            "filename": path.name, "platform": platform, "test_only": True,
            "size_bytes": info.st_size, "sha256": digest.hexdigest(),
        })
    if checkout_sha() != built_sha:
        raise ValueError("checkout changed after candidate build started")
    manifest = {
        "schema_version": 1, "purpose": "test-only-candidates",
        "built_commit_sha": built_sha, "build_tags": ["goolm", "stdjson"],
        "cgo_enabled": "0", "created_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "creation_context": context, "binaries": binaries,
    }
    # Exclusive creation prevents a stale manifest being silently reused.
    manifest_path = binary_dir / "manifest.json"
    with manifest_path.open("x", encoding="utf-8") as stream:
        json.dump(manifest, stream, indent=2)
        stream.write("\n")
    if not stat.S_ISREG(manifest_path.lstat().st_mode) or manifest_path.stat().st_size == 0:
        raise ValueError("manifest is not a nonempty regular file")
    print(f"Validated {len(binaries)} test-only candidate binaries for checkout {built_sha}")


try:
    main()
except (ValueError, OSError, subprocess.CalledProcessError) as error:
    print(f"candidate artifacts: {error}", file=sys.stderr)
    sys.exit(1)
PY
