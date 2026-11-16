# todos — punchlist

Owner of this file: @pm. Only @pm edits it; ask @pm to add or change items.

## Now

The build is complete; nothing is in progress.

## Known limits and follow-ups

- [ ] Long URLs in rendered Markdown break at a hyphen
- [ ] A board's own automations have the last word: on this board, a new issue created with No status usually ends up in Backlog
- [ ] Detection of the board auto-add rejection depends on the wording of GitHub's error message
- [ ] While the repository is private, `go install github.com/pkieltyka/todos@latest` needs `GOPRIVATE=github.com/pkieltyka/todos`; tried and working that way on 2026-11-14

## Done

- [x] Pull request 83 (todos v1), opened by @lead-dev, merged to `master` by Peter on 2026-11-14 as `a33a677`; CI passed on Linux and macOS
- [x] Pull request views ran on real data: while PR 83 was open, @lead-dev ran `todos pr list` and `todos pr view` with and without `--json`, and all four showed correct output
- [x] Closed issues list in the TUI ordered by most recently closed — commit `a646c89`, reviewed by @reviewer with no findings
- [x] Phase 9: Skill and release readiness — @lead-dev + @frontend-dev — done; all nine phases are committed on branch `todos-v1`, the last code commit being `c504e3f`
- [x] No status fix — committed as `c504e3f`
- [x] Phase 9 TUI half, with the board focus fix — @frontend-dev — committed as `c8b6b23`
- [x] Board auto-add race fix — committed as `0512fbc`
- [x] Phase 7: Quick add, core — @lead-dev — done; reviewed by @reviewer with no defects, committed as `a28c1e7`
- [x] Phase 8: Quick add, TUI — @frontend-dev — done; reviewed by @reviewer, committed as `db13c8f`
- [x] Phase 9 core half: agent skill, README, version and cleanup — @lead-dev — done; reviewed with no defects, committed as `38c4888`
- [x] TUI write actions dogfooded against the real repository on throwaway issues 20 and 21, both deleted; during the run issue 6 was moved by mistake because of a focus bug, now fixed; issues 6–10 were then put into their true state with the `todos` binary
- [x] Phase 5: Changes, core — @lead-dev — done; reviewed by @reviewer with no defects, committed as `14c6111`; `todos` can now create and change issues and move cards, and it was used to close issues 5, 11 and 12 and move issue 6 to In progress on the real board
- [x] Phase 6: Changes, TUI — @frontend-dev — done; reviewed by @reviewer, two findings fixed, committed as `2410deb`
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

- Repo `pkieltyka/todos` (https://github.com/pkieltyka/todos) exists on GitHub, issues enabled; Peter renamed it from `pkieltyka/playg`.
- gh version: 2.103.3.
- UI libraries: `charm.land/bubbletea/v2` v2.1.1, `charm.land/lipgloss/v2` v2.1.0, `charm.land/bubbles/v2` v2.2.1.
- @cto is the only one who commits.
- Peter asked that the team does not ask him questions; @cto decides and reports.
- v1 is merged to `master` (PR 83, `a33a677`), and so is the rename cleanup with the detail-screen fix (PR 84, `3248c4b`); work now happens on `master`; the team pushes only when Peter asks.
- Each phase: developer, `make check`, @reviewer, fixes, @cto review and commit.
- When two phases finish together, the developers cross-review each other in addition to @reviewer.
- The shared test fake is a direct in-memory implementation in `internal/service/servicetest`.
- Reviews of code go into the `reviews/` folder, which git ignores.
- Commits are made by path, so one track's unfinished work never lands in the other's commit.
- 68 throwaway issues, numbers 15 to 82, were created and deleted during live testing; issues 1 to 14 are the real backlog, and issue 10 was closed last.
- The project board has a GitHub workflow that closes an issue when its card moves to Done — expected behaviour.
- Removing an assignee or label is judged against the issue itself, so a person who left the organisation can still be unassigned.
- All nine phases are committed on branch `todos-v1`; the last code commit is `c504e3f`.
- A local build reports the version control stamp as its version.
- @lead-dev's session was compacted at the Phase 4 boundary.
- Product and design decisions (Peter and @cto):
  - One repo and one board per run.
  - The board shows issue cards only and moves them between Status columns.
  - Pull requests are a separate read-only view.
  - Comments can be viewed and added.
  - Flat verb commands: `todos list`, `todos add`, `todos assign`, `todos close`, `todos rm`, `todos move`.
  - `todos rm` is a permanent delete and requires `--yes`.
  - The repo is renamed to `pkieltyka/todos`; the Go module path is `github.com/pkieltyka/todos`.
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
