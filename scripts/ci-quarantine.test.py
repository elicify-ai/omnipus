#!/usr/bin/env python3
"""Rule 4 CLI proofs: real classifier, fake GitHub only at its process edge.

Expected exits come from the founder dispatch: run and report named failures,
never excuse an unlisted failure, never accept a closed/missing issue. No Go,
network, browser, test deletion, retries, or softened assertions are involved.
"""
import copy
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('ci-quarantine.py')
CHAT = 'E2E — llm-conformance-chat'
EXEC = 'E2E — llm-conformance-exec'
DESIGN = 'Design system contracts and browser evidence'
WEBKIT = 'Design system browser checks — webkit'
SUMMARY = 'Design system audit ran'
AUDIT = 'Design system enforcement audit'
LOCKS = 'Design system lock audit'
INSTALL = 'Run npx playwright install --with-deps webkit'
TEST_ID = 'Conformance_t0_ChatGoalE2E'
FAIL_CHECK = 'Fail on skipped or flaky Playwright tests'

# Fixture API contract, not the unit being tested. Unknown endpoints fail hard.
GH = r'''
import json, os, pathlib, shutil, sys
fixture = json.loads(pathlib.Path(os.environ['CI_FIXTURE']).read_text())
a = sys.argv[1:]
if fixture.get('api_error'):
    print('HTTP 503: fixture outage', file=sys.stderr); sys.exit(1)
if a[0] == 'api':
    endpoint = a[1]
    if '/issues/' in endpoint:
        number = endpoint.rsplit('/', 1)[1]
        state = fixture['issues'].get(number)
        if state is None:
            print('HTTP 404: fixture issue missing', file=sys.stderr); sys.exit(1)
        response = {'number': int(number), 'state': state}
        if fixture.get('is_pr'): response['pull_request'] = {}
        print(json.dumps(response))
    elif endpoint.endswith('/annotations'):
        print(json.dumps([[{'annotation_level': 'failure', 'message': fixture.get('annotation', '')}]]))
    elif endpoint.endswith('/logs'):
        print(fixture.get('log', 'unrelated failure'))
    elif '/jobs?' in endpoint and '--paginate' in a and '--slurp' in a:
        print(json.dumps([{'jobs': fixture['jobs']}]))
    else:
        raise ValueError('unmodelled API request: ' + repr(a))
elif a[:2] == ['run', 'download']:
    if fixture.get('missing_report'):
        print('fixture report not found', file=sys.stderr); sys.exit(1)
    name = a[a.index('--name') + 1]
    if name != fixture.get('artifact', 'ci-quarantine-playwright-llm-conformance-chat-1'):
        raise ValueError('wrong attempt/artifact: ' + name)
    directory = pathlib.Path(a[a.index('--dir') + 1])
    directory.mkdir(parents=True, exist_ok=True)
    (directory / 'pw-results.json').write_text(json.dumps(fixture['report']))
else:
    raise ValueError('unmodelled gh command: ' + repr(a))
'''


def entry():
    return {'id': TEST_ID, 'kind': 'playwright', 'check': CHAT,
            'spec': 'conformance-design-chat-e2e.spec.ts', 'project': 'default',
            'issue': 1431, 'added': '2026-10-23', 'reason': 'Known claim-loop conformance failure.'}


def infra():
    return {'id': 'design-system-browser-install', 'kind': 'infrastructure',
            'issue': 1426, 'added': '2026-10-23', 'reason': 'Known apt mirror install hang.',
            'checks': [{'check': WEBKIT, 'step': INSTALL},
                       {'check': DESIGN, 'step': 'Run npx playwright install --with-deps chromium firefox webkit'}]}


def job(name, conclusion='success', steps=None, attempt=1, identity=10):
    return {'id': identity, 'name': name, 'status': 'completed',
            'conclusion': conclusion, 'run_attempt': attempt,
            'steps': steps or [{'name': 'Fixture control', 'conclusion': 'success'}]}


def step(name, conclusion):
    return {'name': name, 'conclusion': conclusion}


def report(outcome='flaky', identity=TEST_ID):
    return {'errors': [], 'suites': [{'specs': [
        {'title': identity + ': specification-derived fixture',
         'file': 'conformance-design-chat-e2e.spec.ts',
         'tests': [{'projectName': 'default', 'expectedStatus': 'passed', 'status': outcome,
                    'results': [{'status': 'failed'}, {'status': 'passed'}] if outcome == 'flaky'
                    else [{'status': 'failed'}]}]}], 'suites': []}],
            'stats': {'expected': 0, 'unexpected': int(outcome == 'unexpected'),
                      'flaky': int(outcome == 'flaky'), 'skipped': 0}}


