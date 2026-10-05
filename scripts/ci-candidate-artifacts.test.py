#!/usr/bin/env python3
"""Dispatch-derived fixture tests; never compile Go or access GitHub.

The real publisher runs in a temporary Git repository with exactly the five
candidate names from the task. SHA256 expectations derive from fixture bytes.
Missing, empty, nonregular, outside/symlink, stale and extra inputs must exit 1
without creating a manifest. Successful inputs must preserve checkout/PR-head
provenance and must not copy unrelated environment values into the manifest.

QA additions exercise full-byte SHA256, actual Git/file changes under controlled
process interleavings, and structured workflow condition/path-input semantics.
The workflow harness executes preparation/validation, substitutes external build
commands, and records upload-action inputs. It never certifies a GitHub transfer,
archive contents, executable validity or platform runtime acceptance.
"""

import glob
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import socket
import stat
import subprocess
import sys
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


REPOSITORY = PUBLISHER.parent.parent
WORKFLOW = REPOSITORY / ".github" / "workflows" / "build.yml"


def write_executable(path, source):
    path.write_text("#!" + sys.executable + "\n" + source)
    path.chmod(0o700)


def workflow_condition(condition, previous):
    """Documented Actions status-function subset, not a GitHub runner.

    https://docs.github.com/en/actions/reference/workflows-and-actions/expressions
    No explicit condition means success(); an explicit status function replaces
    that default. Unknown expressions fail the instrument, never default true.
    """
    if condition is None:
        function = "success"
    else:
        match = re.fullmatch(r"\s*\$\{\{\s*(success|always|failure|cancelled)\(\)\s*\}\}\s*", condition)
        if not match:
            raise ValueError(f"unsupported workflow condition: {condition!r}")
        function = match[1]
    return {
        "success": not any(status in ("failure", "cancelled") for status in previous),
        "always": True,
        "failure": "failure" in previous,
        "cancelled": "cancelled" in previous,
    }[function]


