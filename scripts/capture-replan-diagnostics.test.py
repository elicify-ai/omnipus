#!/usr/bin/env python3
"""Chief's diagnostic-capture contract; no gateway, browser, Go or paid calls.

The filesystem and ZIP are real. Fixture evidence is independent of the
collector: a wrong-number member, a literal Judge input/output, and plan events.
The workflow harness executes the actual matrix shell, not a rewritten selector.
"""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import zipfile

ROOT = Path(__file__).resolve().parent.parent
CAPTURE = ROOT / "scripts" / "capture-replan-diagnostics.py"
WORKFLOW = ROOT / ".github" / "workflows" / "pr.yml"


class CaptureTest(unittest.TestCase):
    def setUp(self):
        self.scratch = tempfile.TemporaryDirectory(prefix="replan-capture-")
        self.addCleanup(self.scratch.cleanup)
        self.home = Path(self.scratch.name) / "home"
        self.home.mkdir()
        self.output = Path(self.scratch.name) / "output" / "replan-diagnostics.zip"
        self.secret = "sk-or-ci-diagnostic-secret-123456789"
        self.env = {**os.environ, "OPENROUTER_API_KEY_CI": self.secret}
        self.put("plans/p1.json", {"id": "p1", "workspace_id": "w1", "owner_agent_id": "owner", "state": "done"})
        self.put("tasks/m2.json", {"id": "m2", "plan_id": "p1", "session_id": "s-member", "agent_id": "stub", "status": "done", "result": "verification number is 17"})
        self.put("tasks/m2/runs/2026-10-21.jsonl", {"task_id": "m2", "result": "verification number is 17"})
        self.put("entities/goals/g1.json", {"goal_id": "g1", "owner_kind": "task", "owner_id": "m2", "active_session_id": "s-member", "latest_verdict": {"met": True}})
        self.put("sessions/s-member/meta.json", {"id": "s-member", "agent_id": "stub", "workspace_id": "w1"})
        self.put("sessions/s-member/transcript.jsonl", {"role": "assistant", "content": "verification number is 17"})
        self.put("sessions/s-judge/meta.json", {"id": "s-judge", "agent_id": "judge", "type": "verifier"})
        self.judge_input = {"role": "user", "content": "Required verification number: 42; observed result: 17", "model_message": {"role": "user", "content": "Required verification number: 42; observed result: 17"}}
        self.judge_output = {"role": "assistant", "content": '{"criteria":[{"id":"c1","met":true,"reason":"fixture misjudgment"}]}'}
        self.put_lines("sessions/s-judge/transcript.jsonl", [self.judge_input, self.judge_output])
        self.put("plan_intents/p1.jsonl", {"plan_id": "p1", "kind": "correction", "verb": "supersede"})
        self.put_lines("system/audit.jsonl", [{"event": "plan.approve", "resource": "p1"}, {"event": "plan.approve", "resource": "unrelated"}])
        self.put_text("agents/judge/SOUL.md", "Judge the actual evidence.\n")
        self.put("entities/agents/judge.json", {"id": "judge", "model": "fixture-model"})
        self.put("tasks/unrelated.json", {"id": "other", "plan_id": "other-plan"})
        self.put("entities/goals/unrelated.json", {"goal_id": "other-goal", "owner_kind": "task", "owner_id": "other"})
        self.put("sessions/unrelated/meta.json", {"id": "unrelated", "agent_id": "other", "workspace_id": "other-workspace"})
        self.put_text("config.json", "DO NOT CAPTURE CONFIG " + self.secret)
        self.put_text("credentials.json", "DO NOT CAPTURE CREDENTIALS")
        self.put_text("master.key", "DO NOT CAPTURE MASTER KEY")

    def put_text(self, relative, text):
        p = self.home / relative
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)

    def put(self, relative, value):
        self.put_text(relative, json.dumps(value) + "\n")

    def put_lines(self, relative, values):
        self.put_text(relative, "".join(json.dumps(v) + "\n" for v in values))

    def run_capture(self, *extra):
        return subprocess.run([sys.executable, "-I", str(CAPTURE), "--home", str(self.home), "--output", str(self.output), "--plan-id", "p1", "--metadata", '{"retry":0,"phase":"before-cleanup"}', *extra], env=self.env, text=True, capture_output=True, cwd=ROOT)

    def read_pack(self):
        self.assertTrue(self.output.is_file(), "a diagnostic attempt must leave an inspectable ZIP")
        with zipfile.ZipFile(self.output) as z:
            return {n: z.read(n) for n in z.namelist()}

    def test_selected_evidence_and_exact_judge_exchange_survive_without_source_writes(self):
        before = {p.relative_to(self.home): p.read_bytes() for p in self.home.rglob("*") if p.is_file()}
        result = self.run_capture()
        self.assertEqual(result.returncode, 0, result.stderr)
        pack = self.read_pack()
        required = {"plans/p1.json", "tasks/m2.json", "tasks/m2/runs/2026-10-21.jsonl", "entities/goals/g1.json", "sessions/s-member/transcript.jsonl", "sessions/s-judge/transcript.jsonl", "plan_intents/p1.jsonl", "system/audit.jsonl", "agents/judge/SOUL.md", "entities/agents/judge.json"}
        self.assertEqual(required - pack.keys(), set(), "every specified evidence category must be retained")
        exchange = [json.loads(line) for line in pack["sessions/s-judge/transcript.jsonl"].splitlines()]
        self.assertEqual(exchange, [self.judge_input, self.judge_output], "Judge input/output must not be reconstructed or rewritten")
        self.assertEqual(json.loads(pack["tasks/m2.json"])["result"], "verification number is 17")
        self.assertEqual([json.loads(line) for line in pack["system/audit.jsonl"].splitlines()], [{"event": "plan.approve", "resource": "p1"}])
        manifest = json.loads(pack["manifest.json"])
        self.assertEqual(manifest["metadata"], {"retry": 0, "phase": "before-cleanup"})
        self.assertEqual(manifest["errors"], [])
        for f in manifest["files"]:
            self.assertEqual(hashlib.sha256(pack[f["path"]]).hexdigest(), f["sha256"])
        after = {p.relative_to(self.home): p.read_bytes() for p in self.home.rglob("*") if p.is_file()}
        self.assertEqual(after, before, "capture must never mutate the gateway's source records")
        self.assertFalse(any(n in pack for n in ("config.json", "credentials.json", "master.key", "tasks/unrelated.json", "entities/goals/unrelated.json", "sessions/unrelated/meta.json")))

    def test_secret_bytes_are_scrubbed_but_wrong_result_and_verdict_are_preserved(self):
        self.put_text("sessions/s-judge/transcript.jsonl", json.dumps({"content": self.secret + " evidence:17", "met": True}) + "\n")
        result = self.run_capture()
        self.assertEqual(result.returncode, 0, result.stderr)
        pack = self.read_pack()
        self.assertFalse(any(self.secret.encode() in b for b in pack.values()), "no known credential may enter any artifact entry")
        self.assertIn("sessions/s-judge/transcript.jsonl", pack, "secret scrubbing must not drop the Judge exchange")
        content = json.loads(pack["sessions/s-judge/transcript.jsonl"])
        self.assertEqual(content, {"content": "[REDACTED] evidence:17", "met": True})

    def test_missing_selected_plan_is_a_visible_capture_failure_not_empty_success(self):
        (self.home / "plans/p1.json").unlink()
        result = self.run_capture()
        self.assertEqual(result.returncode, 1, result.stderr)
        manifest = json.loads(self.read_pack()["manifest.json"])
        self.assertTrue(any("plans/p1.json" in e for e in manifest["errors"]), manifest)

    def test_symlink_is_refused_and_target_never_enters_the_pack(self):
        outside = Path(self.scratch.name) / "outside.jsonl"
        outside.write_text("OUTSIDE-PRIVATE-CONTENT\n")
        target = self.home / "sessions/s-judge/transcript.jsonl"
        target.unlink()
        target.symlink_to(outside)
        result = self.run_capture()
        self.assertEqual(result.returncode, 1, "a refused evidence read must not claim a complete capture")
        pack = self.read_pack()
        self.assertNotIn("sessions/s-judge/transcript.jsonl", pack)
        self.assertFalse(any(b"OUTSIDE-PRIVATE-CONTENT" in b for b in pack.values()))
        self.assertTrue(any("symlink" in e for e in json.loads(pack["manifest.json"])["errors"]))


