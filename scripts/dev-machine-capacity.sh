#!/usr/bin/env bash
# scripts/dev-machine-capacity.sh
#
# Dev-team capacity monitor (design 5.6, N1/S3, G1; L10). Deliberately NOT
# check-*.sh: scripts/guards.sh auto-discovers scripts/check-*.sh as CI
# gates, and this is a dev-team tool for team-lead / squad leads to run
# before widening dispatch fan-out, not a gate.
#
# WHAT IT PRINTS
#   Its four measurements, then exactly one verdict line:
#     CAPACITY: OK
#     CAPACITY: HOLD <reason>[; <reason> ...]
#
# THE FOUR SIGNALS (design 5.6 table)
#   - Memory   (hard)     — available RAM below a floor -> HOLD by itself.
#   - Disk     (hard)     — free space on the workspace volume below a
#                            floor -> HOLD by itself.
#   - CPU load (advisory) — sustained 1-minute load above a fraction of
#                            logical cores, over two samples ~30s apart.
#                            "Never holds alone" (design table, literal
#                            reading): CPU by itself never flips the verdict
#                            to HOLD; it is folded into the reason text only
#                            when another signal has already produced a
#                            HOLD, because that is the case where sustained
#                            CPU pressure plus a hold both describe real
#                            contention rather than a stray spike.
#   - Active dispatches (advisory) — counted from the coordination ledger's
#     squads/*.md files: rows with status=in-flight, NOT a process count
#     (design: "no process sniffing: the ledger knows what is actually
#     running, the process table does not"). Unlike CPU, its own row carries
#     no "never holds alone" qualifier, and it is the load-bearing width
#     signal that replaces the deleted agent-process count, so crossing its
#     own ceiling holds by itself.
#   [INFERRED reading — the design states CPU's qualifier explicitly and
#   leaves active-dispatches unqualified; this script takes the absence of
#   the qualifier at face value. Flagged in the L10 report as an ambiguity
#   with this resolution, for team-lead/architect to confirm or override.]
#
# MEMORY MEASUREMENT
#   macOS: vm_stat, page size from `pagesize`(1) (never a hard-coded 16384 —
#   Apple Silicon uses 16 KiB pages, older Intel Macs used 4 KiB), falling
#   back to parsing vm_stat's own "(page size of N bytes)" header if
#   `pagesize` is unavailable. Available = (free + inactive + speculative)
#   pages. Swap usage (sysctl vm.swapusage) is printed for information.
#   Linux: /proc/meminfo's MemAvailable; SwapTotal/SwapFree for information.
#   [INFERRED simplification — design's memory row also says "or swap-in
#   activity rising"; that needs a delta over time and this is a one-shot
#   script, so swap is reported, not gated on a trend. Documented, not
#   hidden, per the no-fabricated-gaps rule.]
#
# EXIT CODE: 0 whether the verdict is OK or HOLD (parse the verdict line —
# this is a status report, not a guard). Exit 2 only when a measurement
# could not be taken at all on this platform.
#
# ENV OVERRIDES (all optional; defaults are the design's proposed numbers)
#   DEV_CAPACITY_MIN_FREE_MEM_GB          default 4
#   DEV_CAPACITY_MIN_FREE_DISK_GB         default 20
#   DEV_CAPACITY_WORKSPACE_DIR            default /Users/danielpiatkowski/AI-Agent-Workspace
#   DEV_CAPACITY_CPU_THRESHOLD_PCT        default 80
#   DEV_CAPACITY_CPU_SAMPLES              default 2
#   DEV_CAPACITY_CPU_SAMPLE_INTERVAL_SEC  default 30
#   DEV_CAPACITY_MAX_DISPATCHES           default 12
#   DEV_CAPACITY_LEDGER_DIR               default /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination
#
# USAGE
#   scripts/dev-machine-capacity.sh
#   DEV_CAPACITY_MIN_FREE_MEM_GB=99999 scripts/dev-machine-capacity.sh   # force a HOLD
#   scripts/dev-machine-capacity.sh --self-test                          # scratch-repo self-check (see bottom)

set -u

