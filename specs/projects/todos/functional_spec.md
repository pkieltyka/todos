---
status: complete
---

# Functional Spec: todos

## 1. Summary

`todos` is a terminal tool for working with one GitHub repository's issues,
pull requests and project board. It has two faces on the same behaviour:

- **TUI**: `todos` with no arguments opens a full-screen interface for humans.
- **CLI**: `todos <command>` runs one operation and exits. It is the interface
  for scripts and AI agents, and it ships with a skill file that teaches agents
  how to use it.

GitHub is the only source of truth. `todos` keeps no database of its own:
everything it shows comes from GitHub, and everything it changes is changed on
GitHub, so people on github.com see the same tickets.

## 2. Who uses it

| User | Interface | Needs |
|---|---|---|
| A developer at a terminal | TUI, sometimes CLI | See what is open, triage, assign, move cards, without leaving the terminal |
| An AI agent or script | CLI with `--json`, guided by the skill | Predictable commands, stable JSON, meaningful exit codes, no prompts |

## 3. Concepts and scope

### 3.1 Repository

Every run works on exactly one repository, resolved in this order:

1. `--repo OWNER/REPO`
2. The `GH_REPO` environment variable, as for `gh`
3. The GitHub remote of the git repository in the current directory. With
   several remotes, `todos` picks the one `gh`'s own library picks.

If neither gives a repository, the command fails with kind `no_repo` (6.5).
`todos help`, `todos version` and `todos skill` need no repository and no
credentials.

### 3.2 Board

The board is one GitHub Project (Projects v2). Classic projects are not
supported. A project is identified by its owner and number. The board for a
run is resolved in this order:

1. `--project`, given as `NUMBER` (owner = the repository owner),
   `OWNER/NUMBER`, or a project URL
2. The project remembered for this repository by `todos board use`
3. Auto-detection from the projects linked to the repository on GitHub:
   - exactly one linked project: use it
   - none: board features fail with kind `no_board`, with a hint to link a
     project or pass `--project`
   - several: the TUI shows a picker and remembers the choice; the CLI fails
     with kind `no_board` and lists the candidates

**Columns** are the options of the project's single-select field named
`Status`, in the project's order. Items with no status appear in a leading
"No status" column, shown only when it is not empty. A project with no
single-select `Status` field cannot be used as a board (kind `no_board`).

**Cards** are the project items that are issues of the current repository.
Other items (pull requests, draft items, issues from other repositories) are
not shown in v1. The board reports how many items are hidden for that reason,
so a half-empty board is never a mystery. Archived items are neither shown nor
counted. Moving or adding an issue whose card was archived brings the card
back; nothing is ever un-archived just by looking.

Cards keep the order GitHub returns within a column. Reordering inside a
column is out of scope.

### 3.3 Pull requests

Pull requests are a separate, **read-only** section in v1: list them, open one,
see its state, review status and checks. `todos` does not merge, review,
comment on, or check out pull requests, and pull requests do not appear on the
board.

### 3.4 Out of scope for v1

Several repositories at once; pull request actions; per-reviewer verdicts and
per-check results (the roll-up status is shown, the rest is one keypress away
in the browser); inline review threads; draft and pull request cards; creating
or configuring projects, fields or status options; milestones (shown, not
edited); issue types, sub-issues, relationships, reactions, pinning, locking,
transfer, attachments; creating or editing labels; editing or deleting
comments; issue templates; notifications; offline use; mouse support;
user-configurable themes, keys or refresh interval; vim-style modal editing
inside the TUI (the external editor covers it, see 4.8); per-item assignee or
label shortcuts inside a quick-add list.

## 4. Features

### 4.1 Issues

| Action | Behaviour |
|---|---|
| List | Filter by state (`open` default, `closed`, `all`), assignee, label(s), and free-text search. Sorted by most recently updated. In the TUI, the `closed` list is instead sorted by most recently closed; the order is worked out over the issues the TUI has loaded, because GitHub cannot list issues by closed time. |
| View | Number, title, state and close reason, author, assignees, labels, milestone, created / updated / closed times, board status, URL, body, and comments in chronological order. |
| Create | Title is required. Optional body, assignees, labels, and a board status. With a status, the new issue is also added to the board in that column. Without one, it is not added to the board. The exception is creating from the TUI's Board view, which always adds the issue to the board in the focused column, including "No status". |
| Quick add | Create many issues at once from a punch list (4.7). |
| Edit | Change title and/or body. |
| Assign | `assign` adds assignees, `unassign` removes them (none named = remove all), `reassign` replaces all current assignees with the ones given. `@me` means the authenticated user. GitHub allows at most 10 assignees. |
| Label | `label` adds labels, `unlabel` removes them. Labels must already exist in the repository. |
| Comment | Add a comment. An empty comment is a usage error. |
| Close / reopen | Close with reason `completed` (default) or `not-planned`, optionally with a closing comment (CLI only). Reopen a closed issue. |
| Delete | Permanently delete the issue. It cannot be undone, and GitHub decides who may do it (repository owners and admins; organisations can forbid it). It always needs explicit confirmation (6.3, 5.4). When GitHub refuses, the error is `forbidden` and says why. |

