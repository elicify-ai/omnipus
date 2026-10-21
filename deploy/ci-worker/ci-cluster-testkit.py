#!/usr/bin/env python3
"""Hermetic process-edge fixtures for ci-cluster-selfcheck.sh; never contacts Fly.

The dispatcher and runner under test are real processes. This helper supplies
Fly's command/argument boundary and guest state, bounds test-owned process groups,
and delivers real signals to the unchanged runner with guest commands stubbed.
"""
import contextlib
import json
import os
from pathlib import Path
import re
import shlex
import signal
import subprocess
import sys
import time


def append(path, text):
    with Path(path).open("a") as stream:
        stream.write(text + "\n")


def exit_code(status):
    return 128 - status if status < 0 else status


def hang(kind, root, ignore_term=False):
    append(root / "events.log", f"HANG {kind} {os.getpid()}")
    if ignore_term:
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
    else:
        def finish(signum, _frame):
            append(root / "events.log", f"TERMINATED {kind} {os.getpid()} {signum}")
            raise SystemExit(128 + signum)
        signal.signal(signal.SIGTERM, finish)
    while True:
        time.sleep(0.1)


def probe_status(status, wrapped):
    if wrapped:
        print(f"PROBE_STATUS: {status}", flush=True)
        return 0
    return status


def validate_probe_wrapper(parts, root):
    """Execute the actual -C shell fragment with an argv-recording pgrep stub.

    Parsing the pattern alone is insufficient: over-escaped $status once made
    our fake green while the real shell printed a literal '$status'. Exercise
    the actual shell for matched/no-match/usage-error statuses, independently
    of the modeled process table below.
    """
    if parts[:2] != ["sh", "-c"] or len(parts) != 3:
        return False
    root.mkdir(parents=True, exist_ok=True)
    # Stub the pgrep command inside the actual shell, leaving the rendered
    # body unchanged. This avoids creating/executing a new script for every
    # assertion while still testing the real shell's quoting and status flow.
    stub = 'pgrep() { printf "PARSER_ARGC:%s\\n" "$#"; printf "PARSER_ARG:%s\\n" "$@"; return "$PARSER_EXIT"; }\n'
    (root / "rendered-probe.json").write_text(json.dumps(parts) + "\n")
    for status in (0, 1, 2):
        env = dict(os.environ, PARSER_EXIT=str(status))
        result = subprocess.run([parts[0], parts[1], stub + parts[2]], capture_output=True, text=True, env=env, timeout=3)
        rows = result.stdout.splitlines()
        if (result.returncode != 0 or "PARSER_ARGC:2" not in rows
                or "PARSER_ARG:-f" not in rows or f"PROBE_STATUS: {status}" not in rows):
            print("probe shell/argv contract failed: " + result.stdout + result.stderr, file=sys.stderr)
            return False
    return True


