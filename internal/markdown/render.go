package markdown

import (
	"container/list"
	"hash/fnv"
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Style selects the glamour style. The caller chooses it; v2 has no auto style.
type Style int

const (
	Dark  Style = iota // glamour DarkStyleConfig
	Light              // glamour LightStyleConfig
	Plain              // glamour NoTTYStyleConfig: no colour, ASCII decorations
)

// Options configures a Renderer.
type Options struct {
	BaseURL   string // "https://HOST/OWNER/REPO", for relative links
	CacheSize int    // entries; 0 means 128
}

// Key identifies one render: the raw source, the width and the style.
type Key struct {
	Hash  uint64 // FNV-64a of the raw source
	Width int
	Style Style
}

const maxInstances = 4

type trKey struct {
	width int
	style Style
}

type lockedTR struct {
	mu sync.Mutex
	r  *glamour.TermRenderer
}

// Renderer renders Markdown and caches the results. It is safe for concurrent
// use. mu guards only the maps, never a glamour call, so renders of different
// widths or styles run in parallel; each glamour instance has its own mutex
// because glamour's thread safety is not documented.
type Renderer struct {
	opts Options
	// render is the glamour step. Tests replace it to push forbidden output
	// through the production fail-closed path.
	render func(src string, width int, s Style) (string, error)

	mu    sync.Mutex
	cache map[Key]*list.Element
	order *list.List // front = most recent; values are *entry
	tr    map[trKey]*lockedTR
	trUse []trKey // least recently used first
}

type entry struct {
	key Key
	out string
}

// NewRenderer returns a Renderer.
func NewRenderer(o Options) *Renderer {
	if o.CacheSize <= 0 {
		o.CacheSize = 128
	}
	r := &Renderer{
		opts:  o,
		cache: make(map[Key]*list.Element),
		order: list.New(),
		tr:    make(map[trKey]*lockedTR),
	}
	r.render = r.glamour
	return r
}

// Key is cheap and never blocks.
func (r *Renderer) Key(src string, width int, s Style) Key {
	h := fnv.New64a()
	_, _ = h.Write([]byte(src))
	return Key{Hash: h.Sum64(), Width: width, Style: s}
}

// Cached is a non-blocking lookup, safe to call from Bubble Tea's Update.
func (r *Renderer) Cached(k Key) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	el, ok := r.cache[k]
	if !ok {
		return "", false
	}
	r.order.MoveToFront(el)
	return el.Value.(*entry).out, true
}

// Render sanitises src, rewrites <img> tags, renders with glamour at exactly
// width cells, checks the output, caches and returns it. It blocks; the TUI
// calls it inside a tea.Cmd.
func (r *Renderer) Render(src string, width int, s Style) (string, error) {
	if width < 1 {
		width = 1
	}
	k := r.Key(src, width, s)
	if out, ok := r.Cached(k); ok {
		return out, nil
	}
	clean := SanitizeBody(src)
	md := rewriteImages(clean)
	out, err := r.render(md, width, s)
	if err != nil {
		return "", err
	}
	if quoteDepth(md) > 0 {
		out = repairQuotes(out, width)
	}
	if !guard(out) {
		out = xansi.Wordwrap(clean, width, "")
	}
	if s == Plain {
		// Plain means no escape sequences at all (NO_COLOR, --no-color):
		// glamour's NoTTY style still emits OSC 8 hyperlinks, and both of its
		// hyperlink modes do. Stripping keeps link text and URL as plain text.
		out = xansi.Strip(out)
	}
	out = trimBlankLines(out)
	r.store(k, out)
	return out, nil
}