Rules that apply everywhere:

- Assignees, labels and statuses are validated before anything is written. An
  unknown value is an error (`invalid_input`) that names it and lists the valid
  ones, and nothing is changed.
- Removing is judged against the issue itself. An assignee or label that the
  issue currently has can always be removed, even one that is no longer in the
  repository's lists, such as a person who has since left the organisation.
  Only a name that is neither on the issue nor known to the repository is an
  error.
- After assigning, `todos` checks that GitHub actually applied it. GitHub can
  silently ignore an assignment the caller is not allowed to make; that is
  reported as `forbidden`, not as success.
- Adding something already present, or removing something already absent, is
  success. So is closing a closed issue or reopening an open one: nothing
  changes, no comment is added, and the output says so.
- Changes send only what changed (add this label, remove that assignee), so
  two people editing different fields of one issue do not overwrite each other.
- Title and body edits replace the whole value (see 4.5).

### 4.2 Pull requests (read-only)

| Action | Behaviour |
|---|---|
| List | Filter by state (`open` default, `closed`, `merged`, `all`). Sorted by most recently updated. Shows number, title, author, branch, draft flag, review status, checks status, updated time. |
| View | The above plus body, base and head branches, merge conflict status, the issues of this repository that the pull request closes, and its conversation comments. |

Review status and checks status are GitHub's own roll-ups, not recomputed:

- Review: `approved`, `changes_requested`, `review_required`, or `none` when
  the repository requires no review and none was given.
- Checks: `passing`, `failing`, `pending`, or `none` when the head commit has
  no checks. It covers both check runs and legacy commit statuses.

### 4.3 Board

| Action | Behaviour |
|---|---|
| Show | Columns with their cards: number, title, assignees, labels. |
| Move | Set an issue's status to a named column, or clear it (the card goes to "No status"). If the issue is not on the board yet, it is added first, so "move" always ends with the issue in that column. |
| Step (TUI only) | Shift a card to the column before or after the one it is in. It needs a current column, so it applies only to an issue already on the board. On any other issue it changes nothing and says how to add it. |
| Add | Add an issue to the board. With a status, same as Move. Without one, a new card lands in "No status" and a card already on the board is left as it is. |
| Remove | Take an issue off the board. The issue itself is untouched. Removing an issue that is not on the board is success. |
| Choose | List the candidate projects and remember one for this repository. |

A status name matches an option exactly first, then case-insensitively. If two
options differ only by case and neither matches exactly, that is
`invalid_input`.

**A board's own automations have the last word.** GitHub projects can be set
up to act on their own: add every new issue to a column, close an issue whose
card reaches a "done" column, move a card when its issue closes. These run a
few seconds after `todos` writes, and `todos` does not fight them: it makes no
delayed corrective writes and never retries. In practice, on a board that puts
new issues in a column automatically, a new issue created with "No status"
usually ends up in that column, and a done item filed as closed by quick add
may appear in the "done" column even though `todos` did not put it on the
board. The TUI shows what the board did at its re-read five seconds later.
Measured on this project's own board: 13 of 16 new "No status" cards were
moved to Backlog by the board within five seconds.

### 4.4 Sync (TUI)

- Data loads when the TUI starts. Nothing in the interface waits on the
  network: views show cached data or a loading state and stay responsive.
- `r` refreshes the current view. The visible view also refreshes
  automatically every 60 seconds, except while a form, picker or confirmation
  is open, and except when the rate limit is nearly used up (10).
- An automatic refresh of an issue list or a board first asks GitHub whether
  anything changed and reloads only if it did (9.1). `r` always reloads.
- The header always shows sync state: syncing, time since last sync, or an
  error. When a refresh fails, the last good data stays on screen, marked stale.
- After a refresh, the selection stays on the same issue (by number, not by
  row) and the scroll position is kept.

How changes appear:

- **Assign, label, close, reopen, move a card** show immediately with a
  pending marker. A field with a pending change keeps the local value until
  the change settles, whatever a refresh returns in the meantime.
