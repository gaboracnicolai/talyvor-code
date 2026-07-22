#!/usr/bin/env bash
# Talyvor Code CLI installer.
#
# Detects the host OS/arch and either downloads a pre-built
# binary (when releases are available) or falls back to a
# `go install` from source.

set -euo pipefail

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "${ARCH}" in
  x86_64)   ARCH="amd64" ;;
  aarch64)  ARCH="arm64" ;;
  arm64)    ARCH="arm64" ;;
esac

BINARY="talyvor-code-${OS}-${ARCH}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

echo "Installing Talyvor Code CLI..."
echo "Detecting: ${OS}/${ARCH}"

# Build from source for now. Once tagged release artefacts exist,
# this script will fall back to `curl -L` from the release URL.
if command -v go >/dev/null 2>&1; then
  echo "Building from source..."
  ( cd agent && go install -trimpath -ldflags="-w -s" ./cmd/agent )
  echo "✅ Installed to $(go env GOPATH)/bin/talyvor-code"
else
  echo "❌ Go not found. Please install Go 1.25+"
  echo "   https://go.dev/dl/"
  exit 1
fi

cat <<'EOF'

Configure by setting environment variables:
  export TALYVOR_LENS_URL=https://lens.talyvor.com   # remote URLs must be https; http is allowed only for localhost
  export TALYVOR_LENS_API_KEY=tlv_...
  export TALYVOR_WORKSPACE_ID=ws-1
  export TALYVOR_ISSUE=ENG-42

Or run: talyvor-code check
EOF
