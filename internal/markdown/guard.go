package markdown

import (
	"strings"
	"unicode/utf8"
)

// guard reports whether out contains only printable text, "\n", "\t", SGR
// sequences (CSI ... m) and OSC 8 hyperlinks. Anything else means the
// sanitiser has a hole, and Render falls back to plain text (markdown.md 5).
//
// It is a small dedicated scanner rather than x/ansi's general parser: the
// accepted grammar is tiny, and being strict is the point.
func guard(out string) bool {
	for i := 0; i < len(out); {
		c := out[i]
		switch {
		case c == 0x1b:
			n, ok := sequence(out[i:])
			if !ok {
				return false
			}
			i += n
		case c == '\n' || c == '\t':
			i++
		case c < 0x20 || c == 0x7f:
			return false
		case c < utf8.RuneSelf:
			i++
		default:
			r, size := utf8.DecodeRuneInString(out[i:])
			if r == utf8.RuneError && size <= 1 {
				return false
			}
			if (r >= 0x80 && r <= 0x9f) || isBidi(r) { // C1 controls, bidi controls
				return false
			}
			i += size
		}
	}
	return true
}

// sequence returns the length of the allowed escape sequence at the start of
// s, which begins with ESC.
func sequence(s string) (int, bool) {
	if len(s) < 2 {
		return 0, false
	}
	switch s[1] {
	case '[': // CSI: parameters, then a final byte; only SGR ('m') is allowed
		for j := 2; j < len(s); j++ {
			c := s[j]
			switch {
			case c >= '0' && c <= '9', c == ';', c == ':':
				continue
			case c == 'm':
				return j + 1, true
			default:
				return 0, false
			}
		}
		return 0, false
	case ']': // OSC: only "8;params;uri", terminated by BEL or ST (ESC \)
		if !strings.HasPrefix(s[2:], "8;") {
			return 0, false
		}
		for j := 2; j < len(s); j++ {
			c := s[j]
			switch {
			case c == 0x07:
				return j + 1, true
			case c == 0x1b:
				if j+1 < len(s) && s[j+1] == '\\' {
					return j + 2, true
				}
				return 0, false
			case c < 0x20 || c == 0x7f:
				return 0, false
			}
		}
		return 0, false
	}
	return 0, false
}