def fake_fly(args):
    root = Path(os.environ["FAKE_DIR"])
    append(root / "calls.log", shlex.join(args))
    def value(flag):
        return args[args.index(flag) + 1]
    def has(name):
        return (root / f"{name}-{machine}").exists()
    def read(name, default=""):
        path = root / f"{name}-{machine}"
        return path.read_text().strip() if path.exists() else default
    if args[:2] == ["machine", "status"]:
        if len(args) != 5 or args[3] != "--app":
            print("Error: unknown flag", file=sys.stderr)
            return 1
        machine = args[2]
        state = read("state")
        if state and state != "unreadable":
            print(f"Machine ID: {machine}\nState: {state}\nHostStatus: ok")
        return 0
    if args[:2] in (["machine", "start"], ["machine", "stop"]):
        machine = args[2]
        operation = args[1]
        (root / f"state-{machine}").write_text("started" if operation == "start" else "stopped")
        if operation == "stop":
            append(root / "stops.log", f"STOP {machine}")
            append(root / "events.log", f"STOP {machine}")
        return 0
    machine = value("--machine")
    if read("state") != "started":
        print(f"Error: machine {machine} is not started", file=sys.stderr)
        return 1
    if args[:2] == ["ssh", "sftp"]:
        remote, dest = args[3:5]
        append(root / "events.log", f"COPY {machine} {remote}")
        phase = "collection" if "/worker/" in dest else "recovery"
        source = root / "remote" / Path(remote).name
        if has(f"partialfail{phase}") or has(f"partialhang{phase}"):
            # Deliberately write VALID identity/HEAD/verdict bytes before a
            # failed operation. Content validity cannot replace transfer success.
            if not source.is_file():
                print("partial-transfer fixture source absent", file=sys.stderr)
                return 2
            Path(dest).write_bytes(source.read_bytes())
            append(root / "events.log", f"PARTIAL_VALID_BYTES {phase} {machine}")
            if has(f"partialhang{phase}"):
                hang(f"{phase}-{machine}", root)
            print("Error: fixture transfer failed after writing valid-looking bytes", file=sys.stderr)
            return 1
        if has("hangsftp") or has(f"hang{phase}"):
            hang(f"{phase}-{machine}", root)
        if has("interruptcollection") and phase == "collection":
            append(root / "events.log", "INTERRUPT_READY collection")
            hang(f"collection-{machine}", root)
        source = root / "remote" / Path(remote).name
        if not source.is_file():
            print(f"Error: no such run log: {remote}", file=sys.stderr)
            return 1
        Path(dest).write_bytes(source.read_bytes())
        append(root / "events.log", f"COPIED {machine} {remote}")
        return 0
    if args[:2] != ["ssh", "console"]:
        print(f"fake fly: unsupported {args}", file=sys.stderr)
        return 2
    command = value("-C")
    parts = shlex.split(command)
    if parts == ["true"]:
        return 0
    if parts == ["md5sum", "/cache/runci.sh"]:
        print(os.environ["FAKE_MD5"] + "  /cache/runci.sh")
        return 0
    if " /tmp/ci-owner" in command and "echo " in command:
        marker = command.split("echo ", 1)[1].split(" >", 1)[0]
        (root / f"owner-{machine}").write_text(marker)
        return 0
    if parts == ["cat", "/tmp/ci-owner"]:
        if has("hangowner"):
            hang(f"owner-{machine}", root)
        marker = read("owner")
        if not marker:
            return 1
        print(marker)
        return 0
    wrapped = parts[:2] == ["sh", "-c"]
    inner = shlex.split(parts[2].split(";", 1)[0]) if wrapped else parts
    if inner and inner[0] == "pgrep":
        append(root / "events.log", f"PROBE {machine} {json.dumps(inner)}")
        # A transport hang occurs before the remote parser can reject even a
        # malformed command. Keep this fault independent of the argv bug so
        # the pinned baseline can reproduce the unbounded PROBE failure too.
        if has("slowpgrep") and any(os.environ["FAKE_SHORT"] in operand for operand in inner[2:]):
            hang(f"probe-{machine}", root)
        # procps' actual interface: options plus exactly one pattern operand.
        if len(inner) != 3 or inner[1] != "-f":
            print("pgrep: only one pattern can be provided", file=sys.stderr)
            return probe_status(2, wrapped)
        pattern = inner[2]
        if wrapped and not validate_probe_wrapper(parts, root):
            return probe_status(2, wrapped)
        is_gate_probe = " " in pattern
        if is_gate_probe and has("slowpgrep"):
            hang(f"probe-{machine}", root)
        if not is_gate_probe and has("hangbusy"):
            hang(f"busy-{machine}", root)
        if has("unreach"):
            print("Error: ssh shell: connection refused", file=sys.stderr)
            return 1
        lines = read("procs").splitlines()
        matches = [str(n) for n, line in enumerate(lines, 1) if re.search(pattern, line)]
        if matches:
            print("\n".join(matches))
        append(root / "events.log", f"PROBE_RESULT {machine} {'alive' if matches else 'gone'}")
        return probe_status(0 if matches else 1, wrapped)
    if inner and inner[0] == "ls":
        # A legacy newest-by-gate lookup can see the preseeded old PASS.
        paths = sorted((root / "remote").glob("*.log"), key=lambda p: p.stat().st_mtime, reverse=True)
        if paths:
            print("/cache/logs/" + paths[0].name)
        return 0
    if not parts or parts[0] != "/cache/runci.sh" or len(parts) not in (3, 4):
        print(f"fake fly: unsupported console command: {command}", file=sys.stderr)
        return 2
    _, ref, gate, *tail = parts
    token = tail[0] if tail else "legacy"
    append(root / "events.log", f"CONSOLE {machine} {os.getpid()} {ref} {gate} {token}")
    if has("predead"):
        return 1
    remote_name = f"{gate}@{ref}-20261020T000000Z-at{token}.log"
    remote = root / "remote" / remote_name
    remote.parent.mkdir(exist_ok=True)
    actual_token = "older-token" if has("wrongtoken") else token
    actual_gate = "other-gate" if has("wronggate") else gate
    actual_ref = "0" * 40 if has("wrongref") else ref
    head = "0" * 40 if has("wronghead") else os.environ["FAKE_SHORT"]
    identity = f"ATTEMPT: {actual_token} gate {actual_gate} ref {actual_ref}\n"
    body = identity + f"HEAD: {head} selfcheck\n"
    if has("emptycopy"):
        body = ""
    if has("headlesscopy"):
        body = identity
    if has("recoverpass"):
        body += "ALL GATES GREEN\n"
    if has("recoverfail"):
        body += "GATE FAILURE(S) — see above\n"
    if has("remotekilled"):
        body += "RUNCI: KILLED by signal HUP — no verdict written; gate state at death is unknown\n"
    remote.write_text(body)
    announced = f"/cache/logs/{remote_name}"
    if has("wrongpath"):
        announced = f"/cache/logs/{gate}@{ref}-20261020T000000Z-atolder-token.log"
    print(f"RUNLOG: {announced}\n{identity}HEAD: {head} selfcheck", flush=True)
    if has("foreign"):
        (root / f"owner-{machine}").write_text("ci-cluster:foreign")
    if has("die"):
        (root / f"state-{machine}").write_text("stopped")
        return 1
    if has("foreignref") or has("foreigngate") or has("tokenprefix"):
        other_ref = "0" * 40 if has("foreignref") else ref
        other_gate = "other-gate" if has("foreigngate") else gate
        other_token = token + "suffix" if has("tokenprefix") else token
        append(root / f"procs-{machine}", f"bash /cache/runci.sh {other_ref} {other_gate} {other_token}")
    if has("hang") or has("remotekilled") or has("recoverpass") or has("recoverfail"):
        hang(f"console-{machine}", root, has("ignoreterm"))
    procfile = root / f"procs-{machine}"
    line = "bash " + " ".join(parts)
    append(procfile, line)
    if has("queued"):
        print("another runci.sh is already running — waiting for worker lock", flush=True)
    if has("slow") or has("queued"):
        # Keep it alive until FOUR real probes have observed it, not an elapsed
        # duration standing in for a liveness claim (strike window is two).
        for _ in range(200):
            if (root / "events.log").read_text().count(f"PROBE_RESULT {machine} alive") >= 4:
                break
            time.sleep(0.1)
        else:
            print("fake healthy gate never received four live probes", file=sys.stderr)
            return 3
    rows = procfile.read_text().splitlines()
    procfile.write_text("\n".join(row for row in rows if row != line) + "\n")
    if has("killed"):
        print("RUNCI: KILLED by signal HUP — no verdict written; gate state at death is unknown", flush=True)
        return 1
    remote.write_text(body + "ALL GATES GREEN\n")
    print("ALL GATES GREEN", flush=True)
    if has("killtier"):
        # A completed remote gate is not permission to ignore a failed LOCAL
        # tier job. Signal only this fake client's owning tier process after
        # its valid final verdict has reached the capture.
        append(root / "events.log", f"INTERRUPTED tier {os.getppid()} TERM")
        os.kill(os.getppid(), signal.SIGTERM)
    if has("verdicthang"):
        hang(f"console-{machine}", root, has("ignoreterm"))
    return 0


