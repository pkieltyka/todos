# 4. Core build notes

Checked on 2026-11-16 against `5f49e6b`, the current tests, saved measurements
and the implementation reviews. These are the reasons and traps behind the
code, not a replacement for the component specifications. External GitHub
observations and their limits are in [GitHub in practice](3-github-in-practice.md).

## Test the right layer

The shared Service fake does not run the service. A green CLI or TUI test
against it cannot establish that real validation, partial results, pacing or
cache invalidation work.

| What is being changed | Test boundary |
|---|---|
| Domain ownership | Clone nested slices, maps, pointers; mutate the returned copy |
| GraphQL document, mapping or classification | Fake HTTP RoundTripper in github |
| Validation, write ordering, metadata or cache policy | Real service with fake Backend/SettingsStore |
| Atomic persistence and permissions | Temp directories in config/cache |
| Flags, output, exit codes | CLI against the shared direct Service fake |
| Service plus real file-store races | CLI integration test, where both packages are permitted imports |

Evidence: [servicetest/fake.go](../internal/service/servicetest/fake.go)
implements methods directly. [core_test.go](../internal/service/core_test.go)
has the separate backend fixture.
`TestContent_DiskReadHeldAcrossWriteNeverReusesOldEpoch` in
[cli/content_test.go](../internal/cli/content_test.go) uses two actual stores
over one directory; it is not another copy of the service cache algorithm.

Do not weaken [import_rules_test.go](../import_rules_test.go) to let service
import servicetest. The tempting “a package may import its own subpackages”
exception once allowed exactly that forbidden dependency.

## A value copy does not give the caller ownership

Issue copies still share AssigneeIDs maps, labels, placement pointers and
other nested values unless explicitly cloned. A fixture edit or optimistic
UI change can otherwise alter a service memo without another read.

Evidence/code: [domain/clone.go](../internal/domain/clone.go) and
`TestCloneIssue_IndependentNestedValues` / `TestCloneIssue_AssigneeIDsOwned`
in [domain_test.go](../internal/domain/domain_test.go).
The service and shared fake clone at their return/storage boundaries.
Keep that ownership when adding fields, not just the outer struct copy.

## Removal and addition cannot share one name validator

The original validation rule would make remove-all fail forever for an
assignee who had left the organization: the issue still named them, but the
assignable-user list did not. Removals now obtain IDs from the issue's own
memberships. Repository metadata is only a fallback for deciding whether an
absent removal is a known no-op. Additions still require full validation.

Evidence: `TestRemovals_DepartedAssigneeUsesIssueID` deliberately gives the
repository a different ID for the same login and checks that the issue ID
wins. `TestRemovals_DeletedLabelUsesIssueID` stages the analogous metadata
gap; it is a backend fixture, not a live claim that deleting a GitHub label
leaves it attached to an issue.

Code: [service/validation.go](../internal/service/validation.go),
[changes.go](../internal/service/changes.go), [changes_test.go](../internal/service/changes_test.go).
This is why Issue keeps assignee IDs even though the public CLI JSON exposes
login names.

Successful decoding is also not sufficient evidence that GitHub applied
every requested assignee. The tests stage a successful response that ignored
a membership change; the service checks the returned set and stops before
the next write. See `TestAssignment_IgnoredRemovalStopsBeforeAddition`.
No live permission-removal experiment was needed to test that defense.

## A successful write must not become a failed write because of reporting

The service returns acknowledged state. An extra read after every write
would add latency and could turn a successful write followed by a network
failure into an apparent rejection. Workflow effects are handled by later
reads; they do not justify replaying a mutation.

Evidence: `TestMutations_AcknowledgedNoRereadAndWorkflowNormal` in
[changes_test.go](../internal/service/changes_test.go).
Likewise, `TestOutputFailure_RealWriteNeverRetries` in
[cli/changes_test.go](../internal/cli/changes_test.go) verifies that failing
stdout does not repeat a write.

