#!/bin/bash
APP_NAME="Feature Hub"
BUNDLE_ID="com.blankstatic.featurehub"

#
# Test notification
#
terminal-notifier \
  -title "$APP_NAME" \
  -message "Swift app notification test" \
  -open http://localhost \
  -sender "$BUNDLE_ID"
  # -subtitle "Test"
  # -contentImage featurehub.icns
