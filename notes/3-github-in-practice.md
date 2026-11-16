# 3. GitHub in practice

These are observations from building and running todos, checked on 2026-11-16
against the code at `5f49e6b` and the saved experiments described below. They
are not GitHub API guarantees. The important results are copied here because
the original temporary files and ignored reviews may disappear. The `/tmp`
evidence paths are historical records on the original machine and cannot be
re-checked from a clone; [note 4](4-core-build-notes.md) explains how to repeat
the measurements.

For the final rules, read [functional_spec.md](../specs/projects/todos/functional_spec.md)
and [cache.md](../specs/projects/todos/components/cache.md). The companion
[core notes](4-core-build-notes.md) explain the implementation traps.

## A project workflow is another writer

Our test board automatically added new issues to Backlog, moved closed issues
to Done, and closed issues moved to Done. Those changes happened separately
from our request. An acknowledged move to Done could still return an open
issue; a later read returned it closed as completed.

Evidence: the retained live mutation transcript for throwaway issue #15 has
`board_status: "Done"` with `state: "open"` in both the move acknowledgement
and an immediate view. The later `19-workflow-state.json` returns closed /
completed. The same transcript covers restoring its archived card; issues
#15 and #16 were subsequently deleted. These records are under
`/tmp/todos-phase5-live/` on the original development machine.

Do not convert a later workflow change into an edit conflict, retry a
successful write, or try to keep an acknowledged state true forever.
The write result is what GitHub acknowledged at that point.

Code: [service/changes.go](../internal/service/changes.go),
[service/board_changes.go](../internal/service/board_changes.go).
`TestMutations_AcknowledgedNoRereadAndWorkflowNormal` in
[changes_test.go](../internal/service/changes_test.go) protects this distinction.
The delayed display reread belongs to the TUI, not to the service write.

## Auto-add can beat an explicit add

A new issue can acquire a project item between our absent-membership read and
our add request. GitHub's observed rejection was:

> Content already exists in this project

The recovery depends on that wording. The current predicate requires a
definite `invalid_input` rejection, not `Uncertain`, and that case-insensitive
phrase. Only then does the service read membership once more and use the
workflow's item. It never sends the add a second time. A timeout with the same
words must not enter this recovery.

Evidence: this branch was added after a live create-from-column failure.
The original pane failure itself is **not re-checked**; the retained
regressions reconstruct the race, including an archived workflow item and
a subsequent status-write failure.

Code: `duplicateProjectItem` / `placeIssue` in
[board_changes.go](../internal/service/board_changes.go);
[board_race_test.go](../internal/service/board_race_test.go), especially
`TestBoard_AutoAddRaceDoesNotRetryOtherFailures`.
The API classification is pinned by `TestAddProjectItem_DuplicateRejectionIsCertain`
in [github/changes_test.go](../internal/github/changes_test.go).

If our create succeeds but placement fails after finding the workflow's card,
the result must retain that real placement. The workflow's add is observed
state, not our own completed `added_to_board` step.

## “No status” is a request, not a durable policy

An add response can sample a null Status before the auto-add workflow writes
Backlog. Skipping a clear because that response was already null lost the
user's requested result. New cards therefore get an explicit clear when
creation requests No status, including quick add.

That fixes our missing write. It cannot prevent a workflow from writing
Backlog after the clear. A live retest reported the clear landing, followed
by Backlog within a few seconds; that retest's original pane log is
**not re-checked**. The documented residual is intentional, not a reason to
add corrective polling.

Evidence/code: [no_status_test.go](../internal/service/no_status_test.go)
stages both the stale-null add response and an already-auto-added card.
`TestNoStatus_NewCardClearsEvenWhenAddAcknowledgesNull` and
`TestNoStatus_QuickAddClearsWorkflowCardInsteadOfPreservingIt` assert the clear
and its pacing. [README.md](../README.md), “Quick add”, states the workflow limit.

Keep the related meanings separate: `BoardAdd(nil)` preserves an existing
card's status; explicit No status on a newly filed item clears it. Also,
filing a checked quick-add item without requesting placement does not prevent
GitHub's own workflows from adding it or moving it to Done.

## Timestamps lag behind data

A matching change token is evidence, not proof that nothing changed. The
clearest counterexample was deleting a comment on throwaway issue #85:

| Response Date, UTC on 2026-11-16 | Newest issue updatedAt | Comment count |
|---|---|---:|
| 18:40:21, before deletion | 18:40:13 | 1 |
| 18:40:23, deletion already acknowledged | 18:40:13 | 0 |
| 18:40:30 | 18:40:24 | 0 |

