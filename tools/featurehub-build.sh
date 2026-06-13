#!/bin/bash
set -euo pipefail

APP_NAME="FeatureHubLauncher"
DISPLAY_NAME="Feature Hub"
BUNDLE_ID="com.blankstatic.featurehub"
APP_DIR="$HOME/Applications/${APP_NAME}.app"
URL="http://localhost"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ICON_SRC="$SCRIPT_DIR/featurehub.icns"
LSREGISTER="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

echo "🧹 Cleaning old app..."

rm -rf "$APP_DIR"

mkdir -p "$APP_DIR/Contents/MacOS"
mkdir -p "$APP_DIR/Contents/Resources"

# -------------------------
# ICON (must be exact name)
# -------------------------
if [[ -f "$ICON_SRC" ]]; then
    cp "$ICON_SRC" "$APP_DIR/Contents/Resources/AppIcon.icns"
else
    echo "❌ Icon not found: $ICON_SRC"
    exit 1
fi

# -------------------------
# Info.plist
# -------------------------
cat > "$APP_DIR/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
"http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>

    <key>CFBundleName</key>
    <string>${DISPLAY_NAME}</string>

    <key>CFBundleDisplayName</key>
    <string>${DISPLAY_NAME}</string>

    <key>CFBundleIdentifier</key>
    <string>${BUNDLE_ID}</string>

    <key>CFBundleExecutable</key>
    <string>${APP_NAME}</string>

    <key>CFBundlePackageType</key>
    <string>APPL</string>

    <key>CFBundleIconFile</key>
    <string>AppIcon</string>

    <key>NSHighResolutionCapable</key>
    <true/>

    <key>LSUIElement</key>
    <true/>

</dict>
</plist>
EOF

# -------------------------
# Swift launcher (opens dashboard and exits)
# -------------------------
cat > "$APP_DIR/Contents/MacOS/main.swift" <<EOF
import Cocoa

let url = URL(string: "${URL}")!
NSWorkspace.shared.open(url)
EOF

# -------------------------
# Compile binary (IMPORTANT NAME MATCH)
# -------------------------
swiftc \
  "$APP_DIR/Contents/MacOS/main.swift" \
  -o "$APP_DIR/Contents/MacOS/${APP_NAME}" \
  -framework Cocoa

rm "$APP_DIR/Contents/MacOS/main.swift"

chmod +x "$APP_DIR/Contents/MacOS/${APP_NAME}"

# -------------------------
# Register app properly
# -------------------------
"$LSREGISTER" -f "$APP_DIR" >/dev/null 2>&1 || true
"$LSREGISTER" -kill -r -domain local -domain system -domain user >/dev/null 2>&1 || true
"$LSREGISTER" -f "$APP_DIR" >/dev/null 2>&1 || true

# -------------------------
# Force Dock refresh
# -------------------------
killall Dock >/dev/null 2>&1 || true

echo "✔ App built successfully"

if ! command -v terminal-notifier >/dev/null 2>&1; then
    echo "⚠ terminal-notifier is not installed; run: brew install terminal-notifier"
    exit 0
fi

# -------------------------
# Notification (custom icon via sender, click opens dashboard URL)
# -------------------------
terminal-notifier \
  -title "${DISPLAY_NAME}" \
  -message "Open localhost" \
  -sender "${BUNDLE_ID}" \
  -open "${URL}"

echo "✔ Notification sent"
