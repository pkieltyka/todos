package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/pkieltyka/todos/internal/domain"
)

const (
	sidePanelAt = 120 // body width from which detail metadata moves to a side panel (UI 1)
	sidePanelW  = 32
	maxMDWidth  = 100 // Markdown wraps at min(content width, 100) (UI 1)
)

// scroller is the shared viewport logic of the two detail screens: one
// viewport for the body and every comment, with comment anchors for ]/[.
type scroller struct {
	vp      viewport.Model
	anchors []int
	blocks  commentBlocks
	last    syncSig
	synced  bool
}

// fresh reports whether the content is up to date for sig, and records it.
func (s *scroller) fresh(sig syncSig) bool {
	if s.synced && s.last == sig {
		return true
	}
	s.last, s.synced = sig, true
	return false
}

func (s *scroller) set(lines []string, anchors []int, w, h int) {
	off := s.vp.YOffset()
	s.vp.SetWidth(w)
	s.vp.SetHeight(h)
	s.vp.SetContentLines(lines)
	s.vp.SetYOffset(off)
	s.anchors = anchors
}

func (s *scroller) act(a Action) {
	switch a {
	case ActDown:
		s.vp.ScrollDown(1)
	case ActUp:
		s.vp.ScrollUp(1)
	case ActTop:
		s.vp.GotoTop()
	case ActBottom:
		s.vp.GotoBottom()
	case ActHalfDown:
		s.vp.HalfPageDown()
	case ActHalfUp:
		s.vp.HalfPageUp()
	case ActNextComment:
		for _, l := range s.anchors {
			if l > s.vp.YOffset() {
				s.vp.SetYOffset(l)
				return
			}
		}
	case ActPrevComment:
		for i := len(s.anchors) - 1; i >= 0; i-- {
			if s.anchors[i] < s.vp.YOffset() {
				s.vp.SetYOffset(s.anchors[i])
				return
			}
		}
		s.vp.GotoTop()
	}
}

func (s *scroller) position() string {
	return fmt.Sprintf("%d%%", int(s.vp.ScrollPercent()*100+0.5))
}

var scrollActions = []Action{ActDown, ActUp, ActTop, ActBottom, ActHalfDown, ActHalfUp, ActNextComment, ActPrevComment}

// mdWidth is the Markdown width for a content area (2 cells of indent).
func mdWidth(contentW int) int { return max(10, min(contentW-2, maxMDWidth)) }

// indent prefixes rendered lines with two spaces.
func indent(s string) []string {
	ls := strings.Split(s, "\n")
	for i, l := range ls {
		ls[i] = "  " + l
	}
	return ls
}

// refreshBanner is shown above retained content when its refresh failed: the
// reason and the fix (finding 10).
func refreshBanner(t Theme, err *domain.Error) []string {
	if err == nil {
		return nil
	}
	out := []string{"", "  " + t.pending().Render("⚠ Refresh failed: "+errText(err)+" · r retry")}
	if h := errHint(err); h != "" {
		out = append(out, "  "+t.muted().Render(h))
	}
	return out
}

func detailBody(c *Ctx, src string, cw int) []string {
	if strings.TrimSpace(src) == "" {
		return []string{"", "  " + c.Theme.muted().Render("No description.")}
	}
	return append([]string{""}, indent(c.markdownKept(src, mdWidth(cw)))...)
}

// commentLines appends the comments block: rule per comment, then the render.
// A failed comments request says so, with the fix, even when the body loaded
// (findings 5 and 10).
func commentLines(c *Ctx, blocks *commentBlocks, out []string, anchors []int, comments []domain.Comment, phase LoadPhase,
	total int, cerr *domain.Error, w int, kind string) ([]string, []int) {
	t := c.Theme
	if cerr != nil {
		out = append(out, "", "  "+t.errorS().Render("✗ Couldn't load comments: "+errText(cerr)+" · r retry"))
		if h := errHint(cerr); h != "" {
			out = append(out, "  "+t.muted().Render(h))
		}
	}
	if phase != PhaseComplete && len(comments) == 0 && cerr == nil {
		return append(out, "", "  "+t.muted().Render(c.Spin+" Loading comments…")), anchors
	}
	blocks.begin()
	for _, cm := range comments {
		out = append(out, "")
		anchors = append(anchors, len(out))
		out = append(out, blocks.get(t, author(cm.Author)+" · "+agoPhrase(c.Now, cm.CreatedAt), c.markdownKept(cm.Body, mdWidth(w)), w)...)
	}
	blocks.end()
	if phase == PhaseBackground {
		out = append(out, "", "  "+t.muted().Render(c.Spin+" Loading comments…"))
	}
	if total > commentsCap && len(comments) >= commentsCap {
		out = append(out, "", "  "+t.muted().Render(fmt.Sprintf("Showing %d of %s comments. o opens the %s in the browser.", commentsCap, itoa(total), kind)))
	}
	return out, anchors
}