The repository total and project token also remained unchanged at the first
post-delete read. A board-free page read before deletion already met the
five-second quiet rule. That rule could not prove it safe against the next
write's propagation lag. In a second delete trial in the same experiment,
the timestamp moved immediately: updatedAt was 18:44:10 in the response
dated 18:44:11. The lag is not consistent.

Evidence: the saved `/tmp/todos-cache-review-20261116/` event/snapshot logs
and the experiment's contemporaneous record. The experiment used one issue,
deleted it, and checked that issues 1–14 still matched their initial readback.
The accepted behavior is pinned by
`TestContent_UnchangedTokenServesOldPageAndOriginalAsOf` in
[content_test.go](../internal/service/content_test.go).
Final policy: [cache.md §2 and §5](../specs/projects/todos/components/cache.md).

The same experiment observed issue updatedAt advance after label and assignee
changes, title/body edits, comments, close/reopen, and lock/unlock. It did not
advance for project placement/status/archive/reorder changes, or pin/unpin.
Project updatedAt eventually advanced for every tested API board operation,
including the board's own workflows. Immediate item counts sometimes lagged
too. Neither timestamp nor count is a synchronous revision number. Issue-list
rows include board placement, so their token still needs its project half even
though Board cards themselves are never cached.

## Labels propagated quickly in the later cache test

Six label changes on throwaway issue #86 were visible in the first list call
after `gh issue edit` returned. A warm, servable page existed before each
write. These are upper bounds ending when that first poll finished, including
process startup and two network requests:

| Change | Seconds after gh returned |
|---|---:|
| Add label | 1.252 |
| Remove label | 1.483 |
| Add label | 1.408 |
| Remove label | 2.036 |
| Add label | 1.542 |
| Remove label | 1.705 |

This does not measure sub-second propagation and does not establish a bound
for comments, other hosts or future GitHub behavior. Issue #86 was deleted;
the saved confirmation is HTTP 410.

Evidence: `/tmp/todos-phase11-xncs51lq/propagation.json` and its request log.
Code consuming the evidence is [service/content.go](../internal/service/content.go),
not an assumption that all mutations update timestamps immediately.

## Some tempting GraphQL shortcuts do not exist

The live schema check exposed only `POSITION` for ProjectV2 item ordering.
There was no “latest updated item” order suitable for a one-item board probe.
An experimental 100-item fingerprint scan cost one point, but still required
every page; it was not a constant-cost replacement for a whole board.
Board cards ultimately stayed live.

GitHub also cannot list issues by closed time. The CLI keeps updated-time
order; the TUI's closed-time order is derived over its loaded rows, not a
server-sorted, globally complete closed list. Do not casually replace the
query order with a nonexistent `CLOSED_AT` field.

Evidence/code: the schema result was recorded with the #85 experiment;
[board_items.graphql](../internal/github/queries/board_items.graphql) requests
position order. [functional_spec.md §4.1](../specs/projects/todos/functional_spec.md)
records the closed-order limitation;
[tui/order.go](../internal/tui/order.go) implements the derived order.

A project is also not a repository-scoped issue list. Its items include
foreign issues, pull requests, drafts and redacted content. Filtering those
locally is deliberate, and repository node IDs survive renames better than
matching display names.
Evidence: `TestBoard_ContentKindsAndRedaction` in
[github_test.go](../internal/github/github_test.go);
[service/board.go](../internal/service/board.go) counts hidden items.

## Access errors do not map neatly to “logged in or not”

Working issue access does not imply project access. A denied optional project
membership must not turn a readable issue into an error. Conversely, an auth,
rate-limit or issue-data failure must not be swallowed as “no board”.

Scope diagnosis is intentionally conservative: a refusal with no explicitly
named missing scope stays `forbidden`. Missing scope headers alone are not
proof of missing scope. Classic-token project reads and writes use
`read:project` / `project`; private-repository access also needs appropriate
repository permission. See [README.md](../README.md) for the supported setup,
not a claim that every fine-grained-token combination was tested.

Evidence/code: `optionalMembershipError` in
[github/issues.go](../internal/github/issues.go),
`TestIssueListMembershipNullBubble_Fallback` and
`TestScopeLessRefusal_Forbidden`. [github/errors.go](../internal/github/errors.go)
keeps auth, scopes, rate limits and privacy-preserving not-found messages distinct.

After an auth failure, a plain retry must re-resolve credentials. The failed
operation itself is not replayed. An environment token can still override a
new stored gh login; the hint names that override.
Evidence: [auth_retry_test.go](../internal/github/auth_retry_test.go), including
`TestAuthFailure_MutationIsNotReplayed`.

