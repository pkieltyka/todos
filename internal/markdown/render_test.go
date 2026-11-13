package markdown

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
)

const base = "https://github.com/pkieltyka/todos"

func render(t *testing.T, src string, width int, s Style) string {
	t.Helper()
	out, err := NewRenderer(Options{BaseURL: base}).Render(src, width, s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !guard(out) {
		t.Fatalf("output fails the guard: %q", out)
	}
	return out
}

func plain(t *testing.T, src string) string {
	t.Helper()
	return xansi.Strip(render(t, src, 60, Plain))
}

// ---- images ----

func TestRewriteImages_pastedScreenshotBlock(t *testing.T) {
	src := `<img width="512" alt="image" src="https://github.com/user-attachments/assets/1.png">` + "\n"
	got := rewriteImages(src)
	if !strings.Contains(got, "![image](<https://github.com/user-attachments/assets/1.png>)") || strings.Contains(got, "<img") {
		t.Fatalf("got %q", got)
	}
	if out := plain(t, src); !strings.Contains(squash(out), "Image: image → https://github.com/user-attachments/assets/1.png") {
		t.Fatalf("rendered %q", out)
	}
}

func TestRewriteImages_inlineRawHTML(t *testing.T) {
	out := plain(t, `see <img src="https://x.test/a.png" alt="pic"> here`)
	if !strings.Contains(out, "see Image: pic → https://x.test/a.png here") {
		t.Fatalf("rendered %q", out)
	}
}

func TestRewriteImages_untouchedInFencedCode(t *testing.T) {
	src := "```\n<img src=\"code.png\">\n```\n"
	if got := rewriteImages(src); got != src {
		t.Fatalf("fenced code changed: %q", got)
	}
}

func TestRewriteImages_untouchedInCodeSpan(t *testing.T) {
	src := "use `<img src=\"x.png\">` in HTML\n"
	if got := rewriteImages(src); got != src {
		t.Fatalf("code span changed: %q", got)
	}
}

func TestRewriteImages_untouchedInIndentedCode(t *testing.T) {
	src := "para\n\n    <img src=\"x.png\">\n"
	if got := rewriteImages(src); got != src {
		t.Fatalf("indented code changed: %q", got)
	}
}

func TestRewriteImages_altDefaultsToImage(t *testing.T) {
	if got := rewriteImages(`<img src="a.png">` + "\n"); !strings.Contains(got, "![image](<a.png>)") {
		t.Fatalf("got %q", got)
	}
}

func TestRewriteImages_noSrcRemoved(t *testing.T) {
	got := rewriteImages(`a <img alt="x"> b` + "\n")
	if strings.Contains(got, "img") || strings.Contains(got, "![") {
		t.Fatalf("got %q", got)
	}
}

// Finding 12: the decoded src goes into a link destination, not autolinked
// text, so "&" and parentheses reach the link target unchanged.
func TestRewriteImages_queryStringAndParensKept(t *testing.T) {
	for _, s := range []Style{Dark, Plain} {
		out := render(t, `<img src="https://safe.test/a?x=1&amp;y=2" alt="pic">`, 100, s)
		if !strings.Contains(out, "https://safe.test/a?x=1&y=2") || strings.Contains(out, "&#38;") || strings.Contains(out, "%26") {
			t.Fatalf("style %d: query string changed: %q", s, out)
		}
		out = render(t, `<img src="https://x.test/a_(b).png" alt="pic">`, 100, s)
		if !strings.Contains(out, "https://x.test/a_(b).png") || strings.Contains(out, "&#40;") {
			t.Fatalf("style %d: parentheses changed: %q", s, out)
		}
	}
	// The OSC 8 target carries the same URL.
	if out := render(t, `<img src="https://safe.test/a?x=1&amp;y=2" alt="pic">`, 100, Dark); !strings.Contains(out, ";https://safe.test/a?x=1&y=2\a") {
		t.Fatalf("link target changed: %q", out)
	}
}

// Finding 13: a tag whose attributes span lines is rewritten whole.
func TestRewriteImages_multilineTag(t *testing.T) {
	for _, src := range []string{
		"<img\n src=\"https://x.test/p.png\"\n alt=\"pic\">\n",
		"before <img\nsrc=\"https://x.test/p.png\"\nalt=\"pic\"> after\n",
		"<p align=\"center\">\n<img\n  src=\"https://x.test/p.png\"\n  alt=\"pic\">\n</p>\n",
	} {
		if out := plain(t, src); !strings.Contains(squash(out), "Image: pic → https://x.test/p.png") {
			t.Fatalf("%q rendered %q", src, out)
		}
	}
}

func TestRewriteImages_attributeEntitiesSanitised(t *testing.T) {
	src := `<img alt="&#27;[31mred" src="a.png">` + "\n"
	out := render(t, src, 60, Dark)
	if strings.Contains(xansi.Strip(out), "[31m") && strings.Contains(out, "\x1b[31m") {
		t.Fatalf("escape sequence leaked: %q", out)
	}
	if strings.Contains(rewriteImages(SanitizeBody(src)), "\x1b") {
		t.Fatal("ESC in rewritten source")
	}
}

func TestRewriteImages_escapesAngleBrackets(t *testing.T) {
	if out := plain(t, `<img alt="a &lt;b&gt; *c* [d]" src="https://x.test/x.png">`); !strings.Contains(out, "Image: a <b> *c* [d] → https://x.test/x.png") {
		t.Fatalf("rendered %q", out)
	}
}

// ---- rendering ----

func TestRender_htmlCommentDropped(t *testing.T) {
	out := plain(t, "<!-- template hint -->\nvisible\n")
	if strings.Contains(out, "template") || !strings.Contains(out, "visible") {
		t.Fatalf("got %q", out)
	}
}

func TestRender_detailsKeepsText(t *testing.T) {
	out := plain(t, "<details><summary>Logs</summary>\n\nbody\n</details>\n")
	if !strings.Contains(out, "Logs") || !strings.Contains(out, "body") || strings.Contains(out, "<") {
		t.Fatalf("got %q", out)
	}
}

func TestRender_markdownImageShowsLink(t *testing.T) {
	if out := plain(t, "![diagram](https://x.test/d.png)"); !strings.Contains(out, "Image: diagram → https://x.test/d.png") {
		t.Fatalf("got %q", out)
	}
}

func TestRender_htmlImageShowsLink(t *testing.T) {
	md := plain(t, "![pic](https://x.test/p.png)")
	html := plain(t, `<img alt="pic" src="https://x.test/p.png">`)
	if strings.TrimSpace(md) != strings.TrimSpace(html) {
		t.Fatalf("markdown %q vs html %q", md, html)
	}
}

func TestRender_widthEqualsWrap(t *testing.T) {
	src := strings.Repeat("word ", 80) + "\n\n- item " + strings.Repeat("long ", 30) + "\n\n```\n" + strings.Repeat("x", 30) + "\n```\n"
	for _, w := range []int{40, 80, 100} {
		for _, s := range []Style{Dark, Light, Plain} {
			for i, line := range strings.Split(render(t, src, w, s), "\n") {
				if got := xansi.StringWidth(line); got > w {
					t.Fatalf("width %d style %d line %d is %d wide: %q", w, s, i, got, xansi.Strip(line))
				}
			}
		}
	}
}

func TestRender_plainHasNoSGR(t *testing.T) {
	out := render(t, "# Title\n\n**bold** and `code`\n\n```go\nx := 1\n```\n", 60, Plain)
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("plain output has SGR: %q", out)
	}
}