// commentBlocks keeps each comment's drawn lines (its header rule and its
// indented Markdown) between rebuilds. A detail screen rebuilds on every
// comment page and every render batch that lands, which is about 45 times
// while 500 comments load; without this each rebuild styled every comment
// again, so the work grew with the square of the comment count. Entries not
// used by the latest rebuild are dropped.
type commentBlocks struct {
	cur, next map[commentBlockKey][]string
	built     int // blocks drawn from scratch; tests read it
}

type commentBlockKey struct {
	head, md            string // the header label and the Markdown as drawn now
	w                   int
	dark, dimmed, plain bool
}

func (b *commentBlocks) begin() { b.next = make(map[commentBlockKey][]string, len(b.cur)) }
func (b *commentBlocks) end()   { b.cur, b.next = b.next, nil }

// get returns the comment's lines; callers copy them out and never change them.
func (b *commentBlocks) get(t Theme, head, md string, w int) []string {
	k := commentBlockKey{head: head, md: md, w: w, dark: t.Dark, dimmed: t.dimmed, plain: t.plain}
	lines, ok := b.cur[k]
	if !ok {
		lines = append([]string{rule(t, head, w)}, indent(md)...)
		b.built++
	}
	b.next[k] = lines
	return lines
}

// detailLayout splits the body for a detail screen: the head lines above the
// viewport (stacked) or a side panel (wide).
func detailLayout(area Rect) (wide bool, contentW int) {
	if area.W >= sidePanelAt {
		return true, area.W - sidePanelW - 1
	}
	return false, area.W
}

// joinPanel draws the viewport and the side panel next to each other.
func joinPanel(t Theme, left string, panel []string, contentW, h int) []string {
	ls := strings.Split(left, "\n")
	out := make([]string, h)
	for i := range out {
		l := ""
		if i < len(ls) {
			l = ls[i]
		}
		p := ""
		if i < len(panel) {
			p = panel[i]
		}
		out[i] = fit(l, contentW) + t.subtle().Render("│") + fit(" "+p, sidePanelW)
	}
	return out
}

func panelRow(t Theme, k, v string) string {
	return t.muted().Render(fmt.Sprintf("%-10s ", k)) + v
}

// ---- issue detail (UI 3.2) ----

// IssueDetail shows one issue with its body and comments.
type IssueDetail struct {
	number int
	from   string // "Issues" or "Board", for the back label
	scroller
	head  []string
	panel []string
}

func newIssueDetail(n int, from string) *IssueDetail {
	return &IssueDetail{number: n, from: from, scroller: scroller{vp: viewport.New()}}
}

func (s *IssueDetail) ID() ScreenID                { return ScreenIssue }
func (s *IssueDetail) TargetIssue(*Ctx) int        { return s.number }
func (s *IssueDetail) Mode() InputMode             { return ModeNav }
func (s *IssueDetail) Cursor(*Ctx) *tea.Cursor     { return nil }
func (s *IssueDetail) Position(*Ctx) string        { return s.position() }
func (s *IssueDetail) SubBar(c *Ctx, w int) string { return detailSub(c, "‹ "+s.from, s.head, w) }
func (s *IssueDetail) Title(*Ctx) string           { return "#" + itoaPlain(s.number) }
func (s *IssueDetail) Actions(*Ctx) []Action {
	acts := append(append([]Action{}, scrollActions...), ActEdit, ActComment, ActAssign, ActLabel, ActClose, ActBack,
		ActNew, ActQuickAdd, ActCloseNotPlanned, ActStatus, ActStatusPrev, ActStatusNext, ActDelete, ActBrowser, ActCopyURL)
	return acts
}
func (s *IssueDetail) Slots() []Slot {
	return []Slot{{Kind: SlotIssue, Number: s.number}, {Kind: SlotIssueComments, Number: s.number}}
}

