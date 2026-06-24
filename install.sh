#!/usr/bin/env bash
#
# SapaLOQ installer (prebuilt release).
#
# Downloads a prebuilt release artifact from GitHub, installs the binaries into
# a user-local bin dir (default ~/.local/bin), seeds a default config under
# ~/.config/sapaloq (never overwriting an existing one), and - unless
# --no-service is given - registers and starts the systemd --user service via
# `sapaloq-core service install`.
#
# It does NOT clone the repo or build anything: only curl, tar and (for the
# service) systemd --user are required. To build from source instead, use
# `make install` from a checkout.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/jahrulnr/sapaloq/main/install.sh | bash
#   ./install.sh [options]
#
# Options:
#   --version TAG       Install a specific release (e.g. v0.1.0). Default: latest.
#   --bin-dir DIR       Install binaries into DIR (default: ~/.local/bin).
#   --no-service        Install binaries only; skip systemd --user setup.
#   --no-autostart      Skip the widget desktop autostart (no launch on login).
#   --no-verify         Skip the sha256 checksum verification (not recommended).
#   --uninstall         Remove the service, autostart entry and binaries.
#                       Config and data under ~/.config/sapaloq are KEPT.
#   -h, --help          Show this help.
#
# Environment:
#   SAPALOQ_REPO        Override GitHub repo slug (default: jahrulnr/sapaloq).
#   SAPALOQ_VERSION     Same as --version.
#
set -euo pipefail

# --- config --------------------------------------------------------------
REPO="${SAPALOQ_REPO:-jahrulnr/sapaloq}"
BIN_DIR="${HOME}/.local/bin"
DATA_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/sapaloq"
DATA_HOME="${XDG_DATA_HOME:-${HOME}/.local/share}"
VERSION="${SAPALOQ_VERSION:-}"
INSTALL_SERVICE=1
INSTALL_AUTOSTART=1
DO_VERIFY=1
DO_UNINSTALL=0

CORE_BIN="sapaloq-core"
WIDGET_BIN="sapaloq-widget"

# --- helpers -------------------------------------------------------------
log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[warn]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[error]\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
	sed -n '3,33p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
	exit 0
}

need() { command -v "$1" >/dev/null 2>&1 || die "required tool not found: $1"; }

# --- arg parsing ---------------------------------------------------------
while [[ $# -gt 0 ]]; do
	case "$1" in
		--version)      VERSION="${2:?--version needs a tag}"; shift 2 ;;
		--version=*)    VERSION="${1#*=}"; shift ;;
		--bin-dir)      BIN_DIR="${2:?--bin-dir needs a path}"; shift 2 ;;
		--bin-dir=*)    BIN_DIR="${1#*=}"; shift ;;
		--no-service)   INSTALL_SERVICE=0; shift ;;
		--no-autostart) INSTALL_AUTOSTART=0; shift ;;
		--no-verify)    DO_VERIFY=0; shift ;;
		--uninstall)    DO_UNINSTALL=1; shift ;;
		-h|--help)      usage ;;
		*) die "unknown option: $1 (try --help)" ;;
	esac
done

# --- uninstall path ------------------------------------------------------
uninstall() {
	log "Uninstalling SapaLOQ"
	if [[ -x "${BIN_DIR}/${CORE_BIN}" ]]; then
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
	rm -f "${DATA_HOME}/icons/hicolor/512x512/apps/sapaloq.png"
	rm -f "${DATA_HOME}/applications/sapaloq.desktop"
	if command -v update-desktop-database >/dev/null 2>&1; then
		update-desktop-database "${DATA_HOME}/applications" >/dev/null 2>&1 || true
	fi

	echo
	log "Done. Config and data were kept at: ${DATA_DIR}"
	echo "    To delete them too (this erases facts, chat history and the vault):"
	echo "        rm -rf \"${DATA_DIR}\""
	exit 0
}

[[ "${DO_UNINSTALL}" -eq 1 ]] && uninstall

# --- prerequisites -------------------------------------------------------
need curl
need tar

# --- platform detection --------------------------------------------------
os="$(uname -s)"
arch="$(uname -m)"
case "${os}" in
	Linux) os="linux" ;;
	*) die "unsupported OS '${os}'. Prebuilt artifacts are Linux-only; build from source with 'make install'." ;;
esac
case "${arch}" in
	x86_64|amd64) arch="amd64" ;;
	*) die "unsupported architecture '${arch}'. No prebuilt artifact yet; build from source with 'make install'." ;;
esac

# --- resolve version (default: latest release) ---------------------------
if [[ -z "${VERSION}" ]]; then
	log "Resolving latest release of ${REPO}"
	VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
		| grep -m1 '"tag_name"' | sed -E 's/.*"tag_name" *: *"([^"]+)".*/\1/')"
	[[ -n "${VERSION}" ]] || die "could not determine the latest release tag (set --version explicitly)."
fi
log "Installing SapaLOQ ${VERSION} (${os}/${arch})"

# --- download + verify ---------------------------------------------------
PKG="sapaloq_${VERSION}_${os}_${arch}"
TARBALL="${PKG}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

