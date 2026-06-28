#!/usr/bin/env bash
set -u

fail=0

kernel="$(uname -s 2>/dev/null || printf unknown)"
printf 'kernel: %s\n' "$kernel"
if [[ "$kernel" != "Linux" ]]; then
  printf 'platform: unsupported (this skill is Linux-only)\n' >&2
  exit 1
fi
printf 'platform: supported\n'

if command -v xdotool >/dev/null 2>&1; then
  printf 'xdotool: %s\n' "$(command -v xdotool)"
  xdotool version 2>/dev/null || true
else
  printf 'xdotool: not found\n' >&2
  fail=1
fi

printf 'session_type: %s\n' "${XDG_SESSION_TYPE:-unknown}"
printf 'display: %s\n' "${DISPLAY:-unset}"
printf 'wayland_display: %s\n' "${WAYLAND_DISPLAY:-unset}"

if [[ -z "${DISPLAY:-}" ]]; then
  printf 'x11_access: unavailable (DISPLAY is unset)\n' >&2
  fail=1
elif command -v xdotool >/dev/null 2>&1; then
  if active_id="$(xdotool getactivewindow 2>/dev/null)"; then
    printf 'x11_access: ok\n'
    printf 'active_window_id: %s\n' "$active_id"
    printf 'active_window_name: '
    xdotool getwindowname "$active_id" 2>/dev/null || printf 'unavailable\n'
  else
    printf 'x11_access: failed to query active window\n' >&2
    fail=1
  fi
fi

if [[ "${XDG_SESSION_TYPE:-}" == "wayland" || -n "${WAYLAND_DISPLAY:-}" ]]; then
  printf '%s\n' 'warning: xdotool controls X11/XWayland clients, not native Wayland windows.' >&2
fi

exit "$fail"
