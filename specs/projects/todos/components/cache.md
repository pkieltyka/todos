---
status: complete
---

# Component: read-through cache

Author: @cto. Reviewed by @lead-dev, whose live experiments and objections are
in `reviews/cache-design-lead-dev.md` and are folded in here. Where the two
disagree, this document is the decision.

## 1. Why

`todos` shares one GitHub allowance (5,000 GraphQL points an hour per user)
with `gh`, with every agent that drives the CLI, and with every open TUI.
Two things spend most of it:

- **An open TUI** reloads the visible view every 60 seconds, whether or not
  anything changed. Measured: an issue list page of 100 costs 3 points and a
  board page of 100 costs 2. A 500-issue list is 15 points a reload, about 900
  an hour; a 1,000-card board is 20 a reload, about 1,200 an hour.
- **Every CLI command starts cold.** Before its real work it reads the
  repository, the viewer, and the board's identity and fields, and often the
  labels and assignable users: four to six requests with the same answer as a
  minute ago.

## 2. What the cache promises, and what it does not

GitHub is a shared, remote store. Another person, an agent or a board
automation can change an issue at any moment, and `todos` is not told. GraphQL
has no conditional requests, so there is no free "has this changed?" answer.

**The board is always live.** Peter's decision: the cards of a board, in the
Board view and in `todos board`, are read from GitHub every time and are never
cached.

Issue lists are cached, but never on a timer. Before each use the cache asks
GitHub one cheap question, the **probe** (1 point, measured), which
returns a **change token**. A stored page is used only when the token is the
one it was stored under.

The token is good evidence, not proof. @lead-dev observed GitHub updating an
issue's `updatedAt` several seconds after the data itself had changed (a
deleted comment: the count was already 0 while the timestamp was still old,
for up to about 7 seconds). So the promise is:

- **Your own changes made through `todos`** show at once. They never depend on
  the token (section 6).
- **Changes made elsewhere** show on the first read after GitHub's own
  timestamps reflect them. That was within about 10 seconds in testing. It is
  not instant and GitHub does not guarantee a bound.
- **Nothing stored is used after 10 minutes**, token or not. This bounds what
  the token cannot see at all, such as a renamed label.
- **`--fresh` on the CLI and `r` in the TUI** skip the cache and read GitHub.
- **The TUI never lets a cached copy replace newer knowledge** (section 5,
  `AsOf`). A cached page is stamped with the time it was really read.

## 3. Two kinds of data, two rules

| Kind | What | Rule |
|---|---|---|
| **Content** | issue list pages (not search) | Used only when a probe made for this call returns the token the page was stored under, the page is under 10 minutes old, and no write has happened since it was read. |
| **Structure** | session (viewer and repository), labels, assignable users, the resolved board with its Status field and options | Reused for 5 minutes. Misses and failures heal themselves (section 6). The Board view and `todos board` do not rely on it for their columns: `Board` reads the board's fields from GitHub every time (section 5). |
| **Never cached** | board cards, one issue, one pull request, comments, pull request lists, search, every write, `doctor` | Always GitHub. |

Why those are never cached:

- The board is where several people move cards at once, and a card in the
  wrong column is the most misleading thing `todos` could show. It stays live.
- One issue or pull request costs about one point, the same as a probe, and
  these are the reads the TUI uses to learn the truth after a change.
- Pull request lists show check results, and a finished CI run does not move
  the pull request's `updatedAt`. No token vouches for them.
- Search runs on an index that lags.

## 4. Where it lives

**The service owns the policy; `internal/cache` is storage.** Every read and
every write already passes through the service, which knows the repository,
the board and the normalised filter. So:

