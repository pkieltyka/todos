---
status: complete
---

# Implementation Plan: todos

Two tracks build in parallel after Phase 1. The **core** track (@lead-dev)
owns `domain`, `github`, `service`, `service/servicetest`, `quickadd`,
`config`, `cli`, `main.go`, `go.mod` and the skill file. The **TUI** track
(@frontend-dev) owns `tui` and `markdown`, and builds against the fake service
from Phase 1 until the real one is wired in at Phase 4. Nobody edits files
outside their track without asking @cto, and nobody but the core track touches
`go.mod`.

Every phase ends the same way: the developer runs
`gofmt -l . && go vet ./... && go test -race ./...`, @reviewer reviews the
diff, the developer fixes what is found, @cto reviews and commits. @pm updates
`TODO.md` at each step. What to build is in `architecture.md` and
`components/`; this file is only the order.

## Phases

- [x] **Phase 1: Foundation** (core). `go.mod` with every pinned dependency,
  held by `internal/tools/deps.go`. `domain` types and the error type. The
  `Service`, `Backend` and `SettingsStore` interfaces. `servicetest.Fake` as a
  direct in-memory implementation: every read, every single-issue change and
  the board changes, with fixtures, gates and the call log; its two quick-add
  methods return an `internal` error until Phase 5. `main.go` and the cobra
  root with `version`, `help` and a placeholder `skill`. `TestImportRules`.
  The check script and CI workflow.
- [x] **Phase 2: Read path, core** (core; parallel with Phase 3). The `github`
  client: credentials, documents, mapping, paging, error classification, rate
  tracking. `config` for the remembered board. Service reads: session,
  repository and board resolution, issues, pull requests, board, labels,
  assignees, doctor. CLI read commands with text, piped and JSON output.
- [x] **Phase 3: Read-only TUI** (TUI; parallel with Phase 2). `markdown`.
  Frame, theme, keymap, store with progressive loading and refresh. Issue list
  and detail, pull request list and detail, board, board setup states, help,
  too-small state. Golden renders.
- [x] **Phase 4: First dogfood** (both). Wire the real service into `todos`
  and `todos tui`. Run it against this repository and its board. Fix what the
  real data breaks.
- [x] **Phase 5: Changes, core** (core; parallel with Phase 6). Mutations in
  `github` and `service`: create, edit, assignees, labels, comment, close and
  reopen, delete, board add / move / remove including archived cards, and
  `partial` results. CLI write commands. Also the pure `quickadd` parser and
  the fake's quick-add methods, so Phase 8 can start on time.
- [x] **Phase 6: Changes, TUI** (TUI; parallel with Phase 5). The
  pending-change engine. Pickers. Create, edit and comment forms with the
  editor, preview and external-editor hand-off. Edit conflict check. Delete
  confirm. Toasts.
- [x] **Phase 7: Quick add, core** (core; parallel with Phase 8). The real
  executor with pacing and stop rules, the draft store, `todos quick-add`.
- [x] **Phase 8: Quick add, TUI** (TUI; parallel with Phase 7). Write, preview,
  progress and stopped steps; draft persistence and recovery.
- [x] **Phase 9: Skill and release readiness** (both). `SKILL.md` and
  `TestSkillMatchesCLI`; README; opt-in live tests; a full dogfood pass of
  every command and every TUI action against the real repository; remove
  `internal/tools/deps.go`; polish.
- [ ] **Phase 10: Cache, structure** (core, with a small TUI change).
  `internal/cache`, the file store. `CacheStore` and `CredentialKey`. The
  service's metadata memo made persistent: session, labels and users for 5
  minutes, the board for 1. `WithFresh`, the `--fresh` flag, `TODOS_CACHE=off`,
  the `doctor` cache check; the TUI marks a manual refresh fresh. Measured
  cold and warm on the real repository. Design: `components/cache.md`.
- [ ] **Phase 11: Cache, content** (core, with a small TUI change).
  `ChangeToken` and its document. Probe-validated issue list pages and board
  pages, the quiet rule, the 10-minute limit, write epochs bumped by every
  change. `AsOf` on pages and boards, used by the TUI store. Measured cold and
  warm on the real repository.
