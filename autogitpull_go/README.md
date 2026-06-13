# That tool makes pulling multiple git repositories even easier

## Demo

![example](.vhs/demo.gif)
![example](../.vhs/demo.gif)

## Install

```sh
bash -c "$(curl -fsSL https://raw.githubusercontent.com/blankstatic/autogitpull/main/tools/install_darwin_arm64.sh)"
```

## MacOS Optional Requirements

```sh
brew install terminal-notifier
tools/featurehub-build.sh --no-notify
```

Test notification
```sh
~/Applications/FeatureHubLauncher.app/Contents/MacOS/terminal-notifier \
  -message test \
  -title autogitpull \
  -subtitle 'New commit' \
  -open http://localhost
```

`autogitpull` uses `~/Applications/FeatureHubLauncher.app` on macOS when it
exists. Override paths with `AUTOGITPULL_NOTIFIER_APP` and
`AUTOGITPULL_DASHBOARD_URL`.

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

# Bundle ID
```sh
osascript -e 'id of app "Games"'

com.apple.games
```

```
Git Watcher (engine)
        ↓
Feature Hub (dashboard UI)
        ↓
macOS Notification Center (optional output)
```
