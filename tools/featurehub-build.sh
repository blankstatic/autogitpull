#!/usr/bin/env bash
set -euo pipefail

APP_NAME="${APP_NAME:-FeatureHubLauncher}"
DISPLAY_NAME="${DISPLAY_NAME:-Feature Hub}"
BUNDLE_ID="${BUNDLE_ID:-com.blankstatic.featurehub}"
DASHBOARD_URL="${DASHBOARD_URL:-http://localhost}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/Applications}"
SEND_TEST_NOTIFICATION=1
DO_CODESIGN=1

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
ICON_SRC="${ICON_SRC:-$SCRIPT_DIR/featurehub.icns}"
LSREGISTER="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
PLIST_BUDDY="/usr/libexec/PlistBuddy"

usage() {
    cat <<USAGE
Build a custom macOS terminal-notifier app bundle for Feature Hub.

The generated app sends notifications with the Feature Hub icon. Clicking a
notification opens the dashboard URL.

Usage:
  $(basename "$0") [options]

Options:
  --app-name NAME          App bundle filename without .app (default: $APP_NAME)
  --display-name NAME      macOS display name (default: $DISPLAY_NAME)
  --bundle-id ID           CFBundleIdentifier (default: $BUNDLE_ID)
  --url URL                Dashboard URL opened on click (default: $DASHBOARD_URL)
  --icon PATH              .icns file (default: $ICON_SRC)
  --install-dir PATH       Destination directory (default: $INSTALL_DIR)
  --no-notify              Build/register only; do not send a test notification
  --no-codesign            Skip ad-hoc codesign after modifying the bundle
  --help                   Show this help

Environment overrides:
  APP_NAME, DISPLAY_NAME, BUNDLE_ID, DASHBOARD_URL, ICON_SRC, INSTALL_DIR,
  TERMINAL_NOTIFIER_APP
USAGE
}

log() {
    printf '%s\n' "$*"
}

fail() {
    printf 'error: %s\n' "$*" >&2
    exit 1
}

require_file() {
    [[ -f "$1" ]] || fail "missing file: $1"
}

require_executable() {
    [[ -x "$1" ]] || fail "missing executable: $1"
}

option_value() {
    local option="$1"
    local value="${2:-}"

    [[ -n "$value" ]] || fail "$option requires a value"
    printf '%s\n' "$value"
}

set_plist_string() {
    local plist="$1"
    local key="$2"
    local value="$3"

    "$PLIST_BUDDY" -c "Set :$key $value" "$plist" 2>/dev/null || \
        "$PLIST_BUDDY" -c "Add :$key string $value" "$plist"
}

set_plist_bool() {
    local plist="$1"
    local key="$2"
    local value="$3"

    "$PLIST_BUDDY" -c "Set :$key $value" "$plist" 2>/dev/null || \
        "$PLIST_BUDDY" -c "Add :$key bool $value" "$plist"
}