Do not collapse `partial` into a generic failure or discard its Issue.
A confirmed create followed by a failed close/placement is still a created
issue. This matters particularly to recovery from quick add.
The final error contract is [functional_spec.md §6.5](../specs/projects/todos/functional_spec.md);
[github/errors_clients_test.go](../internal/github/errors_clients_test.go)
distinguishes started ambiguous failures from explicit rejections and
pre-start cancellation.

## Quick add's source is the recovery record

Reconstructing Markdown from parsed titles and bodies loses the exact bytes
that identify an unsent item. Preserve Source, CRLF, indentation and span
hashes. Do not use titles as unique identities: duplicate titles are valid.
The run reparses and validates plan.Source rather than trusting mutable
plan.Items supplied by a caller.

Evidence/code: [quickadd/remaining.go](../internal/quickadd/remaining.go),
`TestQuickAdd_RemainingVerbatim` and `FuzzParse_SourceSpansDeterminism` in
[parse_test.go](../internal/quickadd/parse_test.go);
`TestQuickAdd_ReparseAndWholeBatchValidation` in
[service/quick_add_test.go](../internal/service/quick_add_test.go).

An item whose result is `created` is removed from the remaining text even
when its later close or placement failed. Returning that title to the unsent
draft would duplicate the issue on resume. An uncertain create stays in the
remaining text with a may-exist span.

Progress callbacks are synchronous boundaries, not fire-and-forget UI
notifications. A callback error must stop before the next write. In-flight
state must survive closing/placing until the item settles; a failed
settled-save must not erase uncertainty.
Evidence: `TestQuickAdd_OnProgressSaveFailureStops` and
`TestQuickAdd_SettledSaveFailurePreservesUnknownOutcome` in the service tests;
[config/drafts.go](../internal/config/drafts.go) promotes InFlight to Uncertain
on load without rewriting the file.

## Pace writes, not items, and own the pacer per run

A create/close/place/restore/clear sequence contains several writes. A delay
once per item leaves the other writes unpaced. The interval is measured from
the end of the preceding write, and a long read/callback may already consume
it. Sleeping a fixed second regardless would waste time.

Evidence/code: [quick_pace.go](../internal/service/quick_pace.go);
`TestQuickAdd_PerWriteOneSecondRunLocal`,
`TestQuickAdd_LongReadTimeNeedsNoExtraWait` and
`TestQuickAdd_PacerWaitChecksElapsedAndCancel`.
The clock and wait are injected; these tests do not sleep.

Assert actual write names and ordering, not only a count. The restore/clear
test once had two branches both expecting three writes, so it could not tell
which sequence happened. `TestQuickAdd_PacesRestoreAndClear` now pins
create/add/clear versus create/unarchive/set.

The run-local core has two other easy-to-miss obligations:

- Its refusal observer must invalidate its own copied metadata as well as the
  parent's. Otherwise a label deleted during the run survives in the run memo.
  `TestStructure_QuickRunRefusalDropsBothMemos` pins this.
- Its epoch bumps must surround the batch and settled items as well as the
  public single-issue calls. `TestContent_QuickAddBumpsRunAndEachSettledItem`
  pins it, including no bumps for dry/all-skipped runs.

Code: [quick_add.go](../internal/service/quick_add.go) and
[structure_writes.go](../internal/service/structure_writes.go).
Do not install the run pacer on the shared service; ordinary writes must remain
independent of a paused quick-add run.

## Config and drafts are not disposable cache

An unreadable or malformed user file must not be silently replaced with
defaults on the next save. Both Save and Delete of a draft check the existing
file before changing it. A draft error disables normal recovery; it does not
authorize discarding the original bytes.

