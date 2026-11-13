# todos — punchlist

Owner of this file: @pm. Only @pm edits it; ask @pm to add or change items.

## Now

- [x] Spec step 1: project overview — @cto — done, approved by Peter
- [x] Spec step 2: functional spec — @cto — done, approved by Peter
- [x] Peter to grant the gh token the project scope: `gh auth refresh -s project` — Peter — done (gh had only gist, read:org, repo, workflow before)
- [x] Decide how todos reaches GitHub: gh binary vs in-process GitHub API via go-gh with gh credentials — Peter — done; in-process go-gh chosen, see Decisions and facts
- [x] Spec step 3: UI design — @cto with @frontend-dev — done, approved by Peter
- [ ] Spec step 4: architecture — @cto — `specs/projects/todos/architecture.md` written with rulings on both proposals; @lead-dev writing component docs `domain.md`, `github.md`, `service.md`, `cli.md` and @frontend-dev writing `tui.md`, `markdown.md`, all under `specs/projects/todos/components/`; then @cto reviews them and writes the implementation plan
- [x] Contradiction check of the three approved spec documents — @reviewer — done; ten findings, all fixed in the specs

## Next

- [ ] Spec step 6: implementation plan — @cto
- [ ] Implementation — @lead-dev (Go core) and @frontend-dev (TUI), review by @reviewer

## Decisions and facts

- Repo `pkieltyka/playg` exists on GitHub, issues enabled, 0 issues.
- gh version: 2.103.3.
- UI libraries: `charm.land/bubbletea/v2` v2.1.1, `charm.land/lipgloss/v2` v2.1.0, `charm.land/bubbles/v2` v2.2.1.
- @cto is the only one who commits.
- Decisions from Peter:
  - One repo and one board per run.
  - The board shows issue cards only and moves them between Status columns.
  - Pull requests are a separate read-only view.
  - Comments can be viewed and added.
  - Flat verb commands: `todos list`, `todos add`, `todos assign`, `todos close`, `todos rm`, `todos move`.
  - `todos rm` is a permanent delete and requires `--yes`.
  - The repo will be renamed to `todos`; the Go module path is now `github.com/pkieltyka/todos`.
  - todos calls the GitHub API in-process through the go-gh library and reuses gh credentials; no login flow of our own.
  - Quick add turns a punch list into one issue per item, in the TUI and as the command `todos quick-add`, capped at 50 items, created one at a time.
  - Markdown is rendered with glamour v2; bodies are edited as raw Markdown with a built-in editor, a preview toggle, and a hand-off to the user editor from `VISUAL`/`EDITOR`; no in-app vim mode in v1.
  - In quick add, done items written as checked boxes are skipped by default, with a per-batch choice to file them as closed; they are never placed on the board.
  - `q` quits and `esc` goes back; the three views are on keys `1`, `2`, `3`; quick add is on `A`.
  - `TODOS_THEME=light|dark` overrides background detection.
  - `main.go` sits at the module root so `go install github.com/pkieltyka/todos@latest` gives a binary named `todos`; packages live under `internal/`: `cli`, `tui`, `markdown`, `service`, `quickadd`, `config`, `github`, `domain`; the CLI framework is cobra; tests never need network or credentials.
  - The repo already has a linked GitHub Project, number 1, titled "playg board", with Status columns Backlog, Sprint, In progress, In review, Done — no new board needs to be created.
  - Everything goes through GraphQL via go-gh.
  - Repository resolution: `--repo`, then `GH_REPO`, then the git remote.
  - Implementation ownership: @lead-dev for `domain`, `github`, `service`, `quickadd`, `config`, `cli`; @frontend-dev for `tui` and `markdown`.
  - Check command for every phase: `gofmt -l .` and `go vet ./...` and `go test -race ./...`