- `service` decides what to look up, when to probe, what to store and when to
  invalidate. It defines the interface it needs:

  ```go
  // service
  type CacheStore interface {
      // Get returns the stored bytes and the time they were read from GitHub.
      Get(space, key string) (data []byte, readAt time.Time, ok bool)
      Put(space, key string, data []byte, readAt time.Time)
      // Epoch returns the space's current write epoch; Bump changes it.
      Epoch(space string) string
      Bump(space string) bool // false when the new epoch could not be written
      Drop(space, prefix string)
  }
  ```

  A nil store means no cache. Every method is best effort and reports nothing:
  a store that cannot read or write behaves as empty.

  **`readAt` is the moment the request to GitHub was sent, taken before the
  call, never the moment the file was written.** Every age in this document
  (5 minutes, 1 minute, 10 minutes), the `RefreshMetadata` cutoff and `AsOf`
  are measured from it. A read that was in flight when something invalidated
  it therefore always carries a time from before the invalidation, however
  late its file is written.

- `internal/cache` implements it with files. It imports `domain` only. It knows
  nothing about issues or tokens.

- `service.Backend` gains two methods, implemented in `github`:

  ```go
  ChangeToken(ctx context.Context, repo domain.RepoRef, project domain.NodeID) (domain.ChangeToken, error)
  CredentialKey(ctx context.Context) (string, error) // opaque; see section 7
  ```

- The `Service` interface does not change. `domain.Page` gains one field,
  `AsOf time.Time` (section 5).

The structure cache is the service's existing metadata memo, made persistent.
There is one expiry per value, not a memory timer stacked on a disk timer: the
memo and the file share the same `readAt`.

## 5. Content: the read path

### The change token

```graphql
query ChangeToken($owner: String!, $name: String!, $project: ID!, $withProject: Boolean!) {
  viewer { login }
  repository(owner: $owner, name: $name) {
    issues(first: 1, orderBy: {field: UPDATED_AT, direction: DESC}) {
      totalCount
      nodes { number updatedAt }
    }
  }
  node(id: $project) @include(if: $withProject) {
    ... on ProjectV2 { updatedAt items { totalCount } }
  }
}
```

```go
type ChangeToken struct {
    Viewer         string
    IssuesTotal    int       // every state
    NewestIssue    int       // number of the most recently updated issue
    NewestUpdated  time.Time
    ProjectUpdated time.Time // zero without a board
    ProjectItems   int
    ServerTime     time.Time // the response's Date header; zero if absent or unparseable
}
```

Two tokens are equal when every field but `ServerTime` is equal.

What @lead-dev observed on the live repository and board:

- `Issue.updatedAt` moved on: label add and remove, assignee change, comment
  add, edit and delete, close and reopen, title and body edit, lock and unlock.
  It did not move on pin and unpin (not shown in lists) or on any project
  change. Milestone changes were not tested.
- `ProjectV2.updatedAt` moved on every board change tried through the API:
  Status set and cleared, the board's own workflow moving or adding a card,
  archive and restore, remove and add, and reorder inside a column. Changes
  made by dragging in the web UI were not tested.
- Both timestamps can trail the data by several seconds.

### One call

`ListIssues` (without search), per page:

1. If the call is marked fresh, or the store is nil: read GitHub, with no
   probe, and store nothing. Done. (Without a probe there is no token to store
   a page under; the next unmarked read stores it.)
2. Read the space's epoch. Probe. If the probe fails for any reason, read
   GitHub and store nothing: a failed probe is never evidence.
3. Look up the page. Use it when its token equals the probe's, its epoch equals
   the one read in step 2, and its `readAt` is under 10 minutes ago. Return it
   with `AsOf` set to its `readAt`.
4. Otherwise read GitHub. Store the page, with the probe's token and the epoch
   from step 2, only when **the quiet rule** holds: `ServerTime` is present, and
   `ServerTime - NewestUpdated` and (with a board) `ServerTime - ProjectUpdated`
   are each between 5 seconds and 10 years. A live read returns `AsOf` zero.

The probe is always sent **before** the read. If something changes between
the two, the page is newer than its token says, the next probe differs, and
the page is read again. A stored page is therefore never older than what its
token vouches for; it can be newer, which costs one extra read. The same
holds when two calls in one process interleave.

