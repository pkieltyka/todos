# 2. How the work is done

The process that built v1 and the cache, and the rules that came out of it.
You can run it with a different team or alone. Keep the checks; they are what
caught the defects.

## The team that built it

Five AI agents, each in its own terminal pane, in one checkout. Peter spoke
only to the architect and asked not to be asked questions: the architect
decides, proceeds, and reports what was decided.

| Role | Owns | Does not |
|---|---|---|
| Architect (@cto) | The specs, the decisions, the final review, every commit | Write feature code |
| Core developer (@lead-dev) | `domain`, `github`, `service`, `cache`, `quickadd`, `config`, `cli`, `main.go`, `go.mod`, the skill | Touch `tui` or `markdown` |
| TUI developer (@frontend-dev) | `tui`, `markdown` | Touch the core or `go.mod` |
| Reviewer (@reviewer) | The first-pass review of every diff | Edit code |
| Project manager (@pm) | `TODO.md` | Write code |

The split into two tracks is real in the code. The TUI was built against
`servicetest.Fake` from Phase 1, before the real service existed, and the two
tracks met at Phase 4. A change that crosses the seam (`service.Service`, the
`domain` types) is agreed between both sides first.

## One phase, start to finish

1. **Design first.** A change of any size starts as a change to a spec
   document. The people who will build it review the draft. The cache design
   went through three reviews and changed in substance each time. Design
   commits are separate from code commits.
2. **A written brief** per developer: what to build, in what order, the tests
   that must exist, what to measure, and what not to touch.
3. **The developer builds** and leaves the work uncommitted. `make check` must
   pass before they report.
4. **First-pass review** by someone who did not write the code, written to
   `reviews/<phase>-<who>.md`. When two tracks finish together, the two
   developers also review each other.
5. **Fixes,** then the architect's final review.
6. **Commit by path,** so one track's unfinished work never lands in the
   other's commit. The plan's checkbox and `TODO.md` are updated.

## What a review must do

- **Read the diff as an adversary.** Each finding has a severity, a
  `file:line` and a concrete way it fails.
- **Revert rules to see if the tests bite.** In a scratch copy, remove one
  rule at a time and run the tests. A rule that can be removed with every test
  still passing is a finding. The Phase 11 review reverted 27 rules, and three
  survived.
- **Check the documents against the code.** That covers the skill, the README
  and the component documents.
- **Check the measurement,** not only the number. See "Measuring" below.

## The final check before a commit

Run the checks in a clean copy of what is about to be committed, not in the
working tree. This catches a file left out of the commit, and it once caught a
flaky test that the working tree never showed.

```sh
d="$TMPDIR/verify" && rm -rf "$d" && mkdir -p "$d"
git archive HEAD | tar -x -C "$d"
git diff --cached --binary > "$d/staged.patch"
cd "$d" && git init -q . && git apply --allow-empty staged.patch
test -z "$(gofmt -l .)"
go build ./... && go vet ./...
go mod tidy -diff
GOOS=windows go build ./...
go test -race -count=1 ./...
```

Windows is not a supported platform (the README says Linux or macOS), but the
code is kept compiling there: `internal/cache` has a non-unix file for that
reason.

CI runs `make check` on Linux and macOS for every push and pull request. Go
1.27.3; the version comes from `go.mod`.

## Tests

- **No test needs the network or credentials.** `github` is tested with a fake
  `http.RoundTripper` and fixtures. `service` is tested against a fake
  backend. `cli` and `tui` are tested against `servicetest.Fake`.
- **No test sleeps.** Clocks and timers are injected. The fake has gates: a
  test holds a call, releases it or fails it, so races are staged in a chosen
  order.
- **Goldens** cover the CLI's text and JSON and the TUI's renders at 80×24 and
  140×40. Each side has its own flag to rewrite them:
  `go test ./internal/cli -update-read-goldens` and
  `go test ./internal/tui -run TestGolden -update`. Read the diff afterwards:
  a change you did not expect is a defect.
- **Live tests are opt-in and read-only:**
  `TODOS_LIVE_TESTS=1 TODOS_LIVE_REPO=pkieltyka/todos go test ./internal/cli -run '^TestLive' -count=1`.

Details for each area are in notes 4 and 5.

## Testing against the real GitHub

This repository's issues and its project board (number 1, still titled
"playg board") are the real data. People see them.

- **Issues 1 to 14 are the real backlog.** Do not use them for experiments.
- **Experiments use throwaway issues** that the experiment creates and then
  deletes. 70 were used and deleted so far (numbers 15 to 82, 85 and 86), so
  the numbering has gaps.
- **The board has an automation:** moving a card to Done closes the issue.
- **A focus bug in the TUI once moved a real issue (6) during a test.** It was
  put back. Use a throwaway issue for any test that writes, and check which
  card has the focus before a key that writes.
- **Cache experiments** set `XDG_CACHE_HOME` to a temporary directory.
- **Check the result on GitHub itself** (`gh issue view`, the web page), not
  only through `todos`.

## Measuring

Every request to GitHub costs rate-limit points, and the cache exists to save
them. A claim about cost or delay is measured on the real repository, and the
report says what the measurement covers. Two reports in the cache work were
true but narrower than they read:

- **A saving measured on an empty list.** It showed the saving on structure
  reads and said nothing about list content.
- **"The change shows after 1.3 to 2.0 seconds".** That was the time the
  measuring command took to run, so anything faster could not be seen. It was
  also measured for label changes only.

Ask of every number: on what data, with what resolution, and for which kind of
change.

## Git

- **One person commits.** Developers leave their work uncommitted.
- **Nothing is pushed unless Peter asks.** A push or a pull request is by
  request.
- **Before saying what is or is not on GitHub, look:** `git status -sb` and
  `git branch -vv`. The architect once reported "nothing has been pushed" when
  the branch had been pushed three times.
- **v1 went in as pull request 83, and the rename cleanup as 84.** Peter
  merged both. Since then, work is committed on `master`.

## Keeping the documents true

- A phase updates its component document in the same commit as the code.
- The skill is checked against the binary by `TestSkillMatchesCLI`.
- Each spec file has a `status` in its frontmatter. All are `complete`. A new
  feature starts by editing the functional spec and the architecture, then the
  component document, then `implementation_plan.md` gets a new phase.
- `TODO.md` is the punchlist: what is open, what is done, and the decisions.
- A lesson that fits none of these goes into a new numbered file in `notes/`.
