---
name: todos
description: Manage GitHub issues and a project board, read pull requests, and file punch lists through the todos CLI.
---

# todos

Use todos for GitHub issue triage, editing, assignment, labels, comments,
closing/reopening, board placement, and quick-add punch lists. Pull requests
are read-only. All commands are noninteractive except the TUI.

## Scope and conduct

- One repository and one board per invocation. Resolve the repository with
  `--repo OWNER/REPO`, then `GH_REPO`, then the git remote selected by gh.
- Choose the board with `--project NUMBER`, `OWNER/NUMBER`, or a project URL;
  otherwise use the remembered board, then the repository's linked project.
  `todos board use` remembers a choice locally. No command creates a board,
  status option, label, or pull request.
- Use existing gh credentials or a classic token in `GH_TOKEN` / `GITHUB_TOKEN`.
  Private repositories need repo scope; board changes need project scope.
- Always pass `--json` for agent operations. Exceptions: help and skill print
  text; the TUI rejects JSON and is for humans, not automation.
- Act within the user's requested scope. Prefer close. Never run `todos rm`
  unless the user explicitly requested permanent deletion of that issue.
  `--yes` confirms an authorized deletion; it does not supply authorization.
- When an operation fails, run `todos doctor --json` and inspect its report
  and exit code. Never print tokens or change authentication/permissions on
  the user's behalf.

## Commands and flags

These are examples, not a sequence to execute. Substitute the requested
repository, issue numbers and existing names. Use quotes for multiword
arguments. Mutations act on one issue; quick-add is the batch exception.

```sh
todos list --state open --mine --label bug --limit 20 --json
todos view 12 --json
todos add 'Fix login redirect' --body 'Steps to reproduce' --assign @me --label bug --status 'In progress' --json
todos quick-add --file tasks.md --assign @me --label bug --status Sprint --checked skip --dry-run --json
todos edit 12 --title 'Fix callback redirect' --body-file details.md --json
todos assign 12 octocat --json
todos unassign 12 octocat --json
todos reassign 12 @me --json
todos label 12 bug --json
todos unlabel 12 bug --json
todos comment 12 'Verified the fix' --json
todos close 12 --reason completed --comment 'Verified the fix' --json
todos reopen 12 --json
todos move 12 'In progress' --json
todos board --json
todos board list --json
todos board use 1 --json
todos board add 12 --status Sprint --json
todos board remove 12 --json
todos pr list --state all --all --json
todos pr view 23 --json
todos labels --json
todos assignees --json
todos doctor --json
todos version --json
```

Only after an explicit permanent-delete request:

```sh
todos rm 12 --yes --json
```

Discovery and human-only entry points:

```sh
todos skill
todos help add
todos --help
todos
todos tui board
```

- Global flags: `--repo`, `--project`, `--json`, `--no-color`, `--help`,
  `--version`. The version flag also supports JSON.
- ISSUE/PR accepts `12`, `#12`, or a matching repository's GitHub URL.
  A wrong repository or URL kind is invalid_input. LOGIN accepts `@me`.
- List state: open (default), closed, all; PR state also accepts merged.
  `--assignee LOGIN` and `--mine` are exclusive. PR lists have no author flag;
  filter their JSON by author. Repeated `--label` filters mean AND.
- `--search TEXT` is GitHub search, sorted by updated time. Its index can lag.
  Search exposes at most 1000 results; `--all` fails above that ceiling.
- `--limit N` defaults to 50 and excludes `--all`. Without search, `--all`
  follows every available page. Inspect `truncated` rather than assuming
  a default list is complete.
- Add/quick-add flags `--assign` and `--label` repeat. Additions require
  existing assignable users/labels. Removal uses the issue's current members,
  including departed assignees and labels absent from repository metadata;
  absent but repository-known names are no-ops, unknown names are errors.
- `unassign` with no logins removes all; assign adds, reassign replaces all.
  At most 10 assignees. Names are case-insensitive for logins; labels are
  exact; statuses match exactly first, then a unique case-insensitive name.
