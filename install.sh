#!/usr/bin/env bash
#
# SapaLOQ installer.
#
# Builds the binaries, installs them into a user-local bin dir (default
# ~/.local/bin), seeds a default config under ~/.config/sapaloq (never
# overwriting an existing one), and — unless --no-service is given — registers
# and starts the systemd --user service via `sapaloq-core service install`.
#
# Usage:
#   ./install.sh [options]
#
# Options:
#   --no-service        Install binaries only; skip systemd --user setup.
#   --no-autostart      Skip the widget desktop autostart (no launch on login).
#   --bin-dir DIR       Install binaries into DIR (default: ~/.local/bin).
#   --uninstall         Remove the service, autostart entry and binaries.
#                       Config and data under ~/.config/sapaloq are KEPT.
#   -h, --help          Show this help.
#
set -euo pipefail

# --- config --------------------------------------------------------------
BIN_DIR="${HOME}/.local/bin"
DATA_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/sapaloq"
INSTALL_SERVICE=1
INSTALL_AUTOSTART=1
DO_UNINSTALL=0

CORE_BIN="sapaloq-core"
WIDGET_BIN="sapaloq-widget"

# Resolve repo root (the dir containing this script).
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"

# --- helpers -------------------------------------------------------------
log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[warn]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[error]\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
	sed -n '3,19p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
	exit 0
}

# --- arg parsing ---------------------------------------------------------
while [[ $# -gt 0 ]]; do
	case "$1" in
		--no-service)   INSTALL_SERVICE=0; shift ;;
		--no-autostart) INSTALL_AUTOSTART=0; shift ;;
		--bin-dir)      BIN_DIR="${2:?--bin-dir needs a path}"; shift 2 ;;
		--bin-dir=*)    BIN_DIR="${1#*=}"; shift ;;
		--uninstall)    DO_UNINSTALL=1; shift ;;
		-h|--help)      usage ;;
		*) die "unknown option: $1 (try --help)" ;;
	esac
done

# --- uninstall path ------------------------------------------------------
uninstall() {
	log "Uninstalling SapaLOQ"
	if command -v "${BIN_DIR}/${CORE_BIN}" >/dev/null 2>&1 || [[ -x "${BIN_DIR}/${CORE_BIN}" ]]; then
		"${BIN_DIR}/${CORE_BIN}" service uninstall || warn "service uninstall reported an issue (continuing)"
	else
		warn "${CORE_BIN} not found in ${BIN_DIR}; skipping service uninstall"
	fi

	for bin in "${CORE_BIN}" "${WIDGET_BIN}"; do
		if [[ -e "${BIN_DIR}/${bin}" ]]; then
			rm -f "${BIN_DIR}/${bin}"
			log "removed ${BIN_DIR}/${bin}"
		fi
	done

	echo
	log "Done. Config and data were kept at: ${DATA_DIR}"
	echo "    To delete them too (this erases facts, chat history and the vault):"
	echo "        rm -rf \"${DATA_DIR}\""
	exit 0
}

[[ "${DO_UNINSTALL}" -eq 1 ]] && uninstall

# --- prerequisites -------------------------------------------------------
command -v go >/dev/null 2>&1 || die "Go toolchain not found. Install Go and retry."

log "Installing SapaLOQ from ${SCRIPT_DIR}"
mkdir -p "${BIN_DIR}"

# --- build + install core ------------------------------------------------
log "Building ${CORE_BIN}"
( cd "${SCRIPT_DIR}" && go build -o "${BIN_DIR}/${CORE_BIN}" ./cmd/sapaloq-core )
log "installed ${BIN_DIR}/${CORE_BIN}"

# --- build + install widget (optional) -----------------------------------
if command -v wails >/dev/null 2>&1; then
	log "Building ${WIDGET_BIN} (wails)"
	GO_TAGS="${GO_TAGS:-webkit2_41}"
	if ( cd "${SCRIPT_DIR}/cmd/sapaloq-widget" && wails build -tags "${GO_TAGS}" ); then
		WIDGET_OUT="${SCRIPT_DIR}/cmd/sapaloq-widget/build/bin/${WIDGET_BIN}"
		if [[ -f "${WIDGET_OUT}" ]]; then
			install -m 0755 "${WIDGET_OUT}" "${BIN_DIR}/${WIDGET_BIN}"
			log "installed ${BIN_DIR}/${WIDGET_BIN}"
		else
			warn "wails build finished but ${WIDGET_OUT} was not found; skipping widget install"
		fi
	else
		warn "wails build failed; skipping widget (core is installed and usable)"
	fi
else
	warn "wails not found; skipping the GUI widget. Install wails + libwebkit2gtk to build it:"
	warn "  go install github.com/wailsapp/wails/v2/cmd/wails@latest"
fi

# --- seed config + runtime dirs -----------------------------------------
mkdir -p "${DATA_DIR}" "${DATA_DIR}/memory" "${DATA_DIR}/state" "${DATA_DIR}/run" "${DATA_DIR}/vault"

CONFIG_FILE="${DATA_DIR}/config.json"
EXAMPLE_CONFIG="${SCRIPT_DIR}/config/config.example.json"
if [[ -f "${CONFIG_FILE}" ]]; then
	log "config exists, leaving it untouched: ${CONFIG_FILE}"
elif [[ -f "${EXAMPLE_CONFIG}" ]]; then
	cp "${EXAMPLE_CONFIG}" "${CONFIG_FILE}"
	log "seeded default config: ${CONFIG_FILE}"
else
	warn "no example config found at ${EXAMPLE_CONFIG}; start by running 'sapaloq-core doctor'"
fi

# --- PATH hint -----------------------------------------------------------
case ":${PATH}:" in
	*":${BIN_DIR}:"*) : ;;
	*) warn "${BIN_DIR} is not on your PATH. Add this to your shell rc:"
	   warn "  export PATH=\"${BIN_DIR}:\$PATH\"" ;;
esac

# --- service + widget autostart -----------------------------------------
# `service install` also writes the widget's XDG autostart entry so the GUI
# launches on login; --no-autostart suppresses just that part.
if [[ "${INSTALL_SERVICE}" -eq 1 ]]; then
	if command -v systemctl >/dev/null 2>&1; then
		log "Registering systemd --user service"
		if [[ "${INSTALL_AUTOSTART}" -eq 0 ]]; then
			SAPALOQ_SKIP_WIDGET_AUTOSTART=1 "${BIN_DIR}/${CORE_BIN}" service install
		else
			"${BIN_DIR}/${CORE_BIN}" service install
		fi
	else
		warn "systemctl not found; skipping service install."
		warn "Run the core manually with: ${CORE_BIN} run"
	fi
else
	log "Skipping service install (--no-service)."
	echo "    Register it later with: ${CORE_BIN} service install"
fi

echo
log "SapaLOQ installed."
echo "    core:   ${BIN_DIR}/${CORE_BIN}"
if [[ -x "${BIN_DIR}/${WIDGET_BIN}" ]]; then
	echo "    widget: ${BIN_DIR}/${WIDGET_BIN}"
	if [[ "${INSTALL_SERVICE}" -eq 1 && "${INSTALL_AUTOSTART}" -eq 1 ]]; then
		echo "    widget autostart: enabled (appears on next login; start now with '${WIDGET_BIN} &')"
	fi
fi
echo "    config: ${CONFIG_FILE}"
