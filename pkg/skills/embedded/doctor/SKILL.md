---
name: doctor
description: Diagnose a concrete setup fault, apply a supported repair, and tell the requester to re-probe their own environment.
---
# Doctor

## Prerequisites

A failing workflow or dependency probe is available.

## Steps

1. Run `tool:run_doctor` and the narrow failing probe on Admin's own environment.
2. Identify the cause rather than listing symptoms.
3. Apply the supported repair with the relevant configuration or `tool:bash`/file tools.
4. Re-run the failing probe in Admin's own environment to confirm the repair. Then require the requester to re-probe their own environment; do not claim another agent's environment is repaired from an Admin-side probe.

## Expected output

A repaired Admin-side fault plus an instruction for the requester to re-probe, or a precise unsupported result.

## Stop and handoff

Do not claim repair from diagnostics alone. Missing document and workspace dependencies are requested by the working agent through its own Ask-gated environment_setup tool; do not install on another agent's behalf. Keep the requester's work resumable until that agent reports a passing probe in its own environment, or until privileges or platform support are absent.
