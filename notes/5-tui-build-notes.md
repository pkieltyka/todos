# 5. TUI build notes

Lessons from building `internal/tui` and `internal/markdown`. The specs say
what the TUI does: `components/tui.md`, `components/markdown.md`,
`ui_design.md`. This note covers what we learned building and running it.

Every claim was checked against the code or a file on 2026-11-16, unless it is
marked *not re-checked* (those come only from the build session).

---

## 1. The libraries: what surprised us

### glamour wraps block quotes badly
Inside a `>` quote, glamour sometimes hard-wraps the last word of a line onto
a line of its own, with no `│ ` prefix. Narrowing the wrap width does not
avoid it.
- **How we know:** found by dogfooding real issue bodies.
- **Fix:** `repairQuotes` (`internal/markdown/render.go:273`) reflows the
  quoted paragraph around such an orphan. Only putting the prefix back still
  left a short middle line, so the paragraph is rewrapped.
- **Test:** `TestRender_quoteLinesKeepTheirPrefix` checks every width from 30
  to 100, quote depths 1 and 2, and that the words come back unchanged.
- **Order matters:** `repairQuotes` runs *before* `guard`
  (`render.go:124-128`). Anything the repair breaks is caught by `guard` and
  falls back to plain wrapped text. Keep that order.

### glamour decodes character references, even without a semicolon
glamour passes text through Go's `html.UnescapeString`, which decodes `&#27`
and `&#x1b` even when there is no `;`.
- **What that means:** stripping literal control and bidi characters from the
  source is not enough. `&#8238;` came out as a real U+202E (a right-to-left
  override) in the rendered body. That was review finding 1 of Phase 3, and it
  is fixed.
- **Defence 1:** `sanitize.go` neutralises numeric references
  (`numericRef`, `neutraliseRef`).
- **Defence 2:** `guard.go:35` rejects C1 and bidi controls in the *output*.
- **Rule:** keep both. The guard on the output is what catches the decoder's
  next surprise.

### glamour's "no TTY" style still writes hyperlinks
The NoTTY style still emits OSC 8 hyperlink escapes, in both of glamour's
hyperlink modes.
- **Fix:** with `--no-color` or `NO_COLOR`, the output is put through
  `ansi.Strip` after rendering (`render.go:129-135`).
- **Test:** `TestNoColor_plainMarkdownAndASCIIProfile` (`keys_test.go`).

### Other glamour facts
- **Long URLs break at a hyphen** when they wrap. This is an open item in
  `TODO.md`; nothing works around it.
- **Renderer instances:** a glamour renderer is built per (width, style) and
  is not safe to share. `Renderer` keeps at most 4 (`maxInstances`), each
  behind a lock (`lockedTR`).
- **Render cache:** the output cache defaults to 128 entries. That limit
  caused the flicker described in 2.2.

### Bubble Tea v2
- **The theme arrives late.** `tea.BackgroundColorMsg` comes after startup.
  The App rebuilds its theme when it lands, unless `TODOS_THEME` fixed it
  (`app.go:428`).
  - The harness uses a fixed theme, so it ignores this message.
  - A dark-to-light flip was tested by @reviewer's probe, not by a committed
    test (`reviews/detail-blocks-reviewer.md`).
- **Overlays:** they are composited with lipgloss v2 layers
  (`lipgloss.NewLayer(...).X().Y().Z()` and `NewCompositor`, `app.go:1030`).
  We never had to cut ANSI strings by hand to draw a box over a screen.
- **Ticks are chains you own.** The clock, the spinner and auto-refresh are
  each one `tea.Tick` that re-arms itself.
  - Each chain must stop when there is nothing to do, and be re-armed exactly
    once.
  - The clock stops while the terminal is too small and is re-armed by a
    resize that fits (`clockOn`, `app.go:450`). Without that, a small terminal
    woke the app once a second forever (Phase 4 review).
  - The spinner runs only while `spinNeeded`.
- **Never block in `Update`.** Rendering Markdown blocks, so `Update` calls
  only `Renderer.Cached`. A miss becomes a Cmd that calls `Render`
  (tui.md §16).
- **`Ctx.Markdown` must stay read-only.** The editor preview calls it from
  `View`.

### teatest v2
- **Version:** it is an untagged pseudo-version
  (`x/exp/teatest/v2 v2.0.0-20261101151554-27ecf3e4d454`) that requires
  bubbletea `v2.0.0-rc.1`. Minimum version selection lifts that to v2.1.1,
  and the two work together.
- **What uses it:** only two end-to-end tests (`e2e_test.go`). Both run in
  `make check` and pass.
- **Everything else** goes through our own harness (section 5), which is
  deterministic and faster.

---

## 2. Bugs that cost real time, and the rule each left

### 2.1 The focus bug that moved issue 6 on the real board
During the live TUI dogfood, a `>` meant for a throwaway issue moved real
issue 6.
- **Cause:** after a step, or a rejected step that snaps back, the board's
  focus stayed on the *column* while the card left it, so focus landed on a
  neighbour. The next key acted on that neighbour.
