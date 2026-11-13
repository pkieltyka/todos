package tui

import (
	"testing"
)

func TestComputeFrame_80x24_body21(t *testing.T) {
	f, ok := ComputeFrame(80, 24)
	if !ok || f.Body.H != 21 || f.Body.Y != 2 || f.Footer.Y != 23 {
		t.Fatalf("frame %+v ok=%v", f, ok)
	}
}

func TestComputeFrame_140x40_body37(t *testing.T) {
	f, ok := ComputeFrame(140, 40)
	if !ok || f.Body.H != 37 || f.Body.W != 140 {
		t.Fatalf("frame %+v ok=%v", f, ok)
	}
}

func TestComputeFrame_tooSmall_79x24_80x23(t *testing.T) {
	if _, ok := ComputeFrame(79, 24); ok {
		t.Fatal("79×24 fits")
	}
	if _, ok := ComputeFrame(80, 23); ok {
		t.Fatal("80×23 fits")
	}
}

func placed(ps []Placed, id ColID) (Placed, bool) {
	for _, p := range ps {
		if p.ID == id {
			return p, true
		}
	}
	return Placed{}, false
}

func widthOf(ps []Placed) int {
	last := ps[len(ps)-1]
	return last.X + last.W
}

func TestFit_dropsLabelsFirstAt80(t *testing.T) {
	// At 80 every column fits; at 60 the minimums do not, and labels drop first.
	if _, ok := placed(issueColumns(80), ColLabels); !ok {
		t.Fatal("labels dropped at 80")
	}
	ps := issueColumns(60)
	if _, ok := placed(ps, ColLabels); ok {
		t.Fatal("labels kept")
	}
	if _, ok := placed(ps, ColAssignees); !ok {
		t.Fatal("assignees dropped before labels")
	}
	if widthOf(issueColumns(80)) != 80 || widthOf(ps) != 60 {
		t.Fatal("columns do not fill the width")
	}
}

func TestFit_titleNeverBelow24(t *testing.T) {
	for w := 40; w <= 200; w += 7 {
		p, ok := placed(issueColumns(w), ColTitle)
		if !ok || p.W < 24 {
			t.Fatalf("width %d: title %+v", w, p)
		}
	}
}

func TestFit_statusColumnOnlyFrom120(t *testing.T) {
	if _, ok := placed(issueColumns(119), ColStatus); ok {
		t.Fatal("status at 119")
	}
	if _, ok := placed(issueColumns(120), ColStatus); !ok {
		t.Fatal("no status at 120")
	}
}

func TestFit_prAuthorOnlyFrom100(t *testing.T) {
	if _, ok := placed(prColumns(99), ColAuthor); ok {
		t.Fatal("author at 99")
	}
	if _, ok := placed(prColumns(100), ColAuthor); !ok {
		t.Fatal("no author at 100")
	}
}

func TestBoardWindow_80_threeColumnsOf24(t *testing.T) {
	w, first, count := BoardWindow(80, 4, 0, 0)
	if w != 24 || first != 0 || count != 3 {
		t.Fatalf("w=%d first=%d count=%d", w, first, count)
	}
}

func TestBoardWindow_140_fiveColumnsOf26(t *testing.T) {
	w, _, count := BoardWindow(140, 5, 0, 0)
	if w != 26 || count != 5 {
		t.Fatalf("w=%d count=%d", w, count)
	}
}

func TestBoardWindow_followsFocusMinimally(t *testing.T) {
	_, first, _ := BoardWindow(80, 6, 3, 0) // focus right of the window
	if first != 1 {
		t.Fatalf("first=%d, want 1", first)
	}
	_, first, _ = BoardWindow(80, 6, 2, 1) // focus already visible: no move
	if first != 1 {
		t.Fatalf("first=%d, want 1", first)
	}
	_, first, _ = BoardWindow(80, 6, 0, 3) // focus left of the window
	if first != 0 {
		t.Fatalf("first=%d, want 0", first)
	}
}

func TestReselect_keepsNumber(t *testing.T) {
	sel, off := reselect([]int{9, 7, 5, 3}, 5, 0, 1, 2)
	if sel != 5 || off != 1 {
		t.Fatalf("sel=%d off=%d", sel, off)
	}
}

func TestReselect_goneClampsToIndex(t *testing.T) {
	if sel, _ := reselect([]int{9, 7, 3}, 5, 2, 0, 10); sel != 3 {
		t.Fatalf("sel=%d, want row 2 (#3)", sel)
	}
	if sel, _ := reselect([]int{9}, 5, 4, 0, 10); sel != 9 {
		t.Fatalf("sel=%d, want the last row", sel)
	}
	if sel, off := reselect(nil, 5, 2, 3, 10); sel != 0 || off != 0 {
		t.Fatal("empty rows keep a selection")
	}
}

func TestReselect_keepsOffset(t *testing.T) {
	rows := []int{10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
	if _, off := reselect(rows, 6, 0, 3, 4); off != 3 {
		t.Fatalf("offset moved to %d", off)
	}
	if _, off := reselect(rows, 1, 0, 3, 4); off != 6 { // selection below: scroll minimally
		t.Fatalf("offset %d, want 6", off)
	}
}
