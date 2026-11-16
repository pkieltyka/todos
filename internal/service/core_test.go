package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkieltyka/todos/internal/domain"
)

type backendCall struct {
	Name    string
	Page    domain.PageRequest
	Filter  domain.IssueFilter
	Project domain.NodeID
	Owner   string
}
type fakeBackend struct {
	Backend       // unimplemented changes deliberately panic if any read path writes
	mu            sync.Mutex
	calls         []backendCall
	repo          domain.Repository
	viewer        domain.User
	labels        []domain.Label
	users         []domain.User
	linked, owned []domain.Project
	fields        map[domain.NodeID]domain.BoardInfo
	items         []domain.ProjectItem
	fail          map[string]error
	labelStarted  chan struct{}
	labelRelease  chan struct{}
	credentialKey string
}

func backendFixture() *fakeBackend {
	p := domain.Project{Ref: domain.ProjectRef{Host: "github.com", Owner: "test", Number: 1}, NodeID: "P1", Title: "One", Usable: true, ViewerCanUpdate: true}
	return &fakeBackend{repo: domain.Repository{Ref: domain.RepoRef{Host: "github.com", Owner: "test", Name: "repo"}, NodeID: "R1", URL: "https://github.com/test/repo", IssuesEnabled: true, ViewerCanCreateIssues: true}, viewer: domain.User{Login: "test", NodeID: "U1"}, labels: []domain.Label{{Name: "bug"}}, users: []domain.User{{Login: "test"}}, linked: []domain.Project{p}, owned: []domain.Project{p}, fields: map[domain.NodeID]domain.BoardInfo{"P1": {Project: p, StatusFieldID: "F1", Options: []domain.StatusOption{{ID: "todo", Name: "Todo"}, {ID: "done", Name: "Done"}}}}, fail: map[string]error{}}
}
func (b *fakeBackend) record(c backendCall) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, c)
	return b.fail[c.Name]
}
func (b *fakeBackend) count(name string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, c := range b.calls {
		if c.Name == name {
			n++
		}
	}
	return n
}
func (b *fakeBackend) Credentials(context.Context) (domain.CredentialInfo, error) {
	return domain.CredentialInfo{Source: "test"}, b.record(backendCall{Name: "Credentials"})
}
func (b *fakeBackend) CredentialKey(ctx context.Context) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err := b.record(backendCall{Name: "CredentialKey"}); err != nil {
		return "", err
	}
	if b.credentialKey != "" {
		return b.credentialKey, nil
	}
	return strings.Repeat("a", 64), nil
}
func (b *fakeBackend) DoctorScopes(context.Context) (domain.CredentialInfo, error) {
	return domain.CredentialInfo{Scopes: []string{"repo", "project"}}, b.record(backendCall{Name: "DoctorScopes"})
}
func (b *fakeBackend) Viewer(context.Context) (domain.User, error) {
	return b.viewer, b.record(backendCall{Name: "Viewer"})
}
func (b *fakeBackend) Repository(context.Context, domain.RepoRef) (domain.Repository, error) {
	return b.repo, b.record(backendCall{Name: "Repository"})
}
func (b *fakeBackend) RefreshCredentials(context.Context) error {
	return b.record(backendCall{Name: "RefreshCredentials"})
}
func (b *fakeBackend) RateLimit() domain.RateLimit {
	return domain.RateLimit{Buckets: []domain.RateBucket{}}
}
func (b *fakeBackend) ListLabels(ctx context.Context, _ domain.RepoRef, p domain.PageRequest) (domain.Page[domain.Label], error) {
	err := b.record(backendCall{Name: "Labels", Page: p})
	if b.labelStarted != nil {
		b.labelStarted <- struct{}{}
		select {
		case <-ctx.Done():
			return domain.Page[domain.Label]{}, ctx.Err()
		case <-b.labelRelease:
		}
	}
	return domain.Page[domain.Label]{Items: b.labels, Total: len(b.labels)}, err
}
func (b *fakeBackend) ListAssignableUsers(_ context.Context, _ domain.RepoRef, p domain.PageRequest) (domain.Page[domain.User], error) {
	return domain.Page[domain.User]{Items: b.users, Total: len(b.users)}, b.record(backendCall{Name: "AssignableUsers", Page: p})
}
func (b *fakeBackend) LinkedProjects(_ context.Context, _ domain.RepoRef, p domain.PageRequest) (domain.Page[domain.Project], error) {
	return domain.Page[domain.Project]{Items: b.linked, Total: len(b.linked)}, b.record(backendCall{Name: "LinkedProjects", Page: p})
}
func (b *fakeBackend) OwnerProjects(_ context.Context, owner string, p domain.PageRequest) (domain.Page[domain.Project], error) {
	return domain.Page[domain.Project]{Items: b.owned, Total: len(b.owned)}, b.record(backendCall{Name: "OwnerProjects", Owner: owner, Page: p})
}
func (b *fakeBackend) Project(_ context.Context, r domain.ProjectRef) (domain.Project, error) {
	if err := b.record(backendCall{Name: "Project"}); err != nil {
		return domain.Project{}, err
	}
	for _, v := range b.fields {
		if v.Project.Ref == r {
			return v.Project, nil
		}
	}
	return domain.Project{}, &domain.Error{Kind: domain.ErrNotFound, Message: "project not found or no access"}
}
func (b *fakeBackend) ProjectFields(_ context.Context, id domain.NodeID) (domain.BoardInfo, error) {
	return b.fields[id], b.record(backendCall{Name: "ProjectFields", Project: id})
}
func (b *fakeBackend) ProjectItems(_ context.Context, id domain.NodeID, p domain.PageRequest) (domain.Page[domain.ProjectItem], error) {
	err := b.record(backendCall{Name: "ProjectItems", Project: id, Page: p})
	offset := 0
	if p.Cursor != "" {
		offset, _ = strconv.Atoi(strings.TrimPrefix(p.Cursor, "raw:"))
	}
	size := p.Size
	if size == 0 {
		size = 100
	}
	end := min(offset+size, len(b.items))
	next := ""
	if end < len(b.items) {
		next = fmt.Sprintf("raw:%d", end)
	}
	return domain.Page[domain.ProjectItem]{Items: b.items[offset:end], Total: len(b.items), HasMore: end < len(b.items), NextCursor: next}, err
}
func (b *fakeBackend) ListIssues(_ context.Context, _ domain.RepoRef, f domain.IssueFilter, p domain.PageRequest, id domain.NodeID) (domain.Page[domain.Issue], error) {
	return domain.Page[domain.Issue]{Items: []domain.Issue{{NodeID: "I1", Number: 1, Title: "Issue", Labels: []domain.Label{{Name: "bug"}}}}, Total: 17, HasMore: true, NextCursor: "rawgithub=="}, b.record(backendCall{Name: "ListIssues", Page: p, Filter: f, Project: id})
}
func (b *fakeBackend) Issue(_ context.Context, _ domain.RepoRef, n int, id domain.NodeID) (domain.IssueDetail, error) {
	return domain.IssueDetail{Issue: domain.Issue{Number: n, NodeID: "I1"}, Body: "body"}, b.record(backendCall{Name: "Issue", Project: id})
}
func (b *fakeBackend) ListPRs(_ context.Context, _ domain.RepoRef, _ domain.PRFilter, p domain.PageRequest) (domain.Page[domain.PullRequest], error) {
	return domain.Page[domain.PullRequest]{Items: []domain.PullRequest{{NodeID: "PR1", Number: 2}}}, b.record(backendCall{Name: "ListPRs", Page: p})
}
func (b *fakeBackend) PR(context.Context, domain.RepoRef, int) (domain.PRDetail, error) {
	return domain.PRDetail{PullRequest: domain.PullRequest{NodeID: "PR1", Number: 2}, Closes: []int{1}}, b.record(backendCall{Name: "PR"})
}
func (b *fakeBackend) IssueComments(_ context.Context, _ domain.RepoRef, _ int, p domain.PageRequest) (domain.Page[domain.Comment], error) {
	return domain.Page[domain.Comment]{Items: []domain.Comment{}}, b.record(backendCall{Name: "IssueComments", Page: p})
}
func (b *fakeBackend) PRComments(_ context.Context, _ domain.RepoRef, _ int, p domain.PageRequest) (domain.Page[domain.Comment], error) {
	return domain.Page[domain.Comment]{Items: []domain.Comment{}}, b.record(backendCall{Name: "PRComments", Page: p})
}

