#!/usr/bin/env python3
"""Unit tests for scripts/gateway-test-shards.py (run-test review 2026-10-30,
HIGH 3/MEDIUM 5: nothing tested the partitioner or the completeness check).

Run: python3 scripts/gateway-test-shards_test.py   (stdlib only, no deps)
CI: wired into the go-test-plan job in .github/workflows/pr.yml, the same
job that computes the shard matrix this partitioner feeds.

Covers: hash partitioning (complete, disjoint, deterministic), the -run
regex builder (regex-metachar escaping, single-name, empty shard), the
runnable-name rule (Test/Fuzz/Example in, Benchmark out — fixture evidence
in the partitioner's docstring), the shard-log result parser, and the
completeness check including the case that MUST fail: a listed test with
no result line.
"""

import importlib.util
import re
import sys
import tempfile
import unittest
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