- **Create** waits for GitHub: the form shows progress, and the new issue
  appears once GitHub has given it a number. **Delete** also waits: the row
  disappears only after GitHub confirms.
- When GitHub rejects a change, it is rolled back on screen and the error is
  shown. When the outcome is unknown (a timeout), `todos` re-reads that issue
  by number before deciding what to show.
- After a change, the issue is shown as GitHub acknowledged it. Anything else
  that changed on GitHub at the same time arrives with the next refresh. A
  moved or newly created card sits at the bottom of its column until a refresh
  shows where GitHub placed it.
- A board's own automations act a few seconds after a change: a new issue may
  be added to a column, and moving a card to a "done" column may close the
  issue. So after creating an issue or changing a card, the TUI looks at that
  issue once more five seconds later, to show what the board did.

### 4.5 Concurrent edits

GitHub is the source of truth and the last write wins. One best-effort guard
in the TUI: just before saving an edited title or body, `todos` re-reads the
issue and compares its current title and body with what the editor started
from. If either differs, it says so and offers **overwrite** or **cancel**.
Cancel keeps the user's text in the form. Changes to other fields (labels,
comments) do not trigger the warning. A change landing between that check and
the save can still be overwritten. The CLI `edit` command overwrites without
checking, because the caller passed the full new value on purpose.

### 4.6 Changes that take several steps

Three operations are more than one write on GitHub: create with a status
(create, then put on the board), move an issue that is not on the board yet
(add, then set status), and close with a comment (comment, then close). If a
later step fails, the earlier ones are **not** undone. The result is reported
as `partial` (6.5), naming what was done, so the caller can finish the job
instead of repeating it.

Quick add (4.7) has the same property per item: an item can be created but not
placed on the board, or created but not closed. It reports that per item in
its own result (`on_board`, `closed`), not through the step names of 6.5.

`todos` never retries a create or a comment on its own. If such a request
times out, the outcome is unknown and the error says so. GitHub offers no way
to make a create safe to repeat, so the promise is "never created twice by
`todos` retrying", and an unknown outcome is left for the user to check.

### 4.7 Quick add

Quick add turns a punch list into issues: the user types or pastes a list, and
each item becomes one issue. It exists in the TUI (5.5) and on the CLI
(`todos quick-add`), with the same rules.

**Reading the list**

- Every non-blank line that is not indented starts an item. Its text is the
  issue title.
- A leading list marker is removed from the title: `-`, `*`, `+`, a number
  followed by `.` or `)`, a checkbox `- [ ]`, or heading `#` marks.
- A checked box (`- [x]`) marks the item as already done. What happens to done
  items is one choice for the whole batch (see below).
- Lines indented by two or more spaces, or a tab, belong to the item above
  them and become its body, as Markdown, with that indentation removed. Nested
  bullets and blank lines inside a body are kept.
- Blank lines between items are ignored.
- `@name` and `#123` inside an item are plain text. There are no per-item
  shortcuts.

**Done items**: one choice per batch.

- **Skip** (the default): done items are not filed. The preview and the result
  list them as skipped.
- **File as closed**: each done item is created and then immediately closed as
  completed, so finished work has a ticket on record.

Skip is the default because issues cannot be un-created in bulk, a pasted list
often contains done items that already have tickets, and filing them doubles
the writes and the notifications sent to people watching the repository.

**Applying to all items**: optional assignees and labels apply to every issue
in the batch. An optional board status applies to the open items only. Done
items are never placed on the board: the chosen column is for work to do, and
`todos` cannot know which column means "done".

**Before anything is created**, the whole list is checked. Any of these is
`invalid_input`, reported with its line number, and nothing is created: an
empty list; an indented line before the first item; an item with no title left
after removing its marker; a title over 256 characters; more than 50 items; an
unknown assignee, label or status.

**Creating**

- Issues are created one at a time, in the order written, about one per
  second, to stay inside GitHub's limit on creating content. Issue numbers
  therefore increase in list order, though not necessarily consecutively.
- At the first failure, creation stops. Issues already created stay. The rest
  are reported as not attempted. An item whose request timed out is reported
  as uncertain: it may exist.
- With a board status, each issue is created and then placed on the board. If
  placing fails, the issue still exists; that item is reported as created but
  not on the board, and creation stops.
- A done item filed as closed is two writes, create then close, each paced
  like any other. If closing fails, the issue exists and stays open; that item
  is reported as created but not closed, and creation stops.
- Every item is accounted for in the result with its title, body and outcome,
  so no text is lost and nothing needs to be re-typed.