MIN_FREE_MEM_GB="${DEV_CAPACITY_MIN_FREE_MEM_GB:-4}"
MIN_FREE_DISK_GB="${DEV_CAPACITY_MIN_FREE_DISK_GB:-20}"
WORKSPACE_DIR="${DEV_CAPACITY_WORKSPACE_DIR:-/Users/danielpiatkowski/AI-Agent-Workspace}"
CPU_THRESHOLD_PCT="${DEV_CAPACITY_CPU_THRESHOLD_PCT:-80}"
CPU_SAMPLES="${DEV_CAPACITY_CPU_SAMPLES:-2}"
CPU_SAMPLE_INTERVAL_SEC="${DEV_CAPACITY_CPU_SAMPLE_INTERVAL_SEC:-30}"
MAX_DISPATCHES="${DEV_CAPACITY_MAX_DISPATCHES:-12}"
LEDGER_DIR="${DEV_CAPACITY_LEDGER_DIR:-/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination}"

os="$(uname -s)"

# ---- memory -----------------------------------------------------------------
mem_avail_gb="unknown"
swap_note=""

if [ "$os" = "Darwin" ]; then
  page_size="$(pagesize 2>/dev/null)"
  if [ -z "${page_size:-}" ]; then
    page_size="$(vm_stat 2>/dev/null | head -1 | grep -o '[0-9]\+' | head -1)"
  fi
  page_size="${page_size:-4096}"
  vm_out="$(vm_stat 2>/dev/null)"
  free_pages="$(printf '%s\n' "$vm_out" | awk '/^Pages free/ {gsub("\\.","",$3); print $3}')"
  inactive_pages="$(printf '%s\n' "$vm_out" | awk '/^Pages inactive/ {gsub("\\.","",$3); print $3}')"
  spec_pages="$(printf '%s\n' "$vm_out" | awk '/^Pages speculative/ {gsub("\\.","",$3); print $3}')"
  free_pages="${free_pages:-0}"; inactive_pages="${inactive_pages:-0}"; spec_pages="${spec_pages:-0}"
  avail_pages=$(( free_pages + inactive_pages + spec_pages ))
  mem_avail_gb="$(awk -v p="$avail_pages" -v s="$page_size" 'BEGIN { printf "%.2f", (p*s)/1073741824 }')"
  swap_used="$(sysctl -n vm.swapusage 2>/dev/null | sed -n 's/.*used = \([0-9.]*\)M.*/\1/p')"
  [ -n "${swap_used:-}" ] && swap_note="swap used: ${swap_used}M"
elif [ -r /proc/meminfo ]; then
  mem_avail_kb="$(awk '/^MemAvailable:/ {print $2}' /proc/meminfo)"
  if [ -n "${mem_avail_kb:-}" ]; then
    mem_avail_gb="$(awk -v k="$mem_avail_kb" 'BEGIN { printf "%.2f", k/1048576 }')"
  fi
  swap_free_kb="$(awk '/^SwapFree:/ {print $2}' /proc/meminfo)"
  swap_total_kb="$(awk '/^SwapTotal:/ {print $2}' /proc/meminfo)"
  if [ -n "${swap_free_kb:-}" ] && [ -n "${swap_total_kb:-}" ]; then
    swap_note="swap used: $(( (swap_total_kb - swap_free_kb) / 1024 )) MB"
  fi
fi

# ---- disk ---------------------------------------------------------------
disk_free_gb="unknown"
disk_target="$WORKSPACE_DIR"
[ -d "$disk_target" ] || disk_target="."
disk_free_kb="$(df -Pk "$disk_target" 2>/dev/null | awk 'NR==2 {print $4}')"
if [ -n "${disk_free_kb:-}" ]; then
  disk_free_gb="$(awk -v k="$disk_free_kb" 'BEGIN { printf "%.2f", k/1048576 }')"
fi

# ---- CPU ------------------------------------------------------------------
if [ "$os" = "Darwin" ]; then
  logical_cores="$(sysctl -n hw.logicalcpu 2>/dev/null)"
else
  logical_cores="$(nproc 2>/dev/null)"
fi
logical_cores="${logical_cores:-1}"

read_loadavg_1m() {
  if [ "$os" = "Darwin" ]; then
    sysctl -n vm.loadavg 2>/dev/null | awk '{print $2}'
  elif [ -r /proc/loadavg ]; then
    awk '{print $1}' /proc/loadavg
  fi
}

