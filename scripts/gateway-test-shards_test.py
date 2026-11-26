#!/usr/bin/env python3
"""Unit tests for scripts/gateway-test-shards.py (run-test review 2026-10-30,
HIGH 3/MEDIUM 5: nothing tested the partitioner or the completeness check).

Run: python3 scripts/gateway-test-shards_test.py   (stdlib only, no deps)
CI: wired into the go-test-plan job in .github/workflows/pr.yml, the same
job that computes the shard matrix this partitioner feeds.

Covers: hash partitioning (complete, disjoint, deterministic), bounded
UTF-8 -run regex chunks (including escaping, exact boundaries and empty
shards), the runnable-name rule (Test/Fuzz/Example in, Benchmark out), the
shard-log result parser and strict completeness. Both real shell runners
are exercised in isolated fixture trees with a fake Go compiler/binary:
large plain/race shards, exact-once execution, early chunk failure,
stdin isolation, fail-fast bounds, and workflow flake-filter output.
"""

import importlib.util
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest
from collections import Counter
from pathlib import Path

# The module file carries dashes in its name (scripts/gateway-test-shards.py),
# so a plain `import` cannot reach it — load it by path.
_spec = importlib.util.spec_from_file_location(
    "gateway_test_shards", Path(__file__).resolve().with_name("gateway-test-shards.py")
)
shards = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(shards)


class PartitionTests(unittest.TestCase):
    def test_partition_is_complete_disjoint_deterministic(self):
        names = [f"Test{i:02d}" for i in range(50)]
        first = shards.partition_tests(names, 4)
        # Complete: every name assigned exactly once.
        assigned = [n for shard in first for n in shard]
        self.assertEqual(sorted(assigned), sorted(names))
        # Disjoint: no duplicates across shards.
        self.assertEqual(len(assigned), len(set(assigned)))
        # Deterministic: same input, same assignment.
        self.assertEqual(first, shards.partition_tests(names, 4))
        # Shard count respected (some shards may be empty for tiny inputs).
        self.assertEqual(len(first), 4)

    def test_partition_of_one_name(self):
        for n in (1, 2, 4):
            shards_list = shards.partition_tests(["TestOnly"], n)
            self.assertEqual([n for shard in shards_list for n in shard], ["TestOnly"])

    def test_partition_stable_by_name_hash(self):
        # The docstring contract: the same test always lands in the same
        # shard, regardless of input order (per-shard ordering follows the
        # input list; assignment is what the hash fixes).
        a = shards.partition_tests(["TestAlpha", "TestBeta"], 3)
        b = shards.partition_tests(["TestBeta", "TestAlpha"], 3)
        where_a = {n: i for i, shard in enumerate(a) for n in shard}
        where_b = {n: i for i, shard in enumerate(b) for n in shard}
        self.assertEqual(where_a, where_b)


class NamesToRegexTests(unittest.TestCase):
    def test_regex_metacharacters_are_escaped(self):
        # A name that would be ambiguous unescaped: TestA(.+)* must match
        # itself literally, not act as a pattern.
        tricky = "TestA(.+)*"
        regex = shards.names_to_regex([tricky, "TestPlain"])
        self.assertEqual(re.match(regex, tricky).group(0), tricky)
        self.assertIsNone(re.match(regex, "TestAx"))
        self.assertIsNone(re.match(regex, "TestA_b"))
        self.assertEqual(re.match(regex, "TestPlain").group(0), "TestPlain")

    def test_single_name_anchored(self):
        regex = shards.names_to_regex(["TestSolo"])
        self.assertEqual(regex, "^TestSolo$")
        self.assertTrue(re.match(regex, "TestSolo"))
        self.assertIsNone(re.match(regex, "TestSoloSub"))

    def test_empty_shard_matches_nothing(self):
        regex = shards.names_to_regex([])
        self.assertEqual(regex, "^$")