- **How we know:** the live incident (recorded in `TODO.md`), then the same
  class found again by review on the reject path (`TestBoard_rejectedStepFocusStaysOnCard_secondPressSameCard`).
- **Rule:** focus follows the *card*, whatever moved it: a step, a rejection,
  an uncertain change resolving, a refresh, or the board's own workflow. See
  the follow block in `screen_board.go:114`.
  - `sel = 0` on an empty column is half of the fix
    (`TestBoard_focusCanRestOnEmptyColumn`).
- **Live testing rule:** before a write key, read the screen and check which
  card is focused.

### 2.2 The detail screen redrew every comment on every rebuild (PR 84 CI failure)
`TestDetail_bodyBeforeComments_commentsCappedAt500` failed on CI at about
5.2 s.
- **Cause:** while 500 comments load, a detail screen rebuilds about 45 times,
  and each rebuild restyled every comment. The cost grew with the square of
  the comment count.
- **Fix:** `commentBlocks` in `screen_detail.go` keeps each comment's drawn
  lines.
- **Second cause:** the renderer's 128-entry cache could evict a render before
  its `renderedMsg` was handled. The comment then flickered back to plain
  text and was drawn twice. `markdownKept` (`app.go:291`) copies cache hits
  into `lastRender`.
- **Tests:** `TestDetail_commentBlocksDrawnOncePerVersion` fails if either
  half is reverted.
- **Rule:** a rebuild must not do work for each item it has already drawn.
  `settleDeadline` is 30 s only so it can catch a hang; it never sets the
  pace.

### 2.3 The harness counted a cancelled gated call as still blocked
`TestFresh_rReplacesAnAutoRefreshInFlight` failed about 1 run in 200.
- **Cause:** `settle` waits until every running Cmd is held at a gate. When
  the code under test cancelled a held call (`r` replacing an auto refresh),
  the harness still counted that gate as holding, so `settle` returned early.
- **Fix:** each `hgate` records which call it caught (`pos`), and `blocked()`
  counts it only while that call has not ended (`EndOrder == 0`,
  `harness_test.go:159-190`).
- **Evidence:** before the fix, 1 of 200 runs of `-race -count=200` failed;
  after it, 0 of 200 (not re-checked).
- **Rule:** if a harness test flakes, suspect the harness's notion of "idle"
  before the code.

### 2.4 Fresh as a flag cost more requests than no cache
The first Phase 10 version marked a manual `r` as a bare "fresh" flag, so every
nested structure read under it went to GitHub again. That meant several
session and board reads per page of a five-page list.
- **How we know:** @cto's Phase 10 review.
- **Fix:** fresh is a time: `domain.WithFreshSince` (cache.md §10).
- **In the TUI:** `App.manualAt` is the time of the key press (`app.go:124`,
  `:615`). Every read of that refresh, including later pages and comment
  continuations, carries that same time (`sinceFor`, `do.go:72`).
- **Test:** `TestFresh_laterPagesKeepTheKeyPressTimeWhileTheClockMoves`
  advances the clock between pages. Without the clock moving, a test could not
  tell the key-press time from "now".

---

## 3. Decisions that changed during the build

- **Closed issues are ordered by closed time, by the TUI.** GitHub's list
  query cannot order by closed time, so `order.go` sorts the closed view
  locally (tui.md §7.4).
  - `lastClosed` keeps a reopened row in place until the next refresh.
  - `todos list --state closed` keeps GitHub's order.