- `--body TEXT` and `--body-file PATH` exclude each other. A dash reads stdin.
  Edit requires a supplied title/body; an empty body clears it. Comment
  accepts either one nonblank TEXT argument or `--body-file PATH`.
- Close reason is completed (default) or not-planned; JSON uses not_planned.
  Closing a closed issue is a no-op and does not post a closing comment.
- Move requires STATUS or `--no-status` (exclusive) and adds a missing card.
  `board add` without status preserves an existing card; a new card has no
  status. `board remove` leaves the issue intact. Archived cards are restored
  by move/add. Creating an issue with a status requests placement; creating without one
  requests no placement.

## Quick add

Read the proposed list, then run the same file/options first with `--dry-run`.
After checking every planned row, run it without that flag within the user's
request. `--file -` reads stdin; `--checked skip` is the default, while close
creates checked items then closes them as completed.

Each unindented nonblank line becomes a title. One bullet (`-`, `*`, `+`),
checkbox, numbered marker (`1.`/`1)`), or heading prefix is removed. Two spaces
or one tab starts a body continuation; nested Markdown and body blank lines
are retained. `@name` and `#123` are literal text, not inline metadata.
Titles must be nonblank and at most 256 Unicode code points. The cap is 50 items,
including skipped checked items. Shared assignees, labels and status are
validated before writes; status applies only to open items, never done items.

Creation is sequential and paced between writes. First failure stops the
batch; every row remains in the result. A created-but-unclosed or unplaced
issue already exists: finish with close or move, never another create.
An uncertain row may exist: check GitHub before resubmitting it. Never rerun
an entire partially successful input. CLI quick-add has no recovery file;
retain its input and result. TUI drafts are persisted separately.

## JSON contract

Ordinary success: one document on stdout. Ordinary failure: empty stdout and
one `{"error": Error}` document on stderr. Two exceptions:

- doctor always prints its six-check report on stdout; checks 1–3 determine
  its exit code. Board/config failures alone do not fail it.
- quick-add prints its complete accounting document on stdout after execution,
  including first-write rejection or partial failure, with an error on stderr.
  Parse/preflight failures leave stdout empty.

Arrays are always `[]`, never null. Times are UTC RFC3339 strings; optional
ones are null. Authors are login strings or null for deleted accounts.
Only view results contain body/comments. Within a major version, fields may
be added, but are not removed, renamed or retyped. Shapes below use type
notation; `?` means the field is conditionally present.

```text
list:          {issues:[Issue], total:int, truncated:bool}
view:          Issue + {body:string, comments:[Comment], comments_truncated:bool}
change:        {issue:Issue, changed:bool}
comment:       {issue:Issue, comment:Comment}
rm:            {deleted:int}
pr list:       {pull_requests:[PullRequest], total:int, truncated:bool}
pr view:       PullRequest + {body:string, mergeable:"mergeable"|"conflicting"|"unknown",
                              closes:[int], comments:[Comment], comments_truncated:bool}
board:         {project:Project, columns:[{status:string|null, cards:[Card]}],
                hidden_items:int, truncated:bool}
board list:    {projects:[Project], selected:Project|null}
board use:     {project:Project}
labels:        {labels:[{name:string, description:string, color:string}]}
assignees:     {assignees:[{login:string, name:string}]}
doctor:        {ok:bool, checks:[{name:string, ok:bool, detail:string, hint:string}]}
version:       {version:string}
quick-add:     {items:[{line:int, title:string, body:string, checked:bool,
                       result:"created"|"failed"|"uncertain"|"unattempted"|"skipped"|"planned",
                       issue:Issue|null, on_board:bool, closed:bool, error:Error|null}],
                counts:{created:int, failed:int, uncertain:int, unattempted:int,
                        skipped:int, planned:int}}

Issue: {number:int, title:string, state:"open"|"closed",
        state_reason:"completed"|"not_planned"|"reopened"|null, author:login|null,
        assignees:[login], labels:[string], milestone:string|null, comments_count:int,
        created_at:time, updated_at:time, closed_at:time|null, url:string,
        board_status:string|null}
Comment: {id:string, author:login|null, created_at:time, body:string, url:string}
PullRequest: {number:int, title:string, state:"open"|"closed"|"merged", draft:bool,
              author:login|null, head:string, base:string,
              review:"approved"|"changes_requested"|"review_required"|"none",
              checks:"passing"|"failing"|"pending"|"none", assignees:[login], labels:[string],
              created_at:time, updated_at:time, closed_at:time|null, merged_at:time|null,
              url:string}
Project: {number:int, owner:login, title:string, url:string, usable:bool}
Card: {number:int, title:string, state:"open"|"closed", assignees:[login], labels:[string], url:string}
Error: {kind:string, message:string, hint:string, uncertain:bool,
        retry_after_seconds?:int, completed?:[string], cause?:string, issue?:Issue}
```