### 4.8 Markdown

Issue and pull request bodies and comments are Markdown, and handling it well
is a core requirement.

**Viewing.** Bodies and comments are shown rendered: headings, emphasis,
lists, task lists, tables, block quotes, links, and code blocks with syntax
highlighting. Text re-flows to the width of the terminal.

**Editing.** Bodies and comments are edited as raw Markdown, three ways that
work together:

- A built-in multi-line editor, for writing and quick changes. It is a plain
  editor: cursor movement, selection, copy and paste. It has no undo and no
  vim modes.
- A preview toggle that shows the draft rendered exactly as the viewer would
  show it.
- A hand-off to the user's own editor (`$VISUAL`, else `$EDITOR`), which opens
  the draft as a `.md` file and brings the saved text back into the form. This
  is where vim users get vim, with their own configuration. When neither
  variable is set, the action is not offered.

## 5. TUI (functional level)

Layout, keys and visual design are specified in `ui_design.md`. This section
fixes what the TUI must do.

### 5.1 Views

Three top-level views, always one keypress apart: **Issues**, **Pull
requests**, **Board**. `todos` opens on Issues; `todos tui prs` and
`todos tui board` open on the others. All three are always present: when one
cannot load, it shows why (5.3) and is not hidden.

| View | Shows | Actions |
|---|---|---|
| Issues | Issue list with state, number, title, labels, assignees, updated time | Open, create, quick add, edit, assign, label, comment, close / reopen, delete, add to or move on board, filter, change state filter, refresh, open in browser |
| Issue detail | Everything in 4.1 View | Same per-issue actions as the list |
| Pull requests | Pull request list (4.2) | Open, filter, show only mine, change state filter, refresh, open in browser |
| Pull request detail | Everything in 4.2 View | Open in browser |
| Board | Columns and cards (4.3) | Move between columns and cards, move a card to another column, open a card's issue, create or quick add issues in the focused column, per-issue actions, remove from board, choose a different project, refresh |

Every list and detail view can also copy the URL of the focused item to the
clipboard.

### 5.2 Requirements

- Every action is reachable from the keyboard, and a help screen lists every
  key. The same action uses the same key in every view.
- **Filtering.** Typing a filter narrows the items already loaded, matching
  title, number, labels, assignees and author. A separate "only mine" toggle
  shows issues assigned to me in the Issues and Board views, and pull requests
  I opened in the Pull requests view. In the Issues view the toggle asks
  GitHub, so it is complete even when the full list is cut off at 500. In the
  Pull requests and Board views it narrows what is loaded, and the view shows
  the match count next to the loaded count so a cut-off list is not mistaken
  for a complete one. Changing the state filter asks GitHub again. The TUI
  does not use GitHub's search, whose index lags behind recent changes.
- **Loading.** Lists show the first 100 items as soon as they arrive and load
  the rest in the background, up to 500 for the current state filter. Beyond
  that the view says how many exist and that the list is cut off.
- **Comments** load when a detail view opens, not with the list, up to 500.
  Beyond that the view says so and points to the browser.
- **Text from GitHub.** Bodies and comments are shown as rendered Markdown
  (4.8), scrollable, and re-flowed when the terminal is resized. Before
  rendering, terminal control sequences are stripped. HTML comments are
  dropped. Other raw HTML has its tags removed and the text between them
  kept. Images appear as their link, whether written as Markdown or as an
  HTML `<img>` tag, which is what GitHub inserts for pasted screenshots.
- **Editing text.** The create form, the edit form and the comment form all
  use the editor, preview and external-editor hand-off of 4.8.
- **Closing.** Close applies as `completed`. Closing as `not planned` is a
  separate action. A closing comment is CLI-only in v1.
- Minimum terminal size is 80×24. Below that, the TUI shows a "terminal too
  small" message instead of a broken layout, and recovers when resized.
- Light and dark terminal backgrounds are both readable. The background is
  detected; `TODOS_THEME=light|dark` overrides the detection for terminals
  that do not report it.
- Run without a terminal attached (piped or redirected), bare `todos` prints
  usage and exits with code 2 instead of starting the TUI.

### 5.3 Empty and error states

Each view has an explicit state for: loading, no items, no items matching the
filter, and failed to load (with the reason and the fix, see 6.5). The Board
view has dedicated states for "no board linked to this repository", "several
projects, pick one", "this project has no Status field" and "missing the
`project` permission", each showing what to do.

### 5.4 Confirmations

- Close and reopen apply immediately; reopening or closing again undoes them.
- Delete asks the user to type the issue number to confirm.
- Leaving a form with unsaved changes asks before discarding them.