log "Downloading ${TARBALL}"
curl -fsSL -o "${TMP}/${TARBALL}" "${BASE_URL}/${TARBALL}" \
	|| die "download failed: ${BASE_URL}/${TARBALL}"

if [[ "${DO_VERIFY}" -eq 1 ]]; then
	if curl -fsSL -o "${TMP}/checksums.txt" "${BASE_URL}/checksums.txt"; then
		log "Verifying checksum"
		expected="$(grep " ${TARBALL}\$" "${TMP}/checksums.txt" | awk '{print $1}')"
		[[ -n "${expected}" ]] || die "checksum for ${TARBALL} not found in checksums.txt"
		if command -v sha256sum >/dev/null 2>&1; then
			actual="$(sha256sum "${TMP}/${TARBALL}" | awk '{print $1}')"
		elif command -v shasum >/dev/null 2>&1; then
			actual="$(shasum -a 256 "${TMP}/${TARBALL}" | awk '{print $1}')"
		else
			die "no sha256 tool (sha256sum/shasum) found; re-run with --no-verify to skip."
		fi
		[[ "${expected}" == "${actual}" ]] || die "checksum mismatch (expected ${expected}, got ${actual})"
		log "checksum ok"
	else
		warn "checksums.txt not available; skipping verification"
	fi
else
	warn "checksum verification skipped (--no-verify)"
fi

log "Extracting"
tar -C "${TMP}" -xzf "${TMP}/${TARBALL}"
SRC="${TMP}/${PKG}"
[[ -d "${SRC}" ]] || die "unexpected archive layout: ${SRC} missing"

# --- install binaries ----------------------------------------------------
mkdir -p "${BIN_DIR}"
for bin in "${CORE_BIN}" "${WIDGET_BIN}"; do
	if [[ -f "${SRC}/${bin}" ]]; then
		install -m 0755 "${SRC}/${bin}" "${BIN_DIR}/${bin}"
		log "installed ${BIN_DIR}/${bin}"
	else
		warn "${bin} not in archive; skipping"
	fi
done

if [[ -f "${SRC}/sapaloq.png" ]]; then
	install -Dm 0644 "${SRC}/sapaloq.png" "${DATA_HOME}/icons/hicolor/512x512/apps/sapaloq.png"
	log "installed ${DATA_HOME}/icons/hicolor/512x512/apps/sapaloq.png"
	if command -v gtk-update-icon-cache >/dev/null 2>&1; then
		gtk-update-icon-cache -f -t "${DATA_HOME}/icons/hicolor" >/dev/null 2>&1 || true
	fi
else
	warn "sapaloq.png not in archive; desktop launcher may use a fallback icon"
fi

# Desktop entry: lets GNOME map the widget's WM_CLASS (=sapaloq) to the SapaLOQ
# icon in the taskbar/dock. Rewrite Exec= to the installed widget path.
if [[ -f "${SRC}/sapaloq.desktop" ]]; then
	mkdir -p "${DATA_HOME}/applications"
	sed "s|^Exec=.*|Exec=${BIN_DIR}/${WIDGET_BIN}|" "${SRC}/sapaloq.desktop" \
		> "${DATA_HOME}/applications/sapaloq.desktop"
	chmod 0644 "${DATA_HOME}/applications/sapaloq.desktop"
	log "installed ${DATA_HOME}/applications/sapaloq.desktop"
	if command -v update-desktop-database >/dev/null 2>&1; then
		update-desktop-database "${DATA_HOME}/applications" >/dev/null 2>&1 || true
	fi
else
	warn "sapaloq.desktop not in archive; no app launcher / taskbar icon mapping"
fi

# --- seed config + runtime dirs -----------------------------------------
mkdir -p "${DATA_DIR}" "${DATA_DIR}/memory" "${DATA_DIR}/state" "${DATA_DIR}/run" "${DATA_DIR}/vault"

CONFIG_FILE="${DATA_DIR}/config.json"
EXAMPLE_CONFIG="${SRC}/config.example.json"
if [[ -f "${CONFIG_FILE}" ]]; then
	log "config exists, leaving it untouched: ${CONFIG_FILE}"
elif [[ -f "${EXAMPLE_CONFIG}" ]]; then
	cp "${EXAMPLE_CONFIG}" "${CONFIG_FILE}"
	log "seeded default config: ${CONFIG_FILE}"
else
	warn "no example config in archive; start by running 'sapaloq-core doctor'"
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
log "SapaLOQ ${VERSION} installed."
echo "    core:   ${BIN_DIR}/${CORE_BIN}"
if [[ -x "${BIN_DIR}/${WIDGET_BIN}" ]]; then
	echo "    widget: ${BIN_DIR}/${WIDGET_BIN}"
	if [[ "${INSTALL_SERVICE}" -eq 1 && "${INSTALL_AUTOSTART}" -eq 1 ]]; then
		echo "    widget autostart: enabled (appears on next login; start now with '${WIDGET_BIN} &')"
	fi
fi
echo "    config: ${CONFIG_FILE}"