**Every page of an issue list is probed.** There is no remembered cursor chain:
a cursor is not a snapshot, and remembering one would let page 2 be served on
page 1's evidence. A 500-issue list unchanged costs 5 points, not 15.

`Board` does not probe and does not use the store for cards: it reads every
page from GitHub, as today. It is live for its columns too: every `Board` call
reads the board's fields and Status options from GitHub (one request), builds
the columns from that, and puts the result in the memo and the store, so the
status picker and the next command see it. Which project is the board, a
choice that almost never changes, is the only part `Board` takes from the
remembered structure.

This replaces the one-minute timer the board's structure had. Measured on a
TUI left idle on the issue list, that timer re-read the board's structure on
every refresh and cost more than the list itself. Everything except `Board`
now reuses the board's structure for 5 minutes, and an unknown status name
still triggers a refresh before a command is rejected (section 6).

`ListIssues` asks for board placement when a board is selected, so an issue
list row shows its board status. Its token therefore includes the project
half, and any board change makes the stored list pages unusable. Without a board the probe is sent with
`withProject: false`. If the project half is refused (a missing scope), the
call is read from GitHub and not stored; the issue list must keep working
exactly as it does today.

### `AsOf` and the TUI

`domain.Page[T].AsOf` says when the data was really read from GitHub: the
`readAt` of a stored page. Zero means "by this call".

What `AsOf` protects, and what it does not (@frontend-dev traced this through
the store):

- **Each issue's fields.** The TUI stamps every issue it puts in its store with
  the page's `AsOf` when set, and with its own send time otherwise. A cached
  page therefore cannot replace an issue the TUI has read more recently, in a
  detail view or from the live board. The same stamp is used to release an
  uncertain change, so a cached page can never settle one. One helper returns
  the stamp for both, so they cannot drift apart.
- **The list itself.** Membership, order, totals and the cursor come from the
  page, not from per-issue stamps. So the issue list keeps its own `DataAt`:
  the oldest `AsOf` of the pages it was built from. A load whose result is
  older than what the list already holds changes nothing in the list; it only
  moves the time the list was last checked. A cache hit then means exactly
  what it should: GitHub's token says nothing changed since the data you
  already have.
- **What protects the list after our own changes is the epoch**, not `AsOf`:
  no page stored before a write through `todos` is ever used.

The reads the TUI uses to settle an uncertain change, its delayed re-read, the
detail views and the board are never cached.

**The header tells the truth.** For the issue list the TUI keeps two times:
`CheckedAt` (when it last asked GitHub, by probe or by read) drives the
60-second refresh; `DataAt` drives everything the user reads. The header shows
the data's real age, `synced 4m ago`, and from 100 columns adds
`checked just now` when the check is newer. `synced just now` appears only
when the data was read by that check. A manual `r` is fresh, so it always ends
in a real `synced just now`. The board and the pull request list are live and
keep today's header.

**A limit that is not new, only wider.** Every page is probed, so page 1 can be
a hit and page 2 a live read. An issue updated between the two can then be in
neither page until the next load. The same race exists today across the half
second between two page requests; with the cache its window is the token's
lag, a few seconds.

## 6. Invalidation

1. **Our own writes.** Every service method that can change GitHub bumps the
   content epoch before it sends anything and again when it returns, whatever
   the outcome, including an uncertain one. The complete list: `CreateIssue`,
   `EditIssue`, `UpdateAssignees`, `Reassign`, `UpdateLabels`, `Close`,
   `Reopen`, `Comment`, `Delete`, `SetStatus`, `BoardAdd`, `RemoveFromBoard`
   and `RunQuickAdd`. `RunQuickAdd` builds its own run-local core, so it bumps
   itself: before its first write, after every item, and when it returns. This
   matters because our own write's timestamp can trail the data like anyone
   else's: without the bump, `todos quick-add` followed by `todos list` could
   be answered from the page stored before the items existed. A page records the epoch read
   before its own request was sent, and is used only while that epoch is still
   current. So a read that was in flight across a write can finish and store
   its page, in this process or another, and the page is simply never used.
   There are no locks.

   If a bump cannot be written (a read-only directory, a full disk) while
   older pages are still readable, the service stops using stored content for
   the rest of the process.

   The epoch is per credential, not per repository: a write through `todos`
   invalidates all content cached for that credential. Writes are rare next to
   reads, and a project board can be shared by several repositories.