### 5.5 Quick add

Available from the Issues and Board views. The rules are in 4.7; this is the
flow.

1. **Write.** A large text area opens. The user types or pastes the list;
   Enter is a new line.
2. **Preview.** One row per item, with its title and whether it has a body,
   plus any problems with their line numbers. Done items are marked and
   counted separately from open ones, and each shows what will happen to it:
   skipped, or created then closed. The preview also holds the choices for the
   whole batch: assignees, labels, board status, and what to do with done
   items (skip or file as closed, one key to switch). Opened from the Board
   view, the status starts as the focused column; opened from the Issues view,
   it starts empty. The preview says that done items are not added to the
   board. Going back returns to the text unchanged.
3. **Create.** The rows become a progress list: each shows working, then its
   new issue number, or the reason it failed. A done item shows creating, then
   closing, then its number as closed. The user can cancel; the item in flight
   finishes first.
4. **Done or stopped.** When everything is created, quick add closes and the
   new issues are in the list. If it stopped early, the text area comes back
   holding only the items that were not created, with the error shown, so
   continuing never duplicates the ones that succeeded. An item with an
   uncertain outcome is kept and flagged, with a prompt to check GitHub before
   sending it again. An item created but not placed on the board, or created
   but not closed, is not kept; it is named in the message so it can be moved
   or closed normally.

The draft is kept safe: it is saved on this machine within half a second of
the last keystroke and restored the next time quick add opens for the same
repository, including after a crash or quitting. During creation it is updated
before each item is sent and after each one settles, so an item that was being
sent when `todos` died comes back flagged as "may exist" and is never sent
again silently. The draft is removed once every item is created or the user
discards it. Discarding a non-empty draft asks first.

## 6. CLI

### 6.1 Commands

```
todos                                   open the TUI (Issues view)
todos tui [issues|prs|board]            open the TUI on a given view

todos list     [--state open|closed|all] [--assignee LOGIN | --mine]
               [--label NAME]... [--search TEXT] [--limit N | --all]
todos view     ISSUE
todos add      TITLE [--body TEXT | --body-file PATH] [--assign LOGIN]...
               [--label NAME]... [--status NAME]
todos quick-add --file PATH [--assign LOGIN]... [--label NAME]...
               [--status NAME] [--checked skip|close] [--dry-run]
todos edit     ISSUE [--title TEXT] [--body TEXT | --body-file PATH]
todos assign   ISSUE LOGIN...
todos unassign ISSUE [LOGIN...]
todos reassign ISSUE LOGIN...
todos label    ISSUE NAME...
todos unlabel  ISSUE NAME...
todos comment  ISSUE (TEXT | --body-file PATH)
todos close    ISSUE [--reason completed|not-planned] [--comment TEXT]
todos reopen   ISSUE
todos rm       ISSUE --yes

todos move     ISSUE (STATUS | --no-status)
todos board                             show the board
todos board list                        list candidate projects
todos board use PROJECT                 remember the board for this repository
todos board add    ISSUE [--status NAME]
todos board remove ISSUE

todos pr list  [--state open|closed|merged|all] [--limit N | --all]
todos pr view  PR

todos labels                            list the repository's labels
todos assignees                         list users who can be assigned
todos doctor                            check credentials, permissions, repo, board
todos skill                             print the agent skill file
todos version
todos help [COMMAND]                    same as --help
```

Global flags: `--repo OWNER/REPO`, `--project PROJECT`, `--json`,
`--fresh`, `--no-color`, `--help`, `--version`. `--fresh` reads everything
from GitHub, ignoring the local cache (9.1).

### 6.2 Arguments

- `ISSUE` and `PR` are a number (`12` or `#12`) or a full GitHub URL. A URL
  for a different repository than the resolved one is `invalid_input`.
- `PROJECT` is `NUMBER`, `OWNER/NUMBER`, or a project URL (3.2).
- `LOGIN` is a GitHub username, or `@me`. `--mine` is short for
  `--assignee @me`; giving both is a usage error. `todos pr list` has no
  author filter in v1, because GitHub's pull request listing offers none;
  callers filter the JSON by `author`.
- `--reason not-planned` is the flag spelling. In JSON the same value is
  `"not_planned"`, as GitHub reports it.
- `--label` given several times means the issue must carry all of them.
- `--search` uses GitHub's search. Its index can lag a few seconds behind a
  change that was just made, and GitHub returns at most 1,000 results for a
  search. With `--limit`, a search with more matches returns what is available
  and `truncated: true`. With `--all`, it fails with `invalid_input` and a hint
  to narrow the query, because "all" cannot be delivered.
