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

ROOT = Path(__file__).resolve().parents[2]
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


def named_tests(package, pattern, log, required, env=None):
    command = ["go", "test", "-json", "-count=1", "-tags", "goolm,stdjson",
               "-p", "1", "-timeout", "1200s"]
    if pattern:
        command += ["-run", pattern]
    run_logged(command + [package], log, env)
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


def capture_measurement(receipt):
    # Section O pins one producer. A successful zero-match go test, an old
    # receipt or fabricated allocation values can never satisfy this step.
    test_name = "TestHistoryPageMeasurement_SectionHReceipt"
    if not receipt.is_absolute():
        raise RuntimeError("T18 receipt path must be absolute")
    receipt.parent.mkdir(parents=True, exist_ok=True)
    receipt.unlink(missing_ok=True)
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    env = {**GO_ENV, "OMNIPUS_CHAT_HISTORY_MEASUREMENT_RECEIPT": str(receipt),
           "OMNIPUS_CANDIDATE_SHA": commit}
    raw = TOOLS / "history-measurement-test.log"
    try:
        named_tests("./pkg/gateway", "^" + test_name + "$", raw, [test_name], env)
    except RuntimeError as error:
        raise RuntimeError("T18 producer missing or failed: " + test_name) from error
    validate_measurement_receipt(receipt, commit)
    # The test is the sole producer. Do not rewrite its header, records or bytes.
    print(receipt.read_text(encoding="utf-8"), end="")
    print(f"OMNIPUS_CHAT_HISTORY_MEASUREMENT_RECEIPT={receipt}; records=2; candidate={commit}")


def validate_measurement_receipt(receipt, commit):
    if not receipt.is_file():
        raise RuntimeError("T18 producer missing: no measurement receipt was written")
    lines = receipt.read_text(encoding="utf-8").splitlines()
    if len(lines) != 3 or lines[0] != "HEAD: " + commit:
        raise RuntimeError("T18 receipt missing/short or candidate HEAD does not match Section O")
    records = [json.loads(line) for line in lines[1:]]
    if any(not isinstance(row, dict) for row in records):
        raise RuntimeError("T18 measurement record must be a JSON object")
    if sorted(row.get("case", "") for row in records) != ["long_prefix", "short_prefix"]:
        raise RuntimeError("T18 requires exactly two real allocation measurement records")
    required = {"message", "case", "sample_count", "limit", "min_rows", "allocated_bytes"}
    for row in records:
        if row.get("message") != "history_page_measurement":
            raise RuntimeError("T18 receipt has an unrelated diagnostic")
        for key in ("sample_count", "limit", "min_rows", "allocated_bytes"):
            if type(row.get(key)) is not int:
                raise RuntimeError(f"T18 measurement {key} is not an integer")
        if row["sample_count"] <= 0 or row["limit"] != 40 or row["min_rows"] != 20 or row["allocated_bytes"] < 0:
            raise RuntimeError("invalid T18 measured allocation record")
        if not required <= set(row) or not set(row) <= required | {"time", "level", "caller"}:
            raise RuntimeError("unexpected T18 measurement fields: do not leak private data")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=["build", "command", "core", "fixtures", "measurement", "repair"])
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
        capture_measurement(args.receipt)


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, ValueError, subprocess.SubprocessError) as error:
        print(f"GATE FAILURE: {error}", file=sys.stderr)
        sys.exit(1)
