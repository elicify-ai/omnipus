#!/usr/bin/env python3
"""Rule 4: validate open issues and classify exceptions at the CI aggregate only.

Leaf jobs and tests are never skipped, retried or softened here. The manifest
names the only eligible tests/install steps. Missing evidence is a failure.
GitHub CLI is the network boundary; --self-test replaces only that boundary.
"""
import argparse
from collections import Counter
from datetime import date
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parent.parent
GOOD = {'success', 'skipped'}
TEST_STEPS = {'Run Playwright tests', 'Fail on skipped or flaky Playwright tests'}
DS_BROWSER = 'Design system browser checks — '
DS_CHECKS = {
    'design-system': ['Design system contracts and browser evidence'],
    'design-system-browser': [DS_BROWSER + p for p in
                              ('chromium', 'firefox', 'webkit', 'chromium-coarse-pointer')],
    'design-system-screenshot': ['Design system appearance gate'],
    'design-system-locks': ['Design system lock audit'],
    'design-system-audit': ['Design system enforcement audit'],
    'design-system-audit-summary': ['Design system audit ran'],
}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def load_manifest(path):
    entries = json.loads(path.read_text())
    require(isinstance(entries, list), 'manifest must be a list')
    seen = set()
    for item in entries:
        require(isinstance(item, dict), 'manifest entry must be an object')
        for key in ('id', 'reason'):
            require(isinstance(item.get(key), str) and item[key].strip(), f'invalid {key}')
        require(item['id'] not in seen, f'duplicate id: {item["id"]}')
        seen.add(item['id'])
        issue = item.get('issue')
        require(type(issue) is int and issue > 0, f'invalid issue: {issue}')
        added = item.get('added')
        require(isinstance(added, str) and date.fromisoformat(added).isoformat() == added
                and date.fromisoformat(added) <= date.today(), f'invalid added date: {added}')
        if item.get('kind') == 'playwright':
            for key in ('check', 'spec', 'project'):
                require(isinstance(item.get(key), str) and item[key].strip(), f'invalid {key}')
            require(item['check'].startswith('E2E — '), 'invalid Playwright check')
        elif item.get('kind') == 'infrastructure':
            require(isinstance(item.get('checks'), list) and item['checks'], 'missing install checks')
            for check in item['checks']:
                require(isinstance(check, dict) and isinstance(check.get('check'), str)
                        and isinstance(check.get('step'), str), 'invalid install check')
                require(check['check'] in {n for v in DS_CHECKS.values() for n in v},
                        'unknown design-system check')
                require(check['step'].startswith('Run npx playwright install --with-deps '),
                        'infrastructure entry must name an installation step')
        else:
            raise ValueError('unknown quarantine kind')
    return entries


def gh(*arguments, as_json=True):
    result = subprocess.run(['gh', *arguments], capture_output=True, text=True, errors='replace')
    if result.returncode:
        raise ValueError(f'GitHub evidence request failed (exit {result.returncode}): {result.stderr.strip()}')
    return json.loads(result.stdout) if as_json else result.stdout


def repository():
    repo = os.environ.get('GITHUB_REPOSITORY', '')
    require(re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repo), 'missing/invalid GITHUB_REPOSITORY')
    return repo


def guard(entries, repo):
    for number in sorted({item['issue'] for item in entries}):
        issue = gh('api', f'repos/{repo}/issues/{number}')
        require(issue.get('number') == number and issue.get('state') == 'open'
                and 'pull_request' not in issue, f'issue #{number} is not an open issue')
    print(f'Quarantine guard: {len(entries)} entries; all referenced issues are open.')


