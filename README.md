# Whippy

A menu bar app that watches your GitHub pull requests and workflow runs, and
tells you when they need you.

- **Menu bar panel:** what needs attention first (failed checks, conflicts,
  changes requested, failed runs), then what is running, waiting, and open.
  The icon's title shows the counts, such as `✕2 ●1`.
- **Dashboard:** your open pull requests by repository, your workflow runs of
  the last day, and settings. Whippy is in the Dock while it is open.
- **Notifications:** checks failed or passed, runs finished, reviews, merge
  conflicts, merges.
- **Read-only:** a click opens the pull request or run on GitHub.

It finds the pull requests you opened and the GitHub Actions runs you
triggered, in every repository your account can see. Other repositories to
watch for runs can be added in Settings.

## Requirements

- macOS. The code is portable, but only macOS has been tried.
- The [GitHub CLI](https://cli.github.com), signed in: `brew install gh`, then
  `gh auth login`. Whippy has no sign-in of its own: it asks `gh` for its
  token, keeps it in memory, and shows how to fix `gh` when it is missing or
  signed out. `GH_TOKEN` or `GITHUB_TOKEN` in the environment take its place.
- Go 1.27.3 or later to build; an older Go downloads it by itself.

A fine-grained personal access token that may not read check runs still
works: pull requests then show their workflow runs in place of their checks.

## Run and build

```sh
go tool mygo dev      # run, rebuilding on changes
go tool mygo build    # package into build/
```

## Develop

```sh
CGO_ENABLED=0 go test ./...   # tests, the views included
go tool mygo vet .            # MyGo's checks
go run ./cmd/probe            # one real poll, printed
go run ./cmd/icons            # redraw the icons from resources/whip.svg
SHOTS=/tmp/shots go test -run TestShots .   # draw the views to PNG files
```

| Path | What it holds |
|---|---|
| `main.go` | windows, tray icon, notifications, wiring |
| `app.go`, `ui_*.go` | state and views |
| `internal/auth` | finding `gh` and its token |
| `internal/github` | the GitHub API client |
| `internal/tracker` | polling, snapshots, and what changed between them |
| `internal/settings` | the settings file |

Whippy polls every 60 seconds, every 30 while something runs, and asks quiet
repositories for their runs every 5 minutes. Settings are kept in
`~/Library/Application Support/whippy/settings.json`.

Built with [MyGo](https://mygo.egoist.dev).