func TestGuard_rejectsForbiddenAcceptsSGRandOSC8(t *testing.T) {
	for _, bad := range []string{"ok \x1b[2J", "x\x1b]52;c;aGk=\x07", "a\x07b", "a\u009bb", "a\u202eb", "a\u2066b"} {
		if guard(bad) {
			t.Errorf("guard accepted %q", bad)
		}
	}
	if !guard("a \x1b[1;38;5;2mb\x1b[m \x1b]8;id=1;https://x\x07l\x1b]8;;\x07 \x1b]8;;u\x1b\\t\x1b]8;;\x1b\\") {
		t.Fatal("guard rejected SGR or OSC 8")
	}
}

// Finding 15: forbidden output pushed through the production path comes back
// as safe plain text. Removing the guard branch in Render fails this test.
func TestRender_guardFallbackIsPlainWrapped(t *testing.T) {
	for _, injected := range []string{"x\x1b[2Jy", "a\u202eb", "osc \x1b]52;c;aGk=\x07"} {
		r := NewRenderer(Options{})
		r.render = func(string, int, Style) (string, error) { return injected, nil }
		src := "hello " + strings.Repeat("word ", 20)
		out, err := r.Render(src, 20, Dark)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "\x1b") || strings.ContainsRune(out, 0x202e) || strings.ContainsRune(out, 0x07) {
			t.Fatalf("forbidden output returned for %q: %q", injected, out)
		}
		if out != trimBlankLines(xansi.Wordwrap(SanitizeBody(src), 20, "")) {
			t.Fatalf("not the plain fallback: %q", out)
		}
	}
}

// Finding 1: bidi controls written as references cannot survive rendering.
func TestRender_bidiReferencesNeutralised(t *testing.T) {
	for _, s := range []Style{Dark, Light, Plain} {
		out := render(t, "&#8238;abc&#8236; and &#x2066;x&#x2069;", 60, s)
		for _, r := range out {
			if isBidi(r) {
				t.Fatalf("style %d: bidi control %U in %q", s, r, out)
			}
		}
	}
}

// Plain is for NO_COLOR: no escape sequences at all, links kept as text.
func TestRender_plainHasNoEscapes(t *testing.T) {
	out := render(t, "a [link](https://example.com) and **bold**", 80, Plain)
	if strings.Contains(out, "\x1b") {
		t.Fatalf("escape sequence in plain output: %q", out)
	}
	if !strings.Contains(out, "link") || !strings.Contains(out, "https://example.com") {
		t.Fatalf("link text or URL lost: %q", out)
	}
}

