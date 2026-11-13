package markdown

import (
	"bytes"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"golang.org/x/net/html"
)

// htmlParser is configured exactly as glamour configures goldmark
// (glamour.go, NewTermRenderer), so "inside raw HTML" is decided here exactly
// as glamour will decide it. Code blocks and code spans never produce HTML
// nodes, so code is never touched.
var htmlParser = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.DefinitionList),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
).Parser()

type replacement struct {
	start, stop int
	text        string
}

// rewriteImages replaces every <img> tag inside raw HTML with a Markdown
// image, because glamour's HTML sanitiser removes images entirely while the
// spec requires that images appear as their link (5.2). glamour then shows it
// exactly like an image written in Markdown: "Image: alt → url". It runs on
// sanitised source, before glamour.
//
// Each HTML node is tokenised as one range, from its first segment to its
// last, so a tag whose attributes span several lines is found whole.
func rewriteImages(src string) string {
	if !strings.Contains(strings.ToLower(src), "<img") {
		return src
	}
	source := []byte(src)
	doc := htmlParser.Parse(text.NewReader(source))

	var reps []replacement
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.HTMLBlock:
			segs := v.Lines()
			if segs.Len() == 0 {
				return ast.WalkContinue, nil
			}
			stop := segs.At(segs.Len() - 1).Stop
			if v.HasClosure() {
				stop = v.ClosureLine.Stop
			}
			// In a block the image must leave the HTML context to be read as
			// Markdown: blank lines around it end the HTML block.
			reps = append(reps, imagesIn(source, segs.At(0).Start, stop, true)...)
		case *ast.RawHTML:
			if v.Segments.Len() == 0 {
				return ast.WalkContinue, nil
			}
			reps = append(reps, imagesIn(source, v.Segments.At(0).Start, v.Segments.At(v.Segments.Len()-1).Stop, false)...)
		}
		return ast.WalkContinue, nil
	})
	if len(reps) == 0 {
		return src
	}
	// Apply from the end so earlier offsets stay valid.
	sort.Slice(reps, func(i, j int) bool { return reps[i].start > reps[j].start })
	out := source
	last := len(out) + 1
	for _, r := range reps {
		if r.stop > last { // overlapping ranges: keep the later-starting one
			continue
		}
		out = append(out[:r.start:r.start], append([]byte(r.text), out[r.stop:]...)...)
		last = r.start
	}
	return string(out)
}

// imagesIn tokenises source[start:stop] and returns a replacement for every
// img tag in it.
func imagesIn(source []byte, start, stop int, block bool) []replacement {
	z := html.NewTokenizer(bytes.NewReader(source[start:stop]))
	var reps []replacement
	off := start
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return reps
		}
		raw := len(z.Raw())
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			name, hasAttr := z.TagName()
			if string(name) == "img" {
				var src, alt string
				for hasAttr {
					var k, v []byte
					k, v, hasAttr = z.TagAttr()
					switch string(k) {
					case "src":
						src = string(v)
					case "alt":
						alt = string(v)
					}
				}
				t := imageMarkdown(alt, src)
				if block && t != "" {
					t = "\n\n" + t + "\n\n"
				}
				reps = append(reps, replacement{start: off, stop: off + raw, text: t})
			}
		}
		off += raw
	}
}

// imageMarkdown is a Markdown image for an <img> tag. The tokenizer has
// decoded entities in the attributes, so they are sanitised again; the URL
// goes into an explicit angle-bracket destination, never into text that an
// autolink parser would read, so "&" in a query string survives intact.
func imageMarkdown(alt, src string) string {
	src = SanitizeLine(src)
	if src == "" {
		return ""
	}
	alt = SanitizeLine(alt)
	if alt == "" {
		alt = "image"
	}
	return "![" + escapeLabel(alt) + "](<" + escapeDestination(src) + ">)"
}

// escapeLabel backslash-escapes every ASCII punctuation character, so the alt
// text is literal Markdown text.
func escapeLabel(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 && strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// escapeDestination makes a URL safe inside "<...>" and keeps its meaning.
// goldmark does not undo backslash escapes there, so nothing is
// backslash-escaped: angle brackets (which cannot appear) and backslashes
// (not valid in a URL) are percent-encoded, and "&" stays as it is, so a
// query string reaches the link target intact. An entity-shaped "&name;" in
// a URL would be decoded, which cannot produce a control character because
// SanitizeLine has already neutralised numeric references.
func escapeDestination(s string) string {
	return strings.NewReplacer("<", "%3C", ">", "%3E", "\\", "%5C").Replace(s)
}
