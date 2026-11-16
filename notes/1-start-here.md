# 1. Start here

Written 2026-11-16 by the architect of the first build, for whoever picks the
project up without the agent sessions that built it.

Peter, named throughout these notes, owns the repository. He makes the product
decisions, merges pull requests, and decides what is pushed.

## What this is

`todos` is a Go CLI and terminal UI for GitHub issues, pull requests
(read-only) and one project board. GitHub is the only source of truth. The
program holds no data of its own beyond a remembered board choice, a quick-add
draft and a disposable read cache.

## Where it stands

- **v1 is merged to `master`** through pull request 83, as the single commit
  `a33a677`.
- **The rename cleanup followed** in pull request 84 (`3248c4b`), after the
  repository was renamed from `pkieltyka/playg` to `pkieltyka/todos`.
- **The read-through cache is on `master`** as phases 10 and 11 (`94eb0de`,
  `2dc5503`).
- **All of it is on GitHub.** When this was written, `origin/master` was level
  with the local `master`.
- **No release has been made.** There is no tag. A build from a checkout
  reports Go's version control stamp as its version; a release build sets it
  with `-ldflags '-X=main.version=VERSION'` (README, "Requirements and
  installation").
- **All eleven phases of the plan are done.** Nothing is in progress.
- **Open ends** are listed in `TODO.md` under "Known limits and follow-ups",
  and at the end of notes 3, 4 and 5.

## What to read, in order

| Read | For |
|---|---|
| `README.md` | What it does, how to install and run it |
| `specs/projects/todos/project_overview.md` | Why it exists, in one page |
| `specs/projects/todos/functional_spec.md` | Every behaviour, command, flag, error and exit code |
| `specs/projects/todos/ui_design.md` | Every screen and key of the TUI |
| `specs/projects/todos/architecture.md` | The packages, who owns which state, the service seam, the decisions |
| `specs/projects/todos/components/<package>.md` | The detailed design of the package you are about to change |
| `specs/projects/todos/components/cache.md` | The cache: what it promises, and the alternatives it rejected (section 12) |
| `notes/2-how-the-work-is-done.md` | The process, the checks, and the rules for testing against the real GitHub |
| `notes/3` to `notes/5` | What building it taught us that the specs do not say |
| `TODO.md` | Open ends, and the list of decisions and facts |

## Which document wins

1. The functional spec says what the program does.
2. The architecture says how, and overrules a component document where the two
   disagree. Section 8 does so explicitly for the test fake.
3. The component documents were updated in the same commit as the code, phase
   by phase. That did not catch everything: writing these notes turned up
   three stale sections (in `tui.md`, `github.md` and `service.md`), corrected
   the same day. Expect a few more.
4. Where a document and the code still disagree, check the tests. If the tests
   pin the code's behaviour, the document is stale: fix the document.

Two decision tables exist so that rejected ideas are not proposed again:
`architecture.md` section 9 and `components/cache.md` section 12. Read them
before adding a lock, a pacer, a wrapper or a second cache.

## Decisions by Peter that bind future work

The full list is in `TODO.md` under "Decisions and facts". These are the ones
that shape design:

- GitHub is the source of truth, and other people change it while we run.
- One repository and one board per run.
- The board is always live. Its cards are never cached.
- An issue list may trail a change made elsewhere by a few seconds.
- `todos rm` deletes permanently and needs `--yes`.
- Pull requests are read-only.
- No login flow of our own. We reuse the credentials of `gh` through go-gh, in
  process, and never shell out to `gh`.

## What is not in git

A fresh clone lacks these. None is needed to continue, but each explains a
reference you will meet.

| Thing | Where it is | What it holds |
|---|---|---|
| The per-phase history of v1 | Local branch `todos-v1`, 18 commits, deleted on GitHub after the merge | Phases 1 to 9 as separate commits. The hashes that `TODO.md` quotes for those phases (`30edb85` to `a646c89`) are on this branch only. |
| `reviews/` | Local folder, ignored by git | About two dozen review reports, one or more for most phases. Their findings were fixed; the ones accepted and left open are carried into notes 4 and 5. |
| `TEAM.md` | Local file, ignored by git | The briefing for the five agents. Note 2 has what matters from it. |
| `.specs_skill_state/` | Local folder, ignored by git | Which spec project is active, for the spec skill. |

To keep the phase history, if Peter wants it kept, tag the local branch and
push the tag: `git tag v1-phases todos-v1 && git push origin v1-phases`.

The branch `rename-cleanup` still exists locally and on GitHub. It was merged
as pull request 84 and can be deleted.

## Layout in one screen

```
main.go, version.go        the binary; main.go sits at the root so go install names it todos
internal/domain            types and the error type; imports nothing of ours
internal/github            the only package that talks to GitHub (GraphQL, plus one REST call, through go-gh)
internal/service           the rules: resolution, validation, multi-step changes, cache policy
internal/service/servicetest   the in-memory fake that cli and tui tests share
internal/cache             the file store under the cache; storage only, no policy
internal/quickadd          the pure parser for punch lists
internal/config            the remembered board and the quick-add draft
internal/cli               cobra commands, text and JSON output, exit codes
internal/tui               the Bubble Tea v2 program
internal/markdown          sanitising and rendering Markdown
skills/todos/SKILL.md      the skill an agent loads to drive the CLI
```

Three tests at the module root hold the structure together:

- **`TestImportRules`:** fails when a package imports one it must not.
- **`TestSkillMatchesCLI`:** fails when the skill names a command or flag the
  binary does not have, or the binary gains a command without an example.
- **`TestMain_EmbedAndRootBinary`:** keeps the skill embedded in the binary
  identical to `skills/todos/SKILL.md`.

## First hour

```sh
make check        # gofmt, vet, race tests; needs no network and no credentials
make build        # ./bin/todos
./bin/todos doctor
./bin/todos       # the TUI, against the repository of the current git remote
```

- **Credentials:** `todos doctor` tells you whether yours can see the
  repository and its board. Boards need the `project` scope for changes and
  `read:project` for reads; `gh auth refresh -s project` covers both.
- **Installing with `go install`:** while the repository is private it needs
  `GOPRIVATE=github.com/pkieltyka/todos` (README).
- **The cache** is at `$XDG_CACHE_HOME/todos/v1`, by default
  `~/.cache/todos/v1`. Deleting the directory is always safe. `--fresh` makes
  one command read GitHub, and `TODOS_CACHE=off` turns the files off.
- **There is no debug flag and no log file,** by decision (`architecture.md`
  section 7, point 13). To look into a live problem, use `todos doctor --json`,
  the `as_of` field in the JSON of `list`, `view` and `board`, and `gh` or the
  GitHub page for what GitHub itself holds.
