#!/bin/sh
# blueprint one-line installer (POSIX sh — no bashisms; runs under dash/ash/sh).
#
# Usage (documented use of curl|sh; read the script first if you prefer):
#   curl -fsSL https://raw.githubusercontent.com/akhilmukkamala/blueprint/main/dist/install.sh | sh
# or download it, inspect it, then run:  sh install.sh
#
# Environment overrides:
#   GITHUB_REPO            owner/repo slug of the release repo
#   BLUEPRINT_VERSION      version to install (e.g. 1.2.3); default: latest release
#   BLUEPRINT_INSTALL_DIR  target directory; default: /usr/local/bin if writable,
#                          otherwise ~/.local/bin (created if missing)
#
# The script downloads the release tarball for this OS/arch, verifies its
# sha256 against the release checksums file, and installs the binary.
# It is idempotent: re-running with the same version already installed is a no-op.
set -eu

# Repo slug is finalized at first publish; until then override with GITHUB_REPO.
GITHUB_REPO="${GITHUB_REPO:-akhilmukkamala/blueprint}"
VERSION="${BLUEPRINT_VERSION:-latest}"

say() { printf '%s\n' "$*"; }
die() { printf 'install.sh: error: %s\n' "$*" >&2; exit 1; }

# --- detect os/arch -----------------------------------------------------------
case "$(uname -s)" in
  Darwin) OS=darwin ;;
  Linux)  OS=linux ;;
  *) die "unsupported OS '$(uname -s)'. On Windows use dist/install.ps1 (or scoop/winget)." ;;
esac
case "$(uname -m)" in
  x86_64|amd64)  ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) die "unsupported architecture '$(uname -m)'. Build from source: go build ./cmd/blueprint (requires cgo)." ;;
esac

# --- downloader ---------------------------------------------------------------
if command -v curl >/dev/null 2>&1; then
  fetch()     { curl -fsSL -o "$2" "$1"; }
  final_url() { curl -fsSL -o /dev/null -w '%{url_effective}' "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch()     { wget -q -O "$2" "$1"; }
  final_url() { die "resolving the latest release needs curl; install curl or set BLUEPRINT_VERSION=x.y.z and re-run"; }
else
  die "neither curl nor wget found; install one and re-run"
fi

# --- resolve version ----------------------------------------------------------
if [ "$VERSION" = "latest" ]; then
  # GitHub redirects /releases/latest to /releases/tag/vX.Y.Z
  effective=$(final_url "https://github.com/${GITHUB_REPO}/releases/latest")
  VERSION=${effective##*/tag/v}
  case "$VERSION" in
    ''|*'/'*|http*) die "could not resolve the latest release of ${GITHUB_REPO}; set BLUEPRINT_VERSION=x.y.z and re-run" ;;
  esac
fi
say "installing blueprint v${VERSION} (${OS}/${ARCH}) from ${GITHUB_REPO}"

# --- choose install dir -------------------------------------------------------
if [ -n "${BLUEPRINT_INSTALL_DIR:-}" ]; then
  DEST_DIR="$BLUEPRINT_INSTALL_DIR"
  mkdir -p "$DEST_DIR" || die "cannot create $DEST_DIR"
elif [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
  DEST_DIR=/usr/local/bin
else
  DEST_DIR="$HOME/.local/bin"
  mkdir -p "$DEST_DIR" || die "cannot create $DEST_DIR"
fi
DEST="$DEST_DIR/blueprint"

# --- idempotency: skip when the requested version is already installed --------
if [ -x "$DEST" ]; then
  installed=$("$DEST" --version 2>/dev/null || true)
  case "$installed" in
    *"$VERSION"*) say "blueprint v${VERSION} already installed at $DEST — nothing to do"; exit 0 ;;
  esac
fi

# --- download + verify --------------------------------------------------------
ASSET="blueprint-${VERSION}-${OS}-${ARCH}.tar.gz"
BASE="https://github.com/${GITHUB_REPO}/releases/download/v${VERSION}"
TMP=$(mktemp -d "${TMPDIR:-/tmp}/blueprint-install.XXXXXX") || die "mktemp failed"
trap 'rm -rf "$TMP"' EXIT INT TERM

say "downloading ${ASSET} ..."
fetch "${BASE}/${ASSET}" "$TMP/$ASSET" || die "download failed: ${BASE}/${ASSET}"
fetch "${BASE}/checksums.txt" "$TMP/checksums.txt" || die "download failed: ${BASE}/checksums.txt"

expected=$(grep " ${ASSET}\$" "$TMP/checksums.txt" | awk '{print $1}')
[ -n "$expected" ] || die "checksums.txt has no entry for ${ASSET}; refusing to install"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$TMP/$ASSET" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$TMP/$ASSET" | awk '{print $1}')
else
  die "neither sha256sum nor shasum found; cannot verify download"
fi
[ "$expected" = "$actual" ] || die "sha256 mismatch for ${ASSET} (expected ${expected}, got ${actual}); download corrupted or tampered — not installing"
say "sha256 verified"

# --- install ------------------------------------------------------------------
tar -xzf "$TMP/$ASSET" -C "$TMP" || die "extraction failed"
[ -f "$TMP/blueprint" ] || die "tarball did not contain a 'blueprint' binary"
chmod 0755 "$TMP/blueprint"
mv -f "$TMP/blueprint" "$DEST" || die "cannot write $DEST"
say "installed $DEST"

# --- PATH advice --------------------------------------------------------------
case ":$PATH:" in
  *":$DEST_DIR:"*) ;;
  *)
    say ""
    say "NOTE: $DEST_DIR is not on your PATH. Add it, e.g.:"
    say "  export PATH=\"$DEST_DIR:\$PATH\"   # add to your shell profile"
    ;;
esac

say "done — run 'blueprint init' in a repo to get started (see INSTALL.md)"
