package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/pkieltyka/todos/internal/domain"
)

const cardRows = 4 // three lines plus a gap (UI 3.5)

// Board is the board view (UI 3.5). Columns are derived from the store
// (A5, tui.md 7.5); "only mine" and "/" are local here (5.2).
type Board struct {
	col       int
	focusKey  string
	sel       int
	prevIdx   map[string]int
	scroll    map[string]int
	colOffset int
	filter    lineInput
	filtering bool
	mine      bool
	cols      []DisplayColumn
	total     int // cards before local filters
}

func newBoard() *Board { return &Board{prevIdx: map[string]int{}, scroll: map[string]int{}} }

func (s *Board) ID() ScreenID             { return ScreenBoard }
func (s *Board) Slots() []Slot            { return nil }
func (s *Board) Title(*Ctx) string        { return "board" }
func (s *Board) Renders(*Ctx) []renderReq { return nil }
func (s *Board) Mode() InputMode {
	if s.filtering {
		return ModeText
	}
	return ModeNav
}

func (s *Board) focused() *DisplayColumn {
	if s.col < 0 || s.col >= len(s.cols) {
		return nil
	}
	return &s.cols[s.col]
}

func (s *Board) Position(*Ctx) string {
	f := s.focused()
	if f == nil || len(f.Numbers) == 0 {
		return ""
	}
	i, _ := indexOf(f.Numbers, s.sel)
	return itoa(i+1) + "/" + itoa(len(f.Numbers))
}

func (s *Board) Actions(c *Ctx) []Action {
	if !c.Store.Board.Loaded {
		return []Action{ActChooseProject}
	}
	acts := []Action{ActLeft, ActRight, ActDown, ActUp, ActTop, ActBottom, ActHalfDown, ActHalfUp}
	has := s.focused() != nil && len(s.focused().Numbers) > 0
	if has {
		acts = append(acts, ActOpen)
	}
	if s.filter.String() != "" {
		acts = append(acts, ActBack)
	}
	acts = append(acts, ActFilter, ActMine)
	if has {
		acts = append(acts, ActBrowser, ActCopyURL)
	}
	acts = append(acts, ActChooseProject)
	if c.Store.Board.Hidden > 0 {
		acts = append(acts, ActHiddenInfo)
	}
	return acts
}

func (s *Board) cardsVisible(c *Ctx) int {
	h := c.Frame.Body.H - 2
	if banner(c, c.Store.Board.Err, c.Store.Board.Loaded, c.Store.Board.LoadedAt) != "" {
		h--
	}
	return max(1, h/cardRows)
}