Evidence/code: `TestConfig_ReadParseFailureEmptyButNeverOverwritten` in
[config_test.go](../internal/config/config_test.go), and
`TestDraft_UnparseableNeverOverwrite` /
`TestDraft_MalformedMarkersOnDiskPreserved` in
[drafts_test.go](../internal/config/drafts_test.go).

Draft hashes and offsets refer to UTF-8 byte spans, not rune indices or a
reserialized item. Bounds, overlaps, hash mismatches and unknown draft fields
are rejected. Config/draft writes sync the file and parent directory;
cache writes only need an atomic disposable replacement.
Compare [config/atomic.go](../internal/config/atomic.go) with
[cache/store.go](../internal/cache/store.go) before sharing a helper.

There are deliberately no cross-process draft conflict checks or file locks.
Revision is not a compare-and-swap token. The tests explicitly establish
last-save-wins. Do not restore the earlier conflict-detection proposal from
an old discussion; see [architecture.md §9](../specs/projects/todos/architecture.md).

## readAt must be taken before the request

The dangerous case is an old read completing late: another process begins
a read, this process refreshes metadata, then the other process writes its
old answer after the refresh. Store-time would make it look new enough.
Request-send time makes the refresh cutoff effective.

Evidence: `TestStructure_ReadBeforeRefreshStoredAfterIsMiss`,
`TestStructure_AnotherServiceReadHeldAcrossRefresh` and
`TestStructure_ReadAtPrecedesBlockedGitHubRead` in
[structure_test.go](../internal/service/structure_test.go).
A file hit must also keep the original expiry; loading it into memory must
not restart another full TTL.

That same timestamp protects TUI/agent ordering. A hit carries its original
Page.AsOf, not the time of its latest probe. A live service page leaves AsOf
zero; the CLI stamps the time before the service call, which is earlier than
the eventual GitHub content request by the time the probe takes. That is the
accepted direction ([cache.md §8](../specs/projects/todos/components/cache.md)):
a stamp may be early and must never be late. A list document takes the oldest
page time, not the newest or command completion time.

Code: [service/core.go](../internal/service/core.go),
[service/content.go](../internal/service/content.go),
[cli/collect.go](../internal/cli/collect.go).
`TestAsOf_ListUsesOldestSendOrCachedReadTime` covers live, cached and mixed
pages. Final contract: [cache.md §4–5 and §8](../specs/projects/todos/components/cache.md).

## Fresh is a time, not a boolean

A boolean “skip every memo hit while fresh” made nested reads more expensive
than having no cache. A scratch revert on a three-page list without a file
store read Repository and Viewer nine times each, and board resolution three
times. The final mark rejects only values read before the command/refresh
started, allowing that command's new values to be reused.

Evidence/code: [domain/fresh.go](../internal/domain/fresh.go),
`TestStructure_FreshNestedReadsReachEachBackendOnce` and
`TestStructure_NoFileStoreKeepsOriginalMemoRequestCounts`.
The nil-store case matters: a file store can mask a broken memo policy.

`TODOS_CACHE=off` sets no fresh mark and retains the finite process memo.
Doctor instead has a fresh, request-local memo and shares its Viewer read
with Session. Removing all memoization for doctor once multiplied session
reads; using the long-lived memo would make the diagnostic stale.

Evidence: `TestStructure_DoctorReadsEachStructureOnce`,
`TestStructure_DoctorBypassesWarmCacheAndWritesNothing` and
[cli/cache_test.go](../internal/cli/cache_test.go).
The cache diagnostic examines directory metadata only; constructors do not
create the directory. Final rule: [cache.md §6](../specs/projects/todos/components/cache.md).

## Invalidation cannot just delete files

A held read can store an old page after both a pre-write and post-write
deletion. Epochs solve that: the fill retains the epoch read before its probe,
and a subsequent read rejects that old epoch even if the file arrived late.
Do not move that epoch read to after the probe.