- `--limit` defaults to 50. `--all` pages through everything with no cap.
  Giving both is a usage error.
- `--body-file -` reads the body from standard input. `--body` and
  `--body-file` together are a usage error.
- `todos edit` with neither `--title` nor `--body` is a usage error.
- Each command acts on one issue. Callers loop for several. The one exception
  is `todos quick-add`, which creates an issue per item of a punch list (4.7).
  `--file -` reads the list from standard input. `--checked` chooses what
  happens to done items: `skip` (the default) or `close`, which files each one
  and closes it as completed. `--dry-run` reads and checks the list and reports
  what would be created, without creating anything.
- `--json` with `todos`, `todos tui` or `todos skill` is a usage error.

### 6.3 No prompts

CLI commands never ask questions and never wait for keyboard input. A missing
required argument is a usage error. `todos rm` refuses to run without `--yes`.

### 6.4 Output

**Text (default)**: human-readable tables and summaries on standard output,
with colour when writing to a terminal. `--no-color` and the `NO_COLOR`
environment variable turn colour off. A truncated list ends with a line saying
how many items were shown out of how many. A successful change prints one line
naming what happened and the URL, for example
`Created #12 Fix login redirect  https://github.com/OWNER/REPO/issues/12`.

**JSON (`--json`)**: exactly one JSON document on standard output and nothing
else. On failure, standard output is empty and the one document is on standard
error (6.5). This is a compatibility contract: within a major version, fields
are only ever added, never renamed, removed or retyped.

| Command | Document on success |
|---|---|
| `list` | `{"issues": [Issue], "total": int, "truncated": bool}` |
| `view` | `Issue` with the view-only fields |
| `add`, `edit`, `assign`, `unassign`, `reassign`, `label`, `unlabel`, `close`, `reopen`, `move`, `board add`, `board remove` | `{"issue": Issue, "changed": bool}` |
| `quick-add` | `QuickAdd` |
| `comment` | `{"issue": Issue, "comment": Comment}` |
| `rm` | `{"deleted": int}` |
| `board` | `Board` |
| `board list` | `{"projects": [Project], "selected": Project or null}` |
| `board use` | `{"project": Project}` |
| `pr list` | `{"pull_requests": [PullRequest], "total": int, "truncated": bool}` |
| `pr view` | `PullRequest` with the view-only fields |
| `labels` | `{"labels": [{"name": string, "description": string, "color": string}]}` |
| `assignees` | `{"assignees": [{"login": string, "name": string}]}` |
| `doctor` | `{"ok": bool, "checks": [{"name": string, "ok": bool, "detail": string, "hint": string}]}` |
| `version` | `{"version": string}` |

`changed` is `false` when the command was a no-op (4.1). The `Issue` in a
change result comes from the write itself and has no view-only fields.

Conventions for every document:

- Times are strings, UTC, RFC 3339. A time that does not apply is `null`.
- Lists are always arrays, `[]` when empty, never `null`.
- A login is a string. `author` is `null` when the account was deleted.
- View-only fields are absent, not `null`, in lists and change results.
- Strings that have no value are `""`, except where `null` is listed below.

```
Issue {
  number: int, title: string, state: "open"|"closed",
  state_reason: "completed"|"not_planned"|"reopened"|null,
  author: login|null, assignees: [login], labels: [string],
  milestone: string|null, comments_count: int,
  created_at: time, updated_at: time, closed_at: time|null, url: string,
  board_status: string|null,

  view only:
  body: string, comments: [Comment], comments_truncated: bool
}

Comment { id: string, author: login|null, created_at: time, body: string, url: string }

PullRequest {
  number: int, title: string, state: "open"|"closed"|"merged", draft: bool,
  author: login|null, head: string, base: string,
  review: "approved"|"changes_requested"|"review_required"|"none",
  checks: "passing"|"failing"|"pending"|"none",
  assignees: [login], labels: [string],
  created_at: time, updated_at: time, closed_at: time|null, merged_at: time|null,
  url: string,

  view only:
  body: string, mergeable: "mergeable"|"conflicting"|"unknown",
  closes: [int], comments: [Comment], comments_truncated: bool
}

Board {
  project: Project,
  columns: [{status: string|null, cards: [Card]}],
  hidden_items: int, truncated: bool
}

Card { number: int, title: string, state: "open"|"closed",
       assignees: [login], labels: [string], url: string }

Project { number: int, owner: login, title: string, url: string, usable: bool }

QuickAdd {
  items: [{
    line: int, title: string, body: string, checked: bool,
    result: "created"|"failed"|"uncertain"|"unattempted"|"skipped"|"planned",
    issue: Issue|null, on_board: bool, closed: bool, error: Error|null
  }],
  counts: {created: int, failed: int, uncertain: int, unattempted: int,
           skipped: int, planned: int}
}
```