Change covers add, edit, assign, unassign, reassign, label, unlabel, close,
reopen, move, board add/remove. `changed:false` is successful idempotence.
`board_status:null` conflates off-board, No status and unavailable board;
issue reads still succeed when board access fails. A null column status is
No status. Board scans at most 1000 items; hidden_items counts non-issue,
foreign or inaccessible items within that scan. Comments cap at 500;
closes includes only this repository. Pull requests remain read-only.

Quick-add's planned rows exist only in dry-run; created counts acknowledged
issues even if close/place failed. on_board and closed report known state.
Error retry_after_seconds appears only for rate_limited. completed/cause and
optional issue appear only for partial. Public completed steps are created,
commented, added_to_board. Restoring a card counts as added_to_board.

## Errors and recovery

Read kind/message/hint and the exit code; do not parse human text. Run doctor
on failures. An uncertain write is never an instruction to retry it: verify
its outcome first, especially creates and comments. An absent search result
does not prove an issue was not created, because indexing can lag.

| Exit | Kind | Recovery |
|---|---|---|
| 0 | success, empty, no-op | Consume the result. |
| 1 | network, timeout | Retry reads after connectivity recovers; reconcile uncertain writes first. |
| 1 | github | Server/malformed response; check doctor, retry reads later, reconcile uncertain writes. |
| 1 | internal | Preserve input/results, inspect doctor/version, report the failure; do not replay writes blindly. |
| 2 | usage, invalid_input | Fix arguments/names/line problems. Validation precedes writes; do not repeat successful partial steps. |
| 3 | not_found | Check repository, number and access; GitHub also hides private inaccessible resources this way. |
| 4 | auth | Ask the user to fix gh credentials or their token, then retry deliberately. |
| 4 | missing_scope | Show the supplied hint; the user can run gh auth refresh -s project (or the named scope). |
| 4 | forbidden | Check permissions; assignment can be silently refused and todos detects it. |
| 5 | rate_limited | Stop; respect retry_after_seconds before another request. |
| 6 | no_repo | Supply the repository or GH_REPO. |
| 6 | no_board | Inspect board list; select an accessible usable board with board use or the project flag. |
| 6 | issues_disabled | Choose a repository with issues enabled, or ask its owner to enable them. |
| 6 | config | Preserve the file/input; ask the user to repair or move aside the unreadable config/draft. |
| 7 | partial | Inspect completed, cause, issue and per-item results; finish only missing steps. Never rerun the whole command/batch. |
| 130 | interrupted | Retain quick-add accounting; reconcile any uncertain write before resuming. |

Quick-add exits 0 when all rows were created/skipped (or planned in dry-run),
7 if a stopped batch created any issue or may have created one, otherwise the
stopping error's code. A real signal uses 130. Partial does not undo confirmed
writes. Inspect the known issue with view, then use close/move as appropriate;
for an unknown create, inspect recent issues/GitHub before deciding to resend.
