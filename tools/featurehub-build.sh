#!/bin/bash
set -e

APP_NAME="FeatureHubLauncher"
DISPLAY_NAME="Feature Hub"
BUNDLE_ID="com.blankstatic.featurehub"
APP_DIR="$HOME/Applications/${APP_NAME}.app"
URL="http://localhost"

echo "🧹 Cleaning old app..."

rm -rf "$APP_DIR"

mkdir -p "$APP_DIR/Contents/MacOS"
mkdir -p "$APP_DIR/Contents/Resources"

# -------------------------
# ICON (must be exact name)
# -------------------------
if [[ -f "./featurehub.icns" ]]; then
    cp "./featurehub.icns" "$APP_DIR/Contents/Resources/AppIcon.icns"
fi

# -------------------------
# Info.plist (CRITICAL FIX)
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

</dict>
</plist>
EOF

# -------------------------
# Swift launcher (NO UI, exits immediately)
# -------------------------
cat > "$APP_DIR/Contents/MacOS/main.swift" <<EOF
import Cocoa

let url = URL(string: "${URL}")!
NSWorkspace.shared.open(url)
exit(0)
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
open "$APP_DIR" >/dev/null 2>&1 || true

"/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister" \
-kill -r -domain local -domain system -domain user >/dev/null 2>&1 || true

# -------------------------
# Force Dock refresh
# -------------------------
killall Dock >/dev/null 2>&1 || true

echo "✔ App built successfully"

# -------------------------
# Notification (WORKING CLICK)
# -------------------------
terminal-notifier \
  -title "${DISPLAY_NAME}" \
  -message "Open localhost" \
  -sender "${BUNDLE_ID}" \
  -execute "$APP_DIR/Contents/MacOS/${APP_NAME}"

echo "✔ Notification sent"