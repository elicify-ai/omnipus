#!/usr/bin/env python3
"""Named, fail-closed CI receipts for the separately built history tools.

Run on CI workers, not as a local whole-suite command. Neither tool is a
product release artifact. Go's own exits and named JSON events are authoritative.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent
TOOLS = ROOT / "build" / "tools"
SUFFIX = ".exe" if os.name == "nt" else ""
GO_ENV = {**os.environ, "CGO_ENABLED": "0"}


def run_logged(command, log, env=None):
    log.parent.mkdir(parents=True, exist_ok=True)
    print("RUN:", " ".join(map(str, command)), flush=True)
    with log.open("w", encoding="utf-8") as output:
        process = subprocess.Popen(command, cwd=ROOT, env=env or GO_ENV,
                                   stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                   text=True, encoding="utf-8", errors="replace")
        assert process.stdout is not None
        for line in process.stdout:
            output.write(line)
            print(line, end="", flush=True)
        code = process.wait()
        output.write(f"exit={code}\n")
    print(f"EXIT: {code}; log={log}", flush=True)
    if code:
        raise RuntimeError(f"command failed with exit {code}; log={log}")


def named_tests(package, pattern, log, required):
    command = ["go", "test", "-json", "-count=1", "-tags", "goolm,stdjson",
               "-p", "1", "-timeout", "1200s"]
    if pattern:
        command += ["-run", pattern]
    run_logged(command + [package], log)
    outcomes = {}
    for line in log.read_text(encoding="utf-8").splitlines():
        if not line.startswith("{"):
            continue
        event = json.loads(line)
        if event.get("Test") and event.get("Action") in ("pass", "fail", "skip"):
            outcomes[event["Test"]] = event["Action"]
    top = {name: action for name, action in outcomes.items() if "/" not in name}
    for name, action in sorted(top.items()):
        print(f"NAMED TEST: {name} {action}")
    print(f"named_test_count={len(top)} passed={sum(v == 'pass' for v in top.values())} "
          f"skipped={sum(v == 'skip' for v in top.values())}")
    if not top or any(outcomes.get(name) != "pass" for name in required):
        raise RuntimeError(f"required named tests absent, skipped or failed: {required}")
    return outcomes


def build_repair():
    TOOLS.mkdir(parents=True, exist_ok=True)
    run_logged(["go", "build", "-tags", "goolm,stdjson", "-o",
                str(TOOLS / ("chat-history-index-repair" + SUFFIX)),
                "./cmd/chat-history-index-repair"], TOOLS / "history-repair-build.log")


def test_command():
    named_tests("./cmd/chat-history-index-repair", None,
                TOOLS / "history-repair-command.log", [
                    "TestHistoryRepairCommand_PreflightManifestAndIdempotence",
                    "TestHistoryRepairCommand_BuiltPhasesCutoverAndManifest",
                    "TestHistoryRepairCommand_SharedConstructorAndRealReader",
                    "TestHistoryRepairCommand_GuardedCLIApplyReadsAndRepeats",
                    "TestHistoryRepairCommand_StoppedReceiptRequiresActualReapedInstance",
                ])


def test_core():
    required = ["TestChatHistoryRecovery_PublicationCrashBoundAndIdempotence",
                "TestChatHistoryRetention_ActualRetainedContinuation",
                "TestChatHistoryReadCut_SlowReadDoesNotHoldAppendLocks"]
    if os.name == "nt":
        required += ["TestChatHistoryRetention_WindowsBusyContinueAndRetry",
                     "TestChatHistoryRetention_ActualRetainedContinuation_NativeWindows",
                     "TestChatHistoryReadCut_SlowReadDoesNotHoldAppendLocks_NativeWindows"]
    named_tests("./pkg/session", "^TestChatHistory(Index|Recovery|ReadCut|Retention)_",
                TOOLS / "history-repair-core.log", required)


def build_fixtures():
    TOOLS.mkdir(parents=True, exist_ok=True)
    driver = TOOLS / ("chat-history-fixture.test" + SUFFIX)
    seed = TOOLS / ("chat-history-seed" + SUFFIX)
    run_logged(["go", "test", "-c", "-tags", "goolm,stdjson", "-o", str(driver),
                "./pkg/session"], TOOLS / "history-fixture-build.log")
    run_logged(["go", "build", "-tags", "goolm,stdjson", "-o", str(seed),
                "./tests/e2e/fixtures/chat-history-paging/seed"],
               TOOLS / "history-seed-build.log")
    # Discovery is a positive check of the OFFICIAL driver, not successful
    # compilation of an unrelated test binary. No fixture mutation is run here.
    result = subprocess.run([str(driver), "-test.list", "^TestChatHistoryFixtureDriver$"],
                            check=True, text=True, capture_output=True, cwd=ROOT)
    if "TestChatHistoryFixtureDriver" not in result.stdout.splitlines():
        raise RuntimeError("official session fixture driver missing from compiled executable")
    print("OMNIPUS_CHAT_HISTORY_FIXTURE_BINARY=" + str(driver))
    print("OMNIPUS_CHAT_HISTORY_SEED_BINARY=" + str(seed))


def capture_measurement(test_name, receipt):
    if not test_name or not receipt.is_absolute():
        raise RuntimeError("measurement needs an exact QA test name and absolute receipt path")
    raw = TOOLS / "history-measurement-test.log"
    named_tests("./pkg/gateway", "^" + test_name + "$", raw, [test_name])
    records = []
    for line in raw.read_text(encoding="utf-8").splitlines():
        if not line.startswith("{"):
            continue
        event = json.loads(line)
        text = event.get("Output", "").strip()
        if not text.startswith("{"):
            continue
        row = json.loads(text)
        if row.get("message") == "history_page_measurement":
            records.append(row)
    if sorted(row.get("case", "") for row in records) != ["long_prefix", "short_prefix"]:
        raise RuntimeError("expected exactly two real allocation measurement records")
    for row in records:
        for key in ("sample_count", "limit", "min_rows", "allocated_bytes"):
            if type(row.get(key)) is not int:
                raise RuntimeError(f"measurement {key} is not an integer")
        if row["sample_count"] <= 0 or row["limit"] != 40 or row["min_rows"] != 20 or row["allocated_bytes"] < 0:
            raise RuntimeError("invalid measured allocation record")
        allowed = {"message", "case", "sample_count", "limit", "min_rows", "allocated_bytes"}
        if set(row) != allowed:
            raise RuntimeError("unexpected measurement fields: do not leak private test data")
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    receipt.parent.mkdir(parents=True, exist_ok=True)
    with receipt.open("w", encoding="utf-8") as output:
        output.write(f"HEAD: {commit}\n")
        for row in records:
            output.write(json.dumps(row, separators=(",", ":")) + "\n")
    print(f"OMNIPUS_CHAT_HISTORY_MEASUREMENT_RECEIPT={receipt}; records=2; candidate={commit}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=["build", "command", "core", "fixtures", "measurement", "repair"])
    parser.add_argument("--measurement-test")
    parser.add_argument("--receipt", type=Path)
    args = parser.parse_args()
    if args.phase in ("build", "repair"):
        build_repair()
    if args.phase in ("command", "repair"):
        test_command()
    if args.phase in ("core", "repair"):
        test_core()
    if args.phase == "fixtures":
        build_fixtures()
    if args.phase == "measurement":
        if args.receipt is None:
            parser.error("--receipt is required")
        capture_measurement(args.measurement_test, args.receipt)


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, subprocess.SubprocessError) as error:
        print(f"GATE FAILURE: {error}", file=sys.stderr)
        sys.exit(1)
