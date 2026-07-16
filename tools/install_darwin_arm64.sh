#!/usr/bin/env bash
set -euo pipefail

REPO_OWNER="${REPO_OWNER:-blankstatic}"
REPO_NAME="${REPO_NAME:-autogitpull}"
BINARY_NAME="${BINARY_NAME:-autogitpull}"
VERSION="${VERSION:-latest}"
TARGET_DIR="${TARGET_DIR:-/usr/local/bin}"
SERVICE_LABEL="com.blankstatic.autogitpull"
SERVICE_PLIST="$HOME/Library/LaunchAgents/$SERVICE_LABEL.plist"
TMP_FILE=""
STAGED_FILE="$TARGET_DIR/.$BINARY_NAME.new.$$"
BACKUP_FILE="$TARGET_DIR/.$BINARY_NAME.backup.$$"
PLIST_BACKUP=""
STAGED_CREATED=0
BACKUP_CREATED=0
PLIST_BACKUP_CREATED=0
SERVICE_WAS_RUNNING=0
SERVICE_STOPPED=0
SERVICE_START_ATTEMPTED=0
BINARY_REPLACED=0
PLIST_TOUCHED=0

cleanup() {
	[[ -z "$TMP_FILE" ]] || rm -f "$TMP_FILE" || true
	[[ -z "$PLIST_BACKUP" ]] || rm -f "$PLIST_BACKUP" || true
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
		launchctl stop "$SERVICE_LABEL" >/dev/null 2>&1 || true
		if [[ -f "$SERVICE_PLIST" ]]; then
			launchctl unload "$SERVICE_PLIST" >/dev/null 2>&1 || true
		fi
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
	if [[ "$PLIST_BACKUP_CREATED" -eq 1 ]]; then
		cp -p "$PLIST_BACKUP" "$SERVICE_PLIST" || rollback_failed=1
	elif [[ "$PLIST_TOUCHED" -eq 1 ]]; then
		rm -f "$SERVICE_PLIST" || rollback_failed=1
	fi
	if [[ "$SERVICE_WAS_RUNNING" -eq 1 && "$SERVICE_STOPPED" -eq 1 ]]; then
		launchctl load "$SERVICE_PLIST" >/dev/null 2>&1 || rollback_failed=1
		launchctl start "$SERVICE_LABEL" >/dev/null 2>&1 || rollback_failed=1
	fi
	if [[ "$rollback_failed" -eq 1 ]]; then
		echo "CRITICAL: rollback was incomplete; inspect $TARGET_DIR/$BINARY_NAME and $SERVICE_PLIST" >&2
	else
		echo "Previous installation restored." >&2
	fi
	exit "$status"
}
trap cleanup EXIT
trap rollback ERR

if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "This installer is only for macOS" >&2
    exit 1
fi
if [[ "$(uname -m)" != "arm64" ]]; then
    echo "This installer supports Apple Silicon (arm64) only" >&2
    exit 1
fi

if [[ "$VERSION" == "latest" ]]; then
    VERSION="$(curl -fsSL --retry 3 --retry-delay 2 "https://api.github.com/repos/$REPO_OWNER/$REPO_NAME/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)"
    if [[ -z "$VERSION" ]]; then
        echo "Failed to determine latest release" >&2
        exit 1
    fi
fi
RAW_BASE_URL="https://raw.githubusercontent.com/$REPO_OWNER/$REPO_NAME/$VERSION"

ASSET_NAME="$BINARY_NAME-macos-arm64"
DOWNLOAD_URL="https://github.com/$REPO_OWNER/$REPO_NAME/releases/download/$VERSION/$ASSET_NAME"
TMP_FILE="$(mktemp "${TMPDIR:-/tmp}/$BINARY_NAME.XXXXXX")"

echo "Downloading $ASSET_NAME from $VERSION..."
curl -fL --retry 3 --retry-delay 2 -o "$TMP_FILE" "$DOWNLOAD_URL"
chmod +x "$TMP_FILE"
xattr -r -d com.apple.quarantine "$TMP_FILE" 2>/dev/null || true
"$TMP_FILE" version >/dev/null

SERVICE_INSTALLED=0
[[ -f "$SERVICE_PLIST" ]] && SERVICE_INSTALLED=1
if launchctl list "$SERVICE_LABEL" >/dev/null 2>&1; then
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
	PLIST_BACKUP="$(mktemp "${TMPDIR:-/tmp}/autogitpull-plist.XXXXXX")"
	cp -p "$SERVICE_PLIST" "$PLIST_BACKUP"
	PLIST_BACKUP_CREATED=1
fi

if [[ "$SERVICE_WAS_RUNNING" -eq 1 ]]; then
	echo "Stopping running launchd service..."
	SERVICE_STOPPED=1
	launchctl stop "$SERVICE_LABEL" >/dev/null 2>&1 || true
	launchctl unload "$SERVICE_PLIST"
fi

if [[ -w "$TARGET_DIR" ]]; then
    mv -f "$STAGED_FILE" "$TARGET_DIR/$BINARY_NAME"
else
	sudo mv -f "$STAGED_FILE" "$TARGET_DIR/$BINARY_NAME"
fi
STAGED_CREATED=0
BINARY_REPLACED=1
echo "Installed: $("$TARGET_DIR/$BINARY_NAME" version)"

if [[ "$SERVICE_INSTALLED" -eq 1 ]]; then
	echo "Updating launchd service definition..."
	PLIST_TOUCHED=1
	"$TARGET_DIR/$BINARY_NAME" service install
fi
if [[ "$SERVICE_INSTALLED" -eq 1 ]]; then
	if [[ "$SERVICE_WAS_RUNNING" -eq 1 ]]; then
		echo "Restarting launchd service..."
	else
		echo "Starting installed launchd service..."
	fi
	SERVICE_START_ATTEMPTED=1
	    "$TARGET_DIR/$BINARY_NAME" service start
		"$TARGET_DIR/$BINARY_NAME" service status
fi
trap - ERR
BINARY_REPLACED=0
SERVICE_STOPPED=0
SERVICE_START_ATTEMPTED=0
if [[ "$BACKUP_CREATED" -eq 1 ]]; then
	if [[ -w "$TARGET_DIR" ]]; then rm -f "$BACKUP_FILE" || true; else sudo rm -f "$BACKUP_FILE" || true; fi
	BACKUP_CREATED=0
fi

if command -v terminal-notifier >/dev/null 2>&1; then
    echo "Installing Feature Hub notifier..."
    NOTIFIER_TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/autogitpull-notifier.XXXXXX")"
    if curl -fsSL --retry 3 --retry-delay 2 "$RAW_BASE_URL/tools/featurehub-build.sh" -o "$NOTIFIER_TMP_DIR/featurehub-build.sh" \
        && curl -fsSL --retry 3 --retry-delay 2 "$RAW_BASE_URL/tools/featurehub.icns" -o "$NOTIFIER_TMP_DIR/featurehub.icns" \
        && chmod +x "$NOTIFIER_TMP_DIR/featurehub-build.sh" \
        && "$NOTIFIER_TMP_DIR/featurehub-build.sh" --no-notify; then
        echo "Feature Hub notifier installed"
    else
        echo "Warning: Feature Hub notifier installation failed; fallback notifications remain available" >&2
    fi
	rm -rf "$NOTIFIER_TMP_DIR"
else
    echo "terminal-notifier not found; install it with: brew install terminal-notifier"
fi