func (s *Board) Sync(c *Ctx) {
	all := c.Store.Columns(c.Store.Issue, nil)
	q := s.filter.String()
	s.total = 0
	for i := range all {
		s.total += len(all[i].Numbers)
		var keep []int
		for _, n := range all[i].Numbers {
			is, ok := c.Store.Issue(n)
			if !ok || !matches(is, q) {
				continue
			}
			if s.mine && !contains(is.Assignees, c.Viewer) {
				continue
			}
			keep = append(keep, n)
		}
		all[i].Numbers = keep
	}
	s.cols = all
	// Keep the focused column by its option ID.
	found := false
	for i, col := range s.cols {
		if col.Key == s.focusKey && (s.focusKey != "" || col.Status == nil) {
			s.col, found = i, true
			break
		}
	}
	if !found {
		s.col = clamp(s.col, 0, len(s.cols)-1)
	}
	f := s.focused()
	if f == nil {
		return
	}
	s.focusKey = f.Key
	vis := s.cardsVisible(c)
	sel, off := reselect(f.Numbers, s.sel, s.prevIdx[f.Key], s.scroll[f.Key], vis)
	s.sel, s.scroll[f.Key] = sel, off
	if i, ok := indexOf(f.Numbers, sel); ok {
		s.prevIdx[f.Key] = i
	}
	_, s.colOffset, _ = BoardWindow(c.Frame.Body.W, len(s.cols), s.col, s.colOffset)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (s *Board) Update(c *Ctx, msg tea.Msg) tea.Cmd {
	if s.filtering {
		if k, ok := msg.(tea.KeyPressMsg); ok {
			enter, esc := s.filter.key(k)
			if esc {
				s.filter.Clear()
			}
			if enter || esc {
				s.filtering = false
			}
		}
		if p, ok := msg.(tea.PasteMsg); ok {
			s.filter.insert(p.Content)
		}
		return nil
	}
	m, ok := msg.(ActionMsg)
	if !ok {
		return nil
	}
	f := s.focused()
	switch m.A {
	case ActLeft, ActRight:
		d := 1
		if m.A == ActLeft {
			d = -1
		}
		s.col = clamp(s.col+d, 0, len(s.cols)-1)
		if g := s.focused(); g != nil {
			s.focusKey = g.Key
			if len(g.Numbers) > 0 {
				s.sel = g.Numbers[clamp(s.prevIdx[g.Key], 0, len(g.Numbers)-1)]
			}
		}
	case ActDown, ActUp, ActTop, ActBottom, ActHalfDown, ActHalfUp:
		if f != nil && len(f.Numbers) > 0 {
			i, _ := indexOf(f.Numbers, s.sel)
			vis := s.cardsVisible(c)
			i = listNav(m.A, i, len(f.Numbers), vis*2)
			s.sel = f.Numbers[i]
			s.prevIdx[f.Key] = i
			s.scroll[f.Key] = clampOffset(s.scroll[f.Key], i, vis, len(f.Numbers))
		}
	case ActOpen:
		if f != nil && len(f.Numbers) > 0 {
			c.Push(newIssueDetail(s.sel, "Board"))
			return c.a.loadIssue(s.sel, ReasonOpen)
		}
	case ActBack:
		s.filter.Clear()
	case ActFilter:
		s.filtering = true
	case ActMine:
		s.mine = !s.mine
	case ActBrowser, ActCopyURL:
		if i, ok := c.Store.Issue(s.sel); ok {
			if m.A == ActBrowser {
				return c.a.openURL(i.URL)
			}
			return c.a.copyURL(i.URL)
		}
	case ActChooseProject:
		ch := newBoardSetup(SetupChoosing, nil)
		ch.pushed = true
		c.Push(ch)
		return c.a.loadCandidates()
	case ActHiddenInfo:
		n := c.Store.Board.Hidden
		what := "items on this board are"
		if n == 1 {
			what = "item on this board is"
		}
		return c.a.toastInfo(itoa(n) + " " + what + " not an issue of " + c.Repo.Owner + "/" + c.Repo.Name +
			" (pull requests, drafts, other repositories), so not shown.")
	}
	return nil
}

func (s *Board) SubBar(c *Ctx, w int) string {
	t := c.Theme
	b := c.Store.Board
	name := "—"
	if b.Info != nil {
		name = line(b.Info.Project.Title) + " (#" + itoaPlain(b.Info.Project.Ref.Number) + ")"
	}
	left := " " + t.text().Render("Board: "+name) + t.subtle().Render(" · ") + t.muted().Render("mine: "+onOff(s.mine)) +
		t.subtle().Render(" · ") + filterLabel(t, s.filtering, s.filter.String())
	if !b.Loaded {
		return spread(left, "", w)
	}
	shown := 0
	for _, col := range s.cols {
		shown += len(col.Numbers)
	}
	right := itoa(s.total) + " cards"
	if s.mine || s.filter.String() != "" {
		right = itoa(shown) + " of " + itoa(s.total) + " cards"
	}
	if b.CardsTruncated || b.Truncated || s.total >= cardsCap {
		right += " · board cut off"
	}
	if b.Hidden > 0 {
		right += " · " + itoa(b.Hidden) + " hidden ⓘ"
	}
	return spread(left, t.muted().Render(right)+" ", w)
}

func (s *Board) Cursor(c *Ctx) *tea.Cursor {
	b := c.Store.Board
	name := "—"
	if b.Info != nil {
		name = line(b.Info.Project.Title) + " (#" + itoaPlain(b.Info.Project.Ref.Number) + ")"
	}
	prefix := " Board: " + name + " · mine: " + onOff(s.mine) + " · /"
	return tea.NewCursor(lipgloss.Width(prefix)+len(s.filter.value), c.Frame.SubBar.Y)
}

func (s *Board) View(c *Ctx, area Rect) string {
	t := c.Theme
	b := c.Store.Board
	switch {
	case !b.Loaded && b.Err != nil:
		return centered(failed(t, "Couldn't load the board", b.Err), area)
	case !b.Loaded:
		return centered([]string{t.muted().Render(c.Spin + " Loading board…")}, area)
	}
	var lines []string
	if bn := banner(c, b.Err, b.Loaded, b.LoadedAt); bn != "" {
		lines = append(lines, bn)
	}
	h := area.H - len(lines)
	colW, first, count := BoardWindow(area.W, len(s.cols), s.col, s.colOffset)
	rendered := make([][]string, 0, count)
	for i := first; i < first+count; i++ {
		rendered = append(rendered, s.column(c, s.cols[i], i == s.col, colW, h))
	}
	for r := 0; r < h; r++ {
		left, right := "   ", "   "
		if r == 0 && first > 0 {
			left = t.muted().Render(fit("‹"+itoaPlain(first), 3))
		}
		if r == 0 && first+count < len(s.cols) {
			right = t.muted().Render(fit(" "+itoaPlain(len(s.cols)-first-count)+"›", 3))
		}
		var row []string
		for _, col := range rendered {
			row = append(row, col[r])
		}
		lines = append(lines, left+strings.Join(row, " ")+right)
	}
	return box(lines, area.W, area.H)
}

// column draws one board column as h lines of exactly w cells.
func (s *Board) column(c *Ctx, col DisplayColumn, focused bool, w, h int) []string {
	t := c.Theme
	tl, tr, bl, br, hz, vt := "┌", "┐", "└", "┘", "─", "│"
	bs := t.subtle()
	if focused {
		tl, tr, bl, br, hz, vt = "┏", "┓", "┗", "┛", "━", "┃"
		bs = t.accent()
	}
	title := " " + col.Name + "  " + itoaPlain(len(col.Numbers)) + " "
	title = ansi.Truncate(title, w-2, "… ")
	top := bs.Render(tl) + t.text().Bold(focused).Render(title) + bs.Render(strings.Repeat(hz, max(0, w-2-lipgloss.Width(title)))+tr)
	out := []string{top}
	inner := h - 2
	cw := w - 4
	vis := max(1, inner/cardRows)
	off := s.scroll[col.Key]
	if !focused {
		off = clampOffset(off, 0, vis, len(col.Numbers))
	}
	var body []string
	for k := off; k < len(col.Numbers) && k < off+vis; k++ {
		n := col.Numbers[k]
		i, _ := c.Store.Issue(n)
		sel := focused && n == s.sel
		for li, l := range cardLines(t, i, cw) {
			g := gutter(t, sel && li == 0)
			cell := g + fit(l, cw) + " "
			if sel {
				cell = t.selected(cell)
			}
			body = append(body, cell)
		}
		body = append(body, strings.Repeat(" ", cw+2))
	}
	if more := len(col.Numbers) - off - vis; more > 0 && len(body) > 0 {
		body[len(body)-1] = " " + fit(t.muted().Render("…"+itoa(more)+" more"), cw) + " "
	}
	for r := 0; r < inner; r++ {
		cell := strings.Repeat(" ", cw+2)
		if r < len(body) {
			cell = body[r]
		}
		out = append(out, bs.Render(vt)+cell+bs.Render(vt))
	}
	out = append(out, bs.Render(bl+strings.Repeat(hz, w-2)+br))
	return out
}

// cardLines is a card: the number and title over two lines, then labels and
// the first assignee (UI 3.5).
func cardLines(t Theme, i domain.Issue, w int) []string {
	num := "#" + itoaPlain(i.Number) + " "
	wrapped := strings.Split(ansi.Wordwrap(num+i.Title, w, ""), "\n")
	l1, l2 := wrapped[0], ""
	if len(wrapped) > 1 {
		l2 = wrapped[1]
		if len(wrapped) > 2 {
			l2 = ansi.Truncate(l2+" "+wrapped[2], w, "…")
		}
	}
	l1 = t.muted().Render(num) + t.text().Render(strings.TrimPrefix(l1, num))
	var names []string
	for _, l := range i.Labels {
		names = append(names, l.Name)
	}
	meta := t.muted().Render(strings.Join(names, " "))
	if len(names) == 0 {
		meta = ""
	} else {
		meta += t.subtle().Render(" · ")
	}
	meta += people(t, i.Assignees)
	return []string{l1, t.text().Render(l2), meta}
}

// ---- board setup states (UI 3.16) ----

// SetupState is the board setup state, chosen by counting
// Error.Candidates (tui.md 4.2).
type SetupState int

const (
	SetupNoBoard SetupState = iota
	SetupSeveral
	SetupMissingScope
	SetupNoStatusField
	SetupFailed
	SetupChoosing // the project picker opened with P
)

func setupStateFor(de *domain.Error) SetupState {
	if de == nil {
		return SetupFailed
	}
	switch {
	case de.Kind == domain.ErrMissingScope:
		return SetupMissingScope
	case de.Kind != domain.ErrNoBoard:
		return SetupFailed
	case len(de.Candidates) == 0:
		return SetupNoBoard
	case len(de.Candidates) == 1:
		return SetupNoStatusField
	}
	return SetupSeveral
}

// BoardSetup replaces the board when it cannot be shown, and is the project
// picker for P.
type BoardSetup struct {
	state    SetupState
	prev     SetupState
	err      *domain.Error
	projects []domain.Project
	cursor   int
	loading  bool
	candErr  *domain.Error // the candidate list failed to load (finding 11)
	pushed   bool          // opened with P from the board: esc pops back
}

func newBoardSetup(state SetupState, err *domain.Error) *BoardSetup {
	s := &BoardSetup{state: state, err: err}
	if state == SetupSeveral && err != nil {
		s.projects = err.Candidates
	}
	if state == SetupChoosing {
		s.loading = true
	}
	return s
}

func (s *BoardSetup) setCandidates(ps []domain.Project) {
	s.loading = false
	s.candErr = nil
	s.projects = ps
	s.cursor = 0
}

// candidatesFailed leaves the loading state and keeps the reason on screen,
// with P to try again (finding 11).
func (s *BoardSetup) candidatesFailed(err *domain.Error) {
	s.loading = false
	s.candErr = err
	s.projects = nil
}

func (s *BoardSetup) ID() ScreenID             { return ScreenBoardSetup }
func (s *BoardSetup) Mode() InputMode          { return ModeNav }
func (s *BoardSetup) Cursor(*Ctx) *tea.Cursor  { return nil }
func (s *BoardSetup) Slots() []Slot            { return []Slot{{Kind: SlotCandidates}} }
func (s *BoardSetup) Title(*Ctx) string        { return "board" }
func (s *BoardSetup) Renders(*Ctx) []renderReq { return nil }
func (s *BoardSetup) Sync(*Ctx)                { s.cursor = clamp(s.cursor, 0, max(0, len(s.projects)-1)) }
func (s *BoardSetup) SubBar(c *Ctx, w int) string {
	return fit(" "+c.Theme.muted().Render("Board"), w)
}

func (s *BoardSetup) Position(*Ctx) string {
	if s.picking() && len(s.projects) > 0 {
		return itoa(s.cursor+1) + "/" + itoa(len(s.projects))
	}
	return ""
}

func (s *BoardSetup) picking() bool { return s.state == SetupSeveral || s.state == SetupChoosing }

func (s *BoardSetup) Actions(*Ctx) []Action {
	switch s.state {
	case SetupNoBoard, SetupNoStatusField:
		return []Action{ActChooseProject}
	case SetupSeveral, SetupChoosing:
		acts := []Action{ActDown, ActUp}
		if len(s.projects) > 0 {
			acts = append(acts, ActOpen)
		}
		if s.candErr != nil {
			acts = append(acts, ActChooseProject)
		}
		if s.state == SetupChoosing {
			acts = append(acts, ActBack)
		}
		return acts
	}
	return nil
}

func (s *BoardSetup) Update(c *Ctx, msg tea.Msg) tea.Cmd {
	m, ok := msg.(ActionMsg)
	if !ok {
		return nil
	}
	switch m.A {
	case ActChooseProject:
		if s.state != SetupChoosing {
			s.prev = s.state
		}
		s.state, s.loading, s.candErr = SetupChoosing, true, nil
		return c.a.loadCandidates()
	case ActDown, ActUp:
		s.cursor = listNav(m.A, s.cursor, len(s.projects), 2)
	case ActOpen:
		if len(s.projects) > 0 {
			return c.a.chooseBoard(s.projects[s.cursor].Ref)
		}
	case ActBack:
		if s.pushed {
			c.Pop()
		} else {
			s.state = s.prev
			c.a.cancel(Slot{Kind: SlotCandidates})
		}
	}
	return nil
}

func (s *BoardSetup) View(c *Ctx, area Rect) string {
	t := c.Theme
	repo := c.Repo.Owner + "/" + c.Repo.Name
	var lines []string
	switch s.state {
	case SetupNoBoard:
		lines = []string{
			t.text().Render("No project board is linked to " + repo + "."),
			"",
			t.muted().Render("Link one on GitHub (repository → Projects → Link a project),"),
			t.muted().Render("or start with:  ") + t.text().Render("todos --project <number>"),
			t.accent().Render("P") + t.muted().Render(" choose from your projects"),
		}
	case SetupMissingScope:
		lines = []string{
			t.text().Render(`The board needs the "project" permission on your GitHub token.`),
			"",
			t.muted().Render("Run:  ") + t.text().Render("gh auth refresh -s project"),
			t.muted().Render("then press ") + t.accent().Render("r") + t.muted().Render("."),
		}
	case SetupNoStatusField:
		name := "The project"
		if s.err != nil && len(s.err.Candidates) == 1 {
			p := s.err.Candidates[0]
			name = `Project "` + line(p.Title) + `" (#` + itoaPlain(p.Ref.Number) + `)`
		}
		lines = []string{
			t.text().Render(name + ` has no "Status" field, so it can't be shown as a board.`),
			t.accent().Render("P") + t.muted().Render(" choose another project"),
		}
	case SetupFailed:
		lines = failed(t, "Couldn't load the board", s.err)
	case SetupSeveral, SetupChoosing:
		lines = s.pickerLines(c, repo)
	}
	return centeredBlock(lines, area)
}

func (s *BoardSetup) pickerLines(c *Ctx, repo string) []string {
	t := c.Theme
	head := "Several projects are linked to " + repo + ". Pick one:"
	if s.state == SetupChoosing {
		head = "Choose a project for " + repo + ":"
	}
	lines := []string{t.text().Render(head), ""}
	switch {
	case s.loading:
		lines = append(lines, t.muted().Render(c.Spin+" Loading projects…"))
	case s.candErr != nil:
		lines = append(lines, failed(t, "Couldn't list projects", s.candErr)...)
		lines = append(lines, t.accent().Render("P")+t.muted().Render(" try again"))
	case len(s.projects) == 0:
		lines = append(lines, t.muted().Render("No projects found. Link one on GitHub first."))
	}
	for i, p := range s.projects {
		bar := " "
		if i == s.cursor {
			bar = t.accent().Render("▌")
		}
		name := fit(t.text().Render(line(p.Title)+" (#"+itoaPlain(p.Ref.Number)+")"), 22)
		meta := itoa(p.TotalItems) + " items · updated " + ago(c.Now, p.UpdatedAt) + " ago"
		if !p.Usable {
			meta += " · no Status field"
		}
		row := bar + name + " " + t.muted().Render(meta)
		if i == s.cursor {
			row = t.selected(row)
		}
		lines = append(lines, row)
	}
	lines = append(lines, "")
	hint := t.accent().Render("enter") + t.muted().Render(" use and remember")
	if s.state == SetupChoosing {
		hint += "   " + t.accent().Render("esc") + t.muted().Render(" cancel")
	}
	return append(lines, hint)
}

// centeredBlock centres a block of lines as a whole, keeping it left-aligned
// inside (the board setup texts read as one paragraph).
func centeredBlock(lines []string, area Rect) string {
	w := 0
	for _, l := range lines {
		w = max(w, lipgloss.Width(l))
	}
	left := max(1, (area.W-w)/2)
	top := max(0, (area.H-len(lines))/2)
	out := make([]string, area.H)
	for i := range out {
		if j := i - top; j >= 0 && j < len(lines) {
			out[i] = strings.Repeat(" ", left) + lines[j]
		}
	}
	return box(out, area.W, area.H)
}
