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
- Sends macOS notifications through the built-in Notifications plugin.
- Can store plugin output, including prepared AI-summary context, per update.
- Can be installed as a macOS `launchd` service.

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
service <command>         Manage the macOS launchd service.
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

## macOS Service

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

It uses launchd label:

```text
com.blankstatic.autogitpull
```

Logs:

```sh
cat /tmp/com.blankstatic.autogitpull.log
cat /tmp/com.blankstatic.autogitpull.error.log
```

## Dashboard

When `autogitpull daemon` or the macOS service is running, the dashboard is
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

The built-in AI Summary plugin is disabled by default. When enabled, it uses the
saved `before_rev` and `after_rev` range to collect `git log --stat` and
`git diff --stat` context, then stores the generated summary in plugin results.
Configure a provider name, API key, model, API type, and API URL on the plugin
settings page. Any OpenAI-compatible provider can use either Responses API or
Chat Completions by changing the API type and URL. Use the AI Summary `Test`
button on the plugin settings page to send `hello` and verify the configured
provider response. API URL, API key, and model must be filled before the plugin
will call a provider. Each generated summary is stored as a separate plugin
result, so a change can have multiple AI summaries.
It can be run manually from a repo page, scoped to selected repositories through
the generic repo plugin controls, or run globally for all changed updates from
the plugin settings page. The plugin settings page lets every plugin switch
between all repositories and selected repositories, and shows the selected
repository list for repo-scoped plugins.

Each recorded update has a change details page at `/update?id=<id>`. It shows
the pull output or error, all AI summaries for that update, and a button to
generate another summary. Pull notifications deep-link to this change page, and
notification sent/skipped/error diagnostics are shown in that page's plugin
results. Plugin results are shown newest first.

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