func (r *Renderer) glamour(src string, width int, s Style) (string, error) {
	inst, err := r.instance(width, s)
	if err != nil {
		return "", err
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	return inst.r.Render(src)
}

func (r *Renderer) instance(width int, s Style) (*lockedTR, error) {
	k := trKey{width, s}
	r.mu.Lock()
	defer r.mu.Unlock()
	if inst, ok := r.tr[k]; ok {
		r.touch(k)
		return inst, nil
	}
	tr, err := newGlamour(width, s, r.opts.BaseURL)
	if err != nil {
		return nil, err
	}
	inst := &lockedTR{r: tr}
	r.tr[k] = inst
	r.trUse = append(r.trUse, k)
	if len(r.trUse) > maxInstances {
		// Evicting only drops our reference; a render holding the old
		// instance finishes normally.
		delete(r.tr, r.trUse[0])
		r.trUse = r.trUse[1:]
	}
	return inst, nil
}

func (r *Renderer) touch(k trKey) {
	for i, v := range r.trUse {
		if v == k {
			r.trUse = append(append(r.trUse[:i:i], r.trUse[i+1:]...), k)
			return
		}
	}
}

func (r *Renderer) store(k Key, out string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if el, ok := r.cache[k]; ok {
		el.Value.(*entry).out = out
		r.order.MoveToFront(el)
		return
	}
	r.cache[k] = r.order.PushFront(&entry{key: k, out: out})
	for r.order.Len() > r.opts.CacheSize {
		el := r.order.Back()
		r.order.Remove(el)
		delete(r.cache, el.Value.(*entry).key)
	}
}

func newGlamour(width int, s Style, baseURL string) (*glamour.TermRenderer, error) {
	var cfg ansi.StyleConfig
	switch s {
	case Light:
		cfg = styles.LightStyleConfig
	case Plain:
		cfg = styles.NoTTYStyleConfig
	default:
		cfg = styles.DarkStyleConfig
	}
	zero := uint(0)
	cfg.Document.Margin = &zero // output width == wrap width; callers place the text
	opts := []glamour.TermRendererOption{
		glamour.WithStyles(cfg),
		glamour.WithWordWrap(width),
		glamour.WithEmoji(),
		glamour.WithInlineTableLinks(true),
	}
	if baseURL != "" {
		// A trailing slash makes relative links resolve inside the
		// repository instead of replacing its last path segment.
		if !strings.HasSuffix(baseURL, "/") {
			baseURL += "/"
		}
		opts = append(opts, glamour.WithBaseURL(baseURL))
	}
	return glamour.NewTermRenderer(opts...)
}

// trimBlankLines removes leading and trailing lines that are blank once
// styling is stripped. glamour surrounds documents with empty lines.
func trimBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	blank := func(l string) bool { return strings.TrimSpace(xansi.Strip(l)) == "" }
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// quoteDepth is the deepest blockquote nesting in Markdown source, parsed the
// way glamour parses it.
func quoteDepth(src string) int {
	if !strings.Contains(src, ">") {
		return 0
	}
	doc := htmlParser.Parse(text.NewReader([]byte(src)))
	deepest := 0
	var walk func(n ast.Node, d int)
	walk = func(n ast.Node, d int) {
		if n.Kind() == ast.KindBlockquote {
			d++
			deepest = max(deepest, d)
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c, d)
		}
	}
	walk(doc, 0)
	return deepest
}