def stop_group(proc):
    # ONLY a new session created by this fixture; never a VM or another job's
    # process group. Collect-before-stop is tested by the fake event stream.
    with contextlib.suppress(ProcessLookupError):
        os.killpg(proc.pid, signal.SIGTERM)
    try:
        proc.wait(timeout=4)
    except subprocess.TimeoutExpired:
        pass
    with contextlib.suppress(ProcessLookupError):
        os.killpg(proc.pid, signal.SIGKILL)
    proc.wait()


def run_fixture(args):
    seconds, output, *command = args
    root = Path(os.environ["FAKE_DIR"])
    output = Path(output)
    with output.open("wb") as stream:
        proc = subprocess.Popen(command, stdout=stream, stderr=subprocess.STDOUT, start_new_session=True)
        def cancelled(signum, _frame):
            stop_group(proc)
            raise SystemExit(128 + signum)
        for sig in (signal.SIGHUP, signal.SIGINT, signal.SIGTERM):
            signal.signal(sig, cancelled)
        interrupt = os.environ.get("FIXTURE_INTERRUPT", "")
        deadline = time.monotonic() + float(seconds)
        sent = False
        capped = False
        while proc.poll() is None:
            events = (root / "events.log").read_text() if (root / "events.log").exists() else ""
            trigger = {
                "gate": "HANG console-",
                "probe": "HANG probe-",
                "collection": "INTERRUPT_READY collection",
            }.get(interrupt)
            if trigger and trigger in events and not sent:
                proc.send_signal(signal.SIGTERM)
                append(root / "events.log", "INTERRUPTED dispatcher TERM")
                sent = True
            if time.monotonic() >= deadline:
                capped = True
                stop_group(proc)
                break
            time.sleep(0.05)
        status = 143 if capped else exit_code(proc.wait())
        if capped:
            stream.write(b"\nFIXTURE CAP: dispatcher did not finish; owned process group stopped\n")
        # Clean only remaining fixture-owned descendants after giving dispatcher
        # cleanup its chance; mark leaks before cleanup so tests cannot hide them.
        leaked = []
        events = (root / "events.log").read_text() if (root / "events.log").exists() else ""
        for line in events.splitlines():
            if line.startswith("CONSOLE ") or line.startswith("HANG "):
                pid = int(line.split()[2])
                try:
                    os.kill(pid, 0)
                except ProcessLookupError:
                    continue
                leaked.append(pid)
        if leaked:
            append(root / "events.log", "LEAK " + " ".join(map(str, leaked)))
        stop_group(proc)
    output.with_suffix(output.suffix + ".exit").write_text(str(status) + "\n")
    return status


