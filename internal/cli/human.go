package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/pkieltyka/todos/internal/domain"
	"github.com/pkieltyka/todos/internal/markdown"
)

// These roles match ui_design.md 6.1; the CLI chooses a background without
// querying the terminal. Colour is applied only to already-sanitised text.
func (r *runtime) paint(role, text string) string {
	if !r.colours() {
		return text
	}
	colours := map[string][2]string{"text": {"#E6EDF3", "#1F2328"}, "muted": {"#8B949E", "#656D76"}, "accent": {"#58A6FF", "#0969DA"}, "open": {"#3FB950", "#1A7F37"}, "done": {"#A371F7", "#8250DF"}, "inactive": {"#8B949E", "#656D76"}, "warning": {"#D29922", "#9A6700"}, "error": {"#F85149", "#CF222E"}}
	index := 0
	if strings.EqualFold(strings.TrimSpace(r.deps.Getenv("TODOS_THEME")), "light") {
		index = 1
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(colours[role][index])).Render(text)
}
func relative(raw string, now time.Time) string {
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return markdown.SanitizeLine(raw)
	}
	if at.IsZero() {
		return "—"
	}
	d := max(0, now.Sub(at))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/24/7))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/24/365))
	}
}
func relativeLong(raw string, now time.Time) string {
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return markdown.SanitizeLine(raw)
	}
	if at.IsZero() {
		return "—"
	}
	d := max(0, now.Sub(at))
	n, unit := 0, "second"
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		n, unit = int(d.Minutes()), "minute"
	case d < 24*time.Hour:
		n, unit = int(d.Hours()), "hour"
	case d < 7*24*time.Hour:
		n, unit = int(d.Hours()/24), "day"
	case d < 30*24*time.Hour:
		n, unit = int(d.Hours()/24/7), "week"
	case d < 365*24*time.Hour:
		n, unit = int(d.Hours()/24/30), "month"
	default:
		n, unit = int(d.Hours()/24/365), "year"
	}
	if n != 1 {
		unit += "s"
	}
	return fmt.Sprintf("%d %s ago", n, unit)
}
func (r *runtime) issueState(i IssueDTO) (glyph, word string) {
	glyph, word, role := "○", "Open", "open"
	if i.State == "closed" {
		glyph, word, role = "✓", "Closed", "done"
		if i.StateReason != nil && *i.StateReason == "not_planned" {
			glyph, word, role = "⊘", "Closed as not planned", "inactive"
		}
	}
	return r.paint(role, glyph), r.paint(role, word)
}
func (r *runtime) prState(p PRDTO) (glyph, word string) {
	glyph, word, role := "○", "Open", "open"
	switch {
	case p.State == "merged":
		glyph, word, role = "●", "Merged", "done"
	case p.State == "closed":
		glyph, word, role = "✕", "Closed", "inactive"
	case p.Draft:
		glyph, word, role = "◇", "Draft", "inactive"
	}
	return r.paint(role, glyph), r.paint(role, word)
}
func (r *runtime) review(state string) string {
	switch state {
	case "approved":
		return r.paint("open", "✓ approved")
	case "changes_requested":
		return r.paint("error", "✗ changes requested")
	case "review_required":
		return r.paint("warning", "○ review required")
	default:
		return r.paint("muted", "—")
	}
}
func (r *runtime) checks(state string) string {
	switch state {
	case "passing":
		return r.paint("open", "✓")
	case "failing":
		return r.paint("error", "✗")
	case "pending":
		return r.paint("warning", "…")
	default:
		return r.paint("muted", "—")
	}
}
func joined(v []string) string {
	if len(v) == 0 {
		return "—"
	}
	return markdown.SanitizeLine(strings.Join(v, ", "))
}
func login(p *string) string {
	if p == nil {
		return "ghost"
	}
	return markdown.SanitizeLine(*p)
}
func boardStatus(status *string, onBoard bool) string {
	if status != nil {
		return markdown.SanitizeLine(*status)
	}
	if onBoard {
		return "No status"
	}
	return "—"
}
func (r *runtime) noun(pr bool) string {
	noun := "issues"
	if pr {
		noun = "pull requests"
	}
	if r.listState != "" && r.listState != "all" {
		return r.listState + " " + noun
	}
	return noun
}
func (r *runtime) humanSummary(w io.Writer, shown, total int, truncated bool, noun string) {
	if truncated {
		r.paragraph(w, fmt.Sprintf("Showing %d of %d %s. Use --limit N or --all for more.", shown, total, noun))
	}
}