- **The list header shows how old the data is,** not when we last asked. A
  cache hit can be minutes old.
  - `ListCache` has two times: `DataAt` (the oldest page's real read) and
    `CheckedAt` (when the last load ended). See tui.md §7.9 and
    `store.go:48-73`.
- **`loadBoard` reads `Board` first, then `BoardInfo`.** `Board` reads the
  board's fields live on every call, so the `BoardInfo` read after it is a
  memo hit that matches the columns (`loadBoard` in `do.go`, tui.md §7.6).
  - The setup screens (no board, missing scope) still appear, because `Board`
    fails with the same errors.
  - **Tests:** `TestBoard_headerShowsTheProjectInfoBoardRefreshed` and
    `TestBoard_setupStatesStillAppearWithBoardReadFirst`.

---

## 4. Traps for the next change

- **Use one stamp for both `Put` and `releaseUncertain`.** A page's stamp is
  `pageStamp(asOf, sent)` (`do.go:176`): the cached read time when there is
  one, else the send time. Using the send time for a cache hit would let
  minutes-old data overwrite a newer detail, or release an uncertain change.
  `cachehit_test.go` pins both cases.
- **Keep the two list times apart.** Auto-refresh scheduling uses
  `CheckedAt` (`app.go:705`); the stale banner and the header use `DataAt`.
  - Swap them and an idle list either never refreshes, or refreshes on every
    tick while a hit keeps it "checked".
- **A load older than the list changes nothing.** In `onIssuesPage`
  (`do.go:238-248`), a shadow load whose data is older than the list's only
  moves `CheckedAt`.
- **`pop()` must not cancel a held queue's `Reread`** (`app.go:362`).
  Otherwise the queue waits up to a minute for the next auto refresh.
- **Quick add: the in-flight item is saved to the draft before its create is
  sent.** That is what makes a crash safe
  (`TestQuickAdd_inFlightSavedBeforeCreate`). Keep that order.
  - `draft.go`'s `remaining` calls `quickadd.Remaining`, which
    `architecture.md` §2 allows for exactly this. It adds only the span of
    the item being sent (`draft.go:81`).
  - The rule for which text is still unsent exists once, in `quickadd`.
    Don't copy it into the TUI.
- **`Sync` runs at the end of `Update`** and owns `lastRender`. `View` and
  render Cmds must not write it, or `-race` breaks.
- **Comment blocks are cached by everything they depend on:** the header label
  (it includes a relative time), the Markdown as drawn now, the width, and the
  theme. A new input to `rule` or `indent` must join the key in
  `commentBlocks`, or old lines get drawn.
- **Board focus is kept by option ID** (`col.Key`), not by column name or
  index. A renamed status keeps focus.

---

## 5. How to test the TUI

- **Model tests** use the harness in `harness_test.go`:
  - `newHarness(t, fixture)` runs every Cmd in its own goroutine and feeds
    the results back in.
  - `h.hold(method, n)` gates the next matching call. You then
    `h.release` or `h.fail` it. For "uncertain but it did apply", use
    `g.g.ApplyThenFail(err)` on the fake's gate.
  - `h.settle()` waits until everything is done or parked at a gate.
  - `h.advance(d)` moves the fixed clock. It fires nothing; ticks are
    delivered with `h.fire`.
  - `servicetest.Fake.Calls()` gives call order, with start and end sequence
    numbers. Request counts in the TUI are asserted this way.
- **Shaping service behaviour:** wrap `Deps.Service`; see `freshService` in
  `fresh_test.go` and `cachedListService` in `cachehit_test.go`. The fake has
  no cache, so cache-hit behaviour is simulated by a wrapper that sets
  `Page.AsOf`.
- **Goldens:** `testdata/TestGolden/<case>/{80x24,140x40}.golden` (ANSI
  stripped) and `testdata/TestGolden_ansi/` (colour, one per main screen).
  - Regenerate with `go test ./internal/tui -run TestGolden -update`, then
    read the diff. A diff you didn't expect is a bug, not noise.
- **Proving a test bites:** we reverted each rule in a scratch copy *outside*
  the repository and checked that its test failed. Two of our new tests passed
  without their fix until we made them advance the clock (2.4).
- **Live:** the TUI has no automated live test. The opt-in live tests are the
  CLI's (`internal/cli/live_test.go`, `TODOS_LIVE_TESTS`, see note 2).
  - The TUI was dogfooded by hand against the real repository, on throwaway
    issues that were then deleted.
  - Watch the focused card before every write key (2.1).
- **Points and requests:** I measured no points from the TUI myself. The
  idle-TUI figure in `TODO.md` (30 points against 58 over 10 minutes) is
  @lead-dev's measurement (note 4).

---

## 6. Open review findings (accepted, not fixed)

- **After a resize, `Ctx.Markdown` can draw an old-width render for a
  moment** (`app.go:278`: `lastRender` is looked up without comparing width).
  - Kept on purpose: the previous render stays up until the new one lands,
    which avoids a blank flash (tui.md §16).
  - Source: `reviews/detail-blocks-reviewer.md`, check 4.
- **Quick add: identical item text can put the "may exist" flag on the wrong
  copy.** `q.draft()` finds spans with `strings.Index`
  (`overlay_quickadd.go:204`), so with two byte-identical items the flag can
  land on the first.
  - Harmless: both copies are the same bytes and both are unsafe to resend.
    The run path uses exact positions.
  - Source: Phase 8 review notes.
- **A cached page 1 plus a live page 2 can skip a row.** A row updated
  between the two answers can be in neither page until the next load.
  - Accepted as a limit; written in cache.md (around line 255) and tui.md
    §7.9.
  - The window is the change token's lag, a few seconds.
- **The TUI never says the cache is off** (`TODOS_CACHE=off`). It was an
  optional idea, not built.

---

## 7. What I would do next, and what I would leave alone

**Next:**
- Fix the hyphen break in long URLs. It is probably a glamour wrap setting or
  a post-pass like `repairQuotes`, so check the renderer first.
- Add a committed theme-flip test (dark to light while a detail is open). It
  is only probed today.

**Leave alone:**
- The asOf rule (tui.md §7.2), the pending-change engine (§8), and
  `pageStamp`, `DataAt` and `CheckedAt`. Every rule there has a test that
  fails without it, and most were found the hard way.
- The order inside `Render`: sanitise, render, `repairQuotes`, `guard`, strip.
