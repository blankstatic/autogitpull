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
- Runs change-processing plugins after recorded pulls.
- Sends desktop notifications through the built-in Notifications plugin.
- Can store plugin output, including prepared AI-summary context, per update.
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
Re-running it performs an update. If the launchd service is installed, its
definition is refreshed and it is started after the atomic binary replacement;
an active service is stopped first for a clean restart.

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

Re-running the Linux installer also updates an existing user systemd unit and
starts it after the atomic binary replacement. `VERSION=vX.Y.Z` installs a
specific release on either platform.

## Build From Source

```sh
cd src
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
30 minutes. Repository pulls run with bounded concurrency so a large repository
set does not start unbounded git processes. The daemon and web dashboard also
share an in-process repo lock so the same repository is not pulled twice at the
same time from the same running process. It logs JSON to stdout when running in
the foreground.

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
- changed update activity for the last year;
- update history;
- skipped and failed pulls;
- a compact plugin summary;
- per-repository details and current local changes.

Additional dashboard pages:

- `/status` shows service state, database path, plugin summary, and daemon run
  status.
- `/settings` configures daemon pull interval and history retention.
- `/plugins` enables and configures change-processing plugins.

Dashboard bulk `Pull all` starts eligible repositories in the background and
returns immediately. Progress appears through update history, repository status,
and the `/status` daemon-style running list.

Notification clicks open the related repository page in this dashboard.

## Plugins

Plugins run from the shared pull/update pipeline used by the web dashboard, the
terminal UI, and the daemon:

1. A pull is recorded with `db.Store.FinishUpdate`.
2. The saved `db.Update` is loaded.
3. `plugins.RunAfterChange` runs enabled plugins.

By default, plugins run only when the saved update has `changed = true`. A plugin
can opt into no-change runs. The built-in Notifications plugin is enabled by
default with `title_prefix=Pulled` and also runs for manual web and TUI pulls
that complete without new changes.
Its plugin card has a test button that sends a standalone OS notification using
the current title prefix without saving the form.
Web and daemon after-pull plugin processing runs after the update is saved and
does not block pull completion, so slow providers such as AI summary APIs cannot
normally stall the pull loop. Web and daemon use bounded plugin worker queues
with backpressure instead of unbounded goroutines. Shutdown stops accepting HTTP
work, waits for active handlers and web bulk pulls, and drains both plugin queues.
Web and daemon shutdown use one shared deadline. If it expires, queued plugin
work is skipped and running plugin HTTP requests and Git diff commands receive
context cancellation before the shared database is closed.

The built-in AI Summary plugin is disabled by default. When enabled, it uses the
saved `before_rev` and `after_rev` range to collect a compact commit log and
`git diff --stat` metadata plus selected per-file unified code diffs, then
stores the generated summary in plugin results.
Configure a provider name, API key, model, prompt, API type, and API URL on the
plugin settings page. Plugin cards use a compact responsive settings grid;
AI Summary keeps connection, model, prompt, and code-detail choices visible while
file filters and context limits live under a collapsed Advanced context controls section.
long prompts and file patterns receive wider rows, while field explanations are
available from the `?` hints without permanently expanding the page. Any OpenAI-compatible provider can use either Responses API
or Chat Completions by changing the API type and URL. Use the AI Summary `Test`
button on the plugin settings page to send `hello` and verify the values currently
shown in the form, including unsaved edits; testing does not save them. API URL,
API key, and model must be filled before the plugin
will call a provider. The configured prompt is sent as the system
prompt/instructions. The user input is the repository name plus a compact commit log,
metadata, `git diff --stat`, and a unified code diff for the saved
`before_rev..after_rev` range. For large changes, metadata is kept first, file
metadata uses a bounded part of the request so code retains its own budget, diffs
are added until the context budget is exhausted, and omitted files are
listed explicitly. The plugin settings control whether code diffs are disabled,
limited per file, or sent in full; optional include/exclude patterns control which
files are eligible. Common secret, dependency, build, and lock-file patterns are
excluded by default. Total-context and per-file byte limits are editable, and the
input preview states why each excluded file was omitted. Values are bounded to
safe ranges (256–2000000 bytes total and at least 64 bytes per file). Git diff
output is capped while it is read, so a very large changed file cannot cause
unbounded memory use. The context-around-changes setting controls how many
unchanged lines surround each diff block (3, 10, 20, 40, or 80; default 20),
trading local detail for coverage of more files. Each generated summary is stored as a separate plugin result,
so a change can have multiple AI summaries.
It can be run manually from a repo page, scoped to selected repositories through
the generic repo plugin controls, or run globally for all changed updates from
the plugin settings page. The plugin settings page lets every plugin switch
between all repositories and selected repositories, and shows the selected
repository list for repo-scoped plugins.

Each recorded update has a change details page at `/update?id=<id>`. It shows
the pull output or error, all AI summaries for that update, and a button to
generate another summary. Pull notifications deep-link to this change page, and
notification sent/skipped/error diagnostics are shown in that page's plugin
results. The page also shows the exact AI prompt and user input preview for that
change. Plugin results are shown newest first.

Plugin settings are stored in the `plugin_settings` table in
`~/.autogitpull/updates.sqlite`.

## Storage

Repositories, settings, plugin settings, update history, and plugin results are
stored in SQLite:

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

Pulls are serialized across the web UI, daemon, TUI, and separate autogitpull
processes. Repository paths are canonicalized so aliases and symlinks cannot
bypass the lock. Pulls have a five-minute timeout. If Git reports an
`index.lock`, autogitpull removes and retries it once only when the lock is a
regular file at least one hour old; fresh locks are left untouched.

For every registered repository, `autogitpull` does this:

1. Reads the current branch with `git branch --show-current`.
2. Compares it with the repository's stored `default_branch`.
3. Checks local changes with `git status --porcelain`.
4. Runs `git pull origin` only when the branch matches and the working tree is
   clean.
5. Records the result in the update history database.
6. Stores the pre-pull and post-pull revisions when available.
7. Runs enabled plugins from the saved update record.

Common skip cases:

```text
current branch feature/foo is not default branch main
repository has uncommitted changes
```

This avoids pulling into feature branches or overwriting active local work.

## Notifications

On macOS, `autogitpull` sends notifications for registration actions. Pull
notifications are handled by the built-in Notifications plugin. It sends
notifications for changed daemon/web/TUI pulls, and also for successful manual
web/TUI pulls even when there are no new changes.
Remote-tracking branch updates reported by `git pull` can also trigger
notifications even when the local `HEAD` did not change. Those events are not
treated as AI-summary code changes because there is no local revision range to
summarize.

On Linux, pull notifications use the desktop notification stack. Clickable
change links are attempted through `notify-send` actions plus `xdg-open`;
otherwise `autogitpull` falls back to a normal `beeep` notification through
session D-Bus, `notify-send`, or `kdialog`. A graphical user session is required.
When the Linux service is installed or started, it imports the current desktop
environment into `systemd --user` so notifications and `xdg-open` can see it.

On macOS, by default it looks for:

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

GitHub Actions builds release binaries:

```text
autogitpull-macos-arm64
autogitpull-linux-amd64
autogitpull-linux-arm64
```

The installers expect the release assets to use those exact names.

<test commit>