Evidence: `TestContent_EpochIsReadBeforeTheGatedProbe` in
[content_test.go](../internal/service/content_test.go), and the two-real-store
test in [cli/content_test.go](../internal/cli/content_test.go). The earlier
tests caught changed epochs but did not catch that ordering edit; the gated
probe test was added specifically to make the distinction observable.

Every public write bumps on entry and return, including uncertain outcomes.
A failed bump disables content use for the process and attempts to drop the
whole credential content space for other processes. Drop is still best effort,
not a transaction. The process flag stays disabled even after a later bump
succeeds.

Code/evidence: `beginChange` / `bumpContent` in
[content.go](../internal/service/content.go),
`TestContent_FailedBumpDropsPagesForOtherProcesses` and
`TestContent_FailedBumpDisablesStoredContentProcessWide`.
Do not replace this with per-repository invalidation: project writes can
affect the list placements of more than one repository.

## Cache mechanics must not change ordinary errors or identities

Two small regressions were easy to miss in happy-path tests:

- Looking up CredentialKey before ordinary repository resolution made cache-on
  show auth where cache-off showed no_repo. A failed cache setup now yields no
  space and lets the ordinary fetch choose the error.
- An old repository name could produce a canonical session that rejected its
  own file identity. Store under GitHub's canonical name once known. A new
  process using an old alias may still need one live session read; aliases
  themselves are not persisted.

Evidence: `TestStructure_CacheCannotChooseTheReadError` and
`TestStructure_RenamedRepositoryUsesCanonicalSpace`.
Code: [structure.go](../internal/service/structure.go) and
[core.go](../internal/service/core.go).

Keep the post-fill partition lookup even though a generation check follows
it: that lookup is what advances the generation when credentials changed
during the fill. Removing only a redundant comparison is different from
removing the lookup. `TestStructure_CredentialChangedDuringFillIsNotStoredInOldPartition`
pins that case. Partition by the actual credential, not the login.

## Board freshness changed because the measurement contradicted the intuition

A one-minute structure timer sounded cheap, but an idle Issues view spent
more points re-resolving board structure than probing/listing issues.
The final split is five-minute metadata reuse, plus live fields/options on
every Board call. A cold Board call already reads fields during resolution;
do not add a second field read to it.

Evidence: the idle tables in [GitHub in practice](3-github-in-practice.md).
Code: `boardInfo` / `refreshBoardFields` in
[board.go](../internal/service/board.go).
`TestBoard_FieldsLiveExactlyOnceAndSharedWithMetadata` tests both warm memo
and warm file, including publication for another BoardInfo reader;
`TestBoard_ColdCallReadsFieldsOnlyOnce` and
`TestStructure_UseBoardSharesFiveMinuteExpiry` protect the other paths.
Final rule: [cache.md §3 and §5](../specs/projects/todos/components/cache.md).

The old unknown-option healing pass was removed. Do not put it back on top
of the unconditional field read; that would add another request when a
column changes during the cards read. Unknown names on writes still get the
normal single metadata refresh.

## Small CLI changes can cross the scripting boundary

A literal body or filename equal to `--json` once exposed the danger of
hand-scanning argv for output mode. The parsed Cobra flag is authoritative
after successful parsing; the failure fallback derives value-taking flags
from Cobra rather than maintaining another list.

Evidence: `TestJSON_FlagValueEqualsJSON` /
`TestDetectJSON_ValuesAndEndOfFlags` in
[root_test.go](../internal/cli/root_test.go) and
`TestChanges_ValueEqualJSONIsBody` in [changes_test.go](../internal/cli/changes_test.go).

Piped TSV is still terminal-facing text: short GitHub fields go through
SanitizeLine even when stdout is not a TTY. Only JSON retains raw domain
strings for machines. Never move sanitization exclusively into the terminal
table renderer. Code: `tsvField` in [render.go](../internal/cli/render.go);
`TestMaliciousText_AlwaysSanitised` in [reads_test.go](../internal/cli/reads_test.go).
The Markdown boundary itself is covered in the frontend notes.