func (s *IssueDetail) Update(c *Ctx, msg tea.Msg) tea.Cmd {
	m, ok := msg.(ActionMsg)
	if !ok {
		return nil
	}
	i, _ := c.Issue(s.number)
	switch m.A {
	case ActNew, ActQuickAdd, ActEdit, ActAssign, ActLabel, ActComment, ActClose, ActCloseNotPlanned,
		ActStatus, ActStatusPrev, ActStatusNext, ActDelete:
		return c.a.issueAction(c, m.A, s.number, false, nil)
	case ActBack:
		c.Pop()
	case ActBrowser:
		return c.a.openURL(i.URL)
	case ActCopyURL:
		return c.a.copyURL(i.URL)
	default:
		s.act(m.A)
	}
	return nil
}

func (s *IssueDetail) Renders(c *Ctx) []renderReq {
	d, ok := c.Store.PeekDetail(s.number)
	if !ok {
		return nil
	}
	_, cw := detailLayout(c.Frame.Body)
	w := mdWidth(cw)
	reqs := []renderReq{{Src: d.Body, Width: w}}
	for _, cm := range d.Comments {
		reqs = append(reqs, renderReq{Src: cm.Body, Width: w})
	}
	return reqs
}

func (s *IssueDetail) Sync(c *Ctx) {
	t := c.Theme
	area := c.Frame.Body
	wide, cw := detailLayout(area)
	i, _ := c.Issue(s.number)
	d, _ := c.Store.PeekDetail(s.number)
	mk := c.Marks(s.number)
	if s.fresh(c.sig(d == nil || !d.Loaded || d.CommentsPhase != PhaseComplete || mk.Busy)) {
		return
	}

	// Pending changes show inline (UI 5.2): additions as "⠋+name" in the
	// pending colour, removals struck through, a spinner on a pending state
	// or board status.
	labelColor := map[string]string{}
	for _, l := range i.Labels {
		labelColor[l.Name] = l.Color
	}
	labels := pendingNames(c, nameList(i.Labels), mk.AddedLabels, mk.RemovedLabels, func(n string) string {
		return t.fg(LabelColor(t, labelColor[n])).Render("●") + " " + t.text().Render(n)
	}, "  ")
	board := t.muted().Render("—")
	if i.Board != nil {
		board = t.text().Render(statusName(i.Board.Status))
	}
	if mk.Board {
		board += " " + t.pending().Render(c.Spin)
	}
	milestone := t.muted().Render("—")
	if i.Milestone != nil {
		milestone = t.text().Render(*i.Milestone)
	}
	assignees := pendingNames(c, i.Assignees, mk.AddedPeople, mk.RemovedPeople, func(n string) string {
		return t.text().Render("@" + n)
	}, " ")
	state := issueStateShort(t, i)
	if mk.State {
		state += " " + t.pending().Render(c.Spin)
	}
	comments := itoa(i.CommentCount) + " comment"
	if i.CommentCount != 1 {
		comments += "s"
	}

	var body []string
	switch {
	case d == nil || (!d.Loaded && d.BodyErr == nil):
		body = []string{"", "  " + t.muted().Render(c.Spin+" Loading…")}
	case !d.Loaded:
		body = append([]string{""}, indentAll(failed(t, "Couldn't load #"+itoaPlain(s.number), d.BodyErr))...)
	default:
		// A failed refresh keeps the old body and says why, with the fix
		// (finding 10).
		body = append(refreshBanner(t, d.BodyErr), detailBody(c, d.Body, cw)...)
	}
	var anchors []int
	if d != nil && d.Loaded {
		body, anchors = commentLines(c, &s.blocks, body, anchors, d.Comments, d.CommentsPhase, d.CommentsTotal, d.CommentsErr, cw, "issue")
	}

	if wide {
		s.head = []string{
			rule(t, "", cw) + t.subtle().Render("┬"+strings.Repeat("─", sidePanelW)),
		}
		s.panel = []string{
			panelRow(t, "Author", t.text().Render(author(i.Author))),
			panelRow(t, "Opened", t.text().Render(agoLong(c.Now, i.CreatedAt))),
			panelRow(t, "Updated", t.text().Render(agoLong(c.Now, i.UpdatedAt))),
			panelRow(t, "State", state),
			panelRow(t, "Assignees", assignees),
		}
		var rows []string
		for _, l := range i.Labels {
			if slices.Contains(mk.AddedLabels, l.Name) {
				rows = append(rows, t.pending().Italic(true).Render(c.Spin+"+"+l.Name))
			} else {
				rows = append(rows, t.fg(LabelColor(t, l.Color)).Render("●")+" "+t.text().Render(l.Name))
			}
		}
		for _, n := range mk.RemovedLabels {
			rows = append(rows, t.pending().Strikethrough(true).Render(n))
		}
		if len(rows) == 0 {
			rows = []string{t.muted().Render("—")}
		}
		for k, r := range rows {
			name := "Labels"
			if k > 0 {
				name = ""
			}
			s.panel = append(s.panel, panelRow(t, name, r))
		}
		s.panel = append(s.panel,
			panelRow(t, "Board", board),
			panelRow(t, "Milestone", milestone),
			panelRow(t, "URL", t.muted().Render(shortURL(i.URL))),
		)
	} else {
		s.head = []string{
			" " + t.bold().Render(i.Title),
			" " + t.muted().Render(author(i.Author)+" opened "+agoPhrase(c.Now, i.CreatedAt)+" · updated "+agoPhrase(c.Now, i.UpdatedAt)+" · "+comments),
			" " + t.muted().Render("Assignees ") + assignees + t.muted().Render("  Labels ") + labels +
				t.muted().Render("  Board ") + board + t.muted().Render("  Milestone ") + milestone,
			rule(t, "", area.W),
		}
		s.panel = nil
	}
	s.set(body, anchors, cw, max(1, area.H-len(s.head)))
}