class CandidateWorkflowHarness:
    """Run the actual candidate-step suffix with external build/upload edges.

    Preparation and validation are real shell/subprocess executions. make/go are
    controlled process boundaries producing spec-derived bytes, never Go builds.
    The upload sink records resolved action INPUTS, not a network upload or ZIP.
    YAML is parsed by the repository's existing lockfile dependency after npm ci.
    All earlier SPA/fixture gates are assumed successful; that is this unit's scope.
    """
    def __init__(self, test, failure=None):
        self.test = test
        scenario = tempfile.TemporaryDirectory(prefix="workflow-", dir=test.root)
        test.addCleanup(scenario.cleanup)
        self.scenario = Path(scenario.name)
        self.workspace = self.scenario / "workspace"
        self.workspace.mkdir()
        self.runner_temp = self.scenario / "runner-temp"
        self.runner_temp.mkdir()
        self.env_file = self.scenario / "github-env"
        self.env_file.touch()
        scripts = self.workspace / "scripts"
        scripts.mkdir()
        shutil.copyfile(PUBLISHER, scripts / PUBLISHER.name)
        self.boundary = self.scenario / "external-commands"
        self.boundary.mkdir()
        self.env = {**test.env, "RUNNER_TEMP": str(self.runner_temp),
                    "GITHUB_ENV": str(self.env_file), "GITHUB_WORKSPACE": str(self.workspace),
                    "PATH": str(self.boundary) + os.pathsep + test.env["PATH"]}
        self.failure = failure
        self.records = []
        self.uploads = []
        self._build_commands()

    def _build_commands(self):
        common = "import os, pathlib, sys\n"
        common += f"names = {[name for name, _ in CANDIDATES]!r}\n"
        common += "directory = pathlib.Path(os.environ['CANDIDATE_DIR'])\n"
        common += "def candidate(name):\n"
        common += "    (directory / name).write_bytes(('workflow candidate: ' + name + '\\n').encode())\n"
        make = common + "expected = ['build-all', 'BUILD_DIR=' + str(directory), 'GO_BUILD_TAGS=goolm,stdjson']\n"
        make += "if sys.argv[1:] != expected or os.environ['CGO_ENABLED'] != '0':\n"
        make += "    raise ValueError('unexpected make boundary arguments/settings')\n"
        make += "for name in names[:-1]: candidate(name)\n"
        make += f"sys.exit({61 if self.failure == 'build' else 0})\n"
        write_executable(self.boundary / "make", make)
        go = "import os, pathlib, sys\n"
        go += "if sys.argv[1:] == ['env', 'GOVERSION']:\n    print('go1.26.6'); sys.exit(0)\n"
        go += common + "args = sys.argv[1:]\n"
        go += "if (args[:3] != ['build', '-tags', 'goolm,stdjson'] or '-ldflags' not in args\n"
        go += "        or args[-1] != './cmd/omnipus' or args[args.index('-o') + 1] != str(directory / names[-1])\n"
        go += "        or [os.environ[k] for k in ('CGO_ENABLED', 'GOOS', 'GOARCH')] != ['0', 'windows', 'amd64']):\n"
        go += "    raise ValueError('unexpected Windows build boundary arguments/settings')\n"
        go += "candidate(names[-1])\n"
        if self.failure == "validate":
            go += "(directory / names[0]).unlink()\n"
        go += f"sys.exit({62 if self.failure == 'windows' else 0})\n"
        write_executable(self.boundary / "go", go)

    def _parsed_job(self):
        program = """import fs from 'node:fs';
import {createRequire} from 'node:module';
const require = createRequire(process.cwd() + '/package.json');
const doc = require('yaml').parseDocument(fs.readFileSync(process.argv[1], 'utf8'));
if (doc.errors.length) throw doc.errors[0];
console.log(JSON.stringify(doc.toJS()));"""
        result = subprocess.run(["node", "--input-type=module", "--eval", program, str(WORKFLOW)],
                                cwd=REPOSITORY, capture_output=True, text=True)
        self.test.assertEqual(result.returncode, 0, "workflow parser failed: " + result.stderr)
        return json.loads(result.stdout)["jobs"]["build"]

    def _expand(self, value):
        if not isinstance(value, str):
            return value
        contexts = {"github.workspace": str(self.workspace),
                    "github.run_id": self.env["GITHUB_RUN_ID"],
                    "github.run_attempt": self.env["GITHUB_RUN_ATTEMPT"],
                    "github.event.pull_request.head.sha": self.env.get("PR_HEAD_SHA", "")}
        contexts.update({"env." + key: item for key, item in self.env.items()})
        def replace(match):
            key = match[1].strip()
            if key not in contexts:
                raise ValueError(f"unsupported workflow context: {key!r}")
            return contexts[key]
        return re.sub(r"\$\{\{\s*(.*?)\s*\}\}", replace, value)

    def run(self):
        job = self._parsed_job()
        self.env.update({key: self._expand(value) for key, value in job.get("env", {}).items()})
        steps = job["steps"]
        starts = [i for i, step in enumerate(steps) if step.get("name") == "Prepare candidate outputs"]
        if len(starts) != 1:
            raise ValueError("candidate workflow suffix must have one preparation step")
        previous = []
        for step in steps[starts[0]:]:
            if step.get("continue-on-error") or step.get("working-directory"):
                raise ValueError("unsupported failure demotion/working-directory in candidate suffix")
            name = step["name"]
            if not workflow_condition(step.get("if"), previous):
                self.records.append({"name": name, "status": "skipped"})
                previous.append("skipped")
                continue
            if "uses" in step:
                if step["uses"] != "actions/upload-artifact@v7":
                    raise ValueError("unsupported action in candidate suffix")
                inputs = {key: self._expand(value) for key, value in step["with"].items()}
                self.uploads.append({"action": step["uses"], "inputs": inputs,
                                     "paths": inputs["path"].splitlines()})
                self.records.append({"name": name, "status": "upload-inputs-recorded"})
                previous.append("success")
                continue
            env = {**self.env, **{key: self._expand(value) for key, value in step.get("env", {}).items()}}
            shell = ["bash", "--noprofile", "--norc", "-e"]
            if step.get("shell") == "bash":
                shell += ["-o", "pipefail"]
            elif "shell" in step:
                raise ValueError("unsupported candidate shell")
            result = subprocess.run(shell + ["-c", self._expand(step["run"])], cwd=self.workspace,
                                    env=env, capture_output=True, text=True)
            status = "success" if result.returncode == 0 else "failure"
            self.records.append({"name": name, "status": status, "exit": result.returncode,
                                 "stdout": result.stdout, "stderr": result.stderr})
            print(f"workflow_step={name!r} direct_exit={result.returncode}")
            for line in self.env_file.read_text().splitlines():
                key, value = line.split("=", 1)
                self.env[key] = value
            previous.append(status)
        return self


