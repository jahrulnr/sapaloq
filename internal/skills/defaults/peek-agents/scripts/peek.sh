#!/usr/bin/env bash
# peek.sh - summarize SapaLOQ sub-agents from their on-disk artifacts.
#
# No jq required: JSON fields are extracted with grep/sed so this runs on a bare
# host. Read-only. Best-effort: missing/empty/corrupt files are skipped, not
# fatal. Empty state still exits 0.
#
# Usage:
#   peek.sh [task-id]
#
# State location resolution (first match wins):
#   1) $1 looks like a task id -> drill into that one task
#   2) $STATE_PATH / $state_path env var
#   3) ${SAPALOQ_DATA_DIR:-$HOME/SapaLOQ}/state
#
# Output (one line per agent):
#   <id>  role=<role>  status=<status>  phase=<phase>  hb=<last_heartbeat>

set -u

# --- resolve state dir -------------------------------------------------------
state_dir="${STATE_PATH:-${state_path:-}}"
if [ -z "$state_dir" ]; then
  data_dir="${SAPALOQ_DATA_DIR:-$HOME/SapaLOQ}"
  state_dir="$data_dir/state"
fi

tasks_dir="$state_dir/tasks"
workers_dir="$state_dir/workers"

# --- tiny no-jq JSON string-field reader ------------------------------------
# json_field <file> <key> : prints the first "key": "value" string value, or "".
json_field() {
  _file="$1"; _key="$2"
  [ -f "$_file" ] || { printf ''; return 0; }
  # Match  "key" : "value"  tolerating whitespace; take first hit.
  sed -n 's/.*"'"$_key"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$_file" 2>/dev/null | head -n1
}

print_task() {
  _id="$1"
  _status_file="$tasks_dir/$_id/status.json"
  _health_file="$workers_dir/$_id/health.json"

  _role="$(json_field "$_status_file" role)"
  _status="$(json_field "$_status_file" status)"
  _phase="$(json_field "$_health_file" phase)"
  _hb="$(json_field "$_health_file" last_heartbeat)"

  [ -n "$_role" ] || _role="-"
  [ -n "$_status" ] || _status="-"
  [ -n "$_phase" ] || _phase="-"
  [ -n "$_hb" ] || _hb="-"

  printf '%s  role=%s  status=%s  phase=%s  hb=%s\n' \
    "$_id" "$_role" "$_status" "$_phase" "$_hb"

  # On drill-down, surface error / question / result tails too.
  if [ "${DRILL:-0}" = "1" ]; then
    _err="$(json_field "$_status_file" error)"
    _q="$(json_field "$_status_file" question)"
    [ -n "$_err" ] && printf '    error: %s\n' "$_err"
    [ -n "$_q" ] && printf '    awaiting: %s\n' "$_q"
    _elog="$workers_dir/$_id/error.log"
    if [ -s "$_elog" ]; then
      printf '    error.log (last 5):\n'
      tail -n 5 "$_elog" 2>/dev/null | sed 's/^/      /'
    fi
  fi
}

# --- drill into one task -----------------------------------------------------
if [ "$#" -ge 1 ] && [ -n "${1:-}" ]; then
  DRILL=1 print_task "$1"
  exit 0
fi

# --- roster: every task that has a status.json -------------------------------
found=0
if [ -d "$tasks_dir" ]; then
  for d in "$tasks_dir"/*/; do
    [ -d "$d" ] || continue
    id="$(basename "$d")"
    [ -f "$tasks_dir/$id/status.json" ] || continue
    print_task "$id"
    found=1
  done
fi

if [ "$found" = "0" ]; then
  printf 'No tasks found under %s\n' "$tasks_dir"
fi
exit 0