def latest_jobs(repo):
    run_id = os.environ.get('GITHUB_RUN_ID', '')
    require(run_id.isdecimal() and int(run_id) > 0, 'missing/invalid GITHUB_RUN_ID')
    pages = gh('api', f'repos/{repo}/actions/runs/{run_id}/jobs?per_page=30&filter=all',
               '--paginate', '--slurp')
    require(isinstance(pages, list), 'invalid jobs evidence')
    jobs = {}
    for page in pages:
        require(isinstance(page.get('jobs'), list), 'missing jobs evidence')
        for job in page['jobs']:
            name = job['name']
            require(type(job.get('run_attempt')) is int and type(job.get('id')) is int,
                    f'invalid attempt evidence: {name}')
            if name not in jobs or (job['run_attempt'], job['id']) > (
                    jobs[name]['run_attempt'], jobs[name]['id']):
                jobs[name] = job
    return jobs


def failed_steps(job):
    require(job.get('status') == 'completed' and isinstance(job.get('steps'), list),
            f'incomplete job evidence: {job.get("name")}')
    return [s for s in job['steps'] if s.get('conclusion') not in GOOD]


def report_tests(report):
    require(isinstance(report, dict) and report.get('errors') == [], 'report has global errors or no error evidence')
    counts = Counter()
    problems = []

    def walk(suites):
        require(isinstance(suites, list), 'report suites missing')
        for suite in suites:
            for spec in suite.get('specs', []):
                require(isinstance(spec.get('tests'), list) and spec['tests'], 'report has empty spec')
                for test in spec['tests']:
                    status = test.get('status')
                    require(status in {'expected', 'unexpected', 'flaky', 'skipped'}, 'invalid report status')
                    counts[status] += 1
                    if status in {'flaky', 'unexpected'}:
                        results = test.get('results')
                        require(test.get('expectedStatus') == 'passed' and isinstance(results, list)
                                and results and all(r.get('status') in {'passed', 'failed', 'timedOut'}
                                                    for r in results)
                                and any(r['status'] in {'failed', 'timedOut'} for r in results)
                                and ((status == 'flaky' and results[-1]['status'] == 'passed')
                                     or (status == 'unexpected' and results[-1]['status'] in {'failed', 'timedOut'})),
                                'report has incomplete failure attempts')
                        problems.append((spec.get('file'), spec.get('title', '').split(':', 1)[0],
                                         test.get('projectName'), status))
            walk(suite.get('suites', []))

    walk(report.get('suites'))
    stats = report.get('stats', {})
    require(all(type(stats.get(k)) is int and stats[k] >= 0 and stats[k] == counts[k]
                for k in ('expected', 'unexpected', 'flaky', 'skipped')), 'report statistics mismatch')
    require(counts['skipped'] == 0 and problems, 'report has skips or no executed failures')
    return problems, counts


def playwright_job(job, entries):
    name = job['name']
    rows, errors = [], []
    steps = failed_steps(job)
    require(job['conclusion'] == 'failure' and steps
            and all(s['name'] in TEST_STEPS and s['conclusion'] == 'failure' for s in steps),
            f'unlisted E2E step failure: {name}: {[s["name"] for s in steps]}')
    group = name.removeprefix('E2E — ')
    parent = Path(os.environ.get('RUNNER_TEMP', str(ROOT / 'build/ci-quarantine-downloads')))
    parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='report-', dir=parent) as directory:
        # Each archive goes into its own new empty directory. Read JSON only;
        # never execute anything downloaded or use it as a working directory.
        gh('run', 'download', os.environ['GITHUB_RUN_ID'], '--repo', repository(),
           '--name', f'ci-quarantine-playwright-{group}-{job["run_attempt"]}',
           '--dir', directory, as_json=False)
        path = Path(directory) / 'pw-results.json'
        require(path.is_file() and not path.is_symlink(), f'missing regular report: {name}')
        problems, counts = report_tests(json.loads(path.read_text()))
    for failure in steps:
        count = counts['unexpected'] if failure['name'] == 'Run Playwright tests' else counts['flaky']
        require(count > 0, f'{failure["name"]} failed without matching report evidence: {name}')
    for spec, test_id, project, status in problems:
        matches = [e for e in entries if e['kind'] == 'playwright' and
                   (e['check'], e['spec'], e['id'], e['project']) == (name, spec, test_id, project)]
        if len(matches) != 1:
            errors.append(f'unlisted test failure: {name}: {spec}::{test_id} ({project})')
        else:
            rows.append((name, f'{spec}::{test_id} ({status})', matches[0]['issue']))
    return rows, errors


