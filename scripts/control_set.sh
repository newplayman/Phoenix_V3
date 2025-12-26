#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
mkdir -p var

cmd="${1:-}"
arg1="${2:-}"
arg2="${3:-}"

json_escape() {
  local s="${1:-}"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\n'/ }"
  printf '%s' "$s"
}

write_control() {
  local desired_state="$1"
  local force_dry_run="$2"
  local risk_mode="$3"
  local reason="$4"
  cat > var/control.json <<EOF
{"desired_state":"$desired_state","force_dry_run":$force_dry_run,"risk_mode":"$risk_mode","reason":"$(json_escape "$reason")"}
EOF
  echo "wrote var/control.json"
}

case "$cmd" in
  pause)
    write_control "PAUSED" "false" "" "${arg1:-manual}"
    ;;
  resume)
    write_control "RUNNING" "false" "" "${arg1:-manual}"
    ;;
  safe-mode)
    write_control "SAFE_MODE" "false" "" "${arg1:-manual}"
    ;;
  force-dry-run-on)
    write_control "RUNNING" "true" "" "${arg1:-manual}"
    ;;
  force-dry-run-off)
    write_control "RUNNING" "false" "" "${arg1:-manual}"
    ;;
  risk-mode)
    if [[ -z "${arg1:-}" ]]; then
      echo "usage: $0 risk-mode <SAFE|DENY|PAUSE|SAFE_MODE|HALT> [reason]" >&2
      exit 2
    fi
    write_control "RUNNING" "false" "$arg1" "${arg2:-manual}"
    ;;
  *)
    cat >&2 <<EOF
usage:
  $0 pause [reason]
  $0 resume [reason]
  $0 safe-mode [reason]
  $0 force-dry-run-on [reason]
  $0 force-dry-run-off [reason]
  $0 risk-mode <SAFE|DENY|PAUSE|SAFE_MODE|HALT> [reason]
EOF
    exit 2
    ;;
esac

