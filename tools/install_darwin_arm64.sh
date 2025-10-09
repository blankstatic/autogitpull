#!/bin/bash

REPO_OWNER="blankstatic"
REPO_NAME="autogitpull"
BINARY_NAME="autogitpull"
VERSION="latest"
TARGET_DIR="/usr/local/bin"

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

sudo chmod +x "/tmp/$BINARY_NAME"
sudo xattr -r -d com.apple.quarantine "/tmp/$BINARY_NAME"

sudo cp "/tmp/$BINARY_NAME" "$TARGET_DIR/$BINARY_NAME"

if [ $? -eq 0 ]; then
    echo "✅ Установка завершена успешно!"
    echo "💡 Проверь работу командой: $BINARY_NAME version"
else
    echo "❌ Ошибка при копировании файла"
    exit 1
fi
