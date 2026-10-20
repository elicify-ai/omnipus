#!/usr/bin/env python3
"""Read-only, branch-local replan diagnostics. Never read config or key files.

The verifier archive retains its real user prompt, model output and tool
exchanges; the soul is captured separately. This is not a provider HTTP trace
or an atomic snapshot of the live stores. Missing reads remain explicit.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import zipfile


class Capture:
    def __init__(self, home, output, plan_ids, metadata):
        self.home, self.output = home, output
        self.plan_ids = set(plan_ids)
        self.metadata = metadata
        self.files, self.errors, self.absent = {}, [], []
        self.workspaces, self.tasks, self.goals, self.sessions = set(), set(), set(), set()
        self.agents = {"judge", "plansupervisor"}
        self.started_at = datetime.now(timezone.utc).isoformat()
        self.secrets = sorted({v for k, v in os.environ.items() if len(v) >= 8 and
                               (k.startswith("OPENROUTER_") and ("KEY" in k or "TOKEN" in k) or
                                k in ("GH_TOKEN", "GITHUB_TOKEN", "OMNIPUS_BEARER_TOKEN"))}, key=len, reverse=True)

    def scrub(self, text):
        for secret in self.secrets:
            text = text.replace(secret, "[REDACTED]")
        return re.sub(r"sk-(?:or-)?[A-Za-z0-9_-]{16,}", "[REDACTED]", text)

    def read(self, relative, optional=False):
        relative = Path(relative)
        if relative.is_absolute() or '..' in relative.parts:
            self.errors.append('refusing path outside capture root: ' + str(relative))
            return None
        path = self.home / relative
        try:
            for part in (path, *path.parents):
                if part == self.home.parent:
                    break
                if part.is_symlink():
                    raise ValueError("refusing symlink: " + str(relative))
            fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
            with os.fdopen(fd, "rb") as f:
                if not stat.S_ISREG(os.fstat(f.fileno()).st_mode):
                    raise ValueError("not a regular file: " + str(relative))
                return f.read().decode("utf-8")
        except FileNotFoundError:
            if optional:
                self.absent.append(str(relative))
            else:
                self.errors.append("missing evidence: " + str(relative))
        except (OSError, UnicodeError, ValueError) as exc:
            self.errors.append(self.scrub(str(relative) + ": " + str(exc)))
        return None

    def record(self, relative, optional=False):
        text = self.read(relative, optional)
        if text is not None:
            self.files[str(relative)] = self.scrub(text).encode()
        return text

    def json_record(self, relative, retain=False):
        text = self.read(relative)
        if text is None:
            return None
        try:
            value = json.loads(text)
            if not isinstance(value, dict):
                raise ValueError("record is not an object")
        except ValueError as exc:
            self.errors.append(str(relative) + ": " + str(exc))
            return None
        if retain:
            self.files[str(relative)] = self.scrub(text).encode()
        return value

    def list_records(self, relative, pattern="*.json"):
        directory = self.home / relative
        if directory.is_symlink():
            self.errors.append("refusing symlink directory: " + str(relative))
            return []
        if not directory.exists():
            self.absent.append(str(relative))
            return []
        return sorted(p.relative_to(self.home) for p in directory.glob(pattern))

    def copy_tree(self, relative):
        relative = Path(relative)
        if relative.is_absolute() or '..' in relative.parts:
            self.errors.append('refusing directory outside capture root: ' + str(relative))
            return
        directory = self.home / relative
        if directory.is_symlink():
            self.errors.append("refusing symlink directory: " + str(relative))
            return
        if not directory.exists():
            self.absent.append(str(relative))
            return
        for parent, dirs, names in os.walk(directory, followlinks=False):
            for name in list(dirs):
                child = Path(parent) / name
                if child.is_symlink():
                    self.errors.append("refusing symlink directory: " + str(child.relative_to(self.home)))
                    dirs.remove(name)
            for name in sorted(names):
                if name in ("config.json", "credentials.json"):
                    continue
                if name.endswith((".json", ".jsonl")) or name == "transcript.day":
                    self.record((Path(parent) / name).relative_to(self.home))

    def collect_records(self):
        if not self.plan_ids:
            self.plan_ids.update(p.stem for p in self.list_records("plans"))
        if not self.plan_ids:
            self.errors.append('no plan records available for diagnostic capture')
        for pid in sorted(self.plan_ids):
            if not pid or Path(pid).name != pid or pid in (".", "..") or "\\" in pid:
                self.errors.append("invalid plan id: " + pid)
                continue
            value = self.json_record(Path("plans") / (pid + ".json"), retain=True)
            if value:
                self.workspaces.add(value.get("workspace_id", ""))
                self.agents.add(value.get("owner_agent_id", ""))
                self.sessions.add(value.get("owner_session_id", ""))
                self.sessions.add((value.get("supervision") or {}).get("session_id", ""))
            self.record(Path("plan_intents") / (pid + ".jsonl"), optional=True)
        for relative in self.list_records("tasks"):
            value = self.json_record(relative)
            if value and value.get("plan_id") in self.plan_ids:
                self.tasks.add(value["id"])
                self.agents.add(value.get("agent_id", ""))
                self.sessions.add(value.get("session_id", ""))
                self.record(relative)
                self.copy_tree(Path("tasks") / value["id"])
        for relative in self.list_records("entities/goals"):
            value = self.json_record(relative)
            if value and value.get("owner_id") in self.tasks | self.plan_ids | (self.sessions - {""}):
                self.goals.add(value.get("goal_id", ""))
                self.sessions.add(value.get("active_session_id", ""))
                self.record(relative)
        for relative in self.list_records("sessions", "*/meta.json"):
            value = self.json_record(relative)
            if value and (value.get("id") in self.sessions - {""} or
                          value.get("agent_id") in ("judge", "plansupervisor") or
                          value.get("workspace_id") in self.workspaces - {""}):
                self.sessions.add(value["id"])
                self.copy_tree(relative.parent)
        for agent in sorted(self.agents - {""}):
            if Path(agent).name == agent and agent not in (".", "..") and "\\" not in agent:
                self.record(Path("entities/agents") / (agent + ".json"), optional=True)
                if agent in ("judge", "plansupervisor"):
                    self.record(Path("agents") / agent / "SOUL.md", optional=True)
        for relative in self.list_records("system", "audit*.jsonl"):
            text = self.read(relative)
            if text is not None:
                ids = self.plan_ids | self.tasks | (self.sessions - {""})
                lines = [line for line in text.splitlines() if any(identifier in line for identifier in ids)]
                self.files[str(relative)] = self.scrub("\n".join(lines) + ("\n" if lines else "")).encode()

    def write(self):
        manifest = {
            "metadata": self.metadata, "started_at": self.started_at,
            "completed_at": datetime.now(timezone.utc).isoformat(),
            "source_home": str(self.home), "plan_ids": sorted(self.plan_ids),
            "task_ids": sorted(self.tasks), "goal_ids": sorted(self.goals),
            "session_ids": sorted(self.sessions - {""}), "errors": self.errors,
            "absent_optional": sorted(set(self.absent)),
            "limitations": ["Live stores are read sequentially, not transactionally.",
                            "Judge input/output are persisted archive payloads, not a provider HTTP envelope or ephemeral assembled system prompt.",
                            "Judge and PlanSupervisor archives include earlier attempts on this isolated CI gateway; correlate by prompt plan id and timestamps."],
            "files": [{"path": name, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest()}
                      for name, data in sorted(self.files.items())],
        }
        self.output.parent.mkdir(parents=True, exist_ok=True)
        with zipfile.ZipFile(self.output, "w", zipfile.ZIP_DEFLATED) as pack:
            for name, data in sorted(self.files.items()):
                pack.writestr(name, data)
            pack.writestr("manifest.json", self.scrub(json.dumps(manifest, indent=2)))
        print(json.dumps({"archive": str(self.output), "files": len(self.files), "errors": self.errors}))
        return 1 if self.errors else 0


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--home", required=True)
    p.add_argument("--output", required=True)
    p.add_argument("--plan-id", action="append", default=[])
    p.add_argument("--metadata", default="{}")
    a = p.parse_args()
    home, output = Path(a.home), Path(a.output)
    if not home.is_absolute() or not home.is_dir() or home.is_symlink():
        p.error("--home must name the explicit, existing, non-symlink CI data directory")
    if output.resolve().is_relative_to(home.resolve()):
        p.error('--output must be outside the live source directory')
    capture = Capture(home, output, a.plan_id, json.loads(a.metadata))
    capture.collect_records()
    return capture.write()


if __name__ == "__main__":
    raise SystemExit(main())
