---
name: doctor
description: Diagnose a concrete setup fault, apply a supported repair, and repeat the failing probe.
---
# Doctor
## Prerequisites
A failing workflow or dependency probe is available.
## Steps
1. Run `tool:run_doctor` and the narrow failing probe.
2. Identify the cause rather than listing symptoms.
3. Apply the supported repair with the relevant configuration or `tool:bash`/file tools.
4. Repeat the original probe in the requesting worker's actual environment.
## Expected output
A repaired and verified fault, or a precise unsupported result.
## Stop and handoff
Do not claim repair from diagnostics alone. Keep work pending when privileges or platform support are absent.