2. **Other people's writes.** The probe, then the 10-minute limit.
3. **Structure that is missing.** The service already calls `RefreshMetadata`
   once before it rejects an unknown label, user or status. `RefreshMetadata`
   drops the memo, records the moment, and ignores any stored structure whose
   `readAt` is before that moment. Because `readAt` is the send time, a file
   written late by another process from a read that began earlier cannot bring
   the old answer back.
4. **Structure that is gone.** When a write fails as not found or as invalid
   input, the service drops its structure entries, so the next command reads
   the truth.
5. **Asked for.** Fresh is a time, not a flag: `domain.WithFreshSince(ctx, t)`
   means nothing whose read was sent before `t` may be used. The first
   structure read of a command then goes to GitHub and the nested reads of the
   same command reuse it, so asking for fresh never costs more than running
   with an empty cache. The CLI marks the command's start for the new global
   flag `--fresh`. The TUI marks the moment `r` was pressed and carries that
   same moment through every page and structure read of the refresh. `doctor`
   marks its own start, and reads and writes no cache file.
   `TODOS_CACHE=off` sets no mark: it only removes the store, and the
   in-process memo works as it did before the cache existed.
6. **A different credential.** Section 7.

## 7. Storage (`internal/cache`)

- Root: `$XDG_CACHE_HOME/todos/v1/`, default `~/.cache/todos/v1/`.
- A *space* is a directory under the root. The service uses two per
  credential: `<credential>/content` and
  `<credential>/structure/<host>/<owner>/<repo>`.
- `<credential>` is `Backend.CredentialKey`: the hex SHA-256 of a fixed prefix,
  the normalised host and the resolved token, computed inside `github`. The
  token never leaves that package. A GitHub token is high-entropy, so its
  digest is not reversible; it is still treated as private (never logged or
  printed). It follows the client's own credential resolution, so it is right
  when `GH_TOKEN` is set, and it changes when the client changes credentials.
  Two tokens for the same login can see different things, which is why the
  login is not the partition. The probe's `viewer` is stored with content as a
  second check; a mismatch is a miss.
- One file per key: `<space>/<sha256(key)>.json`, holding a version, the full
  key (a hash collision or a stale version is a miss), the stored time, and
  the payload. Content payloads also carry the token and the epoch.
- The key of a content page: method, host, owner, repository, the whole
  effective filter after the service's normalisation, board ID, page size and
  cursor. The key excludes the token, so a page is overwritten in place and
  files do not pile up per token.
- The epoch is a small file in the space holding a random value; `Bump` writes
  a new one. A missing epoch file reads as a fixed "none".
- Writes are atomic (temporary file in the same directory, then rename).
  Directories are `0700` and files `0600`; an existing directory with wider
  permissions is tightened, and if that fails the cache is not used.
- Limits: an entry over 4 MB is not stored. At most once a day, recorded in a
  marker file at the root so that separate commands share it, the store
  removes files not modified for 24 hours and credential directories not
  modified for 30 days. Epoch files are never swept.
- A directory that is not owned by the current user is not used.
- The structure space is keyed by the repository's canonical name as GitHub
  returns it, so a repository reached by an old name after a rename shares one
  entry with its new name.
- A `readAt` in the future is a miss. Ages are measured on the local clock, so
  a local clock that runs slow stretches every limit by the same amount; the
  file's modification time is never used as evidence of age.
- No in-memory copy of content: one copy on disk, so an epoch bump in any
  process counts for all of them.
