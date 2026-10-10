#!/usr/bin/env bash
# Install pdfm-mcp (the Parallels MCP server) from GitHub Releases on macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/Parallels/pdfm-mcp/main/scripts/install.sh | bash
#
# The release asset is a universal (Intel + Apple silicon) binary, so no
# architecture detection is needed. The download is verified against the
# release's checksums.txt. sudo is used only if the destination is not
# writable by the current user.
set -euo pipefail

MODE="INSTALL"
PRE_RELEASE="false"
VERSION=""
DESTINATION="/usr/local/bin"
BINARY="pdfm-mcp"

usage() {
  cat <<'EOF'
usage: install.sh [options]

options:
  -i, --install            install (default)
  -U, --update             update (same as install: download and replace)
  -u, --uninstall          remove the installed binary
  -v, --version <ver>      version to install (e.g. 1.2.3 or v1.2.3-beta.4);
                           default: latest production release
  -p, --path <dir>         destination directory (default /usr/local/bin)
  --pre-release            allow prereleases (canary/beta) as "latest"
  -h, --help               show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case $1 in
  -i | --install)
    MODE="INSTALL"
    ;;
  -U | --update)
    MODE="UPDATE"
    ;;
  -u | --uninstall)
    MODE="UNINSTALL"
    ;;
  -v | --version)
    if [[ -z "${2:-}" ]]; then
      echo "Error: $1 requires a value" >&2
      exit 1
    fi
    VERSION="$2"
    shift
    ;;
  -p | --path)
    if [[ -z "${2:-}" ]]; then
      echo "Error: $1 requires a value" >&2
      exit 1
    fi
    DESTINATION="$2"
    shift
    ;;
  --pre-release)
    PRE_RELEASE="true"
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    echo "Invalid option $1" >&2
    usage >&2
    exit 1
    ;;
  esac
  shift
done

# Overridable for testing; defaults to the production repository.
REPO="${PDFM_MCP_REPO:-Parallels/pdfm-mcp}"
GH_BASE="${PDFM_MCP_GH:-https://github.com}"
API_BASE="${PDFM_MCP_API:-https://api.github.com}"
GH_RELEASES="${GH_BASE}/${REPO}/releases"
GH_API="${API_BASE}/repos/${REPO}"
ASSET=""

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "Error: pdfm-mcp is a macOS-only binary (Apple Vision OCR is compiled in)." >&2
  exit 1
fi

WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

# SUDO is set to "sudo" when the destination needs root to write to.
SUDO=""
if [[ $EUID -ne 0 ]] && [[ ! -w "$DESTINATION" ]]; then
  SUDO="sudo"
fi

http_get() {
  # http_get <url> <output-file> -> echoes the HTTP status code (000 on
  # transport failure).
  curl -sSL -w "%{http_code}" "$1" -o "$2" || echo "000"
}

get_latest_release() {
  if [[ "$PRE_RELEASE" = "true" ]]; then
    URL="${GH_API}/releases"
  else
    URL="${GH_API}/releases/latest"
  fi

  HTTP_STATUS=$(http_get "$URL" "$WORK_DIR/release.json")
  if [[ "$HTTP_STATUS" = "403" || "$HTTP_STATUS" = "429" ]]; then
    echo "Error: GitHub API rate limit exceeded. Please try again later." >&2
    exit 1
  fi
  if [[ "$HTTP_STATUS" != "200" ]]; then
    echo "Error: failed to fetch release information. HTTP status: $HTTP_STATUS" >&2
    exit 1
  fi

  TAGS=$(grep -o '"tag_name": *"[^"]*"' "$WORK_DIR/release.json" | sed 's/.*"tag_name": *"//; s/"$//')
  if [[ -z "$TAGS" ]]; then
    echo "Error: no releases found for ${REPO}." >&2
    exit 1
  fi
  if [[ "$PRE_RELEASE" = "true" ]]; then
    # The API lists newest first.
    echo "$TAGS" | head -n 1
  else
    echo "$TAGS"
  fi
}

resolve_version() {
  if [[ -z "$VERSION" ]]; then
    echo "Getting latest version from GitHub"
    VERSION=$(get_latest_release)
  fi
  if [[ "$VERSION" != v* ]]; then
    VERSION="v${VERSION}"
  fi

  IS_FOUND=$(curl -s -o /dev/null -w "%{http_code}" -L "${GH_RELEASES}/${VERSION}")
  if [[ "$IS_FOUND" != "200" ]]; then
    echo "Error: version ${VERSION} not found in GitHub releases (HTTP status: $IS_FOUND)." >&2
    echo "List releases: https://github.com/${REPO}/releases" >&2
    exit 1
  fi
  ASSET="pdfm-mcp_${VERSION}_darwin_universal.tar.gz"
}

