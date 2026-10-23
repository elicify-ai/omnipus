import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { parseArgs } from 'node:util';
import { pathToFileURL } from 'node:url';
import { absolute, safeOutput, optionalCopy, installReadAudit } from './safety.mjs';
import { ROWS, missing, result, combine, markdown, allocationReceipt, prefixWork, readDiagnostics } from './metrics.mjs';
import { seedSynthetic, installCopy, verifyCopyUnchanged, indexPreflight, copiedArchiveShape } from './dataset.mjs';
import { REPO, bridge, candidateEvidence, isolatedEnvironment, provision, mountCopy } from './runtime.mjs';
import { serverCampaign } from './server.mjs';
import { browserCampaign } from './browser.mjs';

export function options(args) {
  const { values } = parseArgs({ args, options: {
    candidate: { type: 'string' }, 'candidate-source': { type: 'string', default: REPO }, gateway: { type: 'string' }, 'fixture-binary': { type: 'string' },
    output: { type: 'string' }, dependencies: { type: 'string', default: path.join(REPO, 'node_modules') },
    groups: { type: 'string', default: '100000' }, 'synthetic-copy': { type: 'string' }, 'ui-built-copy': { type: 'string' },
    'genuine-copy': { type: 'string' }, 'input-root': { type: 'string' },
    'allocation-receipt': { type: 'string' }, 'allocation-run-log': { type: 'string' }, 'artifact-attestation': { type: 'string' },
    'prefix-work': { type: 'string' }, 'offline-evidence': { type: 'string' }, 'go-log': { type: 'string' },
    smoke: { type: 'boolean', default: false }, help: { type: 'boolean', default: false },
  }, strict: true });
  if (values.help) return values;
  for (const key of ['gateway','fixture-binary','output','dependencies','candidate-source']) values[key] = absolute(values[key], `--${key}`);
  for (const key of ['allocation-receipt','allocation-run-log','prefix-work','offline-evidence','artifact-attestation','go-log']) if (values[key]) values[key] = absolute(values[key], `--${key}`);
  values.groups = Number(values.groups);
  if (!Number.isInteger(values.groups) || values.groups < 90) throw new Error('--groups must be an integer >=90 for 90 actual append days');
  if (values['synthetic-copy'] && values['ui-built-copy']) throw new Error('choose --synthetic-copy OR --ui-built-copy, not both');
  values.kind = values['ui-built-copy'] ? 'ui-built' : 'synthetic';
  values.synthetic = optionalCopy(values['synthetic-copy'] ?? values['ui-built-copy'], values['input-root'], '--synthetic-copy/--ui-built-copy');
  values.genuine = optionalCopy(values['genuine-copy'], values['input-root'], '--genuine-copy');
  if (values.smoke && values.groups > 2000 && !values.synthetic) throw new Error('--smoke generation must use --groups <=2000; full dataset belongs to UAT');
  return values;
}
function importedEvidence(opts, candidate) {
  const allocation = opts['allocation-receipt'] ? allocationReceipt(fs.readFileSync(opts['allocation-receipt'], 'utf8'), candidate)
    : missing('no candidate-bound Section-H allocation receipt supplied');
  const log = opts['allocation-run-log'] ? fs.readFileSync(opts['allocation-run-log'], 'utf8') : '';
  const producer = /--- PASS: TestHistoryPageMeasurement_SectionHReceipt\b/u.test(log) &&
    /T18_FULL_READ_NEGATIVE_CONTROL=PASS/u.test(log) && /(?:^|\n)exit=0(?:\n|$)/u.test(log) && !/--- FAIL: TestHistoryPageMeasurement_SectionHReceipt\b/u.test(log);
  const verifiedAllocation = opts['allocation-receipt'] && !producer ? missing('allocation receipt supplied without successful named producer + full-read negative-control log + direct exit=0', { receipt: allocation }) : allocation;
  const work = opts['prefix-work'] ? prefixWork(JSON.parse(fs.readFileSync(opts['prefix-work'], 'utf8')), candidate)
    : missing('no exact-candidate real 1000/100000 prefix diagnostic-pair evidence supplied; Section-H allocation receipt alone cannot prove selected I/O');
  const offline = opts['offline-evidence'] ? JSON.parse(fs.readFileSync(opts['offline-evidence'], 'utf8')) : null;
  const offlineRow = !offline ? missing('offline command not run by this evaluator; provide separate stopped-command throughput/peak-scratch/integrity evidence')
    : offline.candidate !== candidate || !Number.isFinite(offline.throughput_bytes_per_second) || !Number.isInteger(offline.peak_scratch_bytes) || typeof offline.canonical_integrity !== 'boolean' || typeof offline.backup_integrity !== 'boolean'
      ? missing('offline import missing exact candidate or required measured/integrity fields', { imported: offline })
      : result(offline.canonical_integrity && offline.backup_integrity, offline, 'imported stopped-command evidence; not independently re-driven here');
  return { allocation: verifiedAllocation, prefix: combine({ allocated_bytes: verifiedAllocation, selected_work: work }), offline: offlineRow };
}
function writeReport(report, output) {
  fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(report, null, 2), { mode: 0o600 });
  fs.writeFileSync(path.join(output, 'summary.md'), markdown(report), { mode: 0o600 });
}
function rssMonitor(gateway) {
  const samples = [];
  const collect = () => {
    if (!gateway.proc?.pid || gateway.proc.exitCode !== null) return;
    try {
      const kb = Number(execFileSync('ps', ['-o', 'rss=', '-p', String(gateway.proc.pid)], { encoding: 'utf8' }).trim());
      if (Number.isFinite(kb) && kb > 0) samples.push({ at: new Date().toISOString(), pid: gateway.proc.pid, rss_bytes: kb * 1024 });
    } catch { /* Child exited between sample and ps; absence is not zero. */ }
  };
  collect(); const timer = setInterval(collect, 1000);
  return { stop: () => { clearInterval(timer); collect(); return { samples,
    sampled_peak_rss_bytes: samples.length ? Math.max(...samples.map(s => s.rss_bytes)) : null,
    instrument: 'ps RSS every second; process footprint only, NOT attributable allocated Go bytes or a guaranteed allocator peak' }; } };
}
async function datasetCampaign(kind, opts, wire, report, evidence, input) {
  const output = path.join(opts.output, kind); fs.mkdirSync(output, { mode: 0o700 });
  const log = path.join(output, 'gateway.jsonl');
  const childHome = path.join(output, 'child-home'); fs.mkdirSync(childHome, { mode: 0o700 });
  const environment = isolatedEnvironment(childHome);
  const provider = await wire.startTerminalOutcomeProvider();
  let gateway, copy, monitor;
  const dataset = report.datasets[kind] = { rows: Object.fromEntries(ROWS.map(row => [row, missing('campaign not yet measured')])) };
  try {
    gateway = await wire.GatewayProcess.start({ binary: opts.gateway, adminUsername: `b25-${kind}-operator`,
      adminPassword: 'b25-isolated-only-password', apiBase: provider.url, model: 'deepseek/deepseek-v4.1-flash',
      env: { ...environment, OMNIPUS_LOG_FILE: log, OMNIPUS_LOG_LEVEL: 'debug' } });
    if (gateway.port === 10994) throw new Error('refusing reserved live gateway port');
    dataset.gateway = { base_url: gateway.baseURL, isolated_data_directory: gateway.homeDir, log,
      username: gateway.adminUsername, model: 'deepseek/deepseek-v4.1-flash', provider: 'openrouter',
      provider_boundary: 'only external onboarding probe simulated; no model turn or live control execution claimed' };
    const ids = await provision(gateway, wire);
    await gateway.kill9(); gateway.assertStoppedForFixture();
    let sid = ids.session.id;
    if (input) {
      copy = installCopy(input, gateway.homeDir); sid = copy.sid;
      dataset.working_copy_remap = mountCopy(copy, ids, gateway.adminUsername);
      dataset.data = { kind, ...copy, before: undefined, meta: undefined, source: undefined, ...await copiedArchiveShape(copy.directory) };
      dataset.rows.Data = kind === 'genuine' ? result(true, { encoded_archive_bytes: copy.encoded_archive_bytes, actual_days: copy.actual_days, archive_files: copy.archive_files }, 'explicit founder-approved isolated genuine copy supplied; original bytes inventoried read-only; validator still required')
        : missing('accepted synthetic copy shape recorded; group/mix/size provenance needs its generation receipt', dataset.data);
    } else {
      dataset.data = seedSynthetic({ fixtureBinary: opts['fixture-binary'], home: gateway.homeDir, sid,
        agent: ids.agent.id, groups: opts.groups, output, environment: { ...process.env, ...environment, OMNIPUS_HOME: gateway.homeDir } });
      const representative = dataset.data.groups === 100000 && dataset.data.actual_days === 90 && dataset.data.encoded_archive_bytes >= 104857600 && Object.values(dataset.data.mixed_counts).every(n => n > 0);
      dataset.rows.Data = representative ? result(true, dataset.data, 'canonical 100000-group/90-day/mixed/>=100MiB synthetic envelope; NOT genuine-session acceptance')
        : missing('small/non-reference synthetic rehearsal, not the 100000-group >=100MiB envelope', dataset.data);
    }
    writeReport(report, opts.output);
    await gateway.restart(); monitor = rssMonitor(gateway);
    const before = indexPreflight(path.join(gateway.homeDir, 'sessions', sid));
    try { dataset.rows['Server healthy-index'] = await serverCampaign(gateway, wire, sid, log, opts.smoke ? 3 : 30); }
    catch (error) { dataset.rows['Server healthy-index'] = result(false, {}, `healthy-index campaign failed: ${error.message}`); dataset.errors = [String(error.stack)]; }
    writeReport(report, opts.output);
    // restart login minted the current cookie; browser reuses it (never logs in
    // independently and overwrites the account's single session token slot).
    try {
      const ui = await browserCampaign(gateway, wire, sid, ids.other.id, { output, kind,
        candidate: opts.candidate, allocation: evidence.allocation, childEnvironment: environment, smoke: opts.smoke,
        onProgress: measurements => { dataset.browser = measurements; } });
      Object.assign(dataset.rows, ui.rows); dataset.browser = ui.measurements;
    } catch (error) {
      for (const row of ['UI ready','Normal reading','Row memory/replay']) dataset.rows[row] = result(false, dataset.rows[row].measurements ?? {}, `browser campaign/instrument failed: ${error.message}`);
      (dataset.errors ??= []).push(String(error.stack));
    }
    dataset.rows['Prefix independence'] = evidence.prefix;
    dataset.rows['Offline utility'] = evidence.offline;
    dataset.index_after = indexPreflight(path.join(gateway.homeDir, 'sessions', sid));
    dataset.index_unchanged = before.checkpoint_sha256 === dataset.index_after.checkpoint_sha256;
    if (!dataset.index_unchanged) dataset.rows['Server healthy-index'] = result(false, { prior: dataset.rows['Server healthy-index'] }, 'index changed during read-only campaign');
  } catch (error) {
    dataset.errors ??= []; dataset.errors.push(String(error.stack));
    dataset.rows.Data = result(false, dataset.data ?? {}, `setup failed: ${error.message}`);
  } finally {
    if (monitor) dataset.process_memory = monitor.stop();
    dataset.history_page_diagnostics = readDiagnostics(log);
    if (gateway) { await gateway.kill9(); gateway.assertStoppedForFixture(); await gateway.stop(); }
    await provider.close();
    if (copy) dataset.input_integrity = verifyCopyUnchanged(copy);
    writeReport(report, opts.output);
  }
}
export async function main(args = process.argv.slice(2)) {
  const audit = installReadAudit();
  let report, opts;
  try {
    opts = options(args);
    if (opts.help) { console.log('On-demand B25 evaluator. See README.md. Required: --candidate <full SHA> --gateway <absolute built binary> --fixture-binary <absolute session.test> --output <new absolute directory>. Optional: --groups 2000 --smoke; --genuine-copy/--synthetic-copy plus --input-root; Section-H imports. Run through run.sh (slotrun).'); return 0; }
    if (process.platform === 'darwin' && process.env.OMNIPUS_B25_SLOTRUN !== 'squad-1266-b25') throw new Error('local evaluation must run through run.sh / slotrun lane squad-1266-b25');
    safeOutput(opts.output);
    fs.mkdirSync(opts.output, { recursive: true, mode: 0o700 });
    if (fs.existsSync(path.join(opts.output, 'report.json'))) throw new Error('output already contains a report; choose a new evidence directory');
    if (!opts.synthetic && opts.groups >= 100000) {
      if (process.env.FLY_MACHINE_ID !== 'd8d3977c946558' || !opts['go-log']) throw new Error('100000-group generation requires dedicated Fly spare d8d3977c946558 and --go-log with a real squad-lead GO B25 line; this evaluator never starts Fly');
      const go = fs.readFileSync(opts['go-log'], 'utf8').split('\n').find(line => /^GO B25(?:[- ]|$)/.test(line));
      if (!go) throw new Error('no real GO B25 line in supplied squad-lead authorization log');
    }
    report = { schema_version: 1, candidate: opts.candidate, started_at: new Date().toISOString(), mode: opts.smoke ? 'small-synthetic-smoke-NOT-acceptance' : 'reference-evaluation',
      report_path: path.join(opts.output, 'report.json'), host: { hostname: os.hostname(), platform: os.platform(), release: os.release(), architecture: os.arch(),
        cpu: os.cpus()[0]?.model, logical_cpus: os.cpus().length, ram_bytes: os.totalmem(), node: process.version, load_average: os.loadavg() },
      datasets: { [opts.kind]: { rows: Object.fromEntries(ROWS.map(row => [row, missing('not yet measured')])) },
        genuine: { rows: Object.fromEntries(ROWS.map(row => [row, missing('no founder-approved copy supplied')])) } },
      independent_validator: { status: 'UNMEASURED', reason: 'operator evaluator is not the independent UAT validator; clear/reconnect/Retry/Return/focus/live-control lanes must be re-driven separately' } };
    writeReport(report, opts.output);
    report.provenance = candidateEvidence(opts.candidate, opts.gateway, opts['fixture-binary'], opts['artifact-attestation'], opts['candidate-source']);
    const evidence = importedEvidence(opts, opts.candidate);
    // Existing fixture onboarding must never borrow a real provider key from
    // the parent environment. Only its external onboarding probe is simulated.
    process.env.OPENROUTER_API_KEY_CI = 'b25-onboarding-placeholder';
    process.env.OPENROUTER_API_KEY = 'b25-onboarding-placeholder';
    const wire = await bridge(opts.dependencies, opts.output, opts['candidate-source']);
    await datasetCampaign(opts.kind, opts, wire, report, evidence, opts.synthetic);
    if (opts.genuine) await datasetCampaign('genuine', opts, wire, report, evidence, opts.genuine);
    const statuses = Object.values(report.datasets).flatMap(dataset => Object.values(dataset.rows).map(row => row.status));
    report.exit_code = statuses.includes('FAIL') ? 1 : statuses.includes('UNMEASURED') ? 2 : 0;
    return report.exit_code;
  } catch (error) {
    if (report) { report.fatal_error = String(error.stack); report.exit_code = 1; }
    console.error(error.stack); return 1;
  } finally {
    if (report && opts) {
      report.finished_at = new Date().toISOString(); report.filesystem_audit = audit.snapshot();
      writeReport(report, opts.output);
      console.log(`B25_REPORT=${report.report_path}\nB25_EXIT=${report.exit_code}`);
    }
    audit.restore();
  }
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) process.exitCode = await main();
