#!/usr/bin/env bash
set -euo pipefail

REPO_OWNER="${REPO_OWNER:-blankstatic}"
REPO_NAME="${REPO_NAME:-autogitpull}"
BINARY_NAME="${BINARY_NAME:-autogitpull}"
VERSION="${VERSION:-latest}"
TARGET_DIR="${TARGET_DIR:-/usr/local/bin}"
SERVICE_UNIT_NAME="autogitpull.service"
SERVICE_UNIT_PATH="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/$SERVICE_UNIT_NAME"
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
STAGED_FILE="$TARGET_DIR/.$BINARY_NAME.new.$$"
BACKUP_FILE="$TARGET_DIR/.$BINARY_NAME.backup.$$"
UNIT_BACKUP=""
STAGED_CREATED=0
BACKUP_CREATED=0
UNIT_BACKUP_CREATED=0
SERVICE_WAS_RUNNING=0
SERVICE_STOPPED=0
SERVICE_START_ATTEMPTED=0
BINARY_REPLACED=0
UNIT_TOUCHED=0
cleanup() {
	rm -f "$TMP_FILE" || true
	[[ -z "$UNIT_BACKUP" ]] || rm -f "$UNIT_BACKUP" || true
	if [[ "$STAGED_CREATED" -eq 1 || "$BACKUP_CREATED" -eq 1 ]]; then
		if [[ -w "$TARGET_DIR" ]]; then
			rm -f "$STAGED_FILE" "$BACKUP_FILE" || true
		else
			sudo rm -f "$STAGED_FILE" "$BACKUP_FILE" 2>/dev/null || true
		fi
	fi
}

rollback() {
	status=$?
	trap - ERR
	set +e
	echo "Update failed; restoring previous installation..." >&2
	rollback_failed=0
	if [[ "$SERVICE_STOPPED" -eq 1 || "$SERVICE_START_ATTEMPTED" -eq 1 ]]; then
		systemctl --user stop "$SERVICE_UNIT_NAME" || rollback_failed=1
	fi
	if [[ "$BINARY_REPLACED" -eq 1 ]]; then
		if [[ "$BACKUP_CREATED" -eq 1 ]]; then
			if [[ -w "$TARGET_DIR" ]]; then
				mv -f "$BACKUP_FILE" "$TARGET_DIR/$BINARY_NAME" || rollback_failed=1
			else
				sudo mv -f "$BACKUP_FILE" "$TARGET_DIR/$BINARY_NAME" || rollback_failed=1
			fi
			[[ "$rollback_failed" -eq 1 ]] || BACKUP_CREATED=0
		else
			if [[ -w "$TARGET_DIR" ]]; then rm -f "$TARGET_DIR/$BINARY_NAME" || rollback_failed=1; else sudo rm -f "$TARGET_DIR/$BINARY_NAME" || rollback_failed=1; fi
		fi
	fi
	if [[ "$UNIT_BACKUP_CREATED" -eq 1 ]]; then
		mkdir -p "$(dirname "$SERVICE_UNIT_PATH")" || rollback_failed=1
		cp -p "$UNIT_BACKUP" "$SERVICE_UNIT_PATH" || rollback_failed=1
		systemctl --user daemon-reload || rollback_failed=1
	elif [[ "$UNIT_TOUCHED" -eq 1 ]]; then
		rm -f "$SERVICE_UNIT_PATH" || rollback_failed=1
		systemctl --user daemon-reload || rollback_failed=1
	fi
	if [[ "$SERVICE_WAS_RUNNING" -eq 1 && "$SERVICE_STOPPED" -eq 1 ]]; then
		systemctl --user start "$SERVICE_UNIT_NAME" || rollback_failed=1
	fi
	if [[ "$rollback_failed" -eq 1 ]]; then
		echo "CRITICAL: rollback was incomplete; inspect $TARGET_DIR/$BINARY_NAME and $SERVICE_UNIT_PATH" >&2
	else
		echo "Previous installation restored." >&2
	fi
	exit "$status"
}
trap cleanup EXIT
trap rollback ERR

echo "Downloading $ASSET_NAME from $VERSION..."
curl -fL --retry 3 --retry-delay 2 -o "$TMP_FILE" "$DOWNLOAD_URL"
chmod +x "$TMP_FILE"
"$TMP_FILE" version >/dev/null

SERVICE_INSTALLED=0
[[ -f "$SERVICE_UNIT_PATH" ]] && SERVICE_INSTALLED=1
if systemctl --user is-active --quiet "$SERVICE_UNIT_NAME"; then
    SERVICE_WAS_RUNNING=1
    SERVICE_INSTALLED=1
fi

echo "Preparing installation in $TARGET_DIR..."
if [[ -w "$TARGET_DIR" ]]; then
    install -m 0755 "$TMP_FILE" "$STAGED_FILE"
else
	sudo install -m 0755 "$TMP_FILE" "$STAGED_FILE"
fi
STAGED_CREATED=1
if [[ -f "$TARGET_DIR/$BINARY_NAME" ]]; then
	if [[ -w "$TARGET_DIR" ]]; then
		cp -p "$TARGET_DIR/$BINARY_NAME" "$BACKUP_FILE"
	else
		sudo cp -p "$TARGET_DIR/$BINARY_NAME" "$BACKUP_FILE"
	fi
	BACKUP_CREATED=1
fi
if [[ "$SERVICE_INSTALLED" -eq 1 ]]; then
	UNIT_BACKUP="$(mktemp "${TMPDIR:-/tmp}/autogitpull-unit.XXXXXX")"
	cp -p "$SERVICE_UNIT_PATH" "$UNIT_BACKUP"
	UNIT_BACKUP_CREATED=1
fi

if [[ "$SERVICE_WAS_RUNNING" -eq 1 ]]; then
	echo "Stopping running systemd service..."
	SERVICE_STOPPED=1
	systemctl --user stop "$SERVICE_UNIT_NAME"
fi

if [[ -w "$TARGET_DIR" ]]; then
    mv -f "$STAGED_FILE" "$TARGET_DIR/$BINARY_NAME"
else
	sudo mv -f "$STAGED_FILE" "$TARGET_DIR/$BINARY_NAME"
fi
STAGED_CREATED=0
BINARY_REPLACED=1

echo "Installed: $("$TARGET_DIR/$BINARY_NAME" version)"

if [[ "$INSTALL_SERVICE" -eq 1 || "$SERVICE_INSTALLED" -eq 1 ]]; then
	echo "Installing user systemd service..."
	UNIT_TOUCHED=1
	"$TARGET_DIR/$BINARY_NAME" service install

fi

if [[ "$START_SERVICE" -eq 1 || "$SERVICE_INSTALLED" -eq 1 ]]; then
	    echo "Starting user systemd service..."
	SERVICE_START_ATTEMPTED=1
	    "$TARGET_DIR/$BINARY_NAME" service start
    "$TARGET_DIR/$BINARY_NAME" service status
elif [[ "$INSTALL_SERVICE" -eq 1 ]]; then
	echo "Start it with: $BINARY_NAME service start"
fi
trap - ERR
BINARY_REPLACED=0
SERVICE_STOPPED=0
SERVICE_START_ATTEMPTED=0
if [[ "$BACKUP_CREATED" -eq 1 ]]; then
	if [[ -w "$TARGET_DIR" ]]; then rm -f "$BACKUP_FILE" || true; else sudo rm -f "$BACKUP_FILE" || true; fi
	BACKUP_CREATED=0
fi
