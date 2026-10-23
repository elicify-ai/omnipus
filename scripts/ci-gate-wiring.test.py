#!/usr/bin/env python3
"""Parsed-workflow proofs for the chief's additional CI wiring requirements.

Uses PyYAML for this local proof. The aggregate CLI is real. Only GitHub's
process/network boundary is replaced with the quarantine fixture provider.
"""
import importlib.util
from pathlib import Path
import unittest

import yaml

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('quarantine_fixtures', ROOT / 'scripts/ci-quarantine.test.py')
fixtures = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixtures)


class WiringTest(unittest.TestCase):
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
