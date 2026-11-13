// Package quickadd parses punch lists without I/O or metadata lookups.
package quickadd

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pkieltyka/todos/internal/domain"
)

func afterPrefix(s string) (string, bool) {
	if s == "" {
		return s, true
	}
	r, n := utf8.DecodeRuneInString(s)
	if unicode.IsSpace(r) {
		return s[n:], true
	}
	return "", false
}
func title(line string) (string, bool) {
	checked := false
	s := line
	if len(s) > 0 && strings.ContainsRune("-*+", rune(s[0])) {
		if rest, ok := afterPrefix(s[1:]); ok {
			rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
			if len(rest) >= 3 && (strings.HasPrefix(rest, "[ ]") || strings.HasPrefix(rest, "[x]") || strings.HasPrefix(rest, "[X]")) {
				if tail, ok := afterPrefix(rest[3:]); ok {
					return strings.TrimSpace(tail), rest[1] != ' '
				}
			}
			return strings.TrimSpace(rest), false
		}
	}
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	if n > 0 && n < len(s) && (s[n] == '.' || s[n] == ')') {
		if rest, ok := afterPrefix(s[n+1:]); ok {
			return strings.TrimSpace(rest), false
		}
	}
	n = 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	if n > 0 && n <= 6 {
		if rest, ok := afterPrefix(s[n:]); ok {
			return strings.TrimSpace(rest), false
		}
	}
	return strings.TrimSpace(s), checked
}

func Parse(text string) domain.QuickAddPlan {
	plan := domain.QuickAddPlan{Source: text, Items: []domain.QuickAddItem{}, Problems: []domain.Problem{}}
	problem := func(line int, msg string) {
		plan.Problems = append(plan.Problems, domain.Problem{Line: line, Message: msg})
	}
	if !utf8.ValidString(text) {
		problem(1, "input must be valid UTF-8")
		return plan
	}
	offset, lineNo := 0, 0
	body := []string{}
	pending := []string{}
	finish := func(end int) {
		if len(plan.Items) == 0 {
			return
		}
		i := &plan.Items[len(plan.Items)-1]
		i.EndByte = end
		i.Source = text[i.StartByte:end]
		i.Body = strings.Join(body, "\n")
		body = nil
		pending = nil
	}
	for _, raw := range strings.SplitAfter(text, "\n") {
		if raw == "" {
			continue
		}
		lineNo++
		line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		start := offset
		offset += len(raw)
		if strings.TrimSpace(line) == "" {
			if len(plan.Items) > 0 {
				blank := line
				if strings.HasPrefix(blank, "\t") {
					blank = blank[1:]
				} else if strings.HasPrefix(blank, "  ") {
					blank = blank[2:]
				}
				pending = append(pending, blank)
			}
			continue
		}
		if strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") {
			if len(plan.Items) == 0 {
				problem(lineNo, "indented body has no preceding item")
				continue
			}
			if len(body) > 0 {
				body = append(body, pending...)
			}
			pending = nil
			if line[0] == '\t' {
				line = line[1:]
			} else {
				line = line[2:]
			}
			body = append(body, line)
			continue
		}
		if strings.HasPrefix(line, " ") {
			problem(lineNo, "indentation must be at least two spaces or one tab")
			continue
		}
		finish(start)
		if len(plan.Items) == 0 {
			start = 0
		}
		t, checked := title(line)
		item := domain.QuickAddItem{Line: lineNo, Title: t, Checked: checked, StartByte: start}
		plan.Items = append(plan.Items, item)
		if t == "" {
			problem(lineNo, "title must not be empty")
		} else if utf8.RuneCountInString(t) > 256 {
			problem(lineNo, "title must contain at most 256 characters")
		}
	}
	finish(len(text))
	if len(plan.Items) == 0 {
		problem(1, "punch list must contain at least one item")
	}
	if len(plan.Items) > 50 {
		problem(plan.Items[50].Line, "punch list may contain at most 50 items (including checked items)")
	}
	return plan
}
