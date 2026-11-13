package tui

import "charm.land/bubbles/v2/key"

// Action is one thing a key can do. Screens receive actions, never keys
// (tui.md 9.2).
type Action int

const (
	// views and app
	ActViewIssues Action = iota
	ActViewPRs
	ActViewBoard
	ActNextView
	ActPrevView
	ActHelp
	ActQuit
	ActRefresh
	// navigation
	ActUp
	ActDown
	ActLeft
	ActRight
	ActTop
	ActBottom
	ActHalfDown
	ActHalfUp
	ActNextComment
	ActPrevComment
	ActOpen
	ActBack
	// lists
	ActFilter
	ActMine
	ActState
	ActBrowser
	ActCopyURL
	// issue actions (enabled from Phase 6)
	ActNew
	ActQuickAdd
	ActEdit
	ActAssign
	ActLabel
	ActComment
	ActClose
	ActCloseNotPlanned
	ActStatus
	ActStatusPrev
	ActStatusNext
	ActDelete
	// board
	ActChooseProject
	ActHiddenInfo
	// text mode
	ActSubmit
	ActPreview
	ActExternalEditor
	ActDiscardDraft
	ActNextField
	ActPrevField
	// pickers and quick-add preview
	ActToggle
	ActApply
	ActDoneItems
	// confirms
	ActYes
	ActOverwrite

	actCount
)

// Keymap holds one binding per action.
type Keymap struct{ b [actCount]key.Binding }

// Binding returns the binding of an action.
func (k Keymap) Binding(a Action) key.Binding { return k.b[a] }

// DefaultKeymap is the only place key strings appear: the table of UI 4.
func DefaultKeymap() Keymap {
	var k Keymap
	set := func(a Action, help, desc string, keys ...string) {
		k.b[a] = key.NewBinding(key.WithKeys(keys...), key.WithHelp(help, desc))
	}
	set(ActViewIssues, "1", "issues", "1")
	set(ActViewPRs, "2", "pull requests", "2")
	set(ActViewBoard, "3", "board", "3")
	set(ActNextView, "tab", "next view", "tab")
	set(ActPrevView, "shift+tab", "previous view", "shift+tab")
	set(ActHelp, "?", "help", "?")
	set(ActQuit, "q", "quit", "q")
	set(ActRefresh, "r", "refresh", "r")

	set(ActUp, "k", "up", "k", "up")
	set(ActDown, "j", "down", "j", "down")
	set(ActLeft, "h", "column left", "h", "left")
	set(ActRight, "l", "column right", "l", "right")
	set(ActTop, "g", "top", "g", "home")
	set(ActBottom, "G", "bottom", "G", "end")
	set(ActHalfDown, "ctrl+d", "half page down", "ctrl+d", "pgdown")
	set(ActHalfUp, "ctrl+u", "half page up", "ctrl+u", "pgup")
	set(ActNextComment, "]", "next comment", "]")
	set(ActPrevComment, "[", "previous comment", "[")
	set(ActOpen, "enter", "open", "enter")
	set(ActBack, "esc", "back", "esc")

	set(ActFilter, "/", "filter", "/")
	set(ActMine, "m", "mine", "m")
	set(ActState, "s", "state", "s")
	set(ActBrowser, "o", "browser", "o")
	set(ActCopyURL, "y", "copy URL", "y")

	set(ActNew, "n", "new issue", "n")
	set(ActQuickAdd, "A", "quick add", "A")
	set(ActEdit, "e", "edit title/body", "e")
	set(ActAssign, "a", "assignees", "a")
	set(ActLabel, "L", "labels", "L")
	set(ActComment, "c", "comment", "c")
	set(ActClose, "x", "close / reopen", "x")
	set(ActCloseNotPlanned, "X", "close as not planned", "X")
	set(ActStatus, "M", "board status", "M")
	set(ActStatusPrev, "<", "move on board", "<", "shift+left")
	set(ActStatusNext, ">", "move on board", ">", "shift+right")
	set(ActDelete, "D", "delete", "D")

	set(ActChooseProject, "P", "choose project", "P")
	set(ActHiddenInfo, "i", "hidden items", "i")

	set(ActSubmit, "ctrl+s", "save", "ctrl+s")
	set(ActPreview, "ctrl+r", "preview", "ctrl+r")
	set(ActExternalEditor, "ctrl+o", "$EDITOR", "ctrl+o")
	set(ActDiscardDraft, "ctrl+x", "discard draft", "ctrl+x")
	set(ActNextField, "tab", "next field", "tab")
	set(ActPrevField, "shift+tab", "previous field", "shift+tab")

	set(ActToggle, "space", "toggle", "space")
	set(ActApply, "enter", "apply", "enter")
	set(ActDoneItems, "d", "done items", "d")

	set(ActYes, "y", "yes", "y")
	set(ActOverwrite, "o", "overwrite", "o")
	return k
}

