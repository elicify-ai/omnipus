#!/usr/bin/env python3
"""Dispatch-derived fixture tests; never compile Go or access GitHub.

The real publisher runs in a temporary Git repository with exactly the five
candidate names from the task. SHA256 expectations derive from fixture bytes.
Missing, empty, nonregular, outside/symlink, stale and extra inputs must exit 1
without creating a manifest. Successful inputs must preserve checkout/PR-head
provenance and must not copy unrelated environment values into the manifest.
"""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import unittest


PUBLISHER = Path(__file__).with_name("ci-candidate-artifacts.sh")
# Independent oracle: these names/platforms are specified by the dispatch.
CANDIDATES = (
    ("omnipus-linux-amd64", "linux/amd64"),
    ("omnipus-linux-arm64", "linux/arm64"),
    ("omnipus-darwin-arm64", "darwin/arm64"),
    ("omnipus-darwin-amd64", "darwin/amd64"),
    ("omnipus-windows-amd64.exe", "windows/amd64"),
)


class CandidateArtifactsTest(unittest.TestCase):
    def setUp(self):
        fixture_root = Path(__file__).resolve().parent.parent / "build" / "candidate-artifact-fixtures"
        fixture_root.mkdir(parents=True, exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(prefix="candidate-fixture-", dir=fixture_root)
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        subprocess.run(
            ["git", "-c", "user.name=daniel-piatkowski-ai", "-c", "user.email=10800669+daniel-piatkowski-ai@users.noreply.github.com",
             "commit", "-q", "--allow-empty", "-m", "candidate fixture"],
            cwd=self.root, check=True,
        )
        self.sha = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=self.root, text=True).strip()
        self.marker = self.root / "build-started"
        self.marker.touch()
        self.started_ns = time.time_ns()
        os.utime(self.marker, ns=(self.started_ns, self.started_ns))
        self.binaries = self.root / "binaries"
        self.binaries.mkdir()
        self.payloads = {}
        for name, _ in CANDIDATES:
            payload = ("synthetic candidate: " + name + "\n").encode()
            self.payloads[name] = payload
            path = self.binaries / name
            path.write_bytes(payload)
            os.utime(path, ns=(self.started_ns + 1, self.started_ns + 1))
        self.env = {
            **os.environ,
            "GITHUB_REPOSITORY": "elicify-ai/omnipus",
            "GITHUB_WORKFLOW": "build",
            "GITHUB_WORKFLOW_REF": "elicify-ai/omnipus/.github/workflows/build.yml@refs/pull/9/merge",
            "GITHUB_RUN_ID": "12345",
            "GITHUB_RUN_ATTEMPT": "2",
            "GITHUB_EVENT_NAME": "pull_request",
            "GITHUB_REF": "refs/pull/9/merge",
            "GITHUB_SHA": "1" * 40,
            "PR_HEAD_SHA": "2" * 40,
            "UNRELATED_SECRET": "never-publish-this-secret-sentinel",
        }

    def invoke(self, sha=None):
        result = subprocess.run(
            ["bash", str(PUBLISHER), str(self.binaries), str(self.marker), sha or self.sha],
            cwd=self.root, env=self.env, capture_output=True, text=True,
        )
        print(f"fixture={self.id().rsplit('.', 1)[-1]} publisher_exit={result.returncode}")
        if result.stderr:
            print(result.stderr.strip())
        return result

    def assert_rejected(self, error, sha=None):
        result = self.invoke(sha)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertEqual(result.stderr, "candidate artifacts: " + error + "\n")
        self.assertFalse((self.binaries / "manifest.json").exists(), "failure published a manifest")

    def manifest(self):
        result = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads((self.binaries / "manifest.json").read_text())

    def test_valid_fixture_has_exact_checksums_platforms_and_merge_provenance(self):
        manifest = self.manifest()
        self.assertEqual(manifest["schema_version"], 1)
        self.assertEqual(manifest["purpose"], "test-only-candidates")
        self.assertEqual(manifest["built_commit_sha"], self.sha)
        self.assertEqual(manifest["build_tags"], ["goolm", "stdjson"])
        self.assertEqual(manifest["cgo_enabled"], "0")
        self.assertEqual(manifest["binaries"], [
            {"filename": name, "platform": platform, "test_only": True,
             "size_bytes": len(self.payloads[name]),
             "sha256": hashlib.sha256(self.payloads[name]).hexdigest()}
            for name, platform in CANDIDATES
        ])
        self.assertEqual(manifest["creation_context"], {
            "repository": "elicify-ai/omnipus", "workflow": "build",
            "workflow_ref": self.env["GITHUB_WORKFLOW_REF"],
            "run_id": "12345", "run_attempt": "2", "event_name": "pull_request",
            "ref": "refs/pull/9/merge", "event_commit_sha": "1" * 40,
            "pull_request_head_sha": "2" * 40,
            "checkout_differs_from_pull_request_head": True,
            "artifact_name": f"candidate-binaries-{self.sha}-12345-2",
        })
        self.assertRegex(manifest["created_at"], r"^\d{4}-\d{2}-\d{2}T.*\+00:00$")
        self.assertNotIn(self.env["UNRELATED_SECRET"], json.dumps(manifest))
        self.assertEqual(sorted(path.name for path in self.binaries.iterdir()),
                         sorted([name for name, _ in CANDIDATES] + ["manifest.json"]))

    def test_direct_checkout_records_no_pr_head_difference(self):
        self.env["PR_HEAD_SHA"] = self.sha
        context = self.manifest()["creation_context"]
        self.assertEqual(context["pull_request_head_sha"], self.sha)
        self.assertIs(context["checkout_differs_from_pull_request_head"], False)

    def test_non_pr_context_records_null_pr_provenance(self):
        self.env["PR_HEAD_SHA"] = ""
        self.env["GITHUB_EVENT_NAME"] = "workflow_dispatch"
        context = self.manifest()["creation_context"]
        self.assertIsNone(context["pull_request_head_sha"])
        self.assertIsNone(context["checkout_differs_from_pull_request_head"])

    def test_missing_each_candidate_is_rejected(self):
        for name, _ in CANDIDATES:
            with self.subTest(name=name):
                path = self.binaries / name
                path.unlink()
                self.assert_rejected(f"missing candidate binary: {name}")
                path.write_bytes(self.payloads[name])
                os.utime(path, ns=(self.started_ns + 1, self.started_ns + 1))

    def test_zero_bytes_is_rejected(self):
        name = "omnipus-windows-amd64.exe"
        (self.binaries / name).write_bytes(b"")
        self.assert_rejected(f"empty candidate binary: {name}")

    def test_one_byte_is_accepted(self):
        name = "omnipus-windows-amd64.exe"
        (self.binaries / name).write_bytes(b"x")
        binary = self.manifest()["binaries"][-1]
        self.assertEqual(binary["size_bytes"], 1)
        self.assertEqual(binary["sha256"], hashlib.sha256(b"x").hexdigest())

    def test_directory_in_place_of_binary_is_rejected(self):
        name = "omnipus-linux-amd64"
        path = self.binaries / name
        path.unlink()
        path.mkdir()
        self.assert_rejected(f"candidate binary is not a regular file: {name}")

    def test_outside_symlink_is_rejected(self):
        name = "omnipus-linux-amd64"
        outside = self.root / "credentials.json"
        outside.write_bytes(b"synthetic confidential fixture")
        path = self.binaries / name
        path.unlink()
        path.symlink_to(outside)
        self.assert_rejected(f"candidate binary is not a regular file: {name}")

    def test_unlisted_files_and_directories_are_rejected(self):
        for name in ("credentials.json", "config.json", "home", "source.go", "node_modules", "build.log", ".env"):
            with self.subTest(name=name):
                path = self.binaries / name
                if name in ("home", "node_modules"):
                    path.mkdir()
                else:
                    path.write_bytes(b"must not publish")
                self.assert_rejected(f"unexpected candidate entry: {name}")
                if path.is_dir():
                    path.rmdir()
                else:
                    path.unlink()

    def test_binary_older_than_build_marker_is_rejected(self):
        name = "omnipus-darwin-amd64"
        os.utime(self.binaries / name, ns=(self.started_ns - 1, self.started_ns - 1))
        self.assert_rejected(f"stale candidate binary: {name}")

    def test_binary_at_build_marker_is_accepted(self):
        name = "omnipus-darwin-amd64"
        os.utime(self.binaries / name, ns=(self.started_ns, self.started_ns))
        self.assertEqual(self.manifest()["binaries"][3]["filename"], name)

    def test_old_manifest_is_not_overwritten(self):
        path = self.binaries / "manifest.json"
        path.write_bytes(b"stale manifest")
        result = self.invoke()
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stderr, "candidate artifacts: unexpected candidate entry: manifest.json\n")
        self.assertEqual(path.read_bytes(), b"stale manifest")

    def test_changed_checkout_is_rejected(self):
        self.assert_rejected("checkout changed after candidate build started", sha="3" * 40)

    def test_missing_creation_context_is_rejected(self):
        self.env.pop("GITHUB_RUN_ID")
        self.assert_rejected("missing creation context: GITHUB_RUN_ID")


if __name__ == "__main__":
    unittest.main(verbosity=2)