// All columns use display cells, including padding and truncation. Optional
// columns drop in the same priority order as their TUI counterparts.
type humanColumn struct {
	header         string
	min, max, drop int
	grow           bool
}

func (r *runtime) table(w io.Writer, cols []humanColumn, rows [][]string, header bool, indent int) {
	r.tableMeasured(w, cols, rows, rows, header, indent)
}

func (r *runtime) tableMeasured(w io.Writer, cols []humanColumn, rows, measure [][]string, header bool, indent int) {
	widths := make([]int, len(cols))
	active := make([]bool, len(cols))
	available := max(1, r.width()-indent)
	for i, col := range cols {
		active[i] = true
		widths[i] = max(col.min, ansi.StringWidth(col.header))
		if !col.grow {
			for _, row := range measure {
				widths[i] = max(widths[i], ansi.StringWidth(row[i]))
			}
			if col.max > 0 {
				widths[i] = min(widths[i], col.max)
			}
		}
	}
	total := func() int {
		size, count := 0, 0
		for i, a := range active {
			if a {
				size += widths[i]
				count++
			}
		}
		return size + max(0, count-1)*2
	}
	for total() > available {
		drop := -1
		for i, col := range cols {
			if active[i] && col.drop > 0 && (drop < 0 || col.drop > cols[drop].drop) {
				drop = i
			}
		}
		if drop < 0 {
			break
		}
		active[drop] = false
	}
	for total() > available {
		shrink := -1
		for i := range cols {
			if active[i] && widths[i] > 1 && (shrink < 0 || widths[i] > widths[shrink]) {
				shrink = i
			}
		}
		if shrink < 0 {
			break
		}
		widths[shrink]--
	}
	for i, col := range cols {
		if active[i] && col.grow {
			widths[i] += max(0, available-total())
			break
		}
	}
	emit := func(values []string, heading bool) {
		var cells []string
		for i, a := range active {
			if !a {
				continue
			}
			cell := ansi.Truncate(values[i], widths[i], "…")
			cell += strings.Repeat(" ", max(0, widths[i]-ansi.StringWidth(cell)))
			cells = append(cells, cell)
		}
		line := strings.TrimRight(strings.Join(cells, "  "), " ")
		if heading {
			line = r.paint("muted", line)
		}
		fmt.Fprintln(w, strings.Repeat(" ", indent)+line)
	}
	if header {
		heads := make([]string, len(cols))
		for i, col := range cols {
			heads[i] = col.header
		}
		emit(heads, true)
	}
	for _, row := range rows {
		emit(row, false)
	}
}
func (r *runtime) textLine(w io.Writer, s string) {
	fmt.Fprintln(w, ansi.Truncate(s, r.width(), "…"))
}
func renderHuman(ctx context.Context, r *runtime, w io.Writer, value any) error {
	switch v := value.(type) {
	case QuickDTO:
		return renderQuick(r, w, v)
	case ChangeDTO, CommentResultDTO, DeletedDTO:
		return renderChange(r, w, value)

	case IssuesDTO:
		if len(v.Issues) == 0 {
			fmt.Fprintf(w, "No %s.\n", r.noun(false))
			return nil
		}
		rows := [][]string{}
		for _, i := range v.Issues {
			glyph, _ := r.issueState(i)
			rows = append(rows, []string{glyph, strconv.Itoa(i.Number), r.paint("text", markdown.SanitizeLine(i.Title)), joined(i.Labels), joined(i.Assignees), relative(i.UpdatedAt, r.deps.Now())})
		}
		r.table(w, []humanColumn{{"", 1, 1, 0, false}, {"#", 3, 7, 0, false}, {"TITLE", 24, 0, 0, true}, {"LABELS", 8, 24, 4, false}, {"ASSIGNEES", 10, 14, 3, false}, {"UPDATED", 3, 12, 2, false}}, rows, true, 0)
		r.humanSummary(w, len(v.Issues), v.Total, v.Truncated, r.noun(false))
	case PRsDTO:
		if len(v.PullRequests) == 0 {
			fmt.Fprintf(w, "No %s.\n", r.noun(true))
			return nil
		}
		rows := [][]string{}
		for _, p := range v.PullRequests {
			glyph, _ := r.prState(p)
			rows = append(rows, []string{glyph, strconv.Itoa(p.Number), r.paint("text", markdown.SanitizeLine(p.Title)), markdown.SanitizeLine(p.Head), r.review(p.Review), r.checks(p.Checks), relative(p.UpdatedAt, r.deps.Now()), login(p.Author)})
		}
		r.table(w, []humanColumn{{"", 1, 1, 0, false}, {"#", 3, 7, 0, false}, {"TITLE", 24, 0, 0, true}, {"BRANCH", 12, 24, 4, false}, {"REVIEW", 10, 19, 3, false}, {"CHECKS", 2, 9, 1, false}, {"UPDATED", 3, 12, 2, false}, {"AUTHOR", 10, 14, 5, false}}, rows, true, 0)
		r.humanSummary(w, len(v.PullRequests), v.Total, v.Truncated, r.noun(true))
	case BoardDTO:
		r.textLine(w, r.paint("text", fmt.Sprintf("%s (#%d)  %s", markdown.SanitizeLine(v.Project.Title), v.Project.Number, markdown.SanitizeLine(v.Project.URL))))
		cards := 0
		allRows := [][]string{}
		for _, col := range v.Columns {
			for _, card := range col.Cards {
				allRows = append(allRows, boardRow(card))
			}
		}
		for _, col := range v.Columns {
			name := boardStatus(col.Status, true)
			fmt.Fprintln(w)
			r.textLine(w, r.paint("accent", fmt.Sprintf("%s (%d)", name, len(col.Cards))))
			rows := [][]string{}
			for _, card := range col.Cards {
				cards++
				rows = append(rows, boardRow(card))
			}
			r.tableMeasured(w, []humanColumn{{"", 4, 8, 0, false}, {"", 24, 0, 0, true}, {"", 8, 24, 2, false}, {"", 10, 14, 1, false}}, rows, allRows, false, 2)
		}
		if cards == 0 {
			fmt.Fprintln(w, "No issues on this board.")
		}
		if v.HiddenItems > 0 {
			noun, verb := "items", "issues"
			if v.HiddenItems == 1 {
				noun, verb = "item", "an issue"
			}
			r.paragraph(w, fmt.Sprintf("%d %s hidden (not %s of this repository).", v.HiddenItems, noun, verb))
		}
		if v.Truncated {
			fmt.Fprintln(w, "Board truncated at 1000 items.")
		}
	case ProjectsDTO:
		if len(v.Projects) == 0 {
			fmt.Fprintln(w, "No projects found.")
			return nil
		}
		rows := [][]string{}
		for _, p := range v.Projects {
			status := "usable"
			if !p.Usable {
				status = "no Status field"
			}
			if v.Selected != nil && p.Owner == v.Selected.Owner && p.Number == v.Selected.Number {
				status += " (selected)"
			}
			rows = append(rows, []string{fmt.Sprintf("%s/%d", markdown.SanitizeLine(p.Owner), p.Number), markdown.SanitizeLine(p.Title), status, markdown.SanitizeLine(p.URL)})
		}
		r.table(w, []humanColumn{{"PROJECT", 12, 24, 0, false}, {"TITLE", 16, 0, 0, true}, {"STATUS", 6, 28, 1, false}, {"URL", 8, 45, 2, false}}, rows, true, 0)
	case ProjectResultDTO:
		r.textLine(w, fmt.Sprintf("Using %s/%d %s  %s", markdown.SanitizeLine(v.Project.Owner), v.Project.Number, markdown.SanitizeLine(v.Project.Title), markdown.SanitizeLine(v.Project.URL)))
	case LabelsDTO:
		if len(v.Labels) == 0 {
			fmt.Fprintln(w, "No labels found.")
			return nil
		}
		rows := [][]string{}
		for _, l := range v.Labels {
			rows = append(rows, []string{markdown.SanitizeLine(l.Name), markdown.SanitizeLine(l.Description)})
		}
		r.table(w, []humanColumn{{"LABEL", 8, 28, 0, false}, {"DESCRIPTION", 16, 0, 0, true}}, rows, true, 0)
	case AssigneesDTO:
		if len(v.Assignees) == 0 {
			fmt.Fprintln(w, "No assignable users found.")
			return nil
		}
		rows := [][]string{}
		for _, u := range v.Assignees {
			rows = append(rows, []string{markdown.SanitizeLine(u.Login), markdown.SanitizeLine(u.Name)})
		}
		r.table(w, []humanColumn{{"LOGIN", 10, 28, 0, false}, {"NAME", 16, 0, 0, true}}, rows, true, 0)
	case DoctorDTO:
		nameWidth := 0
		for _, check := range v.Checks {
			nameWidth = max(nameWidth, ansi.StringWidth(markdown.SanitizeLine(check.Name)))
		}
		nameWidth = min(nameWidth, max(1, r.width()/3))
		for _, check := range v.Checks {
			glyph, role := "✗", "error"
			if check.OK {
				glyph, role = "✓", "open"
			}
			name := ansi.Truncate(markdown.SanitizeLine(check.Name), nameWidth, "…")
			name += strings.Repeat(" ", max(0, nameWidth-ansi.StringWidth(name)))
			r.textLine(w, r.paint(role, glyph)+" "+name+"  "+markdown.SanitizeLine(check.Detail))
			if check.Hint != "" {
				prefix := strings.Repeat(" ", nameWidth+4)
				width := max(1, r.width()-nameWidth-4)
				for _, line := range strings.Split(ansi.Hardwrap("fix: "+markdown.SanitizeLine(check.Hint), width, false), "\n") {
					fmt.Fprintln(w, prefix+r.paint("muted", line))
				}
			}
		}
	case IssueViewDTO:
		return renderHumanIssue(ctx, r, w, v)
	case PRViewDTO:
		return renderHumanPR(ctx, r, w, v)
	default:
		return &domain.Error{Kind: domain.ErrInternal, Message: "unsupported output type"}
	}
	return nil
}
func (r *runtime) detailHeading(w io.Writer, title string, number int, glyph, word string) {
	right := glyph + " " + word
	left := fmt.Sprintf("#%d  %s", number, markdown.SanitizeLine(title))
	space := r.width() - ansi.StringWidth(right) - 2
	left = ansi.Truncate(left, max(1, space), "…")
	gap := max(1, r.width()-ansi.StringWidth(left)-ansi.StringWidth(right))
	fmt.Fprintln(w, r.paint("text", left)+strings.Repeat(" ", gap)+right)
}
func (r *runtime) body(ctx context.Context, w io.Writer, source string) error {
	if ctx.Err() != nil {
		return domainError(ctx.Err())
	}
	if r.renderer == nil {
		r.renderer = markdown.NewRenderer(markdown.Options{BaseURL: "https://" + r.repo.Host + "/" + r.repo.Owner + "/" + r.repo.Name})
	}
	style := markdown.Dark
	if !r.colours() {
		style = markdown.Plain
	} else if strings.EqualFold(strings.TrimSpace(r.deps.Getenv("TODOS_THEME")), "light") {
		style = markdown.Light
	}
	out, err := r.renderer.Render(source, max(1, min(r.width()-2, 100)), style)
	if err != nil {
		return &domain.Error{Kind: domain.ErrInternal, Message: "could not render Markdown", Err: err}
	}
	for _, line := range strings.Split(out, "\n") {
		if style == markdown.Plain {
			line = strings.TrimRight(line, " ")
			if line == "" {
				fmt.Fprintln(w)
				continue
			}
		}
		fmt.Fprintln(w, "  "+line)
	}
	return nil
}
func humanComments(ctx context.Context, r *runtime, w io.Writer, body string, comments []CommentDTO, truncated bool) error {
	fmt.Fprintln(w)
	if err := r.body(ctx, w, body); err != nil {
		return err
	}
	for _, c := range comments {
		fmt.Fprintln(w)
		r.textLine(w, r.paint("muted", "── "+login(c.Author)+" · "+relativeLong(c.CreatedAt, r.deps.Now())))
		if err := r.body(ctx, w, c.Body); err != nil {
			return err
		}
	}
	if truncated {
		fmt.Fprintln(w, "\nComments truncated at 500.")
	}
	return nil
}
func renderHumanIssue(ctx context.Context, r *runtime, w io.Writer, v IssueViewDTO) error {
	glyph, word := r.issueState(v.IssueDTO)
	r.detailHeading(w, v.Title, v.Number, glyph, word)
	r.textLine(w, r.paint("muted", fmt.Sprintf("%s opened %s · updated %s · %d %s", login(v.Author), relativeLong(v.CreatedAt, r.deps.Now()), relativeLong(v.UpdatedAt, r.deps.Now()), v.CommentsCount, commentNoun(v.CommentsCount))))
	meta := "Assignees: " + joined(v.Assignees) + "   Labels: " + joined(v.Labels) + "   Board: " + boardStatus(v.BoardStatus, r.issueOnBoard) + "   Milestone: " + joinedPointer(v.Milestone)
	for _, line := range strings.Split(ansi.Wordwrap(meta, r.width(), ""), "\n") {
		r.textLine(w, line)
	}
	r.textLine(w, r.paint("accent", markdown.SanitizeLine(v.URL)))
	return humanComments(ctx, r, w, v.Body, v.Comments, v.CommentsTruncated)
}
func joinedPointer(v *string) string {
	if v == nil || *v == "" {
		return "—"
	}
	return markdown.SanitizeLine(*v)
}
func renderHumanPR(ctx context.Context, r *runtime, w io.Writer, v PRViewDTO) error {
	glyph, word := r.prState(v.PRDTO)
	r.detailHeading(w, v.Title, v.Number, glyph, word)
	r.textLine(w, r.paint("muted", fmt.Sprintf("%s wants to merge %s → %s · updated %s", login(v.Author), markdown.SanitizeLine(v.Head), markdown.SanitizeLine(v.Base), relativeLong(v.UpdatedAt, r.deps.Now()))))
	r.textLine(w, "Review: "+r.review(v.Review)+"   Checks: "+r.checks(v.Checks)+" "+markdown.SanitizeLine(v.Checks)+"   Mergeable: "+markdown.SanitizeLine(v.Mergeable))
	if len(v.Closes) > 0 {
		ns := []string{}
		for _, n := range v.Closes {
			ns = append(ns, fmt.Sprintf("#%d", n))
		}
		r.textLine(w, "Closes: "+strings.Join(ns, " "))
	}
	r.textLine(w, r.paint("accent", markdown.SanitizeLine(v.URL)))
	return humanComments(ctx, r, w, v.Body, v.Comments, v.CommentsTruncated)
}

func commentNoun(n int) string {
	if n == 1 {
		return "comment"
	}
	return "comments"
}

func (r *runtime) paragraph(w io.Writer, text string) {
	fmt.Fprintln(w, ansi.Wordwrap(text, r.width(), ""))
}

func boardRow(card CardDTO) []string {
	return []string{fmt.Sprintf("#%d", card.Number), markdown.SanitizeLine(card.Title), joined(card.Labels), joined(card.Assignees)}
}