# Process-edge scheduler: return ONLY the bytes produced by the real git call.
# A real child commit lands after that read and before its caller can continue.
GIT_HEAD_INTERLEAVER = r'''
import json, os, pathlib, subprocess, sys
real = os.environ['INTERLEAVE_REAL_GIT']
args = sys.argv[1:]
result = subprocess.run([real, *args], capture_output=True)
trace = pathlib.Path(os.environ['INTERLEAVE_TRACE'])
if result.returncode == 0 and args == ['rev-parse', 'HEAD'] and not trace.exists():
    subprocess.run([real, '-c', 'user.name=daniel-piatkowski-ai', '-c',
                    'user.email=10800669+daniel-piatkowski-ai@users.noreply.github.com',
                    'commit', '-q', '--allow-empty', '-m', 'checkout interleaving fixture'], check=True)
    new = subprocess.check_output([real, 'rev-parse', 'HEAD'], text=True).strip()
    trace.write_text(json.dumps({'returned_sha': result.stdout.decode().strip(), 'new_sha': new}))
sys.stdout.buffer.write(result.stdout)
sys.stderr.buffer.write(result.stderr)
sys.exit(result.returncode)
'''


# Lead-approved TEST-ONLY debugger scheduler at the python3 executable boundary.
# It runs the exact stdin program, with no patched globals/modules/read/stat/hash.
# A socket handshake pauses before first binary open; the parent changes a REAL
# file and replies. Only cadence changes; the production error path stays real.
PYTHON_FILE_INTERLEAVER = r'''
import ast, bdb, hashlib, json, os, pathlib, socket, sys
if sys.argv[1] != '-':
    raise ValueError('expected real publisher stdin invocation')
sys.argv = sys.argv[1:]
source = sys.stdin.buffer.read()
program = pathlib.Path(os.environ['INTERLEAVE_SOURCE'])
program.write_bytes(source)
opens = [node.lineno for node in ast.walk(ast.parse(source)) if isinstance(node, ast.With)
         and any(isinstance(item.context_expr, ast.Call)
                 and isinstance(item.context_expr.func, ast.Attribute)
                 and item.context_expr.func.attr == 'open'
                 and item.context_expr.args
                 and isinstance(item.context_expr.args[0], ast.Constant)
                 and item.context_expr.args[0].value == 'rb' for item in node.items)]
if len(opens) != 1:
    raise ValueError('debugger requires one unambiguous binary-open cut')
channel = socket.socket(fileno=int(os.environ['INTERLEAVE_FD']))
trace = pathlib.Path(os.environ['INTERLEAVE_TRACE'])
event = {'event': 'before_binary_open', 'source_sha256': hashlib.sha256(source).hexdigest(),
         'line': opens[0], 'released': False}
class InterleavingDebugger(bdb.Bdb):
    def user_line(self, frame):
        if frame.f_code.co_filename == str(program) and frame.f_lineno == opens[0]:
            trace.write_text(json.dumps(event))
            channel.sendall((json.dumps(event) + '\n').encode())
            if channel.recv(1) != b'R':
                raise ValueError('debugger resume handshake failed')
            event['released'] = True
            trace.write_text(json.dumps(event))
            self.clear_all_breaks()
        self.set_continue()
debugger = InterleavingDebugger()
error = debugger.set_break(str(program), opens[0])
if error:
    raise ValueError(error)
debugger.run(compile(source, str(program), 'exec'),
             {'__name__': '__main__', '__file__': str(program)})
'''


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

    def expected_binaries(self, payloads):
        return [{"filename": name, "platform": platform, "test_only": True,
                 "size_bytes": len(payloads[name]),
                 "sha256": hashlib.sha256(payloads[name]).hexdigest()}
                for name, platform in CANDIDATES]

    def test_full_sha256_covers_bytes_across_chunk_boundaries(self):
        # SHA256 covers ALL bytes (dispatch), regardless of implementation's
        # read size. Probe both sides of 1 MiB and a nonperiodic multi-chunk tail.
        chunk = 1024 * 1024
        name = CANDIDATES[0][0]
        for size in (chunk - 1, chunk, chunk + 1, 2 * chunk + 17):
            with self.subTest(size=size):
                (self.binaries / "manifest.json").unlink(missing_ok=True)
                payload = (bytes(range(256)) * (size // 256) + bytes(range(size % 256)))
                self.payloads[name] = payload
                (self.binaries / name).write_bytes(payload)
                manifest = self.manifest()
                self.assertEqual(manifest["binaries"], self.expected_binaries(self.payloads),
                                 f"manifest must checksum complete {size}-byte payload, not a prefix")
                self.assertEqual(manifest["built_commit_sha"], self.sha)

    def test_checkout_change_after_initial_provenance_read_is_rejected(self):
        real_git = shutil.which("git")
        self.assertIsNotNone(real_git, "fixture requires the real Git process")
        boundary = self.root / "git-boundary"
        boundary.mkdir()
        trace = boundary / "transition.json"
        write_executable(boundary / "git", GIT_HEAD_INTERLEAVER)
        self.env.update({"PATH": str(boundary) + os.pathsep + self.env["PATH"],
                         "INTERLEAVE_REAL_GIT": real_git, "INTERLEAVE_TRACE": str(trace)})
        publisher_hash = hashlib.sha256(PUBLISHER.read_bytes()).hexdigest()
        result = self.invoke()
        transition = json.loads(trace.read_text())
        current_sha = subprocess.check_output([real_git, "rev-parse", "HEAD"],
                                              cwd=self.root, text=True).strip()
        self.assertEqual(transition["returned_sha"], self.sha,
                         "initial provenance read must return real pre-change HEAD")
        self.assertEqual(transition["new_sha"], current_sha,
                         "interleaver must change actual repository HEAD, not fabricate its output")
        self.assertNotEqual(current_sha, self.sha, "controlled successor commit did not land")
        self.assertEqual(hashlib.sha256(PUBLISHER.read_bytes()).hexdigest(), publisher_hash,
                         "scheduler must not edit publisher source")
        print("real_head_transition=" + json.dumps(transition, sort_keys=True))
        self.assertEqual(result.returncode, 1, "checkout changed during validation but was accepted")
        self.assertEqual(result.stderr, "candidate artifacts: checkout changed after candidate build started\n")
        self.assertFalse((self.binaries / "manifest.json").exists(), "changed checkout published a manifest")

    def invoke_with_file_interleaving(self, change_mode):
        boundary = Path(tempfile.mkdtemp(prefix="python-boundary-", dir=self.root))
        write_executable(boundary / "python3", PYTHON_FILE_INTERLEAVER)
        publisher_bytes = PUBLISHER.read_bytes()
        source = publisher_bytes.split(b"<<'PY'\n", 1)[1].rsplit(b"\nPY\n", 1)[0] + b"\n"
        source_file, trace = boundary / "exact-publisher.py", boundary / "schedule.json"
        path = self.binaries / CANDIDATES[0][0]
        original_mode = stat.S_IMODE(path.stat().st_mode)
        parent, child = socket.socketpair()
        with parent, child:
            # Fixed protocol hang guard, NOT a timing/assertion tolerance.
            parent.settimeout(30)
            env = {**self.env, "PATH": str(boundary) + os.pathsep + self.env["PATH"],
                   "INTERLEAVE_SOURCE": str(source_file), "INTERLEAVE_TRACE": str(trace),
                   "INTERLEAVE_FD": str(child.fileno())}
            command = ["bash", str(PUBLISHER), str(self.binaries), str(self.marker), self.sha]
            process = subprocess.Popen(command, cwd=self.root, env=env, pass_fds=(child.fileno(),),
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            child.close()
            try:
                with parent.makefile("rb") as channel:
                    event = json.loads(channel.readline())
                    self.assertEqual(event["event"], "before_binary_open")
                    self.assertEqual(event["source_sha256"], hashlib.sha256(source).hexdigest(),
                                     "debugger must execute the exact publisher stdin bytes")
                    self.assertIs(event["released"], False, "file change must precede resume")
                    self.assertEqual(source_file.read_bytes(), source)
                    before = path.stat()
                    if change_mode:
                        path.chmod(original_mode ^ stat.S_IXUSR)
                    after = path.stat()
                    self.assertEqual((after.st_ino, after.st_size, after.st_mtime_ns),
                                     (before.st_ino, before.st_size, before.st_mtime_ns),
                                     "scheduler must change only real file mode, not contents/identity")
                    self.assertEqual(stat.S_IMODE(after.st_mode),
                                     original_mode ^ stat.S_IXUSR if change_mode else original_mode,
                                     "interleaving must act on a real candidate file")
                    print(f"file_gate line={event['line']} source_sha256={event['source_sha256']} "
                          f"mode_before={stat.S_IMODE(before.st_mode):o} mode_after={stat.S_IMODE(after.st_mode):o} "
                          "phase=real-file-change-before-resume")
                    parent.sendall(b"R")
                stdout, stderr = process.communicate(timeout=30)
            finally:
                if process.poll() is None:
                    process.kill()
                    process.wait()
        schedule = json.loads(trace.read_text())
        self.assertIs(schedule["released"], True, "publisher never resumed from controlled cut")
        self.assertEqual(PUBLISHER.read_bytes(), publisher_bytes, "publisher source changed during scheduling")
        print(f"file_interleaving changed_mode={change_mode} source_sha256={event['source_sha256']} "
              f"released={schedule['released']} publisher_exit={process.returncode}")
        return subprocess.CompletedProcess(command, process.returncode, stdout, stderr)

    def test_file_metadata_change_during_validation_is_rejected(self):
        # Positive scheduling control: pausing/resuming alone must still validate.
        control = self.invoke_with_file_interleaving(change_mode=False)
        self.assertEqual(control.returncode, 0, control.stdout + control.stderr)
        manifest_path = self.binaries / "manifest.json"
        self.assertEqual(json.loads(manifest_path.read_text())["binaries"], self.expected_binaries(self.payloads))
        manifest_path.unlink()
        changed = self.invoke_with_file_interleaving(change_mode=True)
        self.assertEqual(changed.returncode, 1, "metadata changed during validation but was accepted")
        self.assertEqual(changed.stderr,
                         f"candidate artifacts: candidate binary changed during validation: {CANDIDATES[0][0]}\n")
        self.assertFalse(manifest_path.exists(), "unstable candidate published a manifest")

    def test_workflow_failed_build_or_validation_never_reaches_upload(self):
        # Instrument truth table derived from current Actions status-function docs.
        for previous, success, failure, cancelled in [([], True, False, False),
                (["success"], True, False, False), (["failure"], False, True, False),
                (["failure", "skipped"], False, True, False), (["cancelled"], False, False, True)]:
            with self.subTest(previous=previous):
                self.assertEqual(workflow_condition(None, previous), success)
                self.assertEqual(workflow_condition("${{ success() }}", previous), success)
                self.assertIs(workflow_condition("${{ always() }}", previous), True)
                self.assertEqual(workflow_condition("${{ failure() }}", previous), failure)
                self.assertEqual(workflow_condition("${{ cancelled() }}", previous), cancelled)
        with self.assertRaisesRegex(ValueError, "^unsupported workflow condition:"):
            workflow_condition("${{ unmodelled() }}", [])
        for stage, failed_step, exit_code in (("build", "Build", 61),
                ("windows", "Build Windows candidate (test-only)", 62),
                ("validate", "Validate candidate binaries and manifest", 1)):
            with self.subTest(stage=stage):
                workflow = CandidateWorkflowHarness(self, failure=stage).run()
                failures = [record for record in workflow.records if record["status"] == "failure"]
                self.assertEqual([(record["name"], record["exit"]) for record in failures],
                                 [(failed_step, exit_code)], "required real failure did not execute")
                if stage == "validate":
                    self.assertEqual(failures[0]["stderr"],
                                     "candidate artifacts: missing candidate binary: omnipus-linux-amd64\n")
                self.assertEqual(workflow.uploads, [], f"{stage} failure reached upload action inputs")
                self.assertEqual(workflow.records[-1],
                                 {"name": "Publish test-only candidate binaries", "status": "skipped"})
                self.assertFalse((Path(workflow.env["CANDIDATE_DIR"]) / "manifest.json").exists(),
                                 "failed candidate suffix published a manifest")

    def test_workflow_upload_inputs_are_exactly_five_binaries_and_manifest(self):
        workflow = CandidateWorkflowHarness(self)
        # Confidential/source/dependency/log fixtures are outside candidate outputs.
        # We record action inputs, not pretend to perform GitHub's upload or ZIP.
        for name in ("credentials.json", "config.json", "source.go", "build.log", ".env"):
            (workflow.workspace / name).write_bytes(b"synthetic excluded sentinel")
        for name in ("home", "node_modules"):
            (workflow.workspace / name).mkdir()
            (workflow.workspace / name / "excluded.txt").write_bytes(b"synthetic excluded sentinel")
        (workflow.workspace / "outside-link").symlink_to(workflow.workspace / "credentials.json")
        workflow.run()
        self.assertEqual([record["status"] for record in workflow.records],
                         ["success", "success", "success", "success", "upload-inputs-recorded"])
        self.assertEqual(len(workflow.uploads), 1, "successful candidate suffix must supply exactly one upload")
        upload = workflow.uploads[0]
        directory = Path(workflow.env["CANDIDATE_DIR"])
        expected = [str(directory / name) for name, _ in CANDIDATES] + [str(directory / "manifest.json")]
        self.assertEqual(upload["paths"], expected,
                         "upload INPUTS must be six exact paths, never a workspace/directory wildcard")
        self.assertEqual([glob.has_magic(path) for path in upload["paths"]], [False] * 6)
        self.assertEqual([Path(path).is_file() for path in upload["paths"]], [True] * 6)
        self.assertEqual(sorted(path.name for path in directory.iterdir()),
                         sorted([name for name, _ in CANDIDATES] + ["manifest.json"]))
        manifest = json.loads((directory / "manifest.json").read_text())
        payloads = {name: ("workflow candidate: " + name + "\n").encode() for name, _ in CANDIDATES}
        self.assertEqual(manifest["binaries"], self.expected_binaries(payloads))
        self.assertEqual(manifest["built_commit_sha"], self.sha, "manifest must bind the real built checkout")
        self.assertEqual(workflow.env["CANDIDATE_BUILT_SHA"], self.sha)
        expected_name = f"candidate-binaries-{self.sha}-12345-2"
        self.assertEqual(manifest["creation_context"]["artifact_name"], expected_name)
        self.assertEqual(upload["inputs"]["name"], expected_name)
        self.assertEqual(upload["inputs"]["if-no-files-found"], "error")
        self.assertIs(upload["inputs"]["include-hidden-files"], False)


if __name__ == "__main__":
    unittest.main(verbosity=2)
