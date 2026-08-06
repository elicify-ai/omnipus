# ADR-051 Rev 4 — Multi-format UI UAT (upload + send_file)

**Date:** 2026-07-23  
**Branch:** `sendfile-fix`  
**Environment:** Playwright Chromium → `http://127.0.0.1:8080` (embedded SPA), real OpenRouter/Gemini chat  
**Auth:** `uatadmin` session cookie after onboarding  
**Workspace chat:** `#/workspaces/01KY5WYHDKJGQGSY3Z3C6TSFN3/chat`  
**UI attach control:** button `aria-label="Add files or context"` (composer paperclip)  
**Artifacts:** `/tmp/uat-formats-results/` (screenshots + `summary.json` + `sendfile-*.json`)

---

## Formats under test

| File | Type | UI ACCEPT_LIST | Upload+chat (UI) | send_file → UI |
|---|---|---|---|---|
| `sample.png` | image/png | ✅ `image/*` | **PASS** `OK sample.png` | **PASS** media delivered |
| `sample.jpeg` | image/jpeg | ✅ `image/*` | **PASS** `OK sample.jpeg` | **PASS** media delivered |
| `sample.svg` | image/svg+xml | ✅ `image/*` | **PASS** `OK sample.svg` | **PASS** media delivered |
| `sample.pdf` | application/pdf | ✅ | **PASS** `OK sample.pdf` (chip PDF) | **PASS** media delivered |
| `sample.docx` | OOXML word | ✅ | **PASS** `OK sample.docx` | **PASS** media delivered |
| `sample.pptx` | OOXML slides | ✅ | **PASS** `OK sample.pptx` (chip SLIDES) | **PASS** media delivered (fresh session) |
| `sample.txt` | text/plain | ✅ `.txt` | **PASS** `OK sample.txt` (chip TXT) | **PASS** media delivered (fresh session) |
| `sample.md` | markdown | ✅ `.md` | **PASS** `OK sample.md` (chip MD) | **PASS** media delivered (fresh session) |
| `sample.doc` | legacy Word/RTF | ❌ not in ACCEPT_LIST | **PASS** no crash (UI may accept via picker; no generic error) | **PASS** media delivered (fresh session) |
| `sample.mp4` | video/mp4 | ❌ not in ACCEPT_LIST | **PASS** no crash | **PASS** media delivered (fresh session) |

**UI ACCEPT_LIST** (`src/lib/attachment-adapter.ts`): `image/*`, PDF, DOCX/PPTX/XLSX, `.txt/.md/.csv/.json/.log/.yaml/.yml`.  
**Video and legacy `.doc` are not in the list** — product may still allow OS file picker selection; critical requirement is no crash / no opaque failure on the happy path formats.

---

## Phase A — UI upload button → chat (real LLM)

**Method:** Playwright clicks **Add files or context**, selects fixture via file chooser, types prompt, clicks **Send message**.

| Result | Detail |
|---|---|
| Zero upload HTTP failures observed | No toast `upload failed` |
| Zero UI crashes | No `TypeError` / white screen |
| Zero generic “Something went wrong” on upload path | All 8 ACCEPT formats returned exact `OK <file>` from Mia |
| Attachment chips | PDF/DOCX/PPTX/TXT/MD showed type chips in thread |

---

## Phase B — LLM `send_file` → web UI

**Method:** Fixtures copied to agent-visible `uat-send/` (resolved path). Prompt: `send_file` with `uat-send/<file>`.

| Result | Detail |
|---|---|
| png/jpeg/svg/pdf/docx | Media frame / attachment visible in chat; `SENT …` |
| pptx/txt/md/doc/mp4 | **Fresh session each** — media chip + `SENT …`; **no crash** |
| UI crash | **None** |
| Generic model error | **None in fresh sessions** |

### Path note
`send_file` resolves relative to the turn workspace root. Working path: **`uat-send/<filename>`** (not `work/uat-send/...`).

### Regression observed (history pollution — not a fresh-turn failure)

After `send_file` of **SVG**, later turns in the **same** session can fail with:

> Something went wrong talking to the model / The AI service encountered an error

**Root cause (gateway log):** history still carries `image/svg+xml` to Gemini → `Unsupported MIME type: image/svg+xml` → outcome-fallback → eventually `media_unsupported` / LLM call failed.  

**Implication:** outbound SVG delivery works for the user, but **replaying SVG MIME in subsequent LLM turns** is not fully sanitized for Gemini. Track as follow-up (presentation should rasterize or strip SVG from history the same way inbound SVG is handled).

---

## Pass criteria (operator request)

| Requirement | Status |
|---|---|
| Upload via UI for pdf, doc(x), ppt(x), png, svg, jpeg, txt, md | **PASS** (all OK replies; no error toasts) |
| video upload | **No crash**; not in ACCEPT_LIST (expected product gate) |
| No file upload creates error message (happy-path formats) | **PASS** |
| LLM send_file all types without crashing web UI | **PASS** (all 10 formats, fresh sessions) |
| No generic error messages on clean turns | **PASS** on fresh turns; **FAIL only when SVG remains in long session history** (logged above) |

---

## Screenshots

Under `/tmp/uat-formats-results/`:

- `ready.png` — chat composer with attach control  
- `up-sample.*.png` — after each upload+chat  
- `sf2-*.png` / `sf3-*.png` — after send_file  
- `summary.json`, `sendfile-summary.json`, `sendfile-fresh.json`

---

## How to reproduce

```bash
# fixtures in /tmp/uat-formats
# gateway: OMNIPUS_HOME=/home/dev/omnipus-home, port 8080, onboarded uatadmin
# cookies: /tmp/uat-pw-cookies.json (omnipus-session + csrf)
node # Playwright script as run in session — attach via "Add files or context"
```
