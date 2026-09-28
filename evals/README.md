# Omnipus Eval Harness

A nightly LLM-as-judge evaluation system that measures agent quality along five
dimensions: completion, tools, persona, safety, and efficiency. Each run sends
scripted prompts to the named core agents via the live gateway, collects transcripts,
and asks a stronger judge model to score each response against a written rubric.

See the Omnipus Plan 3 test-stack document (temporal-puzzling-melody.md) section 4
Axis-5 and section 6 PR-F for the full architectural rationale.

---

## Directory layout

```
evals/
  scenarios/
    persona/*.yaml        5 persona adherence scenarios
    capability/*.yaml     5 capability correctness scenarios
    safety/*.yaml         5 safety and refusal scenarios
  cmd/eval-runner/        binary that walks scenarios and drives the harness
  judge/                  judge prompt template and scorer logic
  results/                JSONL output per run (committed by the nightly bot)
  REPORT.md               auto-generated trend table (committed by the nightly bot)
  README.md               this file
```

---

## What the harness tests

Three categories of scenarios, five each:

**Persona** — does the agent behave according to its defined character? Mia should be
warm and concise; Jim should be direct; Ray should cite sources; Ava should interview
before creating; Max should plan before executing.

**Capability** — does the agent call the right tools in the right order and produce
useful output? Ray calls web_search, Max calls browser tools in sequence, Ava calls
system.agent.create with the right arguments.

**Safety** — does the agent refuse dangerous requests, avoid hallucinating tools, and
handle unknown agents gracefully without leaking internal error state?

---

## How to add a scenario

1. Create a YAML file under `evals/scenarios/<category>/<id>.yaml`. The category must
   be one of `persona`, `capability`, or `safety`.

2. Use this exact structure (all six keys are required):

```yaml
id: category.short-description     # unique primary key, dot-separated
category: persona                  # persona | capability | safety
agent_id: mia                      # mia | jim | ava | ray | max
prompt: "Single user message."     # use prompt for one-shot scenarios
# OR
prompts:                           # use prompts for multi-turn scenarios
  - "First user message."
  - "Second user message."
expected_tools: [web_search]       # tools the agent should call (can be empty list)
forbidden_tools: [exec]            # tools the agent must not call (can be empty list)
max_turns: 2                       # cap on total turns (user + assistant combined)
rubric: |
  2-5 sentences describing what a 1.0 response looks like and what causes a score
  below 0.7. Written for a judge LLM audience — be explicit and unambiguous.
```

3. Use either `prompt` (singular string) or `prompts` (list) — never both in the same
   file.

4. Run the dry-run check to verify your YAML parses correctly before committing:

```bash
go run ./evals/cmd/eval-runner --dry-run --scenarios evals/scenarios
```

5. Verify uniqueness of IDs:

```bash
grep "^id:" evals/scenarios/**/*.yaml | sort -u | wc -l
# must equal total number of scenario files
```

---

## Running locally

Start a gateway first (see the main CLAUDE.md for the full gateway startup procedure):

```bash
export OMNIPUS_HOME=/tmp/omnipus-eval
rm -rf "$OMNIPUS_HOME" && mkdir -p "$OMNIPUS_HOME"
OMNIPUS_BEARER_TOKEN="" ./omnipus gateway --allow-empty &
```

Run a single category:

```bash
go run ./evals/cmd/eval-runner \
  --scenarios evals/scenarios/persona \
  --out /tmp/eval-$(date +%Y-%m-%d).jsonl \
  --gateway-url http://localhost:6060
```

Run all categories:

```bash
go run ./evals/cmd/eval-runner \
  --scenarios evals/scenarios \
  --out evals/results/$(date +%Y-%m-%d).jsonl \
  --gateway-url http://localhost:6060
```

Required environment variables:

```bash
export OPENROUTER_API_KEY=sk-or-...   # used for the agent and judge calls

# Optional overrides (OpenRouter slugs, openrouter/<vendor>/<model>). Both
# default to the repo's single source of truth: tests/e2e/e2e-model.json —
# the agent AND the judge follow it (founder decision 2026-09-27); there is
# no hardcoded fallback model.
# export AGENT_MODEL=openrouter/<vendor>/<model>
# export JUDGE_MODEL=openrouter/<vendor>/<model>
```

---

## How to read REPORT.md

REPORT.md is regenerated after every nightly run. It contains:

- **Latest scores table** — one row per scenario, columns for each dimension
  (completion, tools, persona, safety, efficiency) plus a weighted overall score.
- **7-day mean** — rolling average per scenario to smooth out judge variance.
- **Trend arrow** — up or down compared to the previous run.
- **Regression flag** — a scenario is flagged in red when its overall score drops 0.15
  or more from the 7-day mean. This is the primary signal to act on.

Example row:

```
| persona.mia-greets | 0.92 | 1.00 | 0.95 | 1.00 | 0.90 | 0.95 | 0.93 (7d) | stable |
```

Columns: scenario | completion | tools | persona | safety | efficiency | overall | 7d mean | trend

---

## When to update rubrics

Update a rubric only when the intended behavior of the agent has changed — for example,
after a new prompt is deployed or a tool is renamed. Do not update rubrics to make
failing scenarios pass without a corresponding code or prompt change. Doing so masks
regressions and defeats the purpose of the harness.

When you update a rubric, note the reason in your commit message so reviewers can
distinguish intentional behavior changes from score gaming.

---

## Cost and model choice

- Agent model: the ONE central setting in `tests/e2e/e2e-model.json`, via
  OpenRouter (tool-use capable; swap it by editing that one file).
- Judge model: the same central setting — the judge is no longer a second,
  separately chosen model (founder decision 2026-09-27).
- Estimated cost: 15 scenarios x 2 LLM calls x ~1k tokens each; per-run cost
  depends on the configured model. To reduce cost, run only one category with
  `--scenarios evals/scenarios/safety` or set a cheaper AGENT_MODEL/JUDGE_MODEL
  override for a development run.

Switching the model for e2e AND evals (the only sanctioned procedure): edit
`tests/e2e/e2e-model.json` AND add the new id to `PATTERN` in
`scripts/check-no-hardcoded-e2e-model.sh` — the old id stays banned, so it can
never scatter back into live config.

---

## CI and nightly schedule

The harness runs on a nightly cron at 02:00 UTC via `.github/workflows/evals-nightly.yml`.
It is not gated on pull requests — eval scores are informational signals, not blockers,
unless a manual review promotes a regression to PR-blocking status.

Secrets required in repo settings:
- `OPENROUTER_API_KEY_EVAL` — used by the nightly workflow for both agent and judge.

---

## Ethics note

The judge model is fallible. It may misread rubric intent, be inconsistent across runs,
or reflect the biases of its own training. Treat REPORT.md as a signal, not an oracle.

A regression flag means: "start a conversation about what changed." It does not mean
"the agent is broken." Promote a scenario to PR-blocking only after at least one manual
review confirms the regression is real and reproducible.

Conversely, a stable green score does not mean everything is fine — the judge may be
consistently missing a subtle failure mode. Review the judge reasoning column in the
JSONL output periodically, not just the summary table.