type fakeSettings struct {
	remembered *domain.ProjectRef
	fail       error
	saved      []domain.ProjectRef
	checkError error
}

func (f *fakeSettings) RememberedBoard(context.Context, domain.RepoRef) (*domain.ProjectRef, error) {
	if f.remembered == nil {
		return nil, nil
	}
	v := *f.remembered
	return &v, nil
}
func (f *fakeSettings) RememberBoard(_ context.Context, _ domain.RepoRef, r domain.ProjectRef) error {
	if f.fail != nil {
		return f.fail
	}
	f.saved = append(f.saved, r)
	return nil
}
func (f *fakeSettings) Check(context.Context) (domain.DoctorCheck, error) {
	return domain.DoctorCheck{Name: "Config", OK: f.checkError == nil, Detail: "config"}, f.checkError
}
func newCore(b *fakeBackend, settings *fakeSettings) *core {
	return New(b, settings, Options{ResolveRepository: func(context.Context) (domain.RepoRef, error) { return b.repo.Ref, nil }}).(*core)
}
func assertKind(t *testing.T, err error, want domain.ErrorKind) *domain.Error {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Kind != want {
		t.Fatalf("want %s got %v", want, err)
	}
	return de
}
func TestMetadata_TTLAndCopies(t *testing.T) {
	b := backendFixture()
	s := newCore(b, &fakeSettings{})
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	s.opts.Now = func() time.Time { return now }
	ctx := context.Background()
	labels, err := s.Labels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	labels[0].Name = "tampered"
	labels, _ = s.Labels(ctx)
	if labels[0].Name != "bug" || b.count("Labels") != 1 {
		t.Fatal(labels)
	}
	now = now.Add(5 * time.Minute)
	s.Labels(ctx)
	if b.count("Labels") != 2 {
		t.Fatal("TTL did not expire")
	}
	info, _ := s.BoardInfo(ctx)
	info.Options[0].Name = "tampered"
	again, _ := s.BoardInfo(ctx)
	if again.Options[0].Name != "Todo" {
		t.Fatal("board aliases cache")
	}
}
func TestMetadata_ConcurrentFillsNoHeldNetworkLock(t *testing.T) {
	b := backendFixture()
	b.labelStarted = make(chan struct{}, 1)
	b.labelRelease = make(chan struct{})
	s := newCore(b, &fakeSettings{})
	done := make(chan error, 1)
	go func() { _, err := s.Labels(context.Background()); done <- err }()
	<-b.labelStarted
	if _, err := s.Session(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(b.labelRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestRefreshMetadata_NewCredentialState(t *testing.T) {
	b := backendFixture()
	s := newCore(b, &fakeSettings{})
	s.Session(context.Background())
	b.viewer.Login = "new"
	if err := s.RefreshMetadata(context.Background()); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Session(context.Background())
	if v.Viewer.Login != "new" || b.count("RefreshCredentials") != 1 || b.count("Repository") != 2 {
		t.Fatal(v)
	}
}
func TestBoard_ExplicitRememberedLinkedPrecedence(t *testing.T) {
	b := backendFixture()
	p := b.fields["P1"].Project
	p.Ref.Number = 2
	p.NodeID = "P2"
	info := b.fields["P1"]
	info.Project = p
	b.fields["P2"] = info
	settings := &fakeSettings{remembered: &p.Ref}
	s := newCore(b, settings)
	s.opts.Project = "1"
	v, err := s.BoardInfo(context.Background())
	if err != nil || v.Project.NodeID != "P1" {
		t.Fatal(v, err)
	}
	s = newCore(b, settings)
	v, err = s.BoardInfo(context.Background())
	if err != nil || v.Project.NodeID != "P2" {
		t.Fatal(v, err)
	}
	s = newCore(b, &fakeSettings{})
	v, err = s.BoardInfo(context.Background())
	if err != nil || v.Project.NodeID != "P1" {
		t.Fatal(v, err)
	}
	s = newCore(b, settings)
	s.opts.Project = "99"
	_, err = s.BoardInfo(context.Background())
	assertKind(t, err, domain.ErrNotFound)
}
func TestBoard_NoBoardCandidatesDiscriminator(t *testing.T) {
	for _, count := range []int{0, 2} {
		b := backendFixture()
		b.linked = make([]domain.Project, count)
		s := newCore(b, &fakeSettings{})
		_, err := s.BoardInfo(context.Background())
		de := assertKind(t, err, domain.ErrNoBoard)
		if len(de.Candidates) != count {
			t.Fatal(de)
		}
	}
}
func TestBoard_WrongStatusType(t *testing.T) {
	b := backendFixture()
	info := b.fields["P1"]
	info.StatusFieldID = ""
	info.Project.Usable = false
	b.fields["P1"] = info
	_, err := newCore(b, &fakeSettings{}).BoardInfo(context.Background())
	de := assertKind(t, err, domain.ErrNoBoard)
	if len(de.Candidates) != 1 || de.Candidates[0].Usable {
		t.Fatal(de)
	}
}
func TestBoardUse_PersistBeforeSelection(t *testing.T) {
	b := backendFixture()
	info := b.fields["P1"]
	info.Project.NodeID = "P2"
	info.Project.Ref.Number = 2
	b.fields["P2"] = info
	settings := &fakeSettings{fail: &domain.Error{Kind: domain.ErrConfig, Message: "disk full"}}
	s := newCore(b, settings)
	s.opts.Project = "1"
	if _, err := s.UseBoard(context.Background(), info.Project.Ref); err == nil {
		t.Fatal("ignored save failure")
	}
	v, _ := s.BoardInfo(context.Background())
	if v.Project.NodeID != "P1" {
		t.Fatal("changed selection before save")
	}
	settings.fail = nil
	if _, err := s.UseBoard(context.Background(), info.Project.Ref); err != nil {
		t.Fatal(err)
	}
	v, _ = s.BoardInfo(context.Background())
	if v.Project.NodeID != "P2" || len(settings.saved) != 1 {
		t.Fatal(v)
	}
}
func TestProjectRef_UserOrgAndWrongHost(t *testing.T) {
	repo := backendFixture().repo.Ref
	for _, text := range []string{"3", "test/3", "https://github.com/users/test/projects/3", "https://github.com/orgs/test/projects/3"} {
		ref, err := ParseProjectRef(text, repo)
		if err != nil || ref.Owner != "test" || ref.Number != 3 {
			t.Fatal(ref, err)
		}
	}
	for _, text := range []string{"0", "x/-1", "https://other.test/users/test/projects/3", "https://github.com/test/projects/3"} {
		if _, err := ParseProjectRef(text, repo); err == nil {
			t.Fatal(text)
		}
	}
}
func TestRef_WrongRepoAndKind(t *testing.T) {
	repo := backendFixture().repo.Ref
	for _, text := range []string{"12", "#12", "https://github.com/test/repo/issues/12#comment"} {
		n, err := ParseIssueRef(text, repo, false)
		if err != nil || n != 12 {
			t.Fatal(n, err)
		}
	}
	for _, text := range []string{"0", "-1", "https://github.com/test/other/issues/12", "https://github.com/test/repo/pull/12"} {
		if _, err := ParseIssueRef(text, repo, false); err == nil {
			t.Fatal(text)
		}
	}
}
func TestReads_OnePageRawCursor(t *testing.T) {
	b := backendFixture()
	s := newCore(b, &fakeSettings{})
	page, err := s.ListIssues(context.Background(), domain.IssueFilter{Labels: []string{"bug"}}, domain.PageRequest{Size: 17, Cursor: "github=="})
	if err != nil || page.NextCursor != "rawgithub==" || b.count("ListIssues") != 1 {
		t.Fatal(page, err)
	}
	last := b.calls[len(b.calls)-1]
	if last.Page.Cursor != "github==" || last.Page.Size != 17 {
		t.Fatal(last)
	}
}
func TestIssues_MineExpandsMe(t *testing.T) {
	b := backendFixture()
	s := newCore(b, &fakeSettings{})
	if _, err := s.ListIssues(context.Background(), domain.IssueFilter{Assignee: "@me"}, domain.PageRequest{}); err != nil {
		t.Fatal(err)
	}
	if b.calls[len(b.calls)-1].Filter.Assignee != "test" {
		t.Fatal(b.calls)
	}
}
func TestPRs_MineHasNoServerAuthorFilter(t *testing.T) {
	b := backendFixture()
	s := newCore(b, &fakeSettings{})
	b.repo.IssuesEnabled = false
	if _, err := s.ListPRs(context.Background(), domain.PRFilter{}, domain.PageRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListIssues(context.Background(), domain.IssueFilter{}, domain.PageRequest{}); err == nil {
		t.Fatal("disabled issues allowed")
	}
}
func TestIssueRead_BoardFailureNonfatal(t *testing.T) {
	b := backendFixture()
	b.fail["LinkedProjects"] = &domain.Error{Kind: domain.ErrMissingScope, Message: "project scope"}
	s := newCore(b, &fakeSettings{})
	if _, err := s.GetIssue(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if b.calls[len(b.calls)-1].Project != "" {
		t.Fatal("board selection leaked")
	}
	b.fail["Issue"] = &domain.Error{Kind: domain.ErrAuth, Message: "auth"}
	_, err := s.GetIssue(context.Background(), 1)
	assertKind(t, err, domain.ErrAuth)
}
func TestBoard_1000Scan500DisplayAndHidden(t *testing.T) {
	b := backendFixture()
	for i := 0; i < 1204; i++ {
		item := domain.ProjectItem{ID: domain.NodeID(fmt.Sprint(i)), RepositoryID: "R1", Kind: "Issue", Issue: &domain.Issue{Number: i + 1}, Placement: domain.Placement{OptionID: "todo"}}
		if i < 3 {
			item.Kind = "DraftIssue"
		}
		if i == 3 {
			item.Placement.OptionID = ""
		}
		b.items = append(b.items, item)
	}
	board, err := newCore(b, &fakeSettings{}).Board(context.Background())
	if err != nil || board.ScannedCount != 1000 || board.HiddenCount != 3 || board.VisibleCount != 997 || !board.Truncated || !board.CardsTruncated || len(board.Columns) != 3 || board.Columns[0].Status != nil || board.Columns[1].Cards[0].Number != 5 {
		t.Fatal(board, err)
	}
	if b.count("ProjectItems") != 10 {
		t.Fatal(b.count("ProjectItems"))
	}
}
func TestRead_NeverUnarchives(t *testing.T) {
	b := backendFixture()
	b.items = []domain.ProjectItem{{Archived: true}, {Kind: "Redacted"}}
	v, err := newCore(b, &fakeSettings{}).Board(context.Background())
	if err != nil || v.ScannedCount != 1 || v.HiddenCount != 1 {
		t.Fatal(v, err)
	}
}
func TestBoardCandidates_GroupedDedupAndUsability(t *testing.T) {
	b := backendFixture()
	p := b.owned[0]
	p.NodeID = "P2"
	p.Ref.Number = 2
	b.owned = append(b.owned, p)
	b.fields["P2"] = domain.BoardInfo{Project: p}
	v, err := newCore(b, &fakeSettings{}).BoardCandidates(context.Background())
	if err != nil || len(v.Projects) != 2 || v.Projects[1].Usable || v.Selected == nil || !v.Projects[0].Linked {
		t.Fatal(v, err)
	}
}
func TestDoctor_NeverWrites(t *testing.T) {
	b := backendFixture()
	report, err := newCore(b, &fakeSettings{}).Doctor(context.Background())
	if err != nil || !report.OK || len(report.Checks) != 7 {
		t.Fatal(report, err)
	}
	for _, c := range b.calls {
		if strings.Contains(c.Name, "Create") || strings.Contains(c.Name, "Update") {
			t.Fatal(c)
		}
	}
}
func TestDoctor_AlwaysSevenRows(t *testing.T) {
	for _, method := range []string{"Credentials", "Viewer", "Repository", "LinkedProjects", "ProjectItems"} {
		b := backendFixture()
		b.fail[method] = &domain.Error{Kind: domain.ErrAuth, Message: "rejected"}
		r, _ := newCore(b, &fakeSettings{}).Doctor(context.Background())
		if len(r.Checks) != 7 {
			t.Fatal(method, r)
		}
	}
}
func TestDoctor_FirstThreeDetermineVerdict(t *testing.T) {
	for _, method := range []string{"Credentials", "Viewer", "Repository", "LinkedProjects", "ProjectItems"} {
		b := backendFixture()
		b.fail[method] = &domain.Error{Kind: domain.ErrForbidden, Message: "rejected"}
		r, err := newCore(b, &fakeSettings{}).Doctor(context.Background())
		fatal := method == "Credentials" || method == "Viewer" || method == "Repository"
		if (err != nil) != fatal || r.OK == fatal {
			t.Fatal(method, r, err)
		}
	}
}
func TestDoctor_ConfigWarningIndependentOfBoard(t *testing.T) {
	b := backendFixture()
	b.linked = nil
	r, err := newCore(b, &fakeSettings{checkError: &domain.Error{Kind: domain.ErrConfig, Message: "bad config"}}).Doctor(context.Background())
	if err != nil || !r.OK || r.Checks[3].OK || r.Checks[5].OK {
		t.Fatal(r, err)
	}
}
func TestReads_DetailsAndComments(t *testing.T) {
	b := backendFixture()
	s := newCore(b, &fakeSettings{})
	ctx := context.Background()
	if _, err := s.GetPR(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListIssueComments(ctx, 1, domain.PageRequest{Cursor: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListPRComments(ctx, 2, domain.PageRequest{}); err != nil {
		t.Fatal(err)
	}
	users, err := s.AssignableUsers(ctx)
	if err != nil || !reflect.DeepEqual(users, b.users) {
		t.Fatal(users, err)
	}
}

func TestMetadata_StaleFillNotPublished(t *testing.T) {
	b := backendFixture()
	b.labelStarted = make(chan struct{}, 2)
	b.labelRelease = make(chan struct{})
	s := newCore(b, &fakeSettings{})
	done := make(chan error, 1)
	go func() { _, err := s.Labels(context.Background()); done <- err }()
	<-b.labelStarted
	if err := s.RefreshMetadata(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(b.labelRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.labels.Valid {
		t.Fatal("fill from an old generation became cached")
	}
}
func TestBoard_HiddenKindsAndForeign(t *testing.T) {
	b := backendFixture()
	b.items = []domain.ProjectItem{{Kind: "PullRequest"}, {Kind: "DraftIssue"}, {Kind: "Redacted"}, {Kind: "Issue", RepositoryID: "OTHER", Issue: &domain.Issue{Number: 1}}, {Kind: "Issue", RepositoryID: "R1", Issue: &domain.Issue{Number: 2}}, {Archived: true}}
	v, err := newCore(b, &fakeSettings{}).Board(context.Background())
	if err != nil || v.HiddenCount != 4 || v.ScannedCount != 5 || v.VisibleCount != 1 {
		t.Fatal(v, err)
	}
}
func TestMetadata_FailuresNotCached(t *testing.T) {
	b := backendFixture()
	b.fail["Labels"] = &domain.Error{Kind: domain.ErrForbidden, Message: "policy"}
	s := newCore(b, &fakeSettings{})
	_, err := s.Labels(context.Background())
	assertKind(t, err, domain.ErrForbidden)
	delete(b.fail, "Labels")
	labels, err := s.Labels(context.Background())
	if err != nil || len(labels) != 1 || b.count("Labels") != 2 {
		t.Fatal(labels, err)
	}
}
func TestBoard_Exactly1000NotTruncated(t *testing.T) {
	b := backendFixture()
	b.items = make([]domain.ProjectItem, 1000)
	for i := range b.items {
		b.items[i].Kind = "Redacted"
	}
	v, err := newCore(b, &fakeSettings{}).Board(context.Background())
	if err != nil || v.Truncated || v.ScannedCount != 1000 || v.HiddenCount != 1000 {
		t.Fatal(v, err)
	}
}