// repairQuotes fixes a glamour wrapping bug found in dogfooding: inside a
// blockquote, glamour sometimes hard-wraps the last word of a line onto a
// line of its own without the "│ " prefix. Narrowing the wrap width does not
// avoid the bug; repairing the result does. Giving the orphan its prefix back
// still left a short middle line ("│ a" between two full lines, Phase 9), so
// the quoted paragraph around an orphan is reflowed: its lines' text is joined
// and wrapped again at the width the prefix leaves, each line getting the
// paragraph's prefix (with its styling, cut from the first line) and padding.
func repairQuotes(out string, width int) string {
	lines := strings.Split(out, "\n")
	for i := 1; i < len(lines); i++ {
		prev := xansi.Strip(lines[i-1])
		cur := xansi.Strip(lines[i])
		pw, cw := quotePrefixWidth(prev), quotePrefixWidth(cur)
		// An orphan has text and fewer bars than the text line above it
		// (none, or only the outer ones in a nested quote). glamour separates
		// a nested quote from the outer text with a bar-only line, so real
		// content never matches.
		if pw == 0 || cw >= pw || !hasText(prev) || !hasText(cur) {
			continue
		}
		if textEnd(cur)-cw > width-pw {
			continue // not a short orphan; leave glamour's line alone
		}
		// The paragraph: text lines with the same bars around the orphan,
		// stopping at a list item, which starts a block of its own.
		s := i - 1
		for s > 0 && sameQuoteLine(lines[s-1], pw) && !listItem(xansi.Strip(lines[s]), pw) {
			s--
		}
		e := i + 1
		for e < len(lines) && sameQuoteLine(lines[e], pw) && !listItem(xansi.Strip(lines[e]), pw) {
			e++
		}
		var parts []string
		for k := s; k < e; k++ {
			plain := xansi.Strip(lines[k])
			from := pw
			if k == i {
				from = cw
			}
			parts = append(parts, strings.TrimSpace(xansi.Cut(lines[k], from, textEnd(plain))))
		}
		prefix := xansi.Cut(lines[s], 0, pw)
		var flowed []string
		// Wrapped one cell short of the edge: glamour's own right margin.
		for _, l := range strings.Split(xansi.Wordwrap(strings.Join(parts, " "), max(1, width-pw-1), ""), "\n") {
			fixed := prefix + strings.TrimRight(l, " ")
			if pad := width - xansi.StringWidth(fixed); pad > 0 {
				fixed += strings.Repeat(" ", pad)
			}
			flowed = append(flowed, fixed)
		}
		lines = append(lines[:s], append(flowed, lines[e:]...)...)
		i = s + len(flowed) - 1
	}
	return strings.Join(lines, "\n")
}

// textEnd is where a plain line's text ends and its trailing padding starts;
// the padding itself may be styled, so it is measured on the plain line.
func textEnd(plain string) int { return xansi.StringWidth(strings.TrimRight(plain, " ")) }

// sameQuoteLine reports a line with text and exactly pw cells of quote bars.
func sameQuoteLine(line string, pw int) bool {
	plain := xansi.Strip(line)
	return quotePrefixWidth(plain) == pw && hasText(plain)
}

// listItem reports a quoted line that starts a list item ("• ", "- ", "1. ").
func listItem(plain string, pw int) bool {
	rest := strings.TrimLeft(plain[min(len(plain), byteOffset(plain, pw)):], " ")
	if strings.HasPrefix(rest, "• ") || strings.HasPrefix(rest, "- ") || strings.HasPrefix(rest, "* ") {
		return true
	}
	d := 0
	for d < len(rest) && rest[d] >= '0' && rest[d] <= '9' {
		d++
	}
	return d > 0 && strings.HasPrefix(rest[d:], ". ")
}

// hasText reports a line with something besides quote bars and spaces.
func hasText(plain string) bool {
	return strings.TrimSpace(strings.NewReplacer("│", "", "|", "").Replace(plain)) != ""
}

// quotePrefixWidth is the width of a run of quote bars ("│ " or "| ") at the
// start of a plain line, 0 when there is none.
func quotePrefixWidth(plain string) int {
	w := 0
	for {
		rest := plain[min(len(plain), byteOffset(plain, w)):]
		switch {
		case strings.HasPrefix(rest, "│ "), strings.HasPrefix(rest, "| "):
			w += 2
		default:
			return w
		}
	}
}

// byteOffset is the byte index of cell w in a plain string of narrow runes.
func byteOffset(s string, w int) int {
	cells := 0
	for i := range s {
		if cells == w {
			return i
		}
		cells++
	}
	return len(s)
}