resolve_path() {
    local path="$1"
    local dir
    local base
    local target

    while [[ -L "$path" ]]; do
        dir="$(cd -- "$(dirname -- "$path")" && pwd -P)"
        base="$(basename -- "$path")"
        target="$(readlink "$dir/$base")"
        if [[ "$target" = /* ]]; then
            path="$target"
        else
            path="$dir/$target"
        fi
    done

    dir="$(cd -- "$(dirname -- "$path")" && pwd -P)"
    base="$(basename -- "$path")"
    printf '%s/%s\n' "$dir" "$base"
}

find_terminal_notifier_app() {
    local candidate
    local notifier_bin
    local resolved_bin
    local brew_prefix

    if [[ -n "${TERMINAL_NOTIFIER_APP:-}" ]]; then
        candidate="$TERMINAL_NOTIFIER_APP"
        if [[ -x "$candidate/Contents/MacOS/terminal-notifier" ]]; then
            printf '%s\n' "$candidate"
            return 0
        fi
    fi

    if command -v terminal-notifier >/dev/null 2>&1; then
        notifier_bin="$(command -v terminal-notifier)"
        resolved_bin="$(resolve_path "$notifier_bin")"

        candidate="$(cd -- "$(dirname -- "$resolved_bin")/.." && pwd -P)/terminal-notifier.app"
        if [[ -x "$candidate/Contents/MacOS/terminal-notifier" ]]; then
            printf '%s\n' "$candidate"
            return 0
        fi
    fi

    if command -v brew >/dev/null 2>&1; then
        brew_prefix="$(brew --prefix terminal-notifier 2>/dev/null || true)"
        candidate="$brew_prefix/terminal-notifier.app"
        if [[ -x "$candidate/Contents/MacOS/terminal-notifier" ]]; then
            printf '%s\n' "$candidate"
            return 0
        fi
    fi

    for candidate in \
        "/Applications/terminal-notifier.app" \
        "$HOME/Applications/terminal-notifier.app"; do
        if [[ -x "$candidate/Contents/MacOS/terminal-notifier" ]]; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done

    return 1
}

copy_app_bundle() {
    local source_app="$1"
    local target_app="$2"

    if command -v ditto >/dev/null 2>&1; then
        ditto "$source_app" "$target_app"
    else
        cp -R "$source_app" "$target_app"
    fi
}

register_app() {
    local app_dir="$1"

    if [[ -x "$LSREGISTER" ]]; then
        "$LSREGISTER" -f "$app_dir" >/dev/null 2>&1 || true
    fi
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --app-name)
            APP_NAME="$(option_value "$1" "${2:-}")"
            shift 2
            ;;
        --display-name)
            DISPLAY_NAME="$(option_value "$1" "${2:-}")"
            shift 2
            ;;
        --bundle-id)
            BUNDLE_ID="$(option_value "$1" "${2:-}")"
            shift 2
            ;;
        --url)
            DASHBOARD_URL="$(option_value "$1" "${2:-}")"
            shift 2
            ;;
        --icon)
            ICON_SRC="$(option_value "$1" "${2:-}")"
            shift 2
            ;;
        --install-dir)
            INSTALL_DIR="$(option_value "$1" "${2:-}")"
            shift 2
            ;;
        --no-notify)
            SEND_TEST_NOTIFICATION=0
            shift
            ;;
        --no-codesign)
            DO_CODESIGN=0
            shift
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            fail "unknown option: $1"
            ;;
    esac
done

[[ "$(uname -s)" == "Darwin" ]] || fail "this script only supports macOS"
[[ -n "$APP_NAME" ]] || fail "--app-name cannot be empty"
[[ -n "$DISPLAY_NAME" ]] || fail "--display-name cannot be empty"
[[ -n "$BUNDLE_ID" ]] || fail "--bundle-id cannot be empty"
[[ -n "$DASHBOARD_URL" ]] || fail "--url cannot be empty"
[[ -n "$INSTALL_DIR" ]] || fail "--install-dir cannot be empty"
[[ "$APP_NAME" != *"/"* ]] || fail "--app-name must not contain /"
require_file "$ICON_SRC"
require_executable "$PLIST_BUDDY"

TERMINAL_NOTIFIER_APP="$(find_terminal_notifier_app)" || \
    fail "terminal-notifier.app not found; install it with: brew install terminal-notifier"

APP_DIR="$INSTALL_DIR/${APP_NAME}.app"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/featurehub-notifier.XXXXXX")"
BUILD_APP="$TMP_DIR/${APP_NAME}.app"

cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

log "Building $DISPLAY_NAME notifier app"
log "Source: $TERMINAL_NOTIFIER_APP"
log "Target: $APP_DIR"

mkdir -p "$INSTALL_DIR"
copy_app_bundle "$TERMINAL_NOTIFIER_APP" "$BUILD_APP"

cp "$ICON_SRC" "$BUILD_APP/Contents/Resources/AppIcon.icns"

PLIST="$BUILD_APP/Contents/Info.plist"
require_file "$PLIST"
set_plist_string "$PLIST" "CFBundleName" "$DISPLAY_NAME"
set_plist_string "$PLIST" "CFBundleDisplayName" "$DISPLAY_NAME"
set_plist_string "$PLIST" "CFBundleIdentifier" "$BUNDLE_ID"
set_plist_string "$PLIST" "CFBundleIconFile" "AppIcon"
set_plist_bool "$PLIST" "LSUIElement" "true"

chmod +x "$BUILD_APP/Contents/MacOS/terminal-notifier"

if [[ "$DO_CODESIGN" -eq 1 ]] && command -v codesign >/dev/null 2>&1; then
    codesign --force --sign - "$BUILD_APP" >/dev/null 2>&1 || \
        log "warning: ad-hoc codesign failed; continuing"
fi

rm -rf "$APP_DIR"
mv "$BUILD_APP" "$APP_DIR"

xattr -dr com.apple.quarantine "$APP_DIR" >/dev/null 2>&1 || true
touch "$APP_DIR"
register_app "$APP_DIR"

log "Built: $APP_DIR"

if [[ "$SEND_TEST_NOTIFICATION" -eq 1 ]]; then
    "$APP_DIR/Contents/MacOS/terminal-notifier" \
        -title "$DISPLAY_NAME" \
        -message "Open dashboard" \
        -open "$DASHBOARD_URL"
    log "Sent test notification; click it to open: $DASHBOARD_URL"
fi
