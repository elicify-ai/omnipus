import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import type { EnvKind } from './env';
import type { KeyRowMeasurement, Violation } from './measure';

/**
 * One check's result, written to its OWN file under <rawDir> rather than
 * appended to a shared JSONL file. Playwright workers run as separate
 * processes; fs.appendFileSync is only atomic up to PIPE_BUF, and a
 * violation-heavy entry can exceed that, so concurrent workers could
 * interleave and corrupt a shared file. One file per result sidesteps that
 * entirely — aggregate() below is the only thing that ever merges them.
 */
export interface ResultEntry {
  kind: 'route-check' | 'task-flow';
  id: string;
  templatePath?: string;
  resolvedPath?: string;
  project: string;
  envKind: EnvKind;
  browserName: string;
  // 'settle-timeout' (route-check only): lib/settle.ts's settle() never
  // observed a busy-indicator-free, stable control count within its
  // deadline, and the route is not in NO_CONTENT_BY_DESIGN_ROUTE_IDS — a
  // reportable settle FAILURE, distinct from a genuine "ok, zero controls"
  // reading (see settle.ts's doc comment). Never silently folded into 'ok'.
  status: 'ok' | 'violations' | 'skipped' | 'error' | 'settle-timeout';
  skippedReason?: string;
  controlsChecked?: number;
  violations: Violation[];
  keyRows?: KeyRowMeasurement[];
  screenshotPath?: string;
  errorMessage?: string;
  // Present on route-check entries: whether lib/settle.ts's settle()
  // confirmed the route's own content had finished rendering (or that the
  // route is a documented no-content-by-design shell) before measurePage()
  // ran, and why. Absent on task-flow entries — those settle via explicit
  // Playwright `expect(...).toBeVisible()` waits on their own selectors,
  // not this generic route-sweep mechanism.
  settled?: boolean;
  settleReason?: string;
  timestamp: string;
}

export function recordResult(rawDir: string, entry: ResultEntry): void {
  if (!existsSync(rawDir)) mkdirSync(rawDir, { recursive: true });
  const file = path.join(rawDir, `${entry.kind}-${entry.id.replace(/[^a-zA-Z0-9_.-]/g, '_')}-${entry.project}-${randomUUID()}.json`);
  writeFileSync(file, JSON.stringify(entry, null, 2));
}

export interface AggregateReport {
  generatedAt: string;
  totals: {
    checksRun: number;
    routeChecksOk: number;
    routeChecksWithViolations: number;
    routeChecksSkipped: number;
    routeChecksErrored: number;
    // A route whose content never finished rendering within settle()'s
    // deadline — see ResultEntry.status's 'settle-timeout' doc comment.
    // Never counted in routeChecksOk: a timed-out route's controlsChecked/
    // violations are whatever was on screen at the deadline, not a
    // confirmed-settled measurement.
    routeChecksSettleTimedOut: number;
    // "violations" here is DATA, not a harness/test failure — the flow itself
    // completed (e.g. the model picker really did open and offer 3+ options);
    // one of the four D17 assertions found something in the resulting page
    // state. Only "Errored" means the flow itself broke (a selector timeout,
    // a thrown exception) — see ResultEntry.status's own distinction.
    taskFlowsOk: number;
    taskFlowsWithViolations: number;
    taskFlowsErrored: number;
    totalViolations: number;
    violationsByType: Record<string, number>;
  };
  results: ResultEntry[];
}

export function aggregate(rawDir: string): AggregateReport {
  const files = existsSync(rawDir) ? readdirSync(rawDir).filter((f) => f.endsWith('.json')) : [];
  const results: ResultEntry[] = files
    .map((f) => JSON.parse(readFileSync(path.join(rawDir, f), 'utf8')) as ResultEntry)
    .sort((a, b) => (a.id === b.id ? a.project.localeCompare(b.project) : a.id.localeCompare(b.id)));

  const violationsByType: Record<string, number> = {};
  let totalViolations = 0;
  for (const r of results) {
    for (const v of r.violations) {
      violationsByType[v.type] = (violationsByType[v.type] ?? 0) + 1;
      totalViolations++;
    }
  }

  const routeResults = results.filter((r) => r.kind === 'route-check');
  const flowResults = results.filter((r) => r.kind === 'task-flow');

  return {
    generatedAt: new Date().toISOString(),
    totals: {
      checksRun: results.length,
      routeChecksOk: routeResults.filter((r) => r.status === 'ok').length,
      routeChecksWithViolations: routeResults.filter((r) => r.status === 'violations').length,
      routeChecksSkipped: routeResults.filter((r) => r.status === 'skipped').length,
      routeChecksErrored: routeResults.filter((r) => r.status === 'error').length,
      routeChecksSettleTimedOut: routeResults.filter((r) => r.status === 'settle-timeout').length,
      taskFlowsOk: flowResults.filter((r) => r.status === 'ok').length,
      taskFlowsWithViolations: flowResults.filter((r) => r.status === 'violations').length,
      taskFlowsErrored: flowResults.filter((r) => r.status === 'error').length,
      totalViolations,
      violationsByType,
    },
    results,
  };
}

export function writeAggregateReport(rawDir: string, outFile: string): AggregateReport {
  const report = aggregate(rawDir);
  const dir = path.dirname(outFile);
  if (!existsSync(dir)) mkdirSync(dir, { recursive: true });
  writeFileSync(outFile, JSON.stringify(report, null, 2));
  return report;
}
