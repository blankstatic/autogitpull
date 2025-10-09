# That tool makes pulling multiple git repositories even easier

## Demo

![example](.vhs/demo.gif)
![example](../.vhs/demo.gif)

## Install

```sh
sh -c "$(curl -fsSL https://github.com/blankstatic/autogitpull/main/tools/install_darwin_arm64.sh)"
```

## MacOS Optional Requirements

```sh
brew install terminal-notifier
```

## Service

```sh
launchctl list com.blankstatic.autogitpull
```

```sh
cat /tmp/com.blankstatic.autogitpull.log
```

```sh
cat /tmp/com.blankstatic.autogitpull.error.log
```

## Application

```sh
# Удалите карантинный атрибут
sudo xattr -r -d com.apple.quarantine autogitpull-macos-arm64

# Дайте права на выполнение
sudo chmod +x autogitpull-macos-arm64
```