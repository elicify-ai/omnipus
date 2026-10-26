#!/usr/bin/env python3
"""Dispatch-derived eligibility table; does not run GitHub jobs.

Uses a real YAML parser (PyYAML, for this local proof only). Actions uses YAML
1.2; BaseLoader avoids YAML 1.1's interpretation of the key 'on' as a boolean.
Unknown trigger shapes fail the instrument instead of defaulting to eligible.
"""
import fnmatch
from pathlib import Path
import unittest

import yaml

ROOT = Path(__file__).resolve().parent.parent
WORKFLOWS = ('pr.yml', 'build.yml', 'cross-platform.yml', 'codeql.yml', 'shellcheck.yml')


def eligible(events, event, branch, base, action='opened'):
    if event not in events:
        return False
    if event == 'schedule':
        return branch == 'main'
    config = events[event] or {}
    allowed = {'branches', 'types', 'paths', 'paths-ignore'}
    if set(config) - allowed:
        raise ValueError('unknown trigger filter')
    target = base if event == 'pull_request' else branch
    if 'branches' in config and not any(fnmatch.fnmatchcase(target, p) for p in config['branches']):
        return False
    if event == 'pull_request':
        return action in config.get('types', ['opened', 'synchronize', 'reopened'])
    return True


class TriggersTest(unittest.TestCase):
    def test_instrument_checks_filters_and_rejects_unknown_fields(self):
        events = {'push': {'branches': ['main']}, 'pull_request': {'branches': ['main']}}
        self.assertTrue(eligible(events, 'push', 'main', ''))
        self.assertFalse(eligible(events, 'push', 'work/lane', ''))
        self.assertTrue(eligible(events, 'pull_request', 'feature/standalone', 'main'))
        self.assertFalse(eligible(events, 'pull_request', 'main', 'work/lane'))
        with self.assertRaisesRegex(ValueError, '^unknown trigger filter$'):
            eligible({'push': {'unmodelled': True}}, 'push', 'main', '')

    def test_actual_workflows_match_chief_event_table(self):
        # The PR's draft flag is deliberately not an input: triggers must accept
        # BOTH draft and ready PRs, independent of source-branch naming.
        rows = [
            ('push', 'main', '', True),
            ('push', 'release/v0.1.1', '', True),
            ('push', 'release/other', '', False),
            ('push', 'integration/l1-bundle3', '', False),
            ('push', 'work/lane', '', False),
            ('push', 'feature/1266-chat-history-paging', '', False),
            ('pull_request', 'integration/l1-bundle3', 'release/v0.1.1', True),
            ('pull_request', 'feature/1266-chat-history-paging', 'release/v0.1.1', True),
            ('pull_request', 'feature/1266-chat-history-paging', 'main', True),
            ('pull_request', 'work/lane', 'main', True),
            ('pull_request', 'work/lane', 'integration/l1-bundle3', False),
            ('pull_request', 'work/lane', 'release/other', False),
            ('workflow_dispatch', 'work/lane', '', False),
            # The weekly CodeQL scan on default main is explicitly retained.
            ('schedule', 'main', '', None),
        ]
        for name in WORKFLOWS:
            doc = yaml.load((ROOT / '.github/workflows' / name).read_text(), Loader=yaml.BaseLoader)
            events = doc['on']
            for event, branch, base, expected in rows:
                for state in ('draft', 'ready') if event == 'pull_request' else ('n/a',):
                    with self.subTest(workflow=name, event=event, branch=branch, base=base, state=state):
                        actual = eligible(events, event, branch, base)
                        self.assertEqual(actual, (name == 'codeql.yml') if expected is None else expected)
                        print(f'{name}: {event} branch={branch} base={base or "-"} state={state} runs={actual}')
            for action in ('opened', 'synchronize', 'reopened', 'ready_for_review', 'converted_to_draft'):
                with self.subTest(workflow=name, action=action):
                    self.assertTrue(eligible(events, 'pull_request', 'feature/1266-chat-history-paging',
                                             'release/v0.1.1', action))
            # No source-branch/draft job conditions may override the on: filters.
            for job in doc['jobs'].values():
                condition = job.get('if', '')
                self.assertNotIn('head_ref', condition)
                self.assertNotIn('.draft', condition)
                self.assertNotIn('pull_request.head.ref', condition)


if __name__ == '__main__':
    unittest.main(verbosity=2)