func TestRender_codeBlockHighlightedDarkAndLight(t *testing.T) {
	src := "```go\nfunc main() {}\n```\n"
	dark, light := render(t, src, 60, Dark), render(t, src, 60, Light)
	if !strings.Contains(dark, "\x1b[") || !strings.Contains(light, "\x1b[") {
		t.Fatal("code block not styled")
	}
	if dark == light {
		t.Fatal("dark and light renders are identical")
	}
	if !strings.Contains(xansi.Strip(dark), "func main() {}") {
		t.Fatalf("code text missing: %q", xansi.Strip(dark))
	}
}

func TestRender_relativeLinkUsesBaseURL(t *testing.T) {
	if out := plain(t, "[guide](docs/guide.md)"); !strings.Contains(squash(out), base+"/docs/guide.md") {
		t.Fatalf("got %q", out)
	}
}

func TestRender_emojiShortcode(t *testing.T) {
	if out := plain(t, "ship it :tada:"); !strings.Contains(out, "🎉") {
		t.Fatalf("got %q", out)
	}
}

func TestRender_trimsSurroundingBlankLines(t *testing.T) {
	out := xansi.Strip(render(t, "hello", 40, Dark))
	lines := strings.Split(out, "\n")
	if strings.TrimSpace(lines[0]) == "" || strings.TrimSpace(lines[len(lines)-1]) == "" {
		t.Fatalf("blank edge lines in %q", out)
	}
}

func FuzzRender_onlySGRandOSC8(f *testing.F) {
	for _, s := range []string{
		"# t\n\n&#27;[2J", "<img alt=\"&#x1b;\" src=x>", "\x1b]52;c;x\x07", "| a | b |\n|---|---|\n| &#155; | 2 |",
		"```\n\x1b[31m\n```", "[x](javascript:&#27;)", "<details>&#x9b;</details>", "*a*‮",
	} {
		f.Add(s)
	}
	r := NewRenderer(Options{BaseURL: base, CacheSize: 1})
	f.Fuzz(func(t *testing.T, in string) {
		for _, s := range []Style{Dark, Light, Plain} {
			clean := SanitizeBody(in)
			out, err := r.glamour(rewriteImages(clean), 60, s)
			if err != nil {
				return // a glamour error is reported, not drawn
			}
			if !guard(out) {
				t.Fatalf("style %d: glamour output fails the guard for %q: %q", s, in, out)
			}
		}
	})
}

// ---- cache and concurrency ----

func TestRenderer_cacheHitByKey(t *testing.T) {
	r := NewRenderer(Options{})
	k := r.Key("hello", 40, Dark)
	if _, ok := r.Cached(k); ok {
		t.Fatal("hit before render")
	}
	out, _ := r.Render("hello", 40, Dark)
	got, ok := r.Cached(k)
	if !ok || got != out {
		t.Fatal("miss after render")
	}
}

func TestRenderer_styleSwitchMisses(t *testing.T) {
	r := NewRenderer(Options{})
	_, _ = r.Render("hello", 40, Dark)
	if _, ok := r.Cached(r.Key("hello", 40, Light)); ok {
		t.Fatal("light hit after dark render")
	}
}

func TestRenderer_widthSwitchMisses(t *testing.T) {
	r := NewRenderer(Options{})
	_, _ = r.Render("hello", 40, Dark)
	if _, ok := r.Cached(r.Key("hello", 41, Dark)); ok {
		t.Fatal("hit at another width")
	}
}

func TestRenderer_lruEvicts(t *testing.T) {
	r := NewRenderer(Options{CacheSize: 2})
	_, _ = r.Render("a", 40, Plain)
	_, _ = r.Render("b", 40, Plain)
	_, _ = r.Cached(r.Key("a", 40, Plain)) // a is now most recent
	_, _ = r.Render("c", 40, Plain)
	if _, ok := r.Cached(r.Key("b", 40, Plain)); ok {
		t.Fatal("least recently used entry not evicted")
	}
	if _, ok := r.Cached(r.Key("a", 40, Plain)); !ok {
		t.Fatal("recently used entry evicted")
	}
}

func TestRenderer_concurrentRendersRace(t *testing.T) {
	r := NewRenderer(Options{BaseURL: base, CacheSize: 16})
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 8; i++ {
				src := fmt.Sprintf("# doc %d\n\ntext %d **b**", g%5, i%3)
				w := 30 + (g+i)%6*10
				s := Style((g + i) % 3)
				if _, err := r.Render(src, w, s); err != nil {
					t.Error(err)
					return
				}
				_, _ = r.Cached(r.Key(src, w, s))
			}
		}(g)
	}
	wg.Wait()
}

// squash removes the line breaks and padding glamour adds when wrapping, so a
// long URL can be matched whole.
func squash(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(s, "\n") {
		b.WriteString(strings.TrimSpace(l))
	}
	return b.String()
}