class RegexChunkTests(unittest.TestCase):
    # The dispatch's safe bound, independent of the implementation constant.
    LIMIT = 100000

    def test_large_list_is_split_completely_without_overlap(self):
        names = [f"Test{i:05d}_" + "x" * 140 for i in range(3600)]
        self.assertGreater(sum(len(n.encode()) + 1 for n in names), 131072)
        regexes = shards.names_to_regex_chunks(names)
        self.assertGreater(len(regexes), 1)
        selected = []
        for regex in regexes:
            self.assertLessEqual(len(regex.encode("utf-8")), self.LIMIT)
            pattern = re.compile(regex)
            selected.extend(n for n in names if pattern.fullmatch(n))
        self.assertEqual(Counter(selected), Counter(names))

    def test_single_regex_byte_boundaries_and_escaping(self):
        # Singleton anchors cost 2 bytes; Unicode and escaping cost bytes,
        # not characters. Test immediately below, at and above the bound.
        for suffix in ("x", "é", "."):
            for size in (self.LIMIT - 1, self.LIMIT, self.LIMIT + 1):
                with self.subTest(suffix=suffix, size=size):
                    unit_bytes = len(re.escape(suffix).encode("utf-8"))
                    payload = size - len("^Test$")
                    count, remainder = divmod(payload, unit_bytes)
                    name = "Test" + suffix * count + "x" * remainder
                    if size > self.LIMIT:
                        message = r"-test.run regex is 100001 UTF-8 bytes; limit is 100000 bytes"
                        with self.assertRaisesRegex(ValueError, message):
                            shards.names_to_regex_chunks([name])
                        with self.assertRaisesRegex(ValueError, message):
                            shards.names_to_regex([name])
                    else:
                        self.assertEqual(shards.names_to_regex_chunks([name]), [f"^{re.escape(name)}$"])
                        self.assertEqual(len(shards.names_to_regex([name]).encode("utf-8")), size)

    def test_alternation_overhead_at_boundary(self):
        # ^(TestA|Test...)$ costs 4 anchor/group bytes plus one separator.
        for size in (self.LIMIT - 1, self.LIMIT, self.LIMIT + 1):
            with self.subTest(size=size):
                second = "Test" + "x" * (size - len("^(TestA|Test)$"))
                names = ["TestA", second]
                regexes = shards.names_to_regex_chunks(names)
                if size <= self.LIMIT:
                    self.assertEqual(regexes, [f"^(TestA|{second})$"])
                else:
                    self.assertEqual(regexes, ["^TestA$", f"^{second}$"])
                    with self.assertRaisesRegex(ValueError, r"-test.run regex is 100001 UTF-8 bytes"):
                        shards.names_to_regex(names)

    def test_empty_and_singleton_chunks(self):
        self.assertEqual(shards.names_to_regex_chunks([]), ["^$"])
        self.assertEqual(shards.names_to_regex_chunks(["TestOnly"]), ["^TestOnly$"])


