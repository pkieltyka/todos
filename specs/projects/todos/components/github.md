---
status: complete
---

# GitHub backend

Implements `internal/github`; imports only `internal/domain` from this module.
[Architecture](../architecture.md) is authoritative. The Backend signatures are in [service.md](service.md).

## Files and dependencies

| File | Responsibility |
|---|---|
| client.go | Lazy shared go-gh clients, credentials and context requests |
| queries/*.graphql | The complete literal documents below; go:embed |
| issues.go, pull_requests.go, projects.go | Request variables, decode and mapping |
| errors.go | Ordered classification and uncertainty |
| rate_limit.go | Per-resource header observations and snapshot |
| testdata/ | Synthetic HTTP request/response/error fixtures |

- Pin github.com/cli/go-gh/v2 v2.17.0, as architecture10 specifies.
- No second SDK, persistent HTTP cache, GraphQL generator, retry middleware, semaphore, httptrace or custom redirect policy.
- Repository resolution is the CLI's go-gh repository.Current call, not this package's own remote algorithm.

## Constructor and private machinery

```go
type Options struct {
    Version string
    Host func(context.Context) (string, error)
    Transport http.RoundTripper // nil = http.DefaultTransport; injected by tests
    TokenForHost func(string) (token, source string) // nil = auth.TokenForHost
    Now func() time.Time
}
type Client struct {
    opts Options
    initMu sync.Mutex
    rest *api.RESTClient
    graphql *api.GraphQLClient
    initial *initialization
    host, tokenSource string
    authMu sync.RWMutex
    token string // private memory only; never formatted
    tokenVersion uint64
    needsRefresh bool
    refreshing *credentialRefresh
    tracker rateTracker
}
type operation struct { Name string; Mutation bool; Started bool }
type initialization struct { done chan struct{}; err error }
type credentialRefresh struct { done chan struct{}; err error }
type responseMeta struct { Headers http.Header; CredentialVersion uint64 }
type rateTracker struct { mu sync.Mutex; buckets map[string]domain.RateBucket; blockedUntil time.Time }
type observingTransport struct { base http.RoundTripper; client *Client }

func New(Options) *Client
func (c *Client) initialize(context.Context) error
func (c *Client) requestReady(context.Context) error
func (c *Client) refreshToken(ctx context.Context, force bool) error
func (c *Client) authFailed(credentialVersion uint64)
func (c *Client) query(ctx context.Context, name string, variables map[string]any, out any) error
func (c *Client) mutate(ctx context.Context, name string, variables map[string]any, out any) error
func (t *observingTransport) RoundTrip(*http.Request) (*http.Response,error)
func buildIssueSearch(domain.RepoRef, domain.IssueFilter) (string,error)
func classify(err error, op operation, meta responseMeta, now time.Time) *domain.Error
func (r *rateTracker) observe(http.Header, time.Time)
func (r *rateTracker) snapshot(time.Time) domain.RateLimit
func retryAfter(http.Header, time.Time) time.Duration
```

- New performs no I/O; initialize resolves host/credentials on first authenticated call and creates one reusable REST/GraphQL pair.
- All Client public methods have the exact Backend signatures in service.md, implemented on *Client. Keep a compile-time assertion in cli wiring, avoiding a github -> service import.
- Resolve credentials with auth.TokenForHost. Never expose/store the token in domain or on disk.
- Environment tokens work without gh; keyring lookup may execute gh auth token inside go-gh. Todos does not invoke gh itself for operations.
- Options for both clients: Host, AuthToken, Timeout=30s, EnableCache=false, LogIgnoreEnv=true, no logging writer, User-Agent=todos/VERSION, Accept=application/vnd.github+json.
- Keep go-gh's default redirects. Do not install CheckRedirect or a request-byte observer.
- Provide an observing RoundTripper only to record headers and supply the current in-memory credential for permitted GitHub/API hosts on refresh; protect credential state with a mutex. Never forward a token to an unrelated host.
- Each request carries its own responseMeta pointer in context; never classify concurrent errors using shared last-response headers.
- Before invoking DoWithContext, check ctx.Err. Set operation.Started=true immediately before invocation; an ambiguous failure after that is uncertain for a mutation.
- Decoder destination is per call. A successful response missing its required object or with malformed JSON is github.
- Queries and mutations are never automatically retried.
- An auth failure marks that credential generation stale. The next request re-resolves credentials before sending, so a plain retry after `gh auth login` works; the failed operation is never replayed. Concurrent callers share the credential lookup, and a late failure from an older credential generation does not invalidate newer credentials. An initial missing-token failure is also re-resolved on the next call.
- RefreshCredentials explicitly re-resolves credential state without replaying an operation. RefreshMetadata also refreshes memoised service metadata/session.
- go-gh credential discovery has no context API: run one initialisation worker and let a canceled caller stop waiting on its result. The read-only lookup may finish later, but a canceled use case cannot continue to a write. Do not hold initMu while a canceled caller waits.
- GET /user is used only by DoctorScopes for optional X-OAuth-Scopes; ordinary Credentials reads local source information only.

## Connection and mapping rules

- Page.Size defaults100, accepts1..100. Empty Cursor means after=null; otherwise pass it unchanged.
- Return GitHub endCursor, hasNextPage and totalCount unchanged. Do not bind cursors to filters or prefetch outer pages.
- Issue assignees: first10. Issue/PR labels: first100, no overflow queries.
- Issue memberships: first100, no overflow query/page. IssuePlacements is one first100 includeArchived:true read for write preflight.
- Repository labels/assignable users, linked/owned projects, project fields, board items, PR closing references and comments are pageable where documented.
- ProjectFields completes its field connection internally; PR detail completes closingReferences internally. These are not outer list pages.
- One shared IssueSummary fragment fills every issue row/detail/change/card.
- selectedProjectID is an explicit argument to ListIssues/Issue; empty omits memberships, otherwise map the matching active project item.
- Optional membership errors do not fail issue reads: retain decoded issue fields, clear placement, retry a read without board selection only if null-bubbling lost the issue data. This is a fresh fallback query, not a mutation retry.
- State mappings: OPEN/CLOSED and PR MERGED -> lowercase; stateReason DUPLICATE/unknown -> nil.
- ReviewDecision null -> none; otherwise approved/changes_requested/review_required.
- Checks: absent rollup or contexts.totalCount=0 -> none; SUCCESS -> passing; FAILURE/ERROR -> failing; EXPECTED/PENDING -> pending.
- Conversation comments use default forward creation order; never order by UPDATED_AT or include review threads. Null authors stay nil.
- Closes keeps only the selected repository's node ID and deduplicates numbers.
- Project owner supports User and Organization; retain login/number/ID and Status-field usability.
- BoardItems requests NOT_ARCHIVED and POSITION ASC. Redacted/PR/draft/foreign-repo items remain typed so service can count hidden items.
- Names are never interpolated into GraphQL source. Search text and all IDs go in variables.

## Complete GraphQL documents

Each following block is one complete file, with exactly its required fragments. Repeated fragment text is deliberate; no runtime fragment discovery or code generation is required.

### Session (`queries/session.graphql`)

```graphql
query Session($owner: String!, $repo: String!) {
  viewer { id login name }
  repository(owner: $owner, name: $repo) {
    id nameWithOwner url hasIssuesEnabled viewerPermission viewerCanCreateIssues
  }
}
```

### Viewer (`queries/viewer.graphql`)

```graphql
query Viewer {
  viewer { id login name }
}
```

### Labels (`queries/labels.graphql`)

```graphql
query Labels($owner: String!, $repo: String!, $first: Int!, $after: String) {
  repository(owner: $owner, name: $repo) {
    labels(first: $first, after: $after, orderBy: {field: NAME, direction: ASC}) {
      totalCount nodes { id name description color }
      pageInfo { hasNextPage endCursor }
    }
  }
}
```

### AssignableUsers (`queries/assignable_users.graphql`)

```graphql
query AssignableUsers($owner: String!, $repo: String!, $first: Int!, $after: String) {
  repository(owner: $owner, name: $repo) {
    assignableUsers(first: $first, after: $after) {
      totalCount nodes { id login name } pageInfo { hasNextPage endCursor }
    }
  }
}
```

### IssueList (`queries/issue_list.graphql`)

```graphql
query IssueList($owner: String!, $repo: String!, $first: Int!, $after: String,
                $states: [IssueState!], $filter: IssueFilters, $withBoard: Boolean!) {
  repository(owner: $owner, name: $repo) {
    issues(first: $first, after: $after, states: $states, filterBy: $filter,
           orderBy: {field: UPDATED_AT, direction: DESC}) {
      totalCount pageInfo { hasNextPage endCursor }
      nodes {
        ...IssueSummary
        projectItems(first: 100, includeArchived: false) @include(if: $withBoard) {
          nodes { ...PlacementFields } pageInfo { hasNextPage endCursor }
        }
      }
    }
  }
}

fragment IssueSummary on Issue {
  id number title state stateReason author { login }
  assignees(first: 10) { nodes { id login name } }
  labels(first: 100) {
    nodes { id name color description }
    pageInfo { hasNextPage endCursor }
  }
  milestone { title }
  comments { totalCount }
  createdAt updatedAt closedAt url viewerCanUpdate viewerCanDelete
}

fragment PlacementFields on ProjectV2Item {
  id isArchived project { id }
  fieldValueByName(name: "Status") {
    __typename
    ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
  }
}
```

### IssueView (`queries/issue_view.graphql`)

```graphql
query IssueView($owner: String!, $repo: String!, $number: Int!, $withBoard: Boolean!) {
  repository(owner: $owner, name: $repo) {
    issue(number: $number) {
      ...IssueSummary body
      projectItems(first: 100, includeArchived: false) @include(if: $withBoard) {
        nodes { ...PlacementFields } pageInfo { hasNextPage endCursor }
      }
    }
  }
}

fragment IssueSummary on Issue {
  id number title state stateReason author { login }
  assignees(first: 10) { nodes { id login name } }
  labels(first: 100) {
    nodes { id name color description }
    pageInfo { hasNextPage endCursor }
  }
  milestone { title }
  comments { totalCount }
  createdAt updatedAt closedAt url viewerCanUpdate viewerCanDelete
}

fragment PlacementFields on ProjectV2Item {
  id isArchived project { id }
  fieldValueByName(name: "Status") {
    __typename
    ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
  }
}
```

### IssueComments (`queries/issue_comments.graphql`)

```graphql
query IssueComments($owner: String!, $repo: String!, $number: Int!, $first: Int!, $after: String) {
  repository(owner: $owner, name: $repo) {
    issue(number: $number) {
      comments(first: $first, after: $after) {
        totalCount nodes { ...CommentFields } pageInfo { hasNextPage endCursor }
      }
    }
  }
}

fragment CommentFields on IssueComment {
  id author { login } createdAt body url
}
```

### IssuePlacements (`queries/issue_placements.graphql`)

```graphql
query IssuePlacements($id: ID!) {
  node(id: $id) {
    ... on Issue {
      projectItems(first: 100, includeArchived: true) {
        nodes { ...PlacementFields } pageInfo { hasNextPage endCursor } totalCount
      }
    }
  }
}

fragment PlacementFields on ProjectV2Item {
  id isArchived project { id }
  fieldValueByName(name: "Status") {
    __typename
    ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
  }
}
```

### SearchIssues (`queries/search_issues.graphql`)

```graphql
query SearchIssues($query: String!, $first: Int!, $after: String, $withBoard: Boolean!) {
  search(query: $query, type: ISSUE, first: $first, after: $after) {
    issueCount pageInfo { hasNextPage endCursor }
    nodes {
      ... on Issue {
        ...IssueSummary
        repository { id nameWithOwner }
        projectItems(first: 100, includeArchived: false) @include(if: $withBoard) {
          nodes { ...PlacementFields } pageInfo { hasNextPage endCursor }
        }
      }
    }
  }
}

fragment IssueSummary on Issue {
  id number title state stateReason author { login }
  assignees(first: 10) { nodes { id login name } }
  labels(first: 100) {
    nodes { id name color description }
    pageInfo { hasNextPage endCursor }
  }
  milestone { title }
  comments { totalCount }
  createdAt updatedAt closedAt url viewerCanUpdate viewerCanDelete
}

fragment PlacementFields on ProjectV2Item {
  id isArchived project { id }
  fieldValueByName(name: "Status") {
    __typename
    ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
  }
}
```

### PRList (`queries/p_r_list.graphql`)

```graphql
query PRList($owner: String!, $repo: String!, $first: Int!, $after: String,
             $states: [PullRequestState!]) {
  repository(owner: $owner, name: $repo) {
    pullRequests(first: $first, after: $after, states: $states,
                 orderBy: {field: UPDATED_AT, direction: DESC}) {
      totalCount nodes { ...PRSummary } pageInfo { hasNextPage endCursor }
    }
  }
}

fragment PRSummary on PullRequest {
  id number title state isDraft author { login }
  headRefName baseRefName reviewDecision
  assignees(first: 10) { nodes { id login name } }
  labels(first: 100) {
    nodes { id name color description }
    pageInfo { hasNextPage endCursor }
  }
  comments { totalCount }
  commits(last: 1) {
    nodes { commit { statusCheckRollup { state contexts(first: 1) { totalCount } } } }
  }
  createdAt updatedAt closedAt mergedAt url
}
```

### PRView (`queries/p_r_view.graphql`)

```graphql
query PRView($owner: String!, $repo: String!, $number: Int!, $afterCloses: String) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      ...PRSummary body mergeable
      closingIssuesReferences(first: 100, after: $afterCloses) {
        nodes { number repository { id nameWithOwner } }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}

fragment PRSummary on PullRequest {
  id number title state isDraft author { login }
  headRefName baseRefName reviewDecision
  assignees(first: 10) { nodes { id login name } }
  labels(first: 100) {
    nodes { id name color description }
    pageInfo { hasNextPage endCursor }
  }
  comments { totalCount }
  commits(last: 1) {
    nodes { commit { statusCheckRollup { state contexts(first: 1) { totalCount } } } }
  }
  createdAt updatedAt closedAt mergedAt url
}
```

### PRComments (`queries/p_r_comments.graphql`)

```graphql
query PRComments($owner: String!, $repo: String!, $number: Int!, $first: Int!, $after: String) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      comments(first: $first, after: $after) {
        totalCount nodes { ...CommentFields } pageInfo { hasNextPage endCursor }
      }
    }
  }
}

fragment CommentFields on IssueComment {
  id author { login } createdAt body url
}
```

### LinkedProjects (`queries/linked_projects.graphql`)

```graphql
query LinkedProjects($owner: String!, $repo: String!, $first: Int!, $after: String) {
  repository(owner: $owner, name: $repo) {
    projectsV2(first: $first, after: $after) {
      totalCount nodes { ...ProjectSummary } pageInfo { hasNextPage endCursor }
    }
  }
}

fragment ProjectSummary on ProjectV2 {
  id number title url updatedAt closed viewerCanUpdate
  owner { __typename ... on User { login } ... on Organization { login } }
  items(first: 1, archivedStates: [NOT_ARCHIVED]) { totalCount }
}
```

### OwnerProjects (`queries/owner_projects.graphql`)

```graphql
query OwnerProjects($owner: String!, $first: Int!, $after: String) {
  repositoryOwner(login: $owner) {
    __typename
    ... on User {
      projectsV2(first: $first, after: $after) {
        totalCount nodes { ...ProjectSummary } pageInfo { hasNextPage endCursor }
      }
    }
    ... on Organization {
      projectsV2(first: $first, after: $after) {
        totalCount nodes { ...ProjectSummary } pageInfo { hasNextPage endCursor }
      }
    }
  }
}

fragment ProjectSummary on ProjectV2 {
  id number title url updatedAt closed viewerCanUpdate
  owner { __typename ... on User { login } ... on Organization { login } }
  items(first: 1, archivedStates: [NOT_ARCHIVED]) { totalCount }
}
```

### ProjectByNumber (`queries/project_by_number.graphql`)

```graphql
query ProjectByNumber($owner: String!, $number: Int!) {
  repositoryOwner(login: $owner) {
    __typename
    ... on User { projectV2(number: $number) { ...ProjectSummary } }
    ... on Organization { projectV2(number: $number) { ...ProjectSummary } }
  }
}

fragment ProjectSummary on ProjectV2 {
  id number title url updatedAt closed viewerCanUpdate
  owner { __typename ... on User { login } ... on Organization { login } }
  items(first: 1, archivedStates: [NOT_ARCHIVED]) { totalCount }
}
```

### ProjectFields (`queries/project_fields.graphql`)

```graphql
query ProjectFields($id: ID!, $after: String) {
  node(id: $id) {
    ... on ProjectV2 {
      ...ProjectSummary
      fields(first: 100, after: $after) {
        nodes {
          __typename
          ... on ProjectV2FieldCommon { id name }
          ... on ProjectV2SingleSelectField { options { id name color } }
        }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}

fragment ProjectSummary on ProjectV2 {
  id number title url updatedAt closed viewerCanUpdate
  owner { __typename ... on User { login } ... on Organization { login } }
  items(first: 1, archivedStates: [NOT_ARCHIVED]) { totalCount }
}
```

### BoardItems (`queries/board_items.graphql`)

```graphql
query BoardItems($id: ID!, $first: Int!, $after: String) {
  node(id: $id) {
    ... on ProjectV2 {
      items(first: $first, after: $after, archivedStates: [NOT_ARCHIVED],
            orderBy: {field: POSITION, direction: ASC}) {
        totalCount pageInfo { hasNextPage endCursor }
        nodes {
          ...PlacementFields
          content {
            __typename
            ... on Issue { ...IssueSummary repository { id nameWithOwner } }
          }
        }
      }
    }
  }
}

fragment IssueSummary on Issue {
  id number title state stateReason author { login }
  assignees(first: 10) { nodes { id login name } }
  labels(first: 100) {
    nodes { id name color description }
    pageInfo { hasNextPage endCursor }
  }
  milestone { title }
  comments { totalCount }
  createdAt updatedAt closedAt url viewerCanUpdate viewerCanDelete
}

fragment PlacementFields on ProjectV2Item {
  id isArchived project { id }
  fieldValueByName(name: "Status") {
    __typename
    ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
  }
}
```

### CreateIssue (`queries/create_issue.graphql`)

```graphql
mutation CreateIssue($input: CreateIssueInput!) {
  createIssue(input: $input) { issue { ...IssueSummary } }
}

fragment IssueSummary on Issue {
  id number title state stateReason author { login }
  assignees(first: 10) { nodes { id login name } }
  labels(first: 100) {
    nodes { id name color description }
    pageInfo { hasNextPage endCursor }
  }
  milestone { title }
  comments { totalCount }
  createdAt updatedAt closedAt url viewerCanUpdate viewerCanDelete
}
```

### EditIssue (`queries/edit_issue.graphql`)

```graphql
mutation EditIssue($input: UpdateIssueInput!) {
  updateIssue(input: $input) { issue { ...IssueSummary } }
}

fragment IssueSummary on Issue {
  id number title state stateReason author { login }
  assignees(first: 10) { nodes { id login name } }
  labels(first: 100) {
    nodes { id name color description }
    pageInfo { hasNextPage endCursor }
  }
  milestone { title }
  comments { totalCount }
  createdAt updatedAt closedAt url viewerCanUpdate viewerCanDelete
}
```

### AddAssignees (`queries/add_assignees.graphql`)

```graphql
mutation AddAssignees($input: AddAssigneesToAssignableInput!) {
  addAssigneesToAssignable(input: $input) { assignable { ... on Issue { ...IssueSummary } } }
}

fragment IssueSummary on Issue {
  id number title state stateReason author { login }
  assignees(first: 10) { nodes { id login name } }
  labels(first: 100) {
    nodes { id name color description }
    pageInfo { hasNextPage endCursor }
  }
  milestone { title }
  comments { totalCount }
  createdAt updatedAt closedAt url viewerCanUpdate viewerCanDelete
}
```

### RemoveAssignees (`queries/remove_assignees.graphql`)

```graphql
mutation RemoveAssignees($input: RemoveAssigneesFromAssignableInput!) {
  removeAssigneesFromAssignable(input: $input) { assignable { ... on Issue { ...IssueSummary } } }
}

fragment IssueSummary on Issue {
  id number title state stateReason author { login }
  assignees(first: 10) { nodes { id login name } }
  labels(first: 100) {
    nodes { id name color description }
    pageInfo { hasNextPage endCursor }
  }
  milestone { title }
  comments { totalCount }
  createdAt updatedAt closedAt url viewerCanUpdate viewerCanDelete
}
```

### AddLabels (`queries/add_labels.graphql`)

```graphql
mutation AddLabels($input: AddLabelsToLabelableInput!) {
  addLabelsToLabelable(input: $input) { labelable { ... on Issue { ...IssueSummary } } }
}

fragment IssueSummary on Issue {
  id number title state stateReason author { login }
  assignees(first: 10) { nodes { id login name } }
  labels(first: 100) {
    nodes { id name color description }
    pageInfo { hasNextPage endCursor }
  }
  milestone { title }
  comments { totalCount }
  createdAt updatedAt closedAt url viewerCanUpdate viewerCanDelete
}
```

### RemoveLabels (`queries/remove_labels.graphql`)

```graphql
mutation RemoveLabels($input: RemoveLabelsFromLabelableInput!) {
  removeLabelsFromLabelable(input: $input) { labelable { ... on Issue { ...IssueSummary } } }
}

fragment IssueSummary on Issue {
  id number title state stateReason author { login }
  assignees(first: 10) { nodes { id login name } }
  labels(first: 100) {
    nodes { id name color description }
    pageInfo { hasNextPage endCursor }
  }
  milestone { title }
  comments { totalCount }
  createdAt updatedAt closedAt url viewerCanUpdate viewerCanDelete
}
```

### Comment (`queries/comment.graphql`)

```graphql
mutation Comment($input: AddCommentInput!) {
  addComment(input: $input) {
    commentEdge { node { ...CommentFields } }
    subject { ... on Issue { ...IssueSummary } }
  }
}

fragment CommentFields on IssueComment {
  id author { login } createdAt body url
}

fragment IssueSummary on Issue {
  id number title state stateReason author { login }
  assignees(first: 10) { nodes { id login name } }
  labels(first: 100) {
    nodes { id name color description }
    pageInfo { hasNextPage endCursor }
  }
  milestone { title }
  comments { totalCount }
  createdAt updatedAt closedAt url viewerCanUpdate viewerCanDelete
}
```

### CloseIssue (`queries/close_issue.graphql`)

```graphql
mutation CloseIssue($input: CloseIssueInput!) {
  closeIssue(input: $input) { issue { ...IssueSummary } }
}

fragment IssueSummary on Issue {
  id number title state stateReason author { login }
  assignees(first: 10) { nodes { id login name } }
  labels(first: 100) {
    nodes { id name color description }
    pageInfo { hasNextPage endCursor }
  }
  milestone { title }
  comments { totalCount }
  createdAt updatedAt closedAt url viewerCanUpdate viewerCanDelete
}
```

### ReopenIssue (`queries/reopen_issue.graphql`)

```graphql
mutation ReopenIssue($input: ReopenIssueInput!) {
  reopenIssue(input: $input) { issue { ...IssueSummary } }
}

fragment IssueSummary on Issue {
  id number title state stateReason author { login }
  assignees(first: 10) { nodes { id login name } }
  labels(first: 100) {
    nodes { id name color description }
    pageInfo { hasNextPage endCursor }
  }
  milestone { title }
  comments { totalCount }
  createdAt updatedAt closedAt url viewerCanUpdate viewerCanDelete
}
```

### DeleteIssue (`queries/delete_issue.graphql`)

```graphql
mutation DeleteIssue($input: DeleteIssueInput!) {
  deleteIssue(input: $input) { repository { id } }
}
```

### AddProjectItem (`queries/add_project_item.graphql`)

```graphql
mutation AddProjectItem($input: AddProjectV2ItemByIdInput!) {
  addProjectV2ItemById(input: $input) { item { ...PlacementFields } }
}

fragment PlacementFields on ProjectV2Item {
  id isArchived project { id }
  fieldValueByName(name: "Status") {
    __typename
    ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
  }
}
```

### SetStatus (`queries/set_status.graphql`)

```graphql
mutation SetStatus($input: UpdateProjectV2ItemFieldValueInput!) {
  updateProjectV2ItemFieldValue(input: $input) { projectV2Item { ...PlacementFields } }
}

fragment PlacementFields on ProjectV2Item {
  id isArchived project { id }
  fieldValueByName(name: "Status") {
    __typename
    ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
  }
}
```

### ClearStatus (`queries/clear_status.graphql`)

```graphql
mutation ClearStatus($input: ClearProjectV2ItemFieldValueInput!) {
  clearProjectV2ItemFieldValue(input: $input) { projectV2Item { ...PlacementFields } }
}

fragment PlacementFields on ProjectV2Item {
  id isArchived project { id }
  fieldValueByName(name: "Status") {
    __typename
    ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
  }
}
```

### RemoveProjectItem (`queries/remove_project_item.graphql`)

```graphql
mutation RemoveProjectItem($input: DeleteProjectV2ItemInput!) {
  deleteProjectV2Item(input: $input) { deletedItemId }
}
```

### UnarchiveItem (`queries/unarchive_item.graphql`)

```graphql
mutation UnarchiveItem($input: UnarchiveProjectV2ItemInput!) {
  unarchiveProjectV2Item(input: $input) { item { ...PlacementFields } }
}

fragment PlacementFields on ProjectV2Item {
  id isArchived project { id }
  fieldValueByName(name: "Status") {
    __typename
    ... on ProjectV2ItemFieldSingleSelectValue { name optionId }
  }
}
```

## Request inputs and operation mapping

| Backend method | Document / variables or input |
|---|---|
| Viewer | Viewer; no repository argument or REST call required |
| Repository | Session; owner, repo; use returned repository |
| Credentials | Local source only; Scopes empty until DoctorScopes |
| DoctorScopes | REST GET /user, optional X-OAuth-Scopes; never a mutation |
| ListLabels / ListAssignableUsers | Labels / AssignableUsers, first and after |
| ListIssues | IssueList; states, IssueFilters{assignee,labels}; SearchIssues only if filter.Search nonempty |
| Issue / IssueComments | IssueView / IssueComments; number; withBoard derives from selected project ID |
| ListPRs / PR / PRComments | PRList / PRView / PRComments |
| LinkedProjects / OwnerProjects / Project | Matching document, owner/name/number/page variables |
| ProjectFields / ProjectItems | ProjectFields / BoardItems, id and page variables |
| IssuePlacements | IssuePlacements, id only; read first100, including archived |
| CreateIssue | CreateIssueInput{repositoryId,title,body,assigneeIds,labelIds}; omit project IDs |
| UpdateIssue | UpdateIssueInput{id, supplied title/body/assigneeIds}; omitted fields never sent |
| AddAssignees / RemoveAssignees | {assignableId,assigneeIds} |
| AddLabels / RemoveLabels | {labelableId,labelIds} |
| AddComment | {subjectId,body}; payload contains Comment and Issue summary |
| CloseIssue | {issueId,stateReason:COMPLETED or NOT_PLANNED} |
| ReopenIssue / DeleteIssue | {issueId} |
| AddProjectItem | {projectId,contentId:issueNodeID} |
| SetProjectStatus | {projectId,itemId,fieldId,value:{singleSelectOptionId:optionID}} |
| ClearProjectStatus | {projectId,itemId,fieldId} |
| RemoveProjectItem / UnarchiveProjectItem | {projectId,itemId} |

- service maps names to IDs from metadata; github accepts validated ID inputs.
- clientMutationId is not an idempotency key. Never use it to justify a retry.
- Search expression: scope to repo and is:issue, state/assignee/AND labels, user text, sort:updated-desc.
- Quote qualifier values; reject conflicting scope/type qualifiers so text cannot widen the run's repo.
- Group free-text Boolean expressions under the fixed scope. Validate returned Issue.repository against the selected repo as a final guard; a conflicting result is invalid_input, never shown as an issue in this run. Search nodes therefore also select repository{id nameWithOwner} alongside IssueSummary.
- Search returns issueCount as Total; ProviderTruncated=true if Total>1000.
- CLI rejects --all when ProviderTruncated; bounded reads stop at provider exhaustion with truncated=true.

## Error classification

Use errors.As for api.HTTPError and api.GraphQLError. Apply this priority across relevant GraphQL errors; ignore optional board-field failures only under the issue-read fallback above.

| Priority | Evidence | domain kind |
|---|---|---|
| 1 | No token, HTTP401, UNAUTHENTICATED/UNAUTHORIZED/BAD_CREDENTIALS | auth |
| 2 | INSUFFICIENT_SCOPES or GitHub explicitly names a missing scope | missing_scope |
| 3 | HTTP429; HTTP403 with remaining=0; explicit primary/secondary abuse/rate message; RATE_LIMITED | rate_limited |
| 4 | Other HTTP403 or FORBIDDEN | forbidden |
| 5 | HTTP404, NOT_FOUND, required returned resource null | not_found |
| 6 | HTTP400/422 caused by supplied values, UNPROCESSABLE | invalid_input |
| 7 | HTTP5xx, malformed JSON/required data, INTERNAL, query/schema defects in our document | github |

- A scope-less refusal is forbidden; absent scope headers do not prove missing_scope.
- HTTP422 explicitly reporting spam/abuse follows rate_limited, not ordinary validation.
- HTTP410 with issue creation disabled becomes issues_disabled; otherwise not_found.
- Unknown response failures become github, not an invented public kind.
- Not-found text says “not found or no access”.
- Context canceled: network / “operation interrupted”, wrapping context.Canceled.
- DeadlineExceeded or net.Error.Timeout: timeout. DNS/connect/TLS/EOF: network.
- Local encode/programming failure: internal. Preserve an existing *domain.Error unchanged.
- Mutation uncertainty: after mutate invokes DoWithContext, network/cancellation/timeout/5xx/malformed answer sets Uncertain=true. No byte-tracking or custom redirect decisions.
- An explicit permission/validation/rate rejection is a definite failure, Uncertain=false.
- No mutation or query is automatically retried. service adds partial only after a confirmed earlier step.
- Keep any acknowledged issue from partially decoded mutation data in Error.Issue.
- Build safe Message/Hint, never dump headers, variables, token or raw body. Faces sanitise before terminal output.

### Hints and retry timing

- auth: gh auth login; for an environment token, explain that it overrides stored gh credentials.
- missing_scope: gh auth refresh -s project / read:project / repo as explicitly required.
- forbidden: GitHub's policy/access explanation; do not promise admin can override organisation policy.
- Primary rate: x-ratelimit-remaining=0, wait until x-ratelimit-reset (epoch seconds).
- Secondary rate: Retry-After seconds or HTTP-date; if absent, 60 seconds.
- If both reset and Retry-After apply, use the later time; expired/zero delay becomes at least1 second.
- Public retry_after_seconds is ceil(RetryAfter/time.Second), rate_limited only.
- Partial retains Cause=rate_limited; describe delay in Message without adding unsupported JSON keys.

## Rate-limit snapshot

1. Observe x-ratelimit-resource/limit/remaining/reset on every response, including HTTP200 GraphQL errors.
2. Key by resource within this one-host client: core, graphql, search. Do not mix REST request counts and GraphQL points.
3. For one reset window retain the minimum observed remaining; a newer window replaces it.
4. Invalid/missing headers leave the last known bucket unchanged.
5. Return deep-copied Buckets and the tightest unexpired Remaining/Limit ratio as top-level values.
6. Expired buckets do not pause refresh. With no valid buckets Known=false.
7. Record a secondary cooldown in BlockedUntil when reported; do not sleep/retry automatically.
8. TUI checks strict `Remaining*10 < Limit` before auto-refresh. Manual actions bypass that decision.

## Fixtures and named tests

- Store synthetic data in testdata/queries, testdata/mutations and testdata/errors.
- Each request fixture fixes operationName/document/variables; response fixture fixes JSON and headers.
- Fake RoundTripper gets Host and test-token explicitly; no ambient auth or network.
- Inject time and failures; never sleep or create real issues.

| Cases | Named tests |
|---|---|
| Documents / variables | TestDocuments_AllOperationsAndFragments, TestVariables_NoInterpolation, TestMutation_OnlySuppliedFields |
| Paging | TestPage_RawCursorAndOneOuterPage, TestComments_500AndTruncated, TestProjectFields_MultiplePages, TestPRCloses_PagedAndRepoScoped |
| Bounded nested data | TestIssueLabels_First100Only, TestMemberships_First100Only, TestNoExtraLabelsOperation |
| Issue / PR mapping | TestIssueSummary_AllEntryPoints, TestAuthor_Deleted, TestUnknownStateReason_Null, TestPRClosed_ExcludesMerged, TestPRRollups_AllStatesAndLegacyChecks |
| Board | TestBoard_PositionOrderAndNonArchived, TestBoard_ContentKindsAndRedaction, TestFields_StatusTypeAndOptionOrder, TestIssuePlacements_ArchivedPreflight |
| Errors | TestErrorPriority_Table, TestGraphQL_HTTP200Errors, TestScopeLessRefusal_Forbidden, TestNotFound_PrivateMessage, TestMalformedAnswer_GitHub |
| Uncertainty | TestMutation_StartedTimeoutNetwork5xxMalformedUncertain, TestMutation_PreStartCancellationCertain, TestMutation_ExplicitRejectionCertain, TestNoAutomaticRetry |
| Rate limits | TestRetryAfter_SecondsDateResetFallback, TestQuota_ResourceSeparationAndOutOfOrder, TestQuota_ResetExpires, TestQuota_ConcurrentSnapshot |
| Clients | TestClients_ReusableAndContextBound, TestTimeout_30Seconds, TestDefaultRedirectPolicy_NoOverride, TestLogging_EnvCannotLeak, TestCache_NoDiskWrites |

## Verification sources

- [go-gh v2.17.0 clients/options](https://github.com/cli/go-gh/blob/v2.17.0/pkg/api/client_options.go), [GraphQL implementation](https://github.com/cli/go-gh/blob/v2.17.0/pkg/api/graphql_client.go), [errors](https://github.com/cli/go-gh/blob/v2.17.0/pkg/api/errors.go), [auth](https://github.com/cli/go-gh/blob/v2.17.0/pkg/auth/auth.go).
- [Projects API](https://docs.github.com/en/issues/planning-and-tracking-with-projects/automating-your-project/using-the-api-to-manage-projects), [Projects schema](https://docs.github.com/en/graphql/reference/projects), [PR schema](https://docs.github.com/en/graphql/reference/pulls), [rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api), [search ceiling](https://docs.github.com/en/rest/search/search).
- At the proposal stage, read documents were schema-validated against pkieltyka/playg; detail queries returned expected NOT_FOUND because the repository was empty. Mutation input and payload fields, including unarchive, were checked by read-only introspection; that stage executed no mutations.
- Later live write transcripts cover every CLI write command on throwaway issues #15 and #16 (`/tmp/todos-phase5-live/`), and quick add with checked-close and board placement on #17–#19 (`/tmp/todos-phase7-live/`). These included archived-card restoration and board workflow interactions. All five issues were deleted afterwards.
- Cache experiments on throwaway #85 (`/tmp/todos-cache-review-20261116/`) and #86 (`/tmp/todos-phase11-xncs51lq/`) checked timestamp/token behaviour, board changes, label propagation and query costs. Both issues were deleted afterwards. These paths are historical records on the original development machine; [GitHub in practice](../../../../notes/3-github-in-practice.md) preserves observations and limits, and [core build notes](../../../../notes/4-core-build-notes.md) describes how to repeat measurements.
- No live rate-limit failures were induced, and not every pagination boundary was exercised live. Fixtures cover these cases; opt-in live tests are read-only.

## Cache credential identity (Phase 10)

```go
func (c *Client) CredentialKey(ctx context.Context) (string, error)
```

- Call requestReady, then read normalized host and resolved token under authMu.
- Return the full lowercase hex SHA-256 of `"todos-cache-v1\x00" + host + "\x00" + token`.
- No HTTP call is made solely to compute the key. It follows the client's own environment/config/keyring resolution and changes after credential re-resolution.
- Raw tokens never leave github. Never print/log the key or include it in doctor. Missing credentials and cancellation retain typed errors.
- go-gh's HTTP cache stays disabled; the service owns content-cache policy.
- Tests: `TestCredentialKey_NormalizedAndNoHTTP`, `TestCredentialKey_TracksReresolvedTokenAndHost`, `TestCredentialKey_CancelledAndMissingCredential`.

## Change token (Phase 11)

```go
func (c *Client) ChangeToken(ctx context.Context, repo domain.RepoRef, project domain.NodeID) (domain.ChangeToken, error)
```

- Embedded `queries/change_token.graphql` uses variables only. It requests all issue states, newest updated issue and total, viewer login, and the selected project's updatedAt/items.totalCount. Without a project, withProject is false.
- `executeWithMeta` keeps response headers local to this request. `http.ParseTime(meta.Headers.Get("Date"))` produces ServerTime, zero when absent or invalid. Never read a shared last-response header.
- Missing identities/counts/connections are malformed GitHub errors. A repository with zero issues returns zero newest number/time.
- A classified scope/forbidden/not-found error confined to GraphQL path `node` returns the decoded issue/viewer half plus that error; any other failed probe returns a zero token plus the error. Neither is success or cache evidence. The service falls back to its normal issue read.
- Tests: `TestChangeToken_DocumentVariablesAndDate`, `TestChangeToken_MissingOrUnparseableDateIsZero`, `TestChangeToken_DateIsRequestLocal`, `TestChangeToken_ProjectRefusalIsPartialAndNeverSuccessful`, `TestChangeToken_ProbeFailureReturnsZero`, `TestChangeToken_EmptyRepository`, `TestChangeToken_MalformedCannotVouchForData`.

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
