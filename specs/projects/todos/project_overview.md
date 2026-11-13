---
status: complete
---

# todos

We are building a GitHub CLI in Go, built on top of the local `gh` CLI: a CLI
wrapper on a CLI. The project is called "todos" and the binary is `todos` as
well.

It is a TUI for GitHub issues and boards, and we will use Bubble Tea v2 for the
UI. We want to list issues, create, delete, assign, etc., and also have a
"project" board too. The repo here is on GitHub, and we can use the `gh` CLI to
manage it and create issues. Our program is a TUI to sync and manage GitHub
remote tickets, so we can see them, but people online on GitHub can see them
too.

We also need a view on PRs, as a separate section, so we can view PRs and
their details and state, etc.

If we run just `todos` with no args, it launches the TUI for us, which is human
readable and human controlled.

`todos` also supports CLI arguments to list issues and add / remove / reassign
them, all from the CLI.

`todos` should have a skill file that we can share with agents, so agents can
interact with this program too, via the skill plus the CLI.

It uses `gh` behind the scenes.

## Decisions since the first description

- **How it reaches GitHub.** `todos` calls the GitHub API in-process through
  `go-gh`, the library published by the `gh` team, instead of running the `gh`
  binary for every operation. It still builds on `gh`: it reuses the
  credentials and host settings `gh` already has, and users log in and add
  permissions with `gh`. We do not build our own login.
- **One repository and one board per run.** The repository is the one in the
  current directory, or `--repo`.
- **Name.** The repository will be renamed to `todos`. The Go module path is
  `github.com/pkieltyka/todos`.
- **Markdown.** Issues are Markdown, so rendering it well in the TUI is a hard
  requirement, and bodies can be edited as Markdown.
- **Quick add.** A fast way to type a punch list and have each item become an
  issue.

## How we are building it

- @cto (the orchestrator) is the lead architect and writes the spec, running
  it by @lead-dev and @frontend-dev for feedback as it takes shape.
- The agent team implements: @lead-dev (Go core), @frontend-dev (TUI),
  @reviewer (first-pass review), @pm (`TODO.md` punchlist). @cto does the final
  review and commits.
