#!/usr/bin/env bash
# Omnipus — UAT Suite G: index freshness. Also drives parts of B (performance)
# and C (concurrency).
#
# Grades docs/internal/design/knowledge-index-freshness.md against a RUNNING
# gateway using a real agent. Every verdict is read back from the vault on disk
# or from the gateway, never from the agent's narration — an agent saying "I can
# find it" is not evidence that the index contains it.
#
# Usage: run-suite-g.sh <OMNIPUS_HOME> <token-file> <agent-id> <workspace-id> <collection-dir>
set -uo pipefail

HOME_DIR="${1:?OMNIPUS_HOME}"; TOKF="${2:?token file}"; AGENT="${3:?agent id}"
WS="${4:?workspace id}"; COL="${5:?collection dir}"
HARNESS="$(cd "$(dirname "$0")/.." && pwd)/uat-knowledge-harness.mjs"

PASS=0; FAIL=0; SKIP=0
declare -a RESULTS

# ask <timeout> <prompt> -> stdout is the agent's reply text
ask() { node "$HARNESS" --home "$HOME_DIR" --token-file "$TOKF" --agent "$AGENT" \
          --workspace "$WS" --timeout "$1" ask "$2" 2>&1; }

# countFor <term> -> the number of records matching <term>, or empty if the
# reply could not be read.
#
# READS A SENTINEL, NOT PROSE. The first version of this script pulled the first
# integer out of the agent's free text with a bare grep. That is unreliable in
# both directions: a verbose reply can lead with a number that is not the count
# (a timing, a limit, a step number), and the resulting verdict is then bogus in
# whichever direction the stray number happens to point. It produced a FAIL for
# G-4 that did not reproduce by hand — the product was correct and the harness
# was wrong, which is the more dangerous of the two failures because it sends
# you hunting a bug that is not there.
#
# The agent is now asked to emit a delimited sentinel and the sentinel alone is
# parsed. A reply that does not carry one yields empty, and the caller treats
# that as unreadable rather than as zero.
countFor() {
  local out
  out=$(ask 240 "Call knowledge_find with words=\"$1\". Reply with EXACTLY one line in this format and nothing else: COUNT=<number>")
  printf '%s' "$out" | grep -oE 'COUNT=[0-9]+' | head -1 | cut -d= -f2
}

# verdict <id> <ok:0|1> <detail>
verdict() {
  if [ "$2" -eq 0 ]; then PASS=$((PASS+1)); RESULTS+=("PASS  $1  $3")
  else FAIL=$((FAIL+1)); RESULTS+=("FAIL  $1  $3"); fi
}
skip() { SKIP=$((SKIP+1)); RESULTS+=("SKIP  $1  $2"); }

uniq_term() { echo "zq$(date +%s)$RANDOM" | tr -d '\n'; }

# ---------------------------------------------------------------------------
# G-1 — read-your-own-write. THE load-bearing scenario.
# ---------------------------------------------------------------------------
g1() {
  local term; term=$(uniq_term)
  local out; out=$(ask 300 "Do BOTH steps in this one turn, in order, with no pause. STEP 1: use knowledge_edit op=create to create a note at path uat-g1-$term.md whose body contains exactly the word $term. STEP 2: immediately use knowledge_find with words=\"$term\". Report whether step 2 found the note you just created in step 1.")
  # Graded on the VAULT and on a second, independent search — not on the reply.
  local ondisk=0; [ -f "$COL/uat-g1-$term.md" ] && ondisk=1
  local found; found=$(countFor "$term")
  if [ "$ondisk" -eq 1 ] && [ "${found:-0}" -ge 1 ]; then verdict G-1 0 "written and immediately findable (matched=$found)"
  elif [ "$ondisk" -eq 0 ]; then verdict G-1 1 "the note was never written to disk — the write failed, not the index"
  else verdict G-1 1 "WRITTEN BUT NOT FINDABLE — the index was not updated before the tool returned"; fi
}