def playwright_exceptions(needs, jobs, entries):
    matrix = json.loads(needs['e2e-plan']['outputs']['matrix'])
    expected = {'E2E — ' + item['group'] for item in matrix['include']}
    actual = {name for name in jobs if name.startswith('E2E — ')}
    require(expected and actual == expected, 'incomplete E2E job evidence against selected shard plan')
    rows, errors = [], []
    for name in sorted(actual):
        job = jobs[name]
        if job['conclusion'] == 'success' and job['status'] == 'completed':
            continue
        try:
            matches, failures = playwright_job(job, entries)
            rows.extend(matches)
            errors.extend(failures)
        except (ValueError, KeyError, TypeError, OSError) as error:
            errors.append(f'{name}: {error}')
    require(rows or errors, 'failed Playwright aggregate has no failed-job evidence')
    return rows, errors


def install_exception(job, entries, repo):
    steps = failed_steps(job)
    matches = [(e, c) for e in entries if e['kind'] == 'infrastructure'
               for c in e['checks'] if c['check'] == job['name']]
    require(len(matches) == 1, f'unlisted infrastructure check: {job["name"]}')
    entry, match = matches[0]
    primary = [s for s in steps if s['name'] == match['step'] and s['conclusion'] == 'cancelled']
    require(job['conclusion'] == 'cancelled' and len(primary) == 1,
            f'unlisted infrastructure step: {job["name"]}: {[s["name"] for s in steps]}')
    log = gh('api', f'repos/{repo}/actions/jobs/{job["id"]}/logs', as_json=False)
    require('azure.archive.ubuntu.com/ubuntu' in log and 'Installing dependencies' in log
            and 'The operation was canceled.' in log, f'no known apt-hang evidence: {job["name"]}')
    annotations = gh('api', f'repos/{repo}/check-runs/{job["id"]}/annotations', '--paginate', '--slurp')
    require(any(a.get('annotation_level') == 'failure' and
                a.get('message', '').startswith('The job has exceeded the maximum execution time of ')
                for page in annotations for a in page), f'no job-timeout evidence: {job["name"]}')
    for failure in steps:
        if failure is primary[0]:
            continue
        # Cancellation also runs always() uploads. Only the observed absent
        # browser-report upload is causal, not arbitrary upload/network errors.
        require(failure['name'] == match.get('missing_evidence_step')
                and failure['conclusion'] == 'failure'
                and 'No files were found with the provided path: test-results/design-system-browser-webkit.json' in log,
                f'unlisted infrastructure follow-up: {failure["name"]}')
    return entry['issue'], '; '.join(s['name'] for s in steps)


def design_exceptions(needs, jobs, entries, repo):
    rows, errors = [], []
    exempt = set()
    for need in ('design-system', 'design-system-browser'):
        if needs.get(need, {}).get('result') in GOOD:
            continue
        names = DS_CHECKS[need]
        group_rows, group_errors = [], []
        for name in names:
            try:
                require(name in jobs, f'missing design-system job evidence: {name}')
                job = jobs[name]
                if job['conclusion'] in GOOD:
                    continue
                issue, step = install_exception(job, entries, repo)
                group_rows.append((name, step, issue))
            except (ValueError, KeyError, TypeError, OSError, AttributeError) as error:
                group_errors.append(f'{name}: {error}')
        rows.extend(group_rows)
        errors.extend(group_errors)
        if group_rows and not group_errors:
            exempt.add(need)
        elif not group_errors:
            errors.append(f'no install-timeout evidence: {need}')
    # The audit-summary failure is causal only if the exact dependency chain
    # proves the audit did not run after an evidenced installation timeout.
    if needs.get('design-system-audit-summary', {}).get('result') == 'failure':
        try:
            require(rows and not errors and
                    needs.get('design-system-locks', {}).get('result') == 'success' and
                    needs.get('design-system-audit', {}).get('result') == 'skipped',
                    'design-system lock/audit failure is not an install-timeout cascade')
            summary = jobs.get('Design system audit ran', {})
            steps = failed_steps(summary)
            require(summary.get('conclusion') == 'failure' and len(steps) == 1 and steps[0]['name'] ==
                    'Require the design-system audits to have run when SPA files changed',
                    'unlisted audit summary failure')
            rows.append(('Design system audit ran',
                         'Require the design-system audits to have run when SPA files changed '
                         '(dependent audit did not run after the installation timeout)', rows[0][2]))
            exempt.add('design-system-audit-summary')
        except (ValueError, KeyError, TypeError, OSError, AttributeError) as error:
            errors.append(str(error))
    return exempt, rows, errors


