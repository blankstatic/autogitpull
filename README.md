# autogitpull

`autogitpull` keeps a list of local Git repositories and safely runs `git pull`
for them in the background.

It is built for the common "many repos on one machine" workflow: register the
repositories once, then let the daemon keep default branches up to date while
you work.

## What It Does

- Registers Git repositories in `~/.autogitpull/updates.sqlite`.
- Detects each repository's remote default branch.
- Pulls repositories every 30 minutes when the daemon is running.
- Skips repositories that are not on their default branch.
- Skips repositories with uncommitted local changes.
- Records update history in `~/.autogitpull/updates.sqlite`.
- Shows registered repositories in a terminal UI.
- Starts a local dashboard at `http://localhost:9009` while the daemon runs.
- Sends desktop notifications when a pull brings new changes.
- Can be installed as a macOS `launchd` service or Linux user `systemd`
  service.

The safety rule is intentionally simple: `autogitpull` only pulls when the repo
is on its detected default branch and `git status --porcelain` is clean.

## Install

macOS Apple Silicon:

```sh
bash -c "$(curl -fsSL https://raw.githubusercontent.com/blankstatic/autogitpull/main/tools/install_darwin_arm64.sh)"
```

The installer downloads the latest `autogitpull-macos-arm64` release to
`/usr/local/bin/autogitpull`.

Optional, for richer macOS notifications:

```sh
brew install terminal-notifier
```

When `terminal-notifier` is available, the installer also builds
`~/Applications/FeatureHubLauncher.app` and uses it for clickable
notifications.

Linux amd64/arm64:

```sh
bash -c "$(curl -fsSL https://raw.githubusercontent.com/blankstatic/autogitpull/main/tools/install_linux.sh)"
```

Install and start the Linux user `systemd` service in one step:

```sh
bash -c "$(curl -fsSL https://raw.githubusercontent.com/blankstatic/autogitpull/main/tools/install_linux.sh)" -- --start-service
```

## Build From Source

```sh
cd autogitpull_go
go build -o autogitpull .
```

Run the local binary:

```sh
./autogitpull --help
```

## Quick Start

Register the current repository:

```sh
autogitpull register
```

Register specific repositories:

```sh
autogitpull register ~/work/project-a ~/work/project-b
```

Discover and register repositories under a directory:

```sh
autogitpull discover ~/work
```

Open the terminal UI:

```sh
autogitpull status
```

Run the daemon in the foreground:

```sh
autogitpull daemon
```

Then open:

```text
http://localhost:9009
```

## Daily Usage

Use `autogitpull status` to see what is registered:

```sh
autogitpull status
```

In the TUI:

```text
↑/↓  navigate
p    pull selected repository now
d    unregister selected repository
q    quit
```

Use the daemon when you want automatic updates:

```sh
autogitpull daemon
```

The daemon immediately checks all registered repositories, then repeats every
30 minutes. It logs JSON to stdout when running in the foreground.

## Commands

```text
autogitpull [command] [flags]
```

Available commands:

```text
register [paths...]       Add repositories. With no path, adds the current directory.
unregister [paths...]     Remove repositories. With no path, removes the current directory.
discover [paths...]       Recursively find Git repositories and register them.
status                    Open the terminal UI. This is also the default command.
daemon                    Run the auto-pull loop and web dashboard in the foreground.
service <command>         Manage the macOS launchd or Linux systemd service.
version                   Print the version.
```

Global flags:

```text
-s, --silently            Suppress desktop notifications for the command.
-h, --help                Show help.
```

Examples:

```sh
autogitpull register ~/src/api
autogitpull discover ~/src
autogitpull unregister ~/src/old-project
autogitpull status
autogitpull version
```

## Background Service

Install and start the background service:

```sh
autogitpull service install
autogitpull service start
```

Check service state:

```sh
autogitpull service status
```

Stop or remove it:

```sh
autogitpull service stop
autogitpull service uninstall
```

The service runs:

```sh
autogitpull daemon
```

On macOS it uses launchd label:

```text
com.blankstatic.autogitpull
```

Logs:

```sh
cat /tmp/com.blankstatic.autogitpull.log
cat /tmp/com.blankstatic.autogitpull.error.log
```

On Linux it installs a user `systemd` unit:

```text
~/.config/systemd/user/autogitpull.service
```

Linux logs:

```sh
journalctl --user -u autogitpull.service -f
```

To keep the Linux user service running after logout, enable lingering once:

```sh
sudo loginctl enable-linger "$USER"
```

## Dashboard

When `autogitpull daemon` or the background service is running, the dashboard is
available at:

```text
http://localhost:9009
```

The dashboard shows:

- registered repositories;
- last sync time;
- update history;
- skipped pulls;
- failed pulls;
- changed update activity for the last year;
- per-repository details and current local changes.

Notification clicks open the related repository page in this dashboard.

## Storage

Repositories, settings, and update history are stored in SQLite:

```sh
~/.autogitpull/updates.sqlite
```

On upgrade, an existing legacy config is migrated automatically:

```sh
~/.autogitpull/config.json
```

`register` and `discover` only add repositories where the remote default branch
can be detected from `origin/HEAD`.

`discover` skips hidden and heavy directories such as `.git`, `node_modules`,
`vendor`, `build`, `dist`, `target`, and cache/temp directories.

## Pull Behavior

For every registered repository, `autogitpull` does this:

1. Reads the current branch with `git branch --show-current`.
2. Compares it with the repository's stored `default_branch`.
3. Checks local changes with `git status --porcelain`.
4. Runs `git pull origin` only when the branch matches and the working tree is
   clean.
5. Records the result in the update history database.

Common skip cases:

```text
current branch feature/foo is not default branch main
repository has uncommitted changes
```

This avoids pulling into feature branches or overwriting active local work.

## Notifications

On macOS, `autogitpull` sends notifications for registration actions and for
successful pulls that bring changes.

By default it looks for:

```sh
~/Applications/FeatureHubLauncher.app
```

Build or rebuild the notification app manually:

```sh
brew install terminal-notifier
tools/featurehub-build.sh --no-notify
```

Environment overrides:

```sh
AUTOGITPULL_NOTIFIER_APP=/path/to/FeatureHubLauncher.app
AUTOGITPULL_DASHBOARD_URL=http://localhost:9009
```

Use `--silently` when you do not want desktop notifications for a command:

```sh
autogitpull register --silently ~/work/project
```

## Release Artifacts

GitHub Actions builds the macOS ARM64 binary:

```text
autogitpull-macos-arm64
```

The installer expects the release asset to use that exact name.