def signal_fixture(args):
    runner, directory, full = args
    root = Path(directory)
    root.mkdir(parents=True, exist_ok=True)
    envfile = root / "bash-env.sh"
    envfile.write_text('''cd() {
  if [ "$1" = /cache/omnipus ]; then
    builtin cd "$RUNNER_FIXTURE" || return
    export TMPDIR="$RUNNER_FIXTURE/tmp"
  else builtin cd "$@"; fi
}
mkdir() {
  if [ "$*" = '-p /cache/tmp' ]; then command mkdir -p "$RUNNER_FIXTURE/tmp"
  else printf 'unexpected mkdir: %s\\n' "$*" >&2; return 97; fi
}
gofmt() {
  printf ready > "$RUNNER_FIXTURE/ready"
  while [ ! -f "$RUNNER_FIXTURE/release" ]; do sleep 0.05; done
}
''')
    (root / "tmp").mkdir(exist_ok=True)
    failures = 0
    for sig, expected in ((signal.SIGHUP, 129), (signal.SIGINT, 130), (signal.SIGTERM, 143)):
        name = sig.name.removeprefix("SIG")
        for marker in ("ready", "release"):
            with contextlib.suppress(FileNotFoundError):
                (root / marker).unlink()
        env = dict(os.environ, BASH_ENV=str(envfile), RUNNER_FIXTURE=str(root), RUNCI_AS_CI="1")
        output = root / f"{name}.log"
        with output.open("wb") as stream:
            proc = subprocess.Popen(["bash", runner, full, "gofmt", "0123456789abcdef0123456789abcdef"],
                                    stdout=stream, stderr=subprocess.STDOUT, env=env, start_new_session=True)
            deadline = time.monotonic() + 5
            while not (root / "ready").exists() and proc.poll() is None and time.monotonic() < deadline:
                time.sleep(0.02)
            if not (root / "ready").exists():
                stop_group(proc)
                print(f"FAIL real runner {name}: never entered stubbed gofmt")
                failures += 1
                continue
            proc.send_signal(sig)
            # Bash defers the trap while the foreground command substitution
            # runs. Prove that state, then release the REAL foreground child.
            time.sleep(0.1)
            if proc.poll() is not None:
                print(f"FAIL real runner {name}: signal was not deferred while child active")
                failures += 1
            (root / "release").touch()
            timed_out = False
            try:
                status = exit_code(proc.wait(timeout=5))
            except subprocess.TimeoutExpired:
                timed_out = True
                print(f"FAIL real runner {name}: timed out waiting for signal exit")
                stop_group(proc)
                # Preserve the observed post-cleanup code, never substitute
                # TERM's expected 143 for a failed wait.
                status = exit_code(proc.returncode)
            text = output.read_text()
            correct = (not timed_out and status == expected and f"RUNCI: KILLED by signal {name} " in text
                       and "ALL GATES GREEN" not in text and "GATE FAILURE(S)" not in text)
            print(f"{'PASS' if correct else 'FAIL'} real runner {name}: exit={status}, expected={expected}, trailer={'present' if f'RUNCI: KILLED by signal {name} ' in text else 'absent'}")
            output.with_suffix(".exit").write_text(str(status) + "\n")
            output.with_suffix(".outcome.json").write_text(json.dumps({
                "timed_out": timed_out, "observed_exit": status,
                "expected_exit": expected, "passed": correct,
            }) + "\n")
            failures += not correct
            stop_group(proc)
    return 1 if failures else 0


