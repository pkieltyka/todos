package quickadd

import (
	"github.com/pkieltyka/todos/internal/domain"
	"reflect"
	"strings"
	"testing"
)

func TestParse_AllPrefixes(t *testing.T) {
	for _, prefix := range []string{"", "- ", "* ", "+ ", "1. ", "27) ", "# ", "###### ", "- [ ] ", "* [x] ", "+ [X] "} {
		t.Run(prefix, func(t *testing.T) {
			p := Parse(prefix + "Task")
			if len(p.Problems) > 0 || len(p.Items) != 1 || p.Items[0].Title != "Task" {
				t.Fatal(p)
			}
		})
	}
}
func TestParse_CheckboxCasesAndLiteralShortcuts(t *testing.T) {
	for _, tc := range []struct {
		input, title string
		checked      bool
	}{{"- [x] done", "done", true}, {"+ [X] done", "done", true}, {"* [ ] todo", "todo", false}, {"@me #123 bug", "@me #123 bug", false}, {"#123", "#123", false}, {"- [x]suffix", "[x]suffix", false}, {"1.text", "1.text", false}, {"####### word", "####### word", false}, {"- - nested", "- nested", false}} {
		p := Parse(tc.input)
		if p.Items[0].Title != tc.title || p.Items[0].Checked != tc.checked {
			t.Fatal(tc, p)
		}
	}
}
func TestParse_IndentAndNestedBody(t *testing.T) {
	p := Parse("Task\n  first\n    - nested\n\tlast\n")
	if len(p.Problems) > 0 || p.Items[0].Body != "first\n  - nested\nlast" {
		t.Fatal(p)
	}
}
func TestParse_BlankLinesAndCRLF(t *testing.T) {
	source := "\r\n- one\r\n\r\n  body\r\n\r\n  more\r\n\r\n- two\r\n\r\n"
	p := Parse(source)
	if p.Items[0].Body != "body\n\nmore" || p.Items[1].Body != "" {
		t.Fatal(p)
	}
	if p.Items[0].Source+p.Items[1].Source != source || p.Items[1].Line != 8 {
		t.Fatal(p)
	}
}
func TestParse_OrphanAndOneSpaceIndent(t *testing.T) {
	p := Parse("  orphan\nTask\n bad\n")
	if len(p.Problems) != 2 || p.Problems[0].Line != 1 || p.Problems[1].Line != 3 {
		t.Fatal(p)
	}
}
func TestParse_Unicode256AndEmptyTitle(t *testing.T) {
	for _, tc := range []struct {
		s     string
		valid bool
	}{{strings.Repeat("界", 256), true}, {strings.Repeat("界", 257), false}, {"- [x]", false}, {"+", false}, {"1)", false}, {"##", false}, {" \n", false}, {string([]byte{255}), false}} {
		p := Parse(tc.s)
		if (len(p.Problems) == 0) != tc.valid {
			t.Fatal(tc, p)
		}
	}
}
func TestParse_51IncludingSkipped(t *testing.T) {
	p := Parse(strings.Repeat("- [x] item\n", 51))
	if len(p.Items) != 51 || len(p.Problems) != 1 || p.Problems[0].Line != 51 {
		t.Fatal(p)
	}
}
func TestParse_LongBodyLine(t *testing.T) {
	body := strings.Repeat("x", 200000)
	p := Parse("title\n  " + body)
	if len(p.Problems) > 0 || p.Items[0].Body != body {
		t.Fatal("long body lost")
	}
}
func TestParse_DuplicateTitles(t *testing.T) {
	p := Parse("same\nsame\n")
	if len(p.Items) != 2 || len(p.Problems) > 0 {
		t.Fatal(p)
	}
}
func TestQuickAdd_RemainingVerbatim(t *testing.T) {
	p := Parse("\r\n- first\r\n\r\n* second\r\n  **body**\r\n\r\nthird")
	r := domain.QuickAddResult{Items: []domain.QuickAddItemResult{{Result: "created"}, {Result: "uncertain"}, {Result: "unattempted"}}}
	text, spans := Remaining(p, r)
	if text != p.Items[1].Source+p.Items[2].Source || len(spans) != 1 || spans[0].StartByte != 0 || spans[0].EndByte != len(p.Items[1].Source) || len(spans[0].SourceSHA256) != 64 {
		t.Fatal(text, spans)
	}
}
func FuzzParse_SourceSpansDeterminism(f *testing.F) {
	for _, seed := range []string{"", "- task\r\n  body\n\n+ next", "- [x] done", "  orphan", "a\n bad\nb", string([]byte{255})} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		a, b := Parse(text), Parse(text)
		if !reflect.DeepEqual(a, b) {
			t.Fatal("nondeterministic")
		}
		last := 0
		for _, i := range a.Items {
			if i.StartByte != last || i.EndByte < i.StartByte || i.EndByte > len(text) || i.Source != text[i.StartByte:i.EndByte] {
				t.Fatal("invalid span", i)
			}
			last = i.EndByte
		}
		if len(a.Items) > 0 && last != len(text) {
			t.Fatal("lost source")
		}
		kept, _ := Remaining(a, domain.QuickAddResult{})
		if len(a.Items) > 0 && kept != text {
			t.Fatal("lost remaining bytes")
		}
	})
}

func TestParse_BodyWhitespacePreserved(t *testing.T) {
	p := Parse("task\n  first  \n    \n\t\t\n  last  \n")
	if len(p.Problems) > 0 || p.Items[0].Body != "first  \n  \n\t\nlast  " {
		t.Fatal(p)
	}
}
