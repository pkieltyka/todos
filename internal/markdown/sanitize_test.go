package markdown

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeBody_table(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"csi colour", "a\x1b[31mred\x1b[0m b", "ared b"},
		{"cursor move", "a\x1b[2Ab", "ab"},
		{"clear screen", "x\x1b[2Jy", "xy"},
		{"osc 52 clipboard", "a\x1b]52;c;aGVsbG8=\x07b", "ab"},
		{"osc 8 link", "\x1b]8;;https://evil\x1b\\click\x1b]8;;\x1b\\", "click"},
		{"dcs", "a\x1bPq#0;2;0;0;0\x1b\\b", "ab"},
		{"apc", "a\x1b_payload\x1b\\b", "ab"},
		{"lone esc", "a\x1bb", "a"}, // ESC b is a two-byte escape sequence
		{"lone esc at end", "ab\x1b", "ab"},
		{"bel", "a\x07b", "ab"},
		{"c1 csi rune", "a\u009b31mb", "a31mb"},
		{"del", "a\x7fb", "ab"},
		{"entity esc decimal", "a&#27;[2Jb", "a�[2Jb"},
		{"entity esc hex", "a&#x1b;b", "a�b"},
		{"entity esc hex upper", "a&#X1B;b", "a�b"},
		{"entity c1 decimal", "a&#155;b", "a�b"},
		{"entity c1 hex", "a&#x9B;b", "a�b"},
		{"entity nul", "a&#0;b", "a�b"},
		{"entity above max rune", "a&#1114112;b", "a�b"},
		{"entity huge", "a&#99999999999999999999;b", "a�b"},
		{"entity esc no semicolon", "a&#27[2Jb", "a\uFFFD[2Jb"},
		{"entity hex no semicolon", "000000000&#X1", "000000000\uFFFD"},
		{"entity hex greedy digits", "a&#x1bc;b", "a&#x1bc;b"},
		{"entity bidi override", "&#8238;abc&#8236;", "\uFFFDabc\uFFFD"},
		{"entity bidi isolate hex", "&#x2066;x&#X2069", "\uFFFDx\uFFFD"},
		{"entity newline kept", "a&#10;b", "a&#10;b"},
		{"entity tab kept", "a&#9;b", "a&#9;b"},
		{"entity printable kept", "a&#65;&#x263A;b", "a&#65;&#x263A;b"},
		{"named entity kept", "a&amp;b", "a&amp;b"},
		{"bidi", "a‮b⁦c⁩", "abc"},
		{"crlf", "a\r\nb", "a\nb"},
		{"lone cr", "a\rb", "a\nb"},
		{"invalid utf8", "a\xffb", "a�b"},
		{"tabs and newlines kept", "a\tb\nc", "a\tb\nc"},
		{"emoji and cjk kept", "ok 🎉 漢字", "ok 🎉 漢字"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SanitizeBody(c.in); got != c.want {
				t.Fatalf("SanitizeBody(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSanitizeLine_flattensAndTrims(t *testing.T) {
	cases := map[string]string{
		"  Fix\tthe\n\nbug  ": "Fix the bug",
		"a\x1b[31mb\r\nc":     "ab c",
		"":                    "",
		"\n\t ":               "",
		"one":                 "one",
		"x‮ y":                "x y",
		"title &#27;[2J here": "title �[2J here",
		"double  space nb":    "double space nb",
	}
	for in, want := range cases {
		if got := SanitizeLine(in); got != want {
			t.Errorf("SanitizeLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeBody_idempotent(t *testing.T) {
	inputs := []string{
		"plain", "a\x1b[31mb", "&#27;x", "a\r\n\x07b‮", "\xff\xfe", "# Title\n\n- [ ] item\n",
	}
	for _, in := range inputs {
		once := SanitizeBody(in)
		if twice := SanitizeBody(once); twice != once {
			t.Errorf("not idempotent for %q: %q then %q", in, once, twice)
		}
	}
}

func FuzzSanitizeBody_noControls(f *testing.F) {
	for _, s := range []string{"a\x1b[31mb", "&#27;", "\u009b", "\r\n", "\xff", "&#x9b;[2J"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := SanitizeBody(in)
		if !utf8.ValidString(out) {
			t.Fatalf("invalid UTF-8 out of %q", in)
		}
		for _, r := range out {
			if isControl(r) || isBidi(r) {
				t.Fatalf("control %U left in %q (from %q)", r, out, in)
			}
		}
		if strings.Contains(out, "\x1b") {
			t.Fatalf("ESC left in %q", out)
		}
		for _, m := range numericRef.FindAllString(out, -1) {
			if neutraliseRef(m) != m {
				t.Fatalf("dangerous reference %q left in %q", m, out)
			}
		}
	})
}