func (s *IssueDetail) View(c *Ctx, area Rect) string {
	return detailView(c, area, s.head, s.panel, &s.scroller)
}

// ---- pull request detail (UI 3.4) ----

// PRDetail shows one pull request, read-only.
type PRDetail struct {
	number int
	scroller
	head  []string
	panel []string
}

func newPRDetail(n int) *PRDetail {
	return &PRDetail{number: n, scroller: scroller{vp: viewport.New()}}
}

func (s *PRDetail) ID() ScreenID                { return ScreenPR }
func (s *PRDetail) Mode() InputMode             { return ModeNav }
func (s *PRDetail) Cursor(*Ctx) *tea.Cursor     { return nil }
func (s *PRDetail) Position(*Ctx) string        { return s.position() }
func (s *PRDetail) SubBar(c *Ctx, w int) string { return detailSub(c, "‹ Pull requests", s.head, w) }
func (s *PRDetail) Title(*Ctx) string           { return "#" + itoaPlain(s.number) }
func (s *PRDetail) Actions(*Ctx) []Action {
	return append(append([]Action{}, scrollActions...), ActBack, ActBrowser, ActCopyURL)
}
func (s *PRDetail) Slots() []Slot {
	return []Slot{{Kind: SlotPR, Number: s.number}, {Kind: SlotPRComments, Number: s.number}}
}

func (s *PRDetail) Update(c *Ctx, msg tea.Msg) tea.Cmd {
	m, ok := msg.(ActionMsg)
	if !ok {
		return nil
	}
	p, _ := c.Store.PR(s.number)
	switch m.A {
	case ActBack:
		c.Pop()
	case ActBrowser:
		return c.a.openURL(p.URL)
	case ActCopyURL:
		return c.a.copyURL(p.URL)
	default:
		s.act(m.A)
	}
	return nil
}

func (s *PRDetail) Renders(c *Ctx) []renderReq {
	d := c.Store.PRDetail(s.number)
	if d.Detail == nil {
		return nil
	}
	_, cw := detailLayout(c.Frame.Body)
	w := mdWidth(cw)
	reqs := []renderReq{{Src: d.Detail.Body, Width: w}}
	for _, cm := range d.Comments {
		reqs = append(reqs, renderReq{Src: cm.Body, Width: w})
	}
	return reqs
}