class QuarantineTest(unittest.TestCase):
    def setUp(self):
        # Repository build/ is ignored. Do not put agent outputs in Documents or /tmp.
        root = SCRIPT.parent.parent / 'build' / 'ci-quarantine-tests'
        root.mkdir(parents=True, exist_ok=True)
        self.temp = tempfile.TemporaryDirectory(dir=root)
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        boundary = self.root / 'bin'
        boundary.mkdir()
        gh = boundary / 'gh'
        gh.write_text('#!' + sys.executable + ' -I\n' + GH)
        gh.chmod(0o700)
        self.manifest = [entry(), infra()]
        self.needs = {'changes': {'result': 'success', 'outputs': {'code': 'true', 'spa': 'true'}},
                      'playwright': {'result': 'failure'}, 'lint': {'result': 'success'},
                      'e2e-plan': {'result': 'success', 'outputs': {'matrix': json.dumps(
                          {'include': [{'group': 'llm-conformance-chat'}]})}}}
        self.fixture = {'issues': {'1431': 'open', '1426': 'open'}, 'report': report(),
                        'jobs': [job(CHAT, 'failure', [step(FAIL_CHECK, 'failure')])]}
        self.env = {**os.environ, 'PATH': str(boundary) + os.pathsep + os.environ['PATH'],
                    'GITHUB_REPOSITORY': 'owner/repo', 'GITHUB_RUN_ID': '123',
                    'CI_FIXTURE': str(self.root / 'fixture.json'),
                    'GITHUB_STEP_SUMMARY': str(self.root / 'summary.md')}

    def invoke(self, command='aggregate'):
        (self.root / 'manifest.json').write_text(json.dumps(self.manifest))
        (self.root / 'fixture.json').write_text(json.dumps(self.fixture))
        self.env['RESULTS'] = json.dumps(self.needs)
        Path(self.env['GITHUB_STEP_SUMMARY']).unlink(missing_ok=True)
        result = subprocess.run([sys.executable, '-I', str(SCRIPT), command, '--manifest',
                                 str(self.root / 'manifest.json')], env=self.env,
                                cwd=SCRIPT.parent.parent, capture_output=True, text=True)
        print(f'{self.id().rsplit(".", 1)[-1]} {command} direct_exit={result.returncode}')
        self.output = result.stdout + result.stderr
        self.summary = Path(self.env['GITHUB_STEP_SUMMARY']).read_text() if Path(
            self.env['GITHUB_STEP_SUMMARY']).exists() else ''
        return result.returncode

    def assert_blocked(self, message):
        self.assertEqual(self.invoke(), 1, self.output)
        self.assertIn(message, self.output)
        self.assertNotIn('green except quarantined', self.summary)

    def test_guard_accepts_open_issues_and_empty_list(self):
        self.assertEqual(self.invoke('guard'), 0, self.output)
        self.assertIn('2 entries; all referenced issues are open', self.output)
        self.manifest = []
        self.assertEqual(self.invoke('guard'), 0, self.output)

    def test_closed_missing_pr_or_unavailable_issue_blocks_even_clean_jobs(self):
        for mode in ('closed', 'missing', 'pr', 'outage'):
            with self.subTest(mode=mode):
                self.fixture.update({'issues': {'1431': 'open', '1426': 'open'},
                                     'api_error': False, 'is_pr': False})
                if mode == 'closed': self.fixture['issues']['1431'] = 'closed'
                if mode == 'missing': self.fixture['issues'].pop('1431')
                if mode == 'pr': self.fixture['is_pr'] = True
                if mode == 'outage': self.fixture['api_error'] = True
                self.needs['playwright']['result'] = 'success'
                self.assertEqual(self.invoke('guard'), 1, self.output)
                self.assertEqual(self.invoke(), 1, self.output)

    def test_invalid_manifest_is_rejected(self):
        for field, value in [('issue', 0), ('issue', True), ('added', '2026-02-30'),
                             ('added', '2999-01-01'), ('reason', ''), ('id', '')]:
            with self.subTest(field=field, value=value):
                self.manifest = [entry()]
                self.manifest[0][field] = value
                self.assertEqual(self.invoke('guard'), 1, self.output)
        self.manifest = [entry(), entry()]
        self.assertEqual(self.invoke('guard'), 1, self.output)

    def test_flaky_and_unexpected_conformance_failures_are_reported_not_hidden(self):
        for status in ('flaky', 'unexpected'):
            with self.subTest(status=status):
                self.fixture['report'] = report(status)
                self.fixture['jobs'][0]['steps'] = [step(FAIL_CHECK if status == 'flaky'
                                                        else 'Run Playwright tests', 'failure')]
                self.assertEqual(self.invoke(), 0, self.output)
                self.assertIn('green except quarantined', self.summary)
                self.assertIn(TEST_ID, self.summary)
                self.assertIn('#1431', self.summary)
                self.assertIn(CHAT, self.summary)
                self.assertEqual(self.fixture['jobs'][0]['conclusion'], 'failure')

    def test_unlisted_test_in_quarantined_job_blocks(self):
        self.fixture['report']['suites'][0]['specs'].append(report(identity='UnlistedTest')['suites'][0]['specs'][0])
        self.fixture['report']['stats']['flaky'] = 2
        self.assert_blocked('UnlistedTest')
        self.assertIn(TEST_ID, self.summary)
        self.assertIn('#1431', self.summary)

    def test_unlisted_job_failure_blocks_alongside_quarantine(self):
        self.needs['lint']['result'] = 'failure'
        self.assert_blocked('lint')

    def test_setup_or_upload_failure_in_same_shard_blocks(self):
        for name in ('Build gateway binary', 'Upload Playwright artifacts'):
            with self.subTest(step=name):
                self.fixture['jobs'][0]['steps'] = [step(FAIL_CHECK, 'failure'), step(name, 'failure')]
                self.assert_blocked(name)

    def test_missing_incomplete_or_invalid_report_blocks(self):
        self.fixture['missing_report'] = True
        self.assert_blocked('report')
        self.fixture['missing_report'] = False
        for mutation in ('counts', 'errors', 'skipped', 'no_attempt', 'wrong_project', 'interrupted', 'wrong_final_status'):
            with self.subTest(mutation=mutation):
                r = report()
                self.fixture['report'] = r
                if mutation == 'counts': r['stats']['flaky'] = 0
                if mutation == 'errors': r['errors'] = [{'message': 'global teardown failed'}]
                if mutation == 'skipped': r['stats']['skipped'] = 1
                test = r['suites'][0]['specs'][0]['tests'][0]
                if mutation == 'no_attempt': test['results'] = []
                if mutation == 'wrong_project': test['projectName'] = 'unlisted-project'
                if mutation == 'interrupted': test['results'][0]['status'] = 'interrupted'
                if mutation == 'wrong_final_status': test['results'][-1]['status'] = 'failed'
                self.assertEqual(self.invoke(), 1, self.output)

    def design_failure(self, root_check=WEBKIT):
        install = INSTALL if root_check == WEBKIT else infra()['checks'][1]['step']
        self.needs = {'changes': self.needs['changes'], 'design-system': {'result': 'success'},
                      'design-system-browser': {'result': 'cancelled'},
                      'design-system-locks': {'result': 'success'},
                      'design-system-audit': {'result': 'skipped'},
                      'design-system-audit-summary': {'result': 'failure'}}
        self.fixture['jobs'] = [job(DESIGN), job(LOCKS), job(AUDIT, 'skipped'),
            job(SUMMARY, 'failure', [step('Require the design-system audits to have run when SPA files changed', 'failure')])]
        for i, project in enumerate(('chromium', 'firefox', 'webkit', 'chromium-coarse-pointer')):
            self.fixture['jobs'].append(job('Design system browser checks — ' + project, identity=20+i))
        target = next(j for j in self.fixture['jobs'] if j['name'] == root_check)
        target.update({'conclusion': 'cancelled', 'steps': [step(install, 'cancelled')]})
        if root_check == DESIGN:
            self.needs['design-system']['result'] = 'cancelled'
            self.needs['design-system-browser']['result'] = 'skipped'
            for j in self.fixture['jobs']:
                if j['name'].startswith('Design system browser checks — '): j['conclusion'] = 'skipped'
        self.fixture['annotation'] = 'The job has exceeded the maximum execution time of 30m0s'
        self.fixture['log'] = ('Installing dependencies…\n'
                               'Ign:2 http://azure.archive.ubuntu.com/ubuntu noble InRelease\n'
                               'The operation was canceled.\n')

    def test_design_install_timeout_and_only_its_audit_cascade_are_reported(self):
        for root in (WEBKIT, DESIGN):
            with self.subTest(root=root):
                self.design_failure(root)
                self.assertEqual(self.invoke(), 0, self.output)
                self.assertIn('green except quarantined', self.summary)
                self.assertIn(root, self.summary)
                self.assertIn('#1426', self.summary)
                self.assertIn(SUMMARY, self.summary)

    def test_design_cancellation_without_signature_or_other_failures_blocks(self):
        for mode in ('signature', 'annotation', 'step', 'other_failure', 'audit_failure', 'lock_failure'):
            with self.subTest(mode=mode):
                self.design_failure()
                if mode == 'signature': self.fixture['log'] = 'operator cancelled the run'
                if mode == 'annotation': self.fixture['annotation'] = 'The operation was canceled.'
                if mode == 'step': self.fixture['jobs'][-2]['steps'] = [step('Run browser checks for webkit', 'cancelled')]
                if mode == 'other_failure': self.fixture['jobs'][-1]['conclusion'] = 'failure'
                if mode == 'audit_failure': self.needs['design-system-audit']['result'] = 'failure'
                if mode == 'lock_failure': self.needs['design-system-locks']['result'] = 'failure'
                self.assertEqual(self.invoke(), 1, self.output)

    def test_changed_path_failure_and_missing_job_evidence_block(self):
        self.needs['changes']['result'] = 'failure'
        self.assert_blocked('changes')
        self.needs['changes']['result'] = 'success'
        self.fixture['jobs'] = []
        self.assert_blocked('evidence')

    def test_latest_job_attempt_wins_without_losing_previous_successes(self):
        old = copy.deepcopy(self.fixture['jobs'][0])
        old.update({'conclusion': 'success', 'id': 1})
        self.fixture['jobs'][0].update({'run_attempt': 2, 'id': 11})
        self.fixture['jobs'].insert(0, old)
        self.fixture['artifact'] = 'ci-quarantine-playwright-llm-conformance-chat-2'
        self.assertEqual(self.invoke(), 0, self.output)
        self.fixture['jobs'][-1]['steps'].append(step('New unlisted step', 'failure'))
        self.assert_blocked('New unlisted step')

    def test_shipped_manifest_has_only_the_three_approved_records(self):
        self.manifest = json.loads((SCRIPT.parent.parent / '.github/ci-quarantine.json').read_text())
        self.assertEqual([(e['id'], e['issue']) for e in self.manifest],
                         [(TEST_ID, 1431), ('Conformance_t1_StandaloneTaskE2E', 1431),
                          ('design-system-browser-install', 1426)])
        self.assertEqual([(c['check'], c['step']) for c in self.manifest[2]['checks']],
                         [(DESIGN, 'Run npx playwright install --with-deps chromium firefox webkit'),
                          (WEBKIT, INSTALL)])
        self.assertEqual(self.invoke('guard'), 0, self.output)
        for check, spec, identity, group in (
            (CHAT, 'conformance-design-chat-e2e.spec.ts', TEST_ID, 'llm-conformance-chat'),
            (EXEC, 'conformance-design-exec-e2e.spec.ts', 'Conformance_t1_StandaloneTaskE2E', 'llm-conformance-exec')):
            with self.subTest(check=check):
                self.fixture['report'] = report(identity=identity)
                self.fixture['report']['suites'][0]['specs'][0]['file'] = spec
                self.fixture['jobs'] = [job(check, 'failure', [step(FAIL_CHECK, 'failure')])]
                self.fixture['artifact'] = 'ci-quarantine-playwright-' + group + '-1'
                self.needs['e2e-plan']['outputs']['matrix'] = json.dumps({'include': [{'group': group}]})
                self.assertEqual(self.invoke(), 0, self.output)
                self.assertIn(identity, self.summary)
                self.assertIn('#1431', self.summary)

    def test_exact_missing_browser_upload_is_causal_but_other_upload_errors_block(self):
        self.design_failure()
        self.manifest[1]['checks'][0]['missing_evidence_step'] = 'Upload browser evidence for webkit'
        webkit = next(j for j in self.fixture['jobs'] if j['name'] == WEBKIT)
        webkit['steps'].append(step('Upload browser evidence for webkit', 'failure'))
        self.fixture['log'] += 'No files were found with the provided path: test-results/design-system-browser-webkit.json'
        self.assertEqual(self.invoke(), 0, self.output)
        self.assertIn('Upload browser evidence for webkit', self.summary)
        self.fixture['log'] = self.fixture['log'].split('No files were found')[0] + 'ECONNRESET'
        self.assertEqual(self.invoke(), 1, self.output)

    def test_clean_and_legitimate_skip_results_do_not_need_exception_evidence(self):
        self.needs['playwright']['result'] = 'success'
        self.needs['lint']['result'] = 'skipped'
        self.assertEqual(self.invoke(), 0, self.output)
        self.assertIn('every gate passed or legitimately skipped', self.summary)
        self.assertNotIn('green except quarantined', self.summary)


if __name__ == '__main__':
    unittest.main(verbosity=2)