Checkout builds also do not reliably print `dev`. The Go 1.27.3 toolchain
supplies a VCS version stamp via build information, and that was deliberately
kept. Test version policy through [version.go](../version.go) /
[version_test.go](../version_test.go), not by asserting every local build is dev.

## Accepted limitations still worth knowing

These were identified while checking the implementation and remain intentional
or deferred; the previous sections describe implemented guards, not open bugs.

- A proxy that strips Date causes a probe followed by a live read on every
  list page, without a usable fill. Repeated refusal of the optional project
  probe can have the same cost. There is no adaptive probe circuit breaker.
  The simpler live fallback was accepted; never “optimize” it by serving a
  page after a failed probe. See `quietToken` / `issuePage` and
  `TestContent_ProbeFailuresReadLiveAndNeverStore`.
- `contentDisabled` is package-global. The service test fixture resets it
  and those tests are not parallel. Adding `t.Parallel()` without isolating
  that state would make tests interfere.
- An owner-writable, hand-made cache entry with a valid envelope and valid
  issue IDs could contain another repository's rows. The cache is private
  disposable storage, not an authenticated database; the payload check is not
  a second live repository-membership query. Output still takes the ordinary
  sanitizing path. See `validIssuePage` and [cache/store.go](../internal/cache/store.go).
- Cache age uses the local wall clock. Rejecting a future readAt does not
  prevent a slow clock stretching a TTL. Do not substitute file mtime as a
  consistency fix. [cache.md §7](../specs/projects/todos/components/cache.md)
  records that accepted limit.
- Sweeping is best effort. The root marker avoids a full walk in each CLI
  process, but simultaneous first writers may both sweep. Active-space epochs
  are not ordinary 24-hour garbage. See
  `TestStore_SweepMarkerSharedAcrossProcesses` /
  `TestStore_PrunesOnceWithoutRemovingEpochs` in
  [store_test.go](../internal/cache/store_test.go). This is not a strict total
  disk quota or a reason to add locking.

## How to exercise the core

Normal tests need no GitHub account. Start with the actual affected package;
`make check` supplies the formatting, vet and race check for the whole module.
For parser changes, the existing fuzz target is useful:

```sh
go test ./internal/quickadd -run '^$' -fuzz FuzzParse_SourceSpansDeterminism -fuzztime=10s
```

For shared-fake tests, register a gate before starting the command, wait for
`Gate.Started`, then decide it. `Release` proceeds, `Fail` rejects without
applying, and `ApplyThenFail` models an applied write whose acknowledgement
was lost. Use `Fake.Mutate` for an external edit and `Fake.SetRateLimit` for
quota changes. The call log owns copies of arguments plus start/end order;
assert actual order/overlap rather than sleeping and hoping.

Code/examples: [servicetest/gates.go](../internal/service/servicetest/gates.go)
and `TestFake_ApplyThenFailChangesFixture` /
`TestFake_ArgumentsAndCallOrderOwned` in
[fake_test.go](../internal/service/servicetest/fake_test.go).
Gate cancellation before dispatch is a certain pre-start failure; it is not
the same as cancelling an already-sent HTTP mutation.

Read, write and quick-add CLI goldens all use the same historical flag:

```sh
go test ./internal/cli -update-read-goldens
```

Despite its name, that flag also rewrites `testdata/changes` and
`testdata/quick_add`. Review the diff by output mode. A layout fix must not
silently change JSON/TSV schemas. The foundation/version goldens use their
own explicit files and are not regenerated by that flag.
`TestSkillMatchesCLI` checks command/flag examples against Cobra; it does
not replace semantic checking of every prose statement in the skill.

Read-only live coverage requires an explicit target:

```sh
TODOS_LIVE_TESTS=1 TODOS_LIVE_REPO=pkieltyka/todos go test ./internal/cli -run '^TestLive' -count=1
```