- It holds issue list rows (titles, labels, logins, numbers, board status)
  and structure, never issue bodies, comments or board cards. What is read back is ordinary domain data and goes
  through the same sanitising as a live read before it is drawn or printed.
- **It can never fail a command.** `TODOS_CACHE=off` turns it off. go-gh's own
  HTTP cache stays disabled. `todos doctor` reports the cache directory and
  whether it is usable, without creating it.

## 8. What an agent sees

The cache must not be invisible to the programs that read `todos` output.

- `todos list --json`, `todos view --json` and `todos board --json` each gain
  one field, `as_of`: the time their data was read from GitHub. For a list it
  is the oldest read time of the pages returned, where a live page counts from
  the moment its request was sent, never from when the command finished. For
  `view` and `board`, which are always live, it is the moment their first
  request was sent.
- Format: RFC 3339 in UTC with millisecond precision, so a list and a view
  made in the same second can still be ordered.
- The rule for agents, stated in the skill: for the same issue, the document
  with the later `as_of` is the newer one; when they are equal, prefer
  `todos view`. Text output does not change.
- The skill and the README state the rule in section 2 in two sentences: issue
  lists can trail a change made elsewhere by a few seconds and are never
  reused past 10 minutes; `todos view`, `todos board` and `--fresh` are always
  live.

## 9. What it saves

Measured costs: issue list page with board placement 3 points, board page 2,
probe 1.

| Case | Today | With the cache |
|---|---|---|
| Idle TUI on a 500-issue list, per hour | about 900 points | about 300 probes + 6 full reloads: about 390 |
| Idle TUI on a 100-issue list, per hour | about 180 | 60 probes + 6 reloads: about 78 |
| Idle TUI on a board, per hour | 60 reloads x 2 points a page | the same: the board is live |
| `todos list`, nothing changed | structure reads + 3 points a page | 1 probe a page |
| Any CLI command within 5 minutes of another | 4 to 6 structure reads | 0, or the board's 1 to 2 after a minute |
| A repository that changes constantly | n | n + 1 probe per call |

On this repository, with 14 issues and 12 cards, content caching saves one or
two points a list read; the structure cache saves more. The content cache
earns its keep on large issue lists and on TUIs left open on one.

## 10. Build order

**Phase 10: structure.** `internal/cache`; `CacheStore`; `CredentialKey`;
the service's metadata memo made persistent with the times in section 3;
`WithFreshSince` honoured by the memo; `--fresh`; `TODOS_CACHE=off`; the `doctor`
line.

In the TUI, "fresh" means exactly "the user pressed `r`". A new reason,
`ReasonManual`, is used only by the `r` key and by recovery from an auth or
setup error; the read slot wraps its context with `WithFreshSince` and the time
of the key press for that reason, so every page of the refresh carries the
same moment. Comment pages take the reason of the
load that started them. `r` also reloads labels and assignable users, fresh,
when they are already in the store, so the pickers show GitHub's current
lists. Automatic refreshes, re-reads, prechecks and a project switch are not
fresh.

**Phase 11: content.** `ChangeToken` and its document; the read path in
section 5 for `ListIssues`; `Board` healing stale structure; epochs and the
bumps in every write method listed in section 6; the quiet rule; `AsOf` on
pages in `domain`; in the TUI, the stamp helper, the list's `DataAt` and
`CheckedAt`, and the header; `as_of` in the three JSON documents; the skill
and README lines of section 8.

Each phase ends with a measurement on the real repository: requests, points
and elapsed time for a cold and a warm `todos list`, `todos board` and
`todos view`. Phase 11 also measures a TUI left idle on the issue list for ten
minutes, with the cache on and off.

## 11. Tests the phases must include

No network, as everywhere else: a fake backend, an injected clock, a temporary
directory.

- A quiet cached page, then a changed row with an unchanged token: the page is
  served (this is the accepted limit) and carries its old `AsOf`; the TUI does
  not let it replace an issue read later than that `AsOf`.