func (s *PRDetail) Sync(c *Ctx) {
	t := c.Theme
	area := c.Frame.Body
	wide, cw := detailLayout(area)
	p, _ := c.Store.PR(s.number)
	d := c.Store.PRDetail(s.number)
	if s.fresh(c.sig(d.Detail == nil || d.CommentsPhase != PhaseComplete)) {
		return
	}

	mergeable := t.muted().Render("—")
	closes := ""
	if d.Detail != nil {
		switch d.Detail.Mergeable {
		case "mergeable":
			mergeable = t.fg(t.Open).Render("✓ no conflicts")
		case "conflicting":
			mergeable = t.fg(t.Error).Render("✗ conflicts")
		default:
			mergeable = t.muted().Render("unknown")
		}
		var ns []string
		for _, n := range d.Detail.Closes {
			ns = append(ns, "#"+itoaPlain(n))
		}
		closes = strings.Join(ns, " ")
	}
	rev := review(t, p.Review, true)
	if rev == "" {
		rev = t.muted().Render("none")
	}
	chk := checks(t, p.Checks, true)
	if chk == "" {
		chk = t.muted().Render("none")
	}

	var body []string
	switch {
	case d.Detail == nil && d.DetailErr == nil:
		body = []string{"", "  " + t.muted().Render(c.Spin+" Loading…")}
	case d.Detail == nil:
		body = append([]string{""}, indentAll(failed(t, "Couldn't load #"+itoaPlain(s.number), d.DetailErr))...)
	default:
		body = append(refreshBanner(t, d.DetailErr), detailBody(c, d.Detail.Body, cw)...)
	}
	var anchors []int
	if d.Detail != nil {
		body, anchors = commentLines(c, &s.blocks, body, anchors, d.Comments, d.CommentsPhase, d.CommentsTotal, d.CommentsErr, cw, "pull request")
	}

	if wide {
		s.head = []string{rule(t, "", cw) + t.subtle().Render("┬"+strings.Repeat("─", sidePanelW))}
		s.panel = []string{
			panelRow(t, "Author", t.text().Render(author(p.Author))),
			panelRow(t, "Opened", t.text().Render(agoLong(c.Now, p.CreatedAt))),
			panelRow(t, "Updated", t.text().Render(agoLong(c.Now, p.UpdatedAt))),
			panelRow(t, "Branch", t.text().Render(p.Head+" → "+p.Base)),
			panelRow(t, "Review", rev),
			panelRow(t, "Checks", chk),
			panelRow(t, "Mergeable", mergeable),
		}
		if closes != "" {
			s.panel = append(s.panel, panelRow(t, "Closes", t.text().Render(closes)))
		}
		s.panel = append(s.panel, panelRow(t, "URL", t.muted().Render(shortURL(p.URL))))
	} else {
		s.head = []string{
			" " + t.bold().Render(p.Title),
			" " + t.muted().Render(author(p.Author)+" wants to merge "+p.Head+" → "+p.Base+" · updated "+agoPhrase(c.Now, p.UpdatedAt)),
			" " + t.muted().Render("Review ") + rev + t.muted().Render("   Checks ") + chk + t.muted().Render("   Mergeable ") + mergeable,
		}
		if closes != "" {
			s.head = append(s.head, " "+t.muted().Render("Closes ")+t.text().Render(closes))
		}
		s.head = append(s.head, rule(t, "", area.W))
		s.panel = nil
	}
	s.set(body, anchors, cw, max(1, area.H-len(s.head)))
}

func (s *PRDetail) View(c *Ctx, area Rect) string {
	return detailView(c, area, s.head, s.panel, &s.scroller)
}

// detailSub is the sub-bar of a detail screen: the back label, the number and
// the state word; at wide widths also the title (UI 3.2b).
func detailSub(c *Ctx, back string, _ []string, w int) string {
	t := c.Theme
	var num, title, state string
	switch s := c.a.top(c.a.active).(type) {
	case *IssueDetail:
		i, _ := c.Issue(s.number)
		num, title, state = "#"+itoaPlain(s.number), i.Title, issueStateWord(t, i)
	case *PRDetail:
		p, _ := c.Store.PR(s.number)
		num, title, state = "#"+itoaPlain(s.number), p.Title, prStateWord(t, p)
	}
	left := " " + t.accent().Render(back) + "  " + t.text().Render(num)
	if w >= sidePanelAt {
		left += "  " + t.bold().Render(title)
	}
	return spread(left, state+" ", w)
}

func detailView(c *Ctx, area Rect, head, panel []string, sc *scroller) string {
	lines := append([]string{}, head...)
	h := area.H - len(head)
	if panel != nil {
		_, cw := detailLayout(area)
		lines = append(lines, joinPanel(c.Theme, sc.vp.View(), panel, cw, h)...)
	} else {
		lines = append(lines, strings.Split(sc.vp.View(), "\n")...)
	}
	return box(lines, area.W, area.H)
}

func indentAll(ls []string) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = "  " + l
	}
	return out
}

// shortURL is "github.com/…/142" for the side panel.
func shortURL(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if lipgloss.Width(u) <= sidePanelW-12 {
		return u
	}
	parts := strings.Split(u, "/")
	if len(parts) < 3 {
		return u
	}
	return parts[0] + "/…/" + parts[len(parts)-1]
}
