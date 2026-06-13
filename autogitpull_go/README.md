# autogitpull

`autogitpull` keeps registered Git repositories up to date. It can run on
demand, show repository status in a terminal UI, or run as a macOS launchd
service.

## Demo

![example](.vhs/demo.gif)
![example](../.vhs/demo.gif)

## Install

macOS Apple Silicon:

```sh
bash -c "$(curl -fsSL https://raw.githubusercontent.com/blankstatic/autogitpull/main/tools/install_darwin_arm64.sh)"
```

The installer downloads the latest `autogitpull-macos-arm64` release to
`/usr/local/bin/autogitpull`.

For custom macOS notification icons and click-to-open behavior, install
`terminal-notifier` before running the installer:

```sh
brew install terminal-notifier
```

When `terminal-notifier` is present, the installer also builds
`~/Applications/FeatureHubLauncher.app`. `autogitpull` uses that app for
notifications and falls back to the built-in notifier when it is missing.

## Build From Source

From the repository root:

```sh
cd autogitpull_go
go build -o autogitpull .
```

## Quick Start

Register the current repository:

```sh
autogitpull register
```

Register one or more explicit repositories:

```sh
autogitpull register ~/work/project-a ~/work/project-b
```

Discover repositories recursively under a directory:

```sh
autogitpull discover ~/work
```

Open the terminal status UI:

```sh
autogitpull status
```

Run the daemon in the foreground:

```sh
autogitpull daemon
```

## Commands

```sh
autogitpull [flags]
autogitpull [command]
```

Commands:

```text
daemon       Run the pull loop in the foreground
discover     Recursively find Git repositories and add them to the config
register     Add the current directory or supplied paths to the config
service      Manage the macOS launchd service
status       Show registered repositories in the terminal UI
unregister   Remove the current directory or supplied paths from the config
version      Print the application version
```

Global flags:

```text
-s, --silently   suppress desktop notifications for the command
-h, --help       show help
```

## Configuration

Configuration is stored at:

```sh
~/.autogitpull/config.json
```

Example:

```json
{
  "repositories": [
    {
      "path": "/Users/me/work/project",
      "name": "project",
      "default_branch": "main",
      "added_at": "2026-06-13T12:00:00Z",
      "last_sync": "2026-06-13T12:00:00Z"
    }
  ]
}
```

`register` only adds repositories where the remote default branch can be
detected. `discover` skips hidden directories and common heavy folders such as
`.git`, `node_modules`, `vendor`, `build`, `dist`, `target`, and cache/temp
directories.

## macOS Service

Install and start the launchd service:

```sh
autogitpull service install
autogitpull service start
```

Check status or stop it:

```sh
autogitpull service status
autogitpull service stop
```

Uninstall:

```sh
autogitpull service uninstall
```

The service runs `autogitpull daemon` every 30 minutes using launchd label
`com.blankstatic.autogitpull`.

Logs:

```sh
cat /tmp/com.blankstatic.autogitpull.log
cat /tmp/com.blankstatic.autogitpull.error.log
```

## Notifications

On macOS, `autogitpull` first tries to use:

```sh
~/Applications/FeatureHubLauncher.app
```

Build or rebuild it manually from the repository root:

```sh
brew install terminal-notifier
tools/featurehub-build.sh --no-notify
```

Send a test notification:

```sh
~/Applications/FeatureHubLauncher.app/Contents/MacOS/terminal-notifier \
  -message test \
  -title autogitpull \
  -subtitle 'New commit' \
  -open http://localhost
```

Environment overrides:

```sh
AUTOGITPULL_NOTIFIER_APP=/path/to/FeatureHubLauncher.app
AUTOGITPULL_DASHBOARD_URL=http://localhost
```

The notification app is a customized copy of `terminal-notifier.app` with the
Feature Hub icon and bundle id `com.blankstatic.featurehub`. This is required
because `terminal-notifier -sender` uses the sender app as the click action and
cannot be combined with `-open`.

## Release Artifacts

GitHub Actions builds the macOS ARM64 binary:

```text
autogitpull_go/autogitpull-macos-arm64
```

The release installer expects the asset name:

```text
autogitpull-macos-arm64
```
