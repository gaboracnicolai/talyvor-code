#!/usr/bin/env bash
# Talyvor Code CLI installer.
#
# Downloads a pre-built, CHECKSUM-VERIFIED binary for the host OS/arch from a GitHub
# release. Falls back to `go install` only when run from a source checkout and no release
# matches.
#
# This script is meant to be piped:  curl -sSL <raw url> | bash
#
# It previously could not work that way. It ran `( cd agent && go install ./cmd/agent )`,
# which needs a git clone as the working directory — precisely what piping is not — so the
# documented command failed with "cd: agent: No such file or directory" on every machine.
# And in the one case it could run (inside a clone, with Go present) it installed a binary
# named `agent` while printing "Installed … /talyvor-code" and telling the user to run
# `talyvor-code`.
#
# CHECKSUM VERIFICATION IS NOT OPTIONAL. A piped installer that fetches a binary is a real
# remote-code-execution surface: whoever controls the artifact or the transport controls
# the machine. The download is refused unless its SHA-256 matches the release's
# checksums.txt, and there is deliberately no flag to skip that.

set -euo pipefail

REPO="${TALYVOR_REPO:-gaboracnicolai/talyvor-code}"
BIN_NAME="talyvor-code"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${TALYVOR_VERSION:-latest}"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "${ARCH}" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "❌ Unsupported architecture: ${ARCH}" >&2; exit 1 ;;
esac
case "${OS}" in
  linux|darwin) ;;
  *) echo "❌ Unsupported OS: ${OS} — use the Windows archive from the releases page." >&2; exit 1 ;;
esac

ASSET="${BIN_NAME}-${OS}-${ARCH}"
echo "Installing Talyvor Code CLI…"
echo "Detected: ${OS}/${ARCH}"

need() { command -v "$1" >/dev/null 2>&1; }

# sha256 of $1, portable across the two tools that ship on Linux and macOS.
sha256_of() {
  if need sha256sum; then sha256sum "$1" | cut -d' ' -f1
  elif need shasum;   then shasum -a 256 "$1" | cut -d' ' -f1
  else echo "❌ Need sha256sum or shasum to verify the download." >&2; exit 1
  fi
}

install_from_release() {
  need curl || return 1
  local base
  if [ "${VERSION}" = "latest" ]; then
    base="https://github.com/${REPO}/releases/latest/download"
  else
    base="https://github.com/${REPO}/releases/download/${VERSION}"
  fi

  local tmp; tmp=$(mktemp -d)

  # A 404 here means "no release for this platform yet" — return non-zero so the caller can
  # try a source build. A checksum problem, by contrast, is never a fallback: it exits.
  if ! curl -fsSL --proto '=https' --tlsv1.2 -o "${tmp}/${ASSET}" "${base}/${ASSET}" 2>/dev/null; then
    rm -rf "${tmp}"; return 1
  fi
  if ! curl -fsSL --proto '=https' --tlsv1.2 -o "${tmp}/checksums.txt" "${base}/checksums.txt" 2>/dev/null; then
    echo "❌ Downloaded ${ASSET} but the release published no checksums.txt." >&2
    echo "   Refusing to install an unverified binary." >&2
    rm -rf "${tmp}"; exit 1
  fi

  local want got
  want=$(grep -E "[[:space:]]\*?${ASSET}\$" "${tmp}/checksums.txt" | head -1 | awk '{print $1}' || true)
  if [ -z "${want}" ]; then
    echo "❌ checksums.txt has no entry for ${ASSET}. Refusing to install." >&2
    rm -rf "${tmp}"; exit 1
  fi
  got=$(sha256_of "${tmp}/${ASSET}")
  if [ "${want}" != "${got}" ]; then
    # HARD STOP. A mismatch means the bytes are not what the release published — a tampered
    # artifact or a corrupted transfer. Running them is the entire risk this check exists for.
    echo "❌ CHECKSUM MISMATCH for ${ASSET}" >&2
    echo "   expected ${want}" >&2
    echo "   actual   ${got}" >&2
    echo "   Refusing to install. There is no flag to bypass this." >&2
    rm -rf "${tmp}"; exit 1
  fi
  echo "✅ Checksum verified (sha256 ${got})"

  chmod +x "${tmp}/${ASSET}"
  if [ -w "${INSTALL_DIR}" ]; then
    mv "${tmp}/${ASSET}" "${INSTALL_DIR}/${BIN_NAME}"
  else
    echo "   ${INSTALL_DIR} is not writable — using sudo"
    sudo mv "${tmp}/${ASSET}" "${INSTALL_DIR}/${BIN_NAME}"
  fi
  rm -rf "${tmp}"
  echo "✅ Installed ${BIN_NAME} to ${INSTALL_DIR}/${BIN_NAME}"
  return 0
}

install_from_source() {
  need go || return 1
  # Only possible from a source checkout. DETECT that rather than assuming the caller's
  # working directory — assuming it is the bug that made the piped command fail.
  [ -f "agent/go.mod" ] || return 1
  echo "Building from source…"
  ( cd agent && go install -trimpath -ldflags="-w -s" ./cmd/agent )
  # `go install ./cmd/agent` names the binary after its DIRECTORY — `agent`, not
  # `talyvor-code`. Rename it so the name printed is the name the user can actually run.
  local gobin; gobin=$(go env GOBIN); [ -n "${gobin}" ] || gobin="$(go env GOPATH)/bin"
  [ -f "${gobin}/agent" ] && mv "${gobin}/agent" "${gobin}/${BIN_NAME}"
  echo "✅ Installed ${BIN_NAME} to ${gobin}/${BIN_NAME}"
  echo "   (ensure ${gobin} is on your PATH)"
  return 0
}

if install_from_release; then
  :
elif install_from_source; then
  :
else
  echo "❌ Could not install." >&2
  echo "   No published release matched ${OS}/${ARCH}, and no source build was possible" >&2
  echo "   (a source build needs Go 1.25+ and this script run from the repo root)." >&2
  echo "   Releases: https://github.com/${REPO}/releases" >&2
  echo "   Go:       https://go.dev/dl/" >&2
  exit 1
fi

cat <<'EOF'

Configure by setting environment variables:
  export TALYVOR_LENS_URL=https://lens.talyvor.com   # remote URLs must be https; http is allowed only for localhost
  export TALYVOR_LENS_API_KEY=tlv_...
  export TALYVOR_WORKSPACE_ID=ws-1
  export TALYVOR_ISSUE=ENG-42

Then run: talyvor-code check
EOF