def markdown_cell(value):
    return str(value).replace('|', '\\|').replace('\n', ' ').replace('\r', ' ')


def emit(text):
    print(text)
    path = os.environ.get('GITHUB_STEP_SUMMARY')
    if path:
        with open(path, 'a', encoding='utf-8') as stream:
            stream.write(text + '\n')


def aggregate(entries, repo):
    needs = json.loads(os.environ['RESULTS'])
    require(needs.get('changes', {}).get('result') == 'success', 'changes did not succeed; skips are uninterpretable')
    bad = {key for key, value in needs.items() if value['result'] not in GOOD}
    exceptions, errors = [], []
    if bad & {'playwright', *DS_CHECKS}:
        jobs = latest_jobs(repo)
        if 'playwright' in bad:
            rows, failures = playwright_exceptions(needs, jobs, entries)
            exceptions.extend(rows)
            errors.extend(failures)
            if not failures:
                bad.remove('playwright')
        if bad & set(DS_CHECKS):
            exempt, rows, failures = design_exceptions(needs, jobs, entries, repo)
            bad -= exempt
            exceptions.extend(rows)
            errors.extend(failures)
    # Render failures even when an unrelated gate blocks; never describe that
    # mixed result as green. Raw results are not overwritten by exemptions.
    title = ('FAIL: unquarantined gates: ' + ', '.join(sorted(bad))) if bad else (
        'green except quarantined' if exceptions else 'every gate passed or legitimately skipped')
    lines = ['## CI aggregate: ' + title, '', '| Gate | Raw result |', '| --- | --- |']
    lines += [f'| {markdown_cell(k)} | {markdown_cell(v["result"])} |' for k, v in sorted(needs.items())]
    lines += ['', '### Quarantined failures and dependent checks (not passes)', '',
              '| Check | Exact test / step | Open issue |', '| --- | --- | --- |']
    lines += [f'| {markdown_cell(check)} | {markdown_cell(detail)} | #{issue} |'
              for check, detail, issue in exceptions]
    if not exceptions:
        lines.append('| None | No exceptions used | — |')
    lines += ['', '### Blocking evidence failures', *errors] if errors else []
    emit('\n'.join(lines))
    return int(bool(bad))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('guard', 'aggregate'), nargs='?')
    parser.add_argument('--manifest', type=Path, default=ROOT / '.github/ci-quarantine.json')
    parser.add_argument('--self-test', action='store_true')
    args = parser.parse_args()
    if args.self_test:
        return subprocess.run([sys.executable, '-I', str(Path(__file__).with_name('ci-quarantine.test.py'))]).returncode
    try:
        require(args.command is not None, 'choose guard or aggregate')
        entries = load_manifest(args.manifest)
        repo = repository()
        guard(entries, repo)
        return aggregate(entries, repo) if args.command == 'aggregate' else 0
    except (ValueError, KeyError, TypeError, OSError, AttributeError) as error:
        emit(f'## CI aggregate: FAIL\n\nQuarantine evidence/guard failure: {error}')
        return 1


if __name__ == '__main__':
    sys.exit(main())