- A changed token, an expired page, a bumped epoch, a different credential, a
  different filter, a different page size: each is a miss.
- A read held open across a write, in this store and through a second store on
  the same directory: its page is stored and never used.
- An uncertain write bumps the epoch. No write is ever sent twice.
- Fresh with a warm memo and a warm file reads GitHub once for every structure
  value and for every page, and never twice within one command. `--fresh` and
  `TODOS_CACHE=off` make no more backend calls than a run with an empty cache.
  Expiry is not extended by layering.
- `RefreshMetadata` followed by a file written by "another process" with an
  older stored time: a miss.
- Probe failure, a refused project half, a missing or unparseable `Date`,
  timestamps in the future: read GitHub, store nothing, return what GitHub
  returned.
- Unwritable, corrupt, oversized and wrong-version files; an unusable
  directory; `TODOS_CACHE=off`; cancellation stops promptly.
- `doctor` reads GitHub for everything and creates no file.
- TUI, fresh: a fake service records `FreshSince` per call. After `r` on each
  view every call of that refresh carries the same moment, including later pages and comment
  pages; after an automatic tick, a re-read, a precheck or a project switch,
  none is.
- TUI, content: a page sent at t1, a detail read sent at t2 > t1 that finishes
  first, the page stored after it, then served from the cache: the detail's
  issue is not rolled back. A cached list older than what the view holds
  changes no numbers, totals or order; only `CheckedAt` moves. A cached list
  page whose `readAt` is before a live board read leaves the card in the
  board read's column. A hit with an older `AsOf` neither releases an
  uncertain change nor unblocks a held queue. After a close, a label change or
  a create through `todos`, an automatic refresh uses no page stored before
  it. Header text for a live read, a hit and a manual `r`, at 80 and 140
  columns.
- `as_of` for a live list (its first request's send time, not the end of the
  command), an all-cached list (the oldest `readAt`) and a mixed one (the
  oldest of all).
- `Board` sends its item requests whatever the store holds, and reads the
  board's fields from GitHub exactly once per call, with a warm memo and a warm
  file; the fields it read are what the next `BoardInfo` returns. A list read
  within 5 minutes of another reads no board structure.

## 12. Decisions taken against the alternatives

| Question | Decision | Why |
|---|---|---|
| Timer or probe for content | Probe | A timer shows other people's changes late by design. |
| Claim the probe proves freshness | No | Observed timestamp lag. The promise in section 2 is the honest one. |
| Remember a cursor chain | No, probe every page | A cursor is not a snapshot; simpler; costs 4 points on a 500-issue list. |
| Wrap the backend or cache in the service | In the service | A wrapper cannot see or bypass the service's memo, and writes by node ID carry no repository. |
| Partition by HMAC with a key file | No, plain SHA-256 | The key file sits beside the cache and adds a lifecycle for no gain. |
| Board structure on a 1-minute timer | No. `Board` reads the fields live every call; everything else reuses them for 5 minutes | Measured: on an idle issue list the 1-minute timer cost about 28 of 41 points in ten minutes. Reading the fields inside `Board` makes the board's columns live, which is what Peter asked of the board, and frees every other command from the timer. |
| Cache board cards | No | Peter's decision: the board is live. Measured, an unchanged 1,000-card board would have cost 1 point instead of 20. |
| Cache pull request lists | No | Checks change without moving the timestamp. A short timer is a later option. |
| Serve stale data when GitHub refuses | No | A rate-limit or auth error is shown, not hidden. |
| Web UI drag and milestone changes | Unverified | They affect only the board-status and milestone a list row shows; covered by the 10-minute limit and by `AsOf`. Worth a manual check by Peter. |
| What the TUI header says after a cache hit | The data's real age | @frontend-dev: `synced just now` for a page read nine minutes ago would be untrue. |
| Tell agents how old a list is | Yes, `as_of` | @reviewer: a cached list beside a live `todos view` would otherwise contradict it with no way to tell which is newer. |