Set `TODOS_LIVE_PROJECT=OWNER/NUMBER` when needed.
[live_test.go](../internal/cli/live_test.go) gives the harness a read-only
interface and skips unavailable boards appropriately. It does not test live
mutations, the content cache, or every possible pagination boundary:
`newLiveReads` constructs a service without a CacheStore.
The separate authorized write transcripts are described in the GitHub note.

## How the request/point measurement was made

The retained read-only tool is [tools/measure/measure.py](../tools/measure/measure.py),
cleaned up from the original temporary CLI scripts. It builds a temporary
binary (or accepts `--binary`), runs `list --state all` and `list --state closed`
in cold, warm and content-miss cases, and prints request/point/time rows.
It requires Python 3, OpenSSL, Go for a build, and credentials inherited by
the child process. With `GH_TOKEN` or `GITHUB_TOKEN` already set, run:

```sh
python3 tools/measure/measure.py --repo OWNER/REPO --output measurements.json
```

Each state uses its own throwaway XDG_CACHE_HOME, removed on exit. The proxy
accepts only a single named GraphQL query per request; mutations and other
HTTP operations are refused. It retains counts and operation names, with no
headers, credentials, response bodies or cache partition identifiers. This
script covers CLI measurements; the historical idle TUI runs used the manual
steps below.

The proxy measured the real built binary, not a reconstruction of its
requests. A raw single query misses startup, nesting and concurrent cold
fills: the idle Issues trace actually had three Repository and three Viewer
reads at startup.

1. Build a snapshot binary in a temporary directory and record HEAD, dirty
   state and binary hash. Use a fresh throwaway XDG_CACHE_HOME for each case.
2. Run a loopback HTTPS CONNECT proxy, preserving the logical api.github.com
   host. Trust its temporary certificate only in the child process with
   SSL_CERT_FILE and HTTPS_PROXY. Leave normal machine configuration alone.
3. Forward real query variables and add `rateLimit { cost used remaining resetAt }`
   at the query root. Count operations/requests and sum cost; record elapsed
   time separately. Reject mutations in a read-only run.
4. Keep authentication in child environment/in-memory forwarding. Never print
   headers, tokens or the credential-derived cache space.
5. Compare cold and warm invocations. To measure a content miss with warm
   structure, remove only the temporary content entries. `--fresh` is not a
   miss simulation: it skips the probe altogether.
6. For idle TUI measurements, use a private detached tmux server at 140×40.
   Obtain the actual pane ID from `new-session -P -F '#{pane_id}'`; do not assume
   window/pane index zero. The first attempt aborted at its capture because
   of that assumption. Hide tmux's status row and verify pane dimensions.
   Capture at the desired times, then send only `q` to that private pane.
7. Preserve a per-operation log, counts, point sums, screenshots/text captures
   and the binary identity. Copy important results into a durable note rather
   than relying on temporary paths.

REST quota subtraction was misleading in the first measurement: a five-query
CLI call summed to six GraphQL points while its before/after quota endpoint
readings showed zero change and a different reset window.
Evidence: `/tmp/todos-phase10-final-xnhmqujz/measurements.json`.
This is why the published tables sum each query's returned cost instead.
It is not a claim that the call was free or that all quota APIs are broken.

## What to do next, and what to leave alone

The live missing cases are listed in the GitHub note. For performance work,
measure concurrent cold metadata fills before adding machinery: the startup
duplicate Repository/Viewer calls are real, but suppressing them must preserve
per-caller cancellation, refresh cutoffs and credential/generation checks.
Do not introduce a process-wide request semaphore or issue lock to solve a
metadata problem.

Keep mutation no-replay, conservative uncertainty, source-preserving quick-add
recovery, and readAt anchored before the request. Those rules protect user
data; replacing them with “retry and hope” would trade a measured latency cost
for duplicate issues or lost edits.