class MatrixTest(unittest.TestCase):
    def matrix_shell(self):
        text = WORKFLOW.read_text()
        start = text.index("      - name: Compute shard matrix from tests/e2e/shards.json")
        block = text[start:text.index("\n  playwright:", start)]
        shell = block.split("        run: |\n", 1)[1]
        return "\n".join(line[10:] if line.startswith("          ") else line for line in shell.splitlines())

    def select(self, event="workflow_dispatch", only="", llm="true"):
        with tempfile.TemporaryDirectory() as d:
            output = Path(d) / "outputs"
            env = {**os.environ, "EVENT_NAME": event, "ONLY_SHARD": only, "RUN_LLM_E2E": llm, "GITHUB_OUTPUT": str(output)}
            result = subprocess.run(["bash", "-c", self.matrix_shell()], cwd=ROOT, env=env, text=True, capture_output=True)
            fields = dict(line.split("=", 1) for line in output.read_text().splitlines()) if output.exists() else {}
            return result, fields

    def test_empty_input_preserves_all_current_shards(self):
        result, fields = self.select()
        self.assertEqual(result.returncode, 0, result.stderr)
        actual = json.loads(fields["matrix"])["include"]
        expected = json.loads((ROOT / "tests/e2e/shards.json").read_text())["shards"]
        self.assertEqual([s["group"] for s in actual], [s["group"] for s in expected])
        self.assertEqual(json.loads(fields["llm_skipped"]), [])

    def test_empty_input_preserves_non_llm_admission(self):
        result, fields = self.select(llm="false")
        self.assertEqual(result.returncode, 0, result.stderr)
        expected = json.loads((ROOT / "tests/e2e/shards.json").read_text())["shards"]
        self.assertEqual([s["group"] for s in json.loads(fields["matrix"])["include"]],
                         [s["group"] for s in expected if not s["group"].startswith("llm-")])
        self.assertEqual(len(json.loads(fields["llm_skipped"])), 10)

    def test_manual_input_selects_exactly_replan(self):
        result, fields = self.select(only="llm-conformance-replan")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(fields["matrix"]), {"include": [{"group": "llm-conformance-replan", "key_slot": "c", "specs": "tests/e2e/conformance-design-replan-e2e.spec.ts"}]})
        self.assertEqual(len(json.loads(fields["llm_skipped"])), 9, "excluded paid shards must be reported as not run")

    def test_nonmanual_input_cannot_narrow_push_or_pr(self):
        for event in ("push", "pull_request", "schedule"):
            with self.subTest(event=event):
                result, fields = self.select(event=event, only="llm-conformance-replan")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(len(json.loads(fields["matrix"])["include"]), 24)

    def test_unknown_or_disallowed_paid_selection_fails_loudly(self):
        for only, llm in (("not-a-real-shard", "true"), ("llm-conformance-replan", "false")):
            with self.subTest(only=only, llm=llm):
                result, fields = self.select(only=only, llm=llm)
                self.assertNotEqual(result.returncode, 0, "invalid selection must not produce a successful empty or unrelated matrix")
                self.assertIn("only_shard", result.stderr)
                self.assertNotIn("matrix", fields)


if __name__ == "__main__":
    unittest.main()