# Only the Go process boundary is fake: both shell runners and the Python
# partitioner/verify implementation are real. No Go package is compiled.
_FAKE_GO = textwrap.dedent(r'''
#!/usr/bin/env python3
import json
import os
import re
import shutil
import sys
from pathlib import Path

if Path(sys.argv[0]).name == "go":
    Path(os.environ["FAKE_COMPILE"]).write_text(json.dumps({
        "argv": sys.argv[1:], "cgo": os.environ.get("CGO_ENABLED"),
    }))
    binary = Path(sys.argv[sys.argv.index("-o") + 1])
    shutil.copyfile(sys.argv[0], binary)
    binary.chmod(0o755)
    sys.exit(0)

names = json.loads(Path(os.environ["FAKE_NAMES"]).read_text())
if "-test.list" in sys.argv:
    print("\n".join(names))
    sys.exit(0)

regex = sys.argv[sys.argv.index("-test.run") + 1]
pattern = re.compile(regex)
selected = [n for n in names if pattern.fullmatch(n)]
Path(os.environ["FAKE_AUDIT"], f"{os.getpid()}.json").write_text(json.dumps({
    "names": selected, "bytes": len(regex.encode("utf-8")),
    "argv": sys.argv[1:], "cwd": os.getcwd(),
}))
if len(regex.encode("utf-8")) > 100000:
    print("FAKE EXEC: -test.run exceeds 100000 byte safe limit", file=sys.stderr)
    sys.exit(126)

mode = os.environ.get("FAKE_MODE", "pass")
if mode == "readstdin":
    # A test must not inherit/consume the manifest as its standard input.
    sys.stdin.read()
target = names[0]
for name in selected:
    if mode == "missing" and name == target:
        continue
    status = "FAIL" if mode == "assertion" and name == target else "PASS"
    print(f"--- {status}: {name} (0.00s)")
if target in selected:
    if mode == "race":
        print("WARNING: DATA RACE")
    if mode == "guard":
        print("GUARD FAILURE: synthetic test hygiene failure")
    if mode in ("exit", "assertion", "guard"):
        print("FAIL")
        sys.exit(7)
print("PASS")
''').lstrip()


class ShardedRunnerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="shard-selftest-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        (self.root / "scripts").mkdir()
        (self.root / "bin").mkdir()
        (self.root / "tmp").mkdir()
        for package in ("agent", "gateway"):
            (self.root / "pkg" / package).mkdir(parents=True)
        source = Path(__file__).resolve().parent
        for name in ("gateway-test-shards.py", "run-gateway-sharded.sh", "run-agent-sharded.sh"):
            shutil.copyfile(source / name, self.root / "scripts" / name)
        fake_go = self.root / "bin" / "go"
        fake_go.write_text(_FAKE_GO)
        fake_go.chmod(0o755)
        self.env = dict(os.environ, PATH=str(fake_go.parent) + os.pathsep + os.environ["PATH"])
        self.env.update({
            "TMPDIR": str(self.root / "tmp"),
            "FAKE_NAMES": str(self.root / "names.json"),
            "FAKE_COMPILE": str(self.root / "compile.json"),
        })
        self.run_number = 0

    def run_runner(self, package, names, *, race=False, count=1, mode="pass"):
        self.run_number += 1
        audit = self.root / f"audit-{self.run_number}"
        audit.mkdir()
        self.env.update(FAKE_AUDIT=str(audit), FAKE_MODE=mode)
        Path(self.env["FAKE_NAMES"]).write_text(json.dumps(names))
        out = self.root / f"combined-{self.run_number}.log"
        argv = [
            "bash", str(self.root / "scripts" / f"run-{package}-sharded.sh"),
            "--shards", str(count), "--timeout", "17s", "--tags", "goolm,stdjson", "--out", str(out),
        ]
        if race:
            argv.append("--race")
        result = subprocess.run(argv, env=self.env, stdin=subprocess.DEVNULL, text=True, capture_output=True, check=False)
        calls = [json.loads(p.read_text()) for p in audit.glob("*.json")]
        return result, calls, out

    def assert_every_name_once(self, calls, names, package):
        executed = [name for call in calls for name in call["names"]]
        expected = [n for n in names if not n.startswith("Benchmark")]
        self.assertEqual(len(executed), len(expected))
        self.assertEqual(Counter(executed), Counter(expected))
        for call in calls:
            self.assertLessEqual(call["bytes"], 100000)
            self.assertEqual(Path(call["cwd"]), self.root / "pkg" / package)
            self.assertEqual(call["argv"][:5], ["-test.v", "-test.timeout", "17s", "-test.parallel", "1"])

    def test_large_shards_run_every_name_once_plain_and_race(self):
        names = [f"TestSynthetic{i:05d}_" + "x" * 140 for i in range(3600)]
        names += ["FuzzSynthetic", "Example_synthetic", "BenchmarkNotRun"]
        # Even four old single-regex shards average more than Linux's
        # 131072-byte per-argument ceiling. This is not a tiny-list green.
        self.assertGreater(sum(len(n) + 1 for n in names) // 4, 131072)
        for package in ("gateway", "agent"):
            for race, count in ((False, 4), (True, 2)):
                with self.subTest(package=package, race=race):
                    result, calls, out = self.run_runner(package, names, race=race, count=count)
                    self.assertEqual(result.returncode, 0, result.stderr[-2000:])
                    self.assert_every_name_once(calls, names, package)
                    self.assertGreater(len(calls), count)
                    self.assertEqual(result.stdout.count("SHARD_EXIT=0\n"), count)
                    self.assertIn("completeness: OK — 3602/3602", result.stdout)
                    self.assertEqual(out.read_text(), result.stdout[:result.stdout.index("completeness:")])
                    compile_info = json.loads(Path(self.env["FAKE_COMPILE"]).read_text())
                    self.assertEqual(compile_info["cgo"], "1" if race else "0")
                    self.assertEqual("-race" in compile_info["argv"], race)
                    self.assertEqual(compile_info["argv"][-1], f"./pkg/{package}/")
                    self.assertEqual(compile_info["argv"][0:2], ["test", "-c"])

    def test_early_failure_does_not_skip_chunks_or_get_overwritten(self):
        names = [f"TestEarly{i:04d}_" + "x" * 140 for i in range(1500)]
        for package in ("gateway", "agent"):
            for mode in ("exit", "assertion", "race", "guard"):
                with self.subTest(package=package, mode=mode):
                    result, calls, out = self.run_runner(package, names, mode=mode)
                    self.assertEqual(result.returncode, 1, result.stderr[-2000:])
                    self.assert_every_name_once(calls, names, package)
                    self.assertGreater(len(calls), 1)
                    self.assertIn("completeness: OK — 1500/1500", result.stdout)
                    if mode != "race":
                        self.assertIn("SHARD_EXIT=7\n", out.read_text())
                    self.assertRegex(result.stdout, rf"(?m)^FAIL\tgithub.com/elicify-ai/omnipus/pkg/{package}\t\d+s$")
                    marker = {"exit": "FAIL\n", "assertion": "--- FAIL:", "race": "DATA RACE", "guard": "GUARD FAILURE:"}[mode]
                    self.assertIn(marker, out.read_text())
                    self.assertIn(marker, result.stdout)

    def test_test_stdin_cannot_consume_later_regex_chunks(self):
        names = [f"TestInput{i:04d}_" + "x" * 140 for i in range(1500)]
        for package in ("gateway", "agent"):
            with self.subTest(package=package):
                result, calls, _ = self.run_runner(package, names, mode="readstdin")
                self.assertEqual(result.returncode, 0, result.stderr[-2000:])
                self.assert_every_name_once(calls, names, package)
                self.assertGreater(len(calls), 1)

    def test_empty_shards_do_not_duplicate_tests(self):
        for package in ("gateway", "agent"):
            with self.subTest(package=package):
                result, calls, _ = self.run_runner(package, ["TestOnly"], count=4)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(len(calls), 4)
                self.assert_every_name_once(calls, ["TestOnly"], package)
                self.assertIn("completeness: OK — 1/1", result.stdout)

    def test_missing_result_still_fails_completeness(self):
        for package in ("gateway", "agent"):
            with self.subTest(package=package):
                result, calls, _ = self.run_runner(package, ["TestMissing", "TestPresent"], mode="missing")
                self.assertEqual(result.returncode, 1)
                self.assert_every_name_once(calls, ["TestMissing", "TestPresent"], package)
                self.assertIn("completeness: FAILED — 1 of 2", result.stderr)
                self.assertIn("MISSING RESULT: TestMissing", result.stderr)

    def test_runner_rejects_oversized_utf8_chunk_before_exec(self):
        # Inject malformed output at the partitioner process boundary to
        # prove the shell's independent fail-fast guard, not its generator.
        partitioner = self.root / "scripts" / "gateway-test-shards.py"
        partitioner.write_text(textwrap.dedent('''\
            import os
            import sys
            from pathlib import Path
            path = Path(sys.argv[sys.argv.index("--out-dir") + 1])
            path.mkdir(parents=True)
            chunk = path / "shard-0.regexes"
            chunk.write_text("^Test" + "é" * 50000 + "$" + os.environ["FAKE_CHUNK_ENDING"], encoding="utf-8")
            print(chunk)
        '''))
        for package in ("gateway", "agent"):
            for ending in ("\n", ""):
                with self.subTest(package=package, ending=ending):
                    self.env["FAKE_CHUNK_ENDING"] = ending
                    result, calls, _ = self.run_runner(package, ["TestOnly"])
                    self.assertEqual(result.returncode, 1)
                    self.assertEqual(calls, [], "oversized regex must be rejected before invoking any binary")
                    self.assertIn("FATAL: shard 0 -test.run regex is 100006 bytes; limit is 100000 bytes", result.stderr)

    def test_unrepresentable_single_name_fails_before_exec(self):
        for package in ("gateway", "agent"):
            with self.subTest(package=package):
                result, calls, _ = self.run_runner(package, ["Test" + "x" * 99995])
                self.assertEqual(result.returncode, 1)
                self.assertEqual(calls, [])
                self.assertIn("-test.run regex is 100001 UTF-8 bytes; limit is 100000 bytes", result.stderr)
                self.assertIn("FATAL: test partitioner failed", result.stderr)

    def test_legacy_list_refuses_oversized_regex(self):
        names = ["Test" + "x" * 60000, "Test" + "y" * 60000]
        Path(self.env["FAKE_NAMES"]).write_text(json.dumps(names))
        # The fixture is normally copied by the fake compiler; list only
        # needs the binary edge, so copy it here without invoking a runner.
        binary = self.root / "bin" / "go-test-fixture"
        shutil.copyfile(self.root / "bin" / "go", binary)
        binary.chmod(0o755)
        result = subprocess.run(
            [sys.executable, str(self.root / "scripts" / "gateway-test-shards.py"), "list", "1", "--binary", str(binary)],
            env=self.env, text=True, capture_output=True, check=False,
        )
        self.assertEqual(result.returncode, 2)
        self.assertEqual(result.stdout, "")
        self.assertIn("-test.run regex is 120013 UTF-8 bytes; limit is 100000 bytes", result.stderr)


class RunnableNameRuleTests(unittest.TestCase):
    def test_test_fuzz_example_runnable_benchmark_not(self):
        self.assertTrue(shards.is_runnable("TestAlpha"))
        self.assertTrue(shards.is_runnable("TestMain"))
        self.assertTrue(shards.is_runnable("FuzzYan"))
        self.assertTrue(shards.is_runnable("Example_zulu"))  # suffix form
        self.assertTrue(shards.is_runnable("ExampleWithOutput"))
        self.assertFalse(shards.is_runnable("BenchmarkQux"))
        self.assertFalse(shards.is_runnable("helperTest"))
        self.assertFalse(shards.is_runnable(""))

    def test_get_test_names_filters_benchmarks_from_listed(self):
        # The -test.list side of the rule, without needing a Go toolchain:
        # simulate what a binary prints by stubbing the subprocess call.
        listed = ["TestAlpha", "BenchmarkQux", "FuzzYan", "Example_zulu"]
        original = shards.subprocess.run

        def fake_run(argv, **kwargs):
            out = type("P", (), {})()
            out.stdout = "\n".join(listed) + "\n"
            return out

        shards.subprocess.run = fake_run
        try:
            self.assertEqual(shards.get_test_names("/nonexistent"), ["TestAlpha", "FuzzYan", "Example_zulu"])
        finally:
            shards.subprocess.run = original


class ResultLineParserTests(unittest.TestCase):
    def write_log(self, text):
        handle = tempfile.NamedTemporaryFile("w", suffix=".log", delete=False)
        handle.write(text)
        handle.close()
        self.addCleanup(Path(handle.name).unlink)
        return handle.name

    def test_top_level_results_parsed_subtests_and_noise_ignored(self):
        log = self.write_log(
            "=== SHARD 0 START ===\n"
            "=== RUN   TestAlpha\n"
            "--- PASS: TestAlpha (0.01s)\n"
            "=== RUN   TestParent\n"
            "    --- PASS: TestParent/sub (0.00s)\n"
            "    --- FAIL: TestParent/sub2 (0.00s)\n"
            "--- FAIL: TestParent (0.05s)\n"
            "--- SKIP: TestSkipped (0.00s)\n"
            "SHARD_EXIT=1\n"
            "ok  \tci4fixture/pkgfix\t0.113s\n"
            "a test log line mentioning --- PASS: NotAResult (0.00s) inline\n"
            "    --- PASS: IndentedNotTopLevel (0.00s)\n"
        )
        reported = shards.result_names_from_log(log)
        # Exactly the top-level results; subtests, markers and inline text ignored.
        self.assertEqual(reported, {"TestAlpha", "TestParent", "TestSkipped"})

    def test_duplicate_results_deduplicated(self):
        log = self.write_log("--- PASS: TestA (0.00s)\n--- PASS: TestA (0.00s)\n")
        self.assertEqual(shards.result_names_from_log(log), {"TestA"})


class VerifyCompletenessTests(unittest.TestCase):
    """The completeness check, including the case that must fail."""

    def setUp(self):
        # Fake binary listing via the same stub trick.
        self.listed = ["TestAlpha", "TestBeta", "TestGamma"]
        self.original = shards.subprocess.run

        def fake_run(argv, **kwargs):
            out = type("P", (), {})()
            out.stdout = "\n".join(self.listed) + "\n"
            return out

        shards.subprocess.run = fake_run
        self.addCleanup(setattr, shards.subprocess, "run", self.original)

    def write_log(self, text):
        handle = tempfile.NamedTemporaryFile("w", suffix=".log", delete=False)
        handle.write(text)
        handle.close()
        self.addCleanup(Path(handle.name).unlink)
        return handle.name

    def test_complete_run_passes(self):
        log = self.write_log(
            "--- PASS: TestAlpha (0.00s)\n"
            "--- FAIL: TestBeta (0.01s)\n"  # a FAIL is still a result
            "--- SKIP: TestGamma (0.00s)\n"
        )
        self.assertEqual(shards.verify_log("/bin", log), 0)

    def test_missing_test_fails(self):  # the must-fail case
        log = self.write_log("--- PASS: TestAlpha (0.00s)\n--- PASS: TestBeta (0.00s)\n")
        self.assertEqual(shards.verify_log("/bin", log), 1)

    def test_subtest_results_do_not_cover_the_parent(self):
        # A parent that only ever appears as indented subtest lines has NOT
        # reported; the check must not be fooled by name prefixes either.
        log = self.write_log("--- PASS: TestAlpha (0.00s)\n    --- PASS: TestBeta/sub (0.00s)\n")
        self.assertEqual(shards.verify_log("/bin", log), 1)

    def test_missing_log_file_fails(self):
        self.assertEqual(shards.verify_log("/bin", "/nonexistent/ci4-nope.log"), 1)

    def test_unreadable_log_fails_loudly_not_silently(self):
        log = self.write_log("")
        # Bad UTF-8 bytes in a log must not crash the check into an unhandled
        # traceback: verify_log replaces them and still counts the results.
        Path(log).write_bytes(
            b"noise line with bad byte \xff here\n"
            b"--- PASS: TestAlpha (0.00s)\n"
            b"--- PASS: TestBeta (0.00s)\n"
            b"--- PASS: TestGamma (0.00s)\n"
        )
        self.assertEqual(shards.verify_log("/bin", log), 0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