`QuickAdd` rules:

- `items` lists every item of the input in order, with its parsed title and
  body, whatever happened to it. `line` is its line number in the input.
- `planned` appears only with `--dry-run`, where every item that would be
  created is `planned`.
- `checked` is `true` for a done item. With `--checked skip` its result is
  `skipped`. With `--checked close` its result is `created` and `closed` says
  whether the close step succeeded.
- `error` has the same shape as the `error` object in 6.5.
- `quick-add` is the one command that prints its document on standard output
  even when it fails part-way, because the document is the record of what was
  created. The exit code and standard error still report the failure.
- Exit code: 0 when every item was created or skipped. 7 (`partial`) when it
  stopped after at least one issue was created or may have been. Otherwise,
  when nothing was created, the code of the error that stopped it.

- `board_status` is `null` both when the issue is not on the board and when no
  board is available. Listing and viewing issues never fails because of the
  board: a missing board or missing `project` permission only leaves
  `board_status` empty.
- `closes` lists issue numbers in this repository only.
- `comments` holds conversation comments, up to 500; `comments_truncated`
  says when there are more.
- A column with `status: null` is "No status".
- `Board.truncated` is `true` when the project has more than 1000 items, the
  most `todos` reads. `hidden_items` counts within what was read.
- `Project.usable` is `false` when the project has no single-select `Status`
  field.

### 6.5 Errors and exit codes

Errors go to standard error. In text mode that is a message plus, where there
is a fix, a hint line with the exact command. With `--json` it is one document:

```
{"error": {
  "kind": string, "message": string, "hint": string,
  "retry_after_seconds": int,       rate_limited only
  "uncertain": bool,                true when a write may or may not have applied
  "completed": [string],            partial only: the steps that succeeded
  "cause": string,                  partial only: the kind of the step that failed
  "issue": Issue                    partial only: the issue as far as it got
}}
```

| Exit code | Meaning | Kinds |
|---|---|---|
| 0 | Success, including an empty list and a no-op | |
| 1 | Failed for a reason outside the caller's control | `network`, `timeout`, `github` (server error), `internal` |
| 2 | The command was wrong | `usage`, `invalid_input` |
| 3 | The thing does not exist, or the caller cannot see it | `not_found` |
| 4 | Not allowed | `auth`, `missing_scope`, `forbidden` |
| 5 | Rate limited | `rate_limited` |
| 6 | Setup needed | `no_repo`, `no_board`, `issues_disabled`, `config` |
| 7 | Partly done (4.6): do not rerun the whole command | `partial` |
| 130 | Interrupted with Ctrl-C | |

How GitHub's answers map to kinds, first match wins:

1. No credentials found, or GitHub rejects the token: `auth`.
2. GitHub says the token lacks a permission scope: `missing_scope`, with the
   `gh auth refresh` command as the hint.
3. GitHub reports a primary or secondary rate limit: `rate_limited`.
4. Any other refusal: `forbidden`.
5. Not found: `not_found`. GitHub answers "not found" for private things the
   caller cannot see, so the message says "not found or no access".
6. GitHub rejects the values sent: `invalid_input`.
7. A GitHub 5xx or malformed answer: `github`.

`completed` uses these step names: `created`, `commented`, `added_to_board`.
Every request times out after 30 seconds.

### 6.6 `todos doctor`

Checks, in order, and reports each as pass or fail with a fix:

1. Credentials found, and where from (`gh`, or which environment variable).
2. The authenticated user.
3. The resolved repository: it exists, the caller can read it and write
   issues, and issues are enabled.
4. The resolved board and its status columns.
5. Board access: the caller can read and change project items.

6. The config file: readable and well-formed, or absent.
7. The cache: its directory, and whether it is usable, switched off, or not
   yet created. `doctor` itself never reads from the cache and never writes to
   it.

Checks 3 to 5 use what GitHub reports the caller may do, not scope names, so
they hold for every token type. Where the token exposes its scopes they are
listed for information. `doctor` never changes anything on GitHub, so a pass
means GitHub says the action is allowed, not that it was tried.

`doctor` always prints its full report on standard output, in text or JSON,
whatever the verdict. Exit code: 0 when checks 1 to 3 pass (board, config and
cache problems are reported but do not fail it); otherwise the exit code of the
first failing check.

