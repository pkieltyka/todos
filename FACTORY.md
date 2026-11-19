# todos factory

## Project

todos is a Go CLI and Bubble Tea terminal UI for people who manage GitHub issues and a project board from the terminal. GitHub is the source of truth. The app uses go-gh and the credentials configured for gh.

The lead developer owns the core, CLI, and agent skill. The frontend developer owns the TUI and Markdown rendering. Agree on changes to the service interface or domain types before either track implements them.

## Tasks

The project manager maintains `TODO.md`. The operator marks an item ready by approving its scope and acceptance criteria. Record the owner, verification, and outcome in the tracker. The known limits are backlog items.

## Workflow

Use the shared checkout on `master`. Assign one writing task at a time. Run `make check` before review. The reviewer records findings with file and line references in `reviews/`. The operator reviews the fixes and integrates locally after verification. Pushes and pull requests require Peter's request.

Use the in-memory service fake and terminal golden tests for routine verification. The repository's issues and project board are live data. See `notes/2-how-the-work-is-done.md` for the project's verification procedure.

## Notes

Keep the existing numbered notes in `notes/` and search them with `rg`. Add durable lessons that belong outside the specs to the next numbered note.

## Specs

Keep designs in `specs/projects/todos/`. For a feature, update the functional spec, architecture, relevant component spec, and implementation plan before coding. Locate the relevant documents with `rg --files specs/projects/todos`.
