#!/usr/bin/env bash
set -euo pipefail

REPO_OWNER="${REPO_OWNER:-blankstatic}"
REPO_NAME="${REPO_NAME:-autogitpull}"
BINARY_NAME="${BINARY_NAME:-autogitpull}"
VERSION="${VERSION:-latest}"
TARGET_DIR="${TARGET_DIR:-/usr/local/bin}"
INSTALL_SERVICE=0
START_SERVICE=0

usage() {
    cat <<USAGE
Usage: install_linux.sh [--service] [--start-service]

Environment:
  VERSION      Release tag to install. Defaults to latest.
  TARGET_DIR   Binary install directory. Defaults to /usr/local/bin.

Options:
  --service        Run 'autogitpull service install' after installing the binary.
  --start-service  Install and start the user systemd service.
USAGE
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --service)
            INSTALL_SERVICE=1
            ;;
        --start-service)
            INSTALL_SERVICE=1
            START_SERVICE=1
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Unknown option: $1" >&2
            usage >&2
            exit 1
            ;;
    esac
    shift
done

if [[ "$(uname -s)" != "Linux" ]]; then
    echo "This installer is only for Linux" >&2
    exit 1
fi

case "$(uname -m)" in
    x86_64|amd64)
        ASSET_ARCH="amd64"
        ;;
    aarch64|arm64)
        ASSET_ARCH="arm64"
        ;;
    *)
        echo "Unsupported architecture: $(uname -m)" >&2
        exit 1
        ;;
esac

if [[ "$VERSION" == "latest" ]]; then
    LATEST_TAG="$(curl -fsSL --retry 3 --retry-delay 2 "https://api.github.com/repos/$REPO_OWNER/$REPO_NAME/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)"
    if [[ -z "$LATEST_TAG" ]]; then
        echo "Failed to determine latest release" >&2
        exit 1
    fi
    VERSION="$LATEST_TAG"
fi

ASSET_NAME="$BINARY_NAME-linux-$ASSET_ARCH"
DOWNLOAD_URL="https://github.com/$REPO_OWNER/$REPO_NAME/releases/download/$VERSION/$ASSET_NAME"
TMP_FILE="$(mktemp "${TMPDIR:-/tmp}/$BINARY_NAME.XXXXXX")"
trap 'rm -f "$TMP_FILE"' EXIT

echo "Downloading $ASSET_NAME from $VERSION..."
curl -fL --retry 3 --retry-delay 2 -o "$TMP_FILE" "$DOWNLOAD_URL"
chmod +x "$TMP_FILE"

echo "Installing to $TARGET_DIR/$BINARY_NAME..."
if [[ -w "$TARGET_DIR" ]]; then
    rm -f "$TARGET_DIR/$BINARY_NAME"
    install -m 0755 "$TMP_FILE" "$TARGET_DIR/$BINARY_NAME"
else
    sudo rm -f "$TARGET_DIR/$BINARY_NAME"
    sudo install -m 0755 "$TMP_FILE" "$TARGET_DIR/$BINARY_NAME"
fi

echo "Installed: $("$TARGET_DIR/$BINARY_NAME" version)"

if [[ "$INSTALL_SERVICE" -eq 1 ]]; then
    echo "Installing user systemd service..."
    "$TARGET_DIR/$BINARY_NAME" service install

    if [[ "$START_SERVICE" -eq 1 ]]; then
        "$TARGET_DIR/$BINARY_NAME" service start
        "$TARGET_DIR/$BINARY_NAME" service status
    else
        echo "Start it with: $BINARY_NAME service start"
    fi
fi