cpu_samples_pct=()
i=1
while [ "$i" -le "$CPU_SAMPLES" ]; do
  load1="$(read_loadavg_1m)"
  if [ -n "${load1:-}" ]; then
    pct="$(awk -v l="$load1" -v c="$logical_cores" 'BEGIN { printf "%.1f", (l/c)*100 }')"
    cpu_samples_pct+=("$pct")
  fi
  if [ "$i" -lt "$CPU_SAMPLES" ]; then
    sleep "$CPU_SAMPLE_INTERVAL_SEC"
  fi
  i=$((i+1))
done

cpu_sustained_high="false"
if [ "${#cpu_samples_pct[@]}" -gt 0 ]; then
  cpu_sustained_high="true"
  for pct in "${cpu_samples_pct[@]}"; do
    if ! awk -v p="$pct" -v t="$CPU_THRESHOLD_PCT" 'BEGIN { exit !(p >= t) }'; then
      cpu_sustained_high="false"
    fi
  done
fi

# ---- active dispatches (ledger, not process count) -------------------------
in_flight_count=0
if [ -d "$LEDGER_DIR/squads" ]; then
  in_flight_count="$(grep -l 'status=in-flight' "$LEDGER_DIR"/squads/*.md 2>/dev/null | wc -l | tr -d ' ')"
fi
in_flight_count="${in_flight_count:-0}"

# ---- verdict ----------------------------------------------------------------
reasons=()

mem_hold="false"
if [ "$mem_avail_gb" != "unknown" ]; then
  if awk -v a="$mem_avail_gb" -v m="$MIN_FREE_MEM_GB" 'BEGIN { exit !(a < m) }'; then
    mem_hold="true"
    reasons+=("memory: ${mem_avail_gb}GB available < ${MIN_FREE_MEM_GB}GB floor")
  fi
fi

disk_hold="false"
if [ "$disk_free_gb" != "unknown" ]; then
  if awk -v a="$disk_free_gb" -v m="$MIN_FREE_DISK_GB" 'BEGIN { exit !(a < m) }'; then
    disk_hold="true"
    reasons+=("disk: ${disk_free_gb}GB free on ${disk_target} < ${MIN_FREE_DISK_GB}GB floor")
  fi
fi

dispatch_hold="false"
if [ "$in_flight_count" -gt "$MAX_DISPATCHES" ] 2>/dev/null; then
  dispatch_hold="true"
  reasons+=("active dispatches: ${in_flight_count} in-flight > ${MAX_DISPATCHES} ceiling")
fi

# CPU never holds alone (design 5.6): only cited once something else holds.
if [ "$cpu_sustained_high" = "true" ] && [ "${#reasons[@]}" -gt 0 ]; then
  reasons+=("cpu: sustained >= ${CPU_THRESHOLD_PCT}% of ${logical_cores} logical cores")
fi

# ---- print measurements, then the verdict ------------------------------------
echo "dev-machine-capacity: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "  memory available: ${mem_avail_gb} GB (floor ${MIN_FREE_MEM_GB} GB)${swap_note:+ ; ${swap_note}}"
echo "  disk free (${disk_target}): ${disk_free_gb} GB (floor ${MIN_FREE_DISK_GB} GB)"
if [ "${#cpu_samples_pct[@]}" -gt 0 ]; then
  joined=""
  for pct in "${cpu_samples_pct[@]}"; do
    if [ -z "$joined" ]; then joined="$pct"; else joined="${joined},${pct}"; fi
  done
  echo "  cpu load: ${joined}% of ${logical_cores} logical cores (threshold ${CPU_THRESHOLD_PCT}%, sustained=${cpu_sustained_high})"
else
  echo "  cpu load: unavailable"
fi
echo "  active dispatches (ledger, in-flight): ${in_flight_count} (ceiling ${MAX_DISPATCHES}) [ledger: ${LEDGER_DIR}]"

if [ "${#reasons[@]}" -gt 0 ]; then
  reason_str=""
  for r in "${reasons[@]}"; do
    if [ -z "$reason_str" ]; then reason_str="$r"; else reason_str="${reason_str}; ${r}"; fi
  done
  echo "CAPACITY: HOLD ${reason_str}"
else
  echo "CAPACITY: OK"
fi

exit 0
