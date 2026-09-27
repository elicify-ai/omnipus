/**
 * e2e-model.ts — the ONE place a real-LLM model id may appear as a literal
 * string in tests/e2e/** (the fallback default of the central setting).
 *
 * `OMNIPUS_E2E_MODEL` is set as a GitHub Actions repository variable and
 * threaded into the job/step env by CI (.github/workflows/pr.yml, the e2e
 * gates) and the Fly runner (deploy/ci-worker/runci.sh). When it is unset —
 * local runs without the var — the fallback below is the suite's model.
 *
 * Centralize here; import E2E_MODEL everywhere else. Anywhere else in
 * tests/e2e/** that needs a real-LLM model id imports this — a model id
 * literal outside this file is drift.
 */
export const E2E_MODEL = process.env.OMNIPUS_E2E_MODEL ?? 'deepseek/deepseek-v4.1-flash'