def probe_contract_fixture(args):
    events, directory = args
    root = Path(directory)
    root.mkdir(parents=True, exist_ok=True)
    commands = []
    for row in Path(events).read_text().splitlines():
        if row.startswith("PROBE "):
            command = row.split(" ", 2)[2]
            if command not in commands:
                commands.append(command)
    if not commands:
        print("FAIL rendered probe: no executed probe found")
        return 1
    for command in commands:
        parts = shlex.split(command)
        if parts[:2] != ["sh", "-c"] or not validate_probe_wrapper(parts, root):
            print("FAIL rendered probe shell/argv/status contract")
            return 1
    print(f"PASS rendered probe statuses 0/1/2: {len(commands)} actual command(s)")
    return 0


def user_switch(args):
    root = Path(args[0])
    guest_args = args[1:]
    (root / "user-switch.json").write_text(json.dumps(guest_args) + "\n")
    if guest_args[:3] != ["-u", "ci", "--"]:
        raise SystemExit("unexpected user-switch argv")
    os.execvpe(guest_args[3], guest_args[3:], os.environ)


def identity_fixture(args):
    """Run actual prologue and re-exec; remap only guest filesystem prefixes.

    The complete runner is executed, with no control flow extracted or removed.
    /cache, /home and the lock's /tmp prefix map into a private scratch guest;
    BASH_ENV supplies guest git/id/flock/find/chown/df/python/gofmt commands.
    The user-switch stub records argv and execs the real env/bash child.
    """
    runner, directory, full = args
    root = Path(directory)
    root.mkdir(parents=True, exist_ok=True)
    cache = root / "cache"
    home = root / "home"
    (cache / "omnipus" / ".git").mkdir(parents=True, exist_ok=True)
    (home / "ci").mkdir(parents=True, exist_ok=True)
    (root / "tmp").mkdir(exist_ok=True)
    (root / "bin").mkdir(exist_ok=True)
    script = root / "runci.sh"
    text = Path(runner).read_text()
    # Filesystem redirections are performed by the shell before command
    # stubs can intercept them. Remap these guest path literals, not behavior.
    script.write_text(text.replace("/cache", str(cache))
                      .replace("/tmp/runci.lock", str(root / "tmp" / "runci.lock"))
                      .replace("/home/", str(home) + "/"))
    envfile = root / "guest-env.sh"
    envfile.write_text('''git() {
  case "$*" in
    *'--get-all safe.directory'*) printf '*\\n' ;;
    *'rev-parse --short'*) printf '%s\\n' "${RUNNER_FULL_REF:0:9}" ;;
    *'rev-parse --verify'*) printf '%s\\n' "$RUNNER_FULL_REF" ;;
    *'log -1'*) printf 'identity fixture\\n' ;;
    *) return 0 ;;
  esac
}
id() {
  if [ "$*" = '-u' ]; then printf '0\\n'
  elif [ "$*" = '-u ci' ]; then printf '1000\\n'
  else return 98; fi
}
flock() { return 0; }
find() { return 0; }
chown() { return 0; }
df() { printf 'Use%%\\n1%%\\n'; }
python() { return 0; }
gofmt() { return 0; }
if [ -n "${RUNCI_AS_CI:-}" ]; then
  printf '%s\\n' "$@" > "$RUNNER_ROOT_FIXTURE/child-argv.txt"
fi
''')
    switch = root / "bin" / "runuser"
    switch.write_text('#!/bin/sh\nexec python3 -I "$CI_TESTKIT" user-switch "$RUNNER_ROOT_FIXTURE" "$@"\n')
    switch.chmod(0o755)
    paths = []
    failures = []
    for token in ("0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210"):
        env = dict(os.environ, BASH_ENV=str(envfile), RUNNER_ROOT_FIXTURE=str(root),
                   RUNNER_FULL_REF=full, CI_TESTKIT=str(Path(__file__).resolve()),
                   PATH=str(root / "bin") + os.pathsep + os.environ["PATH"])
        env.pop("RUNCI_AS_CI", None)
        output = root / f"identity-{token}.log"
        with output.open("wb") as stream:
            proc = subprocess.Popen(["bash", str(script), full, "gofmt", token],
                                    stdout=stream, stderr=subprocess.STDOUT, env=env, start_new_session=True)
            try:
                status = exit_code(proc.wait(timeout=10))
            except subprocess.TimeoutExpired:
                stop_group(proc)
                failures.append("root runner did not complete")
                continue
            # tee is an actual child. Wait for its retained log to finish too.
            for _ in range(100):
                content = output.read_text()
                announcements = [line[8:] for line in content.splitlines() if line.startswith("RUNLOG: ")]
                if announcements and Path(announcements[0]).is_file() and "ALL GATES GREEN" in Path(announcements[0]).read_text():
                    break
                time.sleep(0.02)
            else:
                failures.append("retained RUNLOG never completed")
                stop_group(proc)
                continue
            path = Path(announcements[0])
            retained = path.read_text()
            wanted = f"ATTEMPT: {token} gate gofmt ref {full}"
            expected_suffix = f"-at{token}.log"
            switch_args = json.loads((root / "user-switch.json").read_text())
            child = (root / "child-argv.txt").read_text().splitlines()
            correct = (status == 0 and path.name.startswith(f"gofmt@{full}-")
                       and path.name.endswith(expected_suffix)
                       and wanted in content.splitlines() and wanted in retained.splitlines()
                       and child == [full, "gofmt", token]
                       and switch_args[-4:] == [str(script), full, "gofmt", token])
            paths.append(str(path))
            if not correct:
                failures.append(f"identity/header/re-exec contract failed for {token}")
            print(f"{'PASS' if correct else 'FAIL'} real runner identity: token={token} exit={status} child={child}")
            stop_group(proc)
    if len(paths) != 2 or len(set(paths)) != 2:
        failures.append("two supplied tokens did not produce distinct retained artifacts")
    (root / "identity-outcome.json").write_text(json.dumps({"paths": paths, "failures": failures}, indent=2) + "\n")
    for failure in failures:
        print("FAIL real runner identity: " + failure)
    return 1 if failures else 0


if __name__ == "__main__":
    mode, *arguments = sys.argv[1:]
    if mode == "fake":
        raise SystemExit(fake_fly(arguments))
    if mode == "run":
        raise SystemExit(run_fixture(arguments))
    if mode == "signals":
        raise SystemExit(signal_fixture(arguments))
    if mode == "identity":
        raise SystemExit(identity_fixture(arguments))
    if mode == "user-switch":
        user_switch(arguments)
    if mode == "probe-contract":
        raise SystemExit(probe_contract_fixture(arguments))
    raise SystemExit(f"unknown fixture mode: {mode}")