download_release() {
  DOWNLOAD_URL="${GH_RELEASES}/download/${VERSION}/${ASSET}"
  echo "Downloading ${ASSET} from GitHub Releases"
  HTTP_STATUS=$(http_get "$DOWNLOAD_URL" "$WORK_DIR/${ASSET}")
  if [[ "$HTTP_STATUS" = "403" || "$HTTP_STATUS" = "429" ]]; then
    echo "Error: GitHub API rate limit exceeded during download. Please try again later." >&2
    exit 1
  fi
  if [[ "$HTTP_STATUS" != "200" ]]; then
    echo "Error: failed to download ${ASSET} (HTTP status: $HTTP_STATUS)." >&2
    exit 1
  fi
  if [[ ! -s "$WORK_DIR/${ASSET}" ]]; then
    echo "Error: downloaded file is empty or does not exist." >&2
    exit 1
  fi

  # Verify against the published checksums.txt.
  CHECKSUM_STATUS=$(http_get "${GH_RELEASES}/download/${VERSION}/checksums.txt" "$WORK_DIR/checksums.txt")
  if [[ "$CHECKSUM_STATUS" != "200" || ! -s "$WORK_DIR/checksums.txt" ]]; then
    echo "Error: could not download checksums.txt for ${VERSION}; aborting." >&2
    exit 1
  fi
  if ! grep -E "^[0-9a-f]{64}[[:space:]]+${ASSET}[[:space:]]*$" "$WORK_DIR/checksums.txt" >"$WORK_DIR/check.txt"; then
    echo "Error: ${ASSET} is missing from checksums.txt." >&2
    exit 1
  fi
  if ! (cd "$WORK_DIR" && shasum -a 256 -c check.txt); then
    echo "Error: checksum verification failed for ${ASSET}." >&2
    exit 1
  fi
  echo "Checksum verified"
}

install() {
  resolve_version
  echo "Installing pdfm-mcp ${VERSION} for macOS (universal: Apple silicon + Intel)"
  download_release

  echo "Extracting ${BINARY}"
  if ! tar -xzf "$WORK_DIR/${ASSET}" -C "$WORK_DIR"; then
    echo "Error: failed to extract ${ASSET}." >&2
    exit 1
  fi
  if [[ ! -f "$WORK_DIR/${BINARY}" ]]; then
    echo "Error: ${BINARY} not found in the release archive." >&2
    exit 1
  fi

  if [[ ! -d "$DESTINATION" ]]; then
    echo "Creating destination directory: $DESTINATION"
    $SUDO mkdir -p "$DESTINATION"
  fi
  if [[ -f "$DESTINATION/${BINARY}" ]]; then
    echo "Removing existing ${BINARY}"
    $SUDO rm "$DESTINATION/${BINARY}"
  fi
  echo "Moving ${BINARY} to $DESTINATION"
  $SUDO mv "$WORK_DIR/${BINARY}" "$DESTINATION/${BINARY}"
  $SUDO chmod +x "$DESTINATION/${BINARY}"

  # curl-tagged downloads carry the quarantine xattr; drop it so Gatekeeper
  # does not prompt (the release binary is signed and notarized).
  xattr -d com.apple.quarantine "$DESTINATION/${BINARY}" 2>/dev/null || true

  INSTALLED_VERSION=$("$DESTINATION/${BINARY}" --version)
  echo
  echo "pdfm-mcp ${VERSION} has been installed to $DESTINATION/${BINARY} (${INSTALLED_VERSION})"
  echo
  echo "GUI clients ignore your shell PATH, so point them at the absolute path:"
  echo "  claude mcp add pdfm-mcp -- \"$DESTINATION/${BINARY}\""
  echo "  Claude Desktop: \"command\": \"$DESTINATION/${BINARY}\""
}

uninstall() {
  if [[ -f "$DESTINATION/${BINARY}" ]]; then
    echo "Removing ${BINARY} from $DESTINATION"
    $SUDO rm "$DESTINATION/${BINARY}"
    echo "pdfm-mcp has been uninstalled."
    echo
    echo "Client configuration is left in place; remove it separately, e.g.:"
    echo "  claude mcp remove pdfm-mcp"
    echo "  or delete the \"pdfm-mcp\" entry from ~/Library/Application Support/Claude/claude_desktop_config.json"
  else
    echo "${BINARY} is not installed in $DESTINATION"
  fi
}

case $MODE in
UNINSTALL)
  uninstall
  ;;
INSTALL | UPDATE)
  install
  ;;
esac