// helpOverrides are the per-context descriptions (tui.md 9.1).
var helpOverrides = map[ScreenID]map[Action][2]string{
	ScreenIssues:     {ActBack: {"esc", "clear filter"}},
	ScreenPRs:        {ActBack: {"esc", "clear filter"}},
	ScreenBoard:      {ActBack: {"esc", "clear filter"}, ActNew: {"n", "new here"}, ActDown: {"j/k", "card"}, ActLeft: {"h/l", "column"}, ActStatusPrev: {"</>", "move"}},
	ScreenIssue:      {ActDown: {"j/k", "scroll"}},
	ScreenPR:         {ActDown: {"j/k", "scroll"}, ActNextComment: {"]/[", "comments"}},
	ScreenBoardSetup: {ActOpen: {"enter", "use and remember"}, ActBack: {"esc", "cancel"}},
	ScreenHelp:       {ActBack: {"esc", "close"}, ActDown: {"j/k", "scroll"}},
}

// HelpText is the footer and help text of an action in a context.
func HelpText(s ScreenID, a Action, k Keymap) (string, string) {
	if o, ok := helpOverrides[s][a]; ok {
		return o[0], o[1]
	}
	h := k.Binding(a).Help()
	return h.Key, h.Desc
}

// Paired actions are shown once in the footer ("j/k move").
var footerPairs = map[Action][3]string{
	ActDown:        {"j/k", "move", ""},
	ActStatusPrev:  {"</>", "move", ""},
	ActLeft:        {"h/l", "column", ""},
	ActNextComment: {"]/[", "comments", ""},
}

// footerHidden are actions that are enabled but not worth footer space.
var footerHidden = map[Action]bool{
	ActUp: true, ActRight: true, ActPrevComment: true, ActTop: true, ActBottom: true,
	ActHalfDown: true, ActHalfUp: true, ActViewIssues: true, ActViewPRs: true, ActViewBoard: true,
	ActNextView: true, ActPrevView: true, ActQuit: true, ActStatusNext: true,
}

// footerShort are the footer's short descriptions (UI mocks: "n new  e edit
// x close"); the help screen keeps the long ones.
var footerShort = map[Action]string{
	ActNew: "new", ActEdit: "edit", ActClose: "close", ActAssign: "assign", ActLabel: "label",
	ActComment: "comment", ActStatus: "status", ActDelete: "delete", ActCloseNotPlanned: "not planned",
}

// helpGroup is a column of the help screen (UI 3.13).
type helpGroup struct {
	Title   string
	Actions []Action
}

var helpGroups = []helpGroup{
	{"Navigate", []Action{ActDown, ActLeft, ActTop, ActHalfDown, ActNextComment, ActOpen, ActBack}},
	{"Issue actions", []Action{ActNew, ActQuickAdd, ActEdit, ActAssign, ActLabel, ActComment, ActClose,
		ActCloseNotPlanned, ActStatus, ActStatusPrev, ActDelete, ActChooseProject, ActHiddenInfo}},
	{"Views & app", []Action{ActViewIssues, ActNextView, ActRefresh, ActFilter, ActMine, ActState,
		ActBrowser, ActCopyURL, ActHelp, ActQuit}},
}

// helpPairs are the key labels for paired rows on the help screen.
var helpPairs = map[Action][2]string{
	ActDown:        {"j/k ↓/↑", "move"},
	ActLeft:        {"h/l ←/→", "column"},
	ActTop:         {"g/G", "top/bottom"},
	ActHalfDown:    {"ctrl+d/u", "half page"},
	ActNextComment: {"]/[", "comments"},
	ActStatusPrev:  {"< >", "move on board"},
	ActMine:        {"m", "only mine"},
	ActBrowser:     {"o", "open in browser"},
	ActViewIssues:  {"1 2 3", "views"},
}