## 7. Agent skill

- `todos skill` prints a skill file in the Agent Skills format: a `SKILL.md`
  with `name` and `description` front matter, then instructions.
- The skill is built into the binary, so the text always matches the version
  of `todos` the agent is running. Its source lives in the repository at
  `skills/todos/SKILL.md`. Installing it for an agent is copying that output to
  wherever the agent keeps skills.
- It covers: when to use `todos`; every command with examples; always using
  `--json`; the JSON shapes and exit codes of section 6; how to recover from
  each error kind, including `partial` and `uncertain`; filing several issues
  with `quick-add`, checking the list first with `--dry-run`; and rules of
  conduct:
  run `todos doctor` when something fails, never run `todos rm` unless the user
  explicitly asked for a permanent delete, prefer `close`.
- The skill must not drift from the CLI: every command and flag it mentions
  exists, checked automatically.

## 8. Credentials and requirements

- `todos` calls the GitHub API directly, in-process. It does not run the `gh`
  binary for its operations.
- `todos` uses the credentials the GitHub CLI already has. It works when
  either `gh` is installed and logged in (`gh auth login`), or a token is
  provided in `GH_TOKEN` / `GITHUB_TOKEN`.
- `todos` never runs a login flow, never stores a token, and never prints or
  logs one.
- Supported tokens are the one `gh auth login` creates and classic personal
  access tokens. They need the `repo` scope for private repositories, and
  `project` for board features (`read:project` is enough to view).
  `gh auth refresh -s project` adds it. Fine-grained tokens are not a v1
  guarantee.
- github.com is the supported host. GitHub Enterprise hosts configured in `gh`
  are expected to work but are not a v1 guarantee.
- Linux and macOS are supported. Windows is not a v1 target.

## 9. Configuration

One file, `$XDG_CONFIG_HOME/todos/config.yml` (default `~/.config/todos/`),
written by `todos` itself. In v1 it holds one thing: the board (owner and
number) remembered for each repository.

- A missing file is not an error: nothing is remembered yet.
- A file that cannot be read or parsed is treated as empty. In text mode a
  warning line goes to standard error; with `--json` nothing is printed, so
  standard error stays one document. `todos doctor` reports it either way.
  `todos` never overwrites a file it could not parse: `todos board use` then
  fails with kind `config`.

Separately, the TUI keeps unsent quick-add drafts (5.5), one per repository,
under `$XDG_STATE_HOME/todos/` (default `~/.local/state/todos/`).

### 9.1 Cache

To spend less of the GitHub allowance, `todos` keeps a disposable cache under
`$XDG_CACHE_HOME/todos/` (default `~/.cache/todos/`), shared by the CLI and
the TUI. It holds copies of two kinds of data:

- **Issue lists and boards.** Before a stored copy is used, `todos` asks GitHub
  one small question: has anything in this repository or on this board
  changed? The copy is used only if the answer is no. There is no timer.
- **Slow-changing structure.** The signed-in user, the repository, labels and
  assignable users are reused for 5 minutes; the board and its Status columns
  for 1 minute.

Everything else is always read from GitHub: one issue, one pull request,
comments, pull request lists and search.

What a user can rely on:

- A change made through `todos` shows at once.
- A change made elsewhere (github.com, `gh`, another person, a board
  automation) shows on the first read after GitHub's own "last updated" times
  reflect it. That is normally within about ten seconds; GitHub gives no
  guarantee.
- No stored list or board is used for longer than 10 minutes.
- A label, user or status that `todos` does not know is looked up again on
  GitHub before a command is rejected.
- `--fresh` (CLI) and `r` (TUI) read everything from GitHub.
- The cache can never make a command fail. If it cannot be used, `todos` works
  without it. `TODOS_CACHE=off` turns it off. Deleting the directory is safe.
- It is kept separately for each credential, readable only by the user, and
  holds list rows and board cards only: never issue bodies or comments.

## 10. Constraints

- **Responsiveness**: the TUI draws its first frame without waiting for the
  network, and no keypress ever blocks on a request.
- **Scale**: comfortable with 500 issues in a list and 500 cards on a board.
  Larger sets are cut off with a visible notice (TUI) or `truncated: true`
  (CLI), never silently.
- **Safety**: text from GitHub is untrusted. It is never interpreted as
  terminal control sequences and never passed to a shell.
- **Rate limits**: auto-refresh fetches only the visible view, and pauses
  while less than 10% of the hourly allowance remains. Manual refresh and
  explicit actions still work.
