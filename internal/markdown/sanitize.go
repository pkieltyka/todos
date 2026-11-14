// Package markdown sanitises untrusted text from GitHub and renders Markdown
// for a terminal. It is the only way GitHub text reaches a terminal, in both
// the TUI and the CLI (architecture 1.5). It imports nothing of ours.
package markdown

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// SanitizeBody makes Markdown source from GitHub safe to parse. The rules are
// applied in the order of markdown.md section 3. Render calls it; callers need
// it directly only to print a raw body.
func SanitizeBody(s string) string {
	// 1. Invalid UTF-8.
	s = strings.ToValidUTF8(s, "�")
	// 2. Line endings: a bare CR moves the terminal cursor.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	// 3. Well-formed escape sequences.
	s = ansi.Strip(s)
	// 4. Remaining control characters (keeps \t and \n), and
	// 6. bidi controls.
	s = strings.Map(func(r rune) rune {
		if isControl(r) || isBidi(r) {
			return -1
		}
		return r
	}, s)
	// 5. Numeric character references that decode to control characters.
	// goldmark and glamour's HTML path decode these after we run, so they
	// must be neutralised here.
	s = numericRef.ReplaceAllStringFunc(s, neutraliseRef)
	return s
}

// SanitizeLine makes a one-line string from GitHub safe and flat: titles,
// label names, logins, branch names, project names, messages from GitHub.
func SanitizeLine(s string) string {
	s = SanitizeBody(s)
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// isControl reports the runes rule 4 deletes: C0 except tab and newline, DEL
// and C1.
func isControl(r rune) bool {
	switch {
	case r == '\t' || r == '\n':
		return false
	case r < 0x20:
		return true
	case r >= 0x7f && r <= 0x9f:
		return true
	}
	return false
}

// isBidi reports the bidirectional embedding, override and isolate controls.
func isBidi(r rune) bool {
	return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

// numericRef matches a numeric character reference. The trailing semicolon
// is optional because Go's html.UnescapeString, which glamour applies to
// text, decodes "&#27" and "&#x1b" without one. The digit runs are greedy,
// as the decoder's are.
var numericRef = regexp.MustCompile(`&#(?:[xX]([0-9a-fA-F]+)|([0-9]+));?`)

func neutraliseRef(ref string) string {
	m := numericRef.FindStringSubmatch(ref)
	var (
		v   uint64
		err error
	)
	if m[1] != "" {
		v, err = strconv.ParseUint(m[1], 16, 32)
	} else {
		v, err = strconv.ParseUint(m[2], 10, 32)
	}
	// An overflow is above U+10FFFF as well.
	if err != nil || v > unicode.MaxRune {
		return "�"
	}
	r := rune(v)
	// Bidi controls are neutralised too: rule 6 removes them as runes, and
	// a reference would bring them back after rendering.
	if isControl(r) || r == 0 || isBidi(r) {
		return "�"
	}
	return ref
}