## Private installation has a separate authentication problem

Successful `gh auth status` is not sufficient for `go install`. Go's module
fetch/checksum path is separate from todos' API client. The successful private
installation setup used `GOPRIVATE=github.com/pkieltyka/todos` with existing git
access, a throwaway GOBIN/GOPATH, and a working directory outside the checkout.
No global git or Go configuration change was needed in that session.

The exact original install command/environment is **not re-checked**. The
retained installed binary was re-checked while writing these notes: its JSON
version is `v0.0.0-20261114174127-a33a677408f8`, matching the installed master
snapshot. Do not infer that another developer's git transport is configured
from this result.

Code/evidence: [README.md installation](../README.md),
[version.go](../version.go), and `TestVersion_GoInstallBuildInfo` in
[version_test.go](../version_test.go). That automated version test uses a
local file module proxy, not the private GitHub repository.

## Points, requests and latency are different measurements

These primary GraphQL costs were measured by adding `rateLimit { cost }` to
the actual documents, not inferred from elapsed time:

| Shape on this small repository | Requested size | Points |
|---|---:|---:|
| Issue list including board membership | 100 | 3 |
| Same list without board membership | 100 | 2 |
| Board items | 100 | 2 |
| Change-token probe, with or without project | 1 newest issue | 1 |
| Issue list including board membership, CLI default size | 50 | 2 |

The 100-item issue and board queries returned only 14 issues and 12 cards.
Requested connection sizes still matter; these are not 100-row throughput
tests. Mutation secondary-limit pricing is a separate budget from these
primary query costs.

For nonempty CLI lists of 14 issues, the counting-proxy results were:

| Command | Cold requests / points | Warm requests / points | Miss with warm structure |
|---|---:|---:|---:|
| `list --state all` | 6 / 7 | 1 / 1 | 2 / 3 |
| `list --state closed` | 6 / 7 | 1 / 1 | 2 / 3 |

A miss pays a probe and then a read: one extra point and one extra round trip
per page. Warm elapsed times were 0.653s / 0.614s; miss times were 1.662s /
1.506s. These include proxy/TLS overhead, not just application work.
Evidence: `/tmp/todos-phase11-nonempty-c0yqz88s/measurements.json`.
A read-only rerun on 2026-11-16 with the retained
[measurement tool](../tools/measure/measure.py) reproduced all request/point
counts in this table, again with 14 issues in each state.

Ten-minute, untouched TUI runs at 140×40 exposed the cost of metadata:

| Snapshot/policy | View | Requests | Points |
|---|---|---:|---:|
| Earlier one-minute board-structure expiry, cache on | Issues | 38 | 41 |
| Same earlier policy, cache off | Issues | 37 | 58 |
| Final five-minute structure / live Board fields policy | Issues | 27 | 30 |
| Final policy | Board | 28 | 38 |

All totals include startup. The default open Issues list was empty in these
TUI runs; the nonempty CLI rows above cover actual issue payloads. The final
Issues run had ten probes and one list read. Board had ten card reads and
eleven field reads: ten live Board reads plus the TUI's initial BoardInfo
read. The Board row was measured at `c54521b`, before `loadBoard` was
reordered to call Board first. That reorder removes one field read at startup;
the expected totals are ten field reads and 27 requests, not re-measured.

This is why the final design shares board structure for five minutes but
reads Board fields live on every Board call. Simply making list content
cheap left more quota spent on board metadata than on the list itself.
The cached header correctly said `synced 3m ago · checked just now` at 3:25
and `synced 9m ago · checked just now` at 9:25.

Evidence: `results.json` in `/tmp/todos-phase11-tui-idle-r78wtnmt/` and
`/tmp/todos-phase11-board-live-idle-qgn7qx3n/`. Relevant code:
[service/board.go](../internal/service/board.go),
[service/content.go](../internal/service/content.go).
Measurement instructions are in [the core notes](4-core-build-notes.md).

## What remains unverified

Authenticated web-UI dragging and within-column reordering were not tested;
the corresponding API operations were. Milestone set/clear was not tested
because the repository had no milestones. No live test deliberately exhausted
secondary limits, revoked credentials, or varied fine-grained-token grants.
The saved #85 experiment and [cache.md §5 and §12](../specs/projects/todos/components/cache.md)
state these limits.

Useful next experiments are those missing cases on an authorized throwaway
issue, and repeating point/latency measurements on a larger repository.
Do not turn the small-repository timings or six label trials into a
consistency guarantee, cache PR checks under an issue token, or fight board
workflows with repeated corrective writes.
