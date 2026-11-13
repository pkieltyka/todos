# todos — punchlist

Owner of this file: @pm. Only @pm edits it; ask @pm to add or change items.

## Now

- [ ] Phase 5: Changes, core — @lead-dev — in progress
- [ ] Phase 6: Changes, TUI — @frontend-dev — in progress (parallel with Phase 5)
- [ ] Phase 7: Quick add, core — @lead-dev — not started (parallel with Phase 8)
- [ ] Phase 8: Quick add, TUI — @frontend-dev — not started
- [ ] Phase 9: Skill and release readiness — @lead-dev + @frontend-dev — not started

## Polish for Phase 9

- [ ] A wrapped block quote can leave a short middle line
- [ ] Long URLs break at a hyphen
- [ ] A long label truncates at 80 columns in the issue list
- [ ] Blank rows under the more line on the board
- [ ] The browser opener may print over the TUI

## Done

- [x] Phase 4: First dogfood — @lead-dev + @frontend-dev — done; reviewed by @reviewer with one finding fixed, committed as `9fb1f65`; `todos` with no arguments now opens the real TUI against GitHub, and issues 1–14 exist on `pkieltyka/playg` and its board
- [x] Phase 2: Read path, core — @lead-dev — done; reviewed by @reviewer and @frontend-dev, 13 findings fixed, committed as `2c49813`
- [x] Phase 3: Read-only TUI — @frontend-dev — done; cross-reviewed by @lead-dev, 15 findings fixed, committed as `8771f7c`
- [x] Phase 1: Foundation — @lead-dev — done; reviewed by @reviewer and @cto, six review fixes applied, committed as `30edb85`
- [x] Component docs cross-checked against the architecture — @reviewer — six findings, all fixed
- [x] Spec steps 1–4 + implementation plan — @cto — overview, functional spec, UI design, architecture with six component docs, implementation plan: all approved and committed on branch `todos-v1` as `fee30a2`
- [x] Contradiction check of the approved spec documents — @reviewer — ten findings, all fixed in the specs
- [x] Peter granted the gh token the project scope (`gh auth refresh -s project`)
- [x] Decided how todos reaches GitHub — in-process GitHub API via go-gh with gh credentials

## Decisions and facts

- Repo `pkieltyka/playg` exists on GitHub, issues enabled, 0 issues.
- gh version: 2.103.3.
- UI libraries: `charm.land/bubbletea/v2` v2.1.1, `charm.land/lipgloss/v2` v2.1.0, `charm.land/bubbles/v2` v2.2.1.
- @cto is the only one who commits.
- Peter asked that the team does not ask him questions; @cto decides and reports.
- Work happens on branch `todos-v1`; nothing is pushed.
- Each phase: developer, `make check`, @reviewer, fixes, @cto review and commit.
- The shared test fake is a direct in-memory implementation in `internal/service/servicetest`.
- Reviews of code go into the `reviews/` folder, which git ignores.
- Commits are made by path, so one track's unfinished work never lands in the other's commit.
- Our backlog now also lives as GitHub issues 1–14 on `pkieltyka/playg` and its project board, created for dogfooding.
- Nothing has been pushed, so the pull request views can only be checked against test data for now.
- When two phases finish together, the developers cross-review each other in addition to @reviewer.
- The project board has a GitHub workflow that closes an issue when its card moves to Done — expected behaviour.
- At the end of Phase 5, @lead-dev uses the `todos` binary itself to close issues 5, 11 and 12 and move issue 6 to In progress.
- @lead-dev's session was compacted at the Phase 4 boundary.
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
