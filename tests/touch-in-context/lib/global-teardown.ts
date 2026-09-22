import { writeAggregateReport } from './report';
import { RAW_RESULTS_DIR, REPORT_PATH } from './paths';

/**
 * Playwright `globalTeardown` — runs once after every project/worker has
 * finished, however the run ended (pass or fail). Merges the per-check
 * result files (see report.ts's doc comment for why they are one-per-file)
 * into the single JSON report this check is required to produce.
 */
export default async function globalTeardown(): Promise<void> {
  const report = writeAggregateReport(RAW_RESULTS_DIR, REPORT_PATH);
  console.log(
    `[touch-in-context] report written to ${REPORT_PATH} — ` +
      `${report.totals.checksRun} checks, ${report.totals.totalViolations} violations.`,
  );
}
