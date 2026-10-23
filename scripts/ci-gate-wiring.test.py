#!/usr/bin/env python3
"""Parsed-workflow proofs for the chief's additional CI wiring requirements.

Uses PyYAML for this local proof. The aggregate CLI is real. Only GitHub's
process/network boundary is replaced with the quarantine fixture provider.
"""
import importlib.util
from pathlib import Path
import re
import subprocess
import unittest

import yaml

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('quarantine_fixtures', ROOT / 'scripts/ci-quarantine.test.py')
fixtures = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixtures)


def condition(expression, outcomes, conclusions):
    """Small documented Actions subset; reject unsupported expressions."""
    if expression is None:
        return not any(c in ('failure', 'cancelled') for c in conclusions)
    expression = expression.removeprefix('${{').removesuffix('}}').strip()
    always = 'always()' in expression
    terms = expression.split('&&')
    result = True
    for term in terms:
        term = term.strip()
        if term == 'always()':
            continue
        match = re.fullmatch(r"steps\.([A-Za-z_][A-Za-z0-9_-]*)\.outcome\s*(==|!=)\s*'(success|failure|skipped|cancelled)'", term)
        if not match:
            raise ValueError('unmodelled condition: ' + term)
        actual = outcomes.get(match[1], 'skipped')
        result = result and ((actual == match[3]) if match[2] == '==' else (actual != match[3]))
    return result and (always or not any(c in ('failure', 'cancelled') for c in conclusions))


def download_suffix(steps, start, supplied):
    """Execute local fail guard; fake only the two network action outcomes."""
    outcomes, conclusions, attempts, executed, guard_exits = {}, [], 0, [], []
    for index, step in enumerate(steps[start:]):
        # Stop before the existing audit consumer. It is outside this retry unit.
        if index and 'evidence_retry' != step.get('id') and step.get('name') != 'Fail if both evidence downloads failed':
            break
        if not condition(step.get('if'), outcomes, conclusions):
            outcome = 'skipped'
        elif step.get('uses') == 'actions/download-artifact@v7':
            if attempts >= len(supplied):
                raise ValueError('download attempted beyond the one permitted retry')
            outcome = supplied[attempts]
            attempts += 1
            executed.append(step['name'])
        elif 'run' in step:
            run = subprocess.run(['bash', '-e', '-o', 'pipefail', '-c', step['run']],
                                 cwd=ROOT, capture_output=True, text=True)
            outcome = 'success' if run.returncode == 0 else 'failure'
            print('real_download_fail_guard direct_exit=' + str(run.returncode))
            guard_exits.append(run.returncode)
            executed.append(step['name'])
        else:
            raise ValueError('unmodelled retry step')
        if 'id' in step:
            outcomes[step['id']] = outcome
        conclusion = 'success' if outcome == 'failure' and step.get('continue-on-error') == 'true' else outcome
        conclusions.append(conclusion)
    return int(any(c in ('failure', 'cancelled') for c in conclusions)), attempts, executed, guard_exits


class WiringTest(unittest.TestCase):
    def test_retry_condition_instrument_and_unknown_expression_control(self):
        outcomes = {'evidence_dl': 'failure', 'evidence_retry': 'failure'}
        self.assertFalse(condition("steps.evidence_dl.outcome == 'failure'", outcomes, ['failure']))
        self.assertTrue(condition("always() && steps.evidence_dl.outcome == 'failure'", outcomes, ['failure']))
        with self.assertRaisesRegex(ValueError, '^unmodelled condition:'):
            condition('unexpected()', {}, [])

    def test_each_evidence_download_retries_once_and_fails_closed(self):
        found = []
        for path in sorted((ROOT / '.github/workflows').glob('*.yml')):
            workflow = yaml.load(path.read_text(), Loader=yaml.BaseLoader)
            for job_id, job in workflow.get('jobs', {}).items():
                for i, first in enumerate(job.get('steps', [])):
                    if first.get('name') != 'Download all evidence artifacts':
                        continue
                    found.append((path.name, job_id))
                    for supplied, expected_exit, expected_count, guard_runs in (
                        (['success'], 0, 1, False),
                        (['failure', 'success'], 0, 2, False),
                        (['failure', 'failure'], 1, 2, True)):
                        with self.subTest(workflow=path.name, job=job_id, supplied=supplied):
                            code, attempts, executed, guard_exits = download_suffix(job['steps'], i, supplied)
                            print(f'{path.name}::{job_id} outcomes={supplied} exit={code} downloads={attempts}')
                            self.assertEqual((code, attempts), (expected_exit, expected_count))
                            self.assertEqual('Fail if both evidence downloads failed' in executed, guard_runs)
                            self.assertEqual(guard_exits, [1] if guard_runs else [])
                    self.assertEqual(first.get('uses'), 'actions/download-artifact@v7')
                    retry = job['steps'][i + 1]
                    self.assertEqual(retry.get('uses'), first['uses'])
                    self.assertEqual(retry.get('with'), first.get('with'))
                    self.assertNotEqual(retry.get('continue-on-error'), 'true')
        # A zero-match search must fail, not falsely certify coverage.
        self.assertEqual(found, [('pr.yml', 'design-system-audit')])

    def test_hermetic_failure_reaches_actual_required_aggregate(self):
        workflow = yaml.load((ROOT / '.github/workflows/pr.yml').read_text(), Loader=yaml.BaseLoader)
        upstream = {name: {'result': 'success'} for name in workflow['jobs']}
        upstream['hermetic-build'] = {'result': 'failure'}
        # GitHub exposes only declared needs in toJSON(needs). Do not inject
        # hermetic-build here when the actual workflow does not declare it.
        delivered = {name: upstream[name] for name in workflow['jobs']['ci-required']['needs']}
        case = fixtures.QuarantineTest('test_unlisted_job_failure_blocks_alongside_quarantine')
        case.setUp()
        self.addCleanup(case.doCleanups)
        case.needs = delivered
        code = case.invoke()
        print(f'actual_workflow_hermetic_failure direct_exit={code}')
        self.assertEqual(code, 1, 'failed hermetic gate was invisible to the required aggregate: ' + case.output)
        self.assertIn('hermetic-build', case.summary)
        self.assertNotIn('green except quarantined', case.summary)


if __name__ == '__main__':
    unittest.main(verbosity=2)