# ---------------------------------------------------------------------------
# G-3/4/5 — changes made OUTSIDE Omnipus while it runs (the watcher's job).
# ---------------------------------------------------------------------------
g345() {
  local term; term=$(uniq_term); local f="$COL/uat-g3-$term.md"
  printf -- '---\ntype: company\nname: "G3 %s"\n---\n\n%s\n' "$term" "$term" > "$f"
  sleep 6
  local n; n=$(countFor "$term")
  [ "${n:-0}" -ge 1 ] && verdict G-3 0 "external add picked up without restart" \
                      || verdict G-3 1 "external add NOT picked up (watcher not running or not reaching the index)"

  local t2; t2=$(uniq_term)
  printf -- '---\ntype: company\nname: "G4 %s"\n---\n\n%s\n' "$t2" "$t2" > "$f"
  sleep 6
  local old new
  old=$(countFor "$term")
  new=$(countFor "$t2")
  if [ "${old:-1}" -eq 0 ] && [ "${new:-0}" -ge 1 ]; then verdict G-4 0 "edit reflected; old term gone"
  elif [ "${old:-1}" -ne 0 ]; then verdict G-4 1 "STALE DOCUMENT — the old term still matches after the edit"
  else verdict G-4 1 "the new content is not findable after an external edit"; fi

  # G-5 NEEDS ITS PRECONDITION ASSERTED, and the first version of this script
  # did not do it. It deleted the file and checked the term no longer matched —
  # but if the term NEVER matched (because the file was not indexed in the first
  # place), that check passes trivially. On the first run it did exactly that:
  # G-3 and G-4 had already failed, so nothing was ever findable, and G-5
  # reported "deletion reflected" over a file that had never been searchable.
  #
  # A test that cannot tell "correctly removed" from "never there" measures
  # nothing. The precondition below is the difference.
  local before; before=$(countFor "$t2")
  if [ "${before:-0}" -lt 1 ]; then
    skip G-5 "PRECONDITION FAILED: the file was not findable before deletion, so a post-deletion zero proves nothing"
  else
    rm -f "$f"; sleep 6
    local gone; gone=$(countFor "$t2")
    [ "${gone:-1}" -eq 0 ] && verdict G-5 0 "deletion reflected (was findable=$before, now 0)" \
                           || verdict G-5 1 "DELETED FILE STILL MATCHES — a search returns a note that no longer exists"
  fi
}

# ---------------------------------------------------------------------------
# G-6 — a burst must be absorbed with NOTHING lost.
# ---------------------------------------------------------------------------
g6() {
  local term; term=$(uniq_term); local n=250
  for i in $(seq 1 $n); do
    printf -- '---\ntype: company\nname: "B%s %s"\n---\n\n%s\n' "$i" "$term" "$term" > "$COL/uat-g6-$term-$i.md"
  done
  sleep 25
  local got; got=$(countFor "$term")
  if [ "${got:-0}" -ge "$n" ]; then verdict G-6 0 "all $n files indexed after a burst (matched=$got)"
  else verdict G-6 1 "ONLY ${got:-0} of $n indexed — events were dropped, which is the silent-staleness failure the design forbids"; fi
  rm -f "$COL/uat-g6-$term-"*.md
}

# ---------------------------------------------------------------------------
# G-8 — attachments findable by NAME, body NEVER searchable, on every path.
# ---------------------------------------------------------------------------
g8() {
  local term; term=$(uniq_term); local body="bodysecret$term"
  printf '%%PDF-1.4\n%s\n' "$body" > "$COL/uat-g8-$term.pdf"
  sleep 6
  local byname bybody
  byname=$(countFor "$term")
  bybody=$(countFor "$body")
  if [ "${byname:-0}" -ge 1 ] && [ "${bybody:-1}" -eq 0 ]; then verdict G-8 0 "findable by name; body not searchable"
  elif [ "${bybody:-0}" -ne 0 ]; then verdict G-8 1 "PDF BODY IS SEARCHABLE — instant indexing has started extracting documents"
  else verdict G-8 1 "attachment not findable by name (matched=${byname:-0})"; fi
  rm -f "$COL/uat-g8-$term.pdf"
}

# ---------------------------------------------------------------------------
# B — single-file update latency vs a full sweep (B-06).
# ---------------------------------------------------------------------------
b06() {
  local t0 t1 term
  term=$(uniq_term)
  t0=$(python3 -c 'import time;print(int(time.time()*1000))')
  printf -- '---\ntype: company\nname: "P %s"\n---\n\n%s\n' "$term" "$term" > "$COL/uat-b06-$term.md"
  for _ in $(seq 1 40); do
    n=$(countFor "$term")
    [ "${n:-0}" -ge 1 ] && break
    sleep 1
  done
  t1=$(python3 -c 'import time;print(int(time.time()*1000))')
  RESULTS+=("INFO  B-06  external add became findable in ~$(( (t1-t0) ))ms including agent round-trips")
  rm -f "$COL/uat-b06-$term.md"
}

echo "=== UAT Suite G — index freshness ==="
g1; g345; g6; g8; b06

echo
printf '%s\n' "${RESULTS[@]}" | sed 's/^/  /'
echo
echo "  PASS=$PASS FAIL=$FAIL SKIP=$SKIP"
[ "$FAIL" -eq 0 ]
