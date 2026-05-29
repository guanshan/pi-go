#!/usr/bin/env bash
# Download and install the latest pi-go release for the host OS/arch.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/guanshan/pi-go/main/scripts/install.sh | bash
#   ./scripts/install.sh [--version vX.Y.Z] [--bin-dir /usr/local/bin]
#
# Env overrides:
#   PI_VERSION   release tag to install (default: latest)
#   PI_BIN_DIR   install destination (default: /usr/local/bin, or ~/.local/bin if not writable)
#   PI_REPO      github repo (default: guanshan/pi-go)

set -euo pipefail

REPO="${PI_REPO:-guanshan/pi-go}"
VERSION="${PI_VERSION:-}"
BIN_DIR="${PI_BIN_DIR:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --bin-dir) BIN_DIR="$2"; shift 2 ;;
    --repo)    REPO="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,12p' "$0"; exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

err() { echo "error: $*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

have curl  || err "curl is required"
have tar   || err "tar is required"
have uname || err "uname is required"

# --- detect platform ---------------------------------------------------------
os_raw="$(uname -s)"
case "$os_raw" in
  Linux)  os="Linux" ;;
  Darwin) os="macOS" ;;
  *) err "unsupported OS: $os_raw (use the Windows zip from the Releases page)" ;;
esac

arch_raw="$(uname -m)"
case "$arch_raw" in
  x86_64|amd64) arch="x86_64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) err "unsupported arch: $arch_raw" ;;
esac

# --- resolve version ---------------------------------------------------------
if [[ -z "$VERSION" ]]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep -oE '"tag_name":[[:space:]]*"[^"]+"' \
    | head -n1 \
    | sed -E 's/.*"([^"]+)"$/\1/')"
  [[ -n "$VERSION" ]] || err "could not resolve latest release for ${REPO}"
fi
ver_no_v="${VERSION#v}"

# --- resolve install dir -----------------------------------------------------
if [[ -z "$BIN_DIR" ]]; then
  if [[ -w "/usr/local/bin" ]] || [[ "$(id -u)" -eq 0 ]]; then
    BIN_DIR="/usr/local/bin"
  else
    BIN_DIR="${HOME}/.local/bin"
  fi
fi
mkdir -p "$BIN_DIR"
[[ -w "$BIN_DIR" ]] || err "cannot write to ${BIN_DIR}; rerun with sudo or pass --bin-dir"

# --- download & install ------------------------------------------------------
asset="pi_${ver_no_v}_${os}_${arch}.tar.gz"
url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "==> downloading ${asset}"
curl -fsSL "$url" -o "${tmp}/${asset}" \
  || err "download failed: $url"

echo "==> verifying checksum"
checksums_url="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"
if curl -fsSL "$checksums_url" -o "${tmp}/checksums.txt"; then
  expected="$(grep " ${asset}\$" "${tmp}/checksums.txt" | awk '{print $1}')"
  if [[ -n "$expected" ]]; then
    actual="$(shasum -a 256 "${tmp}/${asset}" 2>/dev/null | awk '{print $1}' \
              || sha256sum "${tmp}/${asset}" | awk '{print $1}')"
    [[ "$expected" == "$actual" ]] || err "checksum mismatch: expected $expected, got $actual"
    echo "    ok ($expected)"
  else
    echo "    no checksum entry for ${asset}; skipping verification"
  fi
else
  echo "    checksums.txt not available; skipping verification"
fi

echo "==> extracting"
tar -xzf "${tmp}/${asset}" -C "$tmp"

echo "==> installing to ${BIN_DIR}/pi"
install -m 0755 "${tmp}/pi" "${BIN_DIR}/pi"

if ! echo ":$PATH:" | grep -q ":${BIN_DIR}:"; then
  echo "note: ${BIN_DIR} is not on your PATH. Add this to your shell rc:"
  echo "    export PATH=\"${BIN_DIR}:\$PATH\""
fi

echo "==> done. Installed $(${BIN_DIR}/pi --version 2>/dev/null || echo "${VERSION}")"
