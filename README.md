# todos

todos is a Go CLI and terminal UI for GitHub issues and one project board.
It creates, edits, assigns, labels, comments on and closes issues, and reads pull requests.
GitHub is the source of truth; todos uses the credentials already configured for gh.

## Requirements and installation

Linux or macOS, Go 1.27.3 or newer to build, and GitHub credentials from
`gh auth login` or a classic personal access token in `GH_TOKEN` /
`GITHUB_TOKEN`. Private repositories need the
`repo` scope; boards need `project` for changes (`read:project` for reads).
Use `gh auth refresh -s project` to add board access when needed.
A terminal of at least 65×24 is required for the TUI. Below 80 columns the
TUI uses a compact layout that keeps the view tabs and primary shortcuts visible.

Install with Go. While the repository is private, Go must be told not to
look it up in the public checksum database, and git needs access to it:

```sh
GOPRIVATE=github.com/pkieltyka/todos go install github.com/pkieltyka/todos@latest
```

Once the repository is public, plain `go install github.com/pkieltyka/todos@latest`
is enough.

Or build from a checkout:

```sh
git clone https://github.com/pkieltyka/todos.git
cd todos
make build
./bin/todos
```

`todos version --json` reports the installed module version, or Go's version
control stamp for a build in a Git checkout (including `+dirty` when applicable).
A release `-ldflags '-X=main.version=VERSION'` override takes precedence;
`dev` is only the fallback when no usable build metadata is available.

Select a repository with `--repo OWNER/REPO`, then `GH_REPO`, or let gh choose
the git remote. Use `--project OWNER/NUMBER` for a specific board; otherwise
todos uses the remembered choice or linked project. `todos doctor --json`
checks credentials, repository, board and config without changing GitHub.

Metadata is shared across invocations in a disposable private cache at
`$XDG_CACHE_HOME/todos/v1` (default `~/.cache/todos/v1`). Session, labels,
assignees and board structure expire five minutes after their GitHub read.
The Board view and `todos board` always read live fields, Status options and cards. `--fresh` bypasses cached issue pages and ignores metadata
read before the command began, while reusing new metadata within the command. `TODOS_CACHE=off` disables cache files and keeps the
in-process memo. Board cards, issue/PR details
and comments are always read from GitHub. `todos doctor` reports cache usability
without creating or writing cache files; deleting the cache directory is safe.
Issue lists can trail changes made elsewhere by a few seconds and are never
reused past ten minutes. `todos view`, `todos board` and `--fresh` always read
GitHub live.
For JSON results about the same issue, the later `as_of` is newer; when equal,
prefer `todos view`. `as_of` is UTC RFC3339 with millisecond precision.

## TUI

Run `todos`, or `todos tui issues`, `todos tui prs`, `todos tui board`.

| Keys | Action |
|---|---|
| `1` / `2` / `3`, `tab` | Switch Issues / PRs / Board |
| `j` / `k`, arrows, `enter`, `esc` | Navigate, open, go back |
| `/`, `m`, `s`, `r` | Filter, mine, state, refresh |
| `n`, `e`, `a`, `L`, `c` | Create, edit, assignees, labels, comment |
| `x`, `X`, `M`, `<` / `>` | Close/reopen, not planned, board status, adjacent status |
| `A` | Quick add |
| `o`, `y`, `?`, `q` | Browser, copy URL, help, quit |

Forms support `ctrl+r` Markdown preview, `ctrl+o` external editor (`VISUAL`,
else `EDITOR`), and `ctrl+s` or `alt+s` to save. PRs are read-only. `--no-color` or `NO_COLOR`
disables colour; `TODOS_THEME=light|dark` overrides background detection.

## CLI

Use `--json` for agents and scripts. Without it, terminal output uses tables
and rendered Markdown; pipes receive plain tab-separated lists and raw Markdown.

```sh
todos list --mine --state open --json
todos view 12 --json
todos add 'Fix login redirect' --body 'Steps to reproduce' --label bug --json
todos move 12 'In progress' --json
todos close 12 --comment 'Verified the fix' --json
todos board list --json
todos board use 1 --json
todos pr list --state all --json
todos help
```

Prefer close. Permanent deletion requires both an explicit user request and
`todos rm ISSUE --yes`. Errors explain the kind and a fix; uncertain writes
must be checked on GitHub before retrying. Partial changes are retained.

## Quick add

In the TUI, press `A`, write a punch list, then `ctrl+s` or `alt+s` to preview. Choose
shared assignees, labels and board status; `d` switches checked items between
skip and file-as-closed. Press Enter to create. Drafts are saved automatically;
a may-exist marker requires checking GitHub before resending.

On the CLI, use one title per line, optionally with bullets, checkboxes or
numbers; indent body lines by two spaces or a tab. There is a 50-item cap.

```sh
todos quick-add --file tasks.md --checked skip --dry-run --json
todos quick-add --file tasks.md --checked skip --json
printf '%s\n' '- Fix login' '- [x] Update docs' | todos quick-add --file - --checked close --json
```

Checked items skip by default; close files them as completed without requesting
board placement. GitHub project workflows may independently add those issues
or change their status afterward. A batch status applies only to open items. Creation stops at
the first failure; retain the accounting document and resend only remaining
items after checking uncertain outcomes. CLI quick-add creates no draft file.

Config lives at `$XDG_CONFIG_HOME/todos/config.yml` (default `~/.config/todos/`).
TUI drafts live under `$XDG_STATE_HOME/todos/drafts/` (default
`~/.local/state/todos/drafts/`).

## Give the skill to an agent

```sh
todos skill > SKILL.md
```

Copy that file into the agent's skills directory, for example
`~/.codex/skills/todos/SKILL.md`. It is embedded in the binary and documents
all commands, JSON shapes, exit codes, and partial/uncertain recovery.

## Development

- `make` or `make build`: build `./bin/todos`.
- `make test`: run tests without the race detector.
- `make check`: check formatting, vet and race tests without GitHub access.
- `make install`: install with `go install .`.
- `make clean`: remove `./bin/`.
- `make help`: print the available targets.

Opt-in live tests are read-only and require an explicit repository:

```sh
TODOS_LIVE_TESTS=1 TODOS_LIVE_REPO=pkieltyka/todos go test ./internal/cli -run '^TestLive' -count=1
```

Set `TODOS_LIVE_PROJECT=OWNER/NUMBER` if the repository's board is ambiguous.

## License

This project is licensed under the [MIT License](LICENSE).
Copyright © 2026 Peter Kieltyka.
