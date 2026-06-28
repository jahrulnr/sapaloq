---
name: xdotool
description: Linux-only X11 desktop automation and inspection with xdotool. Use for xdotool, Linux X11 GUI automation, UI documentation, dokumentasi, functional or regression testing, smoke tests, demos, window discovery and activation, keyboard or mouse input, geometry inspection, and focus debugging. Do not use on non-Linux systems, for native Wayland windows, or when a direct API, IPC, browser automation, or application test interface provides a more reliable contract.
---

# xdotool Desktop Automation

Use `xdotool` on Linux to drive and inspect X11 windows through their visible user interface. Keep every sequence observable, narrowly scoped, and reversible. Stop on non-Linux systems; this skill deliberately provides no macOS, Windows, or BSD path.

## Purpose

- Reproduce deterministic UI steps before capturing documentation screenshots.
- Run functional, regression, and smoke tests against a real desktop application.
- Prepare repeatable demos and manual-test fixtures.
- Diagnose focus, active-window, keyboard, mouse, position, and geometry problems.
- Automate legacy X11 applications that expose no suitable API or test driver.

Prefer application APIs, IPC, browser test frameworks, or accessibility interfaces when they express the behavior directly. Use `xdotool` when the visible X11 interaction itself is the contract under test.

## Workflow

1. Run `bash scripts/check-environment.sh` from this skill directory. Stop unless it reports Linux and working X11 access.
2. Confirm the target is an X11 or XWayland window. Stop if it is native Wayland.
3. Inspect before acting. Record the active window, then identify the target by PID or `WM_CLASS`; use title matching only as a fallback.
4. Activate the exact target with `windowactivate --sync` and verify it is active.
5. Perform the smallest keyboard or mouse sequence needed. Prefer keyboard navigation over screen coordinates.
6. Verify a visible postcondition such as title, focus, geometry, application output, or a screenshot captured by a separate tool.
7. Restore focus or window state when the workflow should not leave the desktop changed.

## Discover and select windows

```bash
active_id="$(xdotool getactivewindow)"
xdotool getwindowname "$active_id"
xdotool getwindowgeometry --shell "$active_id"
xprop -id "$active_id" WM_CLASS _NET_WM_PID

# Prefer stable selectors. Review all matches before choosing one.
xdotool search --onlyvisible --pid "$app_pid"
xdotool search --onlyvisible --class 'Sapaloq'
xdotool search --onlyvisible --name 'Settings'
```

Never blindly act on the first search result. A selector can match hidden helper windows or several instances. Narrow the result and verify its name, class, PID, and visibility.

## Activate and interact

```bash
target_id="<verified-window-id>"
xdotool windowactivate --sync "$target_id"
test "$(xdotool getactivewindow)" = "$target_id"

# Clear held modifiers so automation is not affected by keyboard state.
xdotool key --window "$target_id" --clearmodifiers ctrl+l
xdotool type --window "$target_id" --clearmodifiers --delay 20 -- 'example text'
xdotool key --window "$target_id" --clearmodifiers Return

# Use window-relative coordinates only when keyboard navigation is unavailable.
xdotool mousemove --window "$target_id" 120 48 click 1
```

Use `--sync` where supported and wait on an observable state instead of fixed long sleeps. If a short render delay is unavoidable, keep it explicit and explain why.

## Documentation and testing

For documentation, establish a known application state, size and position the window if layout consistency matters, perform the flow, remove transient pointers or tooltips, then capture with a separate screenshot tool. `xdotool` does not capture screenshots.

For tests, define the precondition, action, and postcondition before sending input. Test invalid input, repeated actions, cancellation, focus loss, delayed windows, multiple matching windows, and restart behavior when relevant. A successful command exit only proves input was sent; it does not prove the application behaved correctly.

Use an isolated X server for CI when the application supports it:

```bash
xvfb-run -a sh -c 'start-test-app & app_pid=$!; run-ui-test "$app_pid"'
```

Do not claim compositor-level behavior was tested under Xvfb. Record manual verification for behavior that depends on the real window manager or desktop.

## Safety rules

- Do not type passwords, tokens, recovery codes, or other secrets through `xdotool` unless the user explicitly requires that exact visible workflow and exposure is controlled.
- Do not automate destructive confirmations, purchases, publishing, or external messages without explicit authorization.
- Do not use broad title regexes followed immediately by `click`, `key`, `type`, `windowclose`, or `windowkill`.
- Do not use `eval` to construct commands. Quote text and window IDs.
- Do not run input automation while the user is actively using the same desktop without warning them; focus can change between discovery and action.
- Prefer `windowclose` over `windowkill`; use neither unless closing is part of the requested flow.
- Save the original active window ID and restore it with `xdotool windowactivate --sync "$active_id"` when appropriate.

## Wayland boundary

`xdotool` communicates through X11. On Linux Wayland it may control XWayland clients, but it cannot reliably discover or control native Wayland windows. Confirm the target's backend rather than assuming that a non-empty `DISPLAY` makes the whole desktop controllable. For native Wayland, select a compositor-supported automation method or an application-specific interface and state the coverage difference.

## Resource

Run `scripts/check-environment.sh` before automation to report the Linux session type, X display, active-window access, and likely Wayland limitations without sending input.
