---
status: complete
---

# UI Design: todos

How `todos` looks and is driven: the TUI (sections 1 to 7), the CLI's text
output (section 8), and the design decisions taken (section 9). Behaviour is
defined in `functional_spec.md`; references like (5.2) point to its sections.

The TUI design is built on the real APIs of bubbletea v2.1.1, lipgloss v2.1.0,
bubbles v2.2.1 and glamour v2.0.1, drafted with @frontend-dev.

Conventions in the mocks: `▌` is the selection bar, `⠋` is a pending spinner
(animated), `·` separates header fields. Colours are named by role (section 6),
not drawn. All glyphs are plain Unicode (no Nerd Font required).

---

## 1. The frame

Every screen is the same four bands. Only the body changes.

| Band | Rows | Contents |
|---|---|---|
| Header | 1 | app name, repo, the three view tabs (active one in accent), sync state at the right |
| Sub-bar | 1 | view-specific: state filter, "mine" toggle, filter text, counts, load progress, truncation notice |
| Body | rest | the view |
| Footer | 1 | left: 4–7 most useful keys for the focused thing (from the keymap, so it can't drift); right: position (`12/342`). A toast replaces the left side for messages and errors. The `x` hint names what `x` would do to that issue: `x close` on an open issue, `x reopen` on a closed one (a pending close counts as closed). |

The minimum size is **65×24**, with a 21-row body. At **140×40** the body is
37 rows. Below the minimum, only the too-small screen is drawn (§3.15).

Width rules:

- At 65–79 columns, the header preserves all three tabs and shortens the repo
  or sync text as needed. Active filters show the end of the query on a
  dedicated line. Help groups stack vertically, form footers use compact hints,
  and issue and Quick Add metadata use extra rows.
- Columns are laid out by priority. When space runs out, the lowest-priority
  column is dropped, never squeezed into garbage. The title column always takes
  what is left (minimum 24 cells) and is truncated with `…`.
- At ≥ 120 columns the issue and PR detail views move their metadata into a
  right-hand side panel (§3.2b). Below that it stacks above the body.
- At any width, Markdown is rendered at `min(body width, 100)` so long
  lines stay readable. The render is cached per (item, width) and redone on resize.

### 80×24

```
 todos  pkieltyka/todos    1 Issues  2 PRs  3 Board          ● synced 12s ago
 open ▾  · mine: off · filter: —                              342 open issues
▌○ #142  Fix flaky sync when gh is offline        bug +1     @pk        3d
 ○ #141  Board: remember last focused column      ux         —          3d
 ...                                                              (21 body rows)
 j/k move  enter open  n new  A quick add  / filter  ? help             1/342
```

### 140×40

```
 todos  pkieltyka/todos      1 Issues   2 Pull requests   3 Board                                                     ● synced 12s ago
 state: open ▾  · mine: off · filter: —                                                                         342 open issues
▌○ #142  Fix flaky sync when gh is offline                                    ● bug  ● sync          @pkieltyka   In progress   3 days ago
 ○ #141  Board: remember last focused column                                  ● ux                   —            Todo          3 days ago
 ...                                                                                                                (37 body rows)
 j/k move  enter open  n new  A quick add  e edit  a assign  L label  x close  M move  / filter  ? help                         1/342
```

At 140 columns the list gains the full label names, the board status column
and full relative times. The tab names are spelled out.

---

## 2. Navigation model

- Three top-level views: `1` Issues, `2` Pull requests, `3` Board, or
  `tab`/`shift+tab` to cycle. Each view keeps its own cursor, filter and scroll
  while you are away.
- A **stack** inside each view: list → detail. Overlays (forms, pickers,
  confirms, help) open on top of the stack. `esc` always pops one level and
  never quits.
- Opening an issue from the Board pushes Issue detail onto the Board's stack.
  `esc` returns to the board with the same card focused.
- **Two input modes.** In *navigation mode* single letters are commands. In
  *text mode* (filter field, form fields, picker filter, quick add, confirm
  inputs) letters are text, and only `esc`, `tab`, `enter` and `ctrl+…` keys
  are commands. The footer always shows which mode you are in by its key hints.

---

## 3. Screens (80×24 unless stated)

### 3.1 Issue list

```
 todos  pkieltyka/todos    1 Issues  2 PRs  3 Board          ● synced 12s ago
 open ▾ · mine: off · /sync                      7 of 342 · loading 200/342…
▌○ #142  Fix flaky sync when gh is offline        bug +1     @pk        3d
 ○ #139  Sync: keep selection after refresh       sync       @pk +1     5d
⠋○ #131  Retry policy for gh timeouts             sync bug   —          1w
 ✓ #120  Sync cache invalidation on edit          sync       @lead      2w
 ⊘ #118  Background sync for all views            sync       —          3w
 ○ #101  Document sync interval                   docs       —          1mo
 ○ #97   Sync status in header                    ux         @fe        1mo






 
 
 
 
 
 
 esc clear filter  enter open  n new  e edit  x close  ? help             1/7
```

- Columns by priority: selection/pending gutter (1), state glyph (2), number (6),
  title (≥24), labels (10, then `+N`), assignees (10, `@first +N`), updated (4).
  At 80 columns, labels go first when space is short.
- State glyphs: `○` open, `✓` closed as completed, `⊘` closed as not planned.
  Colour reinforces the glyph but never replaces it.
- `open` and `all` list the most recently updated first. `closed` lists the
  most recently closed first, the time column shows the closed time, and the
  sub-bar says so: ` closed ▾ by closed time · mine: off · /`.
- `/` puts the cursor in the sub-bar filter (text mode). Typing narrows the list live.
  `enter` keeps the filter and returns to navigation. `esc` clears it.
- **Empty states** use the body centre, one line plus the fix:
  - loading (first page not in yet): `⠋ Loading issues…`
  - none: `No open issues. Press n to create one, A to quick add.`
  - no match: `Nothing matches "sync". esc clears the filter.`
  - failed: `✗ Couldn't load issues: not authenticated.` and on the next line
    `Run: gh auth login` (the hint comes from the error kind, 6.5).
- **Truncated:** sub-bar right side reads `500 of 1,204 · list cut off`.

### 3.2 Issue detail

**(a) 80×24, stacked:**

```
 todos  pkieltyka/todos    1 Issues  2 PRs  3 Board          ● synced 12s ago
 ‹ Issues  #142                                                   ○ Open
 Fix flaky sync when gh is offline
 @pkieltyka opened 3d ago · updated 2h ago · 4 comments
 Assignees @pkieltyka  Labels ● bug ● sync  Board In progress  Milestone v1
 ──────────────────────────────────────────────────────────────────────────────
  When `gh` can't reach the network the refresh spinner never stops.

  Steps
  1. Disconnect wifi
  2. Press r

    if err != nil { return err }

 ── @lead-dev · 2h ago ──────────────────────────────────────────────────────
  Reproduced. The context is never cancelled.
 ── @pkieltyka · 1h ago ─────────────────────────────────────────────────────
  Thanks, assigning to me.

 j/k scroll  e edit  c comment  a assign  L label  x close  esc back      38%
```

- One viewport scrolls the body and all the comments together. Comments are
  separated by a rule carrying author and time.
- `]` / `[` jump to the next or previous comment header. `g`/`G` go to the top or bottom.
- The comments load when detail opens: `⠋ Loading comments…` sits under the
  body until they arrive. Over 500: a notice line `Showing 500 of 812
  comments. o opens the issue in the browser.`
- Pending changes show inline: `Labels ● bug ● sync ⠋+docs` (the pending
  addition is in the pending colour with a spinner).

**(b) 140×40, side panel:**

```
 ‹ Issues  #142  Fix flaky sync when gh is offline                                                                        ○ Open
 ────────────────────────────────────────────────────────────────────────────────────────────────┬────────────────────────────────
  When `gh` can't reach the network the refresh spinner never stops.                              │ Author     @pkieltyka
                                                                                                  │ Opened     3 days ago
  Steps                                                                                           │ Updated    2 hours ago
  1. Disconnect wifi                                                                              │ Assignees  @pkieltyka
  ...                                                                                             │ Labels     ● bug
 ── @lead-dev · 2 hours ago ─────────────────────────────────────────────                          │            ● sync
  Reproduced. The context is never cancelled.                                                     │ Board      In progress
                                                                                                  │ Milestone  v1
                                                                                                  │ URL        github.com/…/142
```

### 3.3 Pull request list

```
 todos  pkieltyka/todos    1 Issues  2 PRs  3 Board          ● synced 40s ago
 open ▾ · mine: off · filter: —                                  6 open PRs
▌○ #150  TUI: board view skeleton          fe/board      ✓ approved   ✓    1h
 ◇ #149  Core: go-gh issue client          core/issues   ? review     ⠿    3h
 ○ #147  Docs: skill file                  docs/skill    ± changes    ✗    1d
 ...
 enter open  / filter  s state  m mine  o browser  ? help                 1/6
```

- State glyphs: `○` open, `◇` draft, `●` merged, `✕` closed. The state filter cycles
  open → closed → merged → all.
- Review status: `✓ approved`, `± changes`, `? review` (required), blank for
  none. Checks: `✓` passing, `✗` failing, `⠿` pending (static, not animated),
  blank for none. At 140 columns these become words (`changes requested`, `pending`).
- Columns by priority: state, number, title, branch (head), review, checks,
  updated. Author joins at ≥ 100 columns.

### 3.4 Pull request detail

```
 ‹ Pull requests  #149                                            ◇ Draft
 Core: go-gh issue client
 @lead-dev wants to merge core/issues → master · updated 3h ago
 Review ? review required   Checks ⠿ pending   Mergeable ✓ no conflicts
 Closes #131 #139
 ──────────────────────────────────────────────────────────────────────────────
  Adds the issue client on top of go-gh …
 ── @reviewer · 1h ago ──────────────────────────────────────────────────────
  …
 j/k scroll  ]/[ comments  o browser  y copy URL  esc back                 12%
```

Read-only: there are no issue-action keys here. The `Closes` line is plain
text in v1.

### 3.5 Board

**80×24.** Three columns fit (24 cells each plus gaps). `‹ 1` / `2 ›` show
how many columns are off-screen on each side. The focused column is kept in view.

```
 todos  pkieltyka/todos    1 Issues  2 PRs  3 Board          ● synced 12s ago
 Board: todos (#3) · mine: off · /                      28 cards · 2 hidden ⓘ
   ┌ Todo  9 ─────────────┐ ┏ In progress  4 ━━━━━┓ ┌ Review  3 ───────────┐ 2 ›
   │ #141 Board: remember │ ┃▌#142 Fix flaky sync ┃ │ #150 Board view      │
   │ last focused column  │ ┃ when gh is offline  ┃ │ skeleton             │
   │ ux · —               │ ┃ bug sync · @pk      ┃ │ ux · @fe             │
   │                      │ ┃                     ┃ │                      │
   │ #139 Keep selection  │ ┃ #131 Retry policy ⠋ ┃ │ #149 go-gh issue     │
   │ after refresh        │ ┃ for gh timeouts     ┃ │ client               │
   │ sync · @pk +1        │ ┃ sync bug · —        ┃ │ core · @lead         │
   │ …6 more              │ ┃                     ┃ │                      │
   └──────────────────────┘ ┗━━━━━━━━━━━━━━━━━━━━━┛ └──────────────────────┘
 h/l column  j/k card  </> move  enter open  n new here  ? help           1/4
```

- A card is 3 lines: the number and the title wrapped to 2 lines, then labels and the
  assignee. A blank line separates cards. At 140×40, five columns of 26 cells fit.
- The focused column has a heavy border in accent. The focused card has the
  selection bar and the selection background.
- `2 hidden ⓘ` counts the project items that are not issues of this repo (3.2).
  `i` explains it in a toast.
- A "No status" column appears first and only when it is not empty (3.2).
- `<` / `>` move the card to the previous or next column, optimistically. It
  appears at the bottom of the target column with a spinner (4.4). Focus follows
  the card.
- `/` filters cards across all columns, and empty columns stay visible.

### 3.6 Create / edit form, with preview toggle

The form is a full-body overlay, because 80×24 leaves no room for a floating box.
Edit uses the same form with the title and body pre-filled; the other fields are
hidden (edit changes only the title and body, 4.1).

```
 todos  pkieltyka/todos    1 Issues  2 PRs  3 Board          ● synced 12s ago
 New issue                                                    ✎ write │ preview
 Title     ▌Fix flaky sync when gh is offline
 Assignees  @pkieltyka                                              (enter)
 Labels     ● bug                                                   (enter)
 Status     In progress                                             (enter)
 ──────────────────────────────────────────────────────────────────────── Body
  When `gh` can't reach the network the refresh spinner never stops.
  
  Steps
  1. Disconnect wifi▌
 
 
 
 
 
 
 
 
 
 tab field ctrl+s/alt+s create  ctrl+r preview  ctrl+o $EDITOR esc cancel L4:C19
```

- `ctrl+r` swaps the body textarea for the glamour render in the same space
  (`write │ preview` shows which side is active). In preview the body is read-only
  and `j/k` scroll it. `ctrl+r` switches back with the cursor where it was.
- The Assignees, Labels and Status rows are buttons, reached with `tab`: with
  focus on one, `enter` opens the same picker that `a` / `L` / `M` open
  elsewhere (§3.8). They are not text fields, and the letters themselves do
  nothing here because a form is in text mode.
- `ctrl+o` suspends the TUI (`tea.ExecProcess`) and opens a temp `.md` file
  in `$VISUAL`/`$EDITOR`. On return the body is replaced with the saved file.
  If neither variable is set, the hint is not shown and the key does nothing.
- Saving shows `⠋ Creating…` in the footer. The form stays until GitHub returns
  a number, then closes and focuses the new row.
- Edit conflict (4.5), a small box in the middle of the form:

```
          ┌ #142 changed on GitHub since you started editing ─────┐
          │ The title or body was changed by someone else.        │
          │ o overwrite with my version   esc keep editing        │
          └───────────────────────────────────────────────────────┘
```

- `esc` with unsaved changes uses the same small-box style: `Discard changes? y discard
  · esc keep editing`.

### 3.7 Comment form

```
 ‹ #142 Fix flaky sync when gh is offline
 New comment                                                  ✎ write │ preview
 ──────────────────────────────────────────────────────────────────────────────
  Reproduced on master. The ctx from Update is dropped here:
  
      go m.store.Refresh(context.Background())▌
 
 ...
 ctrl+s/alt+s comment  ctrl+r preview  ctrl+o $EDITOR  esc cancel         L3:C47
```

The same component as the body editor, without the field rows. An empty comment
can't be sent. Either submit shortcut shows the toast `Comment is empty`.

### 3.8 Assign and label pickers

These are floating overlays (lipgloss v2 layers) over the dimmed current screen, about
44×14, centred.

```
            ┌ Assignees · #142 ──────────────────────┐
            │ / ▌                                    │
            │ ▌[x] @me (pkieltyka)                   │
            │  [ ] @lead-dev                         │
            │  [x] @reviewer                         │
            │  [ ] @octocat                          │
            │  …14 more                              │
            │                                        │
            │ 2 selected · max 10                    │
            │ space toggle  enter apply  esc cancel  │
            └────────────────────────────────────────┘
```

```
            ┌ Labels · #142 ─────────────────────────┐
            │ / sy▌                                  │
            │ ▌[x] ● sync                            │
            │  [ ] ● sync-later                      │
            │                                        │
            │ space toggle  enter apply  esc cancel  │
            └────────────────────────────────────────┘
```

- The picker opens in navigation mode: `j/k` move and `space` toggles. `/` (or simply
  typing a letter) puts the cursor in the filter line. `enter` applies and `esc` clears the
  filter first, then cancels.
- `enter` sends only the differences (4.1). Each difference becomes a pending
  marker on the row or detail it came from.
- The assignee list has `@me` pinned first, and the 11th selection is refused with a
  toast. The label list shows a `●` in each label's GitHub colour (§6.3).
- The **Status picker** (`M`) has the same shape with single selection. It lists the
  status options in board order, then `No status`, then `Remove from board`
  (shown only when the issue is on the board).

### 3.9 Quick add: write

```
 Quick add · 7 lines                                    draft saved 2s ago ✓
 ──────────────────────────────────────────────────────────────────────────────
 - Fix header sync label at 80 cols
 - Add `y` to copy URL
   Uses OSC 52, falls back to a toast with the URL.
 - [x] Spike goeditor
 - Board: show hidden-items count
 1. Detail: ]/[ jump between comments▌
 
 
 One issue per line · indent 2+ spaces to add a body · - [x] marks it done
 ctrl+s/alt+s preview  ctrl+o $EDITOR  ctrl+x discard  esc close (keeps draft)
```

- Opened with `A` from Issues or Board. If a draft exists for this repo it comes
  back exactly as it was left.
- The help line under the editor reminds the user of the parsing rules (4.7).

### 3.10 Quick add: preview

```
 Quick add · 4 issues · 1 done                                 esc back to text
 Assignees  —      (a)   Labels  ● ux  (L)   Status  Todo  (M)
 Done items  skip  (d)                  done items are not added to the board
 ──────────────────────────────────────────────────────────────────────────────
  1  Fix header sync label at 80 cols
  2  Add `y` to copy URL                                          + 1 body line
  ✓  Spike goeditor                                              skipped: done
  3  Board: show hidden-items count
  4  Detail: ]/[ jump between comments
 
 enter create 4 issues  a/L/M set for all  d done items  esc back to text
```

- `a`, `L` and `M` open the same pickers as everywhere else and apply to the whole
  batch (4.7). Opened from the Board, Status starts as the focused column.
- `d` switches what happens to done items (`- [x]`) between `skip` and
  `file as closed`. With `file as closed`, the header reads
  `4 issues · 1 done, filed as closed`, the done row reads `create, then
  close`, and the create hint counts it: `enter create 5 issues`. The choice
  resets to `skip` every time quick add opens.
- **Problems** replace the create hint and block creation:

```
  ✗ line 9: title is 312 characters (max 256)
  ✗ Labels: "uxx" doesn't exist. Valid: bug, docs, sync, ux, …
 esc back to text to fix
```

### 3.11 Quick add: progress, and stopped

```
 Quick add · creating 2 of 4                                  esc stop after this
 ──────────────────────────────────────────────────────────────────────────────
  ✓  #151  Fix header sync label at 80 cols
  ⠋        Add `y` to copy URL
  ·        Board: show hidden-items count
  ·        Detail: ]/[ jump between comments
```

A done item being filed as closed shows two steps on its row: `⠋ creating`,
then `⠋ closing`, then `✓ #153 closed`.

The stopped state is the write screen again, holding only the items that were not created,
with a banner. An issue that was created but could not be closed is named the
same way as one that could not be placed on the board (`#153 was created but is
still open: press x on it`).

```
 ✗ Stopped at "Board: show hidden-items count": the request timed out.
   Created #151. #152 was created but is not on the board: press M on it.
   2 items kept below.
 ──────────────────────────────────────────────────────────────────────────────
 - Board: show hidden-items count   ⚠ may exist: check GitHub before resending
 - Detail: ]/[ jump between comments
```

In this example the first item was created and placed. The second was created
but placing it failed, so it is named in the banner and not kept. The third
timed out, so it is kept and flagged. The fourth was never attempted.

The `⚠ may exist` flag is UI decoration on that line, not text in the draft.
It is shown only for an uncertain (timed-out) item, and editing that line
clears it. An item that failed for a known reason (for example a rate limit)
is kept without the flag.

On full success the overlay closes, a toast says `Created 4 issues #151–#155`,
and the list shows them.

### 3.12 Delete confirm

```
          ┌ Delete #142 permanently ───────────────────────────────┐
          │ "Fix flaky sync when gh is offline"                    │
          │ This can't be undone, and deletes its comments too.    │
          │                                                        │
          │ Type 142 to confirm: 14▌                               │
          │                                                        │
          │ enter delete   esc cancel                              │
          └────────────────────────────────────────────────────────┘
```

The border and title use the error colour. `enter` does nothing until the number
matches exactly. Then it shows `⠋ Deleting…` inside the box. The row disappears only
after GitHub confirms (4.4). A `forbidden` refusal is shown in the box with
GitHub's reason, and the box stays open until `esc`.

### 3.13 Help

```
 Help · Issues                                                 / filter keys
 ──────────────────────────────────────────────────────────────────────────────
 Navigate                     Issue actions                Views & app
 j/k ↓/↑   move               n   new issue                1 2 3  views
 g/G       top/bottom         A   quick add                tab    next view
 ctrl+d/u  half page          e   edit title/body          r      refresh
 enter     open               a   assignees                /      filter
 esc       back               L   labels                   m      mine only
                              c   comment                  s      state filter
                              x   close / reopen           o      open in browser
                              X   close as not planned     y      copy URL
                              M   board status             ?      help
                              < > move on board            q      quit
                              D   delete
 esc close
```

This screen is generated from the keymap (`bubbles/help` full view), grouped
into columns, with the current view's group first. It scrolls when it doesn't fit.

### 3.14 Toasts and banners

- Toast: one line in the footer, replacing the key hints. A confirmation
  (`✓ Closed #142 · x to undo`) lasts about 6 seconds or until the next key.
  An error (`✗ Couldn't label #142: forbidden. You need triage access.`) stays
  until the next key, so it cannot time out unread.
- Banner: a line at the top of the body that stays until it is dismissed or fixed,
  for view-level failures while stale data is still shown:
  `⚠ Refresh failed: network unreachable. Showing data from 4m ago · r retry`.

### 3.15 Terminal too small

```
 todos needs at least 65×24.
 This terminal is 64×18.
 Resize the window, or q to quit.
```

It is centred and plain, and nothing else is drawn. All state is kept, and the previous screen
comes back on resize.

### 3.16 Board error states

These are centred in the body. The header and tabs stay so the user can switch views.

```
 No project board is linked to pkieltyka/todos.

 Link one on GitHub (repository → Projects → Link a project),
 or start with:  todos --project <number>
 P choose from your projects
```

```
 Several projects are linked to pkieltyka/todos. Pick one:

 ▌todos (#3)            12 items · updated 2h ago
  Roadmap (#1)          88 items · updated 3w ago

 enter use and remember   esc cancel
```

```
 The board needs the "project" permission on your GitHub token.

 Run:  gh auth refresh -s project
 then press r.
```

```
 Project "Roadmap" (#1) has no "Status" field, so it can't be shown as a board.
 P choose another project
```

---

## 4. Keymap

Rule: a key means the same thing everywhere it is active. Where an action
doesn't apply (for example `c` in PR detail) the key does nothing and isn't listed in
the footer.

| Key | Action | Issues list | Issue detail | PRs list | PR detail | Board |
|---|---|:-:|:-:|:-:|:-:|:-:|
| `1` `2` `3` | Issues / PRs / Board | ✓ | ✓ | ✓ | ✓ | ✓ |
| `tab` `shift+tab` | next / previous view | ✓ | ✓ | ✓ | ✓ | ✓ |
| `j` `k` `↓` `↑` | move / scroll | ✓ | scroll | ✓ | scroll | cards |
| `h` `l` `←` `→` | previous / next column | | | | | ✓ |
| `g` `G` `home` `end` | top / bottom | ✓ | ✓ | ✓ | ✓ | ✓ (in column) |
| `ctrl+d` `ctrl+u` `pgdn` `pgup` | half page | ✓ | ✓ | ✓ | ✓ | ✓ |
| `]` `[` | next / previous comment | | ✓ | | ✓ | |
| `enter` | open | ✓ | | ✓ | | ✓ |
| `esc` | back / close / clear filter | ✓ | ✓ | ✓ | ✓ | ✓ |
| `/` | filter | ✓ | | ✓ | | ✓ |
| `m` | only mine on/off | ✓ | | ✓ | | ✓ |
| `s` | cycle state filter | ✓ | | ✓ | | |
| `r` | refresh | ✓ | ✓ | ✓ | ✓ | ✓ |
| `o` | open in browser | ✓ | ✓ | ✓ | ✓ | ✓ |
| `y` | copy URL | ✓ | ✓ | ✓ | ✓ | ✓ |
| `n` | new issue (Board: in focused column) | ✓ | ✓ | | | ✓ |
| `A` | quick add (Board: status = focused column) | ✓ | ✓ | | | ✓ |
| `e` | edit title and body | ✓ | ✓ | | | ✓ |
| `a` | assignees picker | ✓ | ✓ | | | ✓ |
| `L` | labels picker | ✓ | ✓ | | | ✓ |
| `c` | comment | ✓ | ✓ | | | ✓ |
| `x` | close (completed) / reopen | ✓ | ✓ | | | ✓ |
| `X` | close as not planned | ✓ | ✓ | | | ✓ |
| `M` | board status picker (move / add / remove) | ✓ | ✓ | | | ✓ |
| `<` `>` | move to previous / next status | ✓ | ✓ | | | ✓ |
| `D` | delete (typed confirm) | ✓ | ✓ | | | ✓ |
| `P` | choose project | | | | | ✓ |
| `i` | explain hidden board items | | | | | ✓ |
| `?` | help | ✓ | ✓ | ✓ | ✓ | ✓ |
| `q` | quit | ✓ | ✓ | ✓ | ✓ | ✓ |
| `ctrl+c` | quit | everywhere, including text mode |

`r` always reads GitHub. It skips the cache, including what the labels and
assignee pickers show, and takes over an automatic refresh that is already
running. The 60-second auto-refresh may be answered from the cache.

Text mode (forms, filter, pickers' filter, quick add, confirm inputs):

| Key | Action |
|---|---|
| `tab` / `shift+tab` | next / previous field (forms) |
| `enter` | newline in a body; next field in Title; apply in pickers and confirms |
| `ctrl+s`, `alt+s` | save / create / send / go to preview (quick add) |
| `ctrl+r` | toggle the Markdown preview |
| `ctrl+o` | open the body in `$VISUAL` / `$EDITOR` |
| `ctrl+x` | discard the quick-add draft (asks first) |
| `esc` | leave the field / cancel the form (asks if dirty) |
| textarea defaults | cursor and selection keys of bubbles textarea |

Quick add preview (navigation mode, nothing to type):

| Key | Action |
|---|---|
| `a` `L` `M` | assignees / labels / board status for the whole batch |
| `d` | done items: skip / file as closed |
| `j` `k` | scroll the list of items |
| `enter` | create |
| `esc` | back to the text, unchanged |

"Only mine" (`m`) means issues assigned to me in the Issues and Board views,
and pull requests I opened in the Pull requests view.

Key choices that need care:

- **The bubbles textarea binds `ctrl+e` (line end) and `ctrl+p` (previous line)**, so
  those can't be the editor hand-off or preview keys. `ctrl+o`, `ctrl+r`,
  `ctrl+s`, `alt+s`, and `ctrl+x` are free in the default textarea keymap. Raw mode turns
  off XON/XOFF, so `ctrl+s` reaches us.
- `l` is the board's "next column", so labels are on `L`. Card moves are on `<`/`>`
  rather than `H`/`L` for the same reason.
- `q` quits from anywhere in navigation mode. If writes are still in flight,
  it asks: `2 changes still sending. Quit anyway? y/esc`. Quick-add drafts
  are already on disk and don't block quitting.
- `x` on a closed issue reopens it. That is also the "undo" in the close toast.
- `<`/`>` and `M` are two different actions (functional spec 4.3). `M` *sets*
  a named status and adds the issue to the board if needed. `<`/`>` *step* to
  the neighbouring column, which only means something for a card that already
  has a place. On an issue that isn't on the board they show the toast
  `#142 isn't on the board · M to add it` and change nothing.

---

## 5. State presentation

### 5.1 Focus

- In lists, the focused row has the selection bar `▌` in accent plus the selection
  background across the full row.
- On the board, the focused column gets a heavy accent border and the others a subtle border.
  The focused card has the bar and background.
- In forms, the focused field label is in accent with a `▌` cursor, and unfocused fields are in normal text.
- When an overlay is open, the screen behind it is drawn in the muted colour (dimmed), so
  only one thing looks focused.
- The terminal cursor (`tea.View.Cursor`) is shown only in text mode.

### 5.2 Pending changes (4.4)

- Row level: a spinner in the gutter (`⠋`) while any change on that issue is
  in flight. One shared `spinner.Tick` drives every visible spinner.
- Field level: the pending value is drawn in the pending colour, in italic, with
  a spinner: `⠋+docs`, `⠋@lead-dev`, a struck-through `~~@pk~~` for a pending
  removal, and the card shown at the bottom of its new column with `⠋`.
- On success the marker is removed. No toast; the change itself is the
  feedback. The one exception is close and reopen, which show a confirmation
  toast because it carries the undo hint (§3.14).
- On rejection the value snaps back and an error toast names the issue, the
  change and GitHub's reason.
- On an unknown outcome (timeout) the marker becomes `?` until the re-read by
  number (4.4) settles it.

### 5.3 Sync and stale data (header, right side)

| State | Header text | Colour |
|---|---|---|
| fresh | `● synced 12s ago` | success dot, muted text |
| issue list from the cache | `● synced 4m ago · checked just now` (from 100 columns; `● synced 4m ago` below) | success dot, muted text |
| syncing | `⠋ syncing…` | muted |
| loading more | `⠋ 200/342` (in the sub-bar) | muted |
| refresh failed | `⚠ stale · 4m ago` plus banner (§3.14) | warning |
| auto-refresh paused (rate limit) | `⏸ auto-refresh paused · 4% left` | warning |
| never loaded | `✗ offline` and the view's failed state | error |

The issue list can be answered from the read cache. Its header then tells the
data's real age, not the time of the check: `synced 4m ago`, plus
`· checked just now` from 100 columns. Data read under a minute ago by another
command reads `synced <1m ago`. `synced just now` appears only when the data
was read by that check, and `r` always reads GitHub, so it ends there. The
stale state and banner use the data's age too. The board and the pull request
list are always live and keep the plain header.

Stale data stays fully usable: the user can act on it. List rows are not
greyed out, because that would hurt readability for a state that is often brief.

### 5.4 Errors

- An action error (one issue) goes to a toast with the issue number and GitHub's reason.
- A view load error with no data shows the view's failed state with the fix (§3.1).
- A view load error with older data on screen shows a banner plus the stale header.
- A form or confirm error is shown inside the form or box, and the form stays open with the text intact.
- A permission or setup error that blocks the board gets the board's dedicated states (§3.16).
- The error text comes from the core's typed error kinds (6.5), and each kind maps
  to one short hint line, so the TUI and CLI give the same advice.

---

## 6. Colour and style roles

The theme is built after `tea.BackgroundColorMsg` (`IsDark()`), using
`lipgloss.LightDark`. Glamour gets its built-in `dark` or `light` style chosen the
same way. Until that message arrives (a few milliseconds), dark is assumed.
Some terminals and tmux setups never answer the query; the environment
variable `TODOS_THEME=light|dark` overrides detection for them. Colours are
truecolour hex. lipgloss/colorprofile downsamples them for 256-colour and 16-colour terminals,
and `NO_COLOR` turns them off. **Meaning never depends on colour alone**: every
state also has a glyph or a word.

### 6.1 Roles

| Role | Used for | Dark | Light |
|---|---|---|---|
| `text` | body text, titles | `#E6EDF3` | `#1F2328` |
| `muted` | metadata, times, hints, dimmed backdrop | `#8B949E` | `#656D76` |
| `subtle` | rules, unfocused borders, `·` separators | `#3D444D` | `#D0D7DE` |
| `accent` | focus bar and border, active tab, links, key names in hints | `#58A6FF` | `#0969DA` |
| `selection` (bg) | focused row or card background | `#1C2B3F` | `#DDF4FF` |
| `open` / `success` | open state, `✓` success, passing checks, approved | `#3FB950` | `#1A7F37` |
| `done` | closed-completed, merged | `#A371F7` | `#8250DF` |
| `inactive` | not planned, draft, PR closed without merge | `#8B949E` | `#656D76` |
| `pending` / `warning` | pending changes, stale, review required, pending checks, `may exist` | `#D29922` | `#9A6700` |
| `error` | failures, failing checks, changes requested, delete box | `#F85149` | `#CF222E` |

### 6.2 Styles

- Bold for the issue title in detail, the active tab and the section heads in help.
  Bold is not used for whole rows.
- Italic only for pending values.
- Faint (muted colour) for the backdrop behind overlays.
- Borders: rounded `┌` for overlays and board columns, heavy `┏` for the focused board
  column. There are no borders around lists, which saves 2 columns at 80 wide.
- Header and footer have no background fill, so they look right on any terminal
  background without adjustment.

### 6.3 Label colours

Label colours from GitHub are arbitrary and many are unreadable as text on one
background or the other. So a label is drawn as a `●` in its own colour followed by
the name in `text`. No filled chips, so contrast never depends on the label.

---

## 7. Implementation notes that shape the design

- `View()` returns `tea.View`. Each screen sets `AltScreen: true`, `Cursor`
  (only in text mode) and `WindowTitle` (`todos · #142` etc.) declaratively.
  `View.ProgressBar` (the terminal's native progress bar, where the terminal
  supports it) mirrors quick-add progress and background loading. It's cheap and safe to ignore.
- Overlays are composed with lipgloss v2 layers or the canvas over the rendered base screen,
  not string splicing.
- Every key goes through one `key.KeyMap`. The footer hints and the help screen are
  generated from it, so the keymap table above stays true by construction.
- glamour is rendered off the UI goroutine as a `tea.Cmd` and cached per (item,
  width), so a resize never blocks a keypress (10).
- Text from GitHub is sanitised before it reaches glamour or the list (5.2).
  Titles, label names and logins are also stripped of control sequences.

---

## 8. CLI text output

The commands, flags, JSON and exit codes are fixed in the functional spec
(section 6). This section fixes what the default, human-readable output looks
like. Agents use `--json` and never parse this.

### 8.1 Rules

- **To a terminal**: aligned columns with a muted header row, colour by the
  same roles as the TUI (6.1), titles truncated with `…` to fit the width,
  relative times.
- **Piped or redirected**: one item per line, tab-separated, no header, no
  colour, nothing truncated, times in RFC 3339. This is stable enough for
  `cut` and `awk`, but `--json` is the contract.
- No borders or box drawing. An empty result prints one plain sentence to
  standard output (`No open issues.`) and exits 0.
- Markdown bodies are rendered (4.8) when writing to a terminal and printed
  raw when piped.

### 8.2 Examples

```
$ todos list
  #    TITLE                                  LABELS      ASSIGNEES    UPDATED
○ 142  Fix flaky sync when gh is offline      bug, sync   pkieltyka    3d
○ 141  Board: remember last focused column    ux          —            3d
Showing 50 of 342 open issues. Use --limit N or --all for more.

$ todos view 142
#142  Fix flaky sync when gh is offline                                  ○ Open
pkieltyka opened 3 days ago · updated 2 hours ago · 4 comments
Assignees: pkieltyka   Labels: bug, sync   Board: In progress   Milestone: v1
https://github.com/pkieltyka/todos/issues/142

  When gh can't reach the network the refresh spinner never stops.
  ...

── lead-dev · 2 hours ago
  Reproduced. The context is never cancelled.

$ todos board
todos (#3)  https://github.com/users/pkieltyka/projects/3

Todo (9)
  #141  Board: remember last focused column    ux          —
  #139  Keep selection after refresh           sync        pkieltyka +1
In progress (4)
  #142  Fix flaky sync when gh is offline      bug, sync   pkieltyka
2 items hidden (not issues of this repository).

$ todos close 142
Closed #142 Fix flaky sync when gh is offline  https://github.com/pkieltyka/todos/issues/142

$ todos close 142
#142 is already closed. Nothing changed.

$ todos move 142 Reveiw
error: no status named "Reveiw". Valid: Todo, In progress, Review, Done
$ echo $?
2

$ todos board
error: the board needs the "project" permission on your GitHub token
hint: gh auth refresh -s project

$ todos quick-add --file notes.md --checked close
✓ #151  Fix header sync label at 80 cols
✓ #152  Add `y` to copy URL
✓ #153  Spike goeditor                          (done: filed and closed)
✗        Board: show hidden-items count         rate limited, retry in 2m
·        Detail: ]/[ jump between comments      not attempted
Created 3 of 5. Stopped: rate limited. Nothing was created twice; rerun with only the remaining items.

$ todos doctor
✓ Credentials      from gh (keyring), user pkieltyka
✓ Repository       pkieltyka/todos, issues enabled, you can write
✓ Board            todos (#3): Todo, In progress, Review, Done
✗ Board access     token lacks the "project" permission
                   fix: gh auth refresh -s project
```

### 8.3 Help

- `todos --help` lists commands in four groups, in this order: Issues, Board,
  Pull requests, Other (`doctor`, `skill`, `version`). Its first line says
  that `todos` with no arguments opens the TUI.
- Every command's `--help` gives a one-line description, its usage line, its
  flags, and two or three real examples.
- A usage error prints the one-line problem and the command's usage line to
  standard error, not the full help.

## 9. Design decisions

Taken by @cto on @frontend-dev's open questions. Peter can overrule any of them.

| # | Decision | Why |
|---|---|---|
| 1 | No list-plus-preview split in v1. Wide terminals get the detail side panel only. | A split is a second layout to build and test for every list. Candidate for v1.1. |
| 2 | `q` quits from anywhere in navigation mode; `esc` is always "back". It asks first while changes are still sending. | One meaning per key. |
| 3 | `y` copies the URL, and the toast also prints it. | Some terminals and tmux setups ignore the clipboard request silently. |
| 4 | Labels are a coloured `●` plus the name, not filled chips. | GitHub label colours are arbitrary; chips would be unreadable on some backgrounds. |
| 5 | Relative times everywhere; absolute times only in the detail side panel. | Scanning a list needs "how long ago", not a timestamp. |
| 6 | Board cards are three lines. No compact mode in v1. | One layout to get right first. |
| 7 | `<` / `>` step an issue to the neighbouring column from the Issues list and detail too, not only on the board. Stepping needs a current column, so on an issue that is not on the board they explain how to add it (`M`) and add nothing. | Same action, same key, everywhere; never a silent side effect. |
| 8 | Confirmation toasts time out; error toasts stay until the next key. | An error must not disappear unread. |
| 9 | The `Closes` line in pull request detail is plain text. | Jumping between views from inside a read-only screen can wait. |
| 10 | `TODOS_THEME=light|dark` overrides background detection. | Without it, a terminal that does not answer the query would get the dark theme on a light background. It forces the detection result; it is not a theme setting. |

Not in v1: mouse, user-defined keys or colours, a compact board, the list
split, and an in-app vim mode (functional spec 3.4).
