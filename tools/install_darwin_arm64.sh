#!/bin/bash

REPO_OWNER="blankstatic"
REPO_NAME="autogitpull"
BINARY_NAME="autogitpull"
VERSION="latest"
TARGET_DIR="${TARGET_DIR:-/usr/local/bin}"
RAW_BASE_URL="https://raw.githubusercontent.com/$REPO_OWNER/$REPO_NAME/main"

if [[ "$(uname)" != "Darwin" ]]; then
    echo "❌ Этот скрипт предназначен только для macOS"
    exit 1
fi

ARCH=$(uname -m)
if [[ "$ARCH" != "arm64" ]]; then
    echo "❌ Этот скрипт предназначен только для ARM64 (Apple Silicon) архитектуры"
    echo "💡 Текущая архитектура: $ARCH"
    exit 1
fi

if [ "$VERSION" = "latest" ]; then
    LATEST_TAG=$(curl -s "https://api.github.com/repos/$REPO_OWNER/$REPO_NAME/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

    if [ -z "$LATEST_TAG" ]; then
        echo "❌ Не удалось определить последнюю версию"
        exit 1
    fi

    echo "📦 Найдена последняя версия: $LATEST_TAG"
    DOWNLOAD_URL="https://github.com/$REPO_OWNER/$REPO_NAME/releases/download/$LATEST_TAG/$BINARY_NAME-macos-arm64"
else
    DOWNLOAD_URL="https://github.com/$REPO_OWNER/$REPO_NAME/releases/download/$VERSION/$BINARY_NAME-macos-arm64"
fi

echo "📥 Загрузка $BINARY_NAME из $DOWNLOAD_URL..."

curl -L -o "/tmp/$BINARY_NAME" "$DOWNLOAD_URL"

if [ $? -ne 0 ]; then
    echo "❌ Ошибка при загрузке файла"
    exit 1
fi

echo "🔧 Установка в $TARGET_DIR..."

chmod +x "/tmp/$BINARY_NAME"
xattr -r -d com.apple.quarantine "/tmp/$BINARY_NAME" 2>/dev/null || true

if [ -w "$TARGET_DIR" ]; then
    rm -f "$TARGET_DIR/$BINARY_NAME"
    install -m 0755 "/tmp/$BINARY_NAME" "$TARGET_DIR/$BINARY_NAME"
else
    sudo rm -f "$TARGET_DIR/$BINARY_NAME"
    sudo install -m 0755 "/tmp/$BINARY_NAME" "$TARGET_DIR/$BINARY_NAME"
fi

if [ $? -eq 0 ]; then
    echo "✅ Установка завершена успешно!"
    echo "💡 Проверь работу командой: $BINARY_NAME version"
else
    echo "❌ Ошибка при установке файла"
    exit 1
fi

if command -v terminal-notifier >/dev/null 2>&1; then
    echo "🔔 Установка Feature Hub notifier..."

    NOTIFIER_TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/autogitpull-notifier.XXXXXX")
    trap 'rm -rf "$NOTIFIER_TMP_DIR"' EXIT

    if curl -fsSL "$RAW_BASE_URL/tools/featurehub-build.sh" -o "$NOTIFIER_TMP_DIR/featurehub-build.sh" \
        && curl -fsSL "$RAW_BASE_URL/tools/featurehub.icns" -o "$NOTIFIER_TMP_DIR/featurehub.icns" \
        && chmod +x "$NOTIFIER_TMP_DIR/featurehub-build.sh" \
        && "$NOTIFIER_TMP_DIR/featurehub-build.sh" --no-notify; then
        echo "✅ Feature Hub notifier установлен"
    else
        echo "⚠️ Не удалось установить Feature Hub notifier; $BINARY_NAME продолжит использовать fallback уведомления"
    fi
else
    echo "ℹ️ terminal-notifier не найден; для кастомных macOS уведомлений: brew install terminal-notifier"
fi
